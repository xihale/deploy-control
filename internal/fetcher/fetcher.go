// Package fetcher implements the unified git fetch policy:
// attempt 1 always forced direct, attempts 2-5 over HTTPS through the proxy,
// SSH remotes always direct. Never reports success without git exit 0.
//
// Every git operation runs as the site's run_as CI user (sudo -n -H -u)
// with a scrubbed environment, so worktrees contain no root-owned files and
// the environment cannot smuggle proxy settings past the explicit policy.
package fetcher

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultProxy is the unified proxy endpoint for HTTPS fetches.
const DefaultProxy = "http://127.0.0.1:7654"

// Attempts is the fixed attempt count: 1 direct + 4 proxied-or-direct.
const Attempts = 5

// Result summarizes every attempt of a fetch/clone.
type Result struct {
	OK       bool
	Attempts []string // one human-readable line per attempt
}

// scrubEnv returns a copy of environ with every proxy variable removed,
// in both upper and lower case, so environment proxies cannot leak into
// forced-direct attempts or contradict the explicit policy.
func scrubEnv(environ []string) []string {
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

// Fetcher executes git operations under the unified fetch policy.
type Fetcher struct {
	Proxy string // empty = no proxy attempts, all direct
	Log   func(format string, args ...any)
}

func (f *Fetcher) logf(format string, args ...any) {
	if f.Log != nil {
		f.Log(format, args...)
	}
}

// attemptArgs builds the git argv for attempt i (0-based) given the remote URL.
// The -c config overrides go BEFORE the subcommand: git accepts them only as
// global options, and appending after the subcommand is a usage error.
// Attempt 0 is forced direct: proxy vars scrubbed from env AND empty -c values
// override any repo/global git-config http.proxy / https.proxy.
// Attempts 1-4 on HTTPS remotes add explicit -c http.proxy/-c https.proxy;
// SSH remotes always run direct (SSH is not proxied; we say so, never claim it).
func (f *Fetcher) attemptArgs(remoteURL string, attempt int, base []string) (args []string, env []string, desc string) {
	env = scrubEnv(os.Environ())
	isHTTPS := strings.HasPrefix(remoteURL, "https://") || strings.HasPrefix(remoteURL, "http://")
	args = []string{"git"}
	if attempt == 0 {
		args = append(args, "-c", "http.proxy=", "-c", "https.proxy=")
		args = append(args, base...)
		return args, env, "direct"
	}
	if isHTTPS && f.Proxy != "" {
		args = append(args, "-c", "http.proxy="+f.Proxy, "-c", "https.proxy="+f.Proxy)
		args = append(args, base...)
		return args, env, "proxy " + f.Proxy
	}
	args = append(args, "-c", "http.proxy=", "-c", "https.proxy=")
	args = append(args, base...)
	return args, env, "direct"
}

// run tries the git command under the attempt policy; returns the result with
// per-attempt descriptions. err is non-nil when no attempt exited 0.
// Every attempt is logged via f.logf regardless of outcome.
func (f *Fetcher) run(ctx context.Context, dir, runAs, remoteURL string, base []string) (*Result, error) {
	res := &Result{}
	var lastErr string
	for attempt := range Attempts {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		args, env, desc := f.attemptArgs(remoteURL, attempt, base)
		start := time.Now()
		attemptCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		output, err := runGitFn(attemptCtx, dir, runAs, args, env)
		cancel()
		dur := time.Since(start).Round(time.Millisecond)
		line := fmt.Sprintf("attempt %d/%d (%s): %s", attempt+1, Attempts, desc, summarize(output, err, dur))
		res.Attempts = append(res.Attempts, line)
		f.logf("fetch: site_user=%s dir=%s cmd=%s %s", runAsOrSelf(runAs), dir, strings.Join(base, " "), line)
		if err == nil {
			res.OK = true
			return res, nil
		}
		lastErr = fmt.Sprintf("%s (exit %v)", tail(output, 400), exitCodeOf(err))
		if attempt < Attempts-1 {
			sleepFn(backoff(attempt))
		}
	}
	return res, fmt.Errorf("git %s failed after %d attempts; last error: %s",
		strings.Join(base, " "), Attempts, lastErr)
}

// runAsOrSelf renders the effective user for logs: runAs, or "self" when the
// operation runs as the service's own user.
func runAsOrSelf(runAs string) string {
	if runAs == "" {
		return "self"
	}
	return runAs
}

// Clone clones remoteURL into dir under the unified policy, running git as
// runAs. Work happens in a sibling staging dir that is merged into place only
// on success: a timed-out or interrupted clone must not leave a partial
// directory behind, which would poison every remaining attempt with "already
// exists and is not an empty directory". The runner usually pre-creates dir
// itself and hands ownership to runAs, so the parent is typically NOT
// writable by runAs — the service creates staging and passes it to runAs.
func (f *Fetcher) Clone(ctx context.Context, remoteURL, dir, runAs string) (*Result, error) {
	tmp := dir + ".clone-staging"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, fmt.Errorf("create clone staging: %w", err)
	}
	if runAs != "" {
		if u, err := user.Lookup(runAs); err == nil {
			if uid, e1 := strconv.Atoi(u.Uid); e1 == nil {
				if gid, e2 := strconv.Atoi(u.Gid); e2 == nil {
					_ = os.Chown(tmp, uid, gid)
				}
			}
		}
	}
	res, err := f.run(ctx, "", runAs, remoteURL, []string{"clone", remoteURL, tmp})
	if err != nil {
		_ = os.RemoveAll(tmp)
		return res, err
	}
	if err := moveTree(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		return res, fmt.Errorf("move clone into place: %w", err)
	}
	return res, nil
}

