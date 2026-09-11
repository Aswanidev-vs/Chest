package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Forward-declared color constants shared with speedtest.go (same package).

var speedSpin *spinner

const (
	barFilled = "\u25c6"
	barEmpty  = "\u25c7"
)

// printSpin starts an in-place spinner with the given message on errOut,
// replacing any previous spinner between phases.
func printSpin(errOut io.Writer, msg string) {
	clearSpin(errOut)
	speedSpin = newSpinner(errOut, func() string { return msg })
}

// clearSpin stops the active spinner and clears its line.
func clearSpin(errOut io.Writer) {
	if speedSpin != nil {
		speedSpin.stop()
		speedSpin = nil
	}
}

// printSpeedTable renders the final comparison report: a banner, one grouped
// section per backend, and a legend. Colored via the shared palette.
func printSpeedTable(w io.Writer, rows []speedResult) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(w)

	// Banner.
	inner := 32
	row := func(text, color string) {
		pad := (inner - len([]rune(text))) / 2
		if pad < 0 {
			pad = 0
		}
		fmt.Fprintf(w, "  %s|%s%s%s%s%s%s%s|%s\n",
			chestDim, chestReset, color, strings.Repeat(" ", pad), text,
			strings.Repeat(" ", inner-pad-len([]rune(text))), chestReset, chestDim, chestReset)
	}
	fmt.Fprintf(w, "  %s+%s%s+%s\n", chestDim, chestReset, strings.Repeat("-", inner), chestDim)
	row("CHEST SPEED TEST", chestPrimary)
	row("Ookla vs Cloudflare", chestDim)
	fmt.Fprintf(w, "  %s+%s%s+%s\n", chestDim, chestReset, strings.Repeat("-", inner), chestDim)

	// Scale for the bar gauge: fastest transfer speed across all rows.
	var maxMbps float64
	for _, r := range rows {
		if r.ok && (strings.Contains(strings.ToLower(r.label), "download") ||
			strings.Contains(strings.ToLower(r.label), "upload")) {
			var v float64
			fmt.Sscanf(r.detail, "%f", &v)
			if v > maxMbps {
				maxMbps = v
			}
		}
	}

	// One section per backend, preserving the order the tests actually ran.
	seen := map[string]bool{}
	for _, r := range rows {
		backend := backendOf(r.label)
		if seen[backend] {
			continue
		}
		seen[backend] = true
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  %s-- %s %s\n", chestGold, backend, chestDim+strings.Repeat("-", 46-len(backend))+chestReset)
		for _, rr := range rows {
			if backendOf(rr.label) != backend {
				continue
			}
			printSpeedRow(w, rr, maxMbps)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %sOokla      = multi-stream vs a nearby speedtest server (accurate).%s\n", chestDim, chestReset)
	fmt.Fprintf(w, "  %sCloudflare = multi-stream vs speed.cloudflare.com (reference).%s\n", chestDim, chestReset)
	fmt.Fprintf(w, "  %sCompare the two - don't average across different backends.%s\n", chestDim, chestReset)
}

// backendOf extracts the backend name ("Ookla"/"Cloudflare") from a row label.
func backendOf(label string) string {
	lower := strings.ToLower(label)
	switch {
	case strings.Contains(lower, "ookla"):
		return "Ookla"
	case strings.Contains(lower, "cloudflare"):
		return "Cloudflare"
	default:
		if i := strings.Index(label, " "); i > 0 {
			return label[:i]
		}
		return label
	}
}

// metricOf reduces a row label to its metric name ("ping"/"download"/...).
func metricOf(label string) string {
	lower := strings.ToLower(label)
	for _, m := range []string{"download", "upload", "ping"} {
		if strings.Contains(lower, m) {
			return m
		}
	}
	return "test"
}

// printSpeedRow renders one measurement line: metric, right-aligned value,
// an optional bar gauge (transfer rows), and dim detail (jitter / errors).
func printSpeedRow(w io.Writer, r speedResult, maxMbps float64) {
	const barW = 16
	metric := metricOf(r.label)

	if !r.ok {
		msg := r.err
		if msg == "" {
			msg = "failed"
		}
		fmt.Fprintf(w, "  %-9s %s%-10s%s  %s%-38s%s\n",
			metric, chestRed, "FAIL", chestReset, chestRed, truncate(msg, 38), chestReset)
		return
	}

	val := r.detail
	detail := ""
	// Ping rows: split "57 ms (jitter 2 ms)" into value + jitter detail.
	if metric == "ping" {
		if i := strings.Index(val, "("); i >= 0 {
			detail = strings.Trim(val[i:], "()")
			val = strings.TrimSpace(val[:i])
		}
	}

	bar := strings.Repeat(" ", barW+2)
	if metric == "download" || metric == "upload" {
		var v float64
		fmt.Sscanf(r.detail, "%f", &v)
		fraction := 0.0
		if maxMbps > 0 {
			fraction = v / maxMbps
		}
		bar, _ = speedBar(fraction)
		bar = "[" + chestPrimary + bar + chestReset + "]"
	}

	fmt.Fprintf(w, "  %-9s %s%10s%s  %s  %s%s%s\n",
		metric, chestPrimary, val, chestReset, bar, chestDim, detail, chestReset)
}

func speedBar(fraction float64) (string, int) {
	const barW = 16
	if fraction < 0 {
		fraction = 0
	} else if fraction > 1 {
		fraction = 1
	}
	if fraction > 0 && fraction < 1.0/float64(barW) {
		fraction = 1.0 / float64(barW)
	}

	rawFilled := fraction * float64(barW)
	occupied := int(rawFilled)
	if rawFilled > float64(occupied) {
		occupied++
	}

	return strings.Repeat(barFilled, occupied) + strings.Repeat(barEmpty, barW-occupied), occupied
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "."
}

// printSpeedJSON writes the results as machine-readable JSON.
func printSpeedJSON(w io.Writer, rows []speedResult) {
	type jrow struct {
		Method string   `json:"method"`
		OK     bool     `json:"ok"`
		Mbps   *float64 `json:"mbps,omitempty"`
		Ms     *int64   `json:"ms,omitempty"`
		Error  string   `json:"error,omitempty"`
	}
	out := make([]jrow, 0, len(rows))
	for _, r := range rows {
		j := jrow{Method: r.label, OK: r.ok, Error: r.err}
		lower := strings.ToLower(r.label)
		if strings.Contains(lower, "download") || strings.Contains(lower, "upload") {
			var v float64
			fmt.Sscanf(r.detail, "%f", &v)
			j.Mbps = &v
		} else if strings.Contains(lower, "ping") {
			var v int64
			fmt.Sscanf(r.detail, "%d", &v)
			j.Ms = &v
		}
		out = append(out, j)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func mbps(bytes int64, secs float64) float64 {
	if secs <= 0 {
		return 0
	}
	return float64(bytes) * 8 / (secs * 1_000_000)
}

func realBytes(n int64) int64 { return n }

// zeroReader yields exactly n zero bytes without allocating them up-front, so
// parallel uploads of large payloads don't consume gigabytes of RAM.
type zeroReader struct{ n int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > z.n {
		p = p[:z.n]
	}
	for i := range p {
		p[i] = 0
	}
	z.n -= int64(len(p))
	return len(p), nil
}
