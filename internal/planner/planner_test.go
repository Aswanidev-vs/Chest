package planner

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/rules"
)

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

	plan, err := New(engine, "", models.CollisionSkip, WithDate("auto", "day")).Plan(root, []models.File{file})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(plan.Operations))
	}
	want := filepath.Join(root, "2024", "03", "04", "photo.jpg")
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

	plan, err := New(engine, "", models.CollisionSkip, WithDate("taken", "month")).Plan(root, []models.File{file})
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
