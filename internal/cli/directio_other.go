//go:build !windows && !linux

package cli

import (
	"errors"
	"os"
)

// This fallback targets platforms without an easy portable direct-I/O API
// (macOS, FreeBSD, etc.). The benchmark runs buffered and is labelled as
// such: writes are still flushed via Sync, but reads are served from the OS
// page cache and should be treated as a "cached read" estimate only.

func directSector() int { return 4096 }

func newAlignedBuf(size, align int) []byte { return make([]byte, size) }

func openDirectWrite(path string) (*os.File, error) {
	return nil, errNoDirectIO
}

func openDirectRead(path string) (*os.File, error) {
	return nil, errNoDirectIO
}

var errNoDirectIO = errors.New("direct I/O not supported on this platform")
