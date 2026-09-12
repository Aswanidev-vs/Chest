package scanner

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestScanTestingFolderExtractDates scans the repository's real testing/
// fixtures with date extraction enabled and asserts signature-based format
// detection and metadata enrichment end-to-end.
func TestScanTestingFolderExtractDates(t *testing.T) {
	goleak.VerifyNone(t)

	root, err := filepath.Abs(filepath.Join("..", "..", "testing"))
	require.NoError(t, err)
	require.DirExists(t, root)

	s := New(ScanOptions{
		ExtractDates: true,
	})
	files, err := s.Scan(root)
	require.NoError(t, err)
	require.NotEmpty(t, files)

	byName := make(map[string]struct{}, len(files))
	for _, f := range files {
		byName[f.Name] = struct{}{}
	}

	tests := []struct {
		name     string
		format   string
		mime     string
		category string
	}{
		{"RHCSA SA1 EVENT.pdf", "PDF", "application/pdf", "Document"},
		{"audio (3).wav", "WAV", "audio/wav", "Audio"},
		{"Predator-1.3.15-windows-setup.exe", "PE", "application/vnd.microsoft.portable-executable", "Executable"},
		{"anilist%20about.zip", "ZIP", "application/zip", "Archive"},
		{"177610779053720555.jpg", "JPEG", "image/jpeg", "Image"},
		{"insta.gocut", "GoCut", "application/x-gocut-project", "Video"},
		{"test.gocut", "GoCut", "application/x-gocut-project", "Video"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Contains(t, byName, tt.name, "fixture should be scanned")

			for _, f := range files {
				if f.Name != tt.name {
					continue
				}
				assert.Equal(t, tt.format, f.Format)
				assert.Equal(t, tt.mime, f.MIMEType)
				assert.Equal(t, tt.category, f.Category)
				// PDF fixture embeds a modification date / producer.
				if tt.format == "PDF" {
					assert.NotEmpty(t, f.Metadata["modification_date"])
					assert.NotEmpty(t, f.Metadata["producer"])
				}
				return
			}
			t.Fatalf("file %q not found in scan results", tt.name)
		})
	}
}

// TestScanTestingFolderNoDateExtraction ensures TakenDate stays nil when
// ExtractDates is disabled (default behavior).
func TestScanTestingFolderNoDateExtraction(t *testing.T) {
	goleak.VerifyNone(t)

	root, err := filepath.Abs(filepath.Join("..", "..", "testing"))
	require.NoError(t, err)

	files, err := New(ScanOptions{}).Scan(root)
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, f := range files {
		assert.Nil(t, f.TakenDate, "TakenDate should be nil without ExtractDates")
		assert.Empty(t, f.TakenDateSource)
	}
}