// moveTree renames tmp onto dir when dir does not exist yet; otherwise it
// merges entries one by one, clearing same-named leftovers first (partial
// junk from a previously interrupted clone — the runner refuses pre-existing
// non-repo directories itself, so anything here is ours to replace).
func moveTree(tmp, dir string) error {
	if err := os.Rename(tmp, dir); err == nil {
		return nil
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	for _, e := range entries {
		src := filepath.Join(tmp, e.Name())
		dst := filepath.Join(dir, e.Name())
		_ = os.RemoveAll(dst)
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return os.Remove(tmp)
}

// Fetch runs git fetch --prune origin in the repo at dir under the policy,
// running git as runAs.
func (f *Fetcher) Fetch(ctx context.Context, dir, remoteURL, runAs string) (*Result, error) {
	return f.run(ctx, dir, runAs, remoteURL, []string{"fetch", "--prune", "origin"})
}

// CheckoutLocal runs plain local git commands (no network) as runAs in dir.
// Local checkout/cat-file/rev-parse do not contact the remote, so they are
// outside the proxy policy but still run as the CI user.
func (f *Fetcher) CheckoutLocal(ctx context.Context, dir, runAs string, args ...string) (string, error) {
	argv := append([]string{"git"}, args...)
	out, err := runGitFn(ctx, dir, runAs, argv, scrubEnv(os.Environ()))
	f.logf("fetch: site_user=%s dir=%s cmd=%s exit_desc=%s output=%s",
		runAsOrSelf(runAs), dir, strings.Join(args, " "), outcome(err), tail(out, 200))
	return out, err
}

func outcome(err error) string {
	if err == nil {
		return "ok"
	}
	return "failed"
}

func backoff(attempt int) time.Duration {
	switch attempt {
	case 0:
		return 2 * time.Second
	case 1:
		return 4 * time.Second
	case 2:
		return 8 * time.Second
	default:
		return 15 * time.Second
	}
}

// remoteIsSSH reports whether the remote uses SSH transport.
func remoteIsSSH(remoteURL string) bool {
	return strings.HasPrefix(remoteURL, "ssh://") ||
		strings.Contains(remoteURL, "git@") ||
		strings.HasPrefix(remoteURL, "git://")
}

// DescribeRemote returns an honest transport description for logging.
func (f *Fetcher) DescribeRemote(remoteURL string) string {
	if remoteIsSSH(remoteURL) {
		return "ssh: direct retries only (no proxy)"
	}
	if f.Proxy == "" {
		return "https: direct retries only (proxy disabled)"
	}
	return "https: 1 direct + 4 via " + f.Proxy
}
