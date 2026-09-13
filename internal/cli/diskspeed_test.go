package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiskMBs(t *testing.T) {
	tests := []struct {
		name string
		n    int64
		secs float64
		want float64
	}{
		{name: "simple", n: 1_000_000, secs: 1, want: 1},
		{name: "zero bytes", n: 0, secs: 1, want: 0},
		{name: "zero time", n: 1_000_000, secs: 0, want: 0},
		{name: "negative time", n: 1_000_000, secs: -1, want: 0},
		{name: "10MB/s", n: 10_000_000, secs: 1, want: 10},
		{name: "fast", n: 500_000_000, secs: 0.5, want: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diskMBs(tt.n, tt.secs); got != tt.want {
				t.Fatalf("diskMBs(%d, %v) = %v, want %v", tt.n, tt.secs, got, tt.want)
			}
		})
	}
}

func TestMetricOfDisk(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{"Disk read", "read"},
		{"Disk write", "write"},
		{"Disk 4K read", "4K read"},
		{"Disk 4K write", "4K write"},
		{"Ookla download", "download"},
		{"Cloudflare ping", "ping"},
	}
	for _, tt := range tests {
		if got := metricOf(tt.label); got != tt.want {
			t.Fatalf("metricOf(%q) = %q, want %q", tt.label, got, tt.want)
		}
	}
}

func TestPrintSpeedTableIncludesDisk(t *testing.T) {
	rows := []speedResult{
		{label: "Ookla download", ok: true, detail: "47.10 Mbps"},
		{label: "Disk read", ok: true, detail: "1240.55 MB/s"},
		{label: "Disk write", ok: true, detail: "880.12 MB/s"},
		{label: "Disk 4K read", ok: true, detail: "4521 IOPS"},
		{label: "Disk 4K write", ok: false, err: "permission denied"},
	}
	var b bytes.Buffer
	printSpeedTable(&b, rows)
	out := b.String()
	for _, want := range []string{"Disk", "1240.55 MB/s", "880.12 MB/s", "4521 IOPS", "FAIL", "CHEST SPEED TEST", "Network + Disk"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}
}

// TestDiskBenchSmall runs the real benchmark helpers on a tiny file in a
// temporary dir. It asserts the helpers complete and report sane positive
// throughput without panicking and without leaking the test file.
func TestDiskBenchSmall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench.bin")
	const total = 4 << 20 // 4 MiB: warm-up writes 2 MiB, measured 4 MiB

	w, err := diskSeqWrite(path, total)
	if err != nil {
		t.Fatalf("diskSeqWrite: %v", err)
	}
	if w <= 0 {
		t.Fatalf("seq write MB/s = %v, want > 0", w)
	}

	r, err := diskSeqRead(path, total)
	if err != nil {
		t.Fatalf("diskSeqRead: %v", err)
	}
	if r <= 0 {
		t.Fatalf("seq read MB/s = %v, want > 0", r)
	}

	w4k, r4k := diskRandom4K(path, total)
	if w4k <= 0 {
		t.Fatalf("4K write IOPS = %v, want > 0", w4k)
	}
	if r4k <= 0 {
		t.Fatalf("4K read IOPS = %v, want > 0", r4k)
	}
}
