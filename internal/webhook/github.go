// Package webhook implements GitHub push webhooks with HMAC verification,
// ref/SHA validation and delivery deduplication.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"deploycontrol/internal/config"
	"deploycontrol/internal/dispatch"
	"deploycontrol/internal/store"
)

// maxBodySize caps webhook payloads (GitHub recommends < 25 MiB).
const maxBodySize = 25 << 20

// pushPayload is the subset of the GitHub push event we consume.
type pushPayload struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	HeadCommit *struct {
		ID string `json:"id"`
	} `json:"head_commit"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

type releasePayload struct {
	Action  string `json:"action"`
	Release struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	} `json:"release"`
}

// Handler serves POST /webhook/github/{site}.
type Handler struct {
	Cfg   *config.Config
	Store *store.Store
	Disp  *dispatch.Dispatcher
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	siteName := strings.TrimPrefix(r.URL.Path, "/webhook/github/")
	site, ok := h.Cfg.SiteByName(siteName)
	if !ok {
		http.Error(w, "unknown site", http.StatusNotFound)
		return
	}
	if site.WebhookSecret == "" {
		http.Error(w, "site has no webhook secret configured", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	if !verifyHMAC(site.WebhookSecret, sig, body) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	if !site.AllowsEvent(event) {
		// Ping and unconfigured events are acknowledged but not queued.
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"queued":false,"reason":%q}`, fmt.Sprintf("event %q ignored", event))
		return
	}

	var (
		triggerType string
		branch      string
		sha         string
		targetLabel string
		reason      string
	)
	switch event {
	case "push":
		var payload pushPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "bad payload: "+err.Error(), http.StatusBadRequest)
			return
		}
		triggerType = store.TriggerPush
		branch, sha, ok, reason = matchPushTarget(site, payload)
		targetLabel = payload.Ref
	case "release":
		var payload releasePayload
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "bad payload: "+err.Error(), http.StatusBadRequest)
			return
		}
		triggerType = store.TriggerRelease
		branch, ok, reason = matchReleaseTarget(site, payload)
		targetLabel = "release:" + payload.Release.TagName + ":" + payload.Action
	default:
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"queued":false,"reason":%q}`, fmt.Sprintf("event %q ignored", event))
		return
	}
	if !ok {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"queued":false,"reason":%q}`, reason)
		return
	}

	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if deliveryID == "" {
		http.Error(w, "missing X-GitHub-Delivery", http.StatusBadRequest)
		return
	}

	var (
		job   store.Job
		isNew bool
	)
	if triggerType == store.TriggerRelease {
		job, isNew, err = h.Store.EnqueueRelease(site.Name, deliveryID, branch, sha)
	} else {
		job, isNew, err = h.Store.EnqueuePush(site.Name, deliveryID, branch, sha)
	}
	if err != nil {
		log.Printf("webhook: enqueue site=%s: %v", site.Name, err)
		http.Error(w, "enqueue failed", http.StatusInternalServerError)
		return
	}
	if !isNew {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"queued":false,"reason":"duplicate delivery","job_id":%d}`, job.ID)
		return
	}
	h.Disp.Wake()
	log.Printf("webhook: queued job %d site=%s event=%s target=%s sha=%s delivery=%s", job.ID, site.Name, event, targetLabel, sha, deliveryID)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"queued":true,"job_id":%d}`, job.ID)
}

func matchPushTarget(site config.Site, payload pushPayload) (branch, sha string, ok bool, reason string) {
	re, err := site.RefPattern()
	if err != nil {
		return "", "", false, fmt.Sprintf("invalid ref_regex: %v", err)
	}
	if re != nil {
		if !re.MatchString(payload.Ref) {
			return "", "", false, fmt.Sprintf("ref %q does not match %q", payload.Ref, site.RefRegex)
		}
		branch = refLabel(payload.Ref)
		if !config.ValidBranch(branch) {
			return "", "", false, fmt.Sprintf("invalid ref label %q", branch)
		}
	} else {
		wantRef := "refs/heads/" + site.Branch
		if payload.Ref != wantRef {
			return "", "", false, fmt.Sprintf("ref %q != %q", payload.Ref, wantRef)
		}
		branch = site.Branch
	}
	// Branch or tag deletion pushes have after=0000...0: nothing to deploy.
	if !config.ValidSHA(payload.After) || isZeroSHA(payload.After) {
		return "", "", false, "deleted or invalid after sha"
	}
	// Annotated tag pushes carry the tag object SHA in `after` and the peeled
	// commit SHA in `head_commit.id`; prefer head_commit.id when valid.
	sha = payload.After
	if payload.HeadCommit != nil && config.ValidSHA(payload.HeadCommit.ID) {
		sha = payload.HeadCommit.ID
	}
	return branch, sha, true, ""
}

func matchReleaseTarget(site config.Site, payload releasePayload) (branch string, ok bool, reason string) {
	switch payload.Action {
	case "published", "released", "prereleased", "edited", "deleted":
	default:
		return "", false, fmt.Sprintf("release action %q ignored", payload.Action)
	}
	if payload.Release.Draft && payload.Action != "deleted" {
		return "", false, "draft release ignored"
	}
	if re, err := site.RefPattern(); err != nil {
		return "", false, fmt.Sprintf("invalid ref_regex: %v", err)
	} else if re != nil {
		tagRef := "refs/tags/" + payload.Release.TagName
		if !re.MatchString(tagRef) {
			return "", false, fmt.Sprintf("release tag %q does not match %q", tagRef, site.RefRegex)
		}
	}
	return site.Branch, true, ""
}

func refLabel(ref string) string {
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		return strings.TrimPrefix(ref, "refs/heads/")
	case strings.HasPrefix(ref, "refs/tags/"):
		return strings.TrimPrefix(ref, "refs/tags/")
	default:
		return ref
	}
}

// verifyHMAC checks X-Hub-Signature-256 ("sha256=<hex>") in constant time.
func verifyHMAC(secret, sig string, body []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(sig, prefix) {
		return false
	}
	given, err := hex.DecodeString(strings.TrimPrefix(sig, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), given)
}

func isZeroSHA(s string) bool {
	return strings.Trim(s, "0") == ""
}
