package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deploycontrol/internal/config"
	"deploycontrol/internal/runner"
	"deploycontrol/internal/store"
)

// fakeDeployer records claim order and concurrency per site and globally,
// and can be told to sleep, block until released, fail or panic per job.
// All state is mutex-guarded: test goroutines configure jobs while workers
// read the configuration.
type fakeDeployer struct {
	mu        sync.Mutex
	calls     []int64            // job IDs in claim (start) order
	siteCalls map[string][]int64 // per-site claim order
	siteInFlt map[string]int64
	siteMax   map[string]int64

	sleep  map[int64]time.Duration
	block  map[int64]chan struct{}
	fail   map[int64]error
	panics map[int64]bool

	inFlight  atomic.Int64
	maxGlobal atomic.Int64
}

func newFake() *fakeDeployer {
	return &fakeDeployer{
		siteCalls: map[string][]int64{},
		siteInFlt: map[string]int64{},
		siteMax:   map[string]int64{},
		sleep:     map[int64]time.Duration{},
		block:     map[int64]chan struct{}{},
		fail:      map[int64]error{},
		panics:    map[int64]bool{},
	}
}

func (f *fakeDeployer) setSleep(id int64, d time.Duration) {
	f.mu.Lock()
	f.sleep[id] = d
	f.mu.Unlock()
}

func (f *fakeDeployer) setBlock(id int64, ch chan struct{}) {
	f.mu.Lock()
	f.block[id] = ch
	f.mu.Unlock()
}

func (f *fakeDeployer) setFail(id int64, err error) {
	f.mu.Lock()
	f.fail[id] = err
	f.mu.Unlock()
}

func (f *fakeDeployer) setPanics(id int64) {
	f.mu.Lock()
	f.panics[id] = true
	f.mu.Unlock()
}

func (f *fakeDeployer) Deploy(ctx context.Context, site config.Site, branch, sha string, jobID int64) (runner.Result, error) {
	// Record the claim (start) FIRST: tests wait for a blocked job to show
	// up as claimed before releasing its block.
	f.mu.Lock()
	f.calls = append(f.calls, jobID)
	sleep := f.sleep[jobID]
	block := f.block[jobID]
	panics := f.panics[jobID]
	failErr := f.fail[jobID]
	f.siteInFlt[site.Name]++
	if f.siteInFlt[site.Name] > f.siteMax[site.Name] {
		f.siteMax[site.Name] = f.siteInFlt[site.Name]
	}
	f.siteCalls[site.Name] = append(f.siteCalls[site.Name], jobID)
	f.mu.Unlock()

	g := f.inFlight.Add(1)
	for {
		m := f.maxGlobal.Load()
		if g <= m || f.maxGlobal.CompareAndSwap(m, g) {
			break
		}
	}
	defer func() {
		f.mu.Lock()
		f.siteInFlt[site.Name]--
		f.mu.Unlock()
		f.inFlight.Add(-1)
	}()

	if sleep > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(sleep):
		}
	}
	if block != nil {
		select {
		case <-ctx.Done():
		case <-block:
		}
	}

	if panics {
		panic("simulated deploy panic")
	}
	if failErr != nil {
		return runner.Result{ExitCode: 3}, failErr
	}
	return runner.Result{ExitCode: 0, CheckedSHA: fmt.Sprintf("%040x", jobID)}, nil
}

func (f *fakeDeployer) claimed() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.calls...)
}

func (f *fakeDeployer) siteClaimOrder(site string) []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.siteCalls[site]...)
}

func (f *fakeDeployer) maxSiteInFlight(site string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.siteMax[site]
}

