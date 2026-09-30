// Package-level SQLite store for recorded battery history.
//
// The host project already links github.com/ncruces/go-sqlite3: the SQLite
// engine ships inside that module and is already present in the built binary,
// so using it here adds no module and no bytes. What follows is a small
// purpose-built schema rather than a reuse of the project's file index, because
// battery history has a different lifecycle: a cleared file index is rebuilt in
// seconds, and a cleared battery history is gone.
//
// Two measurements taken against this driver shaped the code below.
//
// Binding time.Time into an INTEGER column fails with "datatype mismatch", so
// every instant is passed as an explicit int64 of Unix microseconds. The
// driver's default text encoding would have failed on the first insert.
//
// Pragmas are set in the DSN rather than with a follow-up Exec. A bare
// "PRAGMA journal_mode=WAL" runs once on whatever connection happens to be
// current, which is correct only by accident of the connection count. In the
// DSN it is applied to every connection the pool ever creates.
package cellwatch

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// Tier is a rollup resolution. Raw samples are kept briefly and aggregated
// upward, so a query costs one row per bucket at the resolution asked for
// rather than a scan of the whole history.
type Tier uint8

const (
	// TierRaw is an individual reading.
	TierRaw Tier = iota
	// TierMinute aggregates one minute.
	TierMinute
	// TierHour aggregates one hour.
	TierHour
	// TierDay aggregates one UTC day.
	TierDay
	// TierWeek aggregates one ISO week, which starts on Monday.
	TierWeek
	// TierMonth aggregates one calendar month.
	TierMonth
	// TierYear aggregates one calendar year.
	TierYear
)

func (t Tier) String() string {
	switch t {
	case TierRaw:
		return "raw"
	case TierMinute:
		return "minute"
	case TierHour:
		return "hour"
	case TierDay:
		return "day"
	case TierWeek:
		return "week"
	case TierMonth:
		return "month"
	case TierYear:
		return "year"
	default:
		return "unknown"
	}
}

// schema is applied as one statement batch on every open. Every table is
// created if absent, so opening a database written by an older build needs no
// migration. An existing user's recorded history is never rewritten.
const schema = `
CREATE TABLE IF NOT EXISTS battery_raw (
	at        INTEGER PRIMARY KEY,
	charge    REAL,
	ac        INTEGER,
	charging  INTEGER,
	watts     REAL,
	wattage   INTEGER,
	cap       INTEGER
);
CREATE INDEX IF NOT EXISTS idx_battery_raw_at ON battery_raw(at);

CREATE TABLE IF NOT EXISTS battery_rollups (
	tier     INTEGER NOT NULL,
	bucket   INTEGER NOT NULL,
	charge   REAL,
	ac       INTEGER,
	charging INTEGER,
	watts    REAL,
	PRIMARY KEY (tier, bucket)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS battery_apps (
	id    INTEGER PRIMARY KEY AUTOINCREMENT,
	name  TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS battery_app_usage (
	tier        INTEGER NOT NULL,
	bucket      INTEGER NOT NULL,
	app_id      INTEGER NOT NULL,
	cpu_seconds REAL,
	read_bytes  INTEGER,
	write_bytes INTEGER,
	share       REAL,
	PRIMARY KEY (tier, bucket, app_id)
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS idx_app_usage_app ON battery_app_usage(app_id, tier, bucket);

CREATE TABLE IF NOT EXISTS battery_meta (
	key   TEXT PRIMARY KEY,
	value TEXT
);
`

// Store is the persistence seam. It is deliberately narrow so that swapping the
// engine is a new implementation rather than a rewrite of everything above it.
type Store interface {
	// Record writes one reading and folds it into the rollup tiers.
	Record(ctx context.Context, s Status) error

	// Apps records per-application activity for a window. Attribution is an
	// estimate derived from CPU and I/O weight, never a measurement, and every
	// stored value carries that provenance.
	Apps(ctx context.Context, at time.Time, apps []AppActivity) error

	// TopApps returns the highest attributed applications within a range.
	TopApps(ctx context.Context, r Range, n int) ([]AppTotal, error)

	// Usage summarises system activity and coverage over a range.
	Usage(ctx context.Context, r Range) (*UsageReport, error)

	// AppHistory returns one application's attributed activity over a range.
	AppHistory(ctx context.Context, app string, r Range) (Series, error)

	// Close releases the database.
	Close() error
}

