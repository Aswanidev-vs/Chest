package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// runSortDryRun executes the sort command in dry-run mode against the repo's
// testing/ fixtures and captures stdout (the planner prints via fmt.Println)
// without touching the filesystem.
func runSortDryRun(t *testing.T, args ...string) (string, error) {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", "..", "testing"))
	require.NoError(t, err)

	full := append([]string{root, "--dry-run", "--yes"}, args...)

	// Capture os.Stdout since the planner writes with fmt.Println.
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	cmd := newSortCmd()
	cmd.SetArgs(full)
	execErr := cmd.Execute()

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), execErr
}

// TestSortFormatFlagDryRun verifies -f/--format groups files by their
// content-detected format in a dry run over the real testing/ folder.
func TestSortFormatFlagDryRun(t *testing.T) {
	output, err := runSortDryRun(t, "--format")
	require.NoError(t, err)

	// --format groups by real extension per its help text (MP4/, MP3/, PNG/).
	assert.Contains(t, output, "JPG", "JPEG fixtures should be grouped under JPG")
	assert.Contains(t, output, "PDF", "PDF fixture should be grouped under PDF")
	assert.Contains(t, output, "MP4", "MP4 fixtures should be grouped under MP4")
	assert.Contains(t, output, "16 files would be moved", "all 16 fixtures should be planned")
}

// TestSortRuleFormatConditionDryRun verifies a custom `format=` rule routes a
// file by detected format during a dry run.
func TestSortRuleFormatConditionDryRun(t *testing.T) {
	output, err := runSortDryRun(t, "--rule", "format=pdf -> MyPdfs")
	require.NoError(t, err)
	assert.Contains(t, output, "MyPdfs", "format=pdf rule should route the PDF fixture to MyPdfs")
}

// TestSortRuleExtensionConditionDryRun verifies extension= stays
// extension-only: extension=pdf must not match anything here (fixtures keep
// their real extensions; the pdf fixture already ends in .pdf so it SHOULD
// match — assert routing to the custom folder).
func TestSortRuleExtensionConditionDryRun(t *testing.T) {
	output, err := runSortDryRun(t, "--rule", "extension=pdf -> PdfByExt")
	require.NoError(t, err)
	assert.Contains(t, output, "PdfByExt", "extension=pdf should match the .pdf fixture")
}

// TestSortDateSourceAutoDryRun verifies --date --date-source auto with month
// granularity parses flags and plans a dry run without error.
func TestSortDateSourceAutoDryRun(t *testing.T) {
	_, err := runSortDryRun(t, "--date", "--date-source", "auto", "--date-granularity", "month")
	require.NoError(t, err)
}

// TestSortDateSourceTakenDryRun verifies --date-source taken is accepted.
func TestSortDateSourceTakenDryRun(t *testing.T) {
	_, err := runSortDryRun(t, "--date", "--date-source", "taken", "--date-granularity", "day")
	require.NoError(t, err)
}

// TestSortDateInvalidFlags ensures invalid flag combos fail with the right
// error (regression guard for flag validation).
func TestSortDateInvalidFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"bad source", []string{"--date", "--date-source", "created"}, "invalid date source"},
		{"bad granularity", []string{"--date", "--date-granularity", "week"}, "invalid date granularity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runSortDryRun(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