func waitFor(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func testConfig(workers int, sites ...string) *config.Config {
	cfg := &config.Config{GlobalConcurrency: workers, DeployTimeoutSec: 30}
	for _, s := range sites {
		cfg.Sites = append(cfg.Sites, config.Site{Name: s, Branch: "main"})
	}
	return cfg
}

func newTest(t *testing.T, workers int, fd *fakeDeployer, sites ...string) (*Dispatcher, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	d := New(testConfig(workers, sites...), st, fd)
	d.LogDir = t.TempDir()
	d.Poll = 20 * time.Millisecond
	return d, st
}

func enqueueN(t *testing.T, st *store.Store, site string, n int, sleep time.Duration, fd *fakeDeployer) []int64 {
	t.Helper()
	var ids []int64
	for range n {
		j, err := st.EnqueueManual(site, "", fmt.Sprintf("%040x", time.Now().UnixNano()))
		if err != nil {
			t.Errorf("enqueue: %v", err)
			return nil
		}
		if sleep > 0 {
			fd.setSleep(j.ID, sleep)
		}
		ids = append(ids, j.ID)
	}
	return ids
}

func allTerminal(st *store.Store) func() bool {
	return func() bool {
		queued, running, err := st.QueueStats()
		return err == nil && queued == 0 && running == 0
	}
}

// TestGlobalConcurrencyCap: the number of simultaneously running deploys
// never exceeds global_concurrency, and the pool actually parallelizes.
func TestGlobalConcurrencyCap(t *testing.T) {
	fd := newFake()
	sites := []string{"s0", "s1", "s2", "s3", "s4", "s5"}
	d, st := newTest(t, 3, fd, sites...)
	d.Start(context.Background())
	defer d.Stop()

	for _, s := range sites {
		enqueueN(t, st, s, 2, 40*time.Millisecond, fd)
	}
	d.Wake()
	waitFor(t, "all jobs terminal", allTerminal(st))

	if max := fd.maxGlobal.Load(); max > 3 {
		t.Fatalf("global concurrency exceeded: %d concurrent deploys (cap 3)", max)
	} else if max < 2 {
		t.Fatalf("pool never parallelized: max in-flight %d, want >1", max)
	}
	if got := len(fd.claimed()); got != 12 {
		t.Fatalf("ran %d jobs, want 12", got)
	}
}

// TestPerSiteSerialization: a site never runs two deploys at once, even
// with free global slots; jobs of the same site run in enqueue order.
func TestPerSiteSerialization(t *testing.T) {
	fd := newFake()
	d, st := newTest(t, 4, fd, "a", "b")
	d.Start(context.Background())
	defer d.Stop()

	// Interleave enqueue order so claims must skip site-blocked jobs.
	for range 4 {
		enqueueN(t, st, "a", 1, 25*time.Millisecond, fd)
		enqueueN(t, st, "b", 1, 25*time.Millisecond, fd)
	}
	d.Wake()
	waitFor(t, "all jobs terminal", allTerminal(st))

	if m := fd.maxSiteInFlight("a"); m != 1 {
		t.Fatalf("site a ran %d deploys concurrently, want exactly 1", m)
	}
	if m := fd.maxSiteInFlight("b"); m != 1 {
		t.Fatalf("site b ran %d deploys concurrently, want exactly 1", m)
	}
	if max := fd.maxGlobal.Load(); max > 4 || max < 2 {
		t.Fatalf("global in-flight %d, want 2..4", max)
	}
	assertAscending(t, "a", fd.siteClaimOrder("a"))
	assertAscending(t, "b", fd.siteClaimOrder("b"))
}

// TestBusySiteNoStarvation: while site a runs, jobs for site b must still
// dispatch, and a job for a enqueued mid-run must not be lost.
func TestBusySiteNoStarvation(t *testing.T) {
	fd := newFake()
	d, st := newTest(t, 1, fd, "a", "b")
	d.Start(context.Background())
	defer d.Stop()

	a1 := enqueueN(t, st, "a", 1, 120*time.Millisecond, fd)
	enqueueN(t, st, "b", 3, 5*time.Millisecond, fd)
	go func() {
		time.Sleep(30 * time.Millisecond)
		enqueueN(t, st, "a", 1, 5*time.Millisecond, fd)
		d.Wake()
	}()
	d.Wake()
	waitFor(t, "all jobs terminal", allTerminal(st))

	aOrder := fd.siteClaimOrder("a")
	if len(aOrder) != 2 || aOrder[0] != a1[0] {
		t.Fatalf("site a claim order %v, want [%d ...]", aOrder, a1[0])
	}
	assertAscending(t, "a", aOrder)
	assertAscending(t, "b", fd.siteClaimOrder("b"))
}

// TestWakeWithoutPoll: with the safety poll effectively disabled, a job
// enqueued while the pool is idle must still start via Wake alone.
func TestWakeWithoutPoll(t *testing.T) {
	fd := newFake()
	d, st := newTest(t, 1, fd, "a")
	d.Poll = 10 * time.Second // longer than the test deadline: wake or bust
	d.Start(context.Background())
	defer d.Stop()

	time.Sleep(50 * time.Millisecond) // workers now parked in select
	enqueueN(t, st, "a", 3, 5*time.Millisecond, fd)
	d.Wake()
	waitFor(t, "jobs terminal via wake", allTerminal(st))
}

// TestSupersededQueuedJobNeverRuns: a newer push supersedes an older
// still-queued push, which must never execute.
func TestSupersededQueuedJobNeverRuns(t *testing.T) {
	fd := newFake()
	d, st := newTest(t, 1, fd, "a")
	sha1, sha2 := fmt.Sprintf("%040x", 1), fmt.Sprintf("%040x", 2)
	j1, isNew, err := st.EnqueuePush("a", "d1", "main", sha1)
	if err != nil || !isNew {
		t.Fatalf("enqueue push 1: isNew=%v err=%v", isNew, err)
	}
	j2, isNew, err := st.EnqueuePush("a", "d2", "main", sha2)
	if err != nil || !isNew {
		t.Fatalf("enqueue push 2: isNew=%v err=%v", isNew, err)
	}
	d.Start(context.Background())
	defer d.Stop()

	waitFor(t, "job2 terminal", func() bool {
		j, err := st.GetJob(j2.ID)
		return err == nil && j.FinishedAt != nil
	})
	got, err := st.GetJob(j1.ID)
	if err != nil {
		t.Fatalf("get job1: %v", err)
	}
	if got.Status != store.StatusSuperseded {
		t.Fatalf("job1 status %q, want superseded", got.Status)
	}
	for _, id := range fd.claimed() {
		if id == j1.ID {
			t.Fatalf("superseded job %d was executed", j1.ID)
		}
	}
	got2, _ := st.GetJob(j2.ID)
	if got2.Status != store.StatusSucceeded {
		t.Fatalf("job2 status %q, want succeeded", got2.Status)
	}
	if got2.CheckedSHA != sha2 {
		t.Fatalf("job2 checked_sha %q, want %q", got2.CheckedSHA, sha2)
	}
}

// TestRunningJobBeatsSupersede: supersede targets only queued jobs; a job
// already running must complete and record its REAL result (CAS finalize).
func TestRunningJobBeatsSupersede(t *testing.T) {
	fd := newFake()
	d, st := newTest(t, 1, fd, "a")
	sha1 := fmt.Sprintf("%040x", 1)
	j1, _, err := st.EnqueuePush("a", "d1", "main", sha1)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	block := make(chan struct{})
	fd.setBlock(j1.ID, block)

	d.Start(context.Background())
	defer d.Stop()

	waitFor(t, "job1 claimed", func() bool { return len(fd.claimed()) == 1 })
	if j, _ := st.GetJob(j1.ID); j.Status != store.StatusRunning {
		t.Fatalf("job1 status %q while blocked, want running", j.Status)
	}
	// Newer push for the same site+branch arrives mid-run: it supersedes
	// nothing (job1 is running) and queues behind it.
	if _, _, err := st.EnqueuePush("a", "d2", "main", fmt.Sprintf("%040x", 2)); err != nil {
		t.Fatalf("enqueue push 2: %v", err)
	}
	if j, _ := st.GetJob(j1.ID); j.Status != store.StatusRunning {
		t.Fatalf("running job1 was clobbered to %q by concurrent push", j.Status)
	}
	close(block)
	waitFor(t, "all jobs terminal", allTerminal(st))

	got, _ := st.GetJob(j1.ID)
	if got.Status != store.StatusSucceeded {
		t.Fatalf("job1 final status %q, want succeeded (real result must win)", got.Status)
	}
	if got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("job1 exit code %v, want 0", got.ExitCode)
	}
	if got.CheckedSHA != sha1 {
		t.Fatalf("job1 checked_sha %q, want %q", got.CheckedSHA, sha1)
	}
}

