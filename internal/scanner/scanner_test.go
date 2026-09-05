package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanner(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "chest_scanner_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create normal file
	normalFile := filepath.Join(tempDir, "file1.txt")
	if err := os.WriteFile(normalFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create dotfile (hidden on Unix)
	dotFile := filepath.Join(tempDir, ".dotfile.txt")
	if err := os.WriteFile(dotFile, []byte("hidden"), 0644); err != nil {
		t.Fatal(err)
	}

	// Subdir
	subDir := filepath.Join(tempDir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	subFile := filepath.Join(subDir, "file2.pdf")
	if err := os.WriteFile(subFile, []byte("%PDF-1.4"), 0644); err != nil {
		t.Fatal(err)
	}

	// Test 1: Scan excluding hidden
	s := New(ScanOptions{
		Recursive:     true,
		IncludeHidden: false,
	})

	files, err := s.Scan(tempDir)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("expected 2 files without hidden, got %d", len(files))
	}

	// Test 2: Scan including hidden
	sHidden := New(ScanOptions{
		Recursive:     true,
		IncludeHidden: true,
	})

	filesHidden, err := sHidden.Scan(tempDir)
	if err != nil {
		t.Fatalf("scan with hidden failed: %v", err)
	}

	if len(filesHidden) != 3 {
		t.Errorf("expected 3 files including hidden, got %d", len(filesHidden))
	}
}
