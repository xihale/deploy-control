package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deploycontrol/internal/analytics"
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

func TestSitesIncludesOptionalURL(t *testing.T) {
	srv, token := newTestServer(t)
	srv.Cfg.Sites = []config.Site{
		{
			Name:   "with-url",
			Repo:   "https://github.com/example/with.git",
			Branch: "main",
			URL:    "https://example.com",
			Script: "/bin/true",
		},
		{
			Name:   "without-url",
			Repo:   "https://github.com/example/without.git",
			Branch: "main",
			Script: "/bin/true",
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var sites []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &sites); err != nil {
		t.Fatalf("unmarshal error: %v, body=%s", err, rec.Body.String())
	}
	if len(sites) != 2 {
		t.Fatalf("expected 2 sites, got %d", len(sites))
	}
	if sites[0]["url"] != "https://example.com" {
		t.Fatalf("expected url 'https://example.com', got %v", sites[0]["url"])
	}
	if _, ok := sites[1]["url"]; ok {
		t.Fatalf("expected no url field for site without url, got %v", sites[1]["url"])
	}
}

func TestAnalyticsEndpointsDisabledAndEnabled(t *testing.T) {
	srv, token := newTestServer(t)

	// Disabled state returns enabled=false with non-null arrays.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/analytics/overview?range=7d", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled overview status %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"series":null`) || strings.Contains(rec.Body.String(), `"sites":null`) {
		t.Fatalf("arrays must not be null: %s", rec.Body.String())
	}

	// Enabled state with ingested Caddy log line.
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	now := time.Now().UTC()
	line := fmt.Sprintf(
		`{"ts":%d,"duration":0.015,"size":4096,"status":200,"request":{"remote_ip":"198.51.100.7","method":"GET","host":"blog.example.com","uri":"/hello","headers":{"User-Agent":["Mozilla/5.0 Chrome/130.0.0.0 Safari/537.36"],"Referer":["https://github.com/"]}},"resp_headers":{"Content-Type":["text/html"]}}`+"\n",
		now.Unix(),
	)
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgYAML := fmt.Sprintf(`
db_path: %s
users:
  - name: admin
    token_hash: %s
    role: admin
sites:
  - name: blog
    repo: https://github.com/example/blog
    branch: main
    url: https://blog.example.com
    script: /bin/true
analytics:
  enabled: true
  db_path: %s
  caddy_logs: [%q]
  timezone: UTC
`, filepath.Join(dir, "deploy.db"), srv.Cfg.Users[0].TokenHash, filepath.Join(dir, "analytics.db"), logPath)
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	ast, err := analytics.Open(cfg.AnalyticsDBFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ast.Close() })
	ing := analytics.NewIngestor(cfg, ast, nil)
	if err := ing.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.Cfg = cfg
	srv.Analytics = ast

	for _, ep := range []string{
		"/api/analytics/overview?range=7d",
		"/api/analytics/sites/blog?range=7d",
		"/api/analytics/dimensions?site=blog&dim=path&range=7d",
		"/api/analytics/export?type=overview&range=7d",
		"/api/analytics/export?type=daily&site=blog&range=7d",
		"/api/analytics/export?type=dimension&site=blog&dim=path&range=7d",
	} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, ep, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", ep, rec.Code, rec.Body.String())
		}
	}
}

func TestAnalyticsBansRBACAndLifecycle(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	banFile := filepath.Join(dir, "bans.caddy")
	adminToken := "admin-secret"
	viewerToken := "viewer-secret"
	adminHash := fmt.Sprintf("%x", sha256.Sum256([]byte(adminToken)))
	viewerHash := fmt.Sprintf("%x", sha256.Sum256([]byte(viewerToken)))

	cfgYAML := fmt.Sprintf(`
db_path: %s
users:
  - name: admin
    token_hash: %s
    role: admin
  - name: viewer
    token_hash: %s
    role: viewer
sites:
  - name: blog
    repo: https://github.com/example/blog
    branch: main
    url: https://blog.example.com
    script: /bin/true
analytics:
  enabled: true
  db_path: %s
  caddy_logs: [%q]
  timezone: UTC
  shield:
    enabled: true
    ban_ttl_hours: 168
    caddy_ban_file: %q
`, filepath.Join(dir, "deploy.db"), adminHash, viewerHash, filepath.Join(dir, "analytics.db"), logPath, banFile)
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ast, err := analytics.Open(cfg.AnalyticsDBFile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ast.Close() })
	shield := analytics.NewShieldManager(cfg, ast, nil)
	srv := &Server{Cfg: cfg, Store: st, Analytics: ast, Shield: shield, LogDir: t.TempDir()}

	// Viewer cannot POST or DELETE bans.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/analytics/bans", strings.NewReader(`{"ip":"203.0.113.44","reason":"test"}`))
	req.Header.Set("Authorization", "Bearer "+viewerToken)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer POST, got %d", rec.Code)
	}

	// Admin bans 203.0.113.44.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/analytics/bans", strings.NewReader(`{"ip":"203.0.113.44","reason":"scanner","ttl_hours":168}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for admin POST, got %d: %s", rec.Code, rec.Body.String())
	}

	// Protected IP is rejected.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/analytics/bans", strings.NewReader(`{"ip":"127.0.0.1","reason":"oops"}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for loopback IP, got %d", rec.Code)
	}

	// Viewer can GET bans list.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/analytics/bans?q=203.0.113.44", nil)
	req.Header.Set("Authorization", "Bearer "+viewerToken)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "203.0.113.44") {
		t.Fatalf("expected 200 with IP in list, got %d: %s", rec.Code, rec.Body.String())
	}

	// Admin deletes ban.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/analytics/bans?ip=203.0.113.44", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"removed":true`) {
		t.Fatalf("expected unban ok, got %d: %s", rec.Code, rec.Body.String())
	}

	// Admin can manually ban and unban a /24 CIDR.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/analytics/bans", strings.NewReader(`{"ip":"203.0.116.0/24","reason":"botnet"}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "203.0.116.0/24") {
		t.Fatalf("expected 201 for CIDR ban, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/analytics/bans?ip=203.0.116.0/24", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"removed":true`) {
		t.Fatalf("expected CIDR unban ok, got %d: %s", rec.Code, rec.Body.String())
	}
}
