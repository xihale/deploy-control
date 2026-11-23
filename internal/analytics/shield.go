package analytics

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"deploycontrol/internal/config"
)

var defaultHoneypotPrefixes = []string{
	"/wp-admin",
	"/wp-login",
	"/wp-content",
	"/wp-includes",
	"/xmlrpc.php",
	"/.env",
	"/.git",
	"/.svn",
	"/.hg",
	"/.ds_store",
	"/cgi-bin/",
	"/phpmyadmin",
	"/pma",
	"/actuator",
}

var extraScannerTokens = []string{
	"dirbuster",
	"gobuster",
	"feroxbuster",
	"wpscan",
	"acunetix",
	"nessus",
	"openvas",
	"fhscan",
	"zmeu",
	"morfeus",
	"python-httpx",
}

func (m *Matcher) detectThreat(site, reqPath, ua, botName string) string {
	lp := strings.ToLower(reqPath)
	for _, prefix := range defaultHoneypotPrefixes {
		if strings.HasPrefix(lp, prefix) {
			return "honeypot"
		}
	}
	for _, custom := range m.extraHoneypot {
		if strings.HasPrefix(lp, custom) {
			return "honeypot"
		}
	}
	if !m.excludePHPSites[site] && strings.HasSuffix(lp, ".php") {
		return "honeypot"
	}

	if m.banScanners {
		if strings.HasSuffix(botName, "(Scanner)") {
			return "scanner"
		}
		lu := strings.ToLower(ua)
		for _, tok := range extraScannerTokens {
			if strings.Contains(lu, tok) {
				return "scanner"
			}
		}
	}
	return ""
}

// ThreatHit is a single log-observed honeypot or scanner hit queued for banning.
type ThreatHit struct {
	IP     string
	Reason string
	Site   string
	Path   string
	UA     string
	TS     int64
}

