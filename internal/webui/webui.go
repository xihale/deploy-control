// Package webui serves the statically exported frontend (Next.js
// `output: export`) from the same origin as the API, so the browser needs no
// CORS configuration and the bearer token never crosses origins.
package webui

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Handler serves files from root. Directory URLs fall back to index.html
// (Next.js writes /site/index.html for /site), and unknown paths return the
// exported 404.html.
type Handler struct {
	root string
}

// New returns a handler rooted at root, which must contain index.html.
func New(root string) (*Handler, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("web root %s: %w", root, err)
	}
	info, err := os.Stat(filepath.Join(abs, "index.html"))
	if err != nil {
		return nil, fmt.Errorf("web root %s: %w", abs, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("web root %s: index.html is a directory", abs)
	}
	return &Handler{root: abs}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.ContainsRune(r.URL.Path, 0) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	upath := path.Clean("/" + r.URL.Path)

	file, info, ok := h.lookup(upath)
	if !ok {
		h.serve404(w, r)
		return
	}
	defer file.Close()

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", cacheControl(upath, info.Name()))
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

// lookup resolves a cleaned URL path to a regular file inside root.
func (h *Handler) lookup(upath string) (*os.File, os.FileInfo, bool) {
	rel := strings.TrimPrefix(upath, "/")
	if rel == "" {
		return h.open(filepath.Join(h.root, "index.html"))
	}
	for _, candidate := range []string{
		filepath.Join(h.root, filepath.FromSlash(rel)),
		filepath.Join(h.root, filepath.FromSlash(rel), "index.html"),
	} {
		if !strings.HasPrefix(candidate, h.root+string(os.PathSeparator)) {
			continue
		}
		if file, info, ok := h.open(candidate); ok {
			return file, info, true
		}
	}
	return nil, nil, false
}

func (h *Handler) open(candidate string) (*os.File, os.FileInfo, bool) {
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, false
	}
	file, err := os.Open(candidate)
	if err != nil {
		return nil, nil, false
	}
	return file, info, true
}

func (h *Handler) serve404(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(filepath.Join(h.root, "404.html"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

// cacheControl fingerprints content-hashed build output and keeps HTML fresh.
func cacheControl(upath, name string) string {
	switch {
	case strings.HasPrefix(upath, "/_next/static/"):
		return "public, max-age=31536000, immutable"
	case strings.HasSuffix(name, ".html"):
		return "no-cache"
	default:
		return "public, max-age=3600"
	}
}
