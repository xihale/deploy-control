package analytics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deploycontrol/internal/config"
)

func TestShieldAutoBanManualUnbanAndCaddySync(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	banFile := filepath.Join(dir, "bans.caddy")
	reloadCountFile := filepath.Join(dir, "reloads.txt")
	reloadScript := filepath.Join(dir, "reload.sh")

	if err := os.WriteFile(reloadScript, []byte("#!/bin/sh\necho x >> "+reloadCountFile+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	yamlText := fmt.Sprintf(`
db_path: %s
users:
  - name: admin
    token_hash: %s
    role: admin
sites:
  - name: blog
    repo: https://github.com/example/blog
    branch: main
    url: https://xeed.ink
    script: /bin/true
  - name: fresh
    repo: https://github.com/example/fresh
    branch: main
    url: https://fresh.xeed.ink
    script: /bin/true
analytics:
  enabled: true
  db_path: %s
  caddy_logs: [%q]
  timezone: Asia/Shanghai
  exclude_ips: ["127.0.0.1"]
  shield:
    enabled: true
    ban_ttl_hours: 168
    caddy_ban_file: %q
    caddy_reload_cmd: [%q]
    allow_ips: ["198.51.100.77"]
    exclude_php_sites: ["fresh"]
`, filepath.Join(dir, "deploy.db"), strings.Repeat("a", 64), filepath.Join(dir, "analytics.db"), logPath, banFile, reloadScript)

	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlText), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	st, err := Open(cfg.AnalyticsDBFile())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	shield := NewShieldManager(cfg, st, nil)
	ing := NewIngestor(cfg, st, nil)
	ing.SetShield(shield)

	now := time.Now()
	recentTS := float64(now.Add(-5 * time.Second).Unix())
	oldTS := float64(now.Add(-10 * 24 * time.Hour).Unix())
	uaChrome := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36"

	lines := []string{
		// Attacker 1 hits /.env -> auto-banned + NOT counted as human PV
		makeCaddyLine(recentTS, "xeed.ink", "/.env", "203.0.113.55", uaChrome, "", 200, 28, 0.001, "text/plain; charset=utf-8"),
		// Attacker 2 uses nuclei scanner on / -> auto-banned
		makeCaddyLine(recentTS+1, "xeed.ink", "/", "203.0.113.66", "nuclei/3.0", "", 403, 0, 0.001, "text/plain"),
		// Legit FreshRSS PHP endpoint -> NOT banned
		makeCaddyLine(recentTS+2, "fresh.xeed.ink", "/api/greader.php", "203.0.113.88", uaChrome, "", 200, 100, 0.005, "application/json"),
		// Legit blog post with "wp" in slug -> NOT banned
		makeCaddyLine(recentTS+3, "xeed.ink", "/blog/adctf25-wp/", "203.0.113.89", uaChrome, "", 200, 5000, 0.010, "text/html"),
		// Protected / allowlisted / private IPs hitting /.env -> NEVER auto-banned
		makeCaddyLine(recentTS+4, "xeed.ink", "/.env", "10.1.0.2", uaChrome, "", 200, 28, 0.001, "text/plain"),
		makeCaddyLine(recentTS+5, "xeed.ink", "/.env", "198.51.100.77", uaChrome, "", 200, 28, 0.001, "text/plain"),
		// Ancient log line older than 7d TTL -> ignored for auto-ban
		makeCaddyLine(oldTS, "xeed.ink", "/.env", "203.0.113.99", uaChrome, "", 200, 28, 0.001, "text/plain"),
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := ing.Tick(ctx); err != nil {
		t.Fatal(err)
	}

	bans, err := st.ListBansPage(time.Now(), "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bans.Total != 2 {
		t.Fatalf("expected 2 active auto-bans, got %d: %+v", bans.Total, bans.Rows)
	}

	// Ensure /.env hit with Chrome UA was NOT counted as human PV.
	ov, err := st.QueryOverview(cfg, DateRange{From: now.In(cfg.AnalyticsLocation()).Format("2006-01-02"), To: now.In(cfg.AnalyticsLocation()).Format("2006-01-02"), Days: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Summary.TodayPV != 1 {
		t.Fatalf("expected today PV = 1 (only /blog/adctf25-wp/), got %d", ov.Summary.TodayPV)
	}

	banBytes, err := os.ReadFile(banFile)
	if err != nil {
		t.Fatal(err)
	}
	banText := string(banBytes)
	if !strings.Contains(banText, "203.0.113.55") || !strings.Contains(banText, "203.0.113.66") || strings.Contains(banText, "203.0.113.88") || strings.Contains(banText, "10.1.0.2") || strings.Contains(banText, "198.51.100.77") {
		t.Fatalf("unexpected Caddy ban file contents:\n%s", banText)
	}

	reloadsAfterFirst, _ := os.ReadFile(reloadCountFile)
	if got := strings.Count(string(reloadsAfterFirst), "\n"); got != 1 {
		t.Fatalf("expected 1 caddy reload, got %d", got)
	}

	// Append repeat hit from already-banned 203.0.113.55 -> hits increments, NO second Caddy reload!
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(makeCaddyLine(float64(time.Now().Unix()), "xeed.ink", "/wp-login.php", "203.0.113.55", uaChrome, "", 403, 0, 0.001, "text/plain") + "\n")
	_ = f.Close()

	if err := ing.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	reloadsAfterRepeat, _ := os.ReadFile(reloadCountFile)
	if got := strings.Count(string(reloadsAfterRepeat), "\n"); got != 1 {
		t.Fatalf("expected reload count to stay 1 on duplicate IP hit, got %d", got)
	}
	searchBans, err := st.ListBansPage(time.Now(), "203.0.113.55", 10, 0)
	if err != nil || len(searchBans.Rows) != 1 || searchBans.Rows[0].Hits != 2 {
		t.Fatalf("expected hits=2 for 203.0.113.55, got %+v (err=%v)", searchBans, err)
	}

	// Protected IP manual ban must be refused.
	if _, err := shield.BanManual(ctx, "10.1.0.2", "oops", 24); err == nil {
		t.Fatal("expected BanManual on private IP 10.1.0.2 to fail")
	}

	// Verify extended honeypot paths (nested .env, sensitive .json, mixed-case .PhP7) vs legit .json paths.
	m := NewMatcher(cfg)
	for _, badPath := range []string{
		"/app/.env",
		"/core/.env.production",
		"/.vscode/sftp.json",
		"/package.json",
		"/composer.json",
		"/credentials.json",
		"/secrets.json",
		"/config.json",
		"/swagger.json",
		"/firebase-adminsdk.json",
		"/actuator/env",
		"/randkeyword.PhP7",
		"/backup.sql.gz",
	} {
		if got := m.detectThreat("blog", badPath, uaChrome, ""); got != "honeypot" {
			t.Fatalf("expected %q to be detected as honeypot, got %q", badPath, got)
		}
	}
	for _, okPath := range []string{
		"/downloads/latest.json",
		"/deploy-meta.json",
		"/compilers/0.16.0/meta.json",
		"/vendor/ziglings/catalog.json",
		"/manifest.json",
		"/blog/adctf25-wp/",
	} {
		if got := m.detectThreat("blog", okPath, uaChrome, ""); got != "" {
			t.Fatalf("expected legit path %q not to trigger threat, got %q", okPath, got)
		}
	}

	// Manual ban + unban updates Caddy ban file.
	if _, err := shield.BanManual(ctx, "203.0.113.120", "manual abuse", 24); err != nil {
		t.Fatal(err)
	}
	banBytes2, _ := os.ReadFile(banFile)
	if !strings.Contains(string(banBytes2), "203.0.113.120") {
		t.Fatalf("expected manual ban in Caddy file, got:\n%s", string(banBytes2))
	}
	removed, err := shield.Unban(ctx, "203.0.113.120")
	if err != nil || !removed {
		t.Fatalf("expected unban success, removed=%v err=%v", removed, err)
	}
	banBytes3, _ := os.ReadFile(banFile)
	if strings.Contains(string(banBytes3), "203.0.113.120") {
		t.Fatalf("expected unbanned IP removed from Caddy file, got:\n%s", string(banBytes3))
	}
}

func TestShieldCapLRUSubnetCollapseAndCooldown(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	banFile := filepath.Join(dir, "bans.caddy")
	reloadCountFile := filepath.Join(dir, "reloads.txt")
	reloadScript := filepath.Join(dir, "reload.sh")

	if err := os.WriteFile(reloadScript, []byte("#!/bin/sh\necho x >> "+reloadCountFile+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	yamlText := fmt.Sprintf(`
db_path: %s
users:
  - name: admin
    token_hash: %s
    role: admin
sites:
  - name: blog
    repo: https://github.com/example/blog
    branch: main
    url: https://xeed.ink
    script: /bin/true
analytics:
  enabled: true
  db_path: %s
  caddy_logs: [%q]
  timezone: Asia/Shanghai
  shield:
    enabled: true
    ban_ttl_hours: 168
    max_bans: 4
    subnet_threshold: 3
    reload_cooldown_sec: 1
    caddy_ban_file: %q
    caddy_reload_cmd: [%q]
    allow_ips: ["198.51.100.77"]
`, filepath.Join(dir, "deploy.db"), strings.Repeat("a", 64), filepath.Join(dir, "analytics.db"), logPath, banFile, reloadScript)

	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlText), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	st, err := Open(cfg.AnalyticsDBFile())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	shield := NewShieldManager(cfg, st, nil)
	ctx := context.Background()

	// Manual ban survives LRU eviction.
	if _, err := shield.BanManual(ctx, "203.0.113.10", "keep-manual", 24); err != nil {
		t.Fatal(err)
	}
	// Protected CIDR overlapping allow_ips (198.51.100.77) must be refused.
	if _, err := shield.BanManual(ctx, "198.51.100.0/24", "too-wide", 24); err == nil {
		t.Fatal("expected BanManual on CIDR overlapping allow_ips to fail")
	}
	// Overly broad prefix must be refused.
	if _, err := shield.BanManual(ctx, "203.0.0.0/8", "way-too-wide", 24); err == nil {
		t.Fatal("expected BanManual on /8 prefix to fail")
	}

	// Simulate 3 IPs from 203.0.114.0/24 -> collapses into 203.0.114.0/24 (1 row).
	now := time.Now()
	if err := shield.RecordAutoBans(ctx, []ThreatHit{
		{IP: "203.0.114.1", Reason: "honeypot", Site: "blog", Path: "/.env", TS: now.Add(-3 * time.Minute).Unix()},
		{IP: "203.0.114.2", Reason: "honeypot", Site: "blog", Path: "/.env", TS: now.Add(-2 * time.Minute).Unix()},
	}); err != nil {
		t.Fatal(err)
	}
	// Because BanManual just ran reload, cooldown=1s skips auto-ban reload right away.
	reloadsBeforeWait, _ := os.ReadFile(reloadCountFile)
	if got := strings.Count(string(reloadsBeforeWait), "\n"); got != 1 {
		t.Fatalf("expected cooldown to defer auto-ban reload (count=1), got %d", got)
	}

	// Wait past 1s cooldown and add the 3rd IP in 203.0.114.0/24 -> collapses to /24 and reloads!
	time.Sleep(1100 * time.Millisecond)
	if err := shield.RecordAutoBans(ctx, []ThreatHit{
		{IP: "203.0.114.3", Reason: "honeypot", Site: "blog", Path: "/package.json", TS: now.Unix()},
	}); err != nil {
		t.Fatal(err)
	}
	reloadsAfterCollapse, _ := os.ReadFile(reloadCountFile)
	if got := strings.Count(string(reloadsAfterCollapse), "\n"); got != 2 {
		t.Fatalf("expected reload after cooldown (count=2), got %d", got)
	}

	banTextBytes, _ := os.ReadFile(banFile)
	if !strings.Contains(string(banTextBytes), "203.0.114.0/24") || strings.Contains(string(banTextBytes), "203.0.114.1") {
		t.Fatalf("expected 203.0.114.0/24 collapsed in Caddyfile, got:\n%s", string(banTextBytes))
	}

	// Hit from another IP in collapsed /24 increments CIDR hits without adding a child row.
	if err := shield.RecordAutoBans(ctx, []ThreatHit{
		{IP: "203.0.114.99", Reason: "honeypot", Site: "blog", Path: "/secrets.json", TS: now.Unix() + 1},
	}); err != nil {
		t.Fatal(err)
	}
	cidrs, err := st.ListBansPage(time.Now(), "203.0.114.0/24", 10, 0)
	if err != nil || len(cidrs.Rows) != 1 || cidrs.Rows[0].Hits != 4 {
		t.Fatalf("expected collapsed CIDR hits=4, got %+v (err=%v)", cidrs, err)
	}

	// 3 IPs in 198.51.100.0/24 (contains allow_ips .77) must NOT collapse to /24.
	// Also pushes total past max_bans=4 so oldest auto entry gets LRU-evicted while manual stays.
	if err := shield.RecordAutoBans(ctx, []ThreatHit{
		{IP: "198.51.100.1", Reason: "honeypot", Site: "blog", Path: "/.env", TS: now.Unix() + 10},
		{IP: "198.51.100.2", Reason: "honeypot", Site: "blog", Path: "/.env", TS: now.Unix() + 11},
		{IP: "198.51.100.3", Reason: "honeypot", Site: "blog", Path: "/.env", TS: now.Unix() + 12},
	}); err != nil {
		t.Fatal(err)
	}

	// Verify deferred reload flushes after cooldown via FlushPendingReload.
	time.Sleep(1100 * time.Millisecond)
	if err := shield.FlushPendingReload(ctx); err != nil {
		t.Fatal(err)
	}
	reloadsAfterFlush, _ := os.ReadFile(reloadCountFile)
	if got := strings.Count(string(reloadsAfterFlush), "\n"); got != 3 {
		t.Fatalf("expected FlushPendingReload to run deferred reload (count=3), got %d", got)
	}

	allBans, err := st.ListBansPage(time.Now(), "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if allBans.ActiveCount != 4 {
		t.Fatalf("expected active_count capped at max_bans=4, got %d (%+v)", allBans.ActiveCount, allBans.Rows)
	}
	var hasManual, hasAllowSubnet bool
	for _, r := range allBans.Rows {
		if r.IP == "203.0.113.10" && r.Source == "manual" {
			hasManual = true
		}
		if r.IP == "198.51.100.0/24" {
			hasAllowSubnet = true
		}
	}
	if !hasManual {
		t.Fatalf("expected manual ban 203.0.113.10 to survive LRU eviction: %+v", allBans.Rows)
	}
	if hasAllowSubnet {
		t.Fatalf("expected subnet containing allow_ips not to collapse to /24: %+v", allBans.Rows)
	}

	// Verify Unban works on CIDR.
	if err := shield.RecordAutoBans(ctx, []ThreatHit{
		{IP: "203.0.115.1", Reason: "honeypot", Site: "blog", Path: "/.env", TS: now.Unix() + 20},
		{IP: "203.0.115.2", Reason: "honeypot", Site: "blog", Path: "/.env", TS: now.Unix() + 21},
		{IP: "203.0.115.3", Reason: "honeypot", Site: "blog", Path: "/.env", TS: now.Unix() + 22},
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := shield.Unban(ctx, "203.0.115.0/24")
	if err != nil || !removed {
		t.Fatalf("expected CIDR unban success, removed=%v err=%v", removed, err)
	}
}
