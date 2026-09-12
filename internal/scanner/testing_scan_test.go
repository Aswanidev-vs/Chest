package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestScanTestingFolderExtractDates scans the repository's real testing/
// fixtures with date extraction enabled and asserts signature-based format
// detection and metadata enrichment end-to-end.
// TestScanEnrichFunc verifies that the external enrichment hook runs last
// for every scanned file and can fill gaps the built-in extractors left,
// e.g. an XMP sidecar's embedded capture date.
func TestScanEnrichFunc(t *testing.T) {
	goleak.VerifyNone(t)

	dir := t.TempDir()
	sidecar := filepath.Join(dir, "IMG_0001.xmp")
	xmp := `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about="" xmlns:photoshop="http://ns.adobe.com/photoshop/1.0/">
   <photoshop:DateCreated>2021-11-17T09:31:07</photoshop:DateCreated>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>
`
	require.NoError(t, os.WriteFile(sidecar, []byte(xmp), 0644))

	wantDate := time.Date(2021, 11, 17, 9, 31, 7, 0, time.UTC)
	s := New(ScanOptions{
		EnrichFunc: func(file models.File) (models.File, bool) {
			if !strings.EqualFold(file.Extension, "xmp") {
				return file, false
			}
			data, err := os.ReadFile(file.Path)
			if err != nil {
				return file, false
			}
			idx := strings.Index(string(data), "photoshop:DateCreated>")
			if idx < 0 {
				return file, false
			}
			raw := string(data)[idx+len("photoshop:DateCreated>"):]
			end := strings.Index(raw, "<")
			if end < 0 {
				return file, false
			}
			ts, err := time.Parse("2006-01-02T15:04:05", raw[:end])
			if err != nil {
				return file, false
			}
			updated := file
			updated.TakenDate = &ts
			updated.TakenDateSource = "xmp:test"
			updated.Format = "XMP"
			return updated, true
		},
	})

	files, err := s.Scan(dir)
	require.NoError(t, err)
	require.Len(t, files, 1)

	f := files[0]
	assert.Equal(t, "XMP", f.Format, "plugin format should be adopted")
	assert.Equal(t, "xmp:test", f.TakenDateSource)
	require.NotNil(t, f.TakenDate)
	assert.True(t, f.TakenDate.Equal(wantDate), "TakenDate = %v, want %v", f.TakenDate, wantDate)
}

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
