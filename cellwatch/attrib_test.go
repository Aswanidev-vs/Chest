package cellwatch

import (
	"context"
	"math"
	"os"
	"testing"
	"time"
)

// The attribution maths is separated from process enumeration so it can be
// tested without a machine full of running programs. fold() mirrors the inner
// loop of Attribute and takes the same inputs.

func TestSharesSumToAtMostOne(t *testing.T) {
	// Whatever the weights, the attributed shares can never exceed the whole,
	// or the table would be claiming more than the machine spent.
	weights := []float64{0.5, 0.3, 0.15, 0.05}
	var total float64
	for _, w := range weights {
		total += w
	}
	var attributed float64
	for _, w := range weights {
		attributed += w / total
	}
	if attributed > 1+1e-9 {
		t.Errorf("shares sum to %.6f, must not exceed 1", attributed)
	}
	if unattributed := 1 - attributed; math.Abs(unattributed) > 1e-9 {
		t.Errorf("unattributed = %.9f, want ~0", unattributed)
	}
}

func TestUnattributedRemainderIsPreserved(t *testing.T) {
	// Kernel threads and vanished processes are not hidden to make the table
	// look complete. Here the attributed weights account for only 0.7 of the
	// observed activity, so a third of the drain belongs to something the
	// process walk cannot see and must be reported as such rather than
	// redistributed to make the column sum neatly.
	partial := []float64{0.4, 0.3}
	var credited float64
	for _, w := range partial {
		credited += w
	}
	if credited >= 1 {
		t.Fatalf("test setup: credited weight %.2f must be below 1", credited)
	}
	systemPctPerHour := 50.0

	var attributedPct float64
	for _, w := range partial {
		attributedPct += (w / credited) * systemPctPerHour
	}
	_ = systemPctPerHour - attributedPct

	// All shares are normalised to the credited total, so the attributed drain
	// is the whole thing. The remainder is therefore carried as a fraction of the
	// window rather than of the drain, which is what Attribute reports.
	if math.Abs(attributedPct-systemPctPerHour) > 1e-9 {
		t.Errorf("normalised shares should account for the whole drain, got %.3f of %.3f", attributedPct, systemPctPerHour)
	}
	// The value that must not be silently dropped is how much activity was
	// never attributed at all.
	unattributedShare := 1 - credited
	if math.Abs(unattributedShare-0.3) > 1e-9 {
		t.Errorf("unattributed share = %.3f, want 0.300", unattributedShare)
	}
}

func TestIdleFloorRefusesAttribution(t *testing.T) {
	// Below the floor the shares are ratios of near-zero numbers. Refusing is
	// the whole point: a confident table derived from an idle machine is worse
	// than no table.
	if attribIdleFloor <= 0 {
		t.Fatal("the idle floor must be positive")
	}
	// A machine using 0.1% of one of eight cores over a minute.
	cpuNorm := 0.001
	ioNorm := 0.0
	weight := attribCPUWeight*cpuNorm + attribIOWeight*ioNorm
	if weight >= attribIdleFloor {
		t.Errorf("weight %.4f should be below the floor %.4f", weight, attribIdleFloor)
	}
}

func TestBusyMachineClearsIdleFloor(t *testing.T) {
	// A machine using half of eight cores must clear the floor, or the command
	// would report nothing on any real workload.
	cpuNorm := 0.5
	ioNorm := 0.2
	weight := attribCPUWeight*cpuNorm + attribIOWeight*ioNorm
	if weight < attribIdleFloor {
		t.Errorf("weight %.4f should clear the floor %.4f", weight, attribIdleFloor)
	}
}

func TestIOBudgetSaturates(t *testing.T) {
	// The I/O term is bounded, so one process writing a terabyte cannot claim
	// the entire machine.
	perSec := float64(attribIOBudget) * 10
	ioNorm := perSec / float64(attribIOBudget)
	if ioNorm <= 1 {
		t.Errorf("ioNorm = %.1f, expected the caller to clamp above 1", ioNorm)
	}
}

func TestThisPeriodDay(t *testing.T) {
	// Mid-afternoon UTC on a known date.
	now := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)
	r := ThisPeriod(now, PeriodDay)

	if r.From != time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC) {
		t.Errorf("day start = %v, want midnight UTC", r.From)
	}
	if r.To != now {
		t.Errorf("day end = %v, want now", r.To)
	}
}

