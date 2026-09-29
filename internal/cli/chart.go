package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Aswanidev-vs/chest/cellwatch"
)

// Terminal charts for recorded history.
//
// The project's existing visual vocabulary is the diamond gauge from
// speedui.go, reused unchanged, and a palette defined once in speedtest.go.
// These charts extend that vocabulary with two elements: a block for area and
// a column for a single bar. Nothing here invents a colour or a border style,
// and no escape sequence is used that the project did not already use, so a
// `chest battery --week` screen sits beside `chest speedtest` without looking
// like it came from a different tool.
//
// The charts carry the same honesty contract as the numbers beside them. A
// series with one point is drawn as a single mark and labelled as such, rather
// than stretched across a row to imply a shape that was never observed. A
// series with no points draws nothing at all, because an empty chart and a
// chart of zeros are different facts.

// Chart glyphs. Both are ordinary Unicode rather than box-drawing characters,
// because the gauge already established that this project renders with
// simple, widely supported marks.
const (
	chartFull  = "█"
	chartEmpty = "░"
	chartDot   = "·"
)

// chartWidth is the number of columns a chart occupies, and therefore the
// number of buckets every series is folded into. The width is fixed rather than
// derived from the data so that a chart drawn from two samples and a chart drawn
// from two hundred are the same size: a chart that grows with the sample count
// draws a sparse day as large as a busy one, which is precisely the confusion
// the coverage warning above it exists to prevent.
const chartWidth = 24

// chartHeight is how many rows tall the vertical bars are drawn. Four is the
// smallest height at which a bar chart still reads as columns rather than as a
// row of blocks, and it keeps three applications to a dozen lines.
const chartHeight = 4

// column renders one vertical bar as height strings, top row first.
//
// A bar is drawn from the baseline upward, so a taller value is a taller column
// rather than a different character in a single row. That is the whole point of
// the change: with one row per series, three applications whose activity is
// spread evenly over a day differ only in the shade of a block, and a reader has
// to trust the peak figure to tell them apart. Stacked columns put the
// comparison in the shape itself.
//
// Partial bars are drawn with the eighth-height block range so that a value
// between two rows is still visible, and an unrecorded column is a dot down its
// full height, which is the one thing the chart cannot honestly fill in.
func column(v float64, observed bool, max float64, height int) []string {
	rows := make([]string, height)
	if !observed {
		for i := range rows {
			rows[i] = chartDot
		}
		return rows
	}
	if max <= 0 {
		// Every recorded value is zero. Drawn as an empty track rather than a
		// solid bar, because a bar would read as activity where there was none.
		for i := range rows {
			rows[i] = chartEmpty
		}
		return rows
	}
	// Height in eighths of a cell, so the value can be finer than one row.
	eighths := int(v / max * float64(height) * 8)
	if eighths < 1 {
		// A measured column must never be blank, or it reads as unrecorded.
		eighths = 1
	}
	full := eighths / 8
	rem := eighths % 8
	// rows[0] is the top of the chart and rows[height-1] sits on the baseline,
	// so the bar is filled from the bottom upward. Filling from the top would
	// draw a tall bar hanging from the ceiling, which is the one thing a bar
	// chart must never look like.
	for i := 0; i < height; i++ {
		// How many cells of this row are covered, counted from the baseline.
		level := height - i
		switch {
		case level <= full:
			rows[i] = chartFull
		case level == full+1 && rem > 0:
			// Partial blocks are the eighth-height range U+2588..U+258F.
			rows[i] = string(rune(0x2580 + 8 - rem))
		default:
			rows[i] = chartEmpty
		}
	}
	return rows
}

// columnOf maps an instant onto the chart column that covers it. Columns divide
// the range into equal parts rather than aligning to the store's buckets, so the
// same code draws an hour, a day, a week and a year without knowing which.
func columnOf(at, from time.Time, span time.Duration, cols int) int {
	if span <= 0 || at.Before(from) {
		return -1
	}
	col := int(float64(at.Sub(from)) / float64(span) * float64(cols))
	if col >= cols {
		return cols - 1
	}
	return col
}