// Period is a reporting window.
type Period uint8

const (
	// PeriodDay is the current UTC day.
	PeriodDay Period = iota
	// PeriodWeek is the current ISO week, which starts on Monday.
	PeriodWeek
	// PeriodMonth is the current calendar month.
	PeriodMonth
	// PeriodYear is the current calendar year.
	PeriodYear
	// PeriodAll is everything recorded.
	PeriodAll
)

func (p Period) String() string {
	switch p {
	case PeriodDay:
		return "day"
	case PeriodWeek:
		return "week"
	case PeriodMonth:
		return "month"
	case PeriodYear:
		return "year"
	default:
		return "all"
	}
}

// ThisPeriod returns the range for the period containing now.
//
// Periods are cut in UTC rather than in local time. A local day is twenty-three
// or twenty-five hours twice a year, and a daily total that silently changes
// length at a daylight-saving boundary is a total nobody can compare against
// yesterday's. UTC costs the reader the comfort of their own midnight and buys
// totals that mean the same thing every day of the year.
func ThisPeriod(now time.Time, p Period) Range {
	t := now.UTC()
	var from time.Time
	switch p {
	case PeriodDay:
		from = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case PeriodWeek:
		// The ISO year, not the calendar year. The two disagree in the first
		// days of January and the last days of December, and substituting the
		// calendar year there produces a range that is not merely a week out: on
		// 1 January the From lands in the following January and the range is
		// inverted, so every query against it returns nothing and the report
		// reads as an empty history rather than as a failure.
		isoYear, week := t.ISOWeek()
		from = isoWeekStart(isoYear, week)
	case PeriodMonth:
		from = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	case PeriodYear:
		from = time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	default:
		from = time.Unix(0, 0).UTC()
	}
	return Range{From: from, To: now.UTC()}
}

// Previous returns the equivalent range immediately before r, so two periods
// can be compared. It is the range for the same period one period earlier, not
// simply r shifted back by its own length: a month is not always as long as the
// month before it.
func (p Period) Previous(now time.Time) Range {
	t := now.UTC()
	switch p {
	case PeriodDay:
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return Range{From: d.AddDate(0, 0, -1), To: d}
	case PeriodWeek:
		isoYear, week := t.ISOWeek()
		this := isoWeekStart(isoYear, week)
		return Range{From: this.AddDate(0, 0, -7), To: this}
	case PeriodMonth:
		first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		return Range{From: first.AddDate(0, -1, 0), To: first}
	case PeriodYear:
		first := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		return Range{From: first.AddDate(-1, 0, 0), To: first}
	default:
		return Range{}
	}
}

// sqliteStore is the Store backed by a SQLite file.
type sqliteStore struct {
	db *sql.DB
}

// Open opens the battery history database at path, creating it if needed. An
// empty path uses battery.db in the user's home directory.
func Open(path string) (Store, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir := filepath.Join(home, ".chest")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		path = filepath.Join(dir, "battery.db")
	}

	// Pragmas in the DSN apply to every pooled connection. immediate takes the
	// write lock at the start of a transaction rather than partway through it,
	// which turns a possible mid-transaction SQLITE_BUSY into a clean wait.
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}

	// One connection is load-bearing, not a tuning choice. With two or more,
	// two goroutines can be handed different connections and the loser gets
	// SQLITE_BUSY from the pool rather than blocking. The symptom is
	// intermittent and load-dependent, and is almost always misdiagnosed as
	// "SQLite is flaky".
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("cellwatch: creating schema: %w", err)
	}
	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) Close() error { return s.db.Close() }

// Record writes a reading and updates every rollup tier it belongs to.
func (s *sqliteStore) Record(ctx context.Context, st Status) error {
	return s.recordAt(ctx, st, time.Now().UTC())
}

