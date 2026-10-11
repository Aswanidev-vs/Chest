package planner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/rules"
)

func writeSourceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create folder for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create %s: %v", path, err)
	}
}

// twoSameNamedFiles builds the plan.md §7 fixture: two files with the same
// name but different content in separate source folders.
func twoSameNamedFiles(t *testing.T, root, name string) []models.File {
	t.Helper()
	first := filepath.Join(root, "a", name)
	second := filepath.Join(root, "b", name)
	writeSourceFile(t, first, "first content")
	writeSourceFile(t, second, "second content")
	return []models.File{
		{Name: name, Path: first, ModTime: time.Now()},
		{Name: name, Path: second, ModTime: time.Now()},
	}
}

func TestPlanExpandsYearDestination(t *testing.T) {
	root := t.TempDir()
	engine := rules.NewEngine([]models.Rule{{
		Name:        "Year",
		Priority:    20,
		Destination: "{year}",
	}})
	files := []models.File{
		{Name: "old.jpg", Path: filepath.Join(root, "old.jpg"), ModTime: time.Date(2023, 8, 15, 0, 0, 0, 0, time.UTC)},
		{Name: "last-year.jpg", Path: filepath.Join(root, "last-year.jpg"), ModTime: time.Date(2025, 5, 10, 0, 0, 0, 0, time.UTC)},
		{Name: "this-year.jpg", Path: filepath.Join(root, "this-year.jpg"), ModTime: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	}

	plan, err := New(engine, "", models.CollisionSkip).Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Operations) != len(files) {
		t.Fatalf("expected %d operations, got %d", len(files), len(plan.Operations))
	}

	wantDestinations := map[string]string{
		"old.jpg":       filepath.Join(root, "2023", "old.jpg"),
		"last-year.jpg": filepath.Join(root, "2025", "last-year.jpg"),
		"this-year.jpg": filepath.Join(root, "2026", "this-year.jpg"),
	}
	for _, operation := range plan.Operations {
		want, ok := wantDestinations[filepath.Base(operation.Source)]
		if !ok {
			t.Fatalf("unexpected source %q", operation.Source)
		}
		if operation.Destination != want {
			t.Errorf("destination for %q = %q, want %q", filepath.Base(operation.Source), operation.Destination, want)
		}
	}
}

func TestPlanExpandsDatePlaceholdersFromTakenDate(t *testing.T) {
	root := t.TempDir()
	taken := time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)
	modified := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	engine := rules.NewEngine([]models.Rule{{
		Name:        "Date",
		Priority:    20,
		Destination: "{date}",
	}})
	file := models.File{
		Name:      "photo.jpg",
		Path:      filepath.Join(root, "photo.jpg"),
		ModTime:   modified,
		TakenDate: &taken,
	}

	plan, err := New(engine, "", models.CollisionSkip, WithDate("auto", "day", "name")).Plan(root, []models.File{file})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(plan.Operations))
	}
	want := filepath.Join(root, "2024", "Mar", "04", "photo.jpg")
	if plan.Operations[0].Destination != want {
		t.Fatalf("Destination = %q, want %q", plan.Operations[0].Destination, want)
	}
}

func TestPlanTakenDateSourceSkipsMissingMetadata(t *testing.T) {
	root := t.TempDir()
	engine := rules.NewEngine([]models.Rule{{
		Name:        "Date",
		Priority:    20,
		Destination: "{date}",
	}})
	file := models.File{Name: "photo.jpg", Path: filepath.Join(root, "photo.jpg"), ModTime: time.Now()}

	plan, err := New(engine, "", models.CollisionSkip, WithDate("taken", "month", "name")).Plan(root, []models.File{file})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Operations) != 0 {
		t.Fatalf("expected no operations, got %d", len(plan.Operations))
	}
	if plan.SkippedFiles != 1 {
		t.Fatalf("SkippedFiles = %d, want 1", plan.SkippedFiles)
	}
}

func TestPlanMonthGranularityUsesMonthNames(t *testing.T) {
	root := t.TempDir()
	engine := rules.NewEngine([]models.Rule{{
		Name:        "Date",
		Priority:    20,
		Destination: "{date}",
	}})
	file := models.File{
		Name:    "IMG_0001.jpg",
		Path:    filepath.Join(root, "IMG_0001.jpg"),
		ModTime: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
	}

	plan, err := New(engine, "", models.CollisionSkip, WithDate("modified", "month", "")).Plan(root, []models.File{file})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	want := filepath.Join(root, "2026", "Aug", "IMG_0001.jpg")
	if len(plan.Operations) != 1 || plan.Operations[0].Destination != want {
		t.Fatalf("Destination = %q, want %q", plan.Operations[0].Destination, want)
	}
}

