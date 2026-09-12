package indexer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestMain ensures no goroutines leak after the package's tests (catches, e.g.,
// an unsynchronized/floating writer goroutine in the indexer).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// newTestStore opens an isolated SQLite store for a test and registers its
// closure with the test lifecycle.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenOrCreate(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Failed to open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestIndexerStore(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_index.db")

	store, err := OpenOrCreate(dbPath)
	require.NoError(t, err, "Failed to open sqlite store")
	defer store.Close()

	// Create test files
	filesDir := filepath.Join(tempDir, "sample_files")
	require.NoError(t, os.MkdirAll(filesDir, 0755))

	f1 := filepath.Join(filesDir, "doc1.txt")
	f2 := filepath.Join(filesDir, "doc2.txt") // identical duplicate
	f3 := filepath.Join(filesDir, "image.png")

	identicalContent := []byte("hello duplicate world from chest")
	require.NoError(t, os.WriteFile(f1, identicalContent, 0644))
	require.NoError(t, os.WriteFile(f2, identicalContent, 0644))
	require.NoError(t, os.WriteFile(f3, []byte("png fake image binary data larger"), 0644))

	// Test Indexing
	count, err := store.IndexDirectory(filesDir, true, nil)
	require.NoError(t, err, "Failed to index")
	assert.Equal(t, 3, count, "Expected 3 indexed files")

	// Test Stats
	stats, err := store.GetStats("")
	require.NoError(t, err, "Failed to get stats")
	assert.Equal(t, 3, stats.TotalFiles, "Expected 3 total files in stats")

	// Test Duplicates
	dups, err := store.FindDuplicates(filesDir)
	require.NoError(t, err, "Failed finding duplicates")
	require.Len(t, dups, 1, "Expected 1 duplicate group")
	assert.Len(t, dups[0].Files, 2, "Expected 2 files in duplicate group")

	// Test Analyze
	report, err := store.Analyze(filesDir)
	require.NoError(t, err, "Failed to analyze")
	assert.Len(t, report.DuplicateGroups, 1, "Expected 1 duplicate group in analyze report")
}

func TestIndexDirectoryExcept(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_except.db")

	store, err := OpenOrCreate(dbPath)
	if err != nil {
		t.Fatalf("Failed to open sqlite store: %v", err)
	}
	defer store.Close()

	// Keep/Root files
	rootDir := filepath.Join(tempDir, "root")
	_ = os.MkdirAll(filepath.Join(rootDir, "keep_sub"), 0755)

	// Files that should be indexed
	dup1 := filepath.Join(rootDir, "dup.txt")
	dup2 := filepath.Join(rootDir, "keep_sub", "dup.txt")
	_ = os.WriteFile(dup1, []byte("same content abc"), 0644)
	_ = os.WriteFile(dup2, []byte("same content abc"), 0644)

	// Files that must be skipped via exclusion
	_ = os.MkdirAll(filepath.Join(rootDir, "node_modules", "pkg"), 0755)
	_ = os.WriteFile(filepath.Join(rootDir, "node_modules", "pkg", "index.js"), []byte("node dep x"), 0644)
	_ = os.WriteFile(filepath.Join(rootDir, "node_modules", "lock"), []byte("node dep y"), 0644)
	_ = os.MkdirAll(filepath.Join(rootDir, "venv"), 0755)
	_ = os.WriteFile(filepath.Join(rootDir, "venv", "site.py"), []byte("venv lib z"), 0644)
	_ = os.WriteFile(filepath.Join(rootDir, ".gitkeep"), []byte("hidden file"), 0644)

	count, err := store.IndexDirectory(rootDir, true, nil, []string{"node_modules,venv"})
	if err != nil {
		t.Fatalf("Failed to index with exclusions: %v", err)
	}
	if count != 2 {
		t.Fatalf("Expected 2 indexed files (excluded dirs pruned), got %d", count)
	}

	// Ensure excluded files are not in the index.
	for _, excluded := range []string{
		filepath.Join(rootDir, "node_modules", "pkg", "index.js"),
		filepath.Join(rootDir, "node_modules", "lock"),
		filepath.Join(rootDir, "venv", "site.py"),
	} {
		var c int
		err := store.db.QueryRow("SELECT COUNT(*) FROM files WHERE path = ?", excluded).Scan(&c)
		if err != nil || c != 0 {
			t.Errorf("Expected excluded path %s to be absent, got %d (err=%v)", excluded, c, err)
		}
	}

	// Glob-style exclusion of a file name, on a fresh root.
	globDir := filepath.Join(tempDir, "glob_root")
	_ = os.MkdirAll(globDir, 0755)
	_ = os.WriteFile(filepath.Join(globDir, "a.txt"), []byte("aaa"), 0644)
	_ = os.WriteFile(filepath.Join(globDir, "b.txt"), []byte("bbb"), 0644)
	_ = os.WriteFile(filepath.Join(globDir, "c.go"), []byte("ccc"), 0644)

	count, err = store.IndexDirectory(globDir, false, nil, []string{"*.txt"})
	if err != nil {
		t.Fatalf("Failed indexing with glob exclusion: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected 1 indexed file when excluding *.txt (c.go), got %d", count)
	}
}

