// Package store implements the SQLite-backed persistent deployment queue.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // register the pure-Go "sqlite" database/sql driver
)

// Job statuses.
const (
	StatusQueued     = "queued"
	StatusRunning    = "running"
	StatusSucceeded  = "succeeded"
	StatusFailed     = "failed"
	StatusSuperseded = "superseded"
)

// Trigger types.
const (
	TriggerPush   = "push"
	TriggerManual = "manual"
)

// Job is one queued/executed deployment.
type Job struct {
	ID          int64      `json:"id"`
	Site        string     `json:"site"`
	TriggerType string     `json:"trigger_type"`
	Branch      string     `json:"branch"`
	SHA         string     `json:"sha"`
	DeliveryID  string     `json:"delivery_id,omitempty"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	Error       string     `json:"error,omitempty"`
	LogPath     string     `json:"log_path,omitempty"`
	CheckedSHA  string     `json:"checked_sha,omitempty"` // SHA actually checked out
}

// ErrNotFound is returned when a job does not exist.
var ErrNotFound = errors.New("job not found")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path and
// migrates the schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // single writer; SQLite prefers one connection
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS jobs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	site TEXT NOT NULL,
	trigger_type TEXT NOT NULL,
	branch TEXT NOT NULL,
	sha TEXT NOT NULL,
	delivery_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'queued',
	attempts INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	started_at TEXT,
	finished_at TEXT,
	exit_code INTEGER,
	error TEXT NOT NULL DEFAULT '',
	log_path TEXT NOT NULL DEFAULT '',
	checked_sha TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_jobs_site_status ON jobs(site, status);
CREATE INDEX IF NOT EXISTS idx_jobs_status_id ON jobs(status, id);
CREATE TABLE IF NOT EXISTS deliveries (
	site TEXT NOT NULL,
	delivery_id TEXT NOT NULL,
	job_id INTEGER NOT NULL REFERENCES jobs(id),
	created_at TEXT NOT NULL,
	PRIMARY KEY (site, delivery_id)
);
`
	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}
	// Existing databases get the checked_sha column via ALTER; fresh ones
	// already have it from the schema above.
	if _, err := s.db.Exec(`ALTER TABLE jobs ADD COLUMN checked_sha TEXT NOT NULL DEFAULT ''`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("add checked_sha column: %w", err)
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Raw exposes the underlying *sql.DB for dispatcher housekeeping updates.
func (s *Store) Raw() *sql.DB { return s.db }

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// EnqueuePush atomically dedups the webhook delivery and enqueues a push job,
// superseding older still-queued push jobs for the same site+branch.
// It returns the new job and whether the delivery was new (false = duplicate).
func (s *Store) EnqueuePush(site, deliveryID, branch, sha string) (Job, bool, error) {
	ts := nowUTC()
	var job Job
	dup := false
	err := s.withTx(func(tx *sql.Tx) error {
		var existing int64
		err := tx.QueryRow(`SELECT job_id FROM deliveries WHERE site=? AND delivery_id=?`, site, deliveryID).Scan(&existing)
		switch {
		case err == nil:
			dup = true
			return s.getJobTx(tx, existing, &job)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		res, err := tx.Exec(
			`INSERT INTO jobs (site, trigger_type, branch, sha, delivery_id, status, created_at)
			 VALUES (?,?,?,?,?,?,?)`,
			site, TriggerPush, branch, sha, deliveryID, StatusQueued, ts)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO deliveries (site, delivery_id, job_id, created_at) VALUES (?,?,?,?)`,
			site, deliveryID, id, ts); err != nil {
			return err
		}
		// Supersede older queued push jobs for the same site+branch (never the new one,
		// never running/finished jobs, never manual jobs).
		if _, err := tx.Exec(
			`UPDATE jobs SET status=?, finished_at=?, error=?
			 WHERE site=? AND branch=? AND trigger_type=? AND status=? AND id<>?`,
			StatusSuperseded, ts, "superseded by newer push", site, branch, TriggerPush, StatusQueued, id); err != nil {
			return err
		}
		return s.getJobTx(tx, id, &job)
	})
	if err != nil {
		return Job{}, false, err
	}
	return job, !dup, nil
}

// EnqueueManual enqueues a manually requested deployment.
func (s *Store) EnqueueManual(site, branch, sha string) (Job, error) {
	ts := nowUTC()
	res, err := s.db.Exec(
		`INSERT INTO jobs (site, trigger_type, branch, sha, status, created_at)
		 VALUES (?,?,?,?,?,?)`,
		site, TriggerManual, branch, sha, StatusQueued, ts)
	if err != nil {
		return Job{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Job{}, err
	}
	var job Job
	if err := s.withTx(func(tx *sql.Tx) error { return s.getJobTx(tx, id, &job) }); err != nil {
		return Job{}, err
	}
	return job, nil
}

// ClaimRunnable atomically claims the oldest queued job whose site has no
// running job: sets it running, records started_at and bumps attempts.
// ok=false means nothing is dispatchable right now (queue empty, or every
// queued job's site is busy). The single SQLite writer connection makes the
// site-not-running filter race-free, so per-site serialization never needs
// a claim-then-requeue round trip.
func (s *Store) ClaimRunnable() (job Job, ok bool, err error) {
	err = s.withTx(func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRow(`
SELECT id FROM jobs
WHERE status=? AND site NOT IN (SELECT site FROM jobs WHERE status=?)
ORDER BY id LIMIT 1`, StatusQueued, StatusRunning).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ts := nowUTC()
		if _, err := tx.Exec(
			`UPDATE jobs SET status=?, started_at=COALESCE(started_at,?), attempts=attempts+1 WHERE id=?`,
			StatusRunning, ts, id); err != nil {
			return err
		}
		ok = true
		return s.getJobTx(tx, id, &job)
	})
	return job, ok, err
}

// SetStarted records the log path (and started_at if not yet set) of a
// running job, so the log API can serve output while the deploy runs.
func (s *Store) SetStarted(id int64, logPath string) error {
	_, err := s.db.Exec(
		`UPDATE jobs SET log_path=?, started_at=COALESCE(started_at,?) WHERE id=? AND status=?`,
		logPath, nowUTC(), id, StatusRunning)
	return err
}

// UpdateBranch rewrites a job's branch while it is still running. Used to
// normalize an omitted manual branch to the site's configured branch so the
// record reflects what actually shipped.
func (s *Store) UpdateBranch(id int64, branch string) error {
	_, err := s.db.Exec(`UPDATE jobs SET branch=? WHERE id=? AND status=?`, branch, id, StatusRunning)
	return err
}

// SetFinishedIfRunning records the terminal outcome of a running job with
// its real exit code and the SHA actually checked out. It is a
// compare-and-set on status='running': if the job's fate was already
// decided elsewhere (e.g. superseded while it ran), the store keeps that
// decision and returns updated=false instead of overwriting it.
func (s *Store) SetFinishedIfRunning(id int64, status string, exitCode *int, errMsg, checkedSHA string) (updated bool, err error) {
	if status != StatusSucceeded && status != StatusFailed {
		return false, fmt.Errorf("invalid terminal status %q", status)
	}
	res, err := s.db.Exec(
		`UPDATE jobs SET status=?, finished_at=?, exit_code=?, error=?, checked_sha=? WHERE id=? AND status=?`,
		status, nowUTC(), exitCode, errMsg, checkedSHA, id, StatusRunning)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RecoverRunning marks every running job failed (used after restart/shutdown).
func (s *Store) RecoverRunning(errMsg string) (int64, error) {
	res, err := s.db.Exec(
		`UPDATE jobs SET status=?, finished_at=?, error=? WHERE status=?`,
		StatusFailed, nowUTC(), errMsg, StatusRunning)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetJob returns the job by id.
func (s *Store) GetJob(id int64) (Job, error) {
	var job Job
	err := s.withTx(func(tx *sql.Tx) error { return s.getJobTx(tx, id, &job) })
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return job, err
}

func (s *Store) getJobTx(tx *sql.Tx, id int64, job *Job) error {
	var createdAt, startedAt, finishedAt sql.NullString
	var exitCode sql.NullInt64
	var deliveryID, errMsg, logPath sql.NullString
	err := tx.QueryRow(`
SELECT id, site, trigger_type, branch, sha, delivery_id, status, attempts,
       created_at, started_at, finished_at, exit_code, error, log_path, checked_sha
FROM jobs WHERE id=?`, id).Scan(
		&job.ID, &job.Site, &job.TriggerType, &job.Branch, &job.SHA, &deliveryID, &job.Status,
		&job.Attempts, &createdAt, &startedAt, &finishedAt, &exitCode, &errMsg, &logPath, &job.CheckedSHA)
	if err != nil {
		return err
	}
	job.CreatedAt = parseTime(createdAt.String)
	job.DeliveryID = deliveryID.String
	job.Error = errMsg.String
	job.LogPath = logPath.String
	if startedAt.Valid {
		t := parseTime(startedAt.String)
		job.StartedAt = &t
	}
	if finishedAt.Valid {
		t := parseTime(finishedAt.String)
		job.FinishedAt = &t
	}
	if exitCode.Valid {
		c := int(exitCode.Int64)
		job.ExitCode = &c
	}
	return nil
}

// ListJobs returns jobs filtered by optional site/status, newest first.
func (s *Store) ListJobs(site, status string, limit, offset int) ([]Job, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	where, args := buildFilter(site, status)
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT id, site, trigger_type, branch, sha, status, attempts, created_at,
	             started_at, finished_at, exit_code, error, log_path, checked_sha
	      FROM jobs` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		var j Job
		var createdAt, startedAt, finishedAt sql.NullString
		var exitCode sql.NullInt64
		if err := rows.Scan(&j.ID, &j.Site, &j.TriggerType, &j.Branch, &j.SHA, &j.Status,
			&j.Attempts, &createdAt, &startedAt, &finishedAt, &exitCode, &j.Error, &j.LogPath, &j.CheckedSHA); err != nil {
			return nil, 0, err
		}
		j.CreatedAt = parseTime(createdAt.String)
		if startedAt.Valid {
			t := parseTime(startedAt.String)
			j.StartedAt = &t
		}
		if finishedAt.Valid {
			t := parseTime(finishedAt.String)
			j.FinishedAt = &t
		}
		if exitCode.Valid {
			c := int(exitCode.Int64)
			j.ExitCode = &c
		}
		jobs = append(jobs, j)
	}
	return jobs, total, rows.Err()
}

func buildFilter(site, status string) (string, []any) {
	var conds []string
	var args []any
	if site != "" {
		conds = append(conds, "site=?")
		args = append(args, site)
	}
	if status != "" {
		conds = append(conds, "status=?")
		args = append(args, status)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// QueueStats returns the number of queued and running jobs.
func (s *Store) QueueStats() (queued, running int, err error) {
	err = s.db.QueryRow(
		`SELECT
		   COALESCE(SUM(CASE WHEN status='queued' THEN 1 ELSE 0 END),0),
		   COALESCE(SUM(CASE WHEN status='running' THEN 1 ELSE 0 END),0)
		 FROM jobs`).Scan(&queued, &running)
	return
}

// HasQueued reports whether any queued job exists.
func (s *Store) HasQueued() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status=?`, StatusQueued).Scan(&n)
	return n > 0, err
}

func (s *Store) withTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
