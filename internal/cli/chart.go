package cli

import (
	"fmt"
	"io"
	"strconv"
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

// chartWidth is the most columns a chart may occupy, and therefore the most
// buckets a series is ever folded into. It is a ceiling, not a width: the width
// itself follows the data and is clipped to this, because a fixed twenty-four
// columns would draw two recorded hours as two marks separated by a desert of
// dots. The ceiling is what stops the renderer growing without bound. A chart
// row is a two-space indent, the fixed labelW column of label, and at most this
// many cells, so 2+22+24 is the widest line the bars can produce -- comfortably
// inside terminalWidth's eighty-column fallback, with no need for the renderer
// to consult the terminal at all, which it could not honour anyway when w is
// not the terminal.
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
			// The partial cell is the lower-block range U+2581..U+2587, where
			// U+2581 (▁) is one eighth filled and U+2587 (▇) is seven. The step
			// is therefore +rem, not 8-rem: the older form drew a one-eighth
			// remainder as a nearly full cell and a seven-eighth remainder as a
			// nearly empty one, inverting every partial bar in the chart.
			//
			// The base is U+2580, not U+257F: U+257F is a triangle, and
			// U+2580 (▀) is the *upper* half block, so off-by-one here renders
			// the smallest remainder as a bar hanging from the top of its cell.
			// U+2588 (█) is a full block, which `full` already emits, and
			// U+2589 upwards are *left* blocks, which would slice the bar
			// sideways.
			rows[i] = string(rune(0x2580 + rem))
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
	if width < 1 {
		// A zero-width bar has no cell to draw, and the "any non-zero value
		// shows a cell" rule below would otherwise ask strings.Repeat for a
		// negative count.
		return ""
	}
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
// Every application is folded into one shared set of columns covering the
// observed range, and every row is drawn against one shared maximum. Both
// choices exist to make the rows comparable to each other, which is the only
// reason to put several of them in one block: a per-row scale makes a light
// application and a heavy one look identical, and a per-row column count would
// leave the rows of one block disagreeing about where the time boundaries fall.
//
// One consequence of the shared width is that a cell covers a different amount
// of time on different days, since the number of cells follows the number of
// samples. The column count is printed beside the observed window for that
// reason, because the two together -- and nothing else -- fix the resolution.
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
	// straight down the screen rather than by eye. The width counts visible
	// characters only: the escape sequences around the name are not columns,
	// and a row that pads to a different count than the blank rows below it
	// leans the whole block sideways.
	const labelW = 22
	any := false
	for _, name := range names {
		vals, ok := grid[name]
		if !ok {
			continue
		}
		// One blank line between applications, and nothing after the last.
		//
		// This used to be a full-width row of ░, which is worse than nothing
		// twice over. ░ is the glyph a measured-but-empty column draws, so as
		// decoration it is indistinguishable from a row of recorded zeros, and
		// it was written without a colour set before a reset, so it emitted an
		// escape sequence that turned nothing off. Whitespace separates the
		// blocks without claiming to be data.
		if any {
			fmt.Fprintln(w)
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
				label = fmt.Sprintf("%s%-*s%s%s", chestDim, labelW-2,
					truncate(name, labelW-2), chestReset, strings.Repeat(" ", 2))
			}
			fmt.Fprintf(w, "  %s%s%s%s\n", label, chestCyan, b.String(), chestReset)
		}
	}
	if !any {
		return
	}

	// Both windows are named. The observed one is what the chart shows; the
	// requested one is what the reader asked for, and the difference between
	// them is the whole reason a sparse chart is worth printing at all. The
	// column count goes with them, since a width that follows the sample count
	// means a cell covers a different amount of time on every run.
	fmt.Fprintf(w, "  %s%s over %d columns%s %stallest %s s CPU%s",
		chestDim, spanLabel(from, to), cols, chestReset,
		chestCyan, strconv.Itoa(int(max+0.5)), chestReset)
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
	return "chart covers the observed " + strconv.Itoa(pct) + "% of " + chartSpanLabel(r)
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
// The instants are converted rather than formatted as they arrive: the store
// hands back whatever location its samples were written in, and a label that
// quietly inherited it would read 12:00 on a machine seven hours east of the
// buckets it is describing.
func spanLabel(from, to time.Time) string {
	from, to = from.UTC(), to.UTC()
	d := to.Sub(from)
	if d < 0 {
		// Only an inverted range can produce one. Naming a negative span would
		// be worse than naming a zero-length one.
		d = 0
	}
	// A span of a day or more is named by date on both ends. Labelling a
	// midnight-to-midnight range by clock time alone renders as "00:00 to
	// 00:00", which is the one span a reader cannot interpret.
	if d >= 24*time.Hour {
		// The year is carried only when the two ends disagree about one, so a
		// span crossing New Year cannot be misread as a five-day one.
		if from.Year() != to.Year() {
			return from.Format("2 Jan 2006") + " to " + to.Format("2 Jan 2006") + " UTC"
		}
		return from.Format("2 Jan") + " to " + to.Format("2 Jan") + " UTC"
	}
	// "single reading" is true of a zero-length span and of nothing else. Two
	// samples half a minute apart are two readings, and merging them into one
	// is the sort of tidy-up this renderer does not do.
	if d == 0 {
		return from.Format("2 Jan 15:04 UTC") + ", single reading"
	}
	// Under a minute, the minute-resolution form would print both ends as the
	// same clock time -- "15:04 to 15:04", which is precisely the uninterpretable
	// span the day branch above exists to avoid. Seconds are shown instead.
	if d < time.Minute {
		return from.Format("2 Jan 15:04:05") + " to " + to.Format("15:04:05") + " UTC"
	}
	return from.Format("2 Jan 15:04") + " to " + to.Format("15:04") + " UTC"
}

// chartSpanLabel names a requested period, in UTC, for comparison against the
// window the chart actually covers.
func chartSpanLabel(r cellwatch.Range) string {
	return spanLabel(r.From, r.To)
}

// cellwatchPoint is the subset of a history point a chart needs. It is
// declared locally rather than imported so the renderer can be tested without
// constructing a store.
type cellwatchPoint struct {
	At         time.Time
	CPUSeconds float64
}
