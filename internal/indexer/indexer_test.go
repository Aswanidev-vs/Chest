package indexer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndexerStore(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_index.db")

	store, err := OpenOrCreate(dbPath)
	if err != nil {
		t.Fatalf("Failed to open sqlite store: %v", err)
	}
	defer store.Close()

	// Create test files
	filesDir := filepath.Join(tempDir, "sample_files")
	_ = os.MkdirAll(filesDir, 0755)

	f1 := filepath.Join(filesDir, "doc1.txt")
	f2 := filepath.Join(filesDir, "doc2.txt") // identical duplicate
	f3 := filepath.Join(filesDir, "image.png")

	identicalContent := []byte("hello duplicate world from chest")
	_ = os.WriteFile(f1, identicalContent, 0644)
	_ = os.WriteFile(f2, identicalContent, 0644)
	_ = os.WriteFile(f3, []byte("png fake image binary data larger"), 0644)

	// Test Indexing
	count, err := store.IndexDirectory(filesDir, true, nil)
	if err != nil {
		t.Fatalf("Failed to index: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected 3 indexed files, got %d", count)
	}

	// Test Stats
	stats, err := store.GetStats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats.TotalFiles != 3 {
		t.Errorf("Expected 3 total files in stats, got %d", stats.TotalFiles)
	}

	// Test Duplicates
	dups, err := store.FindDuplicates()
	if err != nil {
		t.Fatalf("Failed finding duplicates: %v", err)
	}
	if len(dups) != 1 {
		t.Fatalf("Expected 1 duplicate group, got %d", len(dups))
	}
	if len(dups[0].Files) != 2 {
		t.Errorf("Expected 2 files in duplicate group, got %d", len(dups[0].Files))
	}

	// Test Analyze
	report, err := store.Analyze()
	if err != nil {
		t.Fatalf("Failed to analyze: %v", err)
	}
	if len(report.DuplicateGroups) != 1 {
		t.Errorf("Expected 1 duplicate group in analyze report")
	}
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
	if _, err := store.FindDuplicates(); err != nil {
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
