package metadata

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractRealGoCutFixture exercises the .gocut extractor against the real
// project file in testing/.
func TestExtractRealGoCutFixture(t *testing.T) {
	path := filepath.Join("..", "..", "testing", "insta.gocut")
	require.FileExists(t, path)

	res, err := Extract(path, "gocut")
	require.NoError(t, err)

	assert.Equal(t, "GoCut", res.Format)
	assert.Equal(t, "application/x-gocut-project", res.MIMEType)
	assert.Equal(t, "Video", res.Category)

	assert.Equal(t, "insta", res.Fields["project_name"])
	assert.Equal(t, "16:9", res.Fields["aspect_ratio"])
	assert.Equal(t, "1920x1080", res.Fields["resolution"])
	assert.NotEmpty(t, res.Fields["project_id"])

	// Embedded createdAt: 2026-09-01T14:45:48.7517315+05:30.
	assert.Equal(t, "gocut", res.Date.Source)
	assert.Equal(t, 2026, res.Date.Date.Year())
	assert.Equal(t, time.September, res.Date.Date.Month())
	assert.Equal(t, 1, res.Date.Date.Day())
	assert.Equal(t, 14, res.Date.Date.Hour())
}

// TestExtractEmptyGoCutFixture verifies a zero-byte .gocut file still reports
// the GoCut format (via extension) without fields or dates.
func TestExtractEmptyGoCutFixture(t *testing.T) {
	path := filepath.Join("..", "..", "testing", "test.gocut")
	require.FileExists(t, path)

	res, err := Extract(path, "gocut")
	require.NoError(t, err)
	assert.Equal(t, "GoCut", res.Format)
	assert.Empty(t, res.Fields)
	assert.True(t, res.Date.Date.IsZero())
}

// TestGoCutDetectedBySignatureNotExtension proves a renamed .gocut project is
// still detected by its content signature.
func TestGoCutDetectedBySignatureNotExtension(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testing", "insta.gocut"))
	require.NoError(t, err)

	renamed := filepath.Join(t.TempDir(), "video.txt")
	require.NoError(t, os.WriteFile(renamed, raw, 0o644))

	res, err := Extract(renamed, "txt")
	require.NoError(t, err)
	assert.Equal(t, "GoCut", res.Format, "renamed .gocut must be detected by magic signature")
	assert.Equal(t, "Video", res.Category)
	assert.Equal(t, "insta", res.Fields["project_name"])
}

// TestGoCutRegisteredAheadOfBuiltins documents that custom-format extraction
// goes through the same registry: gocut must win over the generic text
// detector for a text-looking project file.
func TestGoCutRegisteredAheadOfBuiltins(t *testing.T) {
	sample := Sample{
		Extension: "gocut",
		Head:      []byte(`{"id":"x","name":"proj"}`),
	}
	assert.True(t, gocutMatch(sample), "gocut extractor must match its own format")
	assert.False(t, gocutMatch(Sample{Extension: "txt", Head: []byte("plain notes")}),
		"gocut extractor must not claim plain text")
}
