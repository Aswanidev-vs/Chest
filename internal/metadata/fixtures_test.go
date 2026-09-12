package metadata

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// regexpDate matches PDF D: timestamps like D:20260815043640Z.
var regexpDate = regexp.MustCompile(`^D:\d{14}`)

// TestMain guards the whole metadata test binary against goroutine leaks.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestExtractRealFixtures runs the extractor against the real files in the
// repository's testing/ folder and asserts signature-based format detection.
func TestExtractRealFixtures(t *testing.T) {
	fixtures := []struct {
		file     string
		format   string
		mime     string
		category string
	}{
		{"177610779053720555.jpg", "JPEG", "image/jpeg", "Image"},
		{"download (6).jpg", "JPEG", "image/jpeg", "Image"},
		{"Relife Wallpaper.jpg", "JPEG", "image/jpeg", "Image"},
		{"Testarossa.jpg", "JPEG", "image/jpeg", "Image"},
		{"RHCSA SA1 EVENT.pdf", "PDF", "application/pdf", "Document"},
		{"Predator-1.3.15-windows-setup.exe", "PE", "application/vnd.microsoft.portable-executable", "Executable"},
		{"anilist%20about.zip", "ZIP", "application/zip", "Archive"},
		{"audio (3).wav", "WAV", "audio/wav", "Audio"},
		{"BLEACH： Thousand-Year Blood War Cour 4 Ending FULL - ＂Rasen Acoustic ver.＂ by 9Lana (Lyrics) [OC7h4xbaTYA].mp3", "MP3", "audio/mpeg", "Audio"},
		{"TVアニメ『BLEACH 千年血戦篇-禍進譚-』#45「DEFEND YOU」ノンクレジットエンディングムービー [tMUTO_gtdkc] (1920x1080).mp4", "QuickTime", "video/quicktime", "Video"},
		{"TVアニメ『BLEACH 千年血戦篇-禍進譚-』メインPV｜2026.07 ON AIR [g2iWIWqJ39s] (1920x1080).mp4", "QuickTime", "video/quicktime", "Video"},
		{"insta.gocut", "GoCut", "application/x-gocut-project", "Video"},
		{"test.gocut", "GoCut", "application/x-gocut-project", "Video"},
	}

	for _, f := range fixtures {
		t.Run(f.format+"/"+f.file, func(t *testing.T) {
			path := filepath.Join("..", "..", "testing", f.file)
			require.FileExists(t, path, "fixture missing from testing/ folder")

			res, err := Extract(path, "")
			require.NoError(t, err)
			assert.Equal(t, f.format, res.Format)
			assert.Equal(t, f.mime, res.MIMEType)
			assert.Equal(t, f.category, res.Category)
		})
	}
}

// TestExtractRealFixturePDFFields verifies the PDF Info dictionary extraction
// against the real PDF fixture.
func TestExtractRealFixturePDFFields(t *testing.T) {
	path := filepath.Join("..", "..", "testing", "RHCSA SA1 EVENT.pdf")
	res, err := Extract(path, "pdf")
	require.NoError(t, err)
	assert.Equal(t, "PDF", res.Format)

	assert.NotEmpty(t, res.Fields["modification_date"], "PDF fixture should expose modification_date field")
	assert.NotEmpty(t, res.Fields["producer"], "PDF fixture should expose producer field")
	assert.True(t, regexpDate.MatchString(res.Fields["modification_date"]),
		"modification_date %q should be a D:YYYYMMDDHHMMSS timestamp", res.Fields["modification_date"])
}

// TestDetectFormatOverridesExtension simulates a renamed PDF (wrong extension)
// to prove magic bytes win over extension fallback.
func TestDetectFormatOverridesExtension(t *testing.T) {
	path := filepath.Join("..", "..", "testing", "RHCSA SA1 EVENT.pdf")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	renamed := filepath.Join(t.TempDir(), "invoice.txt")
	require.NoError(t, os.WriteFile(renamed, data, 0o644))

	res, err := Extract(renamed, "txt")
	require.NoError(t, err)
	assert.Equal(t, "PDF", res.Format, "renamed PDF must still be detected by signature")
	assert.Equal(t, "application/pdf", res.MIMEType)
}
