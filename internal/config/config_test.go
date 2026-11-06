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
