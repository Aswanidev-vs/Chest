package scheduler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	// Inject the path so the test never touches the real ~/.chest.
	mgr := NewManager(filepath.Join(t.TempDir(), "schedules.toml"))

	id1, err := mgr.Add(Schedule{
		Name:      "nightly",
		Cron:      "0 2 * * *",
		Directory: "/tmp/downloads",
		Preset:    "downloads",
		Rules:     []string{"*.tmp -> Temp"},
	})
	if err != nil {
		t.Fatalf("Add(): %v", err)
	}
	if id1 != "s1" {
		t.Fatalf("first id = %q, want s1", id1)
	}

	if _, err := mgr.Add(Schedule{Cron: "30 4 * * *", Directory: "/tmp/desk"}); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	list, err := mgr.List()
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() returned %d entries, want 2", len(list))
	}
	if list[0].Name != "nightly" || list[0].Rules[0] != "*.tmp -> Temp" || list[0].Preset != "downloads" {
		t.Fatalf("round-trip lost fields: %+v", list[0])
	}
	if list[0].CreatedAt.IsZero() {
		t.Fatal("CreatedAt not populated")
	}

	// A fresh manager over the same file must observe the persisted state.
	reloaded := NewManager(mgr.filePath)
	got, err := reloaded.Get("s1")
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if got.Directory != "/tmp/downloads" {
		t.Fatalf("Get() directory = %q", got.Directory)
	}

	if _, err := reloaded.Get("nope"); err == nil {
		t.Fatal("Get() on missing id must error")
	}
}

func TestStoreRemoveAndIDStability(t *testing.T) {
	mgr := NewManager(filepath.Join(t.TempDir(), "schedules.toml"))

	for range 3 {
		if _, err := mgr.Add(Schedule{Cron: "0 2 * * *", Directory: "/tmp"}); err != nil {
			t.Fatalf("Add(): %v", err)
		}
	}
	// IDs must be unique before and after removing a middle entry.
	if err := mgr.Remove("s2"); err != nil {
		t.Fatalf("Remove(): %v", err)
	}

	list, err := mgr.List()
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(list) != 2 || list[0].ID != "s1" || list[1].ID != "s3" {
		t.Fatalf("after remove = %+v", list)
	}

	newID, err := mgr.Add(Schedule{Cron: "0 3 * * *", Directory: "/tmp"})
	if err != nil {
		t.Fatalf("Add(): %v", err)
	}
	if newID != "s4" {
		t.Fatalf("new id = %q, want s4 (must not collide with surviving s1/s3)", newID)
	}

	if err := mgr.Remove("s4"); err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	if err := mgr.Remove("s4"); err == nil {
		t.Fatal("Remove() on missing id must error")
	}
}

func TestLoadCorruptFileWrapsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedules.toml")
	if err := os.WriteFile(path, []byte("not valid toml {{{"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := NewManager(path).List()
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q must mention the file path", err)
	}
}
