package cellwatch

// Regression tests produced by the cellwatch audit. Each one encodes an
// invariant that the package's own documentation already asserts, so a fix that
// satisfies any of them is a fix in the spirit of the package. They are
// deliberately fix-agnostic: where more than one repair is legitimate the
// assertion is written to hold under all of them.

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func auditStore(t *testing.T) *sqliteStore {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "battery.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st.(*sqliteStore)
}

// Coverage.Samples counted the first row twice, because the increment sat both
// inside the first-row branch and after it. Coverage is the figure that decides
// whether a report is allowed to describe a whole period, so an inflated count
// silently promotes thin history to "complete".

func TestCoverageSamplesCountsEachBucketOnce(t *testing.T) {
	s := auditStore(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)

	const buckets = 3
	for i := 0; i < buckets; i++ {
		if err := s.recordAt(ctx, Status{
			Present: true, ChargePct: 50 - float64(i), ChargeKnown: true, AC: ACOffline,
		}, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("recordAt %d: %v", i, err)
		}
	}

	rep, err := s.Usage(ctx, Range{From: base, To: base.Add(4 * time.Hour)})
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if rep.Coverage.Samples != buckets {
		t.Errorf("Coverage.Samples = %d for %d hourly buckets, want %d",
			rep.Coverage.Samples, buckets, buckets)
	}
}

// The rollup upsert computed its replacement as the average of the rows already
// in the bucket, which during an UPSERT is only the pre-update row. Every value
// in the statement except charge was not updated at all, so an hourly bucket
// permanently reported the first sample taken in that hour and ignored every
// later one. Usage derives its charge delta and its drain rate from exactly
// these columns.

func TestRollupFoldsInLaterSamplesInTheBucket(t *testing.T) {
	s := auditStore(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)

	for i, pct := range []float64{80, 60, 40} {
		if err := s.recordAt(ctx, Status{
			Present: true, ChargePct: float64(pct), ChargeKnown: true, AC: ACOffline,
		}, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("recordAt %d: %v", i, err)
		}
	}

	var charge float64
	if err := s.db.QueryRow(
		`SELECT charge FROM battery_rollups WHERE tier=? AND bucket=?`,
		int(TierHour), base.UnixMicro(),
	).Scan(&charge); err != nil {
		t.Fatalf("read hour rollup: %v", err)
	}

	// Any legitimate repair lands between "the mean of the three samples" (60)
	// and "the most recent sample" (40); a repair that keeps the existing row
	// returns 80, which is the bug. The bounds are deliberately loose so that
	// averaging, running-average and last-write-wins fixes all pass.
	if charge >= 80 {
		t.Errorf("hour rollup charge = %.1f, still the first sample; later samples in the bucket were discarded", charge)
	}
	if charge < 40 || charge > 80 {
		t.Errorf("hour rollup charge = %.1f, outside the range of the samples recorded (40..80)", charge)
	}
}

// ThisPeriod and Previous asked t.ISOWeek() for the week number but passed
// t.Year() as the year. In the first days of January the two disagree, because
// those days routinely belong to the previous ISO year. The range that comes
// back is not a slightly wrong week: on 1 January 2021 the From lands in 2022
// and the whole range is inverted, so every query against it returns nothing
// and the report reads as an empty history rather than as a failure.

