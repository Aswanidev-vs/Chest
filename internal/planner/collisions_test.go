package planner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/rules"
)

// imagesRule routes every scanned file into Images/, which is what makes two
// same-named files from different source folders collide on one destination.
func imagesRule() *rules.Engine {
	return rules.NewEngine([]models.Rule{{Name: "Photos", Priority: 20, Destination: "Images"}})
}

// sameNamedPair writes a/photo.jpg and b/photo.jpg with the given contents and
// returns the scanned files for them, in scan order.
func sameNamedPair(t *testing.T, root string, firstContent, secondContent string) []models.File {
	t.Helper()
	first := filepath.Join(root, "a", "photo.jpg")
	second := filepath.Join(root, "b", "photo.jpg")
	writeSourceFile(t, first, firstContent)
	writeSourceFile(t, second, secondContent)
	return []models.File{
		{Name: "photo.jpg", Path: first, ModTime: time.Now()},
		{Name: "photo.jpg", Path: second, ModTime: time.Now()},
	}
}

// recordingHasher swaps the package-level file hasher for a counting wrapper
// so a test can prove exactly which files content-aware resolution opens.
func recordingHasher(t *testing.T) *[]string {
	t.Helper()
	var opened []string
	original := sha256File
	sha256File = func(path string) (string, error) {
		opened = append(opened, path)
		return original(path)
	}
	t.Cleanup(func() { sha256File = original })
	return &opened
}

func TestPlanHashCollisionsSkipsIdenticalContent(t *testing.T) {
	root := t.TempDir()
	files := sameNamedPair(t, root, "identical bytes", "identical bytes")

	plan, err := New(imagesRule(), "", "", WithHashCollisions()).Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// The duplicate must stay exactly where it is: only the first file moves.
	if len(plan.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(plan.Operations))
	}
	if want := filepath.Join(root, "Images", "photo.jpg"); plan.Operations[0].Destination != want {
		t.Errorf("first destination = %q, want %q", plan.Operations[0].Destination, want)
	}
	if plan.SkippedFiles != 1 {
		t.Errorf("SkippedFiles = %d, want 1", plan.SkippedFiles)
	}

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	conflict := plan.Conflicts[0]
	if conflict.Target != files[1].Path {
		t.Errorf("conflict target = %q, want the untouched duplicate %q", conflict.Target, files[1].Path)
	}
	if !strings.Contains(conflict.Resolution, "identical content") {
		t.Errorf("conflict resolution = %q, want it to mention identical content", conflict.Resolution)
	}
}

func TestPlanHashCollisionsRenamesDistinctContent(t *testing.T) {
	root := t.TempDir()
	files := sameNamedPair(t, root, "first content", "second content")

	plan, err := New(imagesRule(), "", "", WithHashCollisions()).Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// Different content means no duplicate: both survive under the normal
	// auto-rename resolution.
	if len(plan.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(plan.Operations))
	}
	firstDest := filepath.Join(root, "Images", "photo.jpg")
	secondDest := filepath.Join(root, "Images", "photo (1).jpg")
	if plan.Operations[0].Destination != firstDest {
		t.Errorf("first destination = %q, want %q", plan.Operations[0].Destination, firstDest)
	}
	if plan.Operations[1].Destination != secondDest {
		t.Errorf("second destination = %q, want %q", plan.Operations[1].Destination, secondDest)
	}
	if !plan.Operations[1].IsCollision {
		t.Error("second operation should still be marked as a collision")
	}

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	if want := "auto-renamed second file to photo (1).jpg"; plan.Conflicts[0].Resolution != want {
		t.Errorf("conflict resolution = %q, want %q", plan.Conflicts[0].Resolution, want)
	}
}

// Regression guard: without the flag the planner must not consult content at
// all, so two byte-identical files keep today's auto-rename behavior.
func TestPlanWithoutHashCollisionsIgnoresContent(t *testing.T) {
	root := t.TempDir()
	files := sameNamedPair(t, root, "identical bytes", "identical bytes")

	opened := recordingHasher(t)

	plan, err := New(imagesRule(), "", "").Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if len(*opened) != 0 {
		t.Fatalf("planner hashed %d file(s) without --hash-collisions: %v", len(*opened), *opened)
	}
	if len(plan.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(plan.Operations))
	}
	if want := filepath.Join(root, "Images", "photo (1).jpg"); plan.Operations[1].Destination != want {
		t.Errorf("second destination = %q, want %q", plan.Operations[1].Destination, want)
	}
	if want := "auto-renamed second file to photo (1).jpg"; plan.Conflicts[0].Resolution != want {
		t.Errorf("conflict resolution = %q, want %q", plan.Conflicts[0].Resolution, want)
	}
}