// TestSetFinishedCASOnce: direct store-level check of the compare-and-set
// semantics the dispatcher relies on to avoid clobbering a decided fate.
func TestSetFinishedCASOnce(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	j, err := st.EnqueueManual("a", "main", fmt.Sprintf("%040x", 1))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, ok, err := st.ClaimRunnable(); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	updated, err := st.SetFinishedIfRunning(j.ID, store.StatusSucceeded, new(int), "", fmt.Sprintf("%040x", 1))
	if err != nil || !updated {
		t.Fatalf("first finalize: updated=%v err=%v", updated, err)
	}
	// A second (late) writer must NOT overwrite the decided fate.
	updated, err = st.SetFinishedIfRunning(j.ID, store.StatusFailed, new(int), "late failure", "")
	if err != nil {
		t.Fatalf("second finalize: %v", err)
	}
	if updated {
		t.Fatal("second finalize overwritten a terminal job")
	}
	got, _ := st.GetJob(j.ID)
	if got.Status != store.StatusSucceeded || got.Error != "" {
		t.Fatalf("job mutated by late writer: status=%q error=%q", got.Status, got.Error)
	}
	// Finalizing a job that is not running must fail the CAS too.
	j2, _ := st.EnqueueManual("a", "main", fmt.Sprintf("%040x", 2))
	if updated, _ := st.SetFinishedIfRunning(j2.ID, store.StatusFailed, nil, "x", ""); updated {
		t.Fatal("finalized a queued job")
	}
}

