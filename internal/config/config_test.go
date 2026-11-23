package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidSHARequiresExactlyHexObjectID(t *testing.T) {
	for _, s := range []string{"0123456789012345678901234567890123456789", "0123456789012345678901234567890123456789012345678901234567890123"} {
		if !ValidSHA(s) {
			t.Fatalf("valid object ID rejected: %q", s)
		}
		if ValidSHA(s+"x") || ValidSHA("x"+s) {
			t.Fatalf("accepted malformed object ID around %q", s)
		}
	}
}

func TestValidBranchRejectsUnsafeArguments(t *testing.T) {
	for _, s := range []string{"-u", "../main", "/main", "main/", "a//b", "a..b"} {
		if ValidBranch(s) {
			t.Fatalf("accepted unsafe branch %q", s)
		}
	}
	for _, s := range []string{"main", "feature/example"} {
		if !ValidBranch(s) {
			t.Fatalf("rejected valid branch %q", s)
		}
	}
}

func TestDefaultDatabaseLivesBesideConfig(t *testing.T) {
	dir := t.TempDir()
	c := Config{Users: []User{{Name: "admin", TokenHash: strings.Repeat("a", 64), Role: RoleAdmin}}, Sites: []Site{{Name: "site", Repo: "/tmp/repo", Branch: "main", Script: "/tmp/deploy"}}}
	if err := c.validate(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if got, want := c.DBFile(), filepath.Join(dir, "deploy.db"); got != want {
		t.Fatalf("database = %q, want %q", got, want)
	}
}

func TestWebDirResolvesRelativeToConfig(t *testing.T) {
	dir := t.TempDir()
	base := Config{Users: []User{{Name: "admin", TokenHash: strings.Repeat("a", 64), Role: RoleAdmin}}, Sites: []Site{{Name: "site", Repo: "/tmp/repo", Branch: "main", Script: "/tmp/deploy"}}}

	off := base
	if err := off.validate(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if got := off.WebRoot(); got != "" {
		t.Fatalf("web root = %q, want empty when web_dir is unset", got)
	}

	on := base
	on.WebDir = "web/out"
	if err := on.validate(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if got, want := on.WebRoot(), filepath.Join(dir, "web", "out"); got != want {
		t.Fatalf("web root = %q, want %q", got, want)
	}

	abs := base
	abs.WebDir = filepath.Join(dir, "dist")
	if err := abs.validate(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if got, want := abs.WebRoot(), filepath.Join(dir, "dist"); got != want {
		t.Fatalf("absolute web root = %q, want %q", got, want)
	}
}

func TestSiteRefRegexValidation(t *testing.T) {
	dir := t.TempDir()
	valid := Config{
		Users: []User{{Name: "admin", TokenHash: strings.Repeat("a", 64), Role: RoleAdmin}},
		Sites: []Site{{
			Name:     "upix-web",
			Repo:     "/tmp/repo",
			Branch:   "main",
			RefRegex: `^refs/tags/web-v[0-9]+\.[0-9]+\.[0-9]+$`,
			Script:   "/tmp/deploy",
		}},
	}
	if err := valid.validate(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("valid ref_regex failed: %v", err)
	}
	re, err := valid.Sites[0].RefPattern()
	if err != nil || re == nil || !re.MatchString("refs/tags/web-v0.2.0") || re.MatchString("refs/tags/web-v0.2.0-beta.1") {
		t.Fatalf("compiled ref_regex mismatch: re=%v err=%v", re, err)
	}

	bad := valid
	bad.Sites = []Site{{
		Name:     "upix-web",
		Repo:     "/tmp/repo",
		Branch:   "main",
		RefRegex: `[unclosed`,
		Script:   "/tmp/deploy",
	}}
	if err := bad.validate(filepath.Join(dir, "config.yaml")); err == nil {
		t.Fatal("invalid ref_regex must fail validation")
	}
}

func TestSiteURLValidation(t *testing.T) {
	dir := t.TempDir()
	baseSite := Site{
		Name:   "blog",
		Repo:   "/tmp/repo",
		Branch: "main",
		Script: "/tmp/deploy",
	}

	for _, raw := range []string{
		"",
		"https://blog.example.com",
		"http://blog.example.com",
		"https://example.com/sub/path",
		"http://localhost:8080",
	} {
		cfg := Config{
			Users: []User{{Name: "admin", TokenHash: strings.Repeat("a", 64), Role: RoleAdmin}},
			Sites: []Site{baseSite},
		}
		cfg.Sites[0].URL = raw
		if err := cfg.validate(filepath.Join(dir, "config.yaml")); err != nil {
			t.Fatalf("expected url %q to be valid, got: %v", raw, err)
		}
	}

	for _, raw := range []string{
		"javascript:alert(1)",
		"https://",
		"http://",
		"ftp://example.com",
		"file:///etc/passwd",
		"not-a-url",
		"://bad",
	} {
		cfg := Config{
			Users: []User{{Name: "admin", TokenHash: strings.Repeat("a", 64), Role: RoleAdmin}},
			Sites: []Site{baseSite},
		}
		cfg.Sites[0].URL = raw
		if err := cfg.validate(filepath.Join(dir, "config.yaml")); err == nil {
			t.Fatalf("expected url %q to be rejected, but validation succeeded", raw)
		}
	}
}

func TestSiteEventsValidation(t *testing.T) {
	dir := t.TempDir()
	baseSite := Site{
		Name:   "mintcal",
		Repo:   "https://github.com/xihale/goke",
		Branch: "main",
		Script: "/tmp/deploy",
	}

	if !baseSite.AllowsEvent("push") || baseSite.AllowsEvent("release") {
		t.Fatalf("default events should allow push only")
	}

	cfg := Config{
		Users: []User{{Name: "admin", TokenHash: strings.Repeat("a", 64), Role: RoleAdmin}},
		Sites: []Site{baseSite},
	}
	cfg.Sites[0].Events = []string{" Push ", "RELEASE"}
	if err := cfg.validate(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("expected push+release events to be valid, got: %v", err)
	}
	if !cfg.Sites[0].AllowsEvent("push") || !cfg.Sites[0].AllowsEvent("release") || cfg.Sites[0].AllowsEvent("ping") {
		t.Fatalf("normalized events did not match expectations: %v", cfg.Sites[0].Events)
	}

	for _, bad := range [][]string{{"issues"}, {"push", "push"}, {""}} {
		badCfg := Config{
			Users: []User{{Name: "admin", TokenHash: strings.Repeat("a", 64), Role: RoleAdmin}},
			Sites: []Site{baseSite},
		}
		badCfg.Sites[0].Events = bad
		if err := badCfg.validate(filepath.Join(dir, "config.yaml")); err == nil {
			t.Fatalf("expected events %v to be rejected", bad)
		}
	}
}

func TestAnalyticsConfigAndSiteHosts(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Users: []User{{Name: "admin", TokenHash: strings.Repeat("a", 64), Role: RoleAdmin}},
		Sites: []Site{{
			Name:   "blog",
			Repo:   "/tmp/repo",
			Branch: "main",
			URL:    "https://xeed.ink",
			Hosts:  []string{"www.xeed.ink:443", "xeed.ink"},
			Script: "/tmp/deploy",
		}},
		Analytics: Analytics{
			Enabled:    true,
			CaddyLogs:  []string{"logs/access.log", "/var/log/caddy/*.log"},
			Timezone:   "Asia/Shanghai",
			ExcludeIPs: []string{"127.0.0.1", "10.0.0.0/8"},
		},
	}
	if err := cfg.validate(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("unexpected analytics config error: %v", err)
	}
	if got, want := cfg.AnalyticsDBFile(), filepath.Join(dir, "analytics.db"); got != want {
		t.Fatalf("analytics db = %q, want %q", got, want)
	}
	if !cfg.Analytics.ShouldIgnoreBots() {
		t.Fatalf("ignore_bots should default to true")
	}
	if got := cfg.Analytics.Timezone; got != "Asia/Shanghai" {
		t.Fatalf("timezone = %q, want Asia/Shanghai", got)
	}
	if got := cfg.Sites[0].AnalyticsHosts(); len(got) != 2 || got[0] != "xeed.ink" || got[1] != "www.xeed.ink" {
		t.Fatalf("unexpected analytics hosts: %v", got)
	}
	if got := cfg.AnalyticsLogPaths(); len(got) != 2 || got[0] != filepath.Join(dir, "logs", "access.log") || got[1] != "/var/log/caddy/*.log" {
		t.Fatalf("unexpected log paths: %v", got)
	}

	badTZ := cfg
	badTZ.Analytics.Timezone = "Invalid/Zone"
	if err := badTZ.validate(filepath.Join(dir, "config.yaml")); err == nil {
		t.Fatal("expected invalid timezone to fail")
	}

	badIP := cfg
	badIP.Analytics.ExcludeIPs = []string{"not-an-ip"}
	if err := badIP.validate(filepath.Join(dir, "config.yaml")); err == nil {
		t.Fatal("expected invalid exclude_ips to fail")
	}

	shieldCfg := cfg
	shieldCfg.Analytics.Shield = Shield{
		Enabled:      true,
		CaddyBanFile: "bans.caddy",
		AllowIPs:     []string{"203.0.113.0/24"},
	}
	if err := shieldCfg.validate(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("expected valid shield config, got: %v", err)
	}
	if got, want := shieldCfg.ShieldBanFile(), filepath.Join(dir, "bans.caddy"); got != want {
		t.Fatalf("shield ban file = %q, want %q", got, want)
	}
	if got := shieldCfg.ShieldBanTTL(); got != 168*time.Hour {
		t.Fatalf("default shield ban ttl = %v, want 168h", got)
	}
	if got := shieldCfg.ShieldMaxBans(); got != 5000 {
		t.Fatalf("default shield max bans = %d, want 5000", got)
	}
	if got := shieldCfg.ShieldSubnetThreshold(); got != 5 {
		t.Fatalf("default shield subnet threshold = %d, want 5", got)
	}
	if got := shieldCfg.ShieldReloadCooldown(); got != 15*time.Second {
		t.Fatalf("default shield reload cooldown = %v, want 15s", got)
	}

	badAllow := shieldCfg
	badAllow.Analytics.Shield.AllowIPs = []string{"bad-cidr"}
	if err := badAllow.validate(filepath.Join(dir, "config.yaml")); err == nil {
		t.Fatal("expected invalid shield allow_ips to fail")
	}

	badCap := shieldCfg
	badCap.Analytics.Shield.MaxBans = -1
	if err := badCap.validate(filepath.Join(dir, "config.yaml")); err == nil {
		t.Fatal("expected negative max_bans to fail")
	}
}