func TestCountFiles(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_count.db")

	store, err := OpenOrCreate(dbPath)
	if err != nil {
		t.Fatalf("Failed to open sqlite store: %v", err)
	}
	defer store.Close()

	root := filepath.Join(tempDir, "root")
	_ = os.MkdirAll(filepath.Join(root, "sub"), 0755)
	_ = os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0755)

	_ = os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0644)
	_ = os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("b"), 0644)
	_ = os.WriteFile(filepath.Join(root, ".hidden"), []byte("h"), 0644)
	_ = os.WriteFile(filepath.Join(root, "node_modules", "pkg", "x.js"), []byte("x"), 0644)

	// No exclusions: hidden file skipped, node_modules counted.
	n, err := store.CountFiles(root)
	if err != nil {
		t.Fatalf("CountFiles failed: %v", err)
	}
	if n != 3 {
		t.Errorf("Expected 3 files (a.txt, b.txt, x.js), got %d", n)
	}

	// Excluded node_modules: pruned entirely.
	n, err = store.CountFiles(root, []string{"node_modules"})
	if err != nil {
		t.Fatalf("CountFiles with exclusions failed: %v", err)
	}
	if n != 2 {
		t.Errorf("Expected 2 files with node_modules excluded, got %d", n)
	}

	// CountFiles must agree with what IndexDirectory actually indexes.
	indexed, err := store.IndexDirectory(root, false, nil, []string{"node_modules"})
	if err != nil {
		t.Fatalf("IndexDirectory failed: %v", err)
	}
	if indexed != n {
		t.Errorf("CountFiles (%d) disagrees with IndexDirectory (%d)", n, indexed)
	}
}

func countRows(t *testing.T, store *Store, root string) int {
	t.Helper()
	scope, args := pathScope(root)
	query := "SELECT COUNT(*) FROM files"
	if scope != "" {
		query += " WHERE " + scope
	}
	var n int
	if err := store.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func TestIndexDirectoryPurgesDeletedFiles(t *testing.T) {
	store := newTestStore(t)
	root := t.TempDir()

	keep := filepath.Join(root, "keep.txt")
	gone := filepath.Join(root, "gone.txt")
	require.NoError(t, os.WriteFile(keep, []byte("keep"), 0644))
	require.NoError(t, os.WriteFile(gone, []byte("gone"), 0644))

	_, err := store.IndexDirectory(root, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, countRows(t, store, root))

	// Delete one file and re-index: its stale row must be removed.
	require.NoError(t, os.Remove(gone))
	_, err = store.IndexDirectory(root, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, countRows(t, store, root), "deleted file's row should be purged")
}

func TestIndexDirectoryPurgesExcludedRows(t *testing.T) {
	store := newTestStore(t)
	root := t.TempDir()

	// First index everything, including a file that will later be excluded.
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("go"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte("txt"), 0644))
	_, err := store.IndexDirectory(root, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, countRows(t, store, root))

	// Re-index excluding *.txt: the previously-indexed txt row must vanish.
	_, err = store.IndexDirectory(root, false, nil, []string{"*.txt"})
	require.NoError(t, err)
	assert.Equal(t, 1, countRows(t, store, root), "excluded file's stale row should be purged")

	var remain string
	require.NoError(t, store.db.QueryRow(
		"SELECT name FROM files WHERE name = 'main.go'").Scan(&remain))
	assert.Equal(t, "main.go", remain)
}

