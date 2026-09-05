//go:build !windows

package search

import (
	"io/fs"
	"strings"
)

func isHiddenEntry(path, name string, d fs.DirEntry) bool {
	return strings.HasPrefix(name, ".") && name != "." && name != ".."
}