// The whole point of the feature: hashing is scoped to the conflicting subset.
// Files that never collide must not be opened, even with the flag on, because a
// large tree must never pay for hashing it.
func TestPlanHashCollisionsOnlyHashesTheConflictSubset(t *testing.T) {
	root := t.TempDir()
	files := sameNamedPair(t, root, "identical bytes", "identical bytes")

	// Two non-colliding files, one of which does not even exist on disk: if the
	// planner ever hashed outside a conflict set this plan could not succeed
	// without touching them.
	lonely := filepath.Join(root, "c", "other.txt")
	writeSourceFile(t, lonely, "lonely content")
	files = append(files,
		models.File{Name: "other.txt", Path: lonely, ModTime: time.Now()},
		models.File{Name: "gone.txt", Path: filepath.Join(root, "c", "gone.txt"), ModTime: time.Now()},
	)

	opened := recordingHasher(t)

	plan, err := New(imagesRule(), "", "", WithHashCollisions()).Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// a/photo.jpg and b/photo.jpg are the only pair that shares a destination,
	// so they are the only files that may be hashed — and each exactly once.
	if len(*opened) != 2 {
		t.Fatalf("expected exactly 2 hashed files, got %d: %v", len(*opened), *opened)
	}
	hashed := map[string]int{}
	for _, path := range *opened {
		hashed[path]++
	}
	for _, path := range []string{files[0].Path, files[1].Path} {
		if hashed[path] != 1 {
			t.Errorf("hash reads for %q = %d, want 1", path, hashed[path])
		}
	}
	for _, path := range []string{lonely, files[2].Path} {
		if hashed[path] != 0 {
			t.Errorf("non-conflicting file %q was hashed %d time(s)", path, hashed[path])
		}
	}

	// photo.jpg (one operation, duplicate skipped), other.txt and gone.txt.
	// Planning never required the two non-conflicting files to be readable.
	if len(plan.Operations) != 3 {
		t.Fatalf("expected 3 operations, got %d", len(plan.Operations))
	}
}

func TestContentHasherCachesAndRemembersUnreadableFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	writeSourceFile(t, path, "content")

	h := newContentHasher()
	if !h.identical(path, path) {
		t.Fatal("identical() = false for the same path")
	}
	if len(h.digests) != 1 {
		t.Fatalf("digests = %d, want 1", len(h.digests))
	}

	// A second call must be served from the cache.
	if !h.identical(path, path) {
		t.Fatal("cached identical() = false")
	}
	if len(h.digests) != 1 {
		t.Fatalf("digests = %d after a cached call, want 1", len(h.digests))
	}

	// An unreadable file is reported as distinct, never as a duplicate, and is
	// not retried.
	missing := filepath.Join(root, "missing.txt")
	if h.identical(path, missing) {
		t.Fatal("identical() = true against a missing file")
	}
	if !h.unread[missing] {
		t.Fatal("missing file was not recorded as unread")
	}
	if _, ok := h.digest(missing); ok {
		t.Fatal("digest() succeeded for a missing file")
	}
}

func TestSHA256FileMatchesKnownDigest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(path, []byte("chest"), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}

	// printf 'chest' | sha256sum
	const want = "7ca0d7019158ccd92de4eb351a1e9eded193a1eb3e760b09b154595f0436f0b4"

	got, err := streamSHA256(path)
	if err != nil {
		t.Fatalf("streamSHA256() error = %v", err)
	}
	if got != want {
		t.Fatalf("streamSHA256() = %q, want %q", got, want)
	}

	if _, err := streamSHA256(filepath.Join(root, "nope.txt")); err == nil {
		t.Fatal("expected an error hashing a missing file")
	}
}

// HashCollisions composes with an explicit rename policy: an identical pair is
// still deduplicated, a distinct pair still walks the " (n)" chain.
func TestPlanHashCollisionsUnderExplicitRenamePolicy(t *testing.T) {
	root := t.TempDir()
	files := sameNamedPair(t, root, "identical bytes", "identical bytes")

	plan, err := New(imagesRule(), "", models.CollisionRename, WithHashCollisions()).Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(plan.Operations))
	}
	if !strings.Contains(plan.Conflicts[0].Resolution, "identical content") {
		t.Errorf("conflict resolution = %q, want it to mention identical content", plan.Conflicts[0].Resolution)
	}
}

// The abort policy still aborts under --hash-collisions: the user asked for the
// run to stop on any collision and content inspection must not soften that.
func TestPlanHashCollisionsKeepsAbortPolicy(t *testing.T) {
	root := t.TempDir()
	files := sameNamedPair(t, root, "identical bytes", "identical bytes")

	if _, err := New(imagesRule(), "", models.CollisionAbort, WithHashCollisions()).Plan(root, files); err == nil {
		t.Fatal("expected an error from the abort policy, got nil")
	}
}
