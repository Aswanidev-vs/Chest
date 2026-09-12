package rules

import (
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/models"
)

func TestParseRule(t *testing.T) {
	rule, err := ParseRule("type=video && size>1GB -> Videos/Large", 100)
	if err != nil {
		t.Fatalf("unexpected error parsing rule: %v", err)
	}

	if rule.Destination != "Videos/Large" {
		t.Errorf("expected destination Videos/Large, got %s", rule.Destination)
	}
	if len(rule.Conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(rule.Conditions))
	}
	if rule.Conditions[0].Field != models.FieldType || rule.Conditions[0].Value != "video" {
		t.Errorf("condition 0 mismatch: %+v", rule.Conditions[0])
	}
}

func TestEngineEvaluate(t *testing.T) {
	r1, _ := ParseRule("*.mkv -> Videos", 10)
	r2, _ := ParseRule("type=video && size>1GB -> Videos/Large", 50)

	engine := NewEngine([]models.Rule{r1, r2})

	// Large mkv video file
	largeFile := models.File{
		Name:      "movie.mkv",
		Extension: "mkv",
		Category:  "video",
		Size:      2 * 1024 * 1024 * 1024, // 2GB
		ModTime:   time.Now(),
	}

	rule, _, matched := engine.Evaluate(largeFile)
	if !matched {
		t.Fatalf("expected match for largeFile")
	}
	if rule.Destination != "Videos/Large" {
		t.Errorf("expected destination Videos/Large due to priority, got %s", rule.Destination)
	}

	// Small mkv video file
	smallFile := models.File{
		Name:      "clip.mkv",
		Extension: "mkv",
		Category:  "video",
		Size:      10 * 1024 * 1024, // 10MB
		ModTime:   time.Now(),
	}

	rule, _, matched = engine.Evaluate(smallFile)
	if !matched {
		t.Fatalf("expected match for smallFile")
	}
	if rule.Destination != "Videos" {
		t.Errorf("expected destination Videos for smallFile, got %s", rule.Destination)
	}
}

func TestDateConditionsUseModificationTime(t *testing.T) {
	rule, err := ParseRule("date=2024 -> Old", 10)
	if err != nil {
		t.Fatalf("ParseRule() error = %v", err)
	}
	file := models.File{ModTime: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)}

	matched, reason := matchRule(rule, file)
	if !matched {
		t.Fatalf("expected match, got reason %q", reason)
	}
}

func TestTakenDateCondition(t *testing.T) {
	rule, err := ParseRule("taken_date=2025-02 -> Photos", 10)
	if err != nil {
		t.Fatalf("ParseRule() error = %v", err)
	}
	taken := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
	file := models.File{TakenDate: &taken}

	matched, reason := matchRule(rule, file)
	if !matched {
		t.Fatalf("expected match, got reason %q", reason)
	}

	file.TakenDate = nil
	matched, _ = matchRule(rule, file)
	if matched {
		t.Fatal("expected missing taken date not to match")
	}
}
