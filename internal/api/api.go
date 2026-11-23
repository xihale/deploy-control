// Package api implements the authenticated HTTP API.
package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"deploycontrol/internal/analytics"
	"deploycontrol/internal/config"
	"deploycontrol/internal/dispatch"
	"deploycontrol/internal/store"
)

// Server serves the REST API.
type Server struct {
	Cfg       *config.Config
	Store     *store.Store
	Disp      *dispatch.Dispatcher
	Analytics *analytics.Store
	Shield    *analytics.ShieldManager
	LogDir    string
}

type identity struct {
	Name string
	Role config.Role
}

// auth resolves the bearer token to a user in constant time per candidate.
func (s *Server) auth(r *http.Request) (identity, bool) {
	h := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || token == "" {
		return identity{}, false
	}
	sum := sha256.Sum256([]byte(token))
	given := fmt.Sprintf("%x", sum)
	for _, u := range s.Cfg.Users {
		if subtle.ConstantTimeCompare([]byte(given), []byte(u.TokenHash)) == 1 {
			return identity{Name: u.Name, Role: u.Role}, true
		}
	}
	return identity{}, false
}

func (s *Server) requireRole(w http.ResponseWriter, r *http.Request, role config.Role) (identity, bool) {
	id, ok := s.auth(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="deployd"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return identity{}, false
	}
	if role == config.RoleAdmin && id.Role != config.RoleAdmin {
		http.Error(w, "forbidden: admin required", http.StatusForbidden)
		return identity{}, false
	}
	return id, true
}

// ServeHTTP routes /api/* and /healthz.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch path {
	case "/api/deploys":
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	case "/api/analytics/bans":
		if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	default:
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	}
	switch {
	case path == "/healthz":
		s.handleHealth(w, r)
	case path == "/api/sites":
		s.handleSites(w, r)
	case path == "/api/me":
		s.handleMe(w, r)
	case path == "/api/deploys":
		if r.Method == http.MethodPost {
			s.handleManualDeploy(w, r)
		} else {
			s.handleListDeploys(w, r)
		}
	case strings.HasPrefix(path, "/api/deploys/"):
		rest := strings.TrimPrefix(path, "/api/deploys/")
		if id, err := strconv.ParseInt(rest, 10, 64); err == nil {
			s.handleJobDetail(w, r, id)
			return
		}
		if strings.HasSuffix(rest, "/log") {
			idStr := strings.TrimSuffix(rest, "/log")
			if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
				s.handleJobLog(w, r, id)
				return
			}
		}
		http.Error(w, "not found", http.StatusNotFound)
	case path == "/api/analytics/overview":
		s.handleAnalyticsOverview(w, r)
	case path == "/api/analytics/dimensions":
		s.handleAnalyticsDimensions(w, r)
	case path == "/api/analytics/export":
		s.handleAnalyticsExport(w, r)
	case path == "/api/analytics/bans":
		s.handleAnalyticsBans(w, r)
	case strings.HasPrefix(path, "/api/analytics/sites/"):
		siteName := strings.TrimPrefix(path, "/api/analytics/sites/")
		if siteName == "" || strings.Contains(siteName, "/") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		s.handleAnalyticsSite(w, r, siteName)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	queued, running, err := s.Store.QueueStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"queued":  queued,
		"running": running,
	})
}

// handleMe reports the authenticated identity so the UI can gate admin-only
// actions instead of discovering the role from a 403.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireRole(w, r, config.RoleViewer)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": id.Name, "role": string(id.Role)})
}

