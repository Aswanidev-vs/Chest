//go:build linux

package cli

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// directSector is the alignment Linux O_DIRECT requires (filesystem logical
// block, typically 4096). A 4096-aligned buffer satisfies 512-byte devices too.
func directSector() int { return 4096 }

// newAlignedBuf returns a slice whose backing storage starts at an
// align-byte boundary, as required by O_DIRECT.
func newAlignedBuf(size, align int) []byte {
	if align <= 1 {
		return make([]byte, size)
	}
	raw := make([]byte, size+align)
	base := uintptr(unsafe.Pointer(&raw[0]))
	off := int((uintptr(align) - base%uintptr(align)) % uintptr(align))
	return raw[off : off+size]
}

// openDirectWrite opens path for unbuffered read/write (create/truncate)
// using O_DIRECT, bypassing the OS page cache.
func openDirectWrite(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_TRUNC|unix.O_DIRECT, 0o644)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// openDirectRead opens path unbuffered for reading via O_DIRECT.
func openDirectRead(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
