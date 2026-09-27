package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// TestReplPreviewLargePlanDoesNotDeadlock covers the pipe-buffer hazard in
// captureReplStdout: a plan far larger than the OS pipe buffer must still
// complete instead of blocking printDryRun forever.
func TestReplPreviewLargePlanDoesNotDeadlock(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 200; i++ {
		writeReplFixture(t, dir, fmt.Sprintf("clip%03d.mp4", i), "fake mp4 bytes")
	}

	s := newReplTestSession(t, dir)
	var out bytes.Buffer

	dryRun := captureReplStdout(t, func() {
		s.run(strings.NewReader("add type=Video -> Videos\npreview\nquit\n"), &out)
	})

	if !strings.Contains(dryRun, "200 files would be moved") {
		t.Fatalf("expected all 200 files in the dry run, got:\n%s", dryRun[:min(len(dryRun), 400)])
	}
}