// TestRestartRecovery: a running row found at Start (crash window) must be
// marked failed with a clear message, never silently resumed.
func TestRestartRecovery(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	j, err := st.EnqueueManual("a", "main", fmt.Sprintf("%040x", 1))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// Simulate a crash mid-run: claim leaves the row running.
	if _, ok, err := st.ClaimRunnable(); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}

	fd := newFake()
	d := New(testConfig(1, "a"), st, fd)
	d.Poll = 20 * time.Millisecond
	d.Start(context.Background())
	defer d.Stop()

	waitFor(t, "running row recovered", func() bool {
		got, err := st.GetJob(j.ID)
		return err == nil && got.Status == store.StatusFailed && got.FinishedAt != nil
	})
	got, _ := st.GetJob(j.ID)
	if !contains(got.Error, "interrupted by restart") {
		t.Fatalf("recovery error %q, want mention of restart", got.Error)
	}
}

// TestShutdownInterruptsRun: Stop must cancel the in-flight deploy and the
// job must end failed with a clear shutdown error.
func TestShutdownInterruptsRun(t *testing.T) {
	fd := newFake()
	d, st := newTest(t, 1, fd, "a")
	j, err := st.EnqueueManual("a", "main", fmt.Sprintf("%040x", 1))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	block := make(chan struct{})
	fd.setBlock(j.ID, block)

	d.Start(context.Background())
	waitFor(t, "job claimed", func() bool { return len(fd.claimed()) == 1 })
	d.Stop() // must not hang: ctx cancellation unblocks the fake deployer

	got, _ := st.GetJob(j.ID)
	if got.Status != store.StatusFailed {
		t.Fatalf("status after shutdown %q, want failed", got.Status)
	}
	if !contains(got.Error, "interrupted by shutdown") {
		t.Fatalf("shutdown error %q", got.Error)
	}
	close(block)
}