func TestWeekPeriodUsesTheISOYearNotTheCalendarYear(t *testing.T) {
	cases := []time.Time{
		time.Date(2024, 12, 30, 12, 0, 0, 0, time.UTC),
		time.Date(2024, 12, 31, 12, 0, 0, 0, time.UTC),
		time.Date(2021, 1, 1, 12, 0, 0, 0, time.UTC),
		time.Date(2021, 1, 3, 12, 0, 0, 0, time.UTC),
		time.Date(2022, 1, 3, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
	}

	for _, now := range cases {
		isoYear, isoWeek := now.ISOWeek()
		want := isoWeekStart(isoYear, isoWeek)

		got := ThisPeriod(now, PeriodWeek)
		if !got.From.Equal(want) {
			t.Errorf("%s is ISO %d-W%02d; ThisPeriod week starts %s, want %s",
				now.Format("2006-01-02"), isoYear, isoWeek,
				got.From.Format("2006-01-02"), want.Format("2006-01-02"))
		}
		if !got.From.Before(got.To) {
			t.Errorf("%s: week range is not ordered, From=%s To=%s",
				now.Format("2006-01-02"), got.From, got.To)
		}

		// Previous must remain the contiguous half-open neighbour of the current
		// week, and must not report a week start later than the one it precedes.
		prev := PeriodWeek.Previous(now)
		if !prev.To.Equal(want) {
			t.Errorf("%s: previous week ends %s, want %s",
				now.Format("2006-01-02"), prev.To, want)
		}
		if !prev.From.Before(prev.To) {
			t.Errorf("%s: previous week range is not ordered, From=%s To=%s",
				now.Format("2006-01-02"), prev.From, prev.To)
		}
	}
}

// rate reported "power source changed, restarting window" but never restarted
// anything. The rejected samples stayed in the window forever, so one unplug or
// one plug-in left the estimator permanently unable to produce a rate, while
// the reason string promised a restart that never happened.

func TestRateRecoversAfterAPowerSourceChange(t *testing.T) {
	e := NewRateEstimator(time.Second)
	e.p = RateParams{MinSpan: time.Second, MinSamples: 3, Alpha: 0.25}

	// Three discharging samples, then the pack goes on mains.
	for i := 0; i < 3; i++ {
		e.Add(Sample{At: epoch.Add(time.Duration(i) * 2 * time.Second), Pct: 50 - float64(i), AC: ACOffline})
	}
	// A long, internally consistent run on mains after the transition.
	for i := 3; i < 20; i++ {
		e.Add(Sample{At: epoch.Add(time.Duration(i) * 2 * time.Second), Pct: 50 + float64(i), AC: ACOnline, Charge: true})
	}

	r := e.Rate()
	if !r.Stable {
		t.Errorf("the window never restarts after a power source change, so the estimator is dead for the life of the process: Stable=%v Samples=%d Reason=%q",
			r.Stable, r.Samples, r.Reason)
	}
}

// Attribute incremented fields on its own receiver while computing a result,
// so the same window attributed twice reported a different number of lost
// processes the second time. A caller that renders the attribution and also
// reports its coverage gets figures that drift on every render.

func TestAttributeIsIdempotent(t *testing.T) {
	at := epoch
	first := procSnapshot{at: at, nCPU: 1, procs: map[int32]procSample{
		1: {pid: 1, name: "steady", created: 1, cpuSeconds: 0},
	}}
	last := procSnapshot{at: at.Add(time.Minute), nCPU: 1, procs: map[int32]procSample{
		1: {pid: 1, name: "steady", created: 1, cpuSeconds: 3},
		3: {pid: 3, name: "regressed", created: 0, cpuSeconds: 50},
	}}
	first.procs[3] = procSample{pid: 3, name: "regressed", created: 0, cpuSeconds: 100}

	a := &Attributor{first: &first, last: &last}
	one := a.Attribute(50)
	two := a.Attribute(50)

	if one.ProcessesLost != two.ProcessesLost {
		t.Errorf("Attribute is not idempotent: ProcessesLost went from %d to %d across two identical calls",
			one.ProcessesLost, two.ProcessesLost)
	}
}

// A process that appeared during the window was skipped outright, although the
// code above it says its counters are already correct without subtraction. The
// effect is worse than dropping a row: every other application absorbs the
// whole of the window, the table reports a share of one, and Unattributed
// reports that nothing was missed.

func TestProcessStartedDuringTheWindowIsAttributed(t *testing.T) {
	at := epoch
	first := procSnapshot{at: at, nCPU: 4, procs: map[int32]procSample{
		1: {pid: 1, name: "incumbent", created: 1, cpuSeconds: 0},
	}}
	last := procSnapshot{at: at.Add(time.Minute), nCPU: 4, procs: map[int32]procSample{
		1: {pid: 1, name: "incumbent", created: 1, cpuSeconds: 240},
		2: {pid: 2, name: "spawnedmidwindow", created: 5, cpuSeconds: 240},
	}}

	got := (&Attributor{first: &first, last: &last}).Attribute(50)

	var total float64
	for _, app := range got.Apps {
		if app.Name == "spawnedmidwindow" {
			total += app.CPUSeconds
		}
	}
	if total == 0 {
		t.Errorf("a process that burned half the window's CPU is absent from the attribution; table was %+v", got.Apps)
	}
}

// Each process's normalised weight was clamped to one, but the per-application
// weight was recomputed from the unclamped folded totals. The two totals
// therefore disagree, a single multi-core process is credited with a share
// above one, and Unattributed comes out negative.

func TestShareStaysWithinZeroToOne(t *testing.T) {
	at := epoch
	first := procSnapshot{at: at, nCPU: 1, procs: map[int32]procSample{
		1: {pid: 1, name: "multicore", created: 1, cpuSeconds: 0},
	}}
	// Five cores' worth of CPU in a one-core window, so the per-process
	// normalisation saturates at one and the folded one does not.
	last := procSnapshot{at: at.Add(time.Minute), nCPU: 1, procs: map[int32]procSample{
		1: {pid: 1, name: "multicore", created: 1, cpuSeconds: 300},
	}}

	got := (&Attributor{first: &first, last: &last}).Attribute(50)

	for _, app := range got.Apps {
		if app.Share < 0 || app.Share > 1 {
			t.Errorf("app %q has Share=%.4f, outside the documented range 0 to 1", app.Name, app.Share)
		}
	}
	if got.Unattributed < 0 || got.Unattributed > 1 {
		t.Errorf("Unattributed = %.4f, outside the documented range 0 to 1", got.Unattributed)
	}
}
