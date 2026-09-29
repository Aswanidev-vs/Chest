package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/cellwatch"
)

// ansiPattern matches the SGR colour escapes the palette emits, so a rendered
// line can be measured as the reader sees it rather than as bytes.
var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestColumnHeightRisesWithValue(t *testing.T) {
	// The whole point of a bar chart: a larger value is a taller column, so the
	// comparison lives in the shape rather than in a shade of block.
	short := column(0.25, true, 1, chartHeight)
	tall := column(1, true, 1, chartHeight)
	if filled(t, short) >= filled(t, tall) {
		t.Errorf("a quarter-height bar (%d cells) must be shorter than a full one (%d)",
			filled(t, short), filled(t, tall))
	}
	if filled(t, tall) != chartHeight {
		t.Errorf("the shared maximum should fill the column, got %d of %d", filled(t, tall), chartHeight)
	}
}

func TestColumnScalesAgainstTheSharedMaximum(t *testing.T) {
	// Half the shared max is half the height. Against a per-row scale, a heavy
	// and a light application would render identically.
	half := column(0.5, true, 1, chartHeight)
	if got := filled(t, half); got != chartHeight/2 {
		t.Errorf("half the max should be half height, got %d of %d", got, chartHeight)
	}
}

func TestColumnMarksUnrecordedAsDots(t *testing.T) {
	// A gap in coverage must be visible as a gap and must not be confused with
	// a recorded zero.
	got := column(0, false, 1, chartHeight)
	if filled(t, got) != 0 || got[chartHeight-1] != chartDot {
		t.Errorf("an unrecorded column should be all dots, got %q", got)
	}
}

func TestColumnRecordedZeroIsNotADot(t *testing.T) {
	// Recorded-and-zero is a measurement. Only unrecorded is a dot, otherwise
	// the dot would mean two different things.
	got := column(0, true, 1, chartHeight)
	if strings.Contains(strings.Join(got, ""), chartDot) {
		t.Errorf("a recorded zero must not render as a dot, got %q", got)
	}
	// A measured column always shows something, even at the very bottom.
	if got[chartHeight-1] == chartEmpty {
		t.Errorf("a recorded zero should sit on the baseline, got %q", got)
	}
}

func TestColumnGrowsUpFromTheBaseline(t *testing.T) {
	// A bar chart filled from the top would draw bars hanging from the ceiling.
	// The filled cells must be contiguous with the last row, which is the
	// baseline, so the check walks upward from the bottom.
	for _, frac := range []float64{0.01, 0.25, 0.5, 0.75, 1} {
		rows := column(frac, true, 1, chartHeight)
		if rows[chartHeight-1] == chartEmpty {
			t.Errorf("frac %v: bar does not reach the baseline, got %q", frac, rows)
			continue
		}
		gap := false
		for i := chartHeight - 1; i >= 0; i-- {
			if rows[i] == chartEmpty {
				gap = true
				continue
			}
			if gap {
				t.Errorf("frac %v: bar floats above the baseline, got %q", frac, rows)
				break
			}
		}
	}
}

func TestColumnWithNoMaxDrawsAnEmptyTrack(t *testing.T) {
	// Every recorded value is zero. A solid bar would read as activity where
	// there was none.
	got := column(0, true, 0, chartHeight)
	if filled(t, got) != 0 {
		t.Errorf("a zero maximum must draw no bar, got %q", got)
	}
	if strings.ContainsRune(strings.Join(got, ""), []rune(chartDot)[0]) {
		t.Errorf("a recorded column must not become a dot, got %q", got)
	}
}

// filled counts the solid cells in a rendered column, which is its height.
func filled(t *testing.T, rows []string) int {
	t.Helper()
	n := 0
	for _, r := range rows {
		if r == chartFull {
			n++
		}
	}
	return n
}

func TestColumnOfMapsOntoTheChartGrid(t *testing.T) {
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	span := 24 * time.Hour
	cols := 24
	if got := columnOf(from, from, span, cols); got != 0 {
		t.Errorf("the range start should map to column 0, got %d", got)
	}
	if got := columnOf(from.Add(12*time.Hour), from, span, cols); got != 12 {
		t.Errorf("the midpoint should map to column 12, got %d", got)
	}
	// The final instant belongs to the last column rather than falling off the
	// end, or the newest sample would be dropped from the chart.
	if got := columnOf(from.Add(24*time.Hour), from, span, cols); got != cols-1 {
		t.Errorf("the range end should map to the last column, got %d", got)
	}
	if got := columnOf(from.Add(-time.Hour), from, span, cols); got != -1 {
		t.Errorf("a sample before the range should be rejected, got %d", got)
	}
}

func TestBarShowsACellForAnyNonZeroValue(t *testing.T) {
	// Zero and "almost nothing" must not both render as empty.
	if got := bar(0.0001, 10); !strings.HasPrefix(got, chartFull) {
		t.Errorf("a tiny non-zero fraction should still show a cell, got %q", got)
	}
	if got := bar(0, 10); strings.Contains(got, chartFull) {
		t.Errorf("a zero fraction should be empty, got %q", got)
	}
}

