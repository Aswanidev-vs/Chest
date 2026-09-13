package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"
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

// TestDirectBufferAligned verifies newAlignedBuf returns a view whose start
// address is aligned, which FILE_FLAG_NO_BUFFERING and O_DIRECT require.
func TestDirectBufferAligned(t *testing.T) {
	align := uintptr(4096)
	b := newAlignedBuf(1<<20, 4096)
	if len(b) != 1<<20 {
		t.Fatalf("len = %d, want %d", len(b), 1<<20)
	}
	if alignUp(uintptr(unsafe.Pointer(&b[0])), align) != uintptr(unsafe.Pointer(&b[0])) {
		t.Fatalf("buffer start %p is not %d-byte aligned", &b[0], align)
	}
}

func alignUp(addr, align uintptr) uintptr { return (addr + align - 1) &^ (align - 1) }

// TestDiskBenchSmall runs the real benchmark helpers on a tiny file in a
// temporary dir. It asserts the helpers complete and report sane positive
// throughput without panicking and without leaking the test file.
func TestDiskBenchSmall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench.bin")
	const total = 4 << 20 // 4 MiB: warm-up writes 2 MiB, measured 4 MiB

	// Open the file the same way runDisk does, falling back to buffered I/O
	// on platforms without direct I/O, so the test works everywhere.
	f, err := openDirectWrite(path)
	if err != nil {
		f, err = os.Create(path)
	}
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	buf := newAlignedBuf(diskBlockSize, directSector())
	w, err := diskSeqWrite(f, total, buf)
	if err != nil {
		t.Fatalf("diskSeqWrite: %v", err)
	}
	if w <= 0 {
		t.Fatalf("seq write MB/s = %v, want > 0", w)
	}
	f.Close()

	rf, err := openDirectRead(path)
	if err != nil {
		rf, err = os.Open(path)
	}
	if err != nil {
		t.Fatalf("read open: %v", err)
	}
	r, err := diskSeqRead(rf, total, newAlignedBuf(diskBlockSize, directSector()))
	if err != nil {
		t.Fatalf("diskSeqRead: %v", err)
	}
	if r <= 0 {
		t.Fatalf("seq read MB/s = %v, want > 0", r)
	}
	rf.Close()

	wf, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer wf.Close()
	w4k, r4k := diskRandom4K(wf, total, newAlignedBuf(disk4KSize, directSector()), int64(directSector()))
	if w4k <= 0 {
		t.Fatalf("4K write IOPS = %v, want > 0", w4k)
	}
	if r4k <= 0 {
		t.Fatalf("4K read IOPS = %v, want > 0", r4k)
	}
}
