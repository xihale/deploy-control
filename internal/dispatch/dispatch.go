// Package dispatch runs the persistent deployment queue with a global
// concurrency cap and per-site serialization.
package dispatch

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"deploycontrol/internal/config"
	"deploycontrol/internal/runner"
	"deploycontrol/internal/store"
)

// Deployer executes one deployment. *runner.Runner satisfies it; the
// interface exists so tests can inject a fake deployer.
type Deployer interface {
	Deploy(ctx context.Context, site config.Site, branch, sha string, jobID int64) (runner.Result, error)
}

// Dispatcher consumes queued jobs from the store.
//
// Concurrency model: exactly GlobalConcurrency worker goroutines each run
// ClaimRunnable → Deploy → SetFinishedIfRunning synchronously, so the pool
// size IS the global cap and can never be exceeded — no job ever runs
// outside a worker. ClaimRunnable atomically skips sites that already have
// a running job (single SQLite writer ⇒ race-free), giving per-site
// serialization without claim-then-requeue churn. An older site-blocked job
// is skipped in favor of newer jobs on free sites, so a busy site cannot
// starve the queue; its site frees as soon as its running job finishes,
// which Wake()s the pool (plus a slow safety poll), so no wakeup is lost.
type Dispatcher struct {
	cfg     *config.Config
	st      *store.Store
	runner  Deployer
	workers int

	// LogDir is the directory holding job-<id>.log files. New fills it from
	// cfg.LogDirectory(); tests may override it before Start.
	LogDir string

	// Poll is the fallback wakeup period. New defaults it to 2s; tests may
	// shrink (or grow, to prove wake-driven progress) it before Start.
	Poll time.Duration

	wake   chan struct{}
	wg     sync.WaitGroup
	cancel context.CancelFunc
	ctx    context.Context
}

// New creates a dispatcher. globalConcurrency slots are enforced inside.
func New(cfg *config.Config, st *store.Store, r Deployer) *Dispatcher {
	n := cfg.GlobalConcurrency
	if n < 1 {
		n = 1
	}
	return &Dispatcher{
		cfg:     cfg,
		st:      st,
		runner:  r,
		workers: n,
		LogDir:  cfg.LogDirectory(),
		Poll:    2 * time.Second,
		wake:    make(chan struct{}, 1),
	}
}

// Start launches worker goroutines and marks pre-existing running jobs
// failed (restart recovery), so a crash never leaves ambiguous running rows.
func (d *Dispatcher) Start(parent context.Context) {
	d.ctx, d.cancel = context.WithCancel(parent)
	if n, err := d.st.RecoverRunning("interrupted by restart"); err != nil {
		log.Printf("dispatch: recover running jobs: %v", err)
	} else if n > 0 {
		log.Printf("dispatch: recovered %d running job(s) as failed after restart", n)
	}
	d.wg.Add(d.workers)
	for range d.workers {
		go d.workerLoop()
	}
}

// Wake nudges the worker pool that a new job may be available.
func (d *Dispatcher) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Stop cancels and waits for in-flight deployments.
func (d *Dispatcher) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	d.wg.Wait()
}

func (d *Dispatcher) workerLoop() {
	defer d.wg.Done()
	ticker := time.NewTicker(d.Poll)
	defer ticker.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.wake:
		case <-ticker.C:
			// Safety-net poll: guarantees liveness even if a wake were
			// missed and catches jobs enqueued during a crash window.
		}
		d.drain()
	}
}

// drain claims and runs dispatchable jobs until none remain. It runs on the
// worker goroutine itself, so one worker executes one deploy at a time.
func (d *Dispatcher) drain() {
	for {
		if d.ctx.Err() != nil {
			return
		}
		job, ok, err := d.st.ClaimRunnable()
		if err != nil {
			log.Printf("dispatch: claim: %v", err)
			return
		}
		if !ok {
			// Queue empty or every remaining job's site is busy; the
			// running job's completion (and the poll) re-triggers us.
			return
		}
		d.runJob(job)
		d.Wake() // a site slot freed; other workers may now proceed
	}
}

