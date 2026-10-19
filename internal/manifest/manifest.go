// Package manifest builds and verifies SHA-256 file inventories.
//
// A manifest records, for every file under a root, the path, size, modification
// time and SHA-256 digest observed at generation time. Re-hashing the same tree
// later and comparing it against those stored digests answers the one question a
// backup has to answer: has anything changed since the copy was taken?
//
// Everything here is read-only with respect to the scanned tree. Files are
// opened for reading only, nothing inside the root is created, renamed or
// removed, and the only bytes this package writes are the manifest document the
// caller asks for.
//
// The digests are computed here rather than borrowed from internal/indexer: the
// indexer's hasher is unexported and coupled to its SQLite store, and a backup
// manifest has to stay meaningful when the cache is cleared. The two agree by
// construction — both are plain SHA-256 over the file's bytes.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

const (
	// Version is the manifest schema version stamped into every document, so a
	// future format change can be detected instead of silently misread.
	Version = 1

	// Algorithm names the digest recorded for every entry.
	Algorithm = "sha256"

	// DefaultMaxSize is the size gate applied when Options.MaxSize is unset.
	// It mirrors the indexer's hashing gate: a backup inventory wants the same
	// 500MB ceiling, because reading a handful of multi-gigabyte blobs turns a
	// fast integrity check into an overnight job.
	DefaultMaxSize int64 = 500 * 1024 * 1024
)

// Reasons a file was walked but never hashed. They are recorded rather than
// dropped: a manifest that quietly omits files looks complete and is not.
const (
	ReasonTooLarge   = "larger than max-size"
	ReasonSymlink    = "symlink"
	ReasonUnreadable = "unreadable"
	ReasonHashFailed = "hash failed"
)

// Status is the per-file verdict of a verification run.
type Status string

const (
	// StatusOK means the file is present and its digest still matches.
	StatusOK Status = "OK"
	// StatusModified means the file is present but its digest changed.
	StatusModified Status = "MODIFIED"
	// StatusMissing means the file was recorded but is gone from disk.
	StatusMissing Status = "MISSING"
	// StatusAdded means the file is on disk but absent from the manifest.
	StatusAdded Status = "ADDED"
	// StatusUnchecked means the file exists but was not hashed, because it is
	// over the size gate or is a symlink. It is never counted as OK.
	StatusUnchecked Status = "UNCHECKED"
	// StatusUnreadable means the file exists but could not be read or hashed.
	StatusUnreadable Status = "UNREADABLE"
)

// Entry is one hashed file in a manifest.
type Entry struct {
	// Path is relative to the manifest root, always slash-separated so a
	// manifest taken on one platform can be checked on another.
	Path string `json:"path"`
	// Size is the byte length observed when the digest was taken.
	Size int64 `json:"size"`
	// MTimeUnix is the modification time in whole seconds since the epoch.
	MTimeUnix int64 `json:"mtime_unix"`
	// SHA256 is the lowercase hex digest of the file's contents.
	SHA256 string `json:"sha256"`
}

// Skipped is a path the walk reached but did not hash, with the reason why. The
// list is what keeps "skipped" from being a silent truncation: verify knows
// these paths belong to the inventory and re-checks them for presence.
type Skipped struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// Manifest is the whole document: metadata, the hashed files, and everything
// that was deliberately left out.
type Manifest struct {
	Version   int    `json:"version"`
	Algorithm string `json:"algorithm"`
	// Root is the absolute directory the walk started from.
	Root string `json:"root"`
	// MaxSize is the size gate that produced this document, in bytes. Verify
	// reuses it when the caller does not override it, so a large file skipped
	// at generation time is skipped again for the same stated reason instead of
	// being reported as a spurious modification.
	MaxSize int64 `json:"max_size"`

	FileCount    int   `json:"file_count"`
	TotalBytes   int64 `json:"total_bytes"`
	SkippedCount int   `json:"skipped_count"`
	// SkippedBytes is the size of the skipped entries whose size is known. Files
	// that could not be read contribute 0.
	SkippedBytes int64 `json:"skipped_bytes"`
	// The per-reason counts, so a consumer can tell "I skipped 3 big files" from
	// "I skipped 3 unreadable files" without re-counting the detail list.
	TooLargeCount   int `json:"too_large_count"`
	SymlinkCount    int `json:"symlink_count"`
	UnreadableCount int `json:"unreadable_count"`

	Files   []Entry   `json:"files"`
	Skipped []Skipped `json:"skipped,omitempty"`
}

