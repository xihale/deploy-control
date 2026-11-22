package config

import (
	"path/filepath"
	"strings"
	"testing"
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