func TestIsEmpty(t *testing.T) {
	store := newTestStore(t)
	empty, err := store.IsEmpty()
	require.NoError(t, err)
	assert.True(t, empty, "fresh store should be empty")

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0644))
	_, err = store.IndexDirectory(root, false, nil)
	require.NoError(t, err)

	empty, err = store.IsEmpty()
	require.NoError(t, err)
	assert.False(t, empty, "store with indexed files should not be empty")
}

func TestPathWithinRootCaseSensitivity(t *testing.T) {
	// Same-casing must always be inside the root, on every platform.
	root := "/tmp/Projects"
	if !pathWithinRoot(root, "/tmp/Projects/file.txt") {
		t.Fatal("same-casing child should be within root")
	}
	if !pathWithinRoot(root, "/tmp/Projects") {
		t.Fatal("root itself should be within root")
	}

	// Differently-cased spelling: must only match on case-insensitive platforms.
	got := pathWithinRoot(root, "/tmp/projects/file.txt")
	if caseInsensitiveFS() != got {
		t.Errorf("case-insensitive match mismatch: platform=%q want=%v got=%v",
			runtime.GOOS, caseInsensitiveFS(), got)
	}

	// A sibling prefix must never match, regardless of case.
	if pathWithinRoot("/tmp/Projects", "/tmp/ProjectsExtra/x.txt") {
		t.Fatal("sibling-prefix dir leaked into root scope")
	}
	if caseInsensitiveFS() && pathWithinRoot("/tmp/Projects", "/tmp/ProjectX/x.txt") {
		t.Fatal("sibling different-case dir leaked into root scope")
	}
}

func TestPathScopeCaseSensitivity(t *testing.T) {
	ci := caseInsensitiveFS()
	sql, args := pathScope("/tmp/Projects")
	usesFold := strings.Contains(sql, "LOWER(")
	if ci != usesFold {
		t.Errorf("pathScope folding mismatch: platform=%q usesFold=%v want=%v", runtime.GOOS, usesFold, ci)
	}
	// One of the args must be the lowercased root when folding is active.
	if ci {
		want := strings.ToLower(filepath.Clean("/tmp/Projects"))
		matched := false
		for _, a := range args {
			if s, ok := a.(string); ok && s == want {
				matched = true
			}
		}
		if !matched {
			t.Errorf("pathScope folding did not pass a lowercased root arg: %v", args)
		}
	}
}

func TestGetStatsCaseInsensitiveScope(t *testing.T) {
	if !caseInsensitiveFS() {
		t.Skip("case-insensitive filesystem behavior only")
	}
	store := newTestStore(t)
	root := filepath.Join(t.TempDir(), "Data") // exact on-disk casing
	require.NoError(t, os.MkdirAll(root, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("aaa"), 0644))
	_, err := store.IndexDirectory(root, false, nil)
	require.NoError(t, err)

	// Query with a different casing of the same directory.
	alt := filepath.Join(filepath.Dir(root), "data")
	exact, err := store.GetStats(root)
	require.NoError(t, err)
	folded, err := store.GetStats(alt)
	require.NoError(t, err)
	assert.Equal(t, exact.TotalFiles, folded.TotalFiles,
		"differently-cased scope should return the same file count")
	assert.Equal(t, exact.TotalSize, folded.TotalSize,
		"differently-cased scope should return the same total size")
}

