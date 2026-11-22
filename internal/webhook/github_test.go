package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"deploycontrol/internal/config"
	"deploycontrol/internal/dispatch"
	"deploycontrol/internal/store"
)

func signPayload(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newTestWebhookHandler(t *testing.T, sites ...config.Site) (*Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := &config.Config{Sites: sites}
	disp := dispatch.New(cfg, st, nil)
	return &Handler{Cfg: cfg, Store: st, Disp: disp}, st
}

func postWebhook(t *testing.T, h *Handler, site, secret, event, delivery string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/github/"+site, bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, body))
	h.ServeHTTP(rec, req)
	return rec
}

func TestWebhookRefRegexTagMatching(t *testing.T) {
	const secret = "test-secret"
	commitSHA := strings.Repeat("a", 40)
	tagObjSHA := strings.Repeat("b", 40)
	zeroSHA := strings.Repeat("0", 40)

	h, st := newTestWebhookHandler(t, config.Site{
		Name:          "upix-web",
		Repo:          "https://github.com/xihale/upix",
		Branch:        "main",
		RefRegex:      `^refs/tags/web-v[0-9]+\.[0-9]+\.[0-9]+$`,
		Script:        "/var/lib/deployd/scripts/run-site",
		WebhookSecret: secret,
	})

	// Branch push is ignored when ref_regex requires stable web-vX.Y.Z tags.
	body, _ := json.Marshal(map[string]any{
		"ref":   "refs/heads/main",
		"after": commitSHA,
	})
	rec := postWebhook(t, h, "upix-web", secret, "push", "del-branch", body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queued":false`) {
		t.Fatalf("branch push = %d %s, want 200 queued=false", rec.Code, rec.Body.String())
	}

	// Prerelease tag is ignored.
	body, _ = json.Marshal(map[string]any{
		"ref":   "refs/tags/web-v0.2.0-beta.1",
		"after": commitSHA,
	})
	rec = postWebhook(t, h, "upix-web", secret, "push", "del-beta", body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queued":false`) {
		t.Fatalf("beta tag = %d %s, want 200 queued=false", rec.Code, rec.Body.String())
	}

	// Deleted tag is ignored.
	body, _ = json.Marshal(map[string]any{
		"ref":   "refs/tags/web-v0.2.0",
		"after": zeroSHA,
	})
	rec = postWebhook(t, h, "upix-web", secret, "push", "del-delete", body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queued":false`) {
		t.Fatalf("deleted tag = %d %s, want 200 queued=false", rec.Code, rec.Body.String())
	}

	// Stable annotated tag queues with tag label as branch and head_commit.id as sha.
	body, _ = json.Marshal(map[string]any{
		"ref":         "refs/tags/web-v0.2.0",
		"after":       tagObjSHA,
		"head_commit": map[string]any{"id": commitSHA},
	})
	rec = postWebhook(t, h, "upix-web", secret, "push", "del-release", body)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"queued":true`) {
		t.Fatalf("stable tag = %d %s, want 202 queued=true", rec.Code, rec.Body.String())
	}

	jobs, total, err := st.ListJobs("upix-web", "", 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("ListJobs = %d %v", total, err)
	}
	if jobs[0].Branch != "web-v0.2.0" || jobs[0].SHA != commitSHA {
		t.Fatalf("queued job = %+v, want branch=web-v0.2.0 sha=%s", jobs[0], commitSHA)
	}

	// Duplicate delivery does not enqueue again.
	rec = postWebhook(t, h, "upix-web", secret, "push", "del-release", body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"duplicate delivery"`) {
		t.Fatalf("duplicate = %d %s, want 200 duplicate delivery", rec.Code, rec.Body.String())
	}
}

func TestWebhookReleaseEventHandling(t *testing.T) {
	const secret = "test-secret"
	h, st := newTestWebhookHandler(t,
		config.Site{
			Name:          "push-only",
			Repo:          "https://github.com/xihale/example",
			Branch:        "main",
			Script:        "/var/lib/deployd/scripts/run-site",
			WebhookSecret: secret,
		},
		config.Site{
			Name:          "goke",
			Repo:          "https://github.com/xihale/goke",
			Branch:        "main",
			Events:        []string{"push", "release"},
			Script:        "/var/lib/deployd/scripts/run-site",
			WebhookSecret: secret,
		},
	)

	publishedBody, _ := json.Marshal(map[string]any{
		"action": "published",
		"release": map[string]any{
			"tag_name": "v0.0.1-beta.9",
			"draft":    false,
		},
	})

	// Site without release enabled ignores release webhook.
	rec := postWebhook(t, h, "push-only", secret, "release", "rel-1", publishedBody)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queued":false`) {
		t.Fatalf("push-only release = %d %s, want 200 queued=false", rec.Code, rec.Body.String())
	}

	// Draft or created release actions are ignored.
	createdBody, _ := json.Marshal(map[string]any{
		"action": "created",
		"release": map[string]any{
			"tag_name": "v0.0.1-beta.9",
			"draft":    false,
		},
	})
	rec = postWebhook(t, h, "goke", secret, "release", "rel-2", createdBody)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queued":false`) {
		t.Fatalf("created release = %d %s, want 200 queued=false", rec.Code, rec.Body.String())
	}

	draftPublished, _ := json.Marshal(map[string]any{
		"action": "published",
		"release": map[string]any{
			"tag_name": "v0.0.1-beta.9",
			"draft":    true,
		},
	})
	rec = postWebhook(t, h, "goke", secret, "release", "rel-3", draftPublished)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queued":false`) {
		t.Fatalf("draft release = %d %s, want 200 queued=false", rec.Code, rec.Body.String())
	}

	// Published release queues a release job on site.Branch with empty sha.
	rec = postWebhook(t, h, "goke", secret, "release", "rel-4", publishedBody)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"queued":true`) {
		t.Fatalf("published release = %d %s, want 202 queued=true", rec.Code, rec.Body.String())
	}

	jobs, total, err := st.ListJobs("goke", "", 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("ListJobs = %d %v", total, err)
	}
	if jobs[0].TriggerType != store.TriggerRelease || jobs[0].Branch != "main" || jobs[0].SHA != "" {
		t.Fatalf("queued release job = %+v, want trigger=release branch=main sha=''", jobs[0])
	}

	// Duplicate release delivery is ignored cleanly.
	rec = postWebhook(t, h, "goke", secret, "release", "rel-4", publishedBody)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"duplicate delivery"`) {
		t.Fatalf("duplicate release = %d %s, want 200 duplicate delivery", rec.Code, rec.Body.String())
	}
}