func TestExpandDatePlaceholdersMonthFormat(t *testing.T) {
	date := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		granularity string
		monthFormat string
		destination string
		want        string
	}{
		{"month name when format empty", "month", "", "{date}", "2026/Aug"},
		{"month name", "month", "name", "{date}", "2026/Aug"},
		{"month number", "month", "number", "{date}", "2026/08"},
		{"day name", "day", "name", "{date}", "2026/Aug/09"},
		{"day number", "day", "number", "{date}", "2026/08/09"},
		{"year unchanged by month format", "year", "number", "{date}", "2026"},
		{"month placeholder name", "year", "name", "{year}/{month}/{day}", "2026/Aug/09"},
		{"month placeholder number", "year", "number", "{year}/{month}/{day}", "2026/08/09"},
		{"unknown format falls back to name", "month", "roman", "{date}", "2026/Aug"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandDatePlaceholders(tt.destination, date, tt.granularity, tt.monthFormat); got != tt.want {
				t.Fatalf("expandDatePlaceholders() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlanIntraPlanCollisionAutoRenamesSecondFile(t *testing.T) {
	root := t.TempDir()
	files := twoSameNamedFiles(t, root, "photo.jpg")

	engine := rules.NewEngine([]models.Rule{{Name: "Photos", Priority: 20, Destination: "Images"}})
	plan, err := New(engine, "", "").Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

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

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	conflict := plan.Conflicts[0]
	if conflict.Target != secondDest {
		t.Errorf("conflict target = %q, want %q", conflict.Target, secondDest)
	}
	wantSources := []string{files[0].Path, files[1].Path}
	if !reflect.DeepEqual(conflict.Sources, wantSources) {
		t.Errorf("conflict sources = %v, want %v", conflict.Sources, wantSources)
	}
	if want := "auto-renamed second file to photo (1).jpg"; conflict.Resolution != want {
		t.Errorf("conflict resolution = %q, want %q", conflict.Resolution, want)
	}
}

func TestPlanExplicitSkipLeavesSecondFileInPlace(t *testing.T) {
	root := t.TempDir()
	files := twoSameNamedFiles(t, root, "photo.jpg")

	engine := rules.NewEngine([]models.Rule{{Name: "Photos", Priority: 20, Destination: "Images"}})
	plan, err := New(engine, "", models.CollisionSkip).Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if len(plan.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(plan.Operations))
	}
	if want := filepath.Join(root, "Images", "photo.jpg"); plan.Operations[0].Destination != want {
		t.Errorf("destination = %q, want %q", plan.Operations[0].Destination, want)
	}
	if plan.SkippedFiles != 1 {
		t.Errorf("SkippedFiles = %d, want 1", plan.SkippedFiles)
	}

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	if want := "second file left in place under skip policy"; plan.Conflicts[0].Resolution != want {
		t.Errorf("conflict resolution = %q, want %q", plan.Conflicts[0].Resolution, want)
	}
}

func TestPlanCaseVariantCollisionDetectedWhenPlatformFoldsCase(t *testing.T) {
	root := t.TempDir()
	upper := filepath.Join(root, "a", "Photo.jpg")
	lower := filepath.Join(root, "b", "photo.jpg")
	writeSourceFile(t, upper, "upper")
	writeSourceFile(t, lower, "lower")

	engine := rules.NewEngine([]models.Rule{{Name: "Photos", Priority: 20, Destination: "Images"}})
	files := []models.File{
		{Name: "Photo.jpg", Path: upper, ModTime: time.Now()},
		{Name: "photo.jpg", Path: lower, ModTime: time.Now()},
	}

	plan, err := New(engine, "", "").Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// On any platform no two operations may target the same normalized
	// destination, otherwise the second move still overwrites the first.
	seen := make(map[string]string)
	for _, op := range plan.Operations {
		key := filesystem.CaseKey(op.Destination)
		if prev, ok := seen[key]; ok {
			t.Fatalf("operations from %q and %q share normalized destination %q", prev, op.Source, key)
		}
		seen[key] = op.Source
	}

	foldsCase := filesystem.CaseKey("Photo.jpg") != "Photo.jpg"
	if foldsCase && len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 recorded conflict on a case-insensitive filesystem, got %d", len(plan.Conflicts))
	}
	if !foldsCase && len(plan.Conflicts) != 0 {
		t.Fatalf("expected no conflict on a case-sensitive filesystem, got %d", len(plan.Conflicts))
	}
}

func TestCaseKeyNormalizesCaseOnlyOnCaseInsensitivePlatforms(t *testing.T) {
	upper := filesystem.CaseKey("Images/PHOTO.JPG")
	lower := filesystem.CaseKey("images/photo.jpg")
	if filesystem.CaseKey("Photo.jpg") != "photo.jpg" {
		if upper == lower {
			t.Fatalf("CaseKey folded case on a case-sensitive platform: %q == %q", upper, lower)
		}
		return
	}
	if upper != lower {
		t.Fatalf("CaseKey did not fold case on a case-insensitive platform: %q != %q", upper, lower)
	}
}

func TestPlanReplacePolicyRecordsIntraPlanConflict(t *testing.T) {
	root := t.TempDir()
	files := twoSameNamedFiles(t, root, "photo.jpg")

	engine := rules.NewEngine([]models.Rule{{Name: "Photos", Priority: 20, Destination: "Images"}})
	plan, err := New(engine, "", models.CollisionReplace).Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if len(plan.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(plan.Operations))
	}
	want := filepath.Join(root, "Images", "photo.jpg")
	for i, op := range plan.Operations {
		if op.Destination != want {
			t.Errorf("operation %d destination = %q, want %q", i, op.Destination, want)
		}
	}
	if !plan.Operations[1].IsCollision {
		t.Error("second operation should be marked as a collision")
	}

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	if want := "overwrite under replace policy"; plan.Conflicts[0].Resolution != want {
		t.Errorf("conflict resolution = %q, want %q", plan.Conflicts[0].Resolution, want)
	}
}

