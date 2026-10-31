package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runSortOn executes the sort command against dir and captures stdout (the
// planner prints via fmt.Println / cmd.OutOrStdout).
func runSortOn(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w

	cmd := newSortCmd()
	cmd.SetArgs(append([]string{dir}, args...))
	execErr := cmd.Execute()

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	r.Close()
	return string(out), execErr
}

// conflictFixture builds root/a/photo.jpg and root/b/photo.jpg so both files
// resolve to the same destination (Images/photo.jpg) and collide inside the
// plan.
func conflictFixture(t *testing.T, firstContent, secondContent string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a"), 0755); err != nil {
		t.Fatalf("failed to create folder: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "b"), 0755); err != nil {
		t.Fatalf("failed to create folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "photo.jpg"), []byte(firstContent), 0644); err != nil {
		t.Fatalf("failed to create a/photo.jpg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b", "photo.jpg"), []byte(secondContent), 0644); err != nil {
		t.Fatalf("failed to create b/photo.jpg: %v", err)
	}
	return root
}

// TestSortHashCollisionsSkipsIdenticalDuplicate: two same-named files with the
// same content are a genuine duplicate, so the second is left in place instead
// of piling up as "photo (1).jpg", and the decision is recorded.
func TestSortHashCollisionsSkipsIdenticalDuplicate(t *testing.T) {
	root := conflictFixture(t, "identical bytes", "identical bytes")

	output, err := runSortOn(t, root, "-r", "--dry-run", "--yes", "--hash-collisions")
	if err != nil {
		t.Fatalf("sort --hash-collisions error = %v", err)
	}

	if !strings.Contains(output, "1 files would be moved") {
		t.Errorf("expected only the first file to be planned:\n%s", output)
	}
	if strings.Contains(output, "photo (1).jpg") {
		t.Errorf("the duplicate should not be auto-renamed:\n%s", output)
	}
	if !strings.Contains(output, "1 conflict(s) detected during planning") {
		t.Errorf("expected the conflict to be recorded:\n%s", output)
	}
	if !strings.Contains(output, "identical content") {
		t.Errorf("expected a content-based resolution:\n%s", output)
	}
}

// TestSortHashCollisionsRenamesDistinctContent: different content means the two
// files are genuinely different, so both survive under the normal auto-rename.
func TestSortHashCollisionsRenamesDistinctContent(t *testing.T) {
	root := conflictFixture(t, "first content", "second content")

	output, err := runSortOn(t, root, "-r", "--dry-run", "--yes", "--hash-collisions")
	if err != nil {
		t.Fatalf("sort --hash-collisions error = %v", err)
	}

	if !strings.Contains(output, "2 files would be moved") {
		t.Errorf("both files should still be planned:\n%s", output)
	}
	if !strings.Contains(output, "photo (1).jpg") {
		t.Errorf("distinct content should be auto-renamed:\n%s", output)
	}
	if strings.Contains(output, "identical content") {
		t.Errorf("distinct content must not be reported as a duplicate:\n%s", output)
	}
	if !strings.Contains(output, "1 conflict(s) detected during planning") {
		t.Errorf("expected the conflict to be recorded:\n%s", output)
	}
}

func TestSortHashCollisionsKeepIdenticalRenamesDuplicate(t *testing.T) {
	root := conflictFixture(t, "identical bytes", "identical bytes")

	output, err := runSortOn(t, root, "-r", "--format", "--dry-run", "--yes", "--hash-collisions", "--keep-identical")
	if err != nil {
		t.Fatalf("sort --hash-collisions --keep-identical error = %v", err)
	}

	if !strings.Contains(output, "2 files would be moved") {
		t.Errorf("keep-identical should plan both files:\n%s", output)
	}
	if !strings.Contains(output, "photo (1).jpg") {
		t.Errorf("keep-identical should use the numbered stem collision name:\n%s", output)
	}
	if strings.Contains(output, "identical content") {
		t.Errorf("keep-identical must not report the duplicate as skipped:\n%s", output)
	}
}

// TestSortWithoutHashCollisionsAutoRenamesDuplicate is the regression guard:
// with the flag off, two byte-identical files keep today's auto-rename result.
func TestSortWithoutHashCollisionsAutoRenamesDuplicate(t *testing.T) {
	root := conflictFixture(t, "identical bytes", "identical bytes")

	output, err := runSortOn(t, root, "-r", "--dry-run", "--yes")
	if err != nil {
		t.Fatalf("sort error = %v", err)
	}

	if !strings.Contains(output, "2 files would be moved") {
		t.Errorf("default behavior must still plan both files:\n%s", output)
	}
	if !strings.Contains(output, "photo (1).jpg") {
		t.Errorf("default behavior must still auto-rename the second file:\n%s", output)
	}
	if strings.Contains(output, "identical content") {
		t.Errorf("content must not be inspected without --hash-collisions:\n%s", output)
	}
}

// TestSortHashCollisionsOffLeavesOutputIdentical pins the strongest guarantee:
// for a run where content does not change any decision, adding the flag must
// not change a single byte of the plan output.
func TestSortHashCollisionsOffLeavesOutputIdentical(t *testing.T) {
	root := conflictFixture(t, "first content", "second content")

	withoutFlag, err := runSortOn(t, root, "-r", "--dry-run", "--yes")
	if err != nil {
		t.Fatalf("sort without --hash-collisions error = %v", err)
	}
	withFlag, err := runSortOn(t, root, "-r", "--dry-run", "--yes", "--hash-collisions")
	if err != nil {
		t.Fatalf("sort with --hash-collisions error = %v", err)
	}

	if withoutFlag != withFlag {
		t.Errorf("--hash-collisions changed the default plan output.\nwithout flag:\n%s\nwith flag:\n%s", withoutFlag, withFlag)
	}
}

// TestSortHashCollisionsJSONPlanConflict verifies the recorded resolution is
// visible in the machine-readable plan too.
func TestSortHashCollisionsJSONPlanConflict(t *testing.T) {
	root := conflictFixture(t, "identical bytes", "identical bytes")

	output, err := runSortOn(t, root, "-r", "--dry-run", "--yes", "--json", "--hash-collisions")
	if err != nil {
		t.Fatalf("sort --json error = %v", err)
	}

	if !strings.Contains(output, `"resolution"`) {
		t.Errorf("expected conflicts in the JSON plan:\n%s", output)
	}
	if !strings.Contains(output, "identical content") {
		t.Errorf("expected the content-based resolution in the JSON plan:\n%s", output)
	}
	if strings.Contains(output, `"destination"`) && !strings.Contains(output, "photo.jpg") {
		t.Errorf("expected a planned destination in the JSON plan:\n%s", output)
	}
}
