package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"deploycontrol/internal/config"
	"deploycontrol/internal/store"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	token := "test-token"
	sum := sha256.Sum256([]byte(token))
	cfg := &config.Config{
		Users: []config.User{{Name: "admin", TokenHash: fmt.Sprintf("%x", sum), Role: config.RoleAdmin}},
	}
	return &Server{Cfg: cfg, Store: st, LogDir: t.TempDir()}, token
}

func TestListDeploysReturnsArrayNotNull(t *testing.T) {
	srv, token := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/deploys?site=missing&status=queued", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Total int              `json:"total"`
		Jobs  []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	// json.Unmarshal 不能区分 null 与缺失，这里直接检查原始字节。
	if strings.Contains(rec.Body.String(), `"jobs":null`) {
		t.Fatalf("jobs must serialize as [], got %q", rec.Body.String())
	}
	if out.Total != 0 || len(out.Jobs) != 0 {
		t.Fatalf("expected empty result, got total=%d jobs=%v", out.Total, out.Jobs)
	}
}

func TestSitesReturnsArrayNotNull(t *testing.T) {
	srv, token := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.TrimSpace(body) != "[]" {
		t.Fatalf("sites without config rows must serialize as [], got %q", body)
	}
}
