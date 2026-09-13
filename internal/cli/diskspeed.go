package cli

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"time"
)

// Disk benchmark tunables. Sizes are deliberately modest so a stray run on a
// busy machine does not hammer the storage device; --disk-size can raise the
// test-file size up to maxDiskSizeMiB.
const (
	defaultDiskSizeMiB = 256
	maxDiskSizeMiB     = 8192
	diskBlockSize      = 1 << 20 // 1 MiB sequential transfer chunk
	disk4KSize         = 4096
	diskRandRegionMiB  = 64   // random I/O targets this region within the file
	diskRandOps        = 1024 // random 4K operations per direction
)

// runDisk benchmarks sequential read/write throughput and random 4K IOPS on
// the target directory, appending results to rows under the "Disk" backend.
// It always uses a private temp file that is guaranteed removed afterwards.
// Pass 0 for sizeMiB to use the default; pass "" for dir to use the OS temp.
func runDisk(errOut io.Writer, rows *[]speedResult, dir string, sizeMiB int64) {
	if sizeMiB <= 0 {
		sizeMiB = defaultDiskSizeMiB
	}
	if sizeMiB > maxDiskSizeMiB {
		fmt.Fprintf(errOut, "  %sDisk size capped at %d MiB%s\n", chestDim, maxDiskSizeMiB, chestReset)
		sizeMiB = maxDiskSizeMiB
	}
	if dir == "" {
		dir = os.TempDir()
	}

	f, err := os.CreateTemp(dir, "chest-disk-*.tmp")
	if err != nil {
		*rows = append(*rows, speedResult{label: "Disk", ok: false, err: fmt.Sprintf("create temp: %v", err)})
		return
	}
	path := f.Name()
	_ = f.Close()
	defer os.Remove(path)
	total := sizeMiB << 20

	printSpin(errOut, fmt.Sprintf("Disk: writing %d MiB...", sizeMiB))
	seqMBs, wErr := diskSeqWrite(path, total)
	clearSpin(errOut)
	if wErr != nil {
		*rows = append(*rows, speedResult{label: "Disk write", ok: false, err: wErr.Error()})
	} else {
		fmt.Fprintf(errOut, "  %sWrite:%s %8.2f MB/s%s\n", chestPrimary, chestReset, seqMBs, chestReset)
		*rows = append(*rows, speedResult{label: "Disk write", ok: true, detail: fmt.Sprintf("%.2f MB/s", seqMBs)})
	}

	printSpin(errOut, "Disk: reading back...")
	seqRMBs, rErr := diskSeqRead(path, total)
	clearSpin(errOut)
	if rErr != nil {
		*rows = append(*rows, speedResult{label: "Disk read", ok: false, err: rErr.Error()})
	} else {
		fmt.Fprintf(errOut, "  %sRead:%s  %8.2f MB/s%s\n", chestPrimary, chestReset, seqRMBs, chestReset)
		*rows = append(*rows, speedResult{label: "Disk read", ok: true, detail: fmt.Sprintf("%.2f MB/s", seqRMBs)})
	}

	printSpin(errOut, "Disk: random 4K read/write...")
	r4k, w4k := diskRandom4K(path, total)
	if r4k > 0 {
		fmt.Fprintf(errOut, "  %s4K read:%s  %7.0f IOPS%s\n", chestPrimary, chestReset, r4k, chestReset)
		*rows = append(*rows, speedResult{label: "Disk 4K read", ok: true, detail: fmt.Sprintf("%.0f IOPS", r4k)})
	} else {
		*rows = append(*rows, speedResult{label: "Disk 4K read", ok: false, err: "no reads completed"})
	}
	if w4k > 0 {
		fmt.Fprintf(errOut, "  %s4K write:%s %7.0f IOPS%s\n", chestPrimary, chestReset, w4k, chestReset)
		*rows = append(*rows, speedResult{label: "Disk 4K write", ok: true, detail: fmt.Sprintf("%.0f IOPS", w4k)})
	} else {
		*rows = append(*rows, speedResult{label: "Disk 4K write", ok: false, err: "no writes completed"})
	}
	clearSpin(errOut)
}

// diskSeqWrite measures sequential write throughput. A warm-up half is
// discarded first, and the file is fsynced so the result reflects data that
// actually reached the device rather than the OS write cache.
func diskSeqWrite(path string, total int64) (float64, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	buf := make([]byte, diskBlockSize)
	warm := int64(0)
	for warm < total/2 {
		if _, err := f.Write(buf); err != nil {
			return 0, err
		}
		warm += int64(len(buf))
	}

	start := time.Now()
	written := int64(0)
	for written < total {
		if _, err := f.Write(buf); err != nil {
			return 0, err
		}
		written += int64(len(buf))
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	return diskMBs(total, time.Since(start).Seconds()), nil
}

// diskSeqRead measures sequential read throughput after a discarded warm-up.
func diskSeqRead(path string, total int64) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	buf := make([]byte, diskBlockSize)
	warm := int64(0)
	for warm < total/2 {
		n, err := f.Read(buf)
		if n > 0 {
			warm += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}

	start := time.Now()
	read := int64(0)
	for read < total {
		n, err := f.Read(buf)
		if n > 0 {
			read += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	return diskMBs(read, time.Since(start).Seconds()), nil
}

// diskRandom4K measures random 4K read and write IOPS against a bounded
// region of the file, using shuffled offsets to exercise the device rather
// than a sequential sweep. Writes are flushed so reported write IOPS reflect
// durable writes.
func diskRandom4K(path string, fileSize int64) (writeIOPS, readIOPS float64) {
	region := int64(diskRandRegionMiB) << 20
	if region > fileSize {
		region = fileSize
	}
	if region < disk4KSize {
		return 0, 0
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	maxOff := region - disk4KSize
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	offsets := make([]int64, diskRandOps)
	for i := range offsets {
		offsets[i] = rng.Int63n(maxOff)
	}
	shuffle := func() { rng.Shuffle(len(offsets), func(i, j int) { offsets[i], offsets[j] = offsets[j], offsets[i] }) }

	buf := make([]byte, disk4KSize)

	shuffle()
	start := time.Now()
	for _, off := range offsets {
		if _, err := f.WriteAt(buf, off); err != nil {
			return 0, 0
		}
	}
	if err := f.Sync(); err != nil {
		return 0, 0
	}
	writeIOPS = float64(diskRandOps) / time.Since(start).Seconds()

	// Reads hit the OS page cache for data just written, so read IOPS are a
	// cached-read baseline (noted in the UI legend).
	shuffle()
	start = time.Now()
	for _, off := range offsets {
		if _, err := f.ReadAt(buf, off); err != nil {
			return 0, 0
		}
	}
	readIOPS = float64(diskRandOps) / time.Since(start).Seconds()
	return writeIOPS, readIOPS
}

// diskMBs converts a byte count and elapsed seconds into decimal MB/s.
func diskMBs(n int64, secs float64) float64 {
	if secs <= 0 {
		return 0
	}
	return float64(n) / (secs * 1_000_000)
}
