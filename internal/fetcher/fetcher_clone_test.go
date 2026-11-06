package fetcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real-git regression test for atomic Clone: a failed clone must not leave a
// partial directory (which poisons retries), and a successful clone must
// rename cleanly into place with no temp dir left behind.
func TestCloneAtomicSuccessAndFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if os.Geteuid() == 0 {
		t.Skip("run as root the scrubbed env still works, but keep tests unprivileged")
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

	// Failure: nonexistent remote leaves no clone dir and no temp dir.
	dstFail := filepath.Join(t.TempDir(), "work", "site")
	if _, err := f.Clone(ctx, src+"/does-not-exist", dstFail, ""); err == nil {
		t.Fatal("clone of missing remote must fail")
	}
	if _, err := os.Stat(dstFail); !os.IsNotExist(err) {
		t.Fatalf("failed clone left %s behind", dstFail)
	}
	if _, err := os.Stat(dstFail + ".clone-tmp"); !os.IsNotExist(err) {
		t.Fatalf("failed clone left temp dir behind")
	}

	// Success: rename into place, tree intact, no temp dir.
	dstOK := filepath.Join(t.TempDir(), "work", "site2")
	res, err := f.Clone(ctx, "file://"+src, dstOK, "")
	if err != nil {
		t.Fatalf("clone: %v (attempts %v)", err, res.Attempts)
	}
	if _, err := os.Stat(filepath.Join(dstOK, ".git")); err != nil {
		t.Fatalf("cloned repo missing .git: %v", err)
	}
	if _, err := os.Stat(dstOK + ".clone-tmp"); !os.IsNotExist(err) {
		t.Fatalf("successful clone left temp dir behind")
	}
	if b, _ := os.ReadFile(filepath.Join(dstOK, "f.txt")); strings.TrimSpace(string(b)) != "hi" {
		t.Fatalf("cloned file wrong: %q", b)
	}
}
