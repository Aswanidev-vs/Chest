//go:build windows

package scanner

import (
	"os"
	"strings"
	"syscall"
)

func isHidden(path, name string, info os.FileInfo) bool {
	// Standard Unix dotfile check
	if strings.HasPrefix(name, ".") && name != "." && name != ".." {
		return true
	}

	// Windows hidden file attribute check
	if sys := info.Sys(); sys != nil {
		if winInfo, ok := sys.(*syscall.Win32FileAttributeData); ok {
			return winInfo.FileAttributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
		}
	}

	return false
}
