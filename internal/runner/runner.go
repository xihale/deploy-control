// Package runner executes deployment scripts after the control service
// itself fetches and checks out the exact commit.
//
// Post-migration script contract (MIGRATION.md): migrated site scripts run
// the site's checks, build, and release ONLY. The service owns fetch and
// checkout — scripts must not fetch, checkout, re-exec, or use the legacy
// lock/rerun protocol — and the deployment result is the script's real exit
// code. Every git operation (clone/fetch/checkout/rev-parse) runs as the
// site's run_as CI user through the unified fetch policy, and the resolved
// SHA is recorded in Result.CheckedSHA for the caller.
//
// The legacy-marker scan below is a guard rail, not compliance verification:
// matching text can prove nothing about a script's actual behavior or side
// effects. Whether a script satisfies the contract is decided by admin
// review at migration time; the service only refuses known pre-migration
// signatures whose exit 0 is not proof of release (flock-conflict
// "touch rerun && exit 0", site-specific legacy SHA variables).
package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"deploycontrol/internal/config"
	"deploycontrol/internal/fetcher"
)

// Result is the real outcome of one deployment run.
type Result struct {
	ExitCode   int    // real script exit code (or synthetic -1 on timeout/error)
	CheckedSHA string // SHA actually checked out (resolved via rev-parse, verified)
	LogPath    string
	Duration   time.Duration
}

// legacyScriptMarkers are signatures of pre-migration deploy scripts
// (MIGRATION.md): the legacy lock/rerun protocol and the per-site legacy SHA
// variables. Any hit means the script's exit code is not proof of release.
var legacyScriptMarkers = []string{
	".deploy.lock",
	".deploy-rerun",
	"BLOG_PUSH_SHA",
	"POETRY_PUSH_SHA",
	"SCHED_PUSH_SHA",
	"ZL_PUSH_SHA",
	"ZP_PUSH_SHA",
}

// findLegacyMarkers returns every legacy marker present in script content.
func findLegacyMarkers(content string) []string {
	var hits []string
	for _, m := range legacyScriptMarkers {
		if strings.Contains(content, m) {
			hits = append(hits, m)
		}
	}
	return hits
}

// checkScriptContract rejects scripts that contain KNOWN pre-migration
// deploy signatures (legacyScriptMarkers). It is a best-effort guard rail:
// text scanning cannot verify that a script builds and releases only, nor
// that it is free of side effects — script compliance is established by
// admin review at migration time (MIGRATION.md), not by this check.
func checkScriptContract(script string) error {
	data, err := os.ReadFile(script)
	if err != nil {
		return fmt.Errorf("read script: %w", err)
	}
	if hits := findLegacyMarkers(string(data)); len(hits) > 0 {
		return fmt.Errorf(
			"script %s contains known pre-migration deploy logic %s; the marker scan cannot prove script compliance — admin review is required (contract: after migration scripts build and release only, no fetch/checkout/lock/rerun; see MIGRATION.md)",
			script, strings.Join(hits, ", "))
	}
	return nil
}

// branchCheckoutArgs moves the worktree to the freshly fetched origin/branch
// with purely local git. No `git pull`: pull contacts the remote directly
// and would bypass the unified direct/proxy fetch policy (notably for manual
// branch deploys). The fetch that ran moments earlier went through the
// policy, so only local refs are needed here.
func branchCheckoutArgs(branch string) []string {
	return []string{"checkout", "-f", "-B", branch, "origin/" + branch}
}

// Runner deploys one site.
type Runner struct {
	Cfg    *config.Config
	Fetch  *fetcher.Fetcher
	LogDir string
}

