package cli

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/Aswanidev-vs/chest/internal/indexer"
)

// progressBar renders an in-place terminal progress bar. It is a no-op when
// stdout is not a terminal, so piped/scripted output stays clean.
type progressBar struct {
	total   int
	current int
	width   int
	active  bool
	lastPct int
}

func newProgressBar(total int) *progressBar {
	p := &progressBar{total: total, width: 30}
	// Detect a TTY by attempting to read terminal size (same approach as
	// terminalWidth); on error stdout is not an interactive terminal.
	if _, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		p.active = true
		if tw := terminalWidth(); tw > 24 {
			if w := tw - 22; w > 12 && w < 60 {
				p.width = w
			}
		}
	}
	return p
}

// advance moves the bar to the given completed count, redrawing only when the
// percentage changes to avoid excessive terminal writes.
func (p *progressBar) advance(current int) {
	if !p.active || p.total <= 0 {
		return
	}
	p.current = current
	pct := int(float64(p.current) * 100 / float64(p.total))
	if pct == p.lastPct {
		return
	}
	p.lastPct = pct
	p.draw(pct, current)
}

func (p *progressBar) draw(pct, current int) {
	filled := p.width * pct / 100
	bar := strings.Repeat("█", filled) + strings.Repeat("░", p.width-filled)
	fmt.Fprintf(os.Stdout, "\r  \x1b[38;5;114m%s\x1b[0m \x1b[1;38;5;254m%3d%%\x1b[0m \x1b[38;5;246m(%d/%d)\x1b[0m",
		bar, pct, current, p.total)
}

// finish clears the progress line so the next output starts on a fresh line.
func (p *progressBar) finish() {
	if !p.active {
		return
	}
	fmt.Fprint(os.Stdout, "\r\x1b[2K")
}

// indexWithProgress runs IndexDirectory while showing a live progress bar. It
// computes an accurate total up-front with a fast, hashing-free count (which
// respects --except), then renders current/total during the real pass.
func indexWithProgress(store *indexer.Store, path string, computeHashes bool, exclusions ...[]string) (int, error) {
	total, _ := store.CountFiles(path, exclusions...)
	bar := newProgressBar(total)
	defer bar.finish()
	return store.IndexDirectory(path, computeHashes, func(curr int) {
		bar.advance(curr)
	}, exclusions...)
}