// bar renders one horizontal bar, the same shape as the speedtest gauge but
// filled, for a value that is a proportion of a known maximum.
func bar(fraction float64, width int) string {
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction * float64(width))
	// A non-zero value shorter than one cell still shows one cell, for the same
	// reason sparkline does: zero and "almost nothing" must not look alike.
	if filled == 0 && fraction > 0 {
		filled = 1
	}
	return strings.Repeat(chartFull, filled) + strings.Repeat(chartEmpty, width-filled)
}

// printAppChart draws a per-application time series under a top-apps table.
//
// Every application is folded into the same fixed set of columns covering the
// reported range, and every row is drawn against one shared maximum. Both
// choices exist to make the rows comparable to each other, which is the only
// reason to put several of them in one block: a per-row scale makes a light
// application and a heavy one look identical, and a width that follows the
// sample count makes a sparse day look as busy as a dense one.
//
// Columns with no recorded sample render as a dot, so a gap is visible as a gap
// rather than being silently closed up.
func printAppChart(w io.Writer, r cellwatch.Range, series map[string][]cellwatchPoint, names []string) {
	if len(names) == 0 {
		return
	}
	fmt.Fprintf(w, "\n  %sACTIVITY OVER TIME%s  %s(ESTIMATE, from measured CPU and I/O; · = not recorded)%s\n",
		chestGold, chestReset, chestDim, chestReset)
	fmt.Fprintf(w, "  %seach block is one column, tallest is the peak of all rows%s\n", chestDim, chestReset)

	// The chart is drawn over the window that was actually observed, not over the
	// period that was asked for. A day in which the sampler ran for two hours
	// would otherwise render as twenty-two dots and two marks: honest, and
	// useless, because the shape the reader came for is squeezed into the last
	// two cells. The unobserved remainder is not hidden by this -- the label
	// below states both windows and the coverage between them.
	from, to, ok := observedSpan(series, names)
	if !ok {
		fmt.Fprintf(w, "  %sno per-application history recorded for this period%s\n", chestDim, chestReset)
		return
	}
	span := to.Sub(from)
	if span <= 0 {
		// A single reading has no width to divide. One column wide is the
		// honest rendering: the alternative is a fabricated timeline.
		span = time.Nanosecond
	}

	// The column count follows the data, up to the full width. A fixed 24
	// columns would draw two recorded hours as two marks separated by a desert
	// of dots, which is the same uselessness in a new place. Rows still share
	// one column count, so they stay aligned and comparable.
	cols := columnCount(series, names)
	if cols > chartWidth {
		cols = chartWidth
	}

	// Fold every series into the shared column grid, tracking which columns
	// actually carry a sample so an unrecorded column stays distinguishable
	// from a recorded zero.
	grid := make(map[string][]float64, len(names))
	seen := make(map[string][]bool, len(names))
	var max float64
	for _, name := range names {
		points := series[name]
		if len(points) == 0 {
			continue
		}
		vals := make([]float64, cols)
		obs := make([]bool, cols)
		for _, p := range points {
			col := columnOf(p.At, from, span, cols)
			if col < 0 {
				continue
			}
			vals[col] += p.CPUSeconds
			obs[col] = true
		}
		grid[name] = vals
		seen[name] = obs
		for _, v := range vals {
			if v > max {
				max = v
			}
		}
	}

	// One row of characters per value is replaced by a block of rows per
	// application, so that the height of a column carries the comparison.
	//
	// The label column is a fixed width so that every application's bars begin
	// in the same terminal column, which is what makes the blocks comparable
	// straight down the screen rather than by eye.
	const labelW = 22
	any := false
	for _, name := range names {
		vals, ok := grid[name]
		if !ok {
			continue
		}
		any = true
		cells := make([][]string, len(vals))
		for i, v := range vals {
			obs := i < len(seen[name]) && seen[name][i]
			cells[i] = column(v, obs, max, chartHeight)
		}
		for row := 0; row < chartHeight; row++ {
			var b strings.Builder
			for _, c := range cells {
				b.WriteString(c[row])
			}
			// The name is printed once, level with the top of its own bars.
			label := strings.Repeat(" ", labelW)
			if row == 0 {
				label = fmt.Sprintf("%s%-*s%s ", chestDim, labelW-2, truncate(name, labelW-2), chestReset)
			}
			fmt.Fprintf(w, "  %s%s%s%s\n", label, chestCyan, b.String(), chestReset)
		}
		// Each application carries its own baseline, so a block reads as a
		// standalone chart rather than as a fragment of a shared one.
		fmt.Fprintf(w, "  %s%s%s\n", strings.Repeat(" ", labelW),
			strings.Repeat(chartEmpty, len(vals)), chestReset)
	}
	if !any {
		return
	}

	// Both windows are named. The observed one is what the chart shows; the
	// requested one is what the reader asked for, and the difference between
	// them is the whole reason a sparse chart is worth printing at all.
	fmt.Fprintf(w, "  %s%s%s %speak %s s CPU per column%s",
		chestDim, spanLabel(from, to), chestReset,
		chestCyan, itoa(int(max+0.5)), chestReset)
	if cov := coverageNote(r, to); cov != "" {
		fmt.Fprintf(w, "  %s· %s%s", chestDim, cov, chestReset)
	}
	fmt.Fprintln(w)
}

