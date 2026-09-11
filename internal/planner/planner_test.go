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
