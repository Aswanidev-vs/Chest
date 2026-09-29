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

func TestSparklineScalesToASharedMaximum(t *testing.T) {
	// Every row is drawn against one max so rows compare. A series scaled to
	// its own maximum would make a heavy application and a light one identical.
	got := sparkline([]float64{0.1, 0.2, 0.15, 0.3}, []bool{true, true, true, true}, 0.3)
	if len([]rune(got)) != 4 {
		t.Errorf("sparkline should render one cell per value, got %q", got)
	}
	if !strings.Contains(got, chartFull) {
		t.Errorf("the shared maximum should render as a full block, got %q", got)
	}

	// Half the max must render at about half height, not full height.
	half := sparkline([]float64{0.15}, []bool{true}, 0.3)
	if strings.Contains(half, chartFull) {
		t.Errorf("half of the shared max must not render as full, got %q", half)
	}
}

func TestSparklineRendersAllZerosAsDots(t *testing.T) {
	// A solid floor would read as activity where there was none, which is the
	// exact confusion the honesty contract exists to prevent.
	got := sparkline([]float64{0, 0, 0}, []bool{true, true, true}, 0)
	if strings.ContainsAny(got, chartFull) {
		t.Errorf("an all-zero series must not draw a solid block, got %q", got)
	}
	if got != strings.Repeat(chartDot, 3) {
		t.Errorf("all-zero series = %q, want three dots", got)
	}
}

func TestSparklineDistinguishesIdleFromEmpty(t *testing.T) {
	// A series of 0.02 and a series of 0.0 must not look identical.
	idle := sparkline([]float64{0.02, 0.02, 0.02}, []bool{true, true, true}, 0.02)
	empty := sparkline([]float64{0, 0, 0}, []bool{true, true, true}, 0)
	if idle == empty {
		t.Errorf("a small non-zero series (%q) must differ from all-zero (%q)", idle, empty)
	}
}

func TestSparklineMarksUnrecordedColumnsAsDots(t *testing.T) {
	// A gap in coverage must be visible as a gap, and must not be confused
	// with a recorded zero.
	got := sparkline([]float64{1, 0, 1}, []bool{true, false, true}, 1)
	cells := []rune(got)
	if len(cells) != 3 {
		t.Fatalf("want three cells, got %q", got)
	}
	if cells[1] != []rune(chartDot)[0] {
		t.Errorf("an unrecorded column should be a dot, got %q", got)
	}
	if cells[0] == []rune(chartDot)[0] || cells[2] == []rune(chartDot)[0] {
		t.Errorf("recorded columns must not be dots, got %q", got)
	}
}

func TestSparklineRecordedZeroIsNotADot(t *testing.T) {
	// Recorded-and-zero is a measurement. Only unrecorded is a dot, otherwise
	// the dot would mean two different things.
	got := sparkline([]float64{0}, []bool{true}, 1)
	if strings.ContainsRune(got, []rune(chartDot)[0]) {
		t.Errorf("a recorded zero should still show a mark, got %q", got)
	}
}

func TestSparklineEmptyInput(t *testing.T) {
	if got := sparkline(nil, nil, 1); got != "" {
		t.Errorf("no values should render nothing, got %q", got)
	}
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

	widths := map[int]bool{}
	for _, line := range strings.Split(buf.String(), "\n") {
		if !strings.Contains(line, "many.exe") && !strings.Contains(line, "one.exe") {
			continue
		}
		// Colour escapes contain no spaces but sit inside the field, so the
		// line is stripped before the chart cell is measured.
		plain := ansiPattern.ReplaceAllString(line, "")
		fields := strings.Fields(plain)
		if len(fields) < 2 {
			t.Fatalf("chart row did not render a name and a chart: %q", line)
		}
		widths[len([]rune(fields[len(fields)-1]))] = true
	}
	if len(widths) != 1 {
		t.Errorf("every chart row must be the same width, got widths %v\n%s", widths, buf.String())
	}
	if !widths[chartWidth] {
		t.Errorf("chart rows should be %d columns wide, got %v", chartWidth, widths)
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
