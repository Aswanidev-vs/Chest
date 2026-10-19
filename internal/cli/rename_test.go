package cli

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/organizer"
)

// runRename executes the rename command with the given args and captures
// stdout (the command writes through cmd.OutOrStdout, which resolves to
// os.Stdout when SetOut is never called).
func runRename(t *testing.T, args ...string) (string, error) {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w

	cmd := newRenameCmd()
	cmd.SetArgs(args)
	execErr := cmd.Execute()

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	r.Close()
	return string(out), execErr
}

func writeRenameFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create folder for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create %s: %v", path, err)
	}
}

func readRenameFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(data)
}

// TestRenameDryRunPreviewsWithoutTouchingDisk verifies the dry run shows a
// FROM/TO preview of the normalized name and leaves the filesystem alone.
func TestRenameDryRunPreviewsWithoutTouchingDisk(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "IMG_20230115 copy(2).jpg")
	writeRenameFixture(t, original, "photo bytes")

	output, err := runRename(t, root,
		"--to", "{date}.{ext}",
		"--date-source", "filename",
		"--date-granularity", "day",
		"--month-format", "number",
		"--dry-run", "--yes",
	)
	if err != nil {
		t.Fatalf("rename --dry-run error = %v", err)
	}

	if !strings.Contains(output, "CHEST RENAME PLAN") {
		t.Errorf("missing plan header in output:\n%s", output)
	}
	if !strings.Contains(output, "FROM: ") || !strings.Contains(output, "TO:   ") {
		t.Errorf("missing FROM/TO preview in output:\n%s", output)
	}
	if !strings.Contains(output, "2023-01-15.jpg") {
		t.Errorf("expected the normalized name 2023-01-15.jpg in output:\n%s", output)
	}
	if !strings.Contains(output, "No changes made.") {
		t.Errorf("dry run should state that nothing changed:\n%s", output)
	}

	if !strings.Contains(output, "IMG_20230115 copy(2).jpg") {
		t.Errorf("preview should name the original file:\n%s", output)
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatalf("dry run must not touch the original file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "2023-01-15.jpg")); err == nil {
		t.Fatal("dry run must not create the renamed file")
	}
}

// TestRenameAppliesAndUndoRestoresOriginalNames drives the full apply path:
// plan, execute, then history undo. The old name must come back byte for byte.
func TestRenameAppliesAndUndoRestoresOriginalNames(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "IMG_20230115 copy(2).jpg")
	writeRenameFixture(t, original, "photo bytes")

	renameTo := filepath.Join(root, "2023-01-15.jpg")
	if err := os.Chtimes(original, time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC), time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("failed to set mtime: %v", err)
	}

	files := []models.File{{
		Name:      filepath.Base(original),
		Path:      original,
		Extension: "jpg",
		ModTime:   time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC),
	}}

	plan, problems, err := buildRenamePlan(root, files, renameOptions{
		Template:        "{year}-{month}-{day}.{ext}",
		DateSource:      "modified",
		DateGranularity: "day",
		MonthFormat:     "number",
		CollisionPolicy: models.CollisionRename,
	})
	if err != nil {
		t.Fatalf("buildRenamePlan() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(plan.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(plan.Operations))
	}
	if plan.Operations[0].Destination != renameTo {
		t.Fatalf("destination = %q, want %q", plan.Operations[0].Destination, renameTo)
	}

	histMgr := history.NewManager(filepath.Join(t.TempDir(), "history.json"))
	result, err := organizer.Execute(plan, histMgr, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.FilesMoved != 1 {
		t.Fatalf("FilesMoved = %d, want 1", result.FilesMoved)
	}

	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatalf("original should be gone after the rename, stat err = %v", err)
	}
	if got := readRenameFixture(t, renameTo); got != "photo bytes" {
		t.Errorf("renamed content = %q, want the original bytes", got)
	}

	// Undo must restore the original name exactly.
	if _, restored, err := histMgr.Undo(0, true); err != nil {
		t.Fatalf("Undo() error = %v", err)
	} else if restored != 1 {
		t.Fatalf("Undo() restored %d file(s), want 1", restored)
	}
	if _, err := os.Stat(renameTo); !os.IsNotExist(err) {
		t.Fatalf("renamed file should be gone after undo, stat err = %v", err)
	}
	if got := readRenameFixture(t, original); got != "photo bytes" {
		t.Errorf("restored content = %q, want the original bytes", got)
	}
}

