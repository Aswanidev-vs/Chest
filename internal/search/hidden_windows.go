//go:build windows

package search

import (
	"io/fs"
	"strings"
	"syscall"
)

func isHiddenEntry(path, name string, d fs.DirEntry) bool {
	if strings.HasPrefix(name, ".") && name != "." && name != ".." {
		return true
	}

	info, err := d.Info()
	if err == nil && info.Sys() != nil {
		if winInfo, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
			return winInfo.FileAttributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
		}
	}

	return false
}
