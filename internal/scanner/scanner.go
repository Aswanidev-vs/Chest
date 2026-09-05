package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Aswanidev-vs/chest/internal/classifier"
	"github.com/Aswanidev-vs/chest/internal/models"
)

// ScanOptions configures scanner behavior
type ScanOptions struct {
	Recursive      bool
	IncludeHidden  bool
	FollowSymlinks bool
	Exclusions     []string // Names, patterns, or relative directories to skip
	IgnoreDirs     []string // Output directories being created by CHEST
}

// Scanner traverses files in a directory
type Scanner struct {
	opts ScanOptions
}

// New creates a new Scanner
func New(opts ScanOptions) *Scanner {
	return &Scanner{opts: opts}
}

// Scan scans target directory according to options
func (s *Scanner) Scan(root string) ([]models.File, error) {
	root = filepath.Clean(root)
	var files []models.File

	// Normalize exclusions
	exclSet := make(map[string]struct{})
	for _, e := range s.opts.Exclusions {
		for _, part := range strings.Split(e, ",") {
			p := strings.TrimSpace(part)
			if p != "" {
				exclSet[strings.ToLower(p)] = struct{}{}
			}
		}
	}

	ignoreSet := make(map[string]struct{})
	for _, ig := range s.opts.IgnoreDirs {
		ignoreSet[filepath.Clean(ig)] = struct{}{}
	}

	walkFn := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip unreadable paths safely
		}

		if path == root {
			return nil
		}

		// Check if path is within an ignored/destination directory
		if _, ok := ignoreSet[filepath.Clean(path)]; ok {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		name := info.Name()

		// Hidden check
		if !s.opts.IncludeHidden && isHidden(path, name, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Exclusion check
		if s.isExcluded(path, root, name, exclSet) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Symlink check
		if info.Mode()&os.ModeSymlink != 0 && !s.opts.FollowSymlinks {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			if !s.opts.Recursive {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		cat := classifier.ClassifyExtension(ext)

		files = append(files, models.File{
			Path:      path,
			RelPath:   relPath,
			Name:      name,
			Extension: ext,
			Size:      info.Size(),
			ModTime:   info.ModTime(),
			IsDir:     false,
			IsHidden:  isHidden(path, name, info),
			Category:  cat,
		})

		return nil
	}

	if s.opts.Recursive {
		err := filepath.Walk(root, walkFn)
		return files, err
	}

	// Non-recursive scan of root only
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if err := walkFn(path, info, nil); err != nil {
			if err == filepath.SkipDir {
				continue
			}
			return files, err
		}
	}

	return files, nil
}

func (s *Scanner) isExcluded(path, root, name string, exclusions map[string]struct{}) bool {
	lowerName := strings.ToLower(name)
	if _, ok := exclusions[lowerName]; ok {
		return true
	}

	rel, err := filepath.Rel(root, path)
	if err == nil {
		lowerRel := strings.ToLower(filepath.ToSlash(rel))
		if _, ok := exclusions[lowerRel]; ok {
			return true
		}
		for excl := range exclusions {
			matched, _ := filepath.Match(excl, lowerName)
			if matched {
				return true
			}
			matchedRel, _ := filepath.Match(excl, lowerRel)
			if matchedRel {
				return true
			}
		}
	}

	return false
}

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