// BanRecord describes an active IP ban entry stored in analytics.db.
type BanRecord struct {
	IP         string `json:"ip"`
	Reason     string `json:"reason"`
	Source     string `json:"source"`
	Site       string `json:"site"`
	SamplePath string `json:"sample_path"`
	SampleUA   string `json:"sample_ua"`
	Hits       int64  `json:"hits"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	ExpiresAt  int64  `json:"expires_at"`
}

// BansResponse is the paginated response for GET /api/analytics/bans.
type BansResponse struct {
	Enabled       bool        `json:"enabled"`
	ShieldEnabled bool        `json:"shield_enabled"`
	DefaultTTLHrs int         `json:"default_ttl_hours"`
	Total         int         `json:"total"`
	ActiveCount   int         `json:"active_count"`
	Rows          []BanRecord `json:"rows"`
}

// RecordAutoBans upserts auto-detected attacker IPs with ttl. It returns true
// when at least one new or previously-expired IP became active.
func (s *Store) RecordAutoBans(hits []ThreatHit, ttl time.Duration, now time.Time) (bool, error) {
	if len(hits) == 0 {
		return false, nil
	}

	type agg struct {
		latest ThreatHit
		count  int64
	}
	byIP := make(map[string]*agg, len(hits))
	for _, h := range hits {
		if h.IP == "" {
			continue
		}
		a := byIP[h.IP]
		if a == nil {
			byIP[h.IP] = &agg{latest: h, count: 1}
		} else {
			a.count++
			if h.TS >= a.latest.TS {
				a.latest = h
			}
		}
	}
	if len(byIP) == 0 {
		return false, nil
	}

	nowUnix := now.Unix()
	var expUnix int64
	if ttl > 0 {
		expUnix = now.Add(ttl).Unix()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	selStmt, err := tx.Prepare(`SELECT expires_at FROM banned_ips WHERE ip = ?`)
	if err != nil {
		return false, err
	}
	defer selStmt.Close()

	insStmt, err := tx.Prepare(`
INSERT INTO banned_ips(ip, reason, source, site, sample_path, sample_ua, hits, created_at, last_seen_at, expires_at)
VALUES (?, ?, 'auto', ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(ip) DO UPDATE SET
	reason = excluded.reason,
	source = 'auto',
	site = excluded.site,
	sample_path = excluded.sample_path,
	sample_ua = excluded.sample_ua,
	hits = excluded.hits,
	created_at = excluded.created_at,
	last_seen_at = excluded.last_seen_at,
	expires_at = excluded.expires_at`)
	if err != nil {
		return false, err
	}
	defer insStmt.Close()

	updStmt, err := tx.Prepare(`
UPDATE banned_ips
SET hits = hits + ?,
	last_seen_at = ?,
	sample_path = CASE WHEN ? != '' THEN ? ELSE sample_path END,
	sample_ua = CASE WHEN ? != '' THEN ? ELSE sample_ua END
WHERE ip = ?`)
	if err != nil {
		return false, err
	}
	defer updStmt.Close()

	changed := false
	for ip, a := range byIP {
		var existingExp int64
		err := selStmt.QueryRow(ip).Scan(&existingExp)
		isActive := err == nil && (existingExp == 0 || existingExp > nowUnix)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}

		ua := trimStr(a.latest.UA, 200)
		p := trimStr(a.latest.Path, 200)
		lastSeen := a.latest.TS
		if lastSeen <= 0 {
			lastSeen = nowUnix
		}

		if !isActive {
			changed = true
			if _, err := insStmt.Exec(ip, a.latest.Reason, a.latest.Site, p, ua, a.count, nowUnix, lastSeen, expUnix); err != nil {
				return false, err
			}
		} else {
			if _, err := updStmt.Exec(a.count, lastSeen, p, p, ua, ua, ip); err != nil {
				return false, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed, nil
}

// BanIPManual inserts or updates a manually banned IP with ttl (<=0 means permanent).
func (s *Store) BanIPManual(ip, reason string, ttl time.Duration, now time.Time) (BanRecord, error) {
	nowUnix := now.Unix()
	var expUnix int64
	if ttl > 0 {
		expUnix = now.Add(ttl).Unix()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "manual"
	}
	if len(reason) > 120 {
		reason = reason[:120]
	}

	_, err := s.db.Exec(`
INSERT INTO banned_ips(ip, reason, source, site, sample_path, sample_ua, hits, created_at, last_seen_at, expires_at)
VALUES (?, ?, 'manual', '', '', '', 1, ?, ?, ?)
ON CONFLICT(ip) DO UPDATE SET
	reason = excluded.reason,
	source = 'manual',
	last_seen_at = excluded.last_seen_at,
	expires_at = excluded.expires_at`,
		ip, reason, nowUnix, nowUnix, expUnix,
	)
	if err != nil {
		return BanRecord{}, err
	}
	return BanRecord{
		IP:         ip,
		Reason:     reason,
		Source:     "manual",
		Hits:       1,
		CreatedAt:  nowUnix,
		LastSeenAt: nowUnix,
		ExpiresAt:  expUnix,
	}, nil
}

// UnbanIP deletes ip from banned_ips and reports whether a row was removed.
func (s *Store) UnbanIP(ip string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM banned_ips WHERE ip = ?`, ip)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// PurgeExpiredBans removes expired temporary bans and reports whether any were deleted.
func (s *Store) PurgeExpiredBans(now time.Time) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM banned_ips WHERE expires_at > 0 AND expires_at <= ?`, now.Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListActiveBanIPs returns all currently active banned IPs sorted lexicographically.
func (s *Store) ListActiveBanIPs(now time.Time) ([]string, error) {
	rows, err := s.db.Query(`SELECT ip FROM banned_ips WHERE expires_at = 0 OR expires_at > ? ORDER BY ip ASC`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ips []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, err
		}
		ips = append(ips, ip)
	}
	return ips, rows.Err()
}

// ListBansPage returns paginated active bans filtered by optional substring q.
func (s *Store) ListBansPage(now time.Time, q string, limit, offset int) (*BansResponse, error) {
	nowUnix := now.Unix()
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	resp := &BansResponse{
		Enabled: true,
		Rows:    make([]BanRecord, 0),
	}

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM banned_ips WHERE expires_at = 0 OR expires_at > ?`, nowUnix).Scan(&resp.ActiveCount); err != nil {
		return nil, err
	}

	where := `WHERE (expires_at = 0 OR expires_at > ?)`
	args := []any{nowUnix}
	if trimmed := strings.TrimSpace(q); trimmed != "" {
		like := "%" + trimmed + "%"
		where += ` AND (ip LIKE ? OR reason LIKE ? OR site LIKE ? OR sample_path LIKE ? OR sample_ua LIKE ?)`
		args = append(args, like, like, like, like, like)
	}

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM banned_ips `+where, args...).Scan(&resp.Total); err != nil {
		return nil, err
	}

	qArgs := append(append([]any(nil), args...), limit, offset)
	rows, err := s.db.Query(`
