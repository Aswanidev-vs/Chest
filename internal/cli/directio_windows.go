//go:build windows

package cli

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// directSector is the alignment Windows filesystems require for unbuffered
// I/O. 4096 is safe on both 512e and 4Kn drives (a 4096-aligned buffer is
// also 512-aligned).
func directSector() int { return 4096 }

// newAlignedBuf returns a slice of length size whose backing storage starts
// at an align-byte boundary, as required by FILE_FLAG_NO_BUFFERING / O_DIRECT.
func newAlignedBuf(size, align int) []byte {
	if align <= 1 {
		return make([]byte, size)
	}
	raw := make([]byte, size+align)
	base := uintptr(unsafe.Pointer(&raw[0]))
	off := int((uintptr(align) - base%uintptr(align)) % uintptr(align))
	return raw[off : off+size]
}

// openDirectWrite opens path for unbuffered read/write (create/truncate),
// bypassing the OS cache via FILE_FLAG_NO_BUFFERING | FILE_FLAG_WRITE_THROUGH.
func openDirectWrite(path string) (*os.File, error) {
	return windowsOpen(path, true)
}

// openDirectRead opens path unbuffered for reading, so reads come from the
// device rather than the OS page cache.
func openDirectRead(path string) (*os.File, error) {
	return windowsOpen(path, false)
}

func windowsOpen(path string, write bool) (*os.File, error) {
	namep, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ)
	mode := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
	disposition := uint32(windows.OPEN_EXISTING)
	if write {
		access |= windows.GENERIC_WRITE
		disposition = windows.CREATE_ALWAYS
	}
	attrs := uint32(windows.FILE_ATTRIBUTE_NORMAL) |
		windows.FILE_FLAG_NO_BUFFERING |
		windows.FILE_FLAG_WRITE_THROUGH

	h, err := windows.CreateFile(namep, access, mode, nil, disposition, attrs, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}
