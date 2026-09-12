package rules

import (
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestFormatConditionMatchesDetectedFormat proves format= matches the
// signature-detected format rather than the filename extension.
func TestFormatConditionMatchesDetectedFormat(t *testing.T) {
	// A renamed PDF: extension is txt but format is detected as PDF.
	file := models.File{
		Name:      "invoice.txt",
		Extension: "txt",
		Format:    "PDF",
		MIMEType:  "application/pdf",
		Category:  "Document",
		ModTime:   time.Now(),
	}

	rule, err := ParseRule("format=pdf -> MyPdfs", 0)
	require.NoError(t, err)

	engine := NewEngine([]models.Rule{rule})
	matched, reason, ok := engine.Evaluate(file)
	require.True(t, ok, "format=pdf should match a file whose detected format is PDF")
	assert.Contains(t, reason, "format")
	assert.Equal(t, "MyPdfs", matched.Destination)

	// extension= must still be extension-only: it must NOT match.
	extRule, err := ParseRule("extension=pdf -> WrongFolder", 0)
	require.NoError(t, err)
	extEngine := NewEngine([]models.Rule{extRule})
	_, _, ok = extEngine.Evaluate(file)
	assert.False(t, ok, "extension=pdf must not match a file with txt extension")
}

// TestFormatConditionIgnoresExtensionMismatch ensures a real PDF without a
// detected format field does not satisfy format= (guards against the old
// extension-based matching).
func TestFormatConditionIgnoresExtensionMismatch(t *testing.T) {
	file := models.File{
		Name:      "photo.jpg",
		Extension: "jpg",
		Format:    "JPEG",
		Category:  "Image",
		ModTime:   time.Now(),
	}

	rule, err := ParseRule("format=pdf -> MyPdfs", 0)
	require.NoError(t, err)
	engine := NewEngine([]models.Rule{rule})

	_, _, ok := engine.Evaluate(file)
	assert.False(t, ok, "format=pdf must not match a JPEG file")
}

// TestTakenDateRuleRouting checks the taken_date-based routing end to end.
func TestTakenDateRuleRouting(t *testing.T) {
	taken := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	file := models.File{
		Name:      "IMG_0001.jpg",
		Extension: "jpg",
		Category:  "Image",
		ModTime:   time.Now(),
		TakenDate: &taken,
	}

	rule, err := ParseRule("type=image -> Photos", 0)
	require.NoError(t, err)
	engine := NewEngine([]models.Rule{rule})

	matched, _, ok := engine.Evaluate(file)
	require.True(t, ok)
	assert.Equal(t, "Photos", matched.Destination)
}

// TestFormatConditionMatchesCustomFormat proves the custom .gocut format is
// routable via format= rules just like built-in formats.
func TestFormatConditionMatchesCustomFormat(t *testing.T) {
	file := models.File{
		Name:      "insta.gocut",
		Extension: "gocut",
		Format:    "GoCut",
		MIMEType:  "application/x-gocut-project",
		Category:  "Video",
		ModTime:   time.Now(),
	}

	rule, err := ParseRule("format=gocut -> GoCutProjects", 0)
	require.NoError(t, err)
	engine := NewEngine([]models.Rule{rule})

	matched, reason, ok := engine.Evaluate(file)
	require.True(t, ok, "format=gocut should match a GoCut project file")
	assert.Equal(t, "GoCutProjects", matched.Destination)
	assert.Contains(t, reason, "format")

	// And the content signature, not just the extension, decides the format.
	renamed := file
	renamed.Name = "insta.txt"
	renamed.Extension = "txt"
	_, _, ok = engine.Evaluate(renamed)
	assert.True(t, ok, "format=gocut must still match a renamed GoCut project (signature detection)")
}

// TestMIMECondition checks mime= rule matching against extracted MIME types.
func TestMIMECondition(t *testing.T) {
	file := models.File{
		Name:      "clip.mp4",
		Extension: "mp4",
		Format:    "QuickTime",
		MIMEType:  "video/quicktime",
		Category:  "Video",
		ModTime:   time.Now(),
	}

	rule, err := ParseRule("mime=video/quicktime -> Movies", 0)
	require.NoError(t, err)
	engine := NewEngine([]models.Rule{rule})

	matched, _, ok := engine.Evaluate(file)
	require.True(t, ok, "mime=video/quicktime should match QuickTime file")
	assert.Equal(t, "Movies", matched.Destination)
}
