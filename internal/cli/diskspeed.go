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

	// Reserve a unique temp path, then hand the same file to each phase.
	f, err := os.CreateTemp(dir, "chest-disk-*.tmp")
	if err != nil {
		*rows = append(*rows, speedResult{label: "Disk", ok: false, err: fmt.Sprintf("create temp: %v", err)})
		return
	}
	path := f.Name()
	_ = f.Close()
	defer os.Remove(path)

	total := sizeMiB << 20
	align := int64(directSector())

	// Prefer direct (unbuffered) I/O so reads bypass the OS page cache. When
	// the platform has no direct I/O (e.g. mac/BSD), fall back to buffered
	// I/O and label the run honestly.
	wf, err := openDirectWrite(path)
	direct := err == nil
	if !direct {
		wf, err = os.Create(path)
		if err != nil {
			*rows = append(*rows, speedResult{label: "Disk", ok: false, err: fmt.Sprintf("create: %v", err)})
			return
		}
	}
	defer wf.Close()

	if direct {
		fmt.Fprintf(errOut, "  %sMode:%s %sdirect (unbuffered)%s - reads bypass OS cache\n", chestDim, chestReset, chestGold, chestReset)
	} else {
		fmt.Fprintf(errOut, "  %sMode:%s %sbuffered%s - no direct I/O on this platform; reads hit OS cache\n", chestDim, chestReset, chestGold, chestReset)
	}

	buf := newAlignedBuf(diskBlockSize, int(align))

	// Write: stop the spinner before printing the result.
	printSpin(errOut, fmt.Sprintf("Disk: writing %d MiB...", sizeMiB))
	seqMBs, wErr := diskSeqWrite(wf, total, buf)
	clearSpin(errOut)
	if wErr != nil {
		*rows = append(*rows, speedResult{label: "Disk write", ok: false, err: wErr.Error()})
	} else {
		fmt.Fprintf(errOut, "  %sWrite:%s %8.2f MB/s%s\n", chestPrimary, chestReset, seqMBs, chestReset)
		*rows = append(*rows, speedResult{label: "Disk write", ok: true, detail: fmt.Sprintf("%.2f MB/s", seqMBs)})
	}

	// Read: reopen for (prefer direct) reading. clearSpin fires right after
	// the read completes and BEFORE the result prints, so the spinner never
	// shares a line with the measurement.
	printSpin(errOut, "Disk: reading back...")
	rf, rErr := openDirectRead(path)
	if rErr != nil {
		rf, rErr = os.Open(path)
	}
	var seqRMBs float64
	if rErr != nil {
		clearSpin(errOut)
		*rows = append(*rows, speedResult{label: "Disk read", ok: false, err: rErr.Error()})
	} else {
		seqRMBs, rErr = diskSeqRead(rf, total, buf)
		rf.Close()
		clearSpin(errOut)
		if rErr != nil {
			*rows = append(*rows, speedResult{label: "Disk read", ok: false, err: rErr.Error()})
		} else {
			fmt.Fprintf(errOut, "  %sRead:%s  %8.2f MB/s%s\n", chestPrimary, chestReset, seqRMBs, chestReset)
			*rows = append(*rows, speedResult{label: "Disk read", ok: true, detail: fmt.Sprintf("%.2f MB/s", seqRMBs)})
		}
	}

	// Random 4K: stop the spinner before printing both results.
	printSpin(errOut, "Disk: random 4K read/write...")
	w4k, r4k, w4kErr := diskRandom4K(wf, total, buf[:disk4KSize], align)
	clearSpin(errOut)
	if r4k > 0 {
		fmt.Fprintf(errOut, "  %s4K read:%s  %7.0f IOPS%s\n", chestPrimary, chestReset, r4k, chestReset)
		*rows = append(*rows, speedResult{label: "Disk 4K read", ok: true, detail: fmt.Sprintf("%.0f IOPS", r4k)})
	} else {
		*rows = append(*rows, speedResult{label: "Disk 4K read", ok: false, err: "no reads completed"})
	}
	if w4kErr != nil {
		*rows = append(*rows, speedResult{label: "Disk 4K write", ok: false, err: w4kErr.Error()})
	} else if w4k > 0 {
		fmt.Fprintf(errOut, "  %s4K write:%s %7.0f IOPS%s\n", chestPrimary, chestReset, w4k, chestReset)
		*rows = append(*rows, speedResult{label: "Disk 4K write", ok: true, detail: fmt.Sprintf("%.0f IOPS", w4k)})
	} else {
		*rows = append(*rows, speedResult{label: "Disk 4K write", ok: false, err: "no writes completed"})
	}
}

// diskSeqWrite measures sequential write throughput. A warm-up half is
// discarded first, and the file is synced so the result reflects data that
// reached the device (direct mode flushes each write via WRITE_THROUGH).
func diskSeqWrite(f *os.File, total int64, buf []byte) (float64, error) {
	warm := int64(0)
	for warm < total/2 {
		if _, err := f.Write(buf); err != nil {
			return 0, err
		}
		warm += int64(len(buf))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
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
// In direct mode the file is reopened unbuffered, so reads come from the
// device rather than the OS page cache.
func diskSeqRead(f *os.File, total int64, buf []byte) (float64, error) {
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
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
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
// region of the file, using shuffled sector-aligned offsets so direct I/O
// (which requires aligned buffers/offsets) is satisfied. Writes are flushed.
func diskRandom4K(f *os.File, fileSize int64, buf []byte, align int64) (writeIOPS, readIOPS float64, writeErr error) {
	bsize := int64(len(buf))
	region := int64(diskRandRegionMiB) << 20
	if region > fileSize {
		region = fileSize
	}
	if region < bsize {
		return 0, 0, nil
	}
	if align < 1 {
		align = 1
	}

	maxOff := region - bsize
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	offsets := make([]int64, diskRandOps)
	for i := range offsets {
		offsets[i] = (rng.Int63n(maxOff) / align) * align
	}
	shuffle := func() { rng.Shuffle(len(offsets), func(i, j int) { offsets[i], offsets[j] = offsets[j], offsets[i] }) }

	shuffle()
	start := time.Now()
	for _, off := range offsets {
		if _, err := f.WriteAt(buf, off); err != nil {
			return 0, 0, err
		}
	}
	writeErr = f.Sync()
	writeIOPS = float64(diskRandOps) / time.Since(start).Seconds()

	shuffle()
	start = time.Now()
	for _, off := range offsets {
		if _, err := f.ReadAt(buf, off); err != nil {
			return writeIOPS, 0, writeErr
		}
	}
	readIOPS = float64(diskRandOps) / time.Since(start).Seconds()
	return writeIOPS, readIOPS, writeErr
}

// diskMBs converts a byte count and elapsed seconds into decimal MB/s.
func diskMBs(n int64, secs float64) float64 {
	if secs <= 0 {
		return 0
	}
	return float64(n) / (secs * 1_000_000)
}