func TestPlanAbortPolicyErrorsOnIntraPlanCollision(t *testing.T) {
	root := t.TempDir()
	files := twoSameNamedFiles(t, root, "photo.jpg")

	engine := rules.NewEngine([]models.Rule{{Name: "Photos", Priority: 20, Destination: "Images"}})
	_, err := New(engine, "", models.CollisionAbort).Plan(root, files)
	if err == nil {
		t.Fatal("expected an error from the abort policy, got nil")
	}
}

func TestPlanRenamePolicyResolvesAgainstDiskAndPlanClaims(t *testing.T) {
	root := t.TempDir()
	// A photo.jpg already lives at the destination, so the in-plan resolution
	// must walk past the disk file and the earlier claim.
	writeSourceFile(t, filepath.Join(root, "Images", "photo.jpg"), "existing")
	files := twoSameNamedFiles(t, root, "photo.jpg")

	engine := rules.NewEngine([]models.Rule{{Name: "Photos", Priority: 20, Destination: "Images"}})
	plan, err := New(engine, "", models.CollisionRename).Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if len(plan.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(plan.Operations))
	}
	wantFirst := filepath.Join(root, "Images", "photo (1).jpg")
	wantSecond := filepath.Join(root, "Images", "photo (2).jpg")
	if plan.Operations[0].Destination != wantFirst {
		t.Errorf("first destination = %q, want %q", plan.Operations[0].Destination, wantFirst)
	}
	if plan.Operations[1].Destination != wantSecond {
		t.Errorf("second destination = %q, want %q", plan.Operations[1].Destination, wantSecond)
	}
	if !plan.Operations[0].IsCollision || !plan.Operations[1].IsCollision {
		t.Error("both operations collide with the on-disk file")
	}

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	if want := "auto-renamed second file to photo (2).jpg"; plan.Conflicts[0].Resolution != want {
		t.Errorf("conflict resolution = %q, want %q", plan.Conflicts[0].Resolution, want)
	}
	if want := files[0].Path; !reflect.DeepEqual(plan.Conflicts[0].Sources, []string{want, files[1].Path}) {
		t.Errorf("conflict sources = %v, want [%s %s]", plan.Conflicts[0].Sources, want, files[1].Path)
	}
}

func TestPlanOnDiskCollisionUnderDefaultPolicySkips(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, filepath.Join(root, "Images", "photo.jpg"), "existing")
	files := twoSameNamedFiles(t, root, "photo.jpg")

	engine := rules.NewEngine([]models.Rule{{Name: "Photos", Priority: 20, Destination: "Images"}})
	plan, err := New(engine, "", "").Plan(root, files)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if len(plan.Operations) != 0 {
		t.Fatalf("expected no operations, got %d", len(plan.Operations))
	}
	if plan.SkippedFiles != 2 {
		t.Fatalf("SkippedFiles = %d, want 2", plan.SkippedFiles)
	}
	if len(plan.Conflicts) != 0 {
		t.Errorf("on-disk-only collisions should not be recorded as plan conflicts, got %d", len(plan.Conflicts))
	}
}

func TestPlanDateSourceFilenameRoutesDestination(t *testing.T) {
	root := t.TempDir()
	file := models.File{
		Name:    "report_20230115.pdf",
		Path:    filepath.Join(root, "report_20230115.pdf"),
		ModTime: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	engine := rules.NewEngine([]models.Rule{{Name: "Reports", Priority: 20, Destination: "{year}/{month}"}})

	plan, err := New(engine, "", "", WithDate("filename", "month", "name")).Plan(root, []models.File{file})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	want := filepath.Join(root, "2023", "Jan", "report_20230115.pdf")
	if len(plan.Operations) != 1 || plan.Operations[0].Destination != want {
		t.Fatalf("destination = %v, want %q", plan.Operations, want)
	}
}

func TestPlanDateSourceFilenameFallsBackToModTime(t *testing.T) {
	root := t.TempDir()
	file := models.File{
		Name:    "notes.pdf",
		Path:    filepath.Join(root, "notes.pdf"),
		ModTime: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	engine := rules.NewEngine([]models.Rule{{Name: "Reports", Priority: 20, Destination: "{year}/{month}"}})

	plan, err := New(engine, "", "", WithDate("filename", "month", "name")).Plan(root, []models.File{file})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	want := filepath.Join(root, "2026", "Jan", "notes.pdf")
	if len(plan.Operations) != 1 || plan.Operations[0].Destination != want {
		t.Fatalf("destination = %v, want %q", plan.Operations, want)
	}
}
