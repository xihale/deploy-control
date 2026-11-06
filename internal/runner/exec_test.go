package runner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"deploycontrol/internal/config"
	"deploycontrol/internal/fetcher"
)

func TestScrubProxy(t *testing.T) {
	env := []string{
		"HTTP_PROXY=http://a:1", "http_proxy=http://a:2",
		"FTP_PROXY=http://f", "no_proxy=x", "NO_PROXY=y",
		"PATH=/bin", "HOME=/root",
	}
	got := scrubProxy(env)
	if len(got) != 2 || got[0] != "PATH=/bin" || got[1] != "HOME=/root" {
		t.Fatalf("scrubProxy = %v, want PATH and HOME only", got)
	}
}

func TestUserArgvSudoEnvWrap(t *testing.T) {
	env := []string{"PATH=/bin", "DEPLOY_COMMIT=abc", "DEPLOY_SITE=t", "HOME=/root"}
	got := userArgv("blog-ci", []string{"/site/scripts/release.sh"}, env)
	// Only the DEPLOY_* contract vars are forwarded through env(1); sudo's
	// env_reset + -H supply the CI user's HOME and a secure PATH.
	want := []string{"sudo", "-n", "-H", "-u", "blog-ci", "env", "DEPLOY_COMMIT=abc", "DEPLOY_SITE=t", "/site/scripts/release.sh"}
	equalArgv(t, got, want)

	got = userArgv("", []string{"/site/scripts/release.sh"}, env)
	equalArgv(t, got, []string{"/site/scripts/release.sh"})
}

func equalArgv(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %v, want %v", got, want)
		}
	}
}

func TestBranchCheckoutArgsNoPull(t *testing.T) {
	args := branchCheckoutArgs("main")
	joined := strings.Join(args, " ")
	if args[0] != "checkout" {
		t.Fatalf("args = %v, want checkout first", args)
	}
	if !contains(args, "-f") || !contains(args, "-B") {
		t.Fatalf("args = %v, want -f and -B", args)
	}
	if args[len(args)-1] != "origin/main" {
		t.Fatalf("args = %v, want tracking origin/main last", args)
	}
	if strings.Contains(joined, "pull") {
		t.Fatalf("branch path must not use git pull (bypasses unified policy): %s", joined)
	}
}

func contains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

func TestFindLegacyMarkers(t *testing.T) {
	legacy := "flock /home/blog-ci/.deploy.lock || { touch .deploy-rerun; exit 0; }\nsha=$BLOG_PUSH_SHA\n"
	hits := findLegacyMarkers(legacy)
	if len(hits) != 3 {
		t.Fatalf("hits = %v, want .deploy.lock, .deploy-rerun, BLOG_PUSH_SHA", hits)
	}
	if hits := findLegacyMarkers("#!/bin/sh\nnpm run build\nrsync out/ /srv/site/\n"); len(hits) != 0 {
		t.Fatalf("clean script flagged: %v", hits)
	}
}

func TestCheckScriptContractRejectsLegacyScript(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy.sh")
	if err := os.WriteFile(legacy, []byte("flock /home/x/.deploy.lock; ZP_PUSH_SHA=$1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := checkScriptContract(legacy)
	if err == nil {
		t.Fatal("legacy script must be rejected")
	}
	if !strings.Contains(err.Error(), "pre-migration deploy logic") ||
		!strings.Contains(err.Error(), "admin review is required") ||
		!strings.Contains(err.Error(), "build and release only") {
		t.Fatalf("error must state known markers and admin review, got: %v", err)
	}

	clean := filepath.Join(dir, "clean.sh")
	if err := os.WriteFile(clean, []byte("#!/bin/sh\nnpm run build\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkScriptContract(clean); err != nil {
		t.Fatalf("migrated build script must pass: %v", err)
	}
}

// failingScriptRunner builds a Runner whose script dir/logdir live in tmp.
func runScriptForTest(t *testing.T, ctx context.Context, scriptBody string) (int, error, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "job.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	r := &Runner{Fetch: &fetcher.Fetcher{Proxy: "http://127.0.0.1:7654"}}
	site := config.Site{Name: "t", Branch: "main"}
	code, runErr := r.runScript(ctx, site, script, dir, "main", "abc123", logFile, 7)
	data, _ := os.ReadFile(logPath)
	return code, runErr, string(data)
}

func TestRunScriptFailureVisible(t *testing.T) {
	code, runErr, log := runScriptForTest(t, context.Background(),
		"#!/bin/sh\necho BUILD_MARKER_OUTPUT\nexit 3\n")
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	if runErr == nil {
		t.Fatal("failing script must return an error")
	}
	if !strings.Contains(runErr.Error(), "script exited 3") {
		t.Fatalf("error must carry the real exit code, got: %v", runErr)
	}
	// The full output lives in the job log; Deploy's error wrapper adds the
	// log path, so the failure is always reachable.
	if !strings.Contains(log, "BUILD_MARKER_OUTPUT") || !strings.Contains(log, "exit: 3") {
		t.Fatalf("job log must record output and exit code, got: %s", log)
	}
}

func TestRunScriptTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	// The script spawns a grandchild; the deploy timeout must kill the
	// whole process group, not only the script process.
	body := "#!/bin/sh\nsleep 30 & echo $! > " + pidFile + "\ntrap 'kill $!' TERM\nwait $!\n"
	script := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "job.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := &Runner{Fetch: &fetcher.Fetcher{}}
	code, runErr := r.runScript(ctx, config.Site{Name: "t"}, script, dir, "main", "abc", logFile, 1)
	if runErr == nil {
		t.Fatal("timed-out script must return an error")
	}
	if code != -1 {
		t.Fatalf("killed script exit code = %d, want -1", code)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("runScript took %v to abort; process group was not killed promptly", elapsed)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("grandchild did not write pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("bad pid file %q: %v", data, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild pid %d still alive after group kill", pid)
		}
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