// Deploy fetches, checks out sha (or branch head) as the site's run_as CI
// user and runs the site script. Never fakes success: every failure returns
// an error that names the job log path (persisted by the dispatcher), and
// every exit code is the script's real one.
func (r *Runner) Deploy(ctx context.Context, site config.Site, branch, sha string, jobID int64) (Result, error) {
	start := time.Now()
	res := Result{ExitCode: -1}

	if sha != "" && !config.ValidSHA(sha) {
		return res, fmt.Errorf("invalid sha %q", sha)
	}
	if branch != "" && !config.ValidBranch(branch) {
		return res, fmt.Errorf("invalid branch %q", branch)
	}
	if branch == "" {
		branch = site.Branch
	}

	script, ok := r.Cfg.ScriptPath(site.Name)
	if !ok {
		return res, fmt.Errorf("site %q has no script", site.Name)
	}
	if st, err := os.Stat(script); err != nil {
		return res, fmt.Errorf("script %s: %w", script, err)
	} else if st.IsDir() {
		return res, fmt.Errorf("script %s is a directory", script)
	} else if st.Mode()&0o111 == 0 {
		return res, fmt.Errorf("script %s is not executable", script)
	}
	if err := checkScriptContract(script); err != nil {
		return res, err
	}

	// Open the job log before any fetch so fetch failures are visible via
	// the log API, not only in the store's error text.
	logPath := filepath.Join(r.LogDir, fmt.Sprintf("job-%d.log", jobID))
	logFile, err := r.openJobLog(logPath, site, branch, sha, jobID)
	if err != nil {
		return res, err
	}
	defer logFile.Close()
	res.LogPath = logPath

	fail := func(err error) (Result, error) {
		fmt.Fprintf(logFile, "!! deploy failed: %v\n", err)
		return res, fmt.Errorf("%w (log %s)", err, logPath)
	}

	workDir := filepath.Join(r.Cfg.WorkRoot(), site.Name)
	checked, err := r.prepareWorktree(ctx, site, workDir, branch, sha, logFile)
	if err != nil {
		return fail(fmt.Errorf("prepare worktree: %w", err))
	}
	res.CheckedSHA = checked

	// Re-verify the tree really is at the resolved SHA before running the
	// script: nothing that ran since checkout may have moved HEAD.
	verify, err := r.Fetch.CheckoutLocal(ctx, workDir, site.RunAs, "rev-parse", "HEAD")
	if err != nil {
		return fail(fmt.Errorf("verify checkout: %v: %s", err, strings.TrimSpace(verify)))
	}
	if strings.TrimSpace(verify) != checked {
		return fail(fmt.Errorf("checkout mismatch: recorded %s, tree at %s", checked, strings.TrimSpace(verify)))
	}

	exit, runErr := r.runScript(ctx, site, script, workDir, branch, checked, logFile, jobID)
	res.ExitCode = exit
	res.Duration = time.Since(start)

	if ctx.Err() != nil {
		return fail(fmt.Errorf("deployment timed out after %ds (process group killed)", r.Cfg.DeployTimeoutSec))
	}
	if runErr != nil {
		// runErr carries the real exit code and the script output tail;
		// fail() adds the log path so the failure is always visible.
		return fail(runErr)
	}
	fmt.Fprintf(logFile, "== deploy ok site=%s commit=%s duration=%s\n",
		site.Name, checked, res.Duration.Round(time.Millisecond))
	return res, nil
}

// openJobLog creates the job log and writes the deployment header.
func (r *Runner) openJobLog(logPath string, site config.Site, branch, sha string, jobID int64) (*os.File, error) {
	if err := os.MkdirAll(r.LogDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	f, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create log %s: %w", logPath, err)
	}
	fmt.Fprintf(f, "== deploy site=%s branch=%s requested_commit=%s run_as=%s job=%d remote=%s time=%s\n",
		site.Name, branch, sha, site.RunAs, jobID,
		r.Fetch.DescribeRemote(site.Repo), time.Now().UTC().Format(time.RFC3339))
	return f, nil
}

// logAttempts appends every recorded fetch/clone attempt line to the job log
// so all fetch activity is visible in the persisted per-job log.
func logAttempts(logw io.Writer, kind string, res *fetcher.Result) {
	if logw == nil || res == nil {
		return
	}
	for _, line := range res.Attempts {
		fmt.Fprintf(logw, "== %s %s\n", kind, line)
	}
}