SELECT ip, reason, source, site, sample_path, sample_ua, hits, created_at, last_seen_at, expires_at
FROM banned_ips `+where+`
ORDER BY last_seen_at DESC, ip ASC
LIMIT ? OFFSET ?`, qArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var r BanRecord
		if err := rows.Scan(&r.IP, &r.Reason, &r.Source, &r.Site, &r.SamplePath, &r.SampleUA, &r.Hits, &r.CreatedAt, &r.LastSeenAt, &r.ExpiresAt); err != nil {
			return nil, err
		}
		resp.Rows = append(resp.Rows, r)
	}
	return resp, rows.Err()
}

// ErrProtectedIP is returned when attempting to ban a loopback, private, or allowlisted IP.
var ErrProtectedIP = errors.New("protected IP cannot be banned")

// ShieldManager coordinates auto-ban ingestion, manual ban/unban API actions,
// and atomic Caddy ban-snippet updates + reloads.
type ShieldManager struct {
	cfg     *config.Config
	store   *Store
	matcher *Matcher
	logf    func(string, ...any)

	mu            sync.Mutex
	reloadPending bool
}

// NewShieldManager builds a ShieldManager from cfg and store.
func NewShieldManager(cfg *config.Config, st *Store, logf func(string, ...any)) *ShieldManager {
	if logf == nil {
		logf = log.Printf
	}
	return &ShieldManager{
		cfg:     cfg,
		store:   st,
		matcher: NewMatcher(cfg),
		logf:    logf,
	}
}

// Sync prunes expired bans, updates CaddyBanFile atomically when the active IP
// set changes, and runs CaddyReloadCmd if configured.
func (sm *ShieldManager) Sync(ctx context.Context) error {
	if !sm.cfg.Analytics.Shield.Enabled {
		return nil
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.syncLocked(ctx)
}

func (sm *ShieldManager) syncLocked(ctx context.Context) error {
	now := time.Now()
	_, err := sm.store.PurgeExpiredBans(now)
	if err != nil {
		return err
	}
	ips, err := sm.store.ListActiveBanIPs(now)
	if err != nil {
		return err
	}
	sort.Strings(ips)

	banFile := sm.cfg.ShieldBanFile()
	if banFile == "" {
		return nil
	}

	rendered := renderCaddyBans(ips)
	existing, readErr := os.ReadFile(banFile)
	fileChanged := readErr != nil || !bytes.Equal(existing, rendered)

	if fileChanged {
		if err := writeFileAtomic(banFile, rendered, 0o644); err != nil {
			return err
		}
		sm.reloadPending = true
	}

	if sm.reloadPending && len(sm.cfg.Analytics.Shield.CaddyReloadCmd) > 0 {
		cmdArgs := sm.cfg.Analytics.Shield.CaddyReloadCmd
		runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, err := exec.CommandContext(runCtx, cmdArgs[0], cmdArgs[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("reload caddy (%v): %w (%s)", cmdArgs, err, strings.TrimSpace(string(out)))
		}
		sm.reloadPending = false
	}
	return nil
}

// RecordAutoBans persists new auto-detected hits and triggers Sync when new
// bans become active (or a prior reload failed).
func (sm *ShieldManager) RecordAutoBans(ctx context.Context, hits []ThreatHit) error {
	if !sm.cfg.Analytics.Shield.Enabled || len(hits) == 0 {
		return nil
	}
	changed, err := sm.store.RecordAutoBans(hits, sm.cfg.ShieldBanTTL(), time.Now())
	if err != nil {
		return err
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !changed && !sm.reloadPending {
		return nil
	}
	return sm.syncLocked(ctx)
}

// BanManual validates rawIP, adds a manual ban (ttlHours < 0 = permanent, 0 = default TTL),
// and immediately syncs Caddy.
func (sm *ShieldManager) BanManual(ctx context.Context, rawIP, reason string, ttlHours int) (BanRecord, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(rawIP))
	if err != nil {
		return BanRecord{}, fmt.Errorf("invalid ip address %q", rawIP)
	}
	ip := addr.Unmap().String()
	if sm.matcher.IsProtectedIP(ip) {
		return BanRecord{}, ErrProtectedIP
	}

	var ttl time.Duration
	switch {
	case ttlHours < 0:
		ttl = 0 // permanent
	case ttlHours == 0:
		ttl = sm.cfg.ShieldBanTTL()
	default:
		ttl = time.Duration(ttlHours) * time.Hour
	}

	rec, err := sm.store.BanIPManual(ip, reason, ttl, time.Now())
	if err != nil {
		return BanRecord{}, err
	}
	if err := sm.Sync(ctx); err != nil {
		return rec, err
	}
	return rec, nil
}

// Unban validates rawIP, removes it from banned_ips, and immediately syncs Caddy.
func (sm *ShieldManager) Unban(ctx context.Context, rawIP string) (bool, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(rawIP))
	if err != nil {
		return false, fmt.Errorf("invalid ip address %q", rawIP)
	}
	ip := addr.Unmap().String()
	removed, err := sm.store.UnbanIP(ip)
	if err != nil {
		return false, err
	}
	if removed {
		if err := sm.Sync(ctx); err != nil {
			return true, err
		}
	}
	return removed, nil
}

func renderCaddyBans(ips []string) []byte {
	var b strings.Builder
	b.WriteString("# Auto-generated by deployd shield. Do not edit manually.\n")
	b.WriteString("(ip_bans) {\n")
	if len(ips) > 0 {
		const chunkSize = 200
		var matcherNames []string
		for start, idx := 0, 0; start < len(ips); start, idx = start+chunkSize, idx+1 {
			end := start + chunkSize
			if end > len(ips) {
				end = len(ips)
			}
			name := fmt.Sprintf("@auto_banned_%d", idx)
			matcherNames = append(matcherNames, name)
			b.WriteString("\t")
			b.WriteString(name)
			b.WriteString(" client_ip ")
			b.WriteString(strings.Join(ips[start:end], " "))
			b.WriteByte('\n')
		}
		b.WriteString("\troute {\n")
		for _, name := range matcherNames {
			b.WriteString("\t\trespond ")
			b.WriteString(name)
			b.WriteString(" 403\n")
		}
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func trimStr(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max]
	}
	return s
}