// TestManualBranchNormalized: a manual deploy with empty branch must run
// with (and record) the site's configured branch.
func TestManualBranchNormalized(t *testing.T) {
	fd := newFake()
	d, st := newTest(t, 1, fd, "a")
	j, err := st.EnqueueManual("a", "", fmt.Sprintf("%040x", 1))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	d.Start(context.Background())
	defer d.Stop()
	waitFor(t, "job terminal", func() bool {
		got, _ := st.GetJob(j.ID)
		return got.FinishedAt != nil
	})
	got, _ := st.GetJob(j.ID)
	if got.Status != store.StatusSucceeded {
		t.Fatalf("status %q, want succeeded", got.Status)
	}
	if got.Branch != "main" {
		t.Fatalf("recorded branch %q, want normalized \"main\"", got.Branch)
	}
}

// TestLogPathDuringRun: log_path must be persisted while the job runs so
// the log API can stream partial output, and must survive completion.
func TestLogPathDuringRun(t *testing.T) {
	fd := newFake()
	d, st := newTest(t, 1, fd, "a")
	j, err := st.EnqueueManual("a", "main", fmt.Sprintf("%040x", 1))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	block := make(chan struct{})
	fd.setBlock(j.ID, block)

	d.Start(context.Background())
	defer d.Stop()
	waitFor(t, "job claimed", func() bool { return len(fd.claimed()) == 1 })

	var mid store.Job
	waitFor(t, "log_path persisted mid-run", func() bool {
		mid, _ = st.GetJob(j.ID)
		return mid.LogPath != ""
	})
	if filepath.Base(mid.LogPath) != fmt.Sprintf("job-%d.log", j.ID) {
		t.Fatalf("mid-run log path %q", mid.LogPath)
	}
	if _, err := os.Stat(mid.LogPath); err != nil {
		t.Fatalf("log file not readable mid-run: %v", err)
	}
	close(block)
	waitFor(t, "job terminal", func() bool {
		got, _ := st.GetJob(j.ID)
		return got.FinishedAt != nil
	})
	if got, _ := st.GetJob(j.ID); got.LogPath == "" {
		t.Fatal("log_path lost after completion")
	}
}

// TestPanicAndFailureRecorded: a panicking deploy becomes a clean job
// failure and must not wedge the dispatcher for subsequent jobs; a failing
// deploy records the real exit code and error.
func TestPanicAndFailureRecorded(t *testing.T) {
	fd := newFake()
	d, st := newTest(t, 2, fd, "a", "b")
	j1, err := st.EnqueueManual("a", "main", fmt.Sprintf("%040x", 1))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	fd.setPanics(j1.ID)
	j2, _ := st.EnqueueManual("b", "main", fmt.Sprintf("%040x", 2))
	fd.setFail(j2.ID, errors.New("boom"))
	j3, _ := st.EnqueueManual("a", "main", fmt.Sprintf("%040x", 3))

	d.Start(context.Background())
	defer d.Stop()
	waitFor(t, "all jobs terminal", allTerminal(st))

	got1, _ := st.GetJob(j1.ID)
	if got1.Status != store.StatusFailed || !contains(got1.Error, "internal panic") {
		t.Fatalf("panicked job: status=%q error=%q", got1.Status, got1.Error)
	}
	got2, _ := st.GetJob(j2.ID)
	if got2.Status != store.StatusFailed || !contains(got2.Error, "boom") || got2.ExitCode == nil || *got2.ExitCode != 3 {
		t.Fatalf("failed job: status=%q error=%q exit=%v", got2.Status, got2.Error, got2.ExitCode)
	}
	got3, _ := st.GetJob(j3.ID)
	if got3.Status != store.StatusSucceeded {
		t.Fatalf("job after panic: status=%q, want succeeded (dispatcher must survive)", got3.Status)
	}
}

func assertAscending(t *testing.T, site string, ids []int64) {
	t.Helper()
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("site %s claim order not ascending: %v", site, ids)
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