// prepareWorktree ensures workDir is a clone of site.Repo at the exact
// requested revision. All fetches go through the unified policy; all git
// operations run as the site's run_as user; the worktree is owned by that
// user. Every fetch attempt is appended to logw.
func (r *Runner) prepareWorktree(ctx context.Context, site config.Site, workDir, branch, sha string, logw io.Writer) (string, error) {
	runAs := site.RunAs
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		if _, err := os.Stat(workDir); err == nil {
			// Directory exists but is not a repo: refuse rather than reuse junk.
			return "", fmt.Errorf("%s exists but is not a git repository", workDir)
		}
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return "", err
		}
	}
	if runAs != "" {
		// Never traverse a CI-owned tree with privileged ownership changes.
		if err := chownTree(workDir, runAs); err != nil {
			return "", err
		}
	}

	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		res, err := r.Fetch.Clone(ctx, site.Repo, workDir, runAs)
		logAttempts(logw, "clone", res)
		if err != nil {
			return "", fmt.Errorf("clone: %w; attempts: %s", err, strings.Join(res.Attempts, " | "))
		}
	}

	res, err := r.Fetch.Fetch(ctx, workDir, site.Repo, runAs)
	logAttempts(logw, "fetch", res)
	if err != nil {
		return "", fmt.Errorf("fetch: %w; attempts: %s", err, strings.Join(res.Attempts, " | "))
	}

	if sha != "" {
		if out, err := r.Fetch.CheckoutLocal(ctx, workDir, runAs, "cat-file", "-e", sha+"^{commit}"); err != nil {
			return "", fmt.Errorf("sha %s not found after fetch (%s)", sha, strings.TrimSpace(out))
		}
		if out, err := r.Fetch.CheckoutLocal(ctx, workDir, runAs, "checkout", "-f", "--detach", sha); err != nil {
			return "", fmt.Errorf("checkout %s: %v: %s", sha, err, strings.TrimSpace(out))
		}
	} else {
		if out, err := r.Fetch.CheckoutLocal(ctx, workDir, runAs, branchCheckoutArgs(branch)...); err != nil {
			return "", fmt.Errorf("checkout branch %s: %v: %s", branch, err, strings.TrimSpace(out))
		}
	}

	// Resolve the REAL checked-out SHA; the requested name is not trusted
	// to be what is on disk.
	out, err := r.Fetch.CheckoutLocal(ctx, workDir, runAs, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %v: %s", err, strings.TrimSpace(out))
	}
	checked := strings.TrimSpace(out)
	if checked == "" {
		return "", fmt.Errorf("rev-parse HEAD produced no output")
	}
	if sha != "" && checked != sha {
		return "", fmt.Errorf("checkout produced %s, requested %s", checked, sha)
	}
	return checked, nil
}

// chownTree sets ownership only on the worktree root. Its contents belong to
// the CI user and must never be recursively chowned by a privileged process.
func chownTree(root, runAs string) error {
	u, err := user.Lookup(runAs)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("worktree root is not a directory: %s", root)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot verify owner of %s", root)
	}
	if int(st.Uid) == uid {
		return nil
	}
	if int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("unexpected owner of worktree %s", root)
	}
	return os.Lchown(root, uid, gid)
}

// runScript executes the site script with the deployment env contract and
// writes combined output to the job log. The child runs in its own process
// group; the deploy timeout kills the whole group. Returns the real exit
// code.
func (r *Runner) runScript(ctx context.Context, site config.Site, script, workDir, branch, sha string, logFile io.Writer, jobID int64) (int, error) {
	env := scrubProxy(os.Environ())
	env = append(env,
		"DEPLOY_SITE="+site.Name,
		"DEPLOY_BRANCH="+branch,
		"DEPLOY_COMMIT="+sha,
		"DEPLOY_JOB_ID="+strconv.FormatInt(jobID, 10),
		"DEPLOY_FETCH_PROXY="+r.Fetch.Proxy,
	)

	argv := userArgv(site.RunAs, []string{script}, env)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	startProc(cmd)
	cmd.Dir = workDir
	if site.RunAs == "" {
		// sudo path forwards env via env(1) inside argv; direct path sets it.
		cmd.Env = env
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	fmt.Fprintf(logFile, "== script start site=%s branch=%s commit=%s script=%s run_as=%s time=%s\n",
		site.Name, branch, sha, script, site.RunAs, time.Now().UTC().Format(time.RFC3339))
	scriptStart := time.Now()

	err := cmd.Run()
	code := exitCodeOfInt(err)
	fmt.Fprintf(logFile, "== script exit: %s (duration %s)\n",
		exitCodeOf(err), time.Since(scriptStart).Round(time.Millisecond))
	if err != nil {
		return code, fmt.Errorf("script exited %d: %w", code, err)
	}
	return code, nil
}
