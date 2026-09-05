package history

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/models"
)

func TestHistoryRecordAndUndo(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "chest_test_hist_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	histFile := filepath.Join(tempDir, "history.json")
	mgr := NewManager(histFile)

	// Create dummy test file
	srcPath := filepath.Join(tempDir, "original.txt")
	dstPath := filepath.Join(tempDir, "Docs", "original.txt")

	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dstPath, []byte("chest test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Record operation (as if it was moved from srcPath to dstPath)
	entry, err := mgr.Record(tempDir, []models.HistoryOperation{
		{
			OriginalSource: srcPath,
			Destination:    dstPath,
			Reason:         "test",
		},
	})
	if err != nil {
		t.Fatalf("failed recording history: %v", err)
	}

	if entry.ID != 1 {
		t.Errorf("expected entry ID 1, got %d", entry.ID)
	}

	// Run Undo
	undoneEntry, count, err := mgr.Undo(1)
	if err != nil {
		t.Fatalf("failed undo: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 restored file, got %d", count)
	}
	if undoneEntry.Status != "Undone" {
		t.Errorf("expected Undone status, got %s", undoneEntry.Status)
	}

	// Verify file is back at srcPath and gone from dstPath
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		t.Errorf("source file was not restored to %s", srcPath)
	}
	if _, err := os.Stat(dstPath); !os.IsNotExist(err) {
		t.Errorf("destination file still exists at %s", dstPath)
	}
}