// Options configures Generate and Verify.
type Options struct {
	// Root is the directory to walk or check. Required.
	Root string
	// MaxSize is the byte ceiling above which files are not hashed. Zero or
	// negative selects DefaultMaxSize.
	MaxSize int64
	// Exclude holds comma-separated names or globs to skip, matched
	// case-insensitively against each entry's name and its root-relative path,
	// mirroring the exclusions `chest index --except` understands.
	Exclude []string
	// Ignore holds absolute paths omitted from the walk entirely. The CLI uses
	// it to keep the manifest document out of the inventory it describes:
	// generating into the scanned tree and then verifying it must not report
	// the manifest itself as an added file.
	Ignore []string
}

// Diff is one file's verification verdict.
type Diff struct {
	Path string `json:"path"`
	// Status is the verdict: OK, MODIFIED, MISSING, ADDED, UNCHECKED or
	// UNREADABLE.
	Status Status `json:"status"`
	// Size is the byte length observed on disk, or the recorded size when the
	// file is missing.
	Size int64 `json:"size"`
	// ExpectedSHA256 is the digest recorded in the manifest, empty for added
	// files.
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	// ActualSHA256 is the digest computed from disk, empty unless the file was
	// hashed.
	ActualSHA256 string `json:"actual_sha256,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// Result is the outcome of a verification run.
type Result struct {
	Root string `json:"root"`
	// ManifestRoot is the root the manifest was generated from, which need not
	// be the directory being checked.
	ManifestRoot  string `json:"manifest_root"`
	ManifestFiles int    `json:"manifest_files"`

	OK         int `json:"ok"`
	Modified   int `json:"modified"`
	Missing    int `json:"missing"`
	Added      int `json:"added"`
	Unchecked  int `json:"unchecked"`
	Unreadable int `json:"unreadable"`

	// VerifiedBytes is the total size of the files whose digests matched.
	VerifiedBytes int64 `json:"verified_bytes"`
	// Intact is true only when nothing was modified, missing or added. Files
	// that were never hashed do not make a run fail, but they are counted so
	// the caller can see the run was partial.
	Intact bool `json:"intact"`
	// Files holds the per-file entries that are not OK, sorted by path.
	Files []Diff `json:"files"`
}

// DiscrepancyCount is the number of files that differ from the manifest.
func (r *Result) DiscrepancyCount() int { return r.Modified + r.Missing + r.Added }

// Excluder decides whether a walked entry is skipped. It is exported because
// the manifest, tree and doctor commands must agree on what an exclusion
// pattern means; one definition is the only way they can.
type Excluder struct{ patterns []string }

// NewExcluder flattens one or more comma-separated exclusion lists into a
// normalized (lowercased) set of names and glob patterns.
func NewExcluder(groups ...[]string) *Excluder {
	e := &Excluder{}
	for _, group := range groups {
		for _, raw := range group {
			for _, part := range strings.Split(raw, ",") {
				if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
					e.patterns = append(e.patterns, p)
				}
			}
		}
	}
	return e
}

// Match reports whether an entry with the given base name and root-relative
// path is excluded. Matching is case-insensitive and falls back to glob
// expansion, so "node_modules", "*.tmp" and "build/cache" all work.
func (e *Excluder) Match(name, rel string) bool {
	if e == nil || len(e.patterns) == 0 {
		return false
	}
	lowerName := strings.ToLower(name)
	lowerRel := strings.ToLower(filepath.ToSlash(rel))
	for _, p := range e.patterns {
		if lowerName == p || lowerRel == p {
			return true
		}
		if matched, _ := filepath.Match(p, lowerName); matched {
			return true
		}
		if matched, _ := filepath.Match(p, lowerRel); matched {
			return true
		}
	}
	return false
}

// Generate walks opts.Root, hashes every eligible file and returns a manifest.
// It never modifies anything under the root.
func Generate(opts Options) (*Manifest, error) {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	maxSize := opts.MaxSize
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	excl := NewExcluder(opts.Exclude)
	ignored := ignoreSet(opts.Ignore)

	m := &Manifest{
		Version: Version,
		// A manifest carries no run timestamp on purpose: identical bytes on
		// disk must produce byte-identical output, so regenerating an inventory
		// can be diffed against the previous one without a wall-clock field
		// guaranteeing a difference.
		Algorithm: Algorithm,
		Root:      root,
		MaxSize:   maxSize,
		Files:     []Entry{},
	}

	var cands []candidate
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			recordSkip(m, relPath(root, path), 0, ReasonUnreadable, err.Error())
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		rel := relPath(root, path)
		if _, skip := ignored[strings.ToLower(filepath.Clean(path))]; skip {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if excl.Match(d.Name(), rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			recordSkip(m, rel, 0, ReasonUnreadable, ierr.Error())
			return nil
		}
		size := info.Size()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			// Hashing a link would record the digest of whatever it points at,
			// possibly outside the root, and would produce a second entry for
			// content that already has one. A link is reported, not followed.
			recordSkip(m, rel, size, ReasonSymlink, "symlink not followed")
			return nil
		case size > maxSize:
			recordSkip(m, rel, size, ReasonTooLarge, fmt.Sprintf("%d bytes exceeds max-size %d", size, maxSize))
			return nil
		}
		cands = append(cands, candidate{rel: rel, path: path, size: size, mod: info.ModTime().Unix()})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walking %s: %w", root, walkErr)
	}

	// Hashing is I/O bound and every file is independent, so a bounded worker
	// pool turns a serial pass over a large tree into a parallel one. Results
	// are written to a pre-sized slice by index, so no lock guards the output
	// and the order is whatever the candidate walk produced.
	entries := make([]Entry, len(cands))
	var mu sync.Mutex
	var eg errgroup.Group
	eg.SetLimit(runtime.GOMAXPROCS(0))
	for i := range cands {
		i := i
		eg.Go(func() error {
			c := cands[i]
			sum, herr := hashFile(c.path)
			if herr != nil {
				mu.Lock()
				recordSkip(m, c.rel, c.size, ReasonHashFailed, herr.Error())
				mu.Unlock()
				return nil
			}
			entries[i] = Entry{Path: c.rel, Size: c.size, MTimeUnix: c.mod, SHA256: sum}
			return nil
		})
	}
	// A hash failure is recorded, not fatal: one unreadable file must not throw
	// away the inventory of everything else.
	_ = eg.Wait()

	for _, e := range entries {
		if e.SHA256 == "" {
			continue
		}
		m.Files = append(m.Files, e)
		m.FileCount++
		m.TotalBytes += e.Size
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	sort.Slice(m.Skipped, func(i, j int) bool { return m.Skipped[i].Path < m.Skipped[j].Path })
	return m, nil
}

// Verify re-hashes the files under opts.Root and compares them against m. It
// never modifies anything: the tree is only read.
//
// A manifest entry that would resolve outside root is a hard error rather than
// a skipped check. A manifest is data, and data can be edited, so a document
// naming "../../.ssh/id_rsa" must not be able to turn an integrity check into
// an arbitrary file reader.
func Verify(opts Options, m *Manifest) (*Result, error) {
	if m == nil {
		return nil, errors.New("no manifest loaded")
	}
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	maxSize := opts.MaxSize
	if maxSize <= 0 {
		maxSize = m.MaxSize
	}
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	excl := NewExcluder(opts.Exclude)
	ignored := ignoreSet(opts.Ignore)

	res := &Result{
		Root:          root,
		ManifestRoot:  m.Root,
		ManifestFiles: len(m.Files),
		Files:         []Diff{},
	}

	abs := make([]string, len(m.Files))
	// known holds every path the manifest accounts for, hashed or not, so the
	// added-file sweep below cannot re-report a file the walk deliberately left
	// out of the digest set.
	known := make(map[string]struct{}, len(m.Files)+len(m.Skipped))
	for i, e := range m.Files {
		p, err := safeJoin(root, e.Path)
		if err != nil {
			return nil, err
		}
		abs[i] = p
		known[normalizeKey(e.Path)] = struct{}{}
	}
	skippedAbs := make([]string, len(m.Skipped))
	for i, s := range m.Skipped {
		p, err := safeJoin(root, s.Path)
		if err != nil {
			return nil, err
		}
		skippedAbs[i] = p
		known[normalizeKey(s.Path)] = struct{}{}
	}

	type outcome struct {
		status Status
		size   int64
		actual string
		detail string
	}
	outcomes := make([]outcome, len(m.Files))
	var eg errgroup.Group
	eg.SetLimit(runtime.GOMAXPROCS(0))
	for i := range m.Files {
		i := i
		eg.Go(func() error {
			st, size, actual, detail := checkOne(abs[i], m.Files[i], maxSize)
			outcomes[i] = outcome{st, size, actual, detail}
			return nil
		})
	}
	_ = eg.Wait()

	for i, e := range m.Files {
		o := outcomes[i]
		switch o.status {
		case StatusOK:
			res.OK++
			res.VerifiedBytes += o.size
		case StatusModified:
			res.Modified++
			res.Files = append(res.Files, Diff{
				Path: e.Path, Status: StatusModified, Size: o.size,
				ExpectedSHA256: e.SHA256, ActualSHA256: o.actual, Detail: o.detail,
			})
		case StatusMissing:
			res.Missing++
			res.Files = append(res.Files, Diff{
				Path: e.Path, Status: StatusMissing, Size: e.Size,
				ExpectedSHA256: e.SHA256, Detail: o.detail,
			})
		case StatusUnchecked:
			res.Unchecked++
			res.Files = append(res.Files, Diff{
				Path: e.Path, Status: StatusUnchecked, Size: o.size,
				ExpectedSHA256: e.SHA256, Detail: o.detail,
			})
		default: // StatusUnreadable
			res.Unreadable++
			res.Files = append(res.Files, Diff{
				Path: e.Path, Status: StatusUnreadable, Size: o.size,
				ExpectedSHA256: e.SHA256, Detail: o.detail,
			})
		}
	}

	// Paths the walk recorded but declined to hash are still part of the
	// manifest's account of the tree. They are re-checked for presence only:
	// reporting them as unchecked keeps a partial inventory honest, and
	// omitting them from `known` would make every oversized file look like it
	// had been added since the backup was taken.
	for i, s := range m.Skipped {
		info, err := os.Lstat(skippedAbs[i])
		if err != nil {
			if os.IsNotExist(err) {
				res.Missing++
				res.Files = append(res.Files, Diff{
					Path: s.Path, Status: StatusMissing, Size: s.Size,
					Detail: "recorded as skipped (" + s.Reason + "), now absent",
				})
			} else {
				res.Unreadable++
				res.Files = append(res.Files, Diff{
					Path: s.Path, Status: StatusUnreadable, Size: s.Size,
					Detail: s.Reason + ": " + err.Error(),
				})
			}
			continue
		}
		switch s.Reason {
		case ReasonUnreadable, ReasonHashFailed:
			res.Unreadable++
			res.Files = append(res.Files, Diff{
				Path: s.Path, Status: StatusUnreadable, Size: info.Size(), Detail: s.Reason,
			})
		default: // ReasonTooLarge, ReasonSymlink
			res.Unchecked++
			res.Files = append(res.Files, Diff{
				Path: s.Path, Status: StatusUnchecked, Size: info.Size(), Detail: s.Reason,
			})
		}
	}

	// The other direction: anything on disk the manifest never recorded. The
	// manifest's own file is skipped via Options.Ignore so that generating into
	// the scanned tree does not make the tree permanently unverifiable.
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			res.Unreadable++
			res.Files = append(res.Files, Diff{
				Path: relPath(root, path), Status: StatusUnreadable, Detail: err.Error(),
			})
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		rel := relPath(root, path)
		if _, skip := ignored[strings.ToLower(filepath.Clean(path))]; skip {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if excl.Match(d.Name(), rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if _, ok := known[normalizeKey(rel)]; ok {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			res.Unreadable++
			res.Files = append(res.Files, Diff{
				Path: rel, Status: StatusUnreadable, Detail: ierr.Error(),
			})
			return nil
		}
		// A symlink on disk has no counterpart in any manifest this package
		// writes, so counting it as ADDED would make every tree that has one
		// permanently fail. It is reported as unchecked instead — visible,
		// never silently ignored, never a false discrepancy.
		if info.Mode()&os.ModeSymlink != 0 {
			res.Unchecked++
			res.Files = append(res.Files, Diff{
				Path: rel, Status: StatusUnchecked, Size: info.Size(),
				Detail: "symlink on disk, not hashed",
			})
			return nil
		}
		detail := ""
		if info.Size() > maxSize {
			detail = fmt.Sprintf("not hashed: %d bytes exceeds max-size %d", info.Size(), maxSize)
		}
		res.Added++
		res.Files = append(res.Files, Diff{
			Path: rel, Status: StatusAdded, Size: info.Size(), Detail: detail,
		})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walking %s: %w", root, walkErr)
	}

	sort.Slice(res.Files, func(i, j int) bool {
		if res.Files[i].Path != res.Files[j].Path {
			return res.Files[i].Path < res.Files[j].Path
		}
		return res.Files[i].Status < res.Files[j].Status
	})
	res.Intact = res.DiscrepancyCount() == 0
	return res, nil
}

// Load reads a manifest document from disk.
func Load(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest %s: %w", path, err)
	}
	defer f.Close()

	var m Manifest
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return nil, fmt.Errorf("parsing manifest %s: %w", path, err)
	}
	if m.Version > Version {
		return nil, fmt.Errorf("manifest %s has unsupported version %d (this build understands up to %d)", path, m.Version, Version)
	}
	if m.Algorithm != Algorithm {
		return nil, fmt.Errorf("%s is not a chest manifest (algorithm %q, want %q)", path, m.Algorithm, Algorithm)
	}
	if m.Files == nil {
		m.Files = []Entry{}
	}
	return &m, nil
}

// WriteJSON serializes the manifest as indented JSON.
func WriteJSON(w io.Writer, m *Manifest) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// CSVRows builds the manifest's CSV table. Hashed files come first, then every
// skipped entry with its reason in the status column, so a spreadsheet shows
// the whole walk rather than only the part that hashed cleanly.
func CSVRows(m *Manifest) [][]string {
	rows := [][]string{{"path", "size", "mtime_unix", "sha256", "status"}}
	for _, f := range m.Files {
		rows = append(rows, []string{
			f.Path,
			strconv.FormatInt(f.Size, 10),
			strconv.FormatInt(f.MTimeUnix, 10),
			f.SHA256,
			"hashed",
		})
	}
	for _, s := range m.Skipped {
		rows = append(rows, []string{s.Path, strconv.FormatInt(s.Size, 10), "", "", s.Reason})
	}
	return rows
}

// CSVStatusRows builds the verification result's CSV table: one row per
// non-OK file, plus the counts as a trailing TOTAL block.
func CSVStatusRows(r *Result) [][]string {
	rows := [][]string{{"path", "status", "size", "expected_sha256", "actual_sha256", "detail"}}
	for _, d := range r.Files {
		rows = append(rows, []string{
			d.Path,
			string(d.Status),
			strconv.FormatInt(d.Size, 10),
			d.ExpectedSHA256,
			d.ActualSHA256,
			d.Detail,
		})
	}
	rows = append(rows,
		[]string{"TOTAL", "OK", strconv.Itoa(r.OK), "", "", "digests matched"},
		[]string{"TOTAL", "MODIFIED", strconv.Itoa(r.Modified), "", "", ""},
		[]string{"TOTAL", "MISSING", strconv.Itoa(r.Missing), "", "", ""},
		[]string{"TOTAL", "ADDED", strconv.Itoa(r.Added), "", "", ""},
		[]string{"TOTAL", "UNCHECKED", strconv.Itoa(r.Unchecked), "", "", "over max-size or symlink"},
		[]string{"TOTAL", "UNREADABLE", strconv.Itoa(r.Unreadable), "", "", ""},
	)
	return rows
}

// candidate is a file the walk accepted, awaiting its digest.
type candidate struct {
	rel  string
	path string
	size int64
	mod  int64
}

// checkOne re-hashes a single recorded file and grades the result.
func checkOne(path string, e Entry, maxSize int64) (Status, int64, string, string) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return StatusMissing, 0, "", ""
		}
		return StatusUnreadable, 0, "", err.Error()
	}
	if info.IsDir() {
		return StatusModified, 0, "", "path is a directory now"
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return StatusModified, 0, "", "path is a symlink now"
	}
	if info.Size() > maxSize {
		return StatusUnchecked, info.Size(), "", fmt.Sprintf("not hashed: %d bytes exceeds max-size %d", info.Size(), maxSize)
	}
	sum, herr := hashFile(path)
	if herr != nil {
		return StatusUnreadable, info.Size(), "", herr.Error()
	}
	if sum != e.SHA256 {
		return StatusModified, info.Size(), sum, ""
	}
	return StatusOK, info.Size(), sum, ""
}

// hashFile streams a file through SHA-256. Opened read-only; nothing under the
// scanned root is ever written.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// resolveRoot turns the caller's root into a clean absolute path and confirms
// it is a readable directory, so both commands fail with one clear message
// instead of an empty walk.
func resolveRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("a directory is required")
	}
	abs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", root, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("cannot read directory %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

// safeJoin resolves a manifest entry against root and refuses anything that
// would leave it: absolute paths, drive letters and "../" walks out of the
// directory all error out instead of being followed.
func safeJoin(root, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errors.New("manifest contains an entry with an empty path")
	}
	if strings.ContainsRune(rel, '\\') {
		// Manifest paths are slash-separated by contract; a backslash means the
		// document was produced by something else (or edited by hand) and its
		// path semantics on Windows would differ from every other platform's.
		return "", fmt.Errorf("manifest entry %q is not a slash-separated relative path", rel)
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || (len(rel) > 1 && rel[1] == ':') {
		return "", fmt.Errorf("manifest entry %q is an absolute path", rel)
	}
	joined := filepath.Join(root, filepath.FromSlash(rel))
	inside, err := filepath.Rel(root, joined)
	if err != nil {
		return "", fmt.Errorf("manifest entry %q cannot be resolved under %s: %w", rel, root, err)
	}
	if inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("manifest entry %q escapes the scanned directory", rel)
	}
	return joined, nil
}

// recordSkip appends a skipped entry and updates the per-reason counters. The
// counters are what a caller reads to learn that the walk did not cover
// everything.
func recordSkip(m *Manifest, path string, size int64, reason, detail string) {
	m.Skipped = append(m.Skipped, Skipped{Path: path, Size: size, Reason: reason, Detail: detail})
	m.SkippedCount++
	m.SkippedBytes += size
	switch reason {
	case ReasonTooLarge:
		m.TooLargeCount++
	case ReasonSymlink:
		m.SymlinkCount++
	default:
		m.UnreadableCount++
	}
}

// relPath expresses path relative to root with forward slashes, which is the
// form stored in the manifest and used for exclusion matching.
func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// normalizeKey puts a manifest or on-disk path into one comparable form, so a
// document written with "./a/b" still matches the walk's "a/b".
func normalizeKey(p string) string {
	s := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	s = strings.TrimPrefix(s, "./")
	return strings.TrimPrefix(s, "/")
}

// ignoreSet lowercases and cleans the absolute paths omitted from the walk, so
// the lookup is insensitive to the spelling of the path on case-insensitive
// filesystems.
func ignoreSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(filepath.Clean(p))
		if err != nil {
			abs = filepath.Clean(p)
		}
		set[strings.ToLower(abs)] = struct{}{}
	}
	return set
}
