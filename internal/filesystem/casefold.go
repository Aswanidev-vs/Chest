package filesystem

import (
	"path/filepath"
	"runtime"
	"strings"
)

// caseInsensitiveFS reports whether the OS treats filesystem paths as
// case-insensitive (macOS and Windows). On case-sensitive filesystems such as
// Linux two spellings that differ only by case are genuinely distinct and must
// never be conflated.
func caseInsensitiveFS() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

// CaseKey returns a map key for path such that two spellings of the same
// destination share a key on case-insensitive platforms, which is how intra-
// plan collision detection avoids treating "Photo.jpg" and "photo.jpg" as
// distinct targets.
func CaseKey(path string) string {
	cleaned := filepath.Clean(path)
	if caseInsensitiveFS() {
		return strings.ToLower(cleaned)
	}
	return cleaned
}
