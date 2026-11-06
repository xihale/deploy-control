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
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
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

	if event := r.Header.Get("X-GitHub-Event"); event != "push" {
		// Ping and other events are acknowledged but not queued.
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"queued":false,"reason":%q}`, fmt.Sprintf("event %q ignored", event))
		return
	}

	var payload pushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	wantRef := "refs/heads/" + site.Branch
	if payload.Ref != wantRef {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"queued":false,"reason":%q}`, fmt.Sprintf("ref %q != %q", payload.Ref, wantRef))
		return
	}
	// Branch deletion pushes have after=0000...0: nothing to deploy.
	if !config.ValidSHA(payload.After) || isZeroSHA(payload.After) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"queued":false,"reason":"deleted or invalid after sha"}`)
		return
	}

	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if deliveryID == "" {
		http.Error(w, "missing X-GitHub-Delivery", http.StatusBadRequest)
		return
	}

	job, isNew, err := h.Store.EnqueuePush(site.Name, deliveryID, site.Branch, payload.After)
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
	log.Printf("webhook: queued job %d site=%s sha=%s delivery=%s", job.ID, site.Name, payload.After, deliveryID)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"queued":true,"job_id":%d}`, job.ID)
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
