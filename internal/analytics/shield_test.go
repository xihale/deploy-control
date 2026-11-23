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
