package fetcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real-git regression test for atomic Clone: a failed clone must leave no
// staging dir; a successful clone must land in dir (rename or merge) with
// partial junk from previous attempts replaced, and no staging dir left.
func TestCloneAtomicSuccessAndFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Source repo with one commit.
	src := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(src, "f.txt"), []byte("hi"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "init")

	f := &Fetcher{} // empty Proxy = direct only; runAs "" = direct exec
	ctx := context.Background()

	// Failure: nonexistent remote leaves no staging dir; pre-created dir
	// (runner flow) is not touched by the failed attempt.
	dstFail := filepath.Join(t.TempDir(), "work", "site")
	if err := os.MkdirAll(dstFail, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Clone(ctx, src+"/does-not-exist", dstFail, ""); err == nil {
		t.Fatal("clone of missing remote must fail")
	}
	if _, err := os.Stat(dstFail + ".clone-staging"); !os.IsNotExist(err) {
		t.Fatalf("failed clone left staging dir behind")
	}
	if entries, _ := os.ReadDir(dstFail); len(entries) != 0 {
		t.Fatalf("failed clone touched pre-created dir: %v", entries)
	}

	// Success with pre-created dir containing partial junk (interrupted
	// clone): junk must be replaced by the real clone.
	dstOK := filepath.Join(t.TempDir(), "work", "site2")
	if err := os.MkdirAll(filepath.Join(dstOK, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dstOK, ".git", "junk"), []byte("x"), 0o644)
	res, err := f.Clone(ctx, "file://"+src, dstOK, "")
	if err != nil {
		t.Fatalf("clone: %v (attempts %v)", err, res.Attempts)
	}
	if _, err := os.Stat(filepath.Join(dstOK, ".git", "HEAD")); err != nil {
		t.Fatalf("cloned repo incomplete: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dstOK, ".git", "junk")); err == nil {
		t.Fatalf("partial junk survived clone merge: %q", b)
	}
	if _, err := os.Stat(dstOK + ".clone-staging"); !os.IsNotExist(err) {
		t.Fatalf("successful clone left staging dir behind")
	}
	if b, _ := os.ReadFile(filepath.Join(dstOK, "f.txt")); strings.TrimSpace(string(b)) != "hi" {
		t.Fatalf("cloned file wrong: %q", b)
	}

	// Success with no pre-created dir: plain rename path.
	dstRename := filepath.Join(t.TempDir(), "work", "site3")
	if _, err := f.Clone(ctx, "file://"+src, dstRename, ""); err != nil {
		t.Fatalf("clone rename path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstRename, ".git", "HEAD")); err != nil {
		t.Fatalf("rename-path clone incomplete: %v", err)
	}
}
