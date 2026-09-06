package indexer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Aswanidev-vs/chest/internal/classifier"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/charlievieth/fastwalk"
	_ "github.com/ncruces/go-sqlite3/driver"
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
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed creating schema: %w", err)
	}

	return &Store{db: db, dbPath: dbPath}, nil
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

	// Build a normalized set of exclusion names/patterns.
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

	indexedCount := int64(0)
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

		if d.IsDir() {
			return nil
		}

		if strings.HasPrefix(name, ".") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		cat := classifier.ClassifyExtension(ext)
		rel, _ := filepath.Rel(root, path)

		hashVal := ""
		if computeHashes && info.Size() > 0 && info.Size() < 500*1024*1024 { // Hash files under 500MB
			h, err := hashFile(path)
			if err == nil {
				hashVal = h
			}
		}

		_, err = stmt.Exec(
			path,
			rel,
			name,
			ext,
			info.Size(),
			info.ModTime().Unix(),
			cat,
			hashVal,
		)
		if err != nil {
			return nil
		}

		// walkFn runs concurrently; guard the shared counter.
		atomic.AddInt64(&indexedCount, 1)
		if progress != nil && atomic.LoadInt64(&indexedCount)%50 == 0 {
			progress(int(atomic.LoadInt64(&indexedCount)))
		}

		return nil
	}

	if err := fastwalk.Walk(&walkConf, root, walkFn); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return int(indexedCount), nil
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

// GetStats returns storage breakdown across categories and largest files
func (s *Store) GetStats() (StatsSummary, error) {
	var summary StatsSummary
	summary.CategoryCounts = make(map[string]int)
	summary.CategorySizes = make(map[string]int64)

	// Total count & size
	row := s.db.QueryRow("SELECT COUNT(*), COALESCE(SUM(size), 0) FROM files")
	if err := row.Scan(&summary.TotalFiles, &summary.TotalSize); err != nil {
		return summary, err
	}

	// Category aggregates
	rows, err := s.db.Query("SELECT category, COUNT(*), SUM(size) FROM files GROUP BY category")
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

	// Top 10 largest files
	topRows, err := s.db.Query("SELECT path, rel_path, name, extension, size, mod_time, category FROM files ORDER BY size DESC LIMIT 10")
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

	return summary, nil
}

// FindDuplicates identifies identical files based on size and hash
func (s *Store) FindDuplicates() ([]DuplicateGroup, error) {
	// First compute hashes for files with matching sizes that don't have hashes yet
	sizeRows, err := s.db.Query("SELECT size FROM files WHERE size > 0 GROUP BY size HAVING COUNT(*) > 1")
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

	// For candidate sizes, compute hash if missing
	for _, sz := range candidateSizes {
		var unhashedPaths []string
		unhashed, err := s.db.Query("SELECT path FROM files WHERE size = ? AND (hash IS NULL OR hash = '')", sz)
		if err == nil {
			for unhashed.Next() {
				var p string
				if err := unhashed.Scan(&p); err == nil {
					unhashedPaths = append(unhashedPaths, p)
				}
			}
			unhashed.Close()
		}

		for _, p := range unhashedPaths {
			if h, err := hashFile(p); err == nil {
				_, _ = s.db.Exec("UPDATE files SET hash = ? WHERE path = ?", h, p)
			}
		}
	}

	// Query groups where hash is identical and occurs > 1 times
	type hashGroupInfo struct {
		hash  string
		count int
		size  int64
	}
	var hashInfos []hashGroupInfo

	hashRows, err := s.db.Query("SELECT hash, COUNT(*), size FROM files WHERE hash != '' GROUP BY hash HAVING COUNT(*) > 1")
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
	for _, hgi := range hashInfos {
		g := DuplicateGroup{
			Hash:       hgi.hash,
			Size:       hgi.size,
			WastedSize: hgi.size * int64(hgi.count-1),
		}

		fileRows, err := s.db.Query("SELECT path, rel_path, name, extension, mod_time, category FROM files WHERE hash = ?", hgi.hash)
		if err != nil {
			continue
		}
		for fileRows.Next() {
			var f IndexedFile
			var modUnix int64
			f.Size = hgi.size
			f.Hash = hgi.hash
			if err := fileRows.Scan(&f.Path, &f.RelPath, &f.Name, &f.Extension, &modUnix, &f.Category); err == nil {
				f.ModTime = time.Unix(modUnix, 0)
				g.Files = append(g.Files, f)
			}
		}
		fileRows.Close()

		groups = append(groups, g)
	}

	return groups, nil
}

// Analyze generates complete intelligence report
func (s *Store) Analyze() (AnalyzeReport, error) {
	stats, err := s.GetStats()
	if err != nil {
		return AnalyzeReport{}, err
	}

	dups, _ := s.FindDuplicates()

	var report AnalyzeReport
	report.Stats = stats
	report.DuplicateGroups = dups

	// Old files (> 180 days ago)
	sixMonthsAgo := time.Now().AddDate(0, -6, 0).Unix()
	oldRows, err := s.db.Query("SELECT path, rel_path, name, extension, size, mod_time, category FROM files WHERE mod_time < ? ORDER BY mod_time ASC LIMIT 20", sixMonthsAgo)
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
	emptyRows, err := s.db.Query("SELECT path, rel_path, name, extension, size, mod_time, category FROM files WHERE size = 0 LIMIT 20")
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