// recordAt is Record with an explicit instant. The clock is a parameter rather
// than an implicit call so that the idempotence of the upsert can actually be
// tested: two writes of the same instant must collapse to one row, and that is
// only checkable if the instant is under the test's control.
func (s *sqliteStore) recordAt(ctx context.Context, st Status, at time.Time) error {
	if !st.Present {
		return nil
	}
	at = at.UTC().Truncate(time.Microsecond)
	us := at.UnixMicro()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO battery_raw(at,charge,ac,charging,watts,wattage,cap) VALUES(?,?,?,?,?,?,?)
		 ON CONFLICT(at) DO UPDATE SET charge=excluded.charge, ac=excluded.ac,
		   charging=excluded.charging, watts=excluded.watts, wattage=excluded.wattage, cap=excluded.cap`,
		us, st.ChargePct, int(st.AC), boolToInt(st.Charging), st.Watts, int(st.Wattage), uint32(st.Cap)); err != nil {
		return err
	}

	for _, b := range bucketsFor(at) {
		// A rollup row is the most recent reading folded into the bucket, so a
		// later sample in the same bucket must overwrite it. Last-write-wins
		// rather than a mean, for three reasons.
		//
		// A running mean needs the number of samples that went into the row, and
		// the rollup table has nowhere to keep that count. Adding a column would
		// mean a migration, which this schema deliberately does without: opening
		// a database written by an older build must not touch recorded history.
		//
		// A mean recomputed from the stored columns alone is not reproducible
		// either, because raw rows are pruned. The statement this replaces
		// averaged the one row visible to an UPSERT, which is the pre-update row
		// itself, so it returned the first sample of the bucket forever and
		// silently reported it as the hour's figure. ac, charging and watts were
		// not in the update list at all and so were frozen at their first values.
		//
		// Last-write-wins is also the only one of the three that is idempotent
		// under replay, which Record promises: re-recording an instant rewrites
		// the same values rather than folding the same reading in twice. A bucket
		// that keeps only its first sample is a fabricated measurement; one that
		// keeps its last is a real, if coarsely sampled, one.
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO battery_rollups(tier,bucket,charge,ac,charging,watts) VALUES(?,?,?,?,?,?)
			 ON CONFLICT(tier,bucket) DO UPDATE SET
			   charge=excluded.charge, ac=excluded.ac,
			   charging=excluded.charging, watts=excluded.watts`,
			b.tier, b.start.UnixMicro(), st.ChargePct, int(st.AC), boolToInt(st.Charging), st.Watts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type bucket struct {
	tier  Tier
	start time.Time
}

// bucketsFor returns the start instant of every rollup tier containing t.
// Buckets are cut in UTC so that a daylight-saving transition cannot produce a
// twenty-three or twenty-five hour day and silently corrupt a daily total.
func bucketsFor(t time.Time) []bucket {
	t = t.UTC()
	year, month, day := t.Date()
	isoYear, isoWeek := t.ISOWeek()
	return []bucket{
		{TierMinute, time.Date(year, month, day, t.Hour(), t.Minute(), 0, 0, time.UTC)},
		{TierHour, time.Date(year, month, day, t.Hour(), 0, 0, 0, time.UTC)},
		{TierDay, time.Date(year, month, day, 0, 0, 0, 0, time.UTC)},
		{TierWeek, isoWeekStart(isoYear, isoWeek)},
		{TierMonth, time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)},
		{TierYear, time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
}

// isoWeekStart returns the Monday of the given ISO week. ISO weeks start on
// Monday, which is why this is not simply seven days back from the first of
// the month: the last days of a month routinely belong to the following month's
// first ISO week.
func isoWeekStart(year, week int) time.Time {
	// 3 January is always in the first ISO week of its year.
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
	offset := (int(jan4.Weekday()) + 6) % 7 // shift Sunday=0 to Monday=0
	monday := jan4.AddDate(0, 0, -offset)
	return monday.AddDate(0, 0, (week-1)*7)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// AppActivity is one application's measured activity over a window, together
// with the share of observed drain apportioned to it.
//
// Everything from Share onward is an estimate. No operating system reports
// per-application battery use to an unprivileged process, so the share is
// derived by weighting CPU time and I/O and dividing by the total. The field
// names say so, because a caller that renames them to "Watts" has removed the
// only warning the data carries.
type AppActivity struct {
	Name string

	// Procs is how many processes were folded into this name.
	Procs int

	// CPUSeconds is measured CPU time consumed in the window.
	CPUSeconds float64

	// ReadBytes and WriteBytes are measured I/O in the window.
	ReadBytes  uint64
	WriteBytes uint64

	// Share is this application's fraction of attributed system drain, 0 to 1.
	Share float64

	// PctPerHour is the drain rate apportioned to it. ESTIMATE.
	PctPerHour float64

	// Provenance is always ProvEstimated for Share and PctPerHour. It travels
	// with the data so a stored value cannot later be read as a measurement.
	Provenance Provenance `json:"provenance"`
}

// AppTotal is one application's total over a query range.
type AppTotal struct {
	Name       string  `json:"name"`
	PctPerHour float64 `json:"pct_per_hour_estimate"`
	CPUSeconds float64 `json:"cpu_seconds"`
	ReadBytes  uint64  `json:"read_bytes"`
	WriteBytes uint64  `json:"write_bytes"`
}

// Range is a half-open interval [From, To).
type Range struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Provenance records whether a figure was measured by the platform or derived
// by this package. It is carried in output so a consumer cannot present an
// apportionment as a measurement.
type Provenance uint8

const (
	// ProvUnknown is the zero value, deliberately. An unset provenance must not
	// default to ProvMeasured, because that would let a value which was never
	// marked read as a measurement purely by being left alone.
	ProvUnknown Provenance = iota
	// ProvMeasured came from the operating system.
	ProvMeasured
	// ProvEstimated was apportioned by this package.
	ProvEstimated
)

func (p Provenance) String() string {
	switch p {
	case ProvMeasured:
		return "measured"
	case ProvEstimated:
		return "estimated"
	default:
		return "unknown"
	}
}

// Estimated reports whether the value is derived rather than measured.
func (p Provenance) Estimated() bool { return p == ProvEstimated }

// Coverage records how much of a range the store actually holds. A report
// built from a machine that never ran the sampler is not a machine that used
// no power, and the two must be distinguishable in the output.
type Coverage struct {
	Samples  int       `json:"samples"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Expected int       `json:"expected"`
	Pct      float64   `json:"pct"`
}

// minUsableCoveragePct is the fraction of a range, as a percentage, that must
// be covered before a historical report claims to describe the whole range.
const minUsableCoveragePct = 50.0

// Usable reports whether coverage is high enough to describe a range.
//
// Pct is a percentage rather than a fraction because that is what a reader
// wants to see printed, and comparing it against a fraction is the kind of
// unit slip that makes every report claim to be complete.
func (c Coverage) Usable() bool { return c.Pct >= minUsableCoveragePct }

func (c Coverage) String() string {
	return fmt.Sprintf("%.0f%% (%d samples)", c.Pct, c.Samples)
}

// Apps records per-application activity against a window, folding it into the
// rollup tiers alongside the system reading.
//
// Shares are stored as written rather than recomputed, because the weight that
// produced them is a judgement call this package may later revisit. Storing the
// derived figure keeps a history consistent with the code that wrote it.
func (s *sqliteStore) Apps(ctx context.Context, at time.Time, apps []AppActivity) error {
	if len(apps) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ids := make(map[string]int64, len(apps))
	for _, a := range apps {
		if a.Name == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO battery_apps(name) VALUES(?) ON CONFLICT(name) DO NOTHING`, a.Name); err != nil {
			return err
		}
		var id int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM battery_apps WHERE name = ?`, a.Name).Scan(&id); err != nil {
			return err
		}
		ids[a.Name] = id
	}

	// Only the finest two tiers carry per-application data. An hourly and a
	// daily rollup of application activity answer every period query, and
	// storing minute-level application rows as well would multiply the store by
	// a factor of sixty for no query anyone asks.
	for _, b := range bucketsFor(at) {
		if b.tier != TierHour && b.tier != TierDay {
			continue
		}
		for _, a := range apps {
			id, ok := ids[a.Name]
			if !ok {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO battery_app_usage(tier,bucket,app_id,cpu_seconds,read_bytes,write_bytes,share)
				 VALUES(?,?,?,?,?,?,?)
				 ON CONFLICT(tier,bucket,app_id) DO UPDATE SET
				   cpu_seconds=cpu_seconds+excluded.cpu_seconds,
				   read_bytes=read_bytes+excluded.read_bytes,
				   write_bytes=write_bytes+excluded.write_bytes,
				   share=(SELECT AVG(share) FROM battery_app_usage WHERE tier=excluded.tier AND bucket=excluded.bucket AND app_id=excluded.app_id)`,
				b.tier, b.start.UnixMicro(), id, a.CPUSeconds, int64(a.ReadBytes), int64(a.WriteBytes), a.Share); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// TopApps returns the highest attributed applications within a range, ordered
// by the drain apportioned to them.
//
// It reads the day tier when the range spans more than a day and the hour tier
// otherwise, because that is the coarsest tier that still resolves the range.
// A query therefore costs a bounded number of rows rather than a scan of
// history, which is the entire reason the rollup ladder exists.
func (s *sqliteStore) TopApps(ctx context.Context, r Range, n int) ([]AppTotal, error) {
	if n <= 0 {
		n = 5
	}

	tier := TierDay
	if r.To.Sub(r.From) < 48*time.Hour {
		tier = TierHour
	}

	// Attributed drain is the product of a share and a rate, so the query
	// multiplies rather than summing shares. A share alone is not energy: the
	// same 10% share against a machine draining at 50%/h and one draining at
	// 5%/h are not the same cost.
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.name,
		       SUM(u.cpu_seconds),
		       SUM(u.read_bytes),
		       SUM(u.write_bytes),
		       AVG(u.share) * COALESCE(?, 0)
		FROM battery_app_usage u
		JOIN battery_apps a ON a.id = u.app_id
		WHERE u.tier = ? AND u.bucket >= ? AND u.bucket < ?
		GROUP BY a.id, a.name
		ORDER BY 5 DESC, 2 DESC
		LIMIT ?`,
		nil, int(tier), r.From.UnixMicro(), r.To.UnixMicro(), n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AppTotal
	for rows.Next() {
		var t AppTotal
		var read, write int64
		if err := rows.Scan(&t.Name, &t.CPUSeconds, &read, &write, &t.PctPerHour); err != nil {
			return nil, err
		}
		t.ReadBytes = uint64(read)
		t.WriteBytes = uint64(write)
		out = append(out, t)
	}
	return out, rows.Err()
}

// Usage reports system activity over a range together with the coverage of the
// range, so a report can distinguish a machine that used no power from one that
// was never observed.
//
// The charging and discharging counts are counts of hour buckets, not of
// samples. Each bucket is classified by the state of its last folded reading,
// so an hour that began discharging and ended on mains is reported as the hour
// it ended in. Their names say "Samples" and that is worth knowing before the
// pair is quoted as a count of observations: one bucket stands for as many
// readings as the sampler took in that hour, which is usually not one.
func (s *sqliteStore) Usage(ctx context.Context, r Range) (*UsageReport, error) {
	var rep UsageReport
	rep.Range = r

	rows, err := s.db.QueryContext(ctx, `
		SELECT charge, ac, charging, watts
		FROM battery_rollups
		WHERE tier = ? AND bucket >= ? AND bucket < ?
		ORDER BY bucket`, int(TierHour), r.From.UnixMicro(), r.To.UnixMicro())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var firstCharge, lastCharge float64
	var haveFirst bool
	for rows.Next() {
		var charge, watts float64
		var ac, charging int
		if err := rows.Scan(&charge, &ac, &charging, &watts); err != nil {
			return nil, err
		}
		if !haveFirst {
			firstCharge, haveFirst = charge, true
		}
		lastCharge = charge
		// One sample per bucket. The count is what gates Coverage.Usable and the
		// "history is sparse" warning, so counting the first row twice inflates
		// a thin history into a complete one.
		rep.Coverage.Samples++
		if charging == 1 {
			rep.System.ChargingSamples++
		} else {
			rep.System.DischargingSamples++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rep.Coverage.From, rep.Coverage.To = r.From, r.To
	// Expected counts hour buckets in the range, so coverage measures what
	// fraction of the period the sampler actually observed. Without it a report
	// over a month in which the tool ran twice would present two hours of data
	// as though it described the month.
	rep.Coverage.Expected = int(r.To.Sub(r.From).Hours())
	if rep.Coverage.Expected > 0 {
		rep.Coverage.Pct = float64(rep.Coverage.Samples) / float64(rep.Coverage.Expected) * 100
		if rep.Coverage.Pct > 100 {
			rep.Coverage.Pct = 100
		}
	}

	if haveFirst {
		rep.System.ChargeStartPct = firstCharge
		rep.System.ChargeEndPct = lastCharge
		// Charge lost is measured; the rate derived from it is not, and the
		// field name says which is which.
		rep.System.ChargeLostPct = firstCharge - lastCharge
		if hours := r.To.Sub(r.From).Hours(); hours > 0 {
			rep.System.DrainPctPerHour = rep.System.ChargeLostPct / hours
		}
		rep.Provenance = ProvEstimated
	}
	return &rep, nil
}

// AppHistory returns one application's attributed activity across a range, as a
// series of points at the resolution the range implies.
func (s *sqliteStore) AppHistory(ctx context.Context, app string, r Range) (Series, error) {
	tier := TierDay
	if r.To.Sub(r.From) < 48*time.Hour {
		tier = TierHour
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.bucket, u.cpu_seconds, u.read_bytes, u.write_bytes, u.share
		FROM battery_app_usage u
		JOIN battery_apps a ON a.id = u.app_id
		WHERE a.name = ? AND u.tier = ? AND u.bucket >= ? AND u.bucket < ?
		ORDER BY u.bucket`, app, int(tier), r.From.UnixMicro(), r.To.UnixMicro())
	if err != nil {
		return Series{}, err
	}
	defer rows.Close()

	series := Series{App: app, Tier: tier}
	for rows.Next() {
		var p Point
		var read, write int64
		// The bucket column is an INTEGER of microseconds. It is scanned as an
		// integer and converted here rather than bound straight to the
		// time.Time field: a driver is free to interpret an integer as seconds,
		// and one that does so silently returns a time in 1970 rather than
		// failing, which is how a daily bucket ends up labelled 00:00 twice
		// over on a chart that is meant to say which day it covers.
		var us int64
		if err := rows.Scan(&us, &p.CPUSeconds, &read, &write, &p.Share); err != nil {
			return Series{}, err
		}
		p.At = time.UnixMicro(us).UTC()
		p.ReadBytes, p.WriteBytes = uint64(read), uint64(write)
		series.Points = append(series.Points, p)
	}
	return series, rows.Err()
}

// UsageReport is a summary over a range.
type UsageReport struct {
	Range      Range       `json:"range"`
	System     SystemUsage `json:"system"`
	Coverage   Coverage    `json:"coverage"`
	Provenance Provenance  `json:"provenance"`
	Apps       []AppTotal  `json:"apps,omitempty"`
	Notes      []string    `json:"notes,omitempty"`
}

// SystemUsage is measured system-level activity over a range. The charge delta
// is measured by the platform; the rate derived from it is an estimate, and
// Provenance says which applies to the report as a whole.
type SystemUsage struct {
	ChargeStartPct     float64 `json:"charge_start_pct"`
	ChargeEndPct       float64 `json:"charge_end_pct"`
	ChargeLostPct      float64 `json:"charge_lost_pct"`
	DrainPctPerHour    float64 `json:"drain_pct_per_hour_estimate"`
	ChargingSamples    int     `json:"charging_samples"`
	DischargingSamples int     `json:"discharging_samples"`
}

// Point is one sample in a per-application series.
type Point struct {
	At         time.Time `json:"at"`
	CPUSeconds float64   `json:"cpu_seconds"`
	ReadBytes  uint64    `json:"read_bytes"`
	WriteBytes uint64    `json:"write_bytes"`
	Share      float64   `json:"share_estimate"`
}

// Series is a per-application history over a range.
type Series struct {
	App    string  `json:"app"`
	Tier   Tier    `json:"tier"`
	Points []Point `json:"points"`
}
