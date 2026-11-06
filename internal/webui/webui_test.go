package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"index.html":            "<html>home</html>",
		"site/index.html":       "<html>site</html>",
		"404.html":              "<html>missing</html>",
		"_next/static/app.js":   "console.log(1)",
		"favicon.svg":           "<svg/>",
		"apple-icon.png":        "png",
		"favicon.ico":           "ico",
		"empty-dir/placeholder": "",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func serve(h *Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestServesIndexAndDirectoryPages(t *testing.T) {
	h := newTestHandler(t)
	for _, tc := range []struct{ target, body string }{
		{"/", "home"},
		{"/site", "site"},
		{"/site/", "site"},
	} {
		rec := serve(h, http.MethodGet, tc.target)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.body) {
			t.Fatalf("%s: status %d body %q", tc.target, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s: cache-control %q, want no-cache for HTML", tc.target, got)
		}
	}
}

func TestHashedAssetsAreImmutable(t *testing.T) {
	rec := serve(newTestHandler(t), http.MethodGet, "/_next/static/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("cache-control %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("content-type %q", ct)
	}
}

func TestUnknownPathsReturnExported404(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/missing", "/empty-dir", "/../etc/passwd", "/%2e%2e/%2e%2e/etc/passwd"} {
		rec := serve(h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", target, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "missing") {
			t.Fatalf("%s: expected exported 404 body, got %q", target, rec.Body.String())
		}
	}
}

func TestRejectsNonReadMethods(t *testing.T) {
	rec := serve(newTestHandler(t), http.MethodPost, "/")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("allow %q", allow)
	}
}

func TestNewRequiresIndex(t *testing.T) {
	if _, err := New(t.TempDir()); err == nil {
		t.Fatal("New must reject a directory without index.html")
	}
}

func TestIconsAreRevalidated(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/favicon.svg", "/apple-icon.png", "/favicon.ico"} {
		rec := serve(h, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", target, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s: cache-control %q, want no-cache", target, got)
		}
	}
}
