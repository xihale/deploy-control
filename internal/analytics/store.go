package analytics

import (
	"crypto/rand"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deploycontrol/internal/config"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// Store persists aggregated analytics metrics and ingestion cursors in a
// dedicated SQLite database separate from deploy.db.
type Store struct {
	db   *sql.DB
	salt string
}

// Cursor tracks the byte offset and inode of an ingested log file.
type Cursor struct {
	Path   string
	Inode  uint64
	Offset int64
}

// DateRange describes the inclusive local-date window of a query.
type DateRange struct {
	From string `json:"from"`
	To   string `json:"to"`
	Days int    `json:"days"`
}

// Summary holds aggregate traffic metrics for either all sites or one site.
type Summary struct {
	TodayDAU      int64   `json:"today_dau"`
	YesterdayDAU  int64   `json:"yesterday_dau"`
	WAU           int64   `json:"wau"`
	MAU           int64   `json:"mau"`
	RangeUV       int64   `json:"range_uv"`
	TodayPV       int64   `json:"today_pv"`
	RangePV       int64   `json:"range_pv"`
	RangeRequests int64   `json:"range_requests"`
	RangeBytes    int64   `json:"range_bytes"`
	Err4xx        int64   `json:"err_4xx"`
	Err5xx        int64   `json:"err_5xx"`
	BotHits       int64   `json:"bot_hits"`
	AvgDurationMS float64 `json:"avg_duration_ms"`
	PVPerUV       float64 `json:"pv_per_uv"`
	EngagedRate   float64 `json:"engaged_rate"`
}

// DailyPoint is one day's metrics in a trend chart or daily breakdown table.
type DailyPoint struct {
	Day           string  `json:"day"`
	DAU           int64   `json:"dau"`
	PV            int64   `json:"pv"`
	Requests      int64   `json:"requests"`
	Bytes         int64   `json:"bytes"`
	Err4xx        int64   `json:"err_4xx"`
	Err5xx        int64   `json:"err_5xx"`
	BotHits       int64   `json:"bot_hits"`
	AvgDurationMS float64 `json:"avg_duration_ms"`
	PVPerUV       float64 `json:"pv_per_uv"`
}

// HourlyPoint is one hour's traffic metrics.
type HourlyPoint struct {
	Hour     string `json:"hour"`
	PV       int64  `json:"pv"`
	Requests int64  `json:"requests"`
	Bytes    int64  `json:"bytes"`
	Err4xx   int64  `json:"err_4xx"`
	Err5xx   int64  `json:"err_5xx"`
}

// SiteRow summarizes one site in the cross-site comparison table.
type SiteRow struct {
	Site          string  `json:"site"`
	URL           string  `json:"url,omitempty"`
	Hosts         []string `json:"hosts"`
	TodayDAU      int64   `json:"today_dau"`
	YesterdayDAU  int64   `json:"yesterday_dau"`
	WAU           int64   `json:"wau"`
	MAU           int64   `json:"mau"`
	RangeUV       int64   `json:"range_uv"`
	TodayPV       int64   `json:"today_pv"`
	RangePV       int64   `json:"range_pv"`
	RangeRequests int64   `json:"range_requests"`
	RangeBytes    int64   `json:"range_bytes"`
	Err4xx        int64   `json:"err_4xx"`
	Err5xx        int64   `json:"err_5xx"`
	BotHits       int64   `json:"bot_hits"`
	AvgDurationMS float64 `json:"avg_duration_ms"`
	PVPerUV       float64 `json:"pv_per_uv"`
}

// OverviewResponse powers the global comprehensive analytics page.
type OverviewResponse struct {
	Enabled  bool         `json:"enabled"`
	Timezone string       `json:"timezone"`
	Range    DateRange    `json:"range"`
	Summary  Summary      `json:"summary"`
	Series   []DailyPoint `json:"series"`
	Sites    []SiteRow    `json:"sites"`
}

// SiteReportResponse powers the per-site detailed analytics page.
type SiteReportResponse struct {
	Enabled  bool          `json:"enabled"`
	Timezone string        `json:"timezone"`
	Site     string        `json:"site"`
	URL      string        `json:"url,omitempty"`
	Hosts    []string      `json:"hosts"`
	Range    DateRange     `json:"range"`
	Summary  Summary       `json:"summary"`
	Daily    []DailyPoint  `json:"daily"`
	Hourly   []HourlyPoint `json:"hourly"`
}

// DimensionRow is one row of a paginated dimension table (path, referrer, browser, etc.).
type DimensionRow struct {
	Key        string  `json:"key"`
	PV         int64   `json:"pv"`
	Requests   int64   `json:"requests"`
	Bytes      int64   `json:"bytes"`
	ErrCount   int64   `json:"err_count"`
	AvgMS      float64 `json:"avg_ms"`
	Share      float64 `json:"share"`
}

// DimensionResponse is a paginated, sortable dimension table slice.
type DimensionResponse struct {
	Enabled       bool           `json:"enabled"`
	Site          string         `json:"site"`
	Dim           string         `json:"dim"`
	Range         DateRange      `json:"range"`
	Total         int            `json:"total"`
	TotalPV       int64          `json:"total_pv"`
	TotalRequests int64          `json:"total_requests"`
	TotalBytes    int64          `json:"total_bytes"`
	Rows          []DimensionRow `json:"rows"`
}

// Open opens (or creates) the analytics SQLite database at path.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create analytics db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("open analytics sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying SQLite connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Salt returns the persistent per-database random salt used for visitor hashing.
func (s *Store) Salt() string {
	return s.salt
}

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS analytics_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ingest_cursors (
	path TEXT PRIMARY KEY,
	inode INTEGER NOT NULL,
	offset INTEGER NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS daily_site_stats (
	day TEXT NOT NULL,
	site TEXT NOT NULL,
	pv INTEGER NOT NULL DEFAULT 0,
	requests INTEGER NOT NULL DEFAULT 0,
	bytes INTEGER NOT NULL DEFAULT 0,
	err_4xx INTEGER NOT NULL DEFAULT 0,
	err_5xx INTEGER NOT NULL DEFAULT 0,
	bot_hits INTEGER NOT NULL DEFAULT 0,
	duration_ms_sum REAL NOT NULL DEFAULT 0,
	duration_count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (day, site)
);
CREATE TABLE IF NOT EXISTS hourly_site_stats (
	hour TEXT NOT NULL,
	site TEXT NOT NULL,
	pv INTEGER NOT NULL DEFAULT 0,
	requests INTEGER NOT NULL DEFAULT 0,
	bytes INTEGER NOT NULL DEFAULT 0,
	err_4xx INTEGER NOT NULL DEFAULT 0,
	err_5xx INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (hour, site)
);
CREATE TABLE IF NOT EXISTS daily_visitors (
	day TEXT NOT NULL,
	site TEXT NOT NULL,
	visitor_hash TEXT NOT NULL,
	hits INTEGER NOT NULL DEFAULT 0,
	first_ts INTEGER NOT NULL,
	last_ts INTEGER NOT NULL,
	PRIMARY KEY (day, site, visitor_hash)
);
CREATE INDEX IF NOT EXISTS idx_daily_visitors_site_day ON daily_visitors(site, day);
CREATE INDEX IF NOT EXISTS idx_daily_visitors_day_hash ON daily_visitors(day, visitor_hash);
CREATE TABLE IF NOT EXISTS daily_dim_stats (
	day TEXT NOT NULL,
	site TEXT NOT NULL,
	dim TEXT NOT NULL,
	key TEXT NOT NULL,
	pv INTEGER NOT NULL DEFAULT 0,
	requests INTEGER NOT NULL DEFAULT 0,
	bytes INTEGER NOT NULL DEFAULT 0,
	err_count INTEGER NOT NULL DEFAULT 0,
	duration_ms_sum REAL NOT NULL DEFAULT 0,
	PRIMARY KEY (day, site, dim, key)
);
CREATE INDEX IF NOT EXISTS idx_daily_dim_lookup ON daily_dim_stats(dim, day, site);
CREATE TABLE IF NOT EXISTS banned_ips (
	ip TEXT PRIMARY KEY,
	subnet TEXT NOT NULL DEFAULT '',
	reason TEXT NOT NULL,
	source TEXT NOT NULL,
	site TEXT NOT NULL DEFAULT '',
	sample_path TEXT NOT NULL DEFAULT '',
	sample_ua TEXT NOT NULL DEFAULT '',
	hits INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL,
	last_seen_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_banned_ips_expires ON banned_ips(expires_at);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate analytics schema: %w", err)
	}
	if err := s.ensureBannedIPsSubnetColumn(); err != nil {
		return fmt.Errorf("migrate banned_ips subnet: %w", err)
	}

	var salt string
	err := s.db.QueryRow(`SELECT value FROM analytics_meta WHERE key = 'visitor_salt'`).Scan(&salt)
	if err == sql.ErrNoRows {
		buf := make([]byte, 32)
		if _, rerr := rand.Read(buf); rerr != nil {
			return fmt.Errorf("generate visitor salt: %w", rerr)
		}
		salt = hex.EncodeToString(buf)
		if _, ierr := s.db.Exec(`INSERT INTO analytics_meta(key, value) VALUES ('visitor_salt', ?)`, salt); ierr != nil {
			return fmt.Errorf("persist visitor salt: %w", ierr)
		}
	} else if err != nil {
		return fmt.Errorf("load visitor salt: %w", err)
	}
	s.salt = salt
	return nil
}

// GetCursor returns the saved ingestion cursor for path, if any.
func (s *Store) GetCursor(path string) (Cursor, bool, error) {
	var c Cursor
	err := s.db.QueryRow(`SELECT path, inode, offset FROM ingest_cursors WHERE path = ?`, path).
		Scan(&c.Path, &c.Inode, &c.Offset)
	if err == sql.ErrNoRows {
		return Cursor{Path: path}, false, nil
	}
	if err != nil {
		return Cursor{}, false, err
	}
	return c, true, nil
}

type dailySiteKey struct {
	day, site string
}

type dailySiteAgg struct {
	pv, requests, bytes, err4xx, err5xx, botHits, durCount int64
	durSum                                                 float64
}

type hourlySiteKey struct {
	hour, site string
}

type hourlySiteAgg struct {
	pv, requests, bytes, err4xx, err5xx int64
}

type visitorKey struct {
	day, site, hash string
}

type visitorAgg struct {
	hits    int64
	firstTS int64
	lastTS  int64
}

type dimKey struct {
	day, site, dim, key string
}

type dimAgg struct {
	pv, requests, bytes, errCount int64
	durSum                        float64
}

// IngestBatch pre-aggregates events in memory and commits all table updates plus
// the file cursor atomically in a single SQLite transaction.
func (s *Store) IngestBatch(events []Event, cursor *Cursor) error {
	if len(events) == 0 && cursor == nil {
		return nil
	}

	dailySite := make(map[dailySiteKey]*dailySiteAgg)
	hourlySite := make(map[hourlySiteKey]*hourlySiteAgg)
	visitors := make(map[visitorKey]*visitorAgg)
	dims := make(map[dimKey]*dimAgg)

	addDim := func(day, site, dim, key string, pv, req, bytes, errs int64, dur float64) {
		if key == "" {
			return
		}
		k := dimKey{day: day, site: site, dim: dim, key: key}
		d := dims[k]
		if d == nil {
			d = &dimAgg{}
			dims[k] = d
		}
		d.pv += pv
		d.requests += req
		d.bytes += bytes
		d.errCount += errs
		d.durSum += dur
	}

	for _, ev := range events {
		dsk := dailySiteKey{day: ev.Day, site: ev.Site}
		ds := dailySite[dsk]
		if ds == nil {
			ds = &dailySiteAgg{}
			dailySite[dsk] = ds
		}
		var e4, e5 int64
		if ev.Status >= 400 && ev.Status < 500 {
			e4 = 1
		} else if ev.Status >= 500 {
			e5 = 1
		}
		errInc := e4 + e5

		if ev.IsBot && ev.BotName != "" {
			addDim(ev.Day, ev.Site, "bot", ev.BotName, 0, 1, ev.Bytes, errInc, ev.DurationMS)
			addDim(ev.Day, ev.Site, "bot_path", ev.Path, 0, 1, ev.Bytes, errInc, ev.DurationMS)
		}
		if ev.BotOnly {
			ds.botHits++
			continue
		}

		var pvInc int64
		if ev.IsPV {
			pvInc = 1
		}

		ds.pv += pvInc
		ds.requests++
		ds.bytes += ev.Bytes
		ds.err4xx += e4
		ds.err5xx += e5
		ds.durSum += ev.DurationMS
		ds.durCount++

		hsk := hourlySiteKey{hour: ev.Hour, site: ev.Site}
		hs := hourlySite[hsk]
		if hs == nil {
			hs = &hourlySiteAgg{}
			hourlySite[hsk] = hs
		}
		hs.pv += pvInc
		hs.requests++
		hs.bytes += ev.Bytes
		hs.err4xx += e4
		hs.err5xx += e5

		if ev.IsPV && ev.VisitorHash != "" {
			vk := visitorKey{day: ev.Day, site: ev.Site, hash: ev.VisitorHash}
			va := visitors[vk]
			if va == nil {
				visitors[vk] = &visitorAgg{hits: 1, firstTS: ev.UnixSec, lastTS: ev.UnixSec}
			} else {
				va.hits++
				if ev.UnixSec < va.firstTS {
					va.firstTS = ev.UnixSec
				}
				if ev.UnixSec > va.lastTS {
					va.lastTS = ev.UnixSec
				}
			}
		}

		addDim(ev.Day, ev.Site, "status", ev.StatusCode, pvInc, 1, ev.Bytes, errInc, ev.DurationMS)
		if errInc > 0 {
			errKey := fmt.Sprintf("%d %s", ev.Status, ev.Path)
			if len(errKey) > 256 {
				errKey = errKey[:256]
			}
			addDim(ev.Day, ev.Site, "error_path", errKey, 0, 1, ev.Bytes, errInc, ev.DurationMS)
		}
		if ev.IsPV {
			addDim(ev.Day, ev.Site, "path", ev.Path, 1, 1, ev.Bytes, 0, ev.DurationMS)
			addDim(ev.Day, ev.Site, "referrer", ev.Referrer, 1, 1, ev.Bytes, 0, ev.DurationMS)
			addDim(ev.Day, ev.Site, "browser", ev.Browser, 1, 1, ev.Bytes, 0, ev.DurationMS)
			addDim(ev.Day, ev.Site, "os", ev.OS, 1, 1, ev.Bytes, 0, ev.DurationMS)
			addDim(ev.Day, ev.Site, "device", ev.Device, 1, 1, ev.Bytes, 0, ev.DurationMS)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if len(dailySite) > 0 {
		stmt, err := tx.Prepare(`
INSERT INTO daily_site_stats(day, site, pv, requests, bytes, err_4xx, err_5xx, bot_hits, duration_ms_sum, duration_count)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(day, site) DO UPDATE SET
	pv = pv + excluded.pv,
	requests = requests + excluded.requests,
	bytes = bytes + excluded.bytes,
	err_4xx = err_4xx + excluded.err_4xx,
	err_5xx = err_5xx + excluded.err_5xx,
	bot_hits = bot_hits + excluded.bot_hits,
	duration_ms_sum = duration_ms_sum + excluded.duration_ms_sum,
	duration_count = duration_count + excluded.duration_count`)
		if err != nil {
			return err
		}
		for k, v := range dailySite {
			if _, err := stmt.Exec(k.day, k.site, v.pv, v.requests, v.bytes, v.err4xx, v.err5xx, v.botHits, v.durSum, v.durCount); err != nil {
				_ = stmt.Close()
				return err
			}
		}
		_ = stmt.Close()
	}

	if len(hourlySite) > 0 {
		stmt, err := tx.Prepare(`
INSERT INTO hourly_site_stats(hour, site, pv, requests, bytes, err_4xx, err_5xx)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(hour, site) DO UPDATE SET
	pv = pv + excluded.pv,
	requests = requests + excluded.requests,
	bytes = bytes + excluded.bytes,
	err_4xx = err_4xx + excluded.err_4xx,
	err_5xx = err_5xx + excluded.err_5xx`)
		if err != nil {
			return err
		}
		for k, v := range hourlySite {
			if _, err := stmt.Exec(k.hour, k.site, v.pv, v.requests, v.bytes, v.err4xx, v.err5xx); err != nil {
				_ = stmt.Close()
				return err
			}
		}
		_ = stmt.Close()
	}

	if len(visitors) > 0 {
		stmt, err := tx.Prepare(`
INSERT INTO daily_visitors(day, site, visitor_hash, hits, first_ts, last_ts)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(day, site, visitor_hash) DO UPDATE SET
	hits = hits + excluded.hits,
	first_ts = MIN(first_ts, excluded.first_ts),
	last_ts = MAX(last_ts, excluded.last_ts)`)
		if err != nil {
			return err
		}
		for k, v := range visitors {
			if _, err := stmt.Exec(k.day, k.site, k.hash, v.hits, v.firstTS, v.lastTS); err != nil {
				_ = stmt.Close()
				return err
			}
		}
		_ = stmt.Close()
	}

	if len(dims) > 0 {
		stmt, err := tx.Prepare(`
INSERT INTO daily_dim_stats(day, site, dim, key, pv, requests, bytes, err_count, duration_ms_sum)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(day, site, dim, key) DO UPDATE SET
	pv = pv + excluded.pv,
	requests = requests + excluded.requests,
	bytes = bytes + excluded.bytes,
	err_count = err_count + excluded.err_count,
	duration_ms_sum = duration_ms_sum + excluded.duration_ms_sum`)
		if err != nil {
			return err
		}
		for k, v := range dims {
			if _, err := stmt.Exec(k.day, k.site, k.dim, k.key, v.pv, v.requests, v.bytes, v.errCount, v.durSum); err != nil {
				_ = stmt.Close()
				return err
			}
		}
		_ = stmt.Close()
	}

	if cursor != nil {
		if _, err := tx.Exec(`
INSERT INTO ingest_cursors(path, inode, offset, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	inode = excluded.inode,
	offset = excluded.offset,
	updated_at = excluded.updated_at`,
			cursor.Path, int64(cursor.Inode), cursor.Offset, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Prune deletes analytics records older than retentionDays (relative to now in loc).
func (s *Store) Prune(retentionDays int, loc *time.Location, now time.Time) error {
	if retentionDays <= 0 {
		return nil
	}
	cutoffDay := now.In(loc).AddDate(0, 0, -retentionDays).Format("2006-01-02")
	hourlyDays := retentionDays
	if hourlyDays > 30 {
		hourlyDays = 30
	}
	cutoffHour := now.In(loc).AddDate(0, 0, -hourlyDays).Format("2006-01-02T15")

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, tbl := range []string{"daily_site_stats", "daily_visitors", "daily_dim_stats"} {
		if _, err := tx.Exec(`DELETE FROM `+tbl+` WHERE day < ?`, cutoffDay); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM hourly_site_stats WHERE hour < ?`, cutoffHour); err != nil {
		return err
	}
	return tx.Commit()
}

// ResolveDateRange parses `range` (today|7d|30d|90d) or explicit `from`/`to` YYYY-MM-DD dates.
func ResolveDateRange(rangeName, fromStr, toStr string, loc *time.Location, now time.Time) (DateRange, error) {
	if loc == nil {
		loc = time.UTC
	}
	today := now.In(loc)
	todayDate := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)

	var fromDate, toDate time.Time
	if strings.TrimSpace(fromStr) != "" || strings.TrimSpace(toStr) != "" {
		var err error
		if strings.TrimSpace(toStr) == "" {
			toDate = todayDate
		} else {
			toDate, err = time.ParseInLocation("2006-01-02", strings.TrimSpace(toStr), loc)
			if err != nil {
				return DateRange{}, fmt.Errorf("invalid to date %q", toStr)
			}
		}
		if strings.TrimSpace(fromStr) == "" {
			fromDate = toDate.AddDate(0, 0, -6)
		} else {
			fromDate, err = time.ParseInLocation("2006-01-02", strings.TrimSpace(fromStr), loc)
			if err != nil {
				return DateRange{}, fmt.Errorf("invalid from date %q", fromStr)
			}
		}
	} else {
		toDate = todayDate
		switch strings.ToLower(strings.TrimSpace(rangeName)) {
		case "today", "1d":
			fromDate = todayDate
		case "30d":
			fromDate = todayDate.AddDate(0, 0, -29)
		case "90d":
			fromDate = todayDate.AddDate(0, 0, -89)
		case "", "7d":
			fromDate = todayDate.AddDate(0, 0, -6)
		default:
			return DateRange{}, fmt.Errorf("invalid range %q (want today, 7d, 30d, or 90d)", rangeName)
		}
	}

	if fromDate.After(toDate) {
		return DateRange{}, fmt.Errorf("from date %s is after to date %s", fromDate.Format("2006-01-02"), toDate.Format("2006-01-02"))
	}
	days := int(toDate.Sub(fromDate).Hours()/24) + 1
	if days > 366 {
		return DateRange{}, fmt.Errorf("date range %d days exceeds max 366 days", days)
	}
	return DateRange{
		From: fromDate.Format("2006-01-02"),
		To:   toDate.Format("2006-01-02"),
		Days: days,
	}, nil
}

func enumerateDays(dr DateRange, loc *time.Location) []string {
	start, err := time.ParseInLocation("2006-01-02", dr.From, loc)
	if err != nil {
		return nil
	}
	days := make([]string, 0, dr.Days)
	for i := 0; i < dr.Days; i++ {
		days = append(days, start.AddDate(0, 0, i).Format("2006-01-02"))
	}
	return days
}

func round1(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10) / 10
}

func round2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}

// QueryOverview builds the global comprehensive analytics report across all sites.
func (s *Store) QueryOverview(cfg *config.Config, dr DateRange, now time.Time) (*OverviewResponse, error) {
	loc := cfg.AnalyticsLocation()
	localNow := now.In(loc)
	today := localNow.Format("2006-01-02")
	yesterday := localNow.AddDate(0, 0, -1).Format("2006-01-02")
	wauFrom := localNow.AddDate(0, 0, -6).Format("2006-01-02")
	mauFrom := localNow.AddDate(0, 0, -29).Format("2006-01-02")

	resp := &OverviewResponse{
		Enabled:  true,
		Timezone: cfg.Analytics.Timezone,
		Range:    dr,
		Series:   make([]DailyPoint, 0, dr.Days),
		Sites:    make([]SiteRow, 0, len(cfg.Sites)),
	}

	// 1. Daily aggregate series across all sites.
	dayMap := make(map[string]*DailyPoint, dr.Days)
	for _, d := range enumerateDays(dr, loc) {
		dayMap[d] = &DailyPoint{Day: d}
	}

	rows, err := s.db.Query(`
SELECT day, SUM(pv), SUM(requests), SUM(bytes), SUM(err_4xx), SUM(err_5xx), SUM(bot_hits), SUM(duration_ms_sum), SUM(duration_count)
FROM daily_site_stats
WHERE day >= ? AND day <= ?
GROUP BY day`, dr.From, dr.To)
	if err != nil {
		return nil, err
	}
	var totalDurSum float64
	var totalDurCount int64
	for rows.Next() {
		var day string
		var pv, req, bytes, e4, e5, bots, dCount int64
		var dSum float64
		if err := rows.Scan(&day, &pv, &req, &bytes, &e4, &e5, &bots, &dSum, &dCount); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if pt := dayMap[day]; pt != nil {
			pt.PV = pv
			pt.Requests = req
			pt.Bytes = bytes
			pt.Err4xx = e4
			pt.Err5xx = e5
			pt.BotHits = bots
			if dCount > 0 {
				pt.AvgDurationMS = round1(dSum / float64(dCount))
			}
		}
		resp.Summary.RangePV += pv
		resp.Summary.RangeRequests += req
		resp.Summary.RangeBytes += bytes
		resp.Summary.Err4xx += e4
		resp.Summary.Err5xx += e5
		resp.Summary.BotHits += bots
		totalDurSum += dSum
		totalDurCount += dCount
	}
	_ = rows.Close()
	if totalDurCount > 0 {
		resp.Summary.AvgDurationMS = round1(totalDurSum / float64(totalDurCount))
	}

	// Daily global deduplicated DAU.
	dauRows, err := s.db.Query(`
SELECT day, COUNT(DISTINCT visitor_hash)
FROM daily_visitors
WHERE day >= ? AND day <= ?
GROUP BY day`, dr.From, dr.To)
	if err != nil {
		return nil, err
	}
	for dauRows.Next() {
		var day string
		var dau int64
		if err := dauRows.Scan(&day, &dau); err != nil {
			_ = dauRows.Close()
			return nil, err
		}
		if pt := dayMap[day]; pt != nil {
			pt.DAU = dau
			if dau > 0 {
				pt.PVPerUV = round2(float64(pt.PV) / float64(dau))
			}
		}
	}
	_ = dauRows.Close()

	for _, d := range enumerateDays(dr, loc) {
		resp.Series = append(resp.Series, *dayMap[d])
	}

	// Global deduplicated visitor windows + engaged rate.
	resp.Summary.TodayDAU, _ = s.countDistinctVisitors("", today, today)
	resp.Summary.YesterdayDAU, _ = s.countDistinctVisitors("", yesterday, yesterday)
	resp.Summary.WAU, _ = s.countDistinctVisitors("", wauFrom, today)
	resp.Summary.MAU, _ = s.countDistinctVisitors("", mauFrom, today)
	resp.Summary.RangeUV, _ = s.countDistinctVisitors("", dr.From, dr.To)
	if resp.Summary.RangeUV > 0 {
		resp.Summary.PVPerUV = round2(float64(resp.Summary.RangePV) / float64(resp.Summary.RangeUV))
	}
	resp.Summary.TodayPV, _ = s.sumTodayPV("", today)
	resp.Summary.EngagedRate, _ = s.engagedRate("", dr.From, dr.To)

	// 2. Per-site breakdown table (includes all configured sites, even with zero traffic).
	siteMap := make(map[string]*SiteRow, len(cfg.Sites))
	siteOrder := make([]string, 0, len(cfg.Sites))
	for _, sc := range cfg.Sites {
		hosts := sc.AnalyticsHosts()
		if hosts == nil {
			hosts = []string{}
		}
		siteMap[sc.Name] = &SiteRow{
			Site:  sc.Name,
			URL:   sc.URL,
			Hosts: hosts,
		}
		siteOrder = append(siteOrder, sc.Name)
	}

	sRows, err := s.db.Query(`
SELECT site,
	SUM(CASE WHEN day = ? THEN pv ELSE 0 END),
	SUM(pv), SUM(requests), SUM(bytes), SUM(err_4xx), SUM(err_5xx), SUM(bot_hits),
	SUM(duration_ms_sum), SUM(duration_count)
FROM daily_site_stats
WHERE day >= ? AND day <= ?
GROUP BY site`, today, dr.From, dr.To)
	if err != nil {
		return nil, err
	}
	for sRows.Next() {
		var name string
		var tPV, rPV, req, bytes, e4, e5, bots, dCount int64
		var dSum float64
		if err := sRows.Scan(&name, &tPV, &rPV, &req, &bytes, &e4, &e5, &bots, &dSum, &dCount); err != nil {
			_ = sRows.Close()
			return nil, err
		}
		sr := siteMap[name]
		if sr == nil {
			sr = &SiteRow{Site: name, Hosts: []string{}}
			siteMap[name] = sr
			siteOrder = append(siteOrder, name)
		}
		sr.TodayPV = tPV
		sr.RangePV = rPV
		sr.RangeRequests = req
		sr.RangeBytes = bytes
		sr.Err4xx = e4
		sr.Err5xx = e5
		sr.BotHits = bots
		if dCount > 0 {
			sr.AvgDurationMS = round1(dSum / float64(dCount))
		}
	}
	_ = sRows.Close()

	// Today's PV might be outside the selected range (e.g. historical custom range).
	if today < dr.From || today > dr.To {
		tRows, err := s.db.Query(`SELECT site, pv FROM daily_site_stats WHERE day = ?`, today)
		if err == nil {
			for tRows.Next() {
				var name string
				var pv int64
				if err := tRows.Scan(&name, &pv); err == nil {
					if sr := siteMap[name]; sr != nil {
						sr.TodayPV = pv
					}
				}
			}
			_ = tRows.Close()
		}
	}

	// Per-site visitor counts across windows.
	fillSiteVisitors := func(from, to string, assign func(sr *SiteRow, n int64)) error {
		vRows, err := s.db.Query(`
SELECT site, COUNT(DISTINCT visitor_hash)
FROM daily_visitors
WHERE day >= ? AND day <= ?
GROUP BY site`, from, to)
		if err != nil {
			return err
		}
		defer vRows.Close()
		for vRows.Next() {
			var name string
			var cnt int64
			if err := vRows.Scan(&name, &cnt); err != nil {
				return err
			}
			if sr := siteMap[name]; sr != nil {
				assign(sr, cnt)
			}
		}
		return nil
	}
	_ = fillSiteVisitors(today, today, func(sr *SiteRow, n int64) { sr.TodayDAU = n })
	_ = fillSiteVisitors(yesterday, yesterday, func(sr *SiteRow, n int64) { sr.YesterdayDAU = n })
	_ = fillSiteVisitors(wauFrom, today, func(sr *SiteRow, n int64) { sr.WAU = n })
	_ = fillSiteVisitors(mauFrom, today, func(sr *SiteRow, n int64) { sr.MAU = n })
	_ = fillSiteVisitors(dr.From, dr.To, func(sr *SiteRow, n int64) { sr.RangeUV = n })

	for _, name := range siteOrder {
		sr := siteMap[name]
		if sr.RangeUV > 0 {
			sr.PVPerUV = round2(float64(sr.RangePV) / float64(sr.RangeUV))
		}
		resp.Sites = append(resp.Sites, *sr)
	}
	return resp, nil
}

// QuerySiteReport builds the detailed report for a single configured site.
func (s *Store) QuerySiteReport(cfg *config.Config, site config.Site, dr DateRange, now time.Time) (*SiteReportResponse, error) {
	loc := cfg.AnalyticsLocation()
	localNow := now.In(loc)
	today := localNow.Format("2006-01-02")
	yesterday := localNow.AddDate(0, 0, -1).Format("2006-01-02")
	wauFrom := localNow.AddDate(0, 0, -6).Format("2006-01-02")
	mauFrom := localNow.AddDate(0, 0, -29).Format("2006-01-02")

	hosts := site.AnalyticsHosts()
	if hosts == nil {
		hosts = []string{}
	}
	resp := &SiteReportResponse{
		Enabled:  true,
		Timezone: cfg.Analytics.Timezone,
		Site:     site.Name,
		URL:      site.URL,
		Hosts:    hosts,
		Range:    dr,
		Daily:    make([]DailyPoint, 0, dr.Days),
		Hourly:   make([]HourlyPoint, 0, 24),
	}

	dayMap := make(map[string]*DailyPoint, dr.Days)
	for _, d := range enumerateDays(dr, loc) {
		dayMap[d] = &DailyPoint{Day: d}
	}

	rows, err := s.db.Query(`
SELECT day, pv, requests, bytes, err_4xx, err_5xx, bot_hits, duration_ms_sum, duration_count
FROM daily_site_stats
WHERE site = ? AND day >= ? AND day <= ?`, site.Name, dr.From, dr.To)
	if err != nil {
		return nil, err
	}
	var totalDurSum float64
	var totalDurCount int64
	for rows.Next() {
		var day string
		var pv, req, bytes, e4, e5, bots, dCount int64
		var dSum float64
		if err := rows.Scan(&day, &pv, &req, &bytes, &e4, &e5, &bots, &dSum, &dCount); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if pt := dayMap[day]; pt != nil {
			pt.PV = pv
			pt.Requests = req
			pt.Bytes = bytes
			pt.Err4xx = e4
			pt.Err5xx = e5
			pt.BotHits = bots
			if dCount > 0 {
				pt.AvgDurationMS = round1(dSum / float64(dCount))
			}
		}
		resp.Summary.RangePV += pv
		resp.Summary.RangeRequests += req
		resp.Summary.RangeBytes += bytes
		resp.Summary.Err4xx += e4
		resp.Summary.Err5xx += e5
		resp.Summary.BotHits += bots
		totalDurSum += dSum
		totalDurCount += dCount
	}
	_ = rows.Close()
	if totalDurCount > 0 {
		resp.Summary.AvgDurationMS = round1(totalDurSum / float64(totalDurCount))
	}

	dauRows, err := s.db.Query(`
SELECT day, COUNT(DISTINCT visitor_hash)
FROM daily_visitors
WHERE site = ? AND day >= ? AND day <= ?
GROUP BY day`, site.Name, dr.From, dr.To)
	if err != nil {
		return nil, err
	}
	for dauRows.Next() {
		var day string
		var dau int64
		if err := dauRows.Scan(&day, &dau); err != nil {
			_ = dauRows.Close()
			return nil, err
		}
		if pt := dayMap[day]; pt != nil {
			pt.DAU = dau
			if dau > 0 {
				pt.PVPerUV = round2(float64(pt.PV) / float64(dau))
			}
		}
	}
	_ = dauRows.Close()

	for _, d := range enumerateDays(dr, loc) {
		resp.Daily = append(resp.Daily, *dayMap[d])
	}

	resp.Summary.TodayDAU, _ = s.countDistinctVisitors(site.Name, today, today)
	resp.Summary.YesterdayDAU, _ = s.countDistinctVisitors(site.Name, yesterday, yesterday)
	resp.Summary.WAU, _ = s.countDistinctVisitors(site.Name, wauFrom, today)
	resp.Summary.MAU, _ = s.countDistinctVisitors(site.Name, mauFrom, today)
	resp.Summary.RangeUV, _ = s.countDistinctVisitors(site.Name, dr.From, dr.To)
	if resp.Summary.RangeUV > 0 {
		resp.Summary.PVPerUV = round2(float64(resp.Summary.RangePV) / float64(resp.Summary.RangeUV))
	}
	resp.Summary.TodayPV, _ = s.sumTodayPV(site.Name, today)
	resp.Summary.EngagedRate, _ = s.engagedRate(site.Name, dr.From, dr.To)

	// Last 24 hours hourly breakdown.
	resp.Hourly, err = s.QueryHourly(site.Name, loc, localNow, 24)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// QueryHourly returns continuous hourly points ending at localNow's hour.
func (s *Store) QueryHourly(site string, loc *time.Location, now time.Time, hours int) ([]HourlyPoint, error) {
	if hours <= 0 {
		hours = 24
	}
	endHour := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, loc)
	startHour := endHour.Add(-time.Duration(hours-1) * time.Hour)
	fromStr := startHour.Format("2006-01-02T15")
	toStr := endHour.Format("2006-01-02T15")

	hMap := make(map[string]*HourlyPoint, hours)
	keys := make([]string, 0, hours)
	for i := 0; i < hours; i++ {
		k := startHour.Add(time.Duration(i) * time.Hour).Format("2006-01-02T15")
		keys = append(keys, k)
		hMap[k] = &HourlyPoint{Hour: k}
	}

	var rows *sql.Rows
	var err error
	if site == "" || site == "_all" {
		rows, err = s.db.Query(`
SELECT hour, SUM(pv), SUM(requests), SUM(bytes), SUM(err_4xx), SUM(err_5xx)
FROM hourly_site_stats
WHERE hour >= ? AND hour <= ?
GROUP BY hour`, fromStr, toStr)
	} else {
		rows, err = s.db.Query(`
SELECT hour, pv, requests, bytes, err_4xx, err_5xx
FROM hourly_site_stats
WHERE site = ? AND hour >= ? AND hour <= ?`, site, fromStr, toStr)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var h string
		var pv, req, bytes, e4, e5 int64
		if err := rows.Scan(&h, &pv, &req, &bytes, &e4, &e5); err != nil {
			return nil, err
		}
		if pt := hMap[h]; pt != nil {
			pt.PV = pv
			pt.Requests = req
			pt.Bytes = bytes
			pt.Err4xx = e4
			pt.Err5xx = e5
		}
	}
	out := make([]HourlyPoint, 0, len(keys))
	for _, k := range keys {
		out = append(out, *hMap[k])
	}
	return out, nil
}

// ValidDimension reports whether dim is a supported breakdown table dimension.
func ValidDimension(dim string) bool {
	switch dim {
	case "path", "referrer", "browser", "os", "device", "status", "error_path", "bot", "bot_path":
		return true
	default:
		return false
	}
}

// QueryDimension returns a paginated, sorted, searchable dimension table for
// either a single site or all sites ("_all" or "").
func (s *Store) QueryDimension(site, dim string, dr DateRange, search, sortBy, order string, limit, offset int) (*DimensionResponse, error) {
	if !ValidDimension(dim) {
		return nil, fmt.Errorf("unsupported dimension %q", dim)
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if site == "" {
		site = "_all"
	}

	sortExpr := "pv_sum"
	switch sortBy {
	case "requests":
		sortExpr = "req_sum"
	case "bytes":
		sortExpr = "bytes_sum"
	case "err_count":
		sortExpr = "err_sum"
	case "avg_ms":
		sortExpr = "avg_ms"
	case "key":
		sortExpr = "key"
	default:
		if dim == "status" || dim == "error_path" || dim == "bot" || dim == "bot_path" {
			sortExpr = "req_sum"
		}
	}
	dirExpr := "DESC"
	if strings.EqualFold(order, "asc") {
		dirExpr = "ASC"
	}

	where := `WHERE dim = ? AND day >= ? AND day <= ?`
	args := []any{dim, dr.From, dr.To}
	if site != "_all" {
		where += ` AND site = ?`
		args = append(args, site)
	}
	if q := strings.TrimSpace(search); q != "" {
		where += ` AND LOWER(key) LIKE ?`
		args = append(args, "%"+strings.ToLower(q)+"%")
	}

	resp := &DimensionResponse{
		Enabled: true,
		Site:    site,
		Dim:     dim,
		Range:   dr,
		Rows:    make([]DimensionRow, 0),
	}

	countSQL := `SELECT COUNT(*), COALESCE(SUM(pv_sum), 0), COALESCE(SUM(req_sum), 0), COALESCE(SUM(bytes_sum), 0) FROM (
		SELECT key, SUM(pv) AS pv_sum, SUM(requests) AS req_sum, SUM(bytes) AS bytes_sum
		FROM daily_dim_stats ` + where + ` GROUP BY key)`
	if err := s.db.QueryRow(countSQL, args...).Scan(&resp.Total, &resp.TotalPV, &resp.TotalRequests, &resp.TotalBytes); err != nil {
		return nil, err
	}

	querySQL := fmt.Sprintf(`
SELECT key,
	SUM(pv) AS pv_sum,
	SUM(requests) AS req_sum,
	SUM(bytes) AS bytes_sum,
	SUM(err_count) AS err_sum,
	CASE WHEN SUM(requests) > 0 THEN SUM(duration_ms_sum) / SUM(requests) ELSE 0 END AS avg_ms
FROM daily_dim_stats
%s
GROUP BY key
ORDER BY %s %s, req_sum DESC, key ASC
LIMIT ? OFFSET ?`, where, sortExpr, dirExpr)
	qArgs := append(append([]any(nil), args...), limit, offset)
	rows, err := s.db.Query(querySQL, qArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	denom := resp.TotalPV
	if dim == "status" || dim == "error_path" || dim == "bot" || dim == "bot_path" || denom == 0 {
		denom = resp.TotalRequests
	}

	for rows.Next() {
		var r DimensionRow
		if err := rows.Scan(&r.Key, &r.PV, &r.Requests, &r.Bytes, &r.ErrCount, &r.AvgMS); err != nil {
			return nil, err
		}
		r.AvgMS = round1(r.AvgMS)
		if denom > 0 {
			num := r.PV
			if dim == "status" || dim == "error_path" || dim == "bot" || dim == "bot_path" || resp.TotalPV == 0 {
				num = r.Requests
			}
			r.Share = round2(float64(num) * 100.0 / float64(denom))
		}
		resp.Rows = append(resp.Rows, r)
	}
	return resp, nil
}

func (s *Store) countDistinctVisitors(site, fromDay, toDay string) (int64, error) {
	var n int64
	var err error
	if site == "" || site == "_all" {
		err = s.db.QueryRow(`SELECT COUNT(DISTINCT visitor_hash) FROM daily_visitors WHERE day >= ? AND day <= ?`, fromDay, toDay).Scan(&n)
	} else {
		err = s.db.QueryRow(`SELECT COUNT(DISTINCT visitor_hash) FROM daily_visitors WHERE site = ? AND day >= ? AND day <= ?`, site, fromDay, toDay).Scan(&n)
	}
	return n, err
}

func (s *Store) sumTodayPV(site, today string) (int64, error) {
	var n int64
	var err error
	if site == "" || site == "_all" {
		err = s.db.QueryRow(`SELECT COALESCE(SUM(pv), 0) FROM daily_site_stats WHERE day = ?`, today).Scan(&n)
	} else {
		err = s.db.QueryRow(`SELECT COALESCE(SUM(pv), 0) FROM daily_site_stats WHERE site = ? AND day = ?`, site, today).Scan(&n)
	}
	return n, err
}

func (s *Store) engagedRate(site, fromDay, toDay string) (float64, error) {
	var total, engaged int64
	var err error
	if site == "" || site == "_all" {
		err = s.db.QueryRow(`
SELECT COUNT(*), COALESCE(SUM(CASE WHEN total_hits > 1 THEN 1 ELSE 0 END), 0)
FROM (
	SELECT day, visitor_hash, SUM(hits) AS total_hits
	FROM daily_visitors
	WHERE day >= ? AND day <= ?
	GROUP BY day, visitor_hash
)`, fromDay, toDay).Scan(&total, &engaged)
	} else {
		err = s.db.QueryRow(`
SELECT COUNT(*), COALESCE(SUM(CASE WHEN hits > 1 THEN 1 ELSE 0 END), 0)
FROM daily_visitors
WHERE site = ? AND day >= ? AND day <= ?`, site, fromDay, toDay).Scan(&total, &engaged)
	}
	if err != nil || total == 0 {
		return 0, err
	}
	return round2(float64(engaged) * 100.0 / float64(total)), nil
}

// WriteOverviewCSV writes the cross-site comparison table as UTF-8 BOM CSV.
func WriteOverviewCSV(w io.Writer, rep *OverviewResponse) error {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Site", "URL", "Today DAU", "Yesterday DAU", "WAU (7d)", "MAU (30d)", "Range UV", "Today PV", "Range PV", "PV/UV", "Requests", "Bytes", "Avg Latency (ms)", "4xx", "5xx", "Bot Hits"})
	for _, s := range rep.Sites {
		_ = cw.Write([]string{
			s.Site, s.URL,
			fmt.Sprint(s.TodayDAU), fmt.Sprint(s.YesterdayDAU),
			fmt.Sprint(s.WAU), fmt.Sprint(s.MAU), fmt.Sprint(s.RangeUV),
			fmt.Sprint(s.TodayPV), fmt.Sprint(s.RangePV), fmt.Sprintf("%.2f", s.PVPerUV),
			fmt.Sprint(s.RangeRequests), fmt.Sprint(s.RangeBytes), fmt.Sprintf("%.1f", s.AvgDurationMS),
			fmt.Sprint(s.Err4xx), fmt.Sprint(s.Err5xx), fmt.Sprint(s.BotHits),
		})
	}
	cw.Flush()
	return cw.Error()
}

// WriteDailyCSV writes a daily series as UTF-8 BOM CSV.
func WriteDailyCSV(w io.Writer, rows []DailyPoint) error {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Day", "DAU", "PV", "PV/UV", "Requests", "Bytes", "Avg Latency (ms)", "4xx", "5xx", "Bot Hits"})
	for _, r := range rows {
		_ = cw.Write([]string{
			r.Day, fmt.Sprint(r.DAU), fmt.Sprint(r.PV), fmt.Sprintf("%.2f", r.PVPerUV),
			fmt.Sprint(r.Requests), fmt.Sprint(r.Bytes), fmt.Sprintf("%.1f", r.AvgDurationMS),
			fmt.Sprint(r.Err4xx), fmt.Sprint(r.Err5xx), fmt.Sprint(r.BotHits),
		})
	}
	cw.Flush()
	return cw.Error()
}

// WriteDimensionCSV writes a dimension breakdown table as UTF-8 BOM CSV.
func WriteDimensionCSV(w io.Writer, rep *DimensionResponse) error {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Key", "PV", "Share (%)", "Requests", "Bytes", "Errors", "Avg Latency (ms)"})
	for _, r := range rep.Rows {
		_ = cw.Write([]string{
			r.Key, fmt.Sprint(r.PV), fmt.Sprintf("%.2f", r.Share),
			fmt.Sprint(r.Requests), fmt.Sprint(r.Bytes), fmt.Sprint(r.ErrCount), fmt.Sprintf("%.1f", r.AvgMS),
		})
	}
	cw.Flush()
	return cw.Error()
}