func TestBarClampsFraction(t *testing.T) {
	for _, f := range []float64{-1, 2} {
		got := bar(f, 10)
		if n := strings.Count(got, chartFull) + strings.Count(got, chartEmpty); n != 10 {
			t.Errorf("bar(%v, 10) rendered %d cells, want exactly 10", f, n)
		}
	}
}

func TestChartSpanLabelNamesTheSpanInUTC(t *testing.T) {
	// The buckets are cut in UTC, so the label must be too. A local-time label
	// would disagree with the data by the machine's offset.
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	day := chartSpanLabel(cellwatch.Range{From: from, To: from.Add(24 * time.Hour)})
	if !strings.Contains(day, "15 Mar") || !strings.Contains(day, "UTC") {
		t.Errorf("a daily span should name its dates in UTC, got %q", day)
	}
	if strings.Count(day, "15:04") != 0 {
		t.Errorf("a multi-day span should not label clock times, got %q", day)
	}

	week := chartSpanLabel(cellwatch.Range{From: from, To: from.Add(7 * 24 * time.Hour)})
	if !strings.Contains(week, "22 Mar") {
		t.Errorf("a weekly span should name both end dates, got %q", week)
	}

	hours := chartSpanLabel(cellwatch.Range{From: from.Add(9 * time.Hour), To: from.Add(17 * time.Hour)})
	if !strings.Contains(hours, "09:00") || !strings.Contains(hours, "17:00") {
		t.Errorf("a sub-daily span should keep clock times, got %q", hours)
	}
}

func TestPrintAppChartRendersSeriesAndLabel(t *testing.T) {
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	series := map[string][]cellwatchPoint{
		"Code.exe": {
			{At: from.Add(2 * time.Hour), CPUSeconds: 1},
			{At: from.Add(10 * time.Hour), CPUSeconds: 5},
			{At: from.Add(14 * time.Hour), CPUSeconds: 3},
		},
	}
	var buf bytes.Buffer
	printAppChart(&buf, cellwatch.Range{From: from, To: to}, series, []string{"Code.exe"})
	out := buf.String()

	if !strings.Contains(out, "Code.exe") {
		t.Errorf("chart should name the application\n%s", out)
	}
	if !strings.Contains(out, "ESTIMATE") {
		t.Errorf("chart must label itself as an estimate\n%s", out)
	}
	if !strings.Contains(out, chartFull) {
		t.Errorf("chart should draw activity\n%s", out)
	}
	// The span must be named, and a partially recorded range must show the
	// gaps rather than closing them up.
	if !strings.Contains(out, "UTC") {
		t.Errorf("chart should label its span\n%s", out)
	}
	if !strings.Contains(out, chartDot) {
		t.Errorf("unrecorded columns should render as dots\n%s", out)
	}
}

func TestPrintAppChartRendersOneFixedWidthForEveryRow(t *testing.T) {
	// Rows must align, which they only do if every series is folded into the
	// same number of columns regardless of how many samples it has.
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	series := map[string][]cellwatchPoint{
		"many.exe": {
			{At: from.Add(time.Hour), CPUSeconds: 1},
			{At: from.Add(2 * time.Hour), CPUSeconds: 1},
			{At: from.Add(3 * time.Hour), CPUSeconds: 1},
		},
		"one.exe": {{At: from, CPUSeconds: 4}},
	}
	var buf bytes.Buffer
	printAppChart(&buf, cellwatch.Range{From: from, To: to}, series, []string{"many.exe", "one.exe"})
	plain := ansiPattern.ReplaceAllString(buf.String(), "")

	// Every block is the same width, so the bars line up down the screen. The
	// width follows the busiest series, because the column grid is shared.
	if !strings.Contains(plain, strings.Repeat(chartEmpty, 3)) {
		t.Errorf("a three-sample series should draw three columns:\n%s", plain)
	}
	if !strings.Contains(plain, "many.exe") || !strings.Contains(plain, "one.exe") {
		t.Errorf("both applications should be named:\n%s", plain)
	}
}

func TestPrintAppChartPlotsTheObservedWindowNotTheRequestedOne(t *testing.T) {
	// A day in which the sampler ran for two hours must not render as
	// twenty-two empty columns and two marks: the chart covers the data, and
	// says which part of the request that was.
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	series := map[string][]cellwatchPoint{
		"Code.exe": {
			{At: from.Add(22 * time.Hour), CPUSeconds: 1},
			{At: from.Add(23 * time.Hour), CPUSeconds: 5},
		},
	}
	var buf bytes.Buffer
	printAppChart(&buf, cellwatch.Range{From: from, To: from.Add(24 * time.Hour)},
		series, []string{"Code.exe"})
	plain := ansiPattern.ReplaceAllString(buf.String(), "")

	// Two samples draw two columns, not a chart mostly made of gaps.
	if strings.Contains(plain, strings.Repeat(chartEmpty, 3)) {
		t.Errorf("two adjacent samples should not leave a desert of gaps:\n%s", plain)
	}
	// Both windows are named, so the compression is disclosed rather than hidden.
	if !strings.Contains(plain, "22:00") {
		t.Errorf("the observed window should be labelled:\n%s", plain)
	}
	if !strings.Contains(plain, "15 Mar") {
		t.Errorf("the requested period should still be named:\n%s", plain)
	}
}

