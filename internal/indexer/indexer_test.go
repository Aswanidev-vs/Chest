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
