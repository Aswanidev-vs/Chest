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

// sgrPattern captures an escape's parameters, which is how a colour set is told
// apart from the reset that closes it.
var sgrPattern = regexp.MustCompile("\x1b\\[([0-9;]*)m")

// barGlyphs are the marks a column is drawn with. A line made only of these and
// whitespace is a chart row; anything else is prose the reader sees around it.
const barGlyphs = "█▓▒░·▀▁▂▃▄▅▆▇"

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

func TestColumnRendersTheExpectedRunes(t *testing.T) {
	// The exact glyphs are pinned, not just the heights, because a bar chart
	// encodes a value in the shape of a cell. A partial block that fills from
	// the wrong end still counts as "one cell" to a height check, so only this
	// test can see it.
	for _, tc := range []struct {
		name     string
		v        float64
		observed bool
		max      float64
		want     []string
	}{
		{"the shared maximum is a solid column", 1, true, 1,
			[]string{chartFull, chartFull, chartFull, chartFull}},
		{"just under the maximum is full rows and a seven-eighth", 0.999, true, 1,
			[]string{"▇", chartFull, chartFull, chartFull}},
		{"three quarters fills three rows", 0.75, true, 1,
			[]string{chartEmpty, chartFull, chartFull, chartFull}},
		{"half fills two rows", 0.5, true, 1,
			[]string{chartEmpty, chartEmpty, chartFull, chartFull}},
		{"a quarter fills one row", 0.25, true, 1,
			[]string{chartEmpty, chartEmpty, chartEmpty, chartFull}},
		{"a tenth is a quarter cell", 0.1, true, 1,
			[]string{chartEmpty, chartEmpty, chartEmpty, "▃"}},
		{"a measured value never rounds away to nothing", 0.0000001, true, 1,
			[]string{chartEmpty, chartEmpty, chartEmpty, "▁"}},
		// A recorded zero and an unrecorded column are different facts, so the
		// zero must be the smallest thing that is still a bar, not a dot and
		// not an empty track.
		{"a recorded zero is the smallest bar", 0, true, 1,
			[]string{chartEmpty, chartEmpty, chartEmpty, "▁"}},
		// Cannot happen from a subtraction of non-negative samples, but if it
		// ever did, a negative bar must not draw below the baseline.
		{"a negative value is clamped to the baseline", -5, true, 1,
			[]string{chartEmpty, chartEmpty, chartEmpty, "▁"}},
		// With no maximum every recorded value is zero, which is the one case
		// where an empty track is the honest rendering.
		{"a zero maximum is an empty track", 0, true, 0,
			[]string{chartEmpty, chartEmpty, chartEmpty, chartEmpty}},
		{"an unrecorded column is dots", 0, false, 1,
			[]string{chartDot, chartDot, chartDot, chartDot}},
		// An unrecorded column stays unrecorded even when a maximum exists to
		// scale it against, because there is no value to scale.
		{"an unrecorded column ignores the maximum", 9, false, 1,
			[]string{chartDot, chartDot, chartDot, chartDot}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := column(tc.v, tc.observed, tc.max, chartHeight)
			if len(got) != len(tc.want) {
				t.Fatalf("column returned %d rows, want %d", len(got), len(tc.want))
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("row %d = %q, want %q (full column %q)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestColumnPartialBlocksFillFromTheBottom(t *testing.T) {
	// The regression this guards: the partial glyph was computed as 8-rem
	// instead of rem, so a one-eighth remainder drew a seven-eighth cell and a
	// seven-eighth remainder drew a one-eighth cell. Every partial bar in the
	// chart was drawn upside down, which overstated small readings and
	// understated large ones without changing any bar's height.
	//
	// Each case picks v so the value lands on exactly one eighth remainder
	// above a single full row: eighths = 8+rem, against a maximum that makes
	// the full column 4*8 = 32 eighths.
	for rem := 1; rem < 8; rem++ {
		v := float64(8+rem) / 32
		want := string(rune(0x2580 + rem))
		got := column(v, true, 1, chartHeight)

		// The partial cell is the topmost one, sitting on the single full row.
		if got[chartHeight-2] != want {
			t.Errorf("rem %d: partial cell = %q (U+%04X), want %q (U+%04X)",
				rem, got[chartHeight-2], []rune(got[chartHeight-2])[0], want, 0x2580+rem)
		}
		// And it must be a lower block in U+2581..U+2587, never the upper half
		// block (U+2580), never a full block (U+2588) and never a left block
		// (U+2589+), any of which would draw a cell that is the wrong shape.
		cp := []rune(got[chartHeight-2])[0]
		if cp < 0x2581 || cp > 0x2587 {
			t.Errorf("rem %d: partial cell is U+%04X, outside the lower-block range U+2581..U+2587", rem, cp)
		}
	}
}

func TestColumnPartialBlocksAddUpToTheValue(t *testing.T) {
	// The invariant a bar chart has to hold: a column is empty track, then at
	// most one partial cell, then a solid run down to the baseline -- and the
	// cells read as eighths must sum to the value.
	//
	// The old glyph was computed as 8-rem rather than rem, which left that sum
	// right (a 7/8 cell above a 1/8 cell is still one cell) and so passed every
	// height check while drawing each bar inside out. Only the shape of the
	// individual cells tells the two apart.
	for eighths := 1; eighths <= 32; eighths++ {
		rows := column(float64(eighths)/32, true, 1, chartHeight)

		total := 0
		for _, r := range rows {
			total += blockEighths(t, r)
		}
		if total != eighths {
			t.Errorf("eighths=%d: the column draws %d eighths, %q", eighths, total, rows)
		}

		// A value landing on a cell boundary has no partial; one that straddles
		// a boundary has exactly one. Two would mean the bar was drawn as two
		// overlapping steps.
		partialAt, partials := -1, 0
		for i, r := range rows {
			if n := blockEighths(t, r); n > 0 && n < 8 {
				partialAt, partials = i, partials+1
			}
		}
		if eighths%8 == 0 {
			if partials != 0 {
				t.Errorf("eighths=%d: got %d partial cells, want none on a cell boundary", eighths, partials)
			}
		} else if partials != 1 {
			t.Errorf("eighths=%d: got %d partial cells, want 1 (the value straddles a cell boundary)", eighths, partials)
		}

		if partialAt < 0 {
			continue
		}
		// The partial is the ceiling of the bar, never a notch in its middle.
		for i := 0; i < partialAt; i++ {
			if blockEighths(t, rows[i]) != 0 {
				t.Errorf("eighths=%d: row %d is drawn above the top of the bar, %q", eighths, i, rows)
			}
		}
		// And everything under it is solid, down to the baseline.
		for i := partialAt + 1; i < len(rows); i++ {
			if n := blockEighths(t, rows[i]); n != 8 {
				t.Errorf("eighths=%d: row %d below the partial is %d/8, want a solid cell, %q",
					eighths, i, n, rows)
			}
		}
	}
}

// blockEighths reports how much of a cell one rendered row fills, counting from
// the bottom, which is how every block element in the range is built.
func blockEighths(t *testing.T, row string) int {
	t.Helper()
	if row == chartDot {
		return -1
	}
	if row == chartEmpty {
		return 0
	}
	cp := []rune(row)[0]
	if cp == 0x2588 {
		return 8
	}
	// U+2581..U+2587 are the lower blocks, one eighth through seven eighths.
	// U+2580 is the *upper* half block and is never a legal partial here, so it
	// is rejected rather than silently counted as one eighth.
	if cp < 0x2581 || cp > 0x2587 {
		t.Fatalf("row %q is U+%04X, which is not a lower block", row, cp)
	}
	return int(cp - 0x2580)
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

func TestColumnOfClampsAndRejectsAtTheEdges(t *testing.T) {
	// Every edge of the grid is pinned here, because both failure modes are
	// silent: a sample that falls off the end is dropped from the chart
	// entirely, and one that is not rejected is drawn into a column it does not
	// belong to.
	base := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	for _, tc := range []struct {
		name string
		at   time.Time
		from time.Time
		span time.Duration
		cols int
		want int
	}{
		{"the first instant opens the first column", base, base, day, 24, 0},
		{"a nanosecond past the start stays in the first column",
			base.Add(time.Nanosecond), base, day, 24, 0},
		{"the halfway instant opens the middle column",
			base.Add(12 * time.Hour), base, day, 24, 12},
		{"the last instant inside the range fills the last column",
			base.Add(day - time.Nanosecond), base, day, 24, 23},
		// The end of the range is inclusive. A sample exactly at `to` is the
		// newest thing recorded, and dropping it would be the worst possible
		// failure: the chart would stop a column short of its own data.
		{"the range end is included in the last column", base.Add(day), base, day, 24, 23},
		{"an instant past the range end is clamped, not dropped",
			base.Add(day + 5*time.Hour), base, day, 24, 23},
		{"a sample before the range is rejected",
			base.Add(-time.Nanosecond), base, day, 24, -1},
		// A zero span means the chart has no width to divide, which callers
		// resolve by widening the span. Mapping onto a column anyway would place
		// a sample on an axis the chart never drew.
		{"a zero span rejects every sample", base, base, 0, 24, -1},
		{"a negative span rejects every sample", base, base, -time.Hour, 24, -1},
		{"a one-column grid holds the single reading", base, base, day, 1, 0},
		{"a one-column grid holds the range end too", base.Add(day), base, day, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := columnOf(tc.at, tc.from, tc.span, tc.cols); got != tc.want {
				t.Errorf("columnOf(%v, %v, %d) = %d, want %d",
					tc.at.Sub(tc.from), tc.span, tc.cols, got, tc.want)
			}
		})
	}
}

func TestColumnOfNeverEscapesTheGrid(t *testing.T) {
	// The clamp and the rejection together make the fold total: no instant
	// anywhere near the range can produce an index the caller would write out
	// of bounds, which is a panic rather than a wrong glyph.
	base := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	for _, cols := range []int{1, 2, 7, 24} {
		for m := -48; m <= 48; m++ {
			at := base.Add(time.Duration(m) * 30 * time.Minute)
			if got := columnOf(at, base, day, cols); got < -1 || got >= cols {
				t.Fatalf("columnOf(+%dm, day, %d) = %d, outside [-1,%d)", m, cols, got, cols)
			}
		}
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
	// same number of columns regardless of how many samples it has. This used
	// to look for a run of three ░ anywhere in the output and was satisfied by
	// the full-width baseline row, so it passed without ever comparing two
	// rows to each other.
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	series := map[string][]cellwatchPoint{
		"many.exe": {
			{At: from.Add(time.Hour), CPUSeconds: 1},
			{At: from.Add(2 * time.Hour), CPUSeconds: 1},
			{At: from.Add(3 * time.Hour), CPUSeconds: 1},
		},
		"one.exe": {{At: from, CPUSeconds: 4}},
	}
	var buf bytes.Buffer
	printAppChart(&buf, cellwatch.Range{From: from, To: from.Add(24 * time.Hour)}, series, []string{"many.exe", "one.exe"})
	plain := ansiPattern.ReplaceAllString(buf.String(), "")

	// The width follows the busiest series, because the column grid is shared:
	// three samples draw three columns, and the single-sample application is
	// padded out to three with dots rather than given a narrower row.
	const cols = 3
	rows := chartRows(plain)
	if len(rows) != 2*chartHeight {
		t.Fatalf("got %d chart rows, want %d:\n%s", len(rows), 2*chartHeight, plain)
	}
	for i, row := range rows {
		if n := len([]rune(row)) - chartRowIndent; n != cols {
			t.Errorf("chart row %d draws %d columns, want %d:\n%s", i, n, cols, plain)
		}
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

func TestSpanLabelNamesEachRangeOfLength(t *testing.T) {
	// Every branch of the label is pinned by its exact output, because the
	// failure this guards is a label that is confidently wrong rather than one
	// that is obviously broken.
	base := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	yearEnd := time.Date(2025, 12, 20, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		from time.Time
		to   time.Time
		want string
	}{
		// A zero-length span is the only one that is a single reading. Two
		// samples half a minute apart are two readings, and calling them one
		// is the sort of tidy-up this renderer does not do.
		{"no elapsed time is a single reading", base, base, "15 Mar 12:00 UTC, single reading"},
		// Under a minute the minute-resolution form would print both ends as
		// the same clock time, which is the "15:04 to 15:04" the day branch
		// exists to avoid.
		{"thirty seconds names both ends to the second",
			base, base.Add(30 * time.Second), "15 Mar 12:00:00 to 12:00:30 UTC"},
		{"fifty nine seconds is still under a minute",
			base, base.Add(59 * time.Second), "15 Mar 12:00:00 to 12:00:59 UTC"},
		// One minute is enough for the minute-resolution form to show two
		// different clock times, so seconds stop being needed.
		{"exactly one minute drops the seconds",
			base, base.Add(time.Minute), "15 Mar 12:00 to 12:01 UTC"},
		{"an hour keeps clock times", base, base.Add(time.Hour), "15 Mar 12:00 to 13:00 UTC"},
		// Twenty-three hours and fifty-nine minutes is the last span that the
		// minute form can render without repeating a clock time.
		{"just under a day keeps clock times",
			base, base.Add(23*time.Hour + 59*time.Minute), "15 Mar 12:00 to 11:59 UTC"},
		{"exactly a day is named by date", base, base.Add(24 * time.Hour), "15 Mar to 16 Mar UTC"},
		{"a week is named by date", base, base.Add(7 * 24 * time.Hour), "15 Mar to 22 Mar UTC"},
		{"within one year the year is omitted",
			base, time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), "15 Mar to 2 Nov UTC"},
		// Without the year, "20 Dec to 24 Jan" reads as a five-day span rather
		// than a year and a month.
		{"a span crossing New Year carries the year",
			yearEnd, yearEnd.Add(35 * 24 * time.Hour), "20 Dec 2025 to 24 Jan 2026 UTC"},
		{"a multi-year span carries both years",
			yearEnd, yearEnd.Add(400 * 24 * time.Hour), "20 Dec 2025 to 24 Jan 2027 UTC"},
		// An inverted range cannot come from observedSpan, but naming a
		// negative span would be worse than naming a zero-length one.
		{"an inverted range is treated as no elapsed time",
			base, base.Add(-time.Hour), "15 Mar 12:00 UTC, single reading"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := spanLabel(tc.from, tc.to); got != tc.want {
				t.Errorf("spanLabel(%v, %v) = %q, want %q", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestSpanLabelIsAlwaysUTC(t *testing.T) {
	// The buckets are cut in UTC, so a local-time label would disagree with
	// them by the machine's offset -- and a test that only passes in one
	// timezone is a test that lies the other half of the time. The instants
	// here arrive in a UTC+7 location, which is what the store hands back on a
	// machine set to one.
	zone := time.FixedZone("UTC+7", 7*3600)
	base := time.Date(2026, 3, 15, 12, 0, 0, 0, zone)
	for _, d := range []time.Duration{0, 30 * time.Second, time.Hour, 24 * time.Hour, 400 * 24 * time.Hour} {
		got := spanLabel(base, base.Add(d))
		if !strings.Contains(got, "UTC") {
			t.Errorf("spanLabel over %v in a UTC+7 zone = %q, missing the UTC marker", d, got)
		}
		// 12:00 in the zone is 05:00 UTC, and the label must show the latter.
		if strings.Contains(got, "12:00") {
			t.Errorf("spanLabel over %v in a UTC+7 zone = %q, used the local clock time", d, got)
		}
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

// renderAppChart draws a fixed set of applications over a day and returns the
// output with the colour escapes stripped, so a test can measure what a reader
// sees rather than what the byte stream contains.
func renderAppChart(t *testing.T, samples int, names []string) string {
	t.Helper()
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	series := make(map[string][]cellwatchPoint, len(names))
	for i, name := range names {
		pts := make([]cellwatchPoint, samples)
		for j := range pts {
			// Spread the samples across the day, offset per application so the
			// series do not land on identical columns.
			pts[j] = cellwatchPoint{
				At:         from.Add(time.Duration(j*24/samples+i) * time.Hour),
				CPUSeconds: float64(j%7) + 1,
			}
		}
		series[name] = pts
	}
	var buf bytes.Buffer
	printAppChart(&buf, cellwatch.Range{From: from, To: from.Add(24 * time.Hour)}, series, names)
	return ansiPattern.ReplaceAllString(buf.String(), "")
}

// chartRowIndent is where an application's cells begin: the two-space indent
// plus the label column, which is where the name is written on the top row.
const chartRowIndent = 2 + 22

// chartRows returns the rendered lines that are an application's cells and
// nothing but cells. The label column is skipped rather than tested, because
// the top row of each block carries the application's name in it, and the
// header and footer carry prose.
func chartRows(plain string) []string {
	var out []string
	for _, line := range strings.Split(plain, "\n") {
		r := []rune(line)
		if len(r) <= chartRowIndent {
			continue
		}
		cells, isRow := r[chartRowIndent:], true
		for _, c := range cells {
			if c != ' ' && !strings.ContainsRune(barGlyphs, c) {
				isRow = false
				break
			}
		}
		if isRow {
			out = append(out, line)
		}
	}
	return out
}

// barColumn reports the column a rendered chart row's first cell sits in, or -1
// if the row draws no cell at all.
func barColumn(row string) int {
	for i, r := range []rune(row) {
		if strings.ContainsRune(barGlyphs, r) {
			return i
		}
	}
	return -1
}

func TestPrintAppChartStartsEveryRowInTheSameColumn(t *testing.T) {
	// The label column is described as a fixed width so that the bars line up
	// straight down the screen. The name is printed on the top row of each
	// block and blank on the rows below, so the two have to pad to the same
	// visible width: one spare column and every block leans left.
	names := []string{"Code.exe", "chrome.exe", "a-very-long-application-name.exe"}
	rows := chartRows(renderAppChart(t, 6, names))
	if len(rows) != len(names)*chartHeight {
		t.Fatalf("got %d chart rows, want %d (%d applications x %d)",
			len(rows), len(names)*chartHeight, len(names), chartHeight)
	}
	want := chartRowIndent
	for i, row := range rows {
		if col := barColumn(row); col != want {
			t.Errorf("chart row %d starts its bars in column %d, want %d\nrow: %q",
				i, col, want, row)
		}
	}
}

func TestPrintAppChartKeepsRowsInsideTheTerminalFallback(t *testing.T) {
	// A chart row is a two-space indent, a 22-column label and at most
	// chartWidth cells. 48 columns has to survive terminalWidth's 80-column
	// fallback, since the renderer cannot ask a bytes.Buffer how wide it is.
	plain := renderAppChart(t, 6, []string{"Code.exe", "chrome.exe"})
	const maxRow = 2 + 22 + chartWidth
	rows := chartRows(plain)
	if len(rows) == 0 {
		t.Fatal("no chart rows were rendered")
	}
	for i, row := range rows {
		if n := len([]rune(row)); n > maxRow {
			t.Errorf("chart row %d is %d columns wide, over the %d-column bound\n%q",
				i, n, maxRow, row)
		}
	}
}

func TestPrintAppChartCapsTheColumnCountAtChartWidth(t *testing.T) {
	// The width follows the data up to a ceiling, so a month of samples folds
	// into the same 24 columns a day of them does. Without the cap the label
	// plus cells would run off a standard terminal.
	plain := renderAppChart(t, 30, []string{"busy.exe"})
	rows := chartRows(plain)
	if len(rows) != chartHeight {
		t.Fatalf("got %d chart rows, want %d", len(rows), chartHeight)
	}
	if n := len([]rune(rows[0])); n != 2+22+chartWidth {
		t.Errorf("thirty samples rendered %d columns, want the %d-column cap", n, 2+22+chartWidth)
	}
	// The count is stated, because a width that follows the sample count means
	// a cell covers a different amount of time on every run.
	if !strings.Contains(plain, "over 24 columns") {
		t.Errorf("the column count should be reported:\n%s", plain)
	}
}

func TestPrintAppChartDrawsNoBaselineRow(t *testing.T) {
	// ░ is what a measured-but-empty column draws. Printed as a full-width row
	// under an application it is indistinguishable from a row of recorded
	// zeros, which is a claim the renderer has no evidence for. The row is
	// gone; what is left is one blank line between blocks.
	plain := renderAppChart(t, 6, []string{"Code.exe", "chrome.exe"})
	rows := chartRows(plain)
	if got := len(rows); got != 2*chartHeight {
		t.Errorf("got %d chart rows, want %d -- a baseline row has crept back in\n%s",
			got, 2*chartHeight, plain)
	}
	// The two blocks are separated by a genuinely empty line, not by glyphs.
	if !strings.Contains(plain, "\n\n") {
		t.Errorf("blocks should be separated by a blank line:\n%q", plain)
	}
}

func TestPrintAppChartBalancesEveryColourEscape(t *testing.T) {
	// A reset with no colour set before it is harmless but meaningless, and a
	// colour with no reset after it bleeds into whatever the caller prints
	// next. The baseline row used to emit exactly the first of those.
	names := []string{"Code.exe", "chrome.exe"}
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	series := map[string][]cellwatchPoint{
		"Code.exe": {
			{At: from.Add(2 * time.Hour), CPUSeconds: 1},
			{At: from.Add(9 * time.Hour), CPUSeconds: 5},
		},
		"chrome.exe": {{At: from, CPUSeconds: 2}},
	}
	var buf bytes.Buffer
	// A partly covered range, so the coverage note is on the footer too.
	printAppChart(&buf, cellwatch.Range{From: from, To: from.Add(24 * time.Hour)},
		series, names)

	for i, line := range strings.Split(buf.String(), "\n") {
		sets, resets := 0, 0
		for _, m := range sgrPattern.FindAllStringSubmatch(line, -1) {
			if m[1] == "0" {
				resets++
			} else {
				sets++
			}
		}
		if sets != resets {
			t.Errorf("line %d sets colour %d times but resets %d times:\n%q", i, sets, resets, line)
		}
	}
}

func TestBarHandlesAZeroWidthWithoutPanicking(t *testing.T) {
	// The "any non-zero value shows a cell" rule would otherwise ask
	// strings.Repeat for a negative count.
	for _, f := range []float64{0, 0.5, 1} {
		if got := bar(f, 0); got != "" {
			t.Errorf("bar(%v, 0) = %q, want an empty string", f, got)
		}
	}
}
