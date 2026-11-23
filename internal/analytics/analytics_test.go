package analytics

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deploycontrol/internal/config"
)

func testAnalyticsConfig(t *testing.T, logPath string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		DBPath: filepath.Join(dir, "deploy.db"),
		Users:  []config.User{{Name: "admin", TokenHash: strings.Repeat("a", 64), Role: config.RoleAdmin}},
		Sites: []config.Site{
			{
				Name:   "blog",
				Repo:   "https://github.com/example/blog",
				Branch: "main",
				URL:    "https://xeed.ink",
				Hosts:  []string{"www.xeed.ink"},
				Script: "/bin/true",
			},
			{
				Name:   "zp",
				Repo:   "https://github.com/example/zp",
				Branch: "main",
				URL:    "https://zp.xeed.ink",
				Script: "/bin/true",
			},
		},
		Analytics: config.Analytics{
			Enabled:      true,
			DBPath:       filepath.Join(dir, "analytics.db"),
			CaddyLogs:    []string{logPath},
			Timezone:     "Asia/Shanghai",
			ExcludePaths: []string{"/internal/"},
			ExcludeIPs:   []string{"127.0.0.1", "10.0.0.0/8"},
		},
	}
	// Trigger Load-equivalent validation via a written YAML or validate?
	// Let's write minimal YAML so config.Load validates everything cleanly.
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
    hosts: [www.xeed.ink]
    script: /bin/true
  - name: zp
    repo: https://github.com/example/zp
    branch: main
    url: https://zp.xeed.ink
    script: /bin/true
analytics:
  enabled: true
  db_path: %s
  caddy_logs: [%q]
  timezone: Asia/Shanghai
  retention_days: 30
  exclude_paths: ["/internal/"]
  exclude_ips: ["127.0.0.1", "10.0.0.0/8"]
