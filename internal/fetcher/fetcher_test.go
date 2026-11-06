package fetcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestScrubEnvRemovesProxyVars(t *testing.T) {
	env := []string{
		"HTTP_PROXY=http://x:1", "http_proxy=http://x:2",
		"HTTPS_PROXY=http://y:1", "https_proxy=http://y:2",
		"no_proxy=localhost", "NO_PROXY=other",
		"PATH=/usr/bin", "HOME=/root",
	}
	got := scrubEnv(env)
	want := []string{"PATH=/usr/bin", "HOME=/root"}
	if len(got) != len(want) {
		t.Fatalf("scrubEnv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scrubEnv = %v, want %v", got, want)
		}
	}
}

func TestAttemptArgsProxyPolicy(t *testing.T) {
	f := &Fetcher{Proxy: DefaultProxy}
	base := []string{"fetch", "--prune", "origin"}

	// Attempt 0: forced direct — empty -c overrides, and they must come
	// BEFORE the subcommand (git rejects -c after it).
	args, env, desc := f.attemptArgs("https://github.com/x/y", 0, base)
	if desc != "direct" {
		t.Fatalf("attempt 0 desc = %q, want direct", desc)
	}
	if args[0] != "git" || args[1] != "-c" {
		t.Fatalf("attempt 0 argv = %v, want -c overrides right after git", args)
	}
	fetchIdx := -1
	for i, a := range args {
		if a == "fetch" {
			fetchIdx = i
			break
		}
	}
	if fetchIdx < 4 || fetchIdx != len(args)-3 {
		t.Fatalf("attempt 0 argv = %v, want fetch subcommand last", args)
	}
	if !containsPair(args, "http.proxy=") || !containsPair(args, "https.proxy=") {
		t.Fatalf("attempt 0 argv = %v, want empty proxy overrides", args)
	}
	if proxyVarPresent(env) {
		t.Fatalf("attempt 0 env still has proxy vars: %v", env)
	}

	// Attempts 1-4 on HTTPS remotes: explicit proxy override.
	args, _, desc = f.attemptArgs("https://github.com/x/y", 1, base)
	if desc != "proxy "+DefaultProxy {
		t.Fatalf("attempt 1 desc = %q, want proxy %s", desc, DefaultProxy)
	}
	if !containsPair(args, "http.proxy="+DefaultProxy) || !containsPair(args, "https.proxy="+DefaultProxy) {
		t.Fatalf("attempt 1 argv = %v, want explicit proxy -c", args)
	}

	// SSH remotes: never proxied, all attempts direct.
	for i := range Attempts {
		args, _, desc = f.attemptArgs("git@github.com:x/y", i, base)
		if desc != "direct" {
			t.Fatalf("ssh attempt %d desc = %q, want direct", i, desc)
		}
		if containsPair(args, "http.proxy="+DefaultProxy) {
			t.Fatalf("ssh attempt %d argv = %v, must not set proxy", i, args)
		}
	}
}

func containsPair(args []string, val string) bool {
	for i, a := range args {
		if a == val && i > 0 && args[i-1] == "-c" {
			return true
		}
	}
	return false
}

func proxyVarPresent(env []string) bool {
	for _, kv := range env {
		lk := strings.ToLower(strings.SplitN(kv, "=", 2)[0])
		if strings.HasSuffix(lk, "_proxy") || lk == "no_proxy" {
			return true
		}
	}
	return false
}

func TestWithUserSudoPrefix(t *testing.T) {
	got := withUser("blog-ci", []string{"git", "fetch", "--prune", "origin"})
	want := []string{"sudo", "-n", "-H", "-u", "blog-ci", "git", "fetch", "--prune", "origin"}
	equalArgv(t, got, want)

	got = withUser("", []string{"git", "status"})
	equalArgv(t, got, []string{"git", "status"})
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

func TestRunForwardsRunAsAndRecordsEveryAttempt(t *testing.T) {
	origRun, origSleep := runGitFn, sleepFn
	defer func() { runGitFn, sleepFn = origRun, origSleep }()

	var seenDir, seenRunAs string
	calls := 0
	runGitFn = func(_ context.Context, dir, runAs string, argv, _ []string) (string, error) {
		calls++
		seenDir, seenRunAs = dir, runAs
		if argv[0] != "git" {
			t.Errorf("argv[0] = %q, want git", argv[0])
		}
		return "boom: not a git repository", errors.New("exit status 128")
	}
	sleepFn = func(time.Duration) {}

	var logs []string
	f := &Fetcher{Proxy: DefaultProxy, Log: func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}}
	res, err := f.run(context.Background(), "/tmp/repo", "blog-ci", "https://github.com/x/y",
		[]string{"fetch", "--prune", "origin"})
	if err == nil {
		t.Fatal("run with always-failing git must return an error")
	}
	if calls != Attempts {
		t.Fatalf("attempts executed = %d, want %d", calls, Attempts)
	}
	if seenDir != "/tmp/repo" || seenRunAs != "blog-ci" {
		t.Fatalf("runGit got dir=%q runAs=%q, want /tmp/repo blog-ci", seenDir, seenRunAs)
	}
	if res.OK {
		t.Fatal("res.OK must stay false after all attempts fail")
	}
	if len(res.Attempts) != Attempts {
		t.Fatalf("recorded attempts = %d, want %d", len(res.Attempts), Attempts)
	}
	if len(logs) != Attempts {
		t.Fatalf("logged attempts = %d, want %d", len(logs), Attempts)
	}
	for i, l := range logs {
		if !strings.Contains(l, fmt.Sprintf("attempt %d/%d", i+1, Attempts)) ||
			!strings.Contains(l, "site_user=blog-ci") {
			t.Fatalf("log %d = %q, want attempt line with site_user=blog-ci", i, l)
		}
	}

	// First-attempt success records exactly one attempt and logs it.
	runGitFn = func(_ context.Context, _, _ string, _, _ []string) (string, error) {
		return "ok", nil
	}
	logs = nil
	res, err = f.run(context.Background(), "", "me-ci", "ssh://git@host/x/y", []string{"fetch", "origin"})
	if err != nil || !res.OK {
		t.Fatalf("run with succeeding git: err=%v ok=%v", err, res.OK)
	}
	if len(res.Attempts) != 1 || len(logs) != 1 {
		t.Fatalf("successful run: attempts=%d logs=%d, want 1 each", len(res.Attempts), len(logs))
	}
}

func TestRunGitKillsProcessGroupOnTimeout(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := fmt.Sprintf("sleep 30 & echo $! > %s; wait", strconv.Quote(pidFile))

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runGit(ctx, dir, "", []string{"sh", "-c", script}, []string{"PATH=/usr/bin:/bin"})
	if err == nil {
		t.Fatal("timed-out runGit must return an error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("runGit took %v to abort; process group was not killed promptly", elapsed)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child did not write pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("bad pid file %q: %v", data, err)
	}
	// The whole group was SIGKILLed, so the grandchild must be gone.
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
