package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// runGitFn and sleepFn are indirection seams so tests can stub process
// execution and backoff without a real git/sudo installation.
var (
	runGitFn = runGit
	sleepFn  = time.Sleep
)

// runGit executes one git invocation (argv[0] is the git binary) with the
// given environment, optionally as runAs, and returns combined stdout+stderr.
//
// The child gets its own process group (Setpgid) and context cancellation
// (the deploy timeout) SIGKILLs the WHOLE group, so a timed-out fetch cannot
// leave orphaned git/ssh/helper processes behind.
func runGit(ctx context.Context, dir, runAs string, argv, env []string) (string, error) {
	argv = withUser(runAs, argv)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid targets the whole process group; ESRCH (already
		// dead) is fine, this is best-effort before WaitDelay kicks in.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// withUser prefixes argv so it runs as runAs: sudo -n never prompts (fails
// closed when no tty/passwordless grant), -H sets HOME to the CI user's home
// so git reads that user's config, and sudo's env_reset drops the caller's
// environment so only the explicit env list reaches the child. runAs "" runs
// as the service's own user.
func withUser(runAs string, argv []string) []string {
	if runAs == "" {
		return argv
	}
	return append([]string{"sudo", "-n", "-H", "-u", runAs}, argv...)
}

// summarize renders one attempt line.
func summarize(output string, err error, dur any) string {
	status := "ok"
	if err != nil {
		status = "failed"
	}
	return fmt.Sprintf("%s in %v; %s", status, dur, tail(output, 200))
}

// tail returns the last n characters of s, normalized.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// exitCodeOf extracts the process exit code from an exec error.
func exitCodeOf(err error) string {
	var ee *exec.ExitError
	if e, ok := err.(*exec.ExitError); ok {
		ee = e
		return fmt.Sprintf("%d", ee.ExitCode())
	}
	return "?"
}
