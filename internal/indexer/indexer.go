package indexer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aswanidev-vs/chest/internal/classifier"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/charlievieth/fastwalk"
	_ "github.com/ncruces/go-sqlite3/driver"
	"golang.org/x/sync/errgroup"
)

// IndexedFile represents a file in SQLite index
type IndexedFile struct {
	Path      string    `json:"path"`
	RelPath   string    `json:"rel_path"`
	Name      string    `json:"name"`
	Extension string    `json:"extension"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"mod_time"`
	Category  string    `json:"category"`
	Hash      string    `json:"hash"`
}

// StatsSummary aggregates storage statistics
type StatsSummary struct {
	TotalFiles       int              `json:"total_files"`
	TotalDirectories int              `json:"total_directories"`
	TotalSize        int64            `json:"total_size"`
	CategoryCounts   map[string]int   `json:"category_counts"`
	CategorySizes    map[string]int64 `json:"category_sizes"`
	LargestFiles     []IndexedFile    `json:"largest_files"`
}

// DuplicateGroup represents files sharing identical hash
type DuplicateGroup struct {
	Hash       string        `json:"hash"`
	Size       int64         `json:"size"`
	WastedSize int64         `json:"wasted_size"`
	Files      []IndexedFile `json:"files"`
}

// AnalyzeReport represents read-only intelligence findings
type AnalyzeReport struct {
	Stats           StatsSummary     `json:"stats"`
	DuplicateGroups []DuplicateGroup `json:"duplicate_groups"`
	OldFiles        []IndexedFile    `json:"old_files"` // Files older than 180 days
	EmptyFiles      []IndexedFile    `json:"empty_files"`
}