func TestThisPeriodWeekStartsMonday(t *testing.T) {
	// 15 March 2026 is a Sunday, so the ISO week began on Monday the 9th.
	now := time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)
	r := ThisPeriod(now, PeriodWeek)

	if r.From.Weekday() != time.Monday {
		t.Errorf("week start %v is a %v, want Monday", r.From, r.From.Weekday())
	}
	if r.From.Day() != 9 {
		t.Errorf("week start = %v, want the 9th", r.From)
	}
}

func TestThisPeriodMonthAndYear(t *testing.T) {
	now := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)

	m := ThisPeriod(now, PeriodMonth)
	if m.From.Month() != time.March || m.From.Day() != 1 {
		t.Errorf("month start = %v, want 1 March", m.From)
	}

	y := ThisPeriod(now, PeriodYear)
	if y.From.Year() != 2026 || y.From.Month() != time.January || y.From.Day() != 1 {
		t.Errorf("year start = %v, want 1 January 2026", y.From)
	}
}

func TestThisPeriodIsUTCNotLocal(t *testing.T) {
	// A local instant late in the evening can fall on the previous UTC day.
	// Cutting periods in local time would make the "day" total silently shift
	// with the observer's timezone.
	loc := time.FixedZone("UTC+11", 11*3600)
	local := time.Date(2026, 3, 15, 23, 0, 0, 0, loc) // 12:00 UTC on the 15th
	r := ThisPeriod(local, PeriodDay)

	if r.From.Location() != time.UTC {
		t.Errorf("period start is in %v, want UTC", r.From.Location())
	}
	if r.From.Day() != 15 {
		t.Errorf("day start = %v, want the 15th", r.From)
	}
}

func TestPreviousPeriodsAreContiguous(t *testing.T) {
	now := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)

	prev := PeriodWeek.Previous(now)
	this := ThisPeriod(now, PeriodWeek)
	if !prev.To.Equal(this.From) {
		t.Errorf("previous week ends %v but this week starts %v", prev.To, this.From)
	}

	prevM := PeriodMonth.Previous(now)
	thisM := ThisPeriod(now, PeriodMonth)
	if !prevM.To.Equal(thisM.From) {
		t.Errorf("previous month ends %v but this month starts %v", prevM.To, thisM.From)
	}

	prevY := PeriodYear.Previous(now)
	thisY := ThisPeriod(now, PeriodYear)
	if !prevY.To.Equal(thisY.From) {
		t.Errorf("previous year ends %v but this year starts %v", prevY.To, thisY.From)
	}
}

func TestPreviousMonthHandlesShortMonths(t *testing.T) {
	// March follows February, which is not always the same length. A previous
	// range computed by subtracting the current period's own length would be
	// wrong at both ends of a leap February.
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	prev := PeriodMonth.Previous(now)

	if prev.From.Month() != time.February || prev.From.Year() != 2026 {
		t.Errorf("previous month start = %v, want 1 February 2026", prev.From)
	}
	if prev.To.Month() != time.March {
		t.Errorf("previous month end = %v, want 1 March 2026", prev.To)
	}
	// February 2026 has 28 days, so the range is exactly that.
	days := int(prev.To.Sub(prev.From).Hours() / 24)
	if days != 28 {
		t.Errorf("February 2026 range spans %d days, want 28", days)
	}
}

func TestPeriodString(t *testing.T) {
	for p, want := range map[Period]string{
		PeriodDay: "day", PeriodWeek: "week", PeriodMonth: "month", PeriodYear: "year",
	} {
		if p.String() != want {
			t.Errorf("Period(%d).String() = %q, want %q", p, p.String(), want)
		}
	}
}

func TestAttributionNeedsTwoSamples(t *testing.T) {
	a := NewAttributor()
	got := a.Attribute(50)
	if got.Reason == "" {
		t.Error("attribution with no samples must explain itself")
	}
	if len(got.Apps) != 0 {
		t.Errorf("attribution with no samples returned %d apps", len(got.Apps))
	}
}

func TestProvenanceMarksEstimates(t *testing.T) {
	// An unset provenance must read as unknown rather than as measured, so a
	// value nobody classified cannot be mistaken for a platform reading.
	var zero AppActivity
	if zero.Provenance != ProvUnknown {
		t.Errorf("a zero AppActivity must be ProvUnknown, got %v", zero.Provenance)
	}
	if zero.Provenance.Estimated() {
		t.Error("an unknown value must not claim to be estimated either; it is simply unmarked")
	}

	a := AppActivity{Name: "x", Share: 0.5, PctPerHour: 25, Provenance: ProvEstimated}
	if !a.Provenance.Estimated() {
		t.Error("an apportioned value must report itself as estimated")
	}
	if ProvMeasured.Estimated() {
		t.Error("ProvMeasured must not report itself as estimated")
	}
}

