//go:build !windows

package scanner

import (
	"os"
	"strings"
)

func isHidden(path, name string, info os.FileInfo) bool {
	return strings.HasPrefix(name, ".") && name != "." && name != ".."
}