// TestRenameCollisionAutoRenamesSecondFile covers two names in the same folder
// collapsing onto one target: the second walks the "name (1).ext" chain and
// the first is never overwritten.
func TestRenameCollisionAutoRenamesSecondFile(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "IMG_0001.jpg")
	collide := filepath.Join(root, "IMG_0002.jpg")
	writeRenameFixture(t, keep, "first content")
	writeRenameFixture(t, collide, "second content")

	files := []models.File{
		{Name: "IMG_0001.jpg", Path: keep, Extension: "jpg", ModTime: time.Now()},
		{Name: "IMG_0002.jpg", Path: collide, Extension: "jpg", ModTime: time.Now()},
	}

	plan, _, err := buildRenamePlan(root, files, renameOptions{
		Template:        "photo.{ext}",
		DateSource:      "modified",
		DateGranularity: "year",
		CollisionPolicy: models.CollisionRename,
	})
	if err != nil {
		t.Fatalf("buildRenamePlan() error = %v", err)
	}

	if len(plan.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(plan.Operations))
	}
	if want := filepath.Join(root, "photo.jpg"); plan.Operations[0].Destination != want {
		t.Errorf("first destination = %q, want %q", plan.Operations[0].Destination, want)
	}
	if want := filepath.Join(root, "photo (1).jpg"); plan.Operations[1].Destination != want {
		t.Errorf("second destination = %q, want %q", plan.Operations[1].Destination, want)
	}
	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}

	if _, err := organizer.Execute(plan, nil, nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := readRenameFixture(t, filepath.Join(root, "photo.jpg")); got != "first content" {
		t.Errorf("photo.jpg content = %q, want the first file's content", got)
	}
	if got := readRenameFixture(t, filepath.Join(root, "photo (1).jpg")); got != "second content" {
		t.Errorf("photo (1).jpg content = %q, want the second file's content", got)
	}
}

// TestRenameCollisionWithExistingFileNeverOverwrites checks the on-disk branch:
// the renamed file must land on a fresh name, not on top of the existing one.
func TestRenameCollisionWithExistingFileNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	taken := filepath.Join(root, "photo.jpg")
	source := filepath.Join(root, "IMG_0009.jpg")
	writeRenameFixture(t, taken, "do not lose me")
	writeRenameFixture(t, source, "new file bytes")

	files := []models.File{{Name: "IMG_0009.jpg", Path: source, Extension: "jpg", ModTime: time.Now()}}

	plan, _, err := buildRenamePlan(root, files, renameOptions{
		Template:        "photo.{ext}",
		DateSource:      "modified",
		DateGranularity: "year",
		CollisionPolicy: models.CollisionRename,
	})
	if err != nil {
		t.Fatalf("buildRenamePlan() error = %v", err)
	}
	if want := filepath.Join(root, "photo (1).jpg"); plan.Operations[0].Destination != want {
		t.Fatalf("destination = %q, want %q", plan.Operations[0].Destination, want)
	}

	if _, err := organizer.Execute(plan, nil, nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := readRenameFixture(t, taken); got != "do not lose me" {
		t.Errorf("existing file was overwritten: content = %q", got)
	}
	if got := readRenameFixture(t, filepath.Join(root, "photo (1).jpg")); got != "new file bytes" {
		t.Errorf("renamed content = %q, want the new file's bytes", got)
	}
}

// TestRenameSkipPolicyLeavesFileUntouched proves the skip policy is honored
// rather than silently renaming anyway.
func TestRenameSkipPolicyLeavesFileUntouched(t *testing.T) {
	root := t.TempDir()
	taken := filepath.Join(root, "photo.jpg")
	source := filepath.Join(root, "IMG_0009.jpg")
	writeRenameFixture(t, taken, "existing")
	writeRenameFixture(t, source, "new file bytes")

	files := []models.File{{Name: "IMG_0009.jpg", Path: source, Extension: "jpg", ModTime: time.Now()}}

	plan, _, err := buildRenamePlan(root, files, renameOptions{
		Template:        "photo.{ext}",
		DateSource:      "modified",
		DateGranularity: "year",
		CollisionPolicy: models.CollisionSkip,
	})
	if err != nil {
		t.Fatalf("buildRenamePlan() error = %v", err)
	}
	if len(plan.Operations) != 0 {
		t.Fatalf("expected 0 operations, got %d", len(plan.Operations))
	}
	if plan.SkippedFiles != 1 {
		t.Errorf("SkippedFiles = %d, want 1", plan.SkippedFiles)
	}
	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
}

// TestRenameSystemPathGuardRejects ensures the OS/system protection applies to
// rename exactly like it does to sort.
func TestRenameSystemPathGuardRejects(t *testing.T) {
	target := "/etc"
	if runtime.GOOS == "windows" {
		target = os.Getenv("WINDIR")
		if target == "" {
			target = os.Getenv("SystemRoot")
		}
		if target == "" {
			t.Skip("WINDIR and SystemRoot are not set; cannot locate a protected path")
		}
	}

	output, err := runRename(t, target, "--to", "{year}.{ext}", "--yes")
	if err == nil {
		t.Fatalf("expected the system path guard to reject %s\n%s", target, output)
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("error = %v, want an access denied message", err)
	}
	if !strings.Contains(err.Error(), "--allow-system") {
		t.Errorf("error should mention --allow-system, got %v", err)
	}
}