func TestColumnCountFollowsTheBusiestSeries(t *testing.T) {
	// The column count is the largest sample count, capped at the full width,
	// so rows share one time axis and recorded data is not spread over gaps.
	base := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	many := make([]cellwatchPoint, 30)
	for i := range many {
		many[i] = cellwatchPoint{At: base.Add(time.Duration(i) * time.Hour)}
	}
	series := map[string][]cellwatchPoint{"a.exe": many, "b.exe": many[:2]}
	if got := columnCount(series, []string{"a.exe", "b.exe"}); got != 30 {
		t.Errorf("columnCount = %d, want 30", got)
	}
	if got := columnCount(map[string][]cellwatchPoint{"a.exe": nil}, []string{"a.exe"}); got != 1 {
		t.Errorf("an empty series should still yield one column, got %d", got)
	}
}

func TestSpanLabelNamesBothEndsOfAMidnightToMidnightRange(t *testing.T) {
	// Labelling a day by clock time alone renders as "00:00 to 00:00", which is
	// the one span a reader cannot interpret.
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	got := spanLabel(from, from.Add(24*time.Hour))
	if strings.Contains(got, "to 00:00") {
		t.Errorf("a midnight-to-midnight span must be named by date, got %q", got)
	}
	if !strings.Contains(got, "15 Mar") || !strings.Contains(got, "16 Mar") {
		t.Errorf("both end dates should appear, got %q", got)
	}
}

func TestObservedSpanCoversTheEarliestAndLatestSample(t *testing.T) {
	base := time.Date(2026, 3, 15, 6, 0, 0, 0, time.UTC)
	series := map[string][]cellwatchPoint{
		"a.exe": {{At: base.Add(2 * time.Hour)}},
		"b.exe": {{At: base}, {At: base.Add(5 * time.Hour)}},
	}
	from, to, ok := observedSpan(series, []string{"a.exe", "b.exe"})
	if !ok {
		t.Fatal("expected an observed span")
	}
	if !from.Equal(base) || !to.Equal(base.Add(5*time.Hour)) {
		t.Errorf("span = %v to %v, want %v to %v", from, to, base, base.Add(5*time.Hour))
	}
}

func TestObservedSpanIsFalseWithNoSamples(t *testing.T) {
	if _, _, ok := observedSpan(map[string][]cellwatchPoint{"a.exe": nil}, []string{"a.exe"}); ok {
		t.Error("no samples must report no observed span")
	}
}

func TestPrintAppChartHandlesASingleReading(t *testing.T) {
	// One sample has no width to divide. It must render as one column rather
	// than as a fabricated timeline.
	at := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)
	var buf bytes.Buffer
	printAppChart(&buf, cellwatch.Range{From: at.Add(-time.Hour), To: at.Add(time.Hour)},
		map[string][]cellwatchPoint{"a.exe": {{At: at, CPUSeconds: 3}}}, []string{"a.exe"})
	plain := ansiPattern.ReplaceAllString(buf.String(), "")
	if !strings.Contains(plain, "single reading") {
		t.Errorf("a single sample should say so:\n%s", plain)
	}
	if !strings.Contains(plain, chartFull) {
		t.Errorf("a single sample should still draw its value:\n%s", plain)
	}
}

func TestCoverageNoteIsEmptyWhenFullyCovered(t *testing.T) {
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	r := cellwatch.Range{From: from, To: from.Add(24 * time.Hour)}
	if got := coverageNote(r, r.To); got != "" {
		t.Errorf("a fully covered period needs no caveat, got %q", got)
	}
	if got := coverageNote(r, from.Add(2*time.Hour)); got == "" {
		t.Error("a partly covered period should say so")
	}
}

func TestPrintAppChartSaysSoWhenThereIsNoSeries(t *testing.T) {
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	printAppChart(&buf, cellwatch.Range{From: from, To: from.Add(time.Hour)},
		map[string][]cellwatchPoint{"x.exe": nil}, []string{"x.exe"})
	if !strings.Contains(buf.String(), "no per-application history") {
		t.Errorf("an empty series should say so rather than draw nothing silently\n%s", buf.String())
	}
}

func TestItoa(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{{0, "0"}, {7, "7"}, {42, "42"}, {1234, "1234"}} {
		if got := itoa(tc.in); got != tc.want {
			t.Errorf("itoa(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