`, cfg.DBPath, strings.Repeat("a", 64), cfg.Analytics.DBPath, logPath)
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlText), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func makeCaddyLine(ts float64, host, uri, ip, ua, ref string, status int, size int64, dur float64, contentType string) string {
	return fmt.Sprintf(
		`{"ts":%f,"duration":%f,"size":%d,"status":%d,"request":{"remote_ip":%q,"client_ip":%q,"method":"GET","host":%q,"uri":%q,"headers":{"User-Agent":[%q],"Referer":[%q]}},"resp_headers":{"Content-Type":[%q]}}`,
		ts, dur, size, status, ip, ip, host, uri, ua, ref, contentType,
	)
}

func TestIngestDeduplicatedDAUAndDimensionTables(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	cfg := testAnalyticsConfig(t, logPath)

	st, err := Open(cfg.AnalyticsDBFile())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Reference time: 2026-11-23 14:00:00 +0800
	loc := cfg.AnalyticsLocation()
	now := time.Date(2026, 11, 23, 14, 0, 0, 0, loc)
	todayTS := float64(now.Add(-30 * time.Minute).Unix())
	yesterdayTS := float64(now.AddDate(0, 0, -1).Unix())
	// 2026-11-22 17:00 UTC == 2026-11-23 01:00 Asia/Shanghai (tests timezone bucketing!)
	crossMidnightTS := float64(time.Date(2026, 11, 22, 17, 0, 0, 0, time.UTC).Unix())

	uaChromeMac := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	uaSafariIOS := "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
	uaBot := "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"

	lines := []string{
		// User 1 visits blog (2 pages) and zp (1 page) today -> global DAU=1 for User 1, blog DAU+1, zp DAU+1
		makeCaddyLine(todayTS, "xeed.ink", "/posts/hello?utm=1", "203.0.113.10", uaChromeMac, "https://github.com/xihale", 200, 12000, 0.012, "text/html; charset=utf-8"),
		makeCaddyLine(todayTS+5, "www.xeed.ink:443", "/about", "203.0.113.10", uaChromeMac, "https://xeed.ink/posts/hello", 304, 0, 0.003, ""),
		makeCaddyLine(todayTS+10, "zp.xeed.ink", "/", "203.0.113.10", uaChromeMac, "", 200, 8000, 0.008, "text/html"),
		// Static asset & JSON API for blog: counts toward requests/bytes, NOT PV!
		makeCaddyLine(todayTS+11, "xeed.ink", "/_astro/app.123.js", "203.0.113.10", uaChromeMac, "https://xeed.ink/", 200, 45000, 0.002, "application/javascript"),
		makeCaddyLine(todayTS+12, "xeed.ink", "/api/comments", "203.0.113.10", uaChromeMac, "https://xeed.ink/", 200, 500, 0.005, "application/json"),
		// User 2 visits blog across UTC midnight (still 2026-11-23 in Asia/Shanghai)
		makeCaddyLine(crossMidnightTS, "xeed.ink", "/", "198.51.100.20", uaSafariIOS, "https://news.ycombinator.com/", 200, 10000, 0.020, "text/html"),
		// 404 error on blog
		makeCaddyLine(todayTS+20, "xeed.ink", "/missing-page", "198.51.100.20", uaSafariIOS, "", 404, 300, 0.001, "text/html"),
		// Bot hit on blog -> counted in bot_hits, NOT pv/dau
		makeCaddyLine(todayTS+30, "xeed.ink", "/posts/hello", "66.249.66.1", uaBot, "", 200, 12000, 0.010, "text/html"),
		// Excluded IP & excluded path & unknown host -> completely ignored
		makeCaddyLine(todayTS+40, "xeed.ink", "/", "10.0.0.5", uaChromeMac, "", 200, 1000, 0.001, "text/html"),
		makeCaddyLine(todayTS+41, "xeed.ink", "/internal/health", "203.0.113.10", uaChromeMac, "", 200, 100, 0.001, "text/plain"),
		makeCaddyLine(todayTS+42, "other.example.com", "/", "203.0.113.10", uaChromeMac, "", 200, 1000, 0.001, "text/html"),
		// Yesterday visit on blog by User 1
		makeCaddyLine(yesterdayTS, "xeed.ink", "/", "203.0.113.10", uaChromeMac, "", 200, 10000, 0.010, "text/html"),
	}

	// Write with a partial trailing line (no newline) to verify partial line is deferred.
	partial := `{"ts":1700000000,"status":200,"request":{"host":"xeed.ink"`
	content := strings.Join(lines, "\n") + "\n" + partial
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ing := NewIngestor(cfg, st, nil)
	if err := ing.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	dr, err := ResolveDateRange("7d", "", "", loc, now)
	if err != nil {
		t.Fatal(err)
	}

	ov, err := st.QueryOverview(cfg, dr, now)
	if err != nil {
		t.Fatal(err)
	}

	// Today global DAU = 2 (User 1 + User 2, deduplicated across blog & zp)
	if ov.Summary.TodayDAU != 2 {
		t.Fatalf("today global DAU = %d, want 2", ov.Summary.TodayDAU)
	}
	if ov.Summary.YesterdayDAU != 1 {
		t.Fatalf("yesterday global DAU = %d, want 1", ov.Summary.YesterdayDAU)
	}
	// WAU = 2 distinct users across yesterday + today
	if ov.Summary.WAU != 2 {
		t.Fatalf("WAU = %d, want 2", ov.Summary.WAU)
	}
	// Today PV = 3 (blog: /posts/hello, /about, /) + 1 (zp: /) = 4
	if ov.Summary.TodayPV != 4 {
		t.Fatalf("today PV = %d, want 4", ov.Summary.TodayPV)
	}
	// Range PV = 4 today + 1 yesterday = 5
	if ov.Summary.RangePV != 5 {
		t.Fatalf("range PV = %d, want 5", ov.Summary.RangePV)
	}
	if ov.Summary.Err4xx != 1 || ov.Summary.BotHits != 1 {
		t.Fatalf("err4xx=%d botHits=%d, want 1 and 1", ov.Summary.Err4xx, ov.Summary.BotHits)
	}
	if len(ov.Series) != 7 {
		t.Fatalf("series len = %d, want 7 continuous days", len(ov.Series))
	}
	if len(ov.Sites) != 2 {
		t.Fatalf("sites len = %d, want 2", len(ov.Sites))
	}
	if ov.Sites[0].Site != "blog" || ov.Sites[0].TodayDAU != 2 || ov.Sites[0].TodayPV != 3 {
		t.Fatalf("unexpected blog row: %+v", ov.Sites[0])
	}
	if ov.Sites[1].Site != "zp" || ov.Sites[1].TodayDAU != 1 || ov.Sites[1].TodayPV != 1 {
		t.Fatalf("unexpected zp row: %+v", ov.Sites[1])
	}

	// Complete the partial line + add one more hit and verify incremental tailing.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`,"uri":"/completed","remote_ip":"198.51.100.99","method":"GET","headers":{"User-Agent":[` + fmt.Sprintf("%q", uaChromeMac) + `]}}}` + "\n")
	// Note: partial line had ts=1700000000 (2023), let's add a today hit on zp too.
	_, _ = f.WriteString(makeCaddyLine(todayTS+50, "zp.xeed.ink", "/run", "198.51.100.30", uaChromeMac, "", 200, 4000, 0.004, "text/html") + "\n")
	_ = f.Close()

	if err := ing.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	sr, err := st.QuerySiteReport(cfg, cfg.Sites[0], dr, now)
	if err != nil {
		t.Fatal(err)
	}
	if sr.Summary.EngagedRate <= 0 {
		t.Fatalf("expected engaged rate > 0, got %v", sr.Summary.EngagedRate)
	}
	if len(sr.Hourly) != 24 {
		t.Fatalf("hourly len = %d, want 24", len(sr.Hourly))
	}

	// Dimension tables: paths, referrers, browsers, error_path.
	paths, err := st.QueryDimension("blog", "path", dr, "", "pv", "desc", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Total != 3 || paths.Rows[0].Key != "/" || paths.Rows[0].PV != 2 {
		t.Fatalf("unexpected path dimension: %+v", paths)
	}

	refs, err := st.QueryDimension("blog", "referrer", dr, "", "pv", "desc", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if refs.Total != 3 {
		t.Fatalf("expected 3 referrers ((direct), github.com, news.ycombinator.com), got %+v", refs.Rows)
	}

	errs, err := st.QueryDimension("blog", "error_path", dr, "missing", "requests", "desc", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if errs.Total != 1 || errs.Rows[0].Key != "404 /missing-page" {
		t.Fatalf("unexpected error_path dim: %+v", errs)
	}

	bots, err := st.QueryDimension("blog", "bot", dr, "", "requests", "desc", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bots.Total != 1 || bots.Rows[0].Key != "Googlebot (Search)" || bots.Rows[0].Requests != 1 {
		t.Fatalf("unexpected bot dim: %+v", bots)
	}
	botPaths, err := st.QueryDimension("blog", "bot_path", dr, "", "requests", "desc", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if botPaths.Total != 1 || botPaths.Rows[0].Key != "/posts/hello" {
		t.Fatalf("unexpected bot_path dim: %+v", botPaths)
	}
	if isBot, name := classifyBot("Mozilla/5.0 (compatible; Barkrowler/0.9; +https://babbar.tech)"); !isBot || name != "Barkrowler (SEO)" {
		t.Fatalf("classifyBot Barkrowler got (%v, %q), want (true, Barkrowler (SEO))", isBot, name)
	}
	if isBot, name := classifyBot("Mozilla/5.0 (compatible; DataForSeoBot/1.0; +https://dataforseo.com/)"); !isBot || name != "DataForSeoBot" {
		t.Fatalf("classifyBot custom token got (%v, %q), want (true, DataForSeoBot)", isBot, name)
	}

	// CSV export sanity check.
	var csvBuf bytes.Buffer
	if err := WriteOverviewCSV(&csvBuf, ov); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csvBuf.String(), "blog,https://xeed.ink") {
		t.Fatalf("unexpected csv output: %s", csvBuf.String())
	}

	// Test log rotation/truncation resets offset and ingests new lines.
	rotated := makeCaddyLine(todayTS+60, "zp.xeed.ink", "/rotated", "198.51.100.31", uaChromeMac, "", 200, 1000, 0.002, "text/html") + "\n"
	if err := os.WriteFile(logPath, []byte(rotated), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ing.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	zpRep, err := st.QuerySiteReport(cfg, cfg.Sites[1], dr, now)
	if err != nil {
		t.Fatal(err)
	}
	if zpRep.Summary.TodayPV != 3 {
		t.Fatalf("zp today PV after rotation = %d, want 3", zpRep.Summary.TodayPV)
	}

	// Prune older than 0 days? Retention 1 day keeps today & yesterday, removes 2023.
	if err := st.Prune(1, loc, now); err != nil {
		t.Fatal(err)
	}
	oldDr := DateRange{From: "2023-01-01", To: "2023-12-31", Days: 365}
	oldPaths, err := st.QueryDimension("blog", "path", oldDr, "", "pv", "desc", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if oldPaths.Total != 0 {
		t.Fatalf("expected 2023 records to be pruned, got %+v", oldPaths)
	}
}