func TestMigrateRelativePaths(t *testing.T) {
	store := newTestStore(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	// Simulate a legacy `chest index .` row with a relative path.
	_, err = store.db.Exec(
		"INSERT INTO files (path, rel_path, name, extension, size, mod_time, category, hash) VALUES (?, ?, 'foo.txt', 'txt', 3, 0, 'OTHER', '')",
		"virtual/sub/foo.txt", "virtual/sub/foo.txt")
	require.NoError(t, err)

	// Force the migration to run in isolation (OpenOrCreate already stamped the
	// marker on the freshly created empty store).
	_, err = store.db.Exec("DELETE FROM meta WHERE key = 'paths_absolute_migrated'")
	require.NoError(t, err)
	require.NoError(t, store.migrateRelativePaths())

	var abs, rel string
	require.NoError(t, store.db.QueryRow(
		"SELECT path, rel_path FROM files WHERE name = 'foo.txt'").Scan(&abs, &rel))
	assert.True(t, filepath.IsAbs(abs), "legacy relative path should be made absolute")
	assert.Equal(t, filepath.Join(cwd, "virtual", "sub", "foo.txt"), abs)
	assert.Equal(t, filepath.Join("virtual", "sub", "foo.txt"), rel)

	// Marker is set, so a second invocation must be a no-op and idempotent.
	assert.NoError(t, store.migrateRelativePaths())
}

func TestIndexIncremental(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "incr.db")

	store, err := OpenOrCreate(dbPath)
	if err != nil {
		t.Fatalf("Failed to open sqlite store: %v", err)
	}
	defer store.Close()

	root := filepath.Join(tempDir, "root")
	_ = os.MkdirAll(root, 0755)
	f := filepath.Join(root, "a.txt")
	_ = os.WriteFile(f, []byte("hello"), 0644)

	// First run: all files are new.
	n1, err := store.IndexDirectory(root, true, nil)
	if err != nil {
		t.Fatalf("first index failed: %v", err)
	}
	if n1 != 1 {
		t.Fatalf("expected 1 new file on first index, got %d", n1)
	}

	// Second run: unchanged file should be skipped entirely (incremental).
	n2, err := store.IndexDirectory(root, true, nil)
	if err != nil {
		t.Fatalf("second index failed: %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 new files on incremental re-run, got %d", n2)
	}

	// The persisted hash must still make duplicates detectable.
	if _, err := store.FindDuplicates(root); err != nil {
		t.Fatalf("FindDuplicates failed: %v", err)
	}

	// Changing content (and thus size) should be detected on the next run.
	_ = os.WriteFile(f, []byte("hello world!"), 0644)
	n3, err := store.IndexDirectory(root, true, nil)
	if err != nil {
		t.Fatalf("third index failed: %v", err)
	}
	if n3 != 1 {
		t.Errorf("expected 1 new file after content change, got %d", n3)
	}
}