// observedSpan returns the earliest and latest instant carrying a sample across
// the named series, so the chart covers the data rather than the request.
func observedSpan(series map[string][]cellwatchPoint, names []string) (from, to time.Time, ok bool) {
	for _, name := range names {
		for _, p := range series[name] {
			if !ok {
				from, to, ok = p.At, p.At, true
				continue
			}
			if p.At.Before(from) {
				from = p.At
			}
			if p.At.After(to) {
				to = p.At
			}
		}
	}
	return from, to, ok
}

// coverageNote says how much of the requested period the chart accounts for, and
// is empty when the chart covers all of it.
func coverageNote(r cellwatch.Range, observedTo time.Time) string {
	total := r.To.Sub(r.From)
	if total <= 0 {
		return ""
	}
	observed := observedTo.Sub(r.From)
	if observed >= total {
		return ""
	}
	pct := int(float64(observed) / float64(total) * 100)
	return "chart covers the observed " + itoa(pct) + "% of " + chartSpanLabel(r)
}

// columnCount is the number of columns the chart should draw: as many as the
// busiest series has samples, so that recorded data is not spread across empty
// cells. It is the largest count across all series, which is what keeps the rows
// aligned to one shared time axis.
func columnCount(series map[string][]cellwatchPoint, names []string) int {
	n := 0
	for _, name := range names {
		if len(series[name]) > n {
			n = len(series[name])
		}
	}
	if n < 1 {
		n = 1
	}
	return n
}

// spanLabel names the extent of the chart in UTC, because the buckets are cut in
// UTC and a local-time label would disagree with them by the machine's offset.
func spanLabel(from, to time.Time) string {
	d := to.Sub(from)
	// A span of a day or more is named by date on both ends. Labelling a
	// midnight-to-midnight range by clock time alone renders as "00:00 to
	// 00:00", which is the one span a reader cannot interpret.
	if d >= 24*time.Hour {
		return from.Format("2 Jan") + " to " + to.Format("2 Jan") + " UTC"
	}
	if d < time.Minute {
		return from.Format("2 Jan 15:04 UTC") + ", single reading"
	}
	return from.Format("2 Jan 15:04") + " to " + to.Format("15:04") + " UTC"
}

// chartSpanLabel names a requested period, in UTC, for comparison against the
// window the chart actually covers.
func chartSpanLabel(r cellwatch.Range) string {
	return spanLabel(r.From, r.To)
}

// itoa avoids pulling strconv in for a single call in a renderer.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// cellwatchPoint is the subset of a history point a chart needs. It is
// declared locally rather than imported so the renderer can be tested without
// constructing a store.
type cellwatchPoint struct {
	At         time.Time
	CPUSeconds float64
}