// OpenOrCreate opens or creates local sqlite index database
func OpenOrCreate(customPath ...string) (*Store, error) {
	dbPath := ""
	if len(customPath) > 0 && customPath[0] != "" {
		dbPath = customPath[0]
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir := filepath.Join(home, ".chest")
		_ = os.MkdirAll(dir, 0755)
		dbPath = filepath.Join(dir, "index.db")
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	// Enable WAL and busy timeout safely across platforms
	_, _ = db.Exec("PRAGMA journal_mode=WAL;")
	_, _ = db.Exec("PRAGMA busy_timeout=5000;")

	schema := `
	CREATE TABLE IF NOT EXISTS files (
		path TEXT PRIMARY KEY,
		rel_path TEXT,
		name TEXT,
		extension TEXT,
		size INTEGER,
		mod_time INTEGER,
		category TEXT,
		hash TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_files_category ON files(category);
	CREATE INDEX IF NOT EXISTS idx_files_size ON files(size);
	CREATE INDEX IF NOT EXISTS idx_files_hash ON files(hash);

	CREATE TABLE IF NOT EXISTS history_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp INTEGER,
		directory TEXT,
		files_count INTEGER,
		status TEXT
	);

	CREATE TABLE IF NOT EXISTS history_operations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id INTEGER,
		original_source TEXT,
		destination TEXT,
		reason TEXT,
		FOREIGN KEY(entry_id) REFERENCES history_entries(id)
	);

	CREATE TABLE IF NOT EXISTS meta (
		key TEXT PRIMARY KEY,
		value TEXT
	);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed creating schema: %w", err)
	}

	s := &Store{db: db, dbPath: dbPath}
	// One-time normalization of legacy relative `path` values (from old
	// `chest index .` runs that stored paths like "sub/foo.txt" instead of
	// absolute paths) so report scoping and stale-row purging behave
	// consistently. Runs at most once per store (guarded by a meta marker).
	if err := s.migrateRelativePaths(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed normalizing legacy paths: %w", err)
	}
	return s, nil
}

// Store wraps SQLite index database
type Store struct {
	db     *sql.DB
	dbPath string
}

// Close database connection
func (s *Store) Close() error {
	return s.db.Close()
}

// ClearIndex truncates cached file records without deleting history
func (s *Store) ClearIndex() error {
	_, err := s.db.Exec("DELETE FROM files; VACUUM;")
	return err
}

// DeleteDB closes and removes database file completely
func (s *Store) DeleteDB() error {
	_ = s.db.Close()
	return os.Remove(s.dbPath)
}

// IsEmpty reports whether the cached index contains zero file records. Report
// commands use this to detect an empty cache (e.g. right after `chest clean`)
// and transparently re-populate instead of showing all-zero results.
func (s *Store) IsEmpty() (bool, error) {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// migrateRelativePaths is a one-time upgrade that rewrites legacy relative
// `path` values — produced by old `chest index .` runs that stored paths like
// "sub/foo.txt" instead of absolute paths — to absolute paths rooted at the
// current working directory. It runs at most once per store: after the first
// successful pass a meta marker is written so subsequent opens skip the scan.
func (s *Store) migrateRelativePaths() error {
	var done string
	if err := s.db.QueryRow("SELECT value FROM meta WHERE key = 'paths_absolute_migrated'").Scan(&done); err == nil && done == "1" {
		return nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	rows, err := s.db.Query("SELECT path, rel_path FROM files")
	if err != nil {
		return err
	}
	type legacy struct{ path, rel string }
	var pending []legacy
	for rows.Next() {
		var p legacy
		if err := rows.Scan(&p.path, &p.rel); err != nil {
			rows.Close()
			return err
		}
		if !filepath.IsAbs(p.path) {
			pending = append(pending, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	upd, err := tx.Prepare("UPDATE files SET path = ?, rel_path = ? WHERE path = ?")
	if err != nil {
		return err
	}
	for _, p := range pending {
		abs := filepath.Join(cwd, filepath.Clean(p.path))
		rel := p.rel
		if r, rerr := filepath.Rel(cwd, abs); rerr == nil {
			rel = r
		}
		if _, err := upd.Exec(abs, rel, p.path); err != nil {
			upd.Close()
			return err
		}
	}
	upd.Close()

	if _, err := tx.Exec("INSERT OR REPLACE INTO meta (key, value) VALUES ('paths_absolute_migrated', '1')"); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordHistory saves operation run into SQLite
func (s *Store) RecordHistory(directory string, ops []models.HistoryOperation) (*models.HistoryEntry, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now()
	res, err := tx.Exec("INSERT INTO history_entries (timestamp, directory, files_count, status) VALUES (?, ?, ?, ?)",
		now.Unix(), directory, len(ops), "Complete")
	if err != nil {
		return nil, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	stmt, err := tx.Prepare("INSERT INTO history_operations (entry_id, original_source, destination, reason) VALUES (?, ?, ?, ?)")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	for _, op := range ops {
		if _, err := stmt.Exec(id, op.OriginalSource, op.Destination, op.Reason); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &models.HistoryEntry{
		ID:         int(id),
		Timestamp:  now,
		Directory:  directory,
		Operations: ops,
		FilesCount: len(ops),
		Status:     "Complete",
	}, nil
}

// ListHistory reads all history entries from SQLite
func (s *Store) ListHistory() ([]models.HistoryEntry, error) {
	rows, err := s.db.Query("SELECT id, timestamp, directory, files_count, status FROM history_entries ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.HistoryEntry
	for rows.Next() {
		var e models.HistoryEntry
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.Directory, &e.FilesCount, &e.Status); err == nil {
			e.Timestamp = time.Unix(ts, 0)
			list = append(list, e)
		}
	}

	return list, nil
}

// GetHistoryEntry retrieves entry and its individual operations
func (s *Store) GetHistoryEntry(id int) (*models.HistoryEntry, error) {
	var e models.HistoryEntry
	var ts int64
	err := s.db.QueryRow("SELECT id, timestamp, directory, files_count, status FROM history_entries WHERE id = ?", id).
		Scan(&e.ID, &ts, &e.Directory, &e.FilesCount, &e.Status)
	if err != nil {
		return nil, err
	}
	e.Timestamp = time.Unix(ts, 0)

	rows, err := s.db.Query("SELECT original_source, destination, reason FROM history_operations WHERE entry_id = ?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var op models.HistoryOperation
		if err := rows.Scan(&op.OriginalSource, &op.Destination, &op.Reason); err == nil {
			e.Operations = append(e.Operations, op)
		}
	}

	return &e, nil
}

// MarkHistoryUndone updates history entry status to Undone
func (s *Store) MarkHistoryUndone(id int) error {
	_, err := s.db.Exec("UPDATE history_entries SET status = 'Undone' WHERE id = ?", id)
	return err
}

// IndexDirectory scans a target folder and updates index.
// exclusions is a list of comma-separated names/globs (dirs or files) to skip,
// matched against each entry name and its path relative to root.
func (s *Store) IndexDirectory(root string, computeHashes bool, progress func(current int), exclusions ...[]string) (int, error) {
	root = filepath.Clean(root)
	if abs, aerr := filepath.Abs(root); aerr == nil {
		// Store absolute paths so report scoping and stale-row purging are
		// consistent regardless of how the user spelled the root (".", "./x",
		// or "/abs/x"). duplicates/analyze already canonicalize before calling.
		root = abs
	}
	exclSet := exclSetFromArgs(exclusions...)

	// Snapshot the current index so unchanged files can be skipped incrementally
	// instead of being re-hashed on every run.
	existing, err := s.loadIndexMap()
	if err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
	INSERT OR REPLACE INTO files (path, rel_path, name, extension, size, mod_time, category, hash)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	type row struct {
		path, rel, name, ext, cat string
		size, modUnix             int64
		hash                      string
	}

	const batchSize = 500
	rowsCh := make(chan row, 2048)
	var (
		processedCount int64 // files passed to the walker (drives progress)
		indexedCount   int64 // rows actually inserted/updated
		seen           = make(map[string]struct{})
		seenMu         = &sync.Mutex{}
	)

	// A single writer goroutine drains rows and flushes them in batches. This
	// keeps inserts off the fastwalk worker goroutines (which would otherwise
	// all contend on the one DB connection) and removes per-file commit cost.
	writerDone := make(chan struct{})
	go func() {
		batch := make([]row, 0, batchSize)
		flush := func() {
			for _, b := range batch {
				if _, err := stmt.Exec(b.path, b.rel, b.name, b.ext, b.size, b.modUnix, b.cat, b.hash); err == nil {
					atomic.AddInt64(&indexedCount, 1)
				}
			}
			batch = batch[:0]
		}
		// Deferred calls run LIFO, so flush() is guaranteed to finish (and
		// indexedCount to be accurate) before writerDone closes. The reader
		// also must not Commit until the final batch is on the connection.
		defer close(writerDone)
		defer flush()
		for r := range rowsCh {
			batch = append(batch, r)
			if len(batch) >= batchSize {
				flush()
			}
		}
	}()

	walkConf := fastwalk.Config{Follow: false}
	walkFn := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		name := d.Name()

		// Prune excluded directories entirely instead of descending into them.
		if isExcludedPath(name, root, path, exclSet) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() || strings.HasPrefix(name, ".") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		modUnix := info.ModTime().Unix()
		size := info.Size()

		// Progress tracks files scanned so the bar reaches 100% even when
		// unchanged files are skipped.
		processed := atomic.AddInt64(&processedCount, 1)
		if progress != nil && processed%50 == 0 {
			progress(int(processed))
		}

		// Incremental skip: if size + mtime match the index and we already have
		// a hash (or don't need one), keep the existing row untouched.
		if meta, ok := existing[path]; ok && meta.size == size && meta.modUnix == modUnix {
			if !computeHashes || meta.hash != "" {
				seenMu.Lock()
				seen[path] = struct{}{}
				seenMu.Unlock()
				return nil
			}
		}

		rel, _ := filepath.Rel(root, path)
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		cat := classifier.ClassifyExtension(ext)

		hashVal := ""
		if computeHashes && size > 0 && size < 500*1024*1024 { // Hash files under 500MB
			if h, err := hashFile(path); err == nil {
				hashVal = h
			}
		}

		seenMu.Lock()
		seen[path] = struct{}{}
		seenMu.Unlock()

		rowsCh <- row{path, rel, name, ext, cat, size, modUnix, hashVal}
		return nil
	}

	if err := fastwalk.Walk(&walkConf, root, walkFn); err != nil {
		close(rowsCh)
		<-writerDone
		return 0, err
	}
	close(rowsCh)
	<-writerDone

	// Purge stale rows: paths previously indexed under this root that were
	// neither seen on disk nor re-added this run — i.e. files that were
	// deleted/moved, or that are now covered by the current --except
	// exclusions. INSERT OR REPLACE never removes rows, so without this the
	// cache would keep describing files that no longer exist.
	del, derr := tx.Prepare("DELETE FROM files WHERE path = ?")
	if derr == nil {
		for p := range existing {
			if _, ok := seen[p]; ok {
				continue
			}
			if !pathWithinRoot(root, p) {
				continue
			}
			_, _ = del.Exec(p)
		}
		_ = del.Close()
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return int(indexedCount), nil
}

// fileMeta stores the fields used to detect whether a file changed.
type fileMeta struct {
	size    int64
	modUnix int64
	hash    string
}

// loadIndexMap loads the current index keyed by absolute path for incremental
// scanning decisions.
func (s *Store) loadIndexMap() (map[string]fileMeta, error) {
	rows, err := s.db.Query("SELECT path, size, mod_time, COALESCE(hash, '') FROM files")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]fileMeta)
	for rows.Next() {
		var p string
		var meta fileMeta
		if err := rows.Scan(&p, &meta.size, &meta.modUnix, &meta.hash); err == nil {
			m[p] = meta
		}
	}
	return m, rows.Err()
}

// isExcludedPath reports whether a walk entry matches an exclusion. Exclusions
// are matched case-insensitively against the entry name and against its path
// relative to root, with glob support via filepath.Match.
func isExcludedPath(name, root, path string, exclusions map[string]struct{}) bool {
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
			if matched, _ := filepath.Match(excl, lowerName); matched {
				return true
			}
			if matched, _ := filepath.Match(excl, lowerRel); matched {
				return true
			}
		}
	}
	return false
}

// exclSetFromArgs flattens one or more comma-separated exclusion lists into a
// normalized (lowercased) set of names/patterns.
func exclSetFromArgs(exclusions ...[]string) map[string]struct{} {
	exclSet := make(map[string]struct{})
	for _, group := range exclusions {
		for _, raw := range group {
			for _, part := range strings.Split(raw, ",") {
				if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
					exclSet[p] = struct{}{}
				}
			}
		}
	}
	return exclSet
}

// CountFiles walks root and returns how many files IndexDirectory would index
// (skipping hidden files, subdirectories, and excluded paths). It performs a
// lightweight, hashing-free scan so callers can build an accurate total for a
// progress bar before the (potentially slow) hashing pass.
func (s *Store) CountFiles(root string, exclusions ...[]string) (int, error) {
	root = filepath.Clean(root)
	exclSet := exclSetFromArgs(exclusions...)

	var count int64
	conf := fastwalk.Config{Follow: false}
	err := fastwalk.Walk(&conf, root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if isExcludedPath(name, root, path, exclSet) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || strings.HasPrefix(name, ".") {
			return nil
		}
		atomic.AddInt64(&count, 1)
		return nil
	})
	return int(count), err
}

// normalizeRoot cleans a scan root the same way IndexDirectory does, so report
// scoping matches the stored `path` column regardless of whether the user wrote
// `testing`, `./testing`, or `E:\Chest\testing`.
func normalizeRoot(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Clean(root)
}

// caseInsensitiveFS reports whether the OS treats filesystem paths as
// case-insensitive (macOS and Windows). CHEST only folds path case for scoped
// lookup and stale-row purging on these platforms; on case-sensitive
// filesystems such as Linux two directories that differ only by case are
// genuinely distinct and must never be conflated.
func caseInsensitiveFS() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

// pathWithinRoot reports whether path is root itself or sits directly under it,
// mirroring the trailing-separator prefix rule used by pathScope so sibling
// directories can never be matched. On case-insensitive filesystems the check
// ignores case so a differently-cased spelling of the root still purges/scopes
// the same indexed rows.
func pathWithinRoot(root, path string) bool {
	if root == "" {
		return false
	}
	r := filepath.Clean(root)
	p := filepath.Clean(path)
	if p == r {
		return true
	}
	pref := r + string(filepath.Separator)
	if caseInsensitiveFS() {
		if strings.EqualFold(p, r) {
			return true
		}
		return strings.HasPrefix(strings.ToLower(p), strings.ToLower(pref))
	}
	return strings.HasPrefix(p, pref)
}

// pathScope builds a `path` prefix predicate (and its params) that narrows a
// query to a single indexed folder subtree. The trailing separator is included in
// the matched prefix, so a sibling folder (e.g. `root2` or `root-two`) can never
// leak into the results. Returns ("", nil) for an empty root (whole-index query).
func pathScope(root string) (string, []any) {
	root = normalizeRoot(root)
	if root == "" {
		return "", nil
	}
	sep := string(filepath.Separator)
	pref := root + sep
	if caseInsensitiveFS() {
		// Fold both sides so a user spelling the directory with different casing
		// than on disk still hits the indexed rows (macOS/Windows are
		// case-insensitive filesystems by default).
		return "(LOWER(path) = ? OR (LOWER(substr(path, 1, ?)) = ? AND length(path) > ?))",
			[]any{strings.ToLower(root), len(pref), strings.ToLower(pref), len(pref)}
	}
	// Linux is case-sensitive: match the folder in question alone plus
	// everything directly under it, case-exactly.
	return "(path = ? OR (substr(path, 1, ?) = ? AND length(path) > ?))",
		[]any{root, len(pref), pref, len(pref)}
}

// GetStats returns storage breakdown across categories and largest files.
// When `root` is non-empty, results are scoped to that indexed folder subtree only.
func (s *Store) GetStats(root string) (StatsSummary, error) {
	return s.GetStatsProgress(root, 0, 0, nil)
}

// GetStatsProgress is GetStats with a 0-100 progress callback. As it moves from
// pctStart toward pctEnd, `progress` is invoked with an increasing percentage once
// each aggregate query completes.
func (s *Store) GetStatsProgress(root string, pctStart, pctEnd int, progress func(pct int)) (StatsSummary, error) {
	scope, scopeArgs := pathScope(root)
	emit := func(pct int) {
		if progress != nil {
			progress(pct)
		}
	}
	span := pctEnd - pctStart
	lerp := func(frac int) int {
		return pctStart + (span * frac / 1000)
	}
	var summary StatsSummary
	summary.CategoryCounts = make(map[string]int)
	summary.CategorySizes = make(map[string]int64)

	// Total count & size
	countSQL := "SELECT COUNT(*), COALESCE(SUM(size), 0) FROM files"
	if scope != "" {
		countSQL += " WHERE " + scope
	}
	row := s.db.QueryRow(countSQL, scopeArgs...)
	if err := row.Scan(&summary.TotalFiles, &summary.TotalSize); err != nil {
		return summary, err
	}
	emit(lerp(300))

	// Category aggregates
	catSQL := "SELECT category, COUNT(*), SUM(size) FROM files"
	if scope != "" {
		catSQL += " WHERE " + scope
	}
	catSQL += " GROUP BY category"
	rows, err := s.db.Query(catSQL, scopeArgs...)
	if err != nil {
		return summary, err
	}
	defer rows.Close()

	for rows.Next() {
		var cat string
		var count int
		var size int64
		if err := rows.Scan(&cat, &count, &size); err == nil {
			summary.CategoryCounts[cat] = count
			summary.CategorySizes[cat] = size
		}
	}
	emit(lerp(700))

	// Top 10 largest files
	topSQL := "SELECT path, rel_path, name, extension, size, mod_time, category FROM files"
	if scope != "" {
		topSQL += " WHERE " + scope
	}
	topSQL += " ORDER BY size DESC LIMIT 10"
	topRows, err := s.db.Query(topSQL, scopeArgs...)
	if err == nil {
		defer topRows.Close()
		for topRows.Next() {
			var f IndexedFile
			var modUnix int64
			if err := topRows.Scan(&f.Path, &f.RelPath, &f.Name, &f.Extension, &f.Size, &modUnix, &f.Category); err == nil {
				f.ModTime = time.Unix(modUnix, 0)
				summary.LargestFiles = append(summary.LargestFiles, f)
			}
		}
	}

	emit(lerp(1000))

	return summary, nil
}

// FindDuplicates identifies identical files based on size and hash.
// When `root` is non-empty, only that indexed folder subtree is considered.
func (s *Store) FindDuplicates(root string) ([]DuplicateGroup, error) {
	return s.FindDuplicatesProgress(root, 0, 0, nil)
}

// FindDuplicatesProgress is FindDuplicates with a 0-100 progress callback so the
// CLI can render a live bar while hashes are computed and groups re-verified.
// Progress is reported linearly within [pctStart, pctEnd]; hashing consumes the
// first 70% of the budget and group re-verification fills the remainder.
func (s *Store) FindDuplicatesProgress(root string, pctStart, pctEnd int, progress func(pct int)) ([]DuplicateGroup, error) {
	scope, scopeArgs := pathScope(root)
	emit := func(pct int) {
		if progress != nil {
			progress(pct)
		}
	}
	span := pctEnd - pctStart
	lerp := func(frac int) int {
		return pctStart + (span * frac / 1000)
	}

	// First compute hashes for files with matching sizes that don't have hashes yet
	sizeSQL := "SELECT size FROM files WHERE size > 0"
	if scope != "" {
		sizeSQL += " AND " + scope
	}
	sizeSQL += " GROUP BY size HAVING COUNT(*) > 1"
	sizeRows, err := s.db.Query(sizeSQL, scopeArgs...)
	if err != nil {
		return nil, err
	}
	defer sizeRows.Close()

	var candidateSizes []int64
	for sizeRows.Next() {
		var sz int64
		if err := sizeRows.Scan(&sz); err == nil {
			candidateSizes = append(candidateSizes, sz)
		}
	}

	// For candidate sizes, compute hash if missing (in parallel, bounded workers).
	// Collect every unhashed path up front so we can report an accurate fraction
	// of the hashing workload as each file completes.
	var (
		eg     errgroup.Group
		mu     sync.Mutex
		out    []struct{ path, hash string }
		toHash int
		done   int
	)
	// fresh records the paths hashed in THIS call. Their hashes are guaranteed
	// accurate (we just read the files), so the re-verify phase can skip them
	// instead of reading the same contents a second time.
	fresh := make(map[string]bool)
	var unhash []string
	for _, sz := range candidateSizes {
		unhashedSQL := "SELECT path FROM files WHERE size = ? AND (hash IS NULL OR hash = '')"
		if scope != "" {
			unhashedSQL += " AND " + scope
		}
		unhashed, err := s.db.Query(unhashedSQL, append([]any{sz}, scopeArgs...)...)
		if err == nil {
			for unhashed.Next() {
				var p string
				if err := unhashed.Scan(&p); err == nil {
					unhash = append(unhash, p)
					toHash++
				}
			}
			unhashed.Close()
		}
	}
	eg.SetLimit(runtime.GOMAXPROCS(0))
	for _, p := range unhash {
		p := p
		eg.Go(func() error {
			h, err := hashFile(p)
			if err != nil {
				return nil // skip unreadable files, matching prior behavior
			}
			mu.Lock()
			out = append(out, struct{ path, hash string }{p, h})
			fresh[p] = true
			done++
			if toHash > 0 {
				emit(lerp(int(int64(done) * 700 / int64(toHash))))
			}
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	if toHash > 0 {
		emit(lerp(700))
	}

	// Persist computed hashes in a single transaction (serial write).
	if len(out) > 0 {
		tx, err := s.db.Begin()
		if err != nil {
			return nil, err
		}
		stmt, err := tx.Prepare("UPDATE files SET hash = ? WHERE path = ?")
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		for _, r := range out {
			if _, err := stmt.Exec(r.hash, r.path); err != nil {
				tx.Rollback()
				stmt.Close()
				return nil, err
			}
		}
		if err := stmt.Close(); err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}

	// Query groups where hash is identical and occurs > 1 times
	type hashGroupInfo struct {
		hash  string
		count int
		size  int64
	}
	var hashInfos []hashGroupInfo

	hashSQL := "SELECT hash, COUNT(*), size FROM files WHERE hash != ''"
	if scope != "" {
		hashSQL += " AND " + scope
	}
	hashSQL += " GROUP BY hash HAVING COUNT(*) > 1"
	hashRows, err := s.db.Query(hashSQL, scopeArgs...)
	if err != nil {
		return nil, err
	}
	for hashRows.Next() {
		var hgi hashGroupInfo
		if err := hashRows.Scan(&hgi.hash, &hgi.count, &hgi.size); err == nil {
			hashInfos = append(hashInfos, hgi)
		}
	}
	hashRows.Close()

	var groups []DuplicateGroup
	for gi, hgi := range hashInfos {
		g := DuplicateGroup{
			Hash:       hgi.hash,
			Size:       hgi.size,
			WastedSize: hgi.size * int64(hgi.count-1),
		}

		fileSQL := "SELECT path, rel_path, name, extension, mod_time, category FROM files WHERE hash = ?"
		if scope != "" {
			fileSQL += " AND " + scope
		}
		fileRows, err := s.db.Query(fileSQL, append([]any{hgi.hash}, scopeArgs...)...)
		if err != nil {
			continue
		}
		var members []IndexedFile
		var paths []string
		for fileRows.Next() {
			var f IndexedFile
			var modUnix int64
			f.Size = hgi.size
			f.Hash = hgi.hash
			if err := fileRows.Scan(&f.Path, &f.RelPath, &f.Name, &f.Extension, &modUnix, &f.Category); err == nil {
				f.ModTime = time.Unix(modUnix, 0)
				members = append(members, f)
				paths = append(paths, f.Path)
			}
		}
		fileRows.Close()

		// Re-verify the grouped files actually share identical content right now.
		// A file can keep the same size and mtime yet change content, which the
		// incremental scan heuristic would otherwise miss. Re-hashing group
		// members guarantees we never report a false duplicate.
		confirmed := make([]bool, len(paths))
		if len(paths) > 1 {
			eg := errgroup.Group{}
			eg.SetLimit(runtime.GOMAXPROCS(0))
			for i, p := range paths {
				i, p := i, p
				eg.Go(func() error {
					// Files we hashed in this exact call are already verified-fresh;
					// re-reading them would double the I/O for no benefit. Only
					// DB-loaded hashes (skipped via the size+mtime heuristic on an
					// earlier run) need re-hashing to guard against stale content.
					if fresh[p] {
						confirmed[i] = true
						return nil
					}
					h, err := hashFile(p)
					if err == nil && h == hgi.hash {
						confirmed[i] = true
					}
					return nil
				})
			}
			_ = eg.Wait()
		}

		for i, f := range members {
			if confirmed[i] {
				g.Files = append(g.Files, f)
			}
		}

		// Only report groups that still have 2+ identical files.
		if len(g.Files) >= 2 {
			g.WastedSize = g.Size * int64(len(g.Files)-1)
			groups = append(groups, g)
		}
		n := len(hashInfos)
		if n > 0 {
			emit(lerp(int(700 + int64(300)*int64(gi+1)/int64(n))))
		}
	}
	emit(lerp(1000))

	return groups, nil
}

// Analyze generates complete intelligence report.
// When `root` is non-empty, the report is scoped to that indexed folder subtree.
func (s *Store) Analyze(root string) (AnalyzeReport, error) {
	return s.AnalyzeProgress(root, nil)
}

// AnalyzeProgress is Analyze with a 0-100 progress callback. The CLI uses it to
// render a live bar across the stats, duplicate and old/empty phases instead of
// static "computing…" text.
func (s *Store) AnalyzeProgress(root string, progress func(pct int)) (AnalyzeReport, error) {
	scope, scopeArgs := pathScope(root)
	emit := func(pct int) {
		if progress != nil {
			progress(pct)
		}
	}

	emit(5)
	stats, err := s.GetStatsProgress(root, 5, 30, progress)
	if err != nil {
		return AnalyzeReport{}, err
	}

	emit(40)
	dups, _ := s.FindDuplicatesProgress(root, 40, 90, progress)

	var report AnalyzeReport
	report.Stats = stats
	report.DuplicateGroups = dups

	// Old files (> 180 days ago)
	sixMonthsAgo := time.Now().AddDate(0, -6, 0).Unix()
	emit(95)
	oldSQL := "SELECT path, rel_path, name, extension, size, mod_time, category FROM files WHERE mod_time < ?"
	if scope != "" {
		oldSQL += " AND " + scope
	}
	oldSQL += " ORDER BY mod_time ASC LIMIT 20"
	oldRows, err := s.db.Query(oldSQL, append([]any{sixMonthsAgo}, scopeArgs...)...)
	if err == nil {
		defer oldRows.Close()
		for oldRows.Next() {
			var f IndexedFile
			var modUnix int64
			if err := oldRows.Scan(&f.Path, &f.RelPath, &f.Name, &f.Extension, &f.Size, &modUnix, &f.Category); err == nil {
				f.ModTime = time.Unix(modUnix, 0)
				report.OldFiles = append(report.OldFiles, f)
			}
		}
	}

	// Empty files
	emit(98)
	emptySQL := "SELECT path, rel_path, name, extension, size, mod_time, category FROM files WHERE size = 0"
	if scope != "" {
		emptySQL += " AND " + scope
	}
	emptySQL += " LIMIT 20"
	emptyRows, err := s.db.Query(emptySQL, scopeArgs...)
	if err == nil {
		defer emptyRows.Close()
		for emptyRows.Next() {
			var f IndexedFile
			var modUnix int64
			if err := emptyRows.Scan(&f.Path, &f.RelPath, &f.Name, &f.Extension, &f.Size, &modUnix, &f.Category); err == nil {
				f.ModTime = time.Unix(modUnix, 0)
				report.EmptyFiles = append(report.EmptyFiles, f)
			}
		}
	}

	emit(100)

	return report, nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
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
