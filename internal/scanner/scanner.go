package scanner

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Aswanidev-vs/chest/internal/classifier"
	"github.com/Aswanidev-vs/chest/internal/metadata"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/charlievieth/fastwalk"
)

// ScanOptions configures scanner behavior
type ScanOptions struct {
	Recursive      bool
	IncludeHidden  bool
	FollowSymlinks bool
	Exclusions     []string                          // Names, patterns, or relative directories to skip
	IgnoreDirs     []string                          // Output directories being created by CHEST
	ClassifyFunc   func(filename, ext string) string // Optional: overrides built-in classifier
	ExtractDates   bool                              // Read embedded media dates when available
	// EnrichFunc optionally enriches a scanned file record after built-in
	// metadata detection, e.g. via external metadata plugins. It returns the
	// (possibly modified) file and true when it produced enrichment. Called
	// once per file; must be safe for concurrent use during recursive scans.
	EnrichFunc func(file models.File) (models.File, bool)
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
	var mu sync.Mutex // guards concurrent appends when recursive walks run in parallel

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
		var cat string
		if s.opts.ClassifyFunc != nil {
			cat = s.opts.ClassifyFunc(name, ext)
		} else {
			cat = classifier.ClassifyExtension(ext)
		}

		file := models.File{
			Path:      path,
			RelPath:   relPath,
			Name:      name,
			Extension: ext,
			Size:      info.Size(),
			ModTime:   info.ModTime(),
			IsDir:     false,
			IsHidden:  isHidden(path, name, info),
			Category:  cat,
		}
		// Use the signature-based metadata extractor to enrich the record with a
		// detected format, MIME type and any embedded metadata fields, plus the
		// taken date when requested.
		if meta, err := metadata.Extract(path, ext); err == nil {
			if meta.Format != "" {
				file.Format = meta.Format
			}
			if file.MIMEType == "" {
				file.MIMEType = meta.MIMEType
			}
			// Signature-based detection knows formats the extension
			// classifier does not; adopt its category only when the
			// classifier had no opinion.
			if file.Category == classifier.TypeOther && meta.Category != "" {
				file.Category = meta.Category
			}
			if len(meta.Fields) > 0 {
				file.Metadata = meta.Fields
			}
			if s.opts.ExtractDates && !meta.Date.Date.IsZero() {
				file.TakenDate = &meta.Date.Date
				file.TakenDateSource = meta.Date.Source
			}
		}

		// External enrichment (e.g. metadata plugins) runs last so it can
		// fill gaps or override values the built-in extractors produced.
		if s.opts.EnrichFunc != nil {
			if enriched, ok := s.opts.EnrichFunc(file); ok {
				file = enriched
			}
		}

		mu.Lock()
		files = append(files, file)
		mu.Unlock()

		return nil
	}

	if s.opts.Recursive {
		conf := fastwalk.Config{Follow: s.opts.FollowSymlinks}
		combined := func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil // Skip unreadable paths safely
			}
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			return walkFn(path, info, nil)
		}
		err := fastwalk.Walk(&conf, root, combined)
		if err != nil {
			return nil, err
		}
		// fastwalk traverses in parallel, so results are unordered. Sort by path
		// to keep planner/output ordering deterministic (matches filepath.Walk's
		// lexical order and makes collision-rename assignments stable).
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		return files, nil
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
