package analytics

import (
	"bufio"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"deploycontrol/internal/config"
)

const (
	defaultPollInterval = 2 * time.Second
	maxBatchLines       = 2000
)

// Ingestor tails configured Caddy JSON access logs and persists metrics into Store.
type Ingestor struct {
	cfg     *config.Config
	store   *Store
	matcher *Matcher
	shield  *ShieldManager
	logf    func(string, ...any)

	mu             sync.Mutex
	lastPruneDay   string
	lastShieldSync time.Time
}

// NewIngestor creates a Caddy log ingestor bound to cfg and store.
func NewIngestor(cfg *config.Config, st *Store, logf func(string, ...any)) *Ingestor {
	if logf == nil {
		logf = log.Printf
	}
	return &Ingestor{
		cfg:     cfg,
		store:   st,
		matcher: NewMatcher(cfg),
		logf:    logf,
	}
}

// SetShield attaches a ShieldManager to handle auto-ban threat hits and periodic
// Caddy ban-list synchronization.
func (ing *Ingestor) SetShield(s *ShieldManager) {
	ing.mu.Lock()
	defer ing.mu.Unlock()
	ing.shield = s
}

// Start runs an immediate ingestion pass and polls for new log lines until ctx
// is cancelled.
func (ing *Ingestor) Start(ctx context.Context) {
	if ing.shield != nil {
		if err := ing.shield.Sync(ctx); err != nil && ctx.Err() == nil {
			ing.logf("shield initial sync: %v", err)
		}
		ing.lastShieldSync = time.Now()
	}
	if err := ing.Tick(ctx); err != nil && ctx.Err() == nil {
		ing.logf("analytics initial ingest: %v", err)
	}
	go func() {
		ticker := time.NewTicker(defaultPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := ing.Tick(ctx); err != nil && ctx.Err() == nil {
					ing.logf("analytics ingest: %v", err)
				}
			}
		}
	}()
}

// Tick performs one full catch-up pass over all configured Caddy log files and
// runs daily retention pruning when the local day rolls over.
func (ing *Ingestor) Tick(ctx context.Context) error {
	ing.mu.Lock()
	defer ing.mu.Unlock()

	paths := ing.resolveFiles()
	for _, p := range paths {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := ing.ingestFile(ctx, p); err != nil {
			ing.logf("analytics ingest %s: %v", p, err)
		}
	}

	loc := ing.cfg.AnalyticsLocation()
	today := time.Now().In(loc).Format("2006-01-02")
	if today != ing.lastPruneDay {
		if err := ing.store.Prune(ing.cfg.Analytics.RetentionDays, loc, time.Now()); err != nil {
			return err
		}
		ing.lastPruneDay = today
	}
	if ing.shield != nil {
		if time.Since(ing.lastShieldSync) >= time.Minute {
			if err := ing.shield.Sync(ctx); err != nil && ctx.Err() == nil {
				ing.logf("shield periodic sync: %v", err)
			} else {
				ing.lastShieldSync = time.Now()
			}
		} else if err := ing.shield.FlushPendingReload(ctx); err != nil && ctx.Err() == nil {
			ing.logf("shield deferred reload: %v", err)
		}
	}
	return nil
}

func (ing *Ingestor) resolveFiles() []string {
	seen := make(map[string]bool)
	var out []string
	for _, pattern := range ing.cfg.AnalyticsLogPaths() {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			matches = []string{pattern}
		}
		for _, m := range matches {
			lower := strings.ToLower(m)
			if strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".zst") ||
				strings.HasSuffix(lower, ".bz2") || strings.HasSuffix(lower, ".xz") {
				continue
			}
			clean := filepath.Clean(m)
			if !seen[clean] {
				seen[clean] = true
				out = append(out, clean)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (ing *Ingestor) ingestFile(ctx context.Context, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return nil
	}

	inode := fileInode(info)
	cursor, found, err := ing.store.GetCursor(path)
	if err != nil {
		return err
	}
	offset := cursor.Offset
	if !found || (inode != 0 && cursor.Inode != 0 && cursor.Inode != inode) || info.Size() < offset {
		offset = 0
	}
	if info.Size() == offset {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(f, 64*1024)
	salt := ing.store.Salt()
	ttlSec := int64(ing.cfg.ShieldBanTTL().Seconds())

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		events := make([]Event, 0, 128)
		var threatHits []ThreatHit
		var bytesConsumed int64
		linesRead := 0
		nowUnix := time.Now().Unix()

		for linesRead < maxBatchLines {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				// Only consume complete newline-terminated lines so partial writes at
				// EOF are retried intact on the next tick.
				if line[len(line)-1] != '\n' {
					break
				}
				bytesConsumed += int64(len(line))
				linesRead++
				if ev, ok := ing.matcher.ParseLine(line, salt); ok {
					events = append(events, ev)
					if ing.shield != nil && ev.Threat != "" && ev.ClientIP != "" {
						if ttlSec <= 0 || ev.UnixSec <= 0 || nowUnix-ev.UnixSec < ttlSec {
							threatHits = append(threatHits, ThreatHit{
								IP:     ev.ClientIP,
								Reason: ev.Threat,
								Site:   ev.Site,
								Path:   ev.Path,
								UA:     ev.UserAgent,
								TS:     ev.UnixSec,
							})
						}
					}
				}
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
		}

		if bytesConsumed == 0 {
			break
		}
		offset += bytesConsumed
		cur := &Cursor{
			Path:   path,
			Inode:  inode,
			Offset: offset,
		}
		if err := ing.store.IngestBatch(events, cur); err != nil {
			return err
		}
		if ing.shield != nil && len(threatHits) > 0 {
			if err := ing.shield.RecordAutoBans(ctx, threatHits); err != nil {
				ing.logf("shield auto-ban %s: %v", path, err)
			}
		}
		if linesRead < maxBatchLines {
			break
		}
	}
	return nil
}

func fileInode(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st != nil {
		return st.Ino
	}
	return 0
}