func TestRenameRejectsBadTemplates(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"missing template", []string{"--dry-run", "--yes"}, "--to is required"},
		{"path separator", []string{"--to", "sub/{year}.{ext}", "--dry-run", "--yes"}, "path separators are not allowed"},
		{"bad collision", []string{"--to", "{year}.{ext}", "--collision", "clobber", "--dry-run"}, "invalid collision policy"},
		{"bad date source", []string{"--to", "{year}.{ext}", "--date-source", "created", "--dry-run"}, "invalid date source"},
		{"bad granularity", []string{"--to", "{year}.{ext}", "--date-granularity", "week", "--dry-run"}, "invalid date granularity"},
		{"bad month format", []string{"--to", "{year}.{ext}", "--month-format", "roman", "--dry-run"}, "invalid month format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runRename(t, append([]string{t.TempDir()}, tt.args...)...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Execute() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestRenameNeverMovesBetweenDirectories is the hard safety invariant: every
// operation must stay in the source file's own folder.
func TestRenameNeverMovesBetweenDirectories(t *testing.T) {
	root := t.TempDir()
	top := filepath.Join(root, "top.jpg")
	nested := filepath.Join(root, "sub", "deep", "nested.jpg")
	writeRenameFixture(t, top, "top bytes")
	writeRenameFixture(t, nested, "nested bytes")

	files := []models.File{
		{Name: "top.jpg", Path: top, Extension: "jpg", ModTime: time.Now()},
		{Name: "nested.jpg", Path: nested, Extension: "jpg", ModTime: time.Now()},
	}

	plan, _, err := buildRenamePlan(root, files, renameOptions{
		Template:        "renamed.{ext}",
		DateSource:      "modified",
		DateGranularity: "year",
		CollisionPolicy: models.CollisionRename,
	})
	if err != nil {
		t.Fatalf("buildRenamePlan() error = %v", err)
	}
	if len(plan.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(plan.Operations))
	}
	if len(plan.FoldersToCreate) != 0 {
		t.Errorf("a rename must never create folders, got %v", plan.FoldersToCreate)
	}
	for _, op := range plan.Operations {
		if filepath.Dir(op.Source) != filepath.Dir(op.Destination) {
			t.Errorf("rename moved %s out of %s into %s", op.Source, filepath.Dir(op.Source), filepath.Dir(op.Destination))
		}
	}

	if _, err := organizer.Execute(plan, nil, nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "deep", "renamed.jpg")); err != nil {
		t.Errorf("nested file should have been renamed in place: %v", err)
	}
}

func TestExpandRenameTemplate(t *testing.T) {
	date := time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC)
	image := models.File{Name: "IMG_0001.JPG", Extension: "jpg"}
	extensionless := models.File{Name: "LICENSE"}

	tests := []struct {
		name        string
		template    string
		file        models.File
		granularity string
		monthFormat string
		want        string
	}{
		{"ext lowercased", "photo.{ext}", image, "year", "name", "photo.jpg"},
		{"ext from uppercase extension", "photo.{ext}", models.File{Name: "A.PNG", Extension: "PNG"}, "year", "name", "photo.png"},
		{"ext empty when missing", "photo.{ext}", extensionless, "year", "name", "photo"},
		{"date day numeric", "{date}.{ext}", image, "day", "number", "2023-01-15.jpg"},
		{"date day month name", "{date}.{ext}", image, "day", "name", "2023-Jan-15.jpg"},
		{"date month", "{date}.{ext}", image, "month", "number", "2023-01.jpg"},
		{"date year", "{date}.{ext}", image, "year", "number", "2023.jpg"},
		{"individual placeholders", "{year}{month}{day}.{ext}", image, "year", "number", "20230115.jpg"},
		{"literal text kept", "scan-{year}-{month}.{ext}", image, "year", "number", "scan-2023-01.jpg"},
		{"no placeholders", "static.txt", image, "day", "number", "static.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandRenameTemplate(tt.template, tt.file, date, tt.granularity, tt.monthFormat)
			if got != tt.want {
				t.Fatalf("expandRenameTemplate() = %q, want %q", got, tt.want)
			}
			if strings.ContainsAny(got, `/\`) {
				t.Fatalf("expanded name %q must not contain a path separator", got)
			}
		})
	}
}

// TestRenameRuleSelectionRenamesOnlyMatches covers the rule engine side of
// `chest rename`: rules decide which files are renamed, --to decides the name.
func TestRenameRuleSelectionRenamesOnlyMatches(t *testing.T) {
	root := t.TempDir()
	writeRenameFixture(t, filepath.Join(root, "IMG_0001.jpg"), "image bytes")
	writeRenameFixture(t, filepath.Join(root, "notes.txt"), "text bytes")

	output, err := runRename(t, root, "--to", "renamed.{ext}", "--rule", "ext=jpg -> Photos", "--dry-run", "--yes")
	if err != nil {
		t.Fatalf("rename error = %v", err)
	}
	if !strings.Contains(output, "IMG_0001.jpg") {
		t.Errorf("matched file missing from the preview:\n%s", output)
	}
	if strings.Contains(output, "notes.txt") {
		t.Errorf("unmatched file should not be renamed:\n%s", output)
	}
	if !strings.Contains(output, "1 files would be renamed") {
		t.Errorf("expected exactly one planned rename:\n%s", output)
	}
}
