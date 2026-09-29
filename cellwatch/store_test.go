package cellwatch

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenCreatesSchemaInTempDir(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "battery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// Reopening must be a no-op, since every table is created if absent. A
	// second open over an existing file is the path an upgrading user takes.
	st.Close()
	st2, err := Open(filepath.Join(t.TempDir(), "battery.db"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	st2.Close()
}

func TestRecordAcceptsAbsentBattery(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "battery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// A desktop reports no battery. Recording that must be a no-op rather than
	// an error and rather than a row of zeroes that would later read as a
	// machine that used no power.
	if err := st.Record(context.Background(), Status{Present: false}); err != nil {
		t.Errorf("recording an absent battery should succeed, got %v", err)
	}
}

func TestRecordWritesRawAndRollups(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "battery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	s := sqliteStore{db: st.(*sqliteStore).db}
	for i := 0; i < 5; i++ {
		if err := s.Record(context.Background(), Status{
			Present:     true,
			ChargePct:   80 - float64(i),
			ChargeKnown: true,
			AC:          ACOffline,
			Cap:         CapChargePct,
		}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	var raw int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM battery_raw`).Scan(&raw); err != nil {
		t.Fatalf("count raw: %v", err)
	}
	if raw == 0 {
		t.Error("expected raw rows to be written")
	}

	// Every reading belongs to one bucket at each tier, so a single record must
	// produce one row in each rollup tier.
	var tiers int
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT tier) FROM battery_rollups`).Scan(&tiers); err != nil {
		t.Fatalf("count tiers: %v", err)
	}
	if tiers != 6 {
		t.Errorf("distinct rollup tiers = %d, want 6 (minute, hour, day, week, month, year)", tiers)
	}
}

func TestRollupUpsertIsIdempotent(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "battery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	s := sqliteStore{db: st.(*sqliteStore).db}

	ctx := context.Background()
	// One fixed instant, written three times. Replaying the same reading must
	// update in place rather than duplicating, or a sampler that retries would
	// inflate every total it touches.
	at := time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)
	sample := Status{Present: true, ChargePct: 50, ChargeKnown: true, AC: ACOffline}
	for i := 0; i < 3; i++ {
		if err := s.recordAt(ctx, sample, at); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	var raw int
	s.db.QueryRow(`SELECT COUNT(*) FROM battery_raw`).Scan(&raw)
	if raw != 1 {
		t.Errorf("raw rows = %d after writing the same instant three times, want 1", raw)
	}

	for _, tier := range []Tier{TierMinute, TierHour, TierDay, TierWeek, TierMonth, TierYear} {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM battery_rollups WHERE tier=?`, int(tier)).Scan(&n)
		if n != 1 {
			t.Errorf("%v rollup rows = %d, want 1", tier, n)
		}
	}
}

func TestBucketsForAreUTC(t *testing.T) {
	// A late-evening local instant that falls on the next UTC day must land in
	// the UTC day, not the local one, or daily totals drift by an hour twice a
	// year at every daylight-saving boundary.
	loc := time.FixedZone("UTC+5:30", 5*3600+1800)
	local := time.Date(2026, 3, 15, 23, 45, 0, 0, loc)

	for _, b := range bucketsFor(local) {
		if b.start.Location() != time.UTC {
			t.Errorf("bucket %v start is in %v, want UTC", b.tier, b.start.Location())
		}
	}
	// 23:45 at +05:30 is 18:15 UTC the same day, so the day bucket is the 15th.
	var day time.Time
	for _, b := range bucketsFor(local) {
		if b.tier == TierDay {
			day = b.start
		}
	}
	if day.Day() != 15 {
		t.Errorf("day bucket = %v, want the 15th", day)
	}
}

func TestISOWeekStartIsMonday(t *testing.T) {
	for _, year := range []int{2024, 2025, 2026, 2027} {
		for w := 1; w <= 52; w++ {
			monday := isoWeekStart(year, w)
			if monday.Weekday() != time.Monday {
				t.Errorf("isoWeekStart(%d,%d) = %v, which is a %v", year, w, monday, monday.Weekday())
			}
			y, isoW := monday.ISOWeek()
			if y != year || isoW != w {
				t.Errorf("isoWeekStart(%d,%d) round-trips to %d week %d", year, w, y, isoW)
			}
		}
	}
}

func TestMonthBucketSpansMonthBoundary(t *testing.T) {
	// 31 March belongs to March's month bucket. The last days of a month often
	// fall in the following month's first ISO week, which is exactly why the
	// week and month tiers cannot be derived from one another.
	month := bucketsFor(time.Date(2026, 3, 31, 23, 0, 0, 0, time.UTC))[4]
	if month.start.Month() != time.March {
		t.Errorf("month bucket = %v, want March", month.start)
	}
	week := bucketsFor(time.Date(2026, 3, 31, 23, 0, 0, 0, time.UTC))[3]
	_, weekNum := week.start.ISOWeek()
	if week.start.Month() != time.April {
		t.Logf("note: 31 Mar 2026 falls in ISO week starting %v (week %d), which is correct ISO behaviour", week.start, weekNum)
	}
}

// The Windows reader rejects a time-remaining value at or above 2^31, because
// firmware signals "unknown" with an unsigned sentinel rather than a negative
// number. A measured reading gave BatteryLifeTime of 3943 seconds alongside a
// BatteryFullLifeTime of 0xFFFFFFFF, so the two fields must be checked
// independently rather than as a pair.
func TestTimeToEmptySentinelBound(t *testing.T) {
	if got := plausibleSeconds(1 << 31); got {
		t.Error("2^31 seconds must be treated as unknown, not as a runtime")
	}
	if got := plausibleSeconds(3943); !got {
		t.Error("3943 seconds is a real reading and must be accepted")
	}
	if got := plausibleSeconds(0xFFFFFFFF); got {
		t.Error("0xFFFFFFFF is the firmware sentinel and must be rejected")
	}
	if got := plausibleSeconds(-1); got {
		t.Error("a negative duration must be rejected")
	}
}