func (s *Server) handleSites(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireRole(w, r, config.RoleViewer); !ok {
		return
	}
	type siteView struct {
		Name      string   `json:"name"`
		Repo      string   `json:"repo"`
		Branch    string   `json:"branch"`
		URL       string   `json:"url,omitempty"`
		Hosts     []string `json:"hosts,omitempty"`
		RefRegex  string   `json:"ref_regex,omitempty"`
		Events    []string `json:"events,omitempty"`
		Script    string   `json:"script"`
		HasSecret bool     `json:"has_webhook_secret"`
		RunAs     string   `json:"run_as,omitempty"`
	}
	out := make([]siteView, 0, len(s.Cfg.Sites))
	for _, site := range s.Cfg.Sites {
		out = append(out, siteView{
			Name:      site.Name,
			Repo:      site.Repo,
			Branch:    site.Branch,
			URL:       site.URL,
			Hosts:     site.AnalyticsHosts(),
			RefRegex:  site.RefRegex,
			Events:    site.Events,
			Script:    site.Script,
			HasSecret: site.WebhookSecret != "",
			RunAs:     site.RunAs,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListDeploys(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireRole(w, r, config.RoleViewer); !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	jobs, total, err := s.Store.ListJobs(q.Get("site"), q.Get("status"), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "jobs": jobs})
}

func (s *Server) handleJobDetail(w http.ResponseWriter, r *http.Request, id int64) {
	if _, ok := s.requireRole(w, r, config.RoleViewer); !ok {
		return
	}
	job, err := s.Store.GetJob(id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleJobLog streams the tail of a job log. `bytes` limits the tail size
// (default 16384, max 1 MiB). Path traversal is impossible: the path is
// constructed from the numeric job id and must live under LogDir.
func (s *Server) handleJobLog(w http.ResponseWriter, r *http.Request, id int64) {
	if _, ok := s.requireRole(w, r, config.RoleViewer); !ok {
		return
	}
	job, err := s.Store.GetJob(id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if job.LogPath == "" {
		http.Error(w, "no log yet", http.StatusNotFound)
		return
	}
	logPath := filepath.Join(s.LogDir, fmt.Sprintf("job-%d.log", job.ID))
	if !strings.HasPrefix(logPath, s.LogDir+string(os.PathSeparator)) {
		http.Error(w, "invalid log path", http.StatusBadRequest)
		return
	}
	file, err := os.Open(logPath)
	if err != nil {
		http.Error(w, "log unavailable", http.StatusNotFound)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		http.Error(w, "log unavailable", http.StatusInternalServerError)
		return
	}
	max := 16384
	if b := r.URL.Query().Get("bytes"); b != "" {
		if n, err := strconv.Atoi(b); err == nil && n > 0 && n <= 1<<20 {
			max = n
		}
	}
	if stat.Size() > int64(max) {
		if _, err := file.Seek(stat.Size()-int64(max), io.SeekStart); err != nil {
			http.Error(w, "log unavailable", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.CopyN(w, file, int64(max))
}

// handleManualDeploy queues a manual deployment: POST /api/deploys
// {"site":"name","branch":"optional","sha":"optional-40/64-hex"}.
func (s *Server) handleManualDeploy(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireRole(w, r, config.RoleAdmin); !ok {
		return
	}
	var req struct {
		Site   string `json:"site"`
		Branch string `json:"branch"`
		SHA    string `json:"sha"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	site, ok := s.Cfg.SiteByName(strings.TrimSpace(req.Site))
	if !ok {
		http.Error(w, "unknown site", http.StatusNotFound)
		return
	}
	branch := strings.TrimSpace(req.Branch)
	sha := strings.TrimSpace(req.SHA)
	if branch != "" && !config.ValidBranch(branch) {
		http.Error(w, "invalid branch", http.StatusBadRequest)
		return
	}
	if sha != "" && !config.ValidSHA(sha) {
		http.Error(w, "invalid sha (40 or 64 hex required)", http.StatusBadRequest)
		return
	}
	job, err := s.Store.EnqueueManual(site.Name, branch, sha)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.Disp.Wake()
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) analyticsEnabled() bool {
	return s.Cfg != nil && s.Cfg.Analytics.Enabled && s.Analytics != nil
}

func (s *Server) resolveAnalyticsRange(r *http.Request) (analytics.DateRange, bool) {
	q := r.URL.Query()
	loc := s.Cfg.AnalyticsLocation()
	dr, err := analytics.ResolveDateRange(q.Get("range"), q.Get("from"), q.Get("to"), loc, time.Now())
	if err != nil {
		return analytics.DateRange{}, false
	}
	return dr, true
}

func (s *Server) handleAnalyticsOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireRole(w, r, config.RoleViewer); !ok {
		return
	}
	q := r.URL.Query()
	dr, err := analytics.ResolveDateRange(q.Get("range"), q.Get("from"), q.Get("to"), s.Cfg.AnalyticsLocation(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.analyticsEnabled() {
		writeJSON(w, http.StatusOK, analytics.OverviewResponse{
			Enabled:  false,
			Timezone: s.Cfg.Analytics.Timezone,
			Range:    dr,
			Series:   []analytics.DailyPoint{},
			Sites:    []analytics.SiteRow{},
		})
		return
	}
	rep, err := s.Analytics.QueryOverview(s.Cfg, dr, time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleAnalyticsSite(w http.ResponseWriter, r *http.Request, siteName string) {
	if _, ok := s.requireRole(w, r, config.RoleViewer); !ok {
		return
	}
	site, ok := s.Cfg.SiteByName(siteName)
	if !ok {
		http.Error(w, "unknown site", http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	dr, err := analytics.ResolveDateRange(q.Get("range"), q.Get("from"), q.Get("to"), s.Cfg.AnalyticsLocation(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.analyticsEnabled() {
		hosts := site.AnalyticsHosts()
		if hosts == nil {
			hosts = []string{}
		}
		writeJSON(w, http.StatusOK, analytics.SiteReportResponse{
			Enabled:  false,
			Timezone: s.Cfg.Analytics.Timezone,
			Site:     site.Name,
			URL:      site.URL,
			Hosts:    hosts,
			Range:    dr,
			Daily:    []analytics.DailyPoint{},
			Hourly:   []analytics.HourlyPoint{},
		})
		return
	}
	rep, err := s.Analytics.QuerySiteReport(s.Cfg, site, dr, time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleAnalyticsDimensions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireRole(w, r, config.RoleViewer); !ok {
		return
	}
	q := r.URL.Query()
	siteName := strings.TrimSpace(q.Get("site"))
	if siteName == "" {
		siteName = "_all"
	}
	if siteName != "_all" {
		if _, ok := s.Cfg.SiteByName(siteName); !ok {
			http.Error(w, "unknown site", http.StatusNotFound)
			return
		}
	}
	dim := strings.TrimSpace(q.Get("dim"))
	if dim == "" {
		dim = "path"
	}
	if !analytics.ValidDimension(dim) {
		http.Error(w, "unsupported dim", http.StatusBadRequest)
		return
	}
	dr, err := analytics.ResolveDateRange(q.Get("range"), q.Get("from"), q.Get("to"), s.Cfg.AnalyticsLocation(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.analyticsEnabled() {
		writeJSON(w, http.StatusOK, analytics.DimensionResponse{
			Enabled: false,
			Site:    siteName,
			Dim:     dim,
			Range:   dr,
			Rows:    []analytics.DimensionRow{},
		})
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	rep, err := s.Analytics.QueryDimension(siteName, dim, dr, q.Get("q"), q.Get("sort"), q.Get("order"), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleAnalyticsExport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireRole(w, r, config.RoleViewer); !ok {
		return
	}
	if !s.analyticsEnabled() {
		http.Error(w, "analytics disabled", http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	dr, err := analytics.ResolveDateRange(q.Get("range"), q.Get("from"), q.Get("to"), s.Cfg.AnalyticsLocation(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	kind := strings.TrimSpace(q.Get("type"))
	if kind == "" {
		kind = "overview"
	}
	siteName := strings.TrimSpace(q.Get("site"))
	if siteName == "" {
		siteName = "_all"
	}
	if siteName != "_all" {
		if _, ok := s.Cfg.SiteByName(siteName); !ok {
			http.Error(w, "unknown site", http.StatusNotFound)
			return
		}
	}

	safeLabel := func(v string) string {
		var b strings.Builder
		for _, r := range v {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				b.WriteRune(r)
			}
		}
		if b.Len() == 0 {
			return "all"
		}
		return b.String()
	}

	switch kind {
	case "overview":
		rep, err := s.Analytics.QueryOverview(s.Cfg, dr, time.Now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="analytics-overview-%s-to-%s.csv"`, dr.From, dr.To))
		_ = analytics.WriteOverviewCSV(w, rep)
	case "daily":
		var rows []analytics.DailyPoint
		if siteName == "_all" {
			rep, err := s.Analytics.QueryOverview(s.Cfg, dr, time.Now())
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			rows = rep.Series
		} else {
			site, _ := s.Cfg.SiteByName(siteName)
			rep, err := s.Analytics.QuerySiteReport(s.Cfg, site, dr, time.Now())
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			rows = rep.Daily
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="analytics-daily-%s-%s-to-%s.csv"`, safeLabel(siteName), dr.From, dr.To))
		_ = analytics.WriteDailyCSV(w, rows)
	case "dimension":
		dim := strings.TrimSpace(q.Get("dim"))
		if !analytics.ValidDimension(dim) {
			http.Error(w, "unsupported dim", http.StatusBadRequest)
			return
		}
		rep, err := s.Analytics.QueryDimension(siteName, dim, dr, q.Get("q"), q.Get("sort"), q.Get("order"), 500, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="analytics-%s-%s-%s-to-%s.csv"`, safeLabel(siteName), dim, dr.From, dr.To))
		_ = analytics.WriteDimensionCSV(w, rep)
	default:
		http.Error(w, "unsupported export type", http.StatusBadRequest)
	}
}

func (s *Server) handleAnalyticsBans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if _, ok := s.requireRole(w, r, config.RoleViewer); !ok {
			return
		}
		if !s.analyticsEnabled() {
			writeJSON(w, http.StatusOK, analytics.BansResponse{
				Enabled:       false,
				ShieldEnabled: false,
				DefaultTTLHrs: s.Cfg.Analytics.Shield.BanTTLHours,
				Rows:          []analytics.BanRecord{},
			})
			return
		}
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		rep, err := s.Analytics.ListBansPage(time.Now(), q.Get("q"), limit, offset)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		rep.ShieldEnabled = s.Cfg.Analytics.Shield.Enabled
		rep.DefaultTTLHrs = s.Cfg.Analytics.Shield.BanTTLHours
		writeJSON(w, http.StatusOK, rep)

	case http.MethodPost:
		if _, ok := s.requireRole(w, r, config.RoleAdmin); !ok {
			return
		}
		if !s.analyticsEnabled() || s.Shield == nil || !s.Cfg.Analytics.Shield.Enabled {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "shield is disabled"})
			return
		}
		var req struct {
			IP       string `json:"ip"`
			Reason   string `json:"reason"`
			TTLHours int    `json:"ttl_hours"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		rec, err := s.Shield.BanManual(r.Context(), req.IP, req.Reason, req.TTLHours)
		if err != nil {
			status := http.StatusBadRequest
			if !errors.Is(err, analytics.ErrProtectedIP) && !strings.Contains(err.Error(), "invalid ip") {
				status = http.StatusInternalServerError
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, rec)

	case http.MethodDelete:
		if _, ok := s.requireRole(w, r, config.RoleAdmin); !ok {
			return
		}
		if !s.analyticsEnabled() || s.Shield == nil || !s.Cfg.Analytics.Shield.Enabled {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "shield is disabled"})
			return
		}
		ip := strings.TrimSpace(r.URL.Query().Get("ip"))
		if ip == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ip is required"})
			return
		}
		removed, err := s.Shield.Unban(r.Context(), ip)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "invalid ip") {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": removed})
	}
}
