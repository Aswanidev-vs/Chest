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

// sparkline renders values as a single row of block characters, one per column.
//
// Three arguments rather than one, because a chart that scales to its own
// maximum cannot be used to compare rows. Every series is drawn against a max
// shared by all of them, so an application that used a third of another's CPU
// looks like a third, and a dot means one thing only: nothing was recorded for
// that column. A per-row scale would render a light app and a heavy app
// identically, which is the same class of error as reporting a rate nobody
// measured.
func sparkline(values []float64, observed []bool, max float64) string {
	if len(values) == 0 {
		return ""
	}
	if max <= 0 {
		// Every value is zero. Drawn as dots rather than as a solid floor,
		// because a solid bar would read as activity where there was none.
		return strings.Repeat(chartDot, len(values))
	}
	var b strings.Builder
	for i, v := range values {
		if i < len(observed) && !observed[i] {
			b.WriteString(chartDot)
			continue
		}
		// Quantised to eighths so a small non-zero value still shows a mark: a
		// measured column must never be indistinguishable from an unrecorded
		// one, which is what the dot above now means.
		eighths := int(v / max * 8)
		if eighths < 1 {
			eighths = 1
		}
		if eighths > 8 {
			eighths = 8
		}
		if eighths == 8 {
			b.WriteString(chartFull)
			continue
		}
		// Partial blocks are the eighth-height range U+2588..U+258F.
		b.WriteRune(rune(0x2580 + 8 - eighths))
	}
	return b.String()
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
	span := r.To.Sub(r.From)
	fmt.Fprintf(w, "\n  %sACTIVITY OVER TIME%s  %s(ESTIMATE, from measured CPU and I/O; · = not recorded)%s\n",
		chestGold, chestReset, chestDim, chestReset)

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
		vals := make([]float64, chartWidth)
		obs := make([]bool, chartWidth)
		for _, p := range points {
			col := columnOf(p.At, r.From, span, chartWidth)
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

	any := false
	for _, name := range names {
		vals, ok := grid[name]
		if !ok {
			continue
		}
		any = true
		fmt.Fprintf(w, "  %s%-22s%s %s%s%s\n",
			chestDim, truncate(name, 22), chestReset,
			chestCyan, sparkline(vals, seen[name], max), chestReset)
	}
	if !any {
		fmt.Fprintf(w, "  %sno per-application history recorded for this period%s\n", chestDim, chestReset)
		return
	}
	// The axis is labelled rather than left to the reader's inference, because a
	// bare row of blocks does not say what span it covers.
	fmt.Fprintf(w, "  %s%s%s %speak %s s CPU per column%s\n",
		chestDim, chartSpanLabel(r), chestReset,
		chestCyan, itoa(int(max+0.5)), chestReset)
}

// chartSpanLabel names the horizontal extent of the chart, in UTC, because the
// buckets are cut in UTC and a local-time label would disagree with them by the
// machine's offset.
func chartSpanLabel(r cellwatch.Range) string {
	if r.To.Sub(r.From) >= 48*time.Hour {
		return r.From.Format("2 Jan") + " to " + r.To.Format("2 Jan") + " UTC"
	}
	return r.From.Format("2 Jan 15:04") + " to " + r.To.Format("15:04") + " UTC"
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
