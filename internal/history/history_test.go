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

func TestHistoryRecordPartialAndFailed(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(filepath.Join(tempDir, "history.json"))

	// Complete: moved files, no errors (existing callers compile unchanged).
	complete, err := mgr.Record(tempDir, []models.HistoryOperation{
		{OriginalSource: filepath.Join(tempDir, "c"), Destination: filepath.Join(tempDir, "Docs", "c"), Reason: "test"},
	})
	if err != nil {
		t.Fatalf("record complete: %v", err)
	}
	if complete.Status != "Complete" {
		t.Errorf("expected Complete, got %s", complete.Status)
	}
	if len(complete.Errors) != 0 {
		t.Errorf("expected no errors, got %v", complete.Errors)
	}

	// Partial: some ops moved alongside recorded per-file failures.
	partial, err := mgr.Record(tempDir, []models.HistoryOperation{
		{OriginalSource: filepath.Join(tempDir, "p"), Destination: filepath.Join(tempDir, "Docs", "p"), Reason: "test"},
	}, "move q: permission denied")
	if err != nil {
		t.Fatalf("record partial: %v", err)
	}
	if partial.Status != "Partial" {
		t.Errorf("expected Partial, got %s", partial.Status)
	}
	if len(partial.Errors) != 1 {
		t.Errorf("expected 1 recorded error, got %d", len(partial.Errors))
	}

	// Failed: errors but nothing moved.
	failed, err := mgr.Record(tempDir, nil, "move x: no space")
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}
	if failed.Status != "Failed" {
		t.Errorf("expected Failed, got %s", failed.Status)
	}
}

func TestHistoryUndoRollbackOnMidBatchFailure(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(filepath.Join(tempDir, "history.json"))

	// op0: a real moved file that can be restored cleanly, proving it was
	// touched before the failing op and then rolled back.
	dest0 := filepath.Join(tempDir, "Docs", "ok.txt")
	src0 := filepath.Join(tempDir, "ok.txt")
	if err := os.MkdirAll(filepath.Dir(dest0), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest0, []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	// op1: destination exists, but its restore target sits *under a file* so
	// os.Stat reports it missing (the safety pre-check passes) while MoveFile's
	// EnsureDir fails: a deterministic mid-batch failure without relying on
	// permissions tricks or monkeypatching.
	blocker := filepath.Join(tempDir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	dest1 := filepath.Join(tempDir, "Videos", "bad.mp4")
	src1 := filepath.Join(blocker, "bad.mp4") // parent is a file
	if _, err := os.Stat(src1); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s must appear absent, stat err=%v", src1, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest1), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest1, []byte("bad"), 0644); err != nil {
		t.Fatal(err)
	}

	entry, err := mgr.Record(tempDir, []models.HistoryOperation{
		{OriginalSource: src0, Destination: dest0, Reason: "test"},
		{OriginalSource: src1, Destination: dest1, Reason: "test"},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	_, count, err := mgr.Undo(entry.ID)
	if err == nil {
		t.Fatalf("expected undo to fail mid-batch, got count=%d", count)
	}
	if count != 0 {
		t.Errorf("expected restored count 0 after rollback, got %d", count)
	}

	// op0 was restored then rolled back: back at dest0, gone from src0.
	if _, err := os.Stat(dest0); err != nil {
		t.Errorf("op0 file should be rolled back to %s: %v", dest0, err)
	}
	if _, err := os.Stat(src0); !os.IsNotExist(err) {
		t.Errorf("op0 file should not remain at %s", src0)
	}
	// op1 never moved.
	if _, err := os.Stat(dest1); err != nil {
		t.Errorf("op1 file must remain at %s", dest1)
	}

	// The entry must stay Complete (retryable), not Undone.
	entries, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Status != "Complete" {
		t.Fatalf("expected single Complete entry after failed undo, got %+v", entries)
	}
}
