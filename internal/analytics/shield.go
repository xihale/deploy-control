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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"deploycontrol/internal/config"
)

var (
	honeypotPathRe = regexp.MustCompile(`(?i)((^|/)\.env([.~_-][^/]*)?$|(^|/)\.(git|svn|hg|bzr|ds_store|htaccess|htpasswd|ssh|aws|docker|kube|vscode|idea|npmrc|pypirc|netrc)(/|$)|(^|/)(wp-admin|wp-login|wp-content|wp-includes|wp-config|wordpress|xmlrpc\.php|wp-links-opml\.php|wp-access\.php|phpmyadmin|pma|adminer|cgi-bin|actuator|_profiler|_environment|server-status|server-info|phpinfo)(/|$|\.)|(^|/)(package\.json|package-lock\.json|composer\.json|composer\.lock|tsconfig\.json|jsconfig\.json|sftp\.json|config\.json|settings\.json|secrets?\.json|credentials?\.json|service[-_]?account\.json|google-services\.json|firebase[^/]*\.json|appsettings(\.[^/]+)?\.json|swagger\.json|openapi\.json|docker-compose\.ya?ml|dockerfile|config\.ya?ml|secrets?\.ya?ml|database\.ya?ml|id_rsa(\.pub)?|id_ed25519(\.pub)?|authorized_keys|web\.config)$|\.(sql|sqlite|sqlite3|bak|swp)(\.(gz|bz2|xz))?$)`)
	honeypotPHPRe  = regexp.MustCompile(`(?i)\.php[0-9a-z]*(/|$)`)
)

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
	if honeypotPathRe.MatchString(reqPath) {
		return "honeypot"
	}
	for _, custom := range m.extraHoneypot {
		if strings.HasPrefix(lp, custom) {
			return "honeypot"
		}
	}
	if !m.excludePHPSites[site] && honeypotPHPRe.MatchString(reqPath) {
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
	Enabled         bool        `json:"enabled"`
	ShieldEnabled   bool        `json:"shield_enabled"`
	DefaultTTLHrs   int         `json:"default_ttl_hours"`
	MaxBans         int         `json:"max_bans"`
	SubnetThreshold int         `json:"subnet_threshold"`
	Total           int         `json:"total"`
	ActiveCount     int         `json:"active_count"`
	Rows            []BanRecord `json:"rows"`
}

// AutoBanPolicy bounds ban-table growth and triggers /24 or /64 subnet collapsing.
type AutoBanPolicy struct {
	TTL             time.Duration
	MaxBans         int
	SubnetThreshold int
	CanBanSubnet    func(netip.Prefix) bool
}

func (s *Store) ensureBannedIPsSubnetColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(banned_ips)`)
	if err != nil {
		return err
	}
	hasSubnet := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "subnet" {
			hasSubnet = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasSubnet {
		if _, err := s.db.Exec(`ALTER TABLE banned_ips ADD COLUMN subnet TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_banned_ips_subnet ON banned_ips(subnet)`); err != nil {
		return err
	}

	// Backfill subnet column on rows migrated from older schemas.
	pendingRows, err := s.db.Query(`SELECT ip FROM banned_ips WHERE subnet = ''`)
	if err != nil {
		return err
	}
	var toUpdate []string
	for pendingRows.Next() {
		var ip string
		if err := pendingRows.Scan(&ip); err == nil && ip != "" {
			toUpdate = append(toUpdate, ip)
		}
	}
	_ = pendingRows.Close()
	for _, ip := range toUpdate {
		if sub, _, ok := subnetForTarget(ip); ok {
			if _, err := s.db.Exec(`UPDATE banned_ips SET subnet = ? WHERE ip = ?`, sub, ip); err != nil {
				return err
			}
		}
	}
	return nil
}

func subnetForTarget(target string) (string, netip.Prefix, bool) {
	s := strings.TrimSpace(target)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return "", netip.Prefix{}, false
		}
		p = unmapPrefix(p)
		if !p.IsValid() {
			return "", netip.Prefix{}, false
		}
		if p.Addr().Is4() && p.Bits() >= 24 {
			sub, _ := p.Addr().Prefix(24)
			sub = sub.Masked()
			return sub.String(), sub, true
		}
		if p.Addr().Is6() && p.Bits() >= 64 {
			sub, _ := p.Addr().Prefix(64)
			sub = sub.Masked()
			return sub.String(), sub, true
		}
		return p.String(), p, true
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return "", netip.Prefix{}, false
	}
	addr = addr.Unmap()
	if addr.Is4() {
		sub, _ := addr.Prefix(24)
		sub = sub.Masked()
		return sub.String(), sub, true
	}
	sub, _ := addr.Prefix(64)
	sub = sub.Masked()
	return sub.String(), sub, true
}

func parseBanTarget(raw string) (canonical string, isCIDR bool, prefix netip.Prefix, err error) {
	s := strings.TrimSpace(raw)
	if strings.Contains(s, "/") {
		p, perr := netip.ParsePrefix(s)
		if perr != nil {
			return "", false, netip.Prefix{}, fmt.Errorf("invalid ip/cidr %q", raw)
		}
		p = unmapPrefix(p)
		if !p.IsValid() {
			return "", false, netip.Prefix{}, fmt.Errorf("invalid ip/cidr %q", raw)
		}
		if (p.Addr().Is4() && p.Bits() < 16) || (p.Addr().Is6() && p.Bits() < 32) {
			return "", false, netip.Prefix{}, fmt.Errorf("invalid ip/cidr %q: prefix too broad", raw)
		}
		if p.IsSingleIP() {
			a := p.Addr()
			return a.String(), false, netip.PrefixFrom(a, a.BitLen()), nil
		}
		return p.String(), true, p, nil
	}
	addr, aerr := netip.ParseAddr(s)
	if aerr != nil {
		return "", false, netip.Prefix{}, fmt.Errorf("invalid ip address %q", raw)
	}
	addr = addr.Unmap()
	return addr.String(), false, netip.PrefixFrom(addr, addr.BitLen()), nil
}

func parseUnbanTarget(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return "", fmt.Errorf("invalid ip/cidr %q", raw)
		}
		p = unmapPrefix(p)
		if p.IsSingleIP() {
			return p.Addr().String(), nil
		}
		return p.String(), nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return "", fmt.Errorf("invalid ip address %q", raw)
	}
	return addr.Unmap().String(), nil
}

type ipAgg struct {
	ip     string
	addr   netip.Addr
	subnet string
	prefix netip.Prefix
	latest ThreatHit
	count  int64
}

type cidrRow struct {
	text   string
	prefix netip.Prefix
}

func trimAutoBansToCapTx(tx *sql.Tx, maxBans int) (bool, error) {
	if maxBans <= 0 {
		return false, nil
	}
	var total int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM banned_ips`).Scan(&total); err != nil {
		return false, err
	}
	if total <= maxBans {
		return false, nil
	}
	excess := total - maxBans
	res, err := tx.Exec(`
DELETE FROM banned_ips WHERE ip IN (
	SELECT ip FROM banned_ips
	WHERE source = 'auto'
	ORDER BY last_seen_at ASC, created_at ASC, ip ASC
	LIMIT ?
)`, excess)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RecordAutoBans upserts auto-detected attacker IPs with policy TTL, collapses
// dense /24 or /64 subnets into CIDR bans, and enforces MaxBans via auto-LRU
// eviction. It returns true when the active ban set changed.
func (s *Store) RecordAutoBans(hits []ThreatHit, policy AutoBanPolicy, now time.Time) (bool, error) {
	if len(hits) == 0 {
		return false, nil
	}

	byIP := make(map[string]*ipAgg, len(hits))
	for _, h := range hits {
		if h.IP == "" {
			continue
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(h.IP))
		if err != nil {
			continue
		}
		addr = addr.Unmap()
		ip := addr.String()
		a := byIP[ip]
		if a == nil {
			sub, pref, _ := subnetForTarget(ip)
			byIP[ip] = &ipAgg{ip: ip, addr: addr, subnet: sub, prefix: pref, latest: h, count: 1}
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
	if policy.TTL > 0 {
		expUnix = now.Add(policy.TTL).Unix()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	changed := false
	// Purge expired entries first so subnet/LRU counts only reflect active bans.
	if res, err := tx.Exec(`DELETE FROM banned_ips WHERE expires_at > 0 AND expires_at <= ?`, nowUnix); err != nil {
		return false, err
	} else if n, _ := res.RowsAffected(); n > 0 {
		changed = true
	}

	// Load active CIDR bans so new hits falling inside an existing banned CIDR
	// increment that CIDR rather than creating duplicate child IP rows.
	var activeCIDRs []cidrRow
	cidrRows, err := tx.Query(`SELECT ip FROM banned_ips WHERE ip LIKE '%/%'`)
	if err != nil {
		return false, err
	}
	for cidrRows.Next() {
		var raw string
		if err := cidrRows.Scan(&raw); err != nil {
			_ = cidrRows.Close()
			return false, err
		}
		if p, perr := netip.ParsePrefix(raw); perr == nil {
			activeCIDRs = append(activeCIDRs, cidrRow{text: raw, prefix: unmapPrefix(p)})
		}
	}
	_ = cidrRows.Close()

	byCIDRHit := make(map[string]*ipAgg)
	for ip, a := range byIP {
		var matchedCIDR string
		bestBits := -1
		for _, c := range activeCIDRs {
			if c.prefix.Contains(a.addr) && c.prefix.Bits() > bestBits {
				matchedCIDR = c.text
				bestBits = c.prefix.Bits()
			}
		}
		if matchedCIDR != "" {
			delete(byIP, ip)
			cur := byCIDRHit[matchedCIDR]
			if cur == nil {
				byCIDRHit[matchedCIDR] = &ipAgg{ip: matchedCIDR, latest: a.latest, count: a.count}
			} else {
				cur.count += a.count
				if a.latest.TS >= cur.latest.TS {
					cur.latest = a.latest
				}
			}
		}
	}

	updStmt, err := tx.Prepare(`
UPDATE banned_ips
SET hits = hits + ?,
	last_seen_at = MAX(last_seen_at, ?),
	sample_path = CASE WHEN ? != '' THEN ? ELSE sample_path END,
	sample_ua = CASE WHEN ? != '' THEN ? ELSE sample_ua END
WHERE ip = ?`)
	if err != nil {
		return false, err
	}
	defer updStmt.Close()

	for cidrText, a := range byCIDRHit {
		ua := trimStr(a.latest.UA, 200)
		p := trimStr(a.latest.Path, 200)
		lastSeen := a.latest.TS
		if lastSeen <= 0 {
			lastSeen = nowUnix
		}
		if _, err := updStmt.Exec(a.count, lastSeen, p, p, ua, ua, cidrText); err != nil {
			return false, err
		}
	}

	insStmt, err := tx.Prepare(`
INSERT INTO banned_ips(ip, subnet, reason, source, site, sample_path, sample_ua, hits, created_at, last_seen_at, expires_at)
VALUES (?, ?, ?, 'auto', ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(ip) DO UPDATE SET
	subnet = excluded.subnet,
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

	// Group remaining single IPs by /24 or /64 subnet to detect subnet-wide storms.
	type subGroup struct {
		subnet string
		prefix netip.Prefix
		items  []*ipAgg
	}
	bySub := make(map[string]*subGroup)
	var noSub []*ipAgg
	for _, a := range byIP {
		if a.subnet == "" {
			noSub = append(noSub, a)
			continue
		}
		g := bySub[a.subnet]
		if g == nil {
			g = &subGroup{subnet: a.subnet, prefix: a.prefix}
			bySub[a.subnet] = g
		}
		g.items = append(g.items, a)
	}

	type existingSubRow struct {
		ip       string
		source   string
		hits     int64
		created  int64
		lastSeen int64
	}

	selSubStmt, err := tx.Prepare(`SELECT ip, source, hits, created_at, last_seen_at FROM banned_ips WHERE subnet = ? AND ip NOT LIKE '%/%'`)
	if err != nil {
		return false, err
	}
	defer selSubStmt.Close()

	var singles []*ipAgg
	singles = append(singles, noSub...)

	for subKey, g := range bySub {
		sRows, err := selSubStmt.Query(subKey)
		if err != nil {
			return false, err
		}
		existing := make(map[string]existingSubRow)
		for sRows.Next() {
			var r existingSubRow
			if err := sRows.Scan(&r.ip, &r.source, &r.hits, &r.created, &r.lastSeen); err != nil {
				_ = sRows.Close()
				return false, err
			}
			existing[r.ip] = r
		}
		_ = sRows.Close()

		shouldCollapse := false
		if policy.SubnetThreshold > 0 && (policy.CanBanSubnet == nil || policy.CanBanSubnet(g.prefix)) {
			autoIPs := make(map[string]bool)
			for ip, r := range existing {
				if r.source == "auto" {
					autoIPs[ip] = true
				}
			}
			for _, a := range g.items {
				if r, ok := existing[a.ip]; ok && r.source != "auto" {
					continue
				}
				autoIPs[a.ip] = true
			}
			shouldCollapse = len(autoIPs) >= policy.SubnetThreshold
		}

		if !shouldCollapse {
			singles = append(singles, g.items...)
			continue
		}

		// Collapse all auto child IPs in this subnet into one CIDR ban.
		var totalHits int64
		minCreated := nowUnix
		maxLastSeen := nowUnix
		var latestHit ThreatHit
		for _, r := range existing {
			if r.source != "auto" {
				continue
			}
			totalHits += r.hits
			if r.created > 0 && r.created < minCreated {
				minCreated = r.created
			}
			if r.lastSeen > maxLastSeen {
				maxLastSeen = r.lastSeen
			}
		}
		for _, a := range g.items {
			if r, ok := existing[a.ip]; ok && r.source != "auto" {
				// Still keep manual row hit count up to date instead of deleting it.
				singles = append(singles, a)
				continue
			}
			totalHits += a.count
			ts := a.latest.TS
			if ts <= 0 {
				ts = nowUnix
			}
			if ts > maxLastSeen {
				maxLastSeen = ts
			}
			if a.latest.TS >= latestHit.TS {
				latestHit = a.latest
			}
		}
		if _, err := tx.Exec(`DELETE FROM banned_ips WHERE subnet = ? AND source = 'auto' AND ip != ?`, subKey, subKey); err != nil {
			return false, err
		}
		if _, err := insStmt.Exec(
			subKey, subKey, latestHit.Reason, latestHit.Site,
			trimStr(latestHit.Path, 200), trimStr(latestHit.UA, 200),
			totalHits, minCreated, maxLastSeen, expUnix,
		); err != nil {
			return false, err
		}
		changed = true
	}

	selStmt, err := tx.Prepare(`SELECT ip FROM banned_ips WHERE ip = ?`)
	if err != nil {
		return false, err
	}
	defer selStmt.Close()

	for _, a := range singles {
		var existingIP string
		err := selStmt.QueryRow(a.ip).Scan(&existingIP)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}

		ua := trimStr(a.latest.UA, 200)
		p := trimStr(a.latest.Path, 200)
		lastSeen := a.latest.TS
		if lastSeen <= 0 {
			lastSeen = nowUnix
		}

		if errors.Is(err, sql.ErrNoRows) {
			changed = true
			if _, err := insStmt.Exec(a.ip, a.subnet, a.latest.Reason, a.latest.Site, p, ua, a.count, nowUnix, lastSeen, expUnix); err != nil {
				return false, err
			}
		} else {
			if _, err := updStmt.Exec(a.count, lastSeen, p, p, ua, ua, a.ip); err != nil {
				return false, err
			}
		}
	}

	if trimmed, err := trimAutoBansToCapTx(tx, policy.MaxBans); err != nil {
		return false, err
	} else if trimmed {
		changed = true
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed, nil
}

// BanIPManual inserts or updates a manually banned IP/CIDR with ttl (<=0 means permanent)
// and enforces maxBans against older auto-bans when configured.
func (s *Store) BanIPManual(ip, reason string, ttl time.Duration, now time.Time, maxBans ...int) (BanRecord, error) {
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
	sub, _, _ := subnetForTarget(ip)

	tx, err := s.db.Begin()
	if err != nil {
		return BanRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
INSERT INTO banned_ips(ip, subnet, reason, source, site, sample_path, sample_ua, hits, created_at, last_seen_at, expires_at)
VALUES (?, ?, ?, 'manual', '', '', '', 1, ?, ?, ?)
ON CONFLICT(ip) DO UPDATE SET
	subnet = excluded.subnet,
	reason = excluded.reason,
	source = 'manual',
	last_seen_at = excluded.last_seen_at,
	expires_at = excluded.expires_at`,
		ip, sub, reason, nowUnix, nowUnix, expUnix,
	); err != nil {
		return BanRecord{}, err
	}
	if ip == sub && strings.Contains(ip, "/") {
		if _, err := tx.Exec(`DELETE FROM banned_ips WHERE subnet = ? AND source = 'auto' AND ip != ?`, sub, ip); err != nil {
			return BanRecord{}, err
		}
	}
	if len(maxBans) > 0 && maxBans[0] > 0 {
		if _, err := trimAutoBansToCapTx(tx, maxBans[0]); err != nil {
			return BanRecord{}, err
		}
	}
	if err := tx.Commit(); err != nil {
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
	lastReloadAt  time.Time
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
// set changes, and runs CaddyReloadCmd if configured (subject to ReloadCooldown).
func (sm *ShieldManager) Sync(ctx context.Context) error {
	if !sm.cfg.Analytics.Shield.Enabled {
		return nil
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.syncLocked(ctx, false)
}

// FlushPendingReload executes a deferred Caddy reload once the configured
// cooldown has elapsed. It is cheap to call on every ingestion tick.
func (sm *ShieldManager) FlushPendingReload(ctx context.Context) error {
	if !sm.cfg.Analytics.Shield.Enabled {
		return nil
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !sm.reloadPending {
		return nil
	}
	cooldown := sm.cfg.ShieldReloadCooldown()
	if !sm.lastReloadAt.IsZero() && cooldown > 0 && time.Since(sm.lastReloadAt) < cooldown {
		return nil
	}
	return sm.syncLocked(ctx, false)
}

func (sm *ShieldManager) syncLocked(ctx context.Context, forceReload bool) error {
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
		cooldown := sm.cfg.ShieldReloadCooldown()
		if !forceReload && !sm.lastReloadAt.IsZero() && cooldown > 0 && time.Since(sm.lastReloadAt) < cooldown {
			return nil
		}
		cmdArgs := sm.cfg.Analytics.Shield.CaddyReloadCmd
		runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, err := exec.CommandContext(runCtx, cmdArgs[0], cmdArgs[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("reload caddy (%v): %w (%s)", cmdArgs, err, strings.TrimSpace(string(out)))
		}
		sm.reloadPending = false
		sm.lastReloadAt = time.Now()
	}
	return nil
}

// RecordAutoBans persists new auto-detected hits and triggers Sync when new
// bans become active (or a prior reload failed).
func (sm *ShieldManager) RecordAutoBans(ctx context.Context, hits []ThreatHit) error {
	if !sm.cfg.Analytics.Shield.Enabled || len(hits) == 0 {
		return nil
	}
	policy := AutoBanPolicy{
		TTL:             sm.cfg.ShieldBanTTL(),
		MaxBans:         sm.cfg.ShieldMaxBans(),
		SubnetThreshold: sm.cfg.ShieldSubnetThreshold(),
		CanBanSubnet: func(p netip.Prefix) bool {
			return !sm.matcher.IsProtectedPrefix(p)
		},
	}
	changed, err := sm.store.RecordAutoBans(hits, policy, time.Now())
	if err != nil {
		return err
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !changed && !sm.reloadPending {
		return nil
	}
	return sm.syncLocked(ctx, false)
}

// BanManual validates rawIP (single IP or CIDR), adds a manual ban
// (ttlHours < 0 = permanent, 0 = default TTL), and immediately syncs Caddy.
func (sm *ShieldManager) BanManual(ctx context.Context, rawIP, reason string, ttlHours int) (BanRecord, error) {
	canonical, isCIDR, prefix, err := parseBanTarget(rawIP)
	if err != nil {
		return BanRecord{}, err
	}
	if isCIDR {
		if sm.matcher.IsProtectedPrefix(prefix) {
			return BanRecord{}, ErrProtectedIP
		}
	} else if sm.matcher.IsProtectedIP(canonical) {
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

	rec, err := sm.store.BanIPManual(canonical, reason, ttl, time.Now(), sm.cfg.ShieldMaxBans())
	if err != nil {
		return BanRecord{}, err
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if err := sm.syncLocked(ctx, true); err != nil {
		return rec, err
	}
	return rec, nil
}

// Unban validates rawIP (single IP or CIDR), removes it from banned_ips, and
// immediately syncs Caddy.
func (sm *ShieldManager) Unban(ctx context.Context, rawIP string) (bool, error) {
	canonical, err := parseUnbanTarget(rawIP)
	if err != nil {
		return false, err
	}
	removed, err := sm.store.UnbanIP(canonical)
	if err != nil {
		return false, err
	}
	if removed {
		sm.mu.Lock()
		defer sm.mu.Unlock()
		if err := sm.syncLocked(ctx, true); err != nil {
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
