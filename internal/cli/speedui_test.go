package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestSpeedBar(t *testing.T) {
	tests := []struct {
		name   string
		frac   float64
		want   string
		filled int
	}{
		{name: "empty", frac: 0, want: "◇◇◇◇◇◇◇◇◇◇◇◇◇◇◇◇", filled: 0},
		{name: "minimum", frac: 0.01, want: "◆◇◇◇◇◇◇◇◇◇◇◇◇◇◇◇", filled: 1},
		{name: "partial with padding", frac: 0.1, want: "◆◆◇◇◇◇◇◇◇◇◇◇◇◇◇◇", filled: 2},
		{name: "half", frac: 0.5, want: "◆◆◆◆◆◆◆◆◇◇◇◇◇◇◇◇", filled: 8},
		{name: "quarter", frac: 0.25, want: "◆◆◆◆◇◇◇◇◇◇◇◇◇◇◇◇", filled: 4},
		{name: "three eighths", frac: 0.375, want: "◆◆◆◆◆◆◇◇◇◇◇◇◇◇◇◇", filled: 6},
		{name: "five eighths", frac: 0.625, want: "◆◆◆◆◆◆◆◆◆◆◇◇◇◇◇◇", filled: 10},
		{name: "three quarters", frac: 0.75, want: "◆◆◆◆◆◆◆◆◆◆◆◆◇◇◇◇", filled: 12},
		{name: "seven eighths", frac: 0.875, want: "◆◆◆◆◆◆◆◆◆◆◆◆◆◆◇◇", filled: 14},
		{name: "full", frac: 1, want: "◆◆◆◆◆◆◆◆◆◆◆◆◆◆◆◆", filled: 16},
		{name: "above full", frac: 2, want: "◆◆◆◆◆◆◆◆◆◆◆◆◆◆◆◆", filled: 16},
		{name: "below empty", frac: -1, want: "◇◇◇◇◇◇◇◇◇◇◇◇◇◇◇◇", filled: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, filled := speedBar(tt.frac)
			if got != tt.want || filled != tt.filled {
				t.Fatalf("speedBar(%v) = %q (%d), want %q (%d)", tt.frac, got, filled, tt.want, tt.filled)
			}
		})
	}
}

func TestPrintSpeedTableRenders(t *testing.T) {
	rows := []speedResult{
		{label: "Ookla ping", ok: true, detail: "194 ms (jitter 12 ms)"},
		{label: "Ookla download", ok: true, detail: "47.10 Mbps"},
		{label: "Ookla upload", ok: true, detail: "45.16 Mbps"},
		{label: "Cloudflare ping", ok: true, detail: "52 ms (jitter 6 ms)"},
		{label: "Cloudflare download", ok: true, detail: "49.55 Mbps"},
		{label: "Cloudflare upload", ok: false, err: "connect: connection refused"},
	}
	var b bytes.Buffer
	printSpeedTable(&b, rows)
	out := b.String()
	for _, want := range []string{"Ookla", "Cloudflare", "FAIL", "jitter 6 ms", "CHEST SPEED TEST", barFilled} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "EXTRA") || strings.Contains(out, "MISSING") {
		t.Fatalf("format verb leak:\n%s", out)
	}
	t.Logf("%q", out)
}