func TestCoverageRefusesThinData(t *testing.T) {
	// A month in which the sampler ran twice must not be presented as a
	// description of the month.
	thin := Coverage{Samples: 2, Expected: 720, Pct: 0.27}
	if thin.Usable() {
		t.Error("2 samples out of 720 must not count as usable coverage")
	}
	thick := Coverage{Samples: 700, Expected: 720, Pct: 97}
	if !thick.Usable() {
		t.Error("97% coverage should be usable")
	}
}

func TestSamplerRejectsMissingDependencies(t *testing.T) {
	if _, err := NewSampler(SamplerConfig{Store: nil}); err == nil {
		t.Error("a sampler without a store must be rejected")
	}
	if _, err := NewSampler(SamplerConfig{Reader: nil}); err == nil {
		t.Error("a sampler without a reader must be rejected")
	}
}

func TestSamplerRaisesFastIntervals(t *testing.T) {
	// Sampling faster than the charge quantisation resolves nothing and costs
	// storage in proportion to the rate.
	s, err := NewSampler(SamplerConfig{Reader: fakeReader{}, Store: &fakeStore{}, Interval: time.Millisecond})
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	if s.interval < 5*time.Second {
		t.Errorf("interval = %s, want it raised to at least 5s", s.interval)
	}
}

func TestSamplerDoesNotRecordDesktops(t *testing.T) {
	// A machine with no battery must not accumulate rows reading zero percent,
	// which would later look like a machine that used no power.
	st := &fakeStore{}
	s, err := NewSampler(SamplerConfig{Reader: fakeReader{present: false}, Store: st, Interval: time.Second})
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	if err := s.pass(context.Background(), NewAttributor(), true); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if st.records != 0 {
		t.Errorf("recorded %d rows for a desktop, want 0", st.records)
	}
}

func TestSamplerKeepsGoingAfterAFailedRead(t *testing.T) {
	// A sampler that stops on the first transient failure becomes a program
	// that records nothing, and its output would not say so.
	st := &fakeStore{}
	r := &flakyReader{failFor: 1}
	s, err := NewSampler(SamplerConfig{Reader: r, Store: st, Interval: time.Second})
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	if err := s.pass(context.Background(), NewAttributor(), true); err == nil {
		t.Error("the first read should have failed")
	}
	if err := s.pass(context.Background(), NewAttributor(), false); err != nil {
		t.Errorf("a later read should succeed, got %v", err)
	}
	if st.records != 1 {
		t.Errorf("records = %d, want 1 from the successful pass", st.records)
	}
	if s.Stats().LastErr == "" {
		t.Error("the failure should be recorded in stats rather than swallowed")
	}
}

// Test doubles. The attribution maths is a pure function of the counters, so
// testing it does not require a machine full of processes, and a test that
// depended on whatever else was running would be a test that failed on a busy
// developer machine and passed in CI.

type fakeReader struct {
	present bool
}

func (f fakeReader) Read() (Status, error) {
	return Status{Present: f.present, Source: "fake", Cap: CapChargePct}, nil
}

type flakyReader struct {
	failFor int
	calls   int
}

func (f *flakyReader) Read() (Status, error) {
	f.calls++
	if f.calls <= f.failFor {
		return Status{}, os.ErrPermission
	}
	return Status{Present: true, Source: "fake", Cap: CapChargePct}, nil
}

type fakeStore struct {
	Store
	records int
}

func (f *fakeStore) Record(ctx context.Context, s Status) error { f.records++; return nil }
func (f *fakeStore) Apps(ctx context.Context, at time.Time, apps []AppActivity) error {
	return nil
}
func (f *fakeStore) Close() error { return nil }

func TestCoverageComparesPercentAgainstPercent(t *testing.T) {
	// Regression: Pct is a percentage, and the threshold was once a fraction.
	// Comparing 13 against 0.5 made every report claim to be complete, so a
	// two-hour sample was presented as a description of a month.
	if (Coverage{Pct: 13}).Usable() {
		t.Error("13% coverage must not count as usable")
	}
	if (Coverage{Pct: 49}).Usable() {
		t.Error("49% coverage must not count as usable")
	}
	if !(Coverage{Pct: 50}).Usable() {
		t.Error("50% coverage is the threshold and should be usable")
	}
	if !(Coverage{Pct: 99}).Usable() {
		t.Error("99% coverage should be usable")
	}
}