// TestDuplicateHashReverify proves FindDuplicates re-hashes group members, so a
// file whose content changed while keeping the same size and mtime (which the
// incremental heuristic would skip) is not falsely reported as a duplicate.
func TestDuplicateHashReverify(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "reverify.db")

	store, err := OpenOrCreate(dbPath)
	if err != nil {
		t.Fatalf("Failed to open sqlite store: %v", err)
	}
	defer store.Close()

	root := filepath.Join(tempDir, "root")
	f1 := filepath.Join(root, "a.txt")
	f2 := filepath.Join(root, "b.txt")
	_ = os.MkdirAll(root, 0755)
	_ = os.WriteFile(f1, []byte("AAAA"), 0644)
	_ = os.WriteFile(f2, []byte("AAAA"), 0644)

	// Index both identical files with hashes.
	if _, err := store.IndexDirectory(root, true, nil); err != nil {
		t.Fatalf("index failed: %v", err)
	}
	dups, err := store.FindDuplicates(root)
	if err != nil {
		t.Fatalf("FindDuplicates failed: %v", err)
	}
	if len(dups) != 1 || len(dups[0].Files) != 2 {
		t.Fatalf("expected 1 duplicate group of 2 files, got %d groups", len(dups))
	}

	// Change one file's content but keep size AND mtime unchanged, simulating
	// what the incremental heuristic would miss.
	orig := time.Now()
	_ = os.WriteFile(f2, []byte("BBBB"), 0644) // same length (4 bytes)
	_ = os.Chtimes(f2, orig, orig)             // reset mtime to original
	_ = os.Chtimes(f1, orig, orig)             // keep f1 mtime matching stored
	if _, err := store.IndexDirectory(root, true, nil); err != nil {
		t.Fatalf("re-index failed: %v", err)
	}

	// Even though size+mtime match, re-verification must drop the changed file.
	dups, err = store.FindDuplicates(root)
	if err != nil {
		t.Fatalf("FindDuplicates after change failed: %v", err)
	}
	if len(dups) != 0 {
		t.Fatalf("expected 0 duplicate groups after content divergence, got %d", len(dups))
	}
}
// TestFindDuplicatesComputesMissingHashes exercises the cold path exercised by
// `chest analyze` (which indexes with computeHashes=false): FindDuplicates must
// hash size-candidates from scratch, persist them, and — because those hashes
// were produced in this same call — skip re-reading them during re-verification
// while still reporting the duplicates correctly.
func TestFindDuplicatesComputesMissingHashes(t *testing.T) {
	store := newTestStore(t)
	root := t.TempDir()

	a := filepath.Join(root, "a.bin")
	b := filepath.Join(root, "b.bin")
	require.NoError(t, os.WriteFile(a, []byte("same-bytes-12345"), 0644))
	require.NoError(t, os.WriteFile(b, []byte("same-bytes-12345"), 0644))

	// Index WITHOUT hashing, exactly like the analyze command.
	idx, err := store.IndexDirectory(root, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, idx)

	// FindDuplicates computes + persists the missing hashes and still finds dup.
	dups, err := store.FindDuplicates(root)
	require.NoError(t, err)
	require.Len(t, dups, 1, "expected 1 duplicate group from computed hashes")
	assert.Len(t, dups[0].Files, 2)

	// Second call: hashes are persisted, nothing left to hash, same result.
	dups2, err := store.FindDuplicates(root)
	require.NoError(t, err)
	require.Len(t, dups2, 1, "expected same duplicate group on repeat call")

	// Distinct content (different size) must not be reported as duplicates.
	c := filepath.Join(root, "c.bin")
	require.NoError(t, os.WriteFile(c, []byte("different-size-content!"), 0644))
	_, err = store.IndexDirectory(root, false, nil)
	require.NoError(t, err)
	dups3, err := store.FindDuplicates(root)
	require.NoError(t, err)
	require.Len(t, dups3, 1, "still only the original 2-file group")
}

// TestIndexDirectoryParallelProgress races the progress callback, which the
// parallel fastwalk walk invokes from multiple workers simultaneously. The
// callback here is mutex-protected, so under `-race` this proves the indexer
// neither races its own progress invocation nor returns before workers finish,
// and goleak confirms no writer goroutine is leaked.
func TestIndexDirectoryParallelProgress(t *testing.T) {
	store := newTestStore(t)
	root := t.TempDir()

	const n = 200
	for i := 0; i < n; i++ {
		require.NoError(t,
			os.WriteFile(filepath.Join(root, fmt.Sprintf("f%03d.bin", i)),
				[]byte(strings.Repeat("x", 256)), 0644))
	}

	var (
		mu    sync.Mutex
		calls int
		last  int
	)
	count, err := store.IndexDirectory(root, true, func(cur int) {
		mu.Lock()
		calls++
		if cur > last {
			last = cur
		}
		mu.Unlock()
	})
	require.NoError(t, err)
	assert.Equal(t, n, count)

	mu.Lock()
	defer mu.Unlock()
	assert.Greater(t, calls, 0, "progress callback must be invoked")
	assert.Equal(t, n, last, "progress must reach the full file count")
}