// runJob executes one claimed job and records the REAL result. Panics are
// recovered and recorded as job failures so a bug in one deploy can neither
// wedge the queue nor leak the site slot.
func (d *Dispatcher) runJob(job store.Job) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("dispatch: job %d panicked: %v", job.ID, p)
			if _, err := d.st.SetFinishedIfRunning(job.ID, store.StatusFailed, nil, "internal panic during deploy", ""); err != nil {
				log.Printf("dispatch: record panic failure for job %d: %v", job.ID, err)
			}
			d.Wake()
		}
	}()

	site, ok := d.cfg.SiteByName(job.Site)
	if !ok {
		d.finish(job.ID, store.StatusFailed, nil, "", fmt.Sprintf("unknown site %q (removed from config?)", job.Site))
		return
	}

	branch := job.Branch
	if branch == "" {
		// Manual deploys may omit the branch: normalize to the site's
		// configured branch so the DB row reflects what actually shipped.
		branch = site.Branch
		if err := d.st.UpdateBranch(job.ID, branch); err != nil {
			log.Printf("dispatch: normalize branch for job %d: %v", job.ID, err)
		}
	}

	// Persist the log path as soon as the job starts and pre-create the file
	// so the log API serves (partial) output while the deploy runs. The
	// runner later truncates and writes this same path.
	logPath := filepath.Join(d.LogDir, fmt.Sprintf("job-%d.log", job.ID))
	if err := d.st.SetStarted(job.ID, logPath); err != nil {
		log.Printf("dispatch: mark started job %d: %v", job.ID, err)
	}
	if d.LogDir != "" {
		if err := os.MkdirAll(d.LogDir, 0o755); err != nil {
			log.Printf("dispatch: create log dir %s: %v", d.LogDir, err)
		} else if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.Close()
		} else {
			log.Printf("dispatch: pre-create log %s: %v", logPath, err)
		}
	}

	// The deploy context derives from the dispatcher context so shutdown
	// cancels in-flight runs; the per-deploy timeout is enforced via the
	// context deadline below.
	ctx, cancel := context.WithTimeout(d.ctx, timeoutFor(d.cfg))
	defer cancel()

	res, err := d.runner.Deploy(ctx, site, branch, job.SHA, job.ID)
	switch {
	case d.ctx.Err() != nil:
		d.finish(job.ID, store.StatusFailed, intPtr(res.ExitCode), res.CheckedSHA, "interrupted by shutdown")
	case ctx.Err() != nil:
		d.finish(job.ID, store.StatusFailed, intPtr(res.ExitCode), res.CheckedSHA,
			fmt.Sprintf("timeout after %ds; checked_out=%s", d.cfg.DeployTimeoutSec, res.CheckedSHA))
	case err != nil:
		d.finish(job.ID, store.StatusFailed, intPtr(res.ExitCode), res.CheckedSHA, err.Error())
	default:
		d.finish(job.ID, store.StatusSucceeded, intPtr(res.ExitCode), res.CheckedSHA, "")
	}
}

// finish records the terminal outcome through the compare-and-set so a
// concurrent supersede decision is never overwritten by a late result.
func (d *Dispatcher) finish(id int64, status string, exitCode *int, checkedSHA, msg string) {
	updated, err := d.st.SetFinishedIfRunning(id, status, exitCode, msg, checkedSHA)
	if err != nil {
		log.Printf("dispatch: finalize job %d: %v", id, err)
		return
	}
	if !updated {
		log.Printf("dispatch: job %d final state already decided elsewhere; kept store value", id)
	}
}

func timeoutFor(cfg *config.Config) time.Duration {
	return time.Duration(cfg.DeployTimeoutSec) * time.Second
}

func intPtr(c int) *int { return &c }
