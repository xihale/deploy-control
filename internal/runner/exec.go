package runner

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// scrubProxy removes every proxy variable (both cases) so scripts see the
// neutral environment; DEPLOY_FETCH_PROXY carries the sanctioned endpoint.
func scrubProxy(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		lk := strings.ToLower(key)
		if strings.HasSuffix(lk, "_proxy") || lk == "no_proxy" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// userArgv forwards only deployment variables across sudo's environment
// reset, preserving the target user's HOME and avoiding exposure of service secrets.
func userArgv(runAs string, argv, env []string) []string {
	if runAs == "" {
		return argv
	}
	full := []string{"sudo", "-n", "-H", "-u", runAs, "env"}
	for _, value := range env {
		if strings.HasPrefix(value, "DEPLOY_") {
			full = append(full, value)
		}
	}
	return append(full, argv...)
}

// startProc gives the child its own process group and makes context
// cancellation (the deploy timeout) SIGKILL the WHOLE group, not just the
// direct child. Build tools that spawn background workers cannot leak
// grandchildren past the timeout.
func startProc(cmd *exec.Cmd) {
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
}

// exitCodeOf renders a printable exit status.
func exitCodeOf(err error) string {
	if err == nil {
		return "0"
	}
	var ee *exec.ExitError
	if e, ok := err.(*exec.ExitError); ok {
		ee = e
		return fmt.Sprintf("%d", ee.ExitCode())
	}
	return "signal/unknown"
}

// exitCodeOfInt returns the numeric exit code; -1 when not a normal exit.
func exitCodeOfInt(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode()
	}
	return -1
}
