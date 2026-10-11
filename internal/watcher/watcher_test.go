package watcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/planner"
	"github.com/Aswanidev-vs/chest/internal/rules"
)

func TestProcessBatchRecordsSingleHistoryEntry(t *testing.T) {
	dir := t.TempDir()

	f1 := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f1, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	f2 := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(f2, []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}

	// A condition-less rule matches every file and routes it into Docs/.
	engine := rules.NewEngine([]models.Rule{{Name: "Docs", Destination: "Docs"}})

	hist := history.NewManager(filepath.Join(dir, "history.json"))
	w := &Watcher{
		opts:    WatchOptions{Directory: dir, DateSource: "modified"},
		planner: planner.New(engine, "", models.CollisionRename),
		history: hist,
	}

	var events []Event
	w.processBatch([]string{f1, f2}, func(ev Event) {
		events = append(events, ev)
	})

	// Both files should be routed into Docs/ on disk.
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dir, "Docs", name)); err != nil {
			t.Errorf("expected %s routed into Docs/: %v", name, err)
		}
	}

	// One KindOrganized event per planned operation keeps live feedback intact.
	if len(events) != 2 {
		t.Fatalf("expected 2 organized events, got %d", len(events))
	}
	dests := make(map[string]bool)
	for _, ev := range events {
		if ev.Kind != KindOrganized {
			t.Errorf("expected KindOrganized, got %v", ev.Kind)
		}
		if ev.Dest == "" {
			t.Errorf("organized event missing destination")
		}
		dests[ev.Dest] = true
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if !dests[filepath.Join(dir, "Docs", name)] {
			t.Errorf("missing organized event destination for %s (seen %v)", name, dests)
		}
	}

	// The whole batch must produce exactly ONE history entry, not one per file.
	entries, err := hist.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly ONE history entry for the batch, got %d", len(entries))
	}
	if entries[0].FilesCount != 2 {
		t.Errorf("expected FilesCount 2, got %d", entries[0].FilesCount)
	}
	if entries[0].Status != "Complete" {
		t.Errorf("expected Complete status, got %s", entries[0].Status)
	}
}
