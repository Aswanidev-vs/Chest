package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/indexer"
)

func TestRefreshIndexBeforeReport(t *testing.T) {
	store, err := indexer.OpenOrCreate(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Explicit path => always walks that scope (auto-refresh).
	target, err := refreshIndexBeforeReport(store, root, false, nil, false)
	if err != nil {
		t.Fatalf("scoped refresh: %v", err)
	}
	if filepath.Clean(target) != filepath.Clean(root) {
		t.Fatalf("expected target %q, got %q", root, target)
	}

	// Non-empty cache, no path, no force => skipped (reads cache as-is).
	skipped, err := refreshIndexBeforeReport(store, "", false, nil, false)
	if err != nil {
		t.Fatalf("skip check: %v", err)
	}
	if skipped != "" {
		t.Fatalf("expected skip, got target %q", skipped)
	}

	// force => walk even without a path (overrides the skip above).
	forced, err := refreshIndexBeforeReport(store, root, false, nil, true)
	if err != nil {
		t.Fatalf("forced refresh: %v", err)
	}
	if filepath.Clean(forced) != filepath.Clean(root) {
		t.Fatalf("forced target mismatch: %q vs %q", forced, root)
	}
}