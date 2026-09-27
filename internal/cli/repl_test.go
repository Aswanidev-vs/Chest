package cli

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newReplTestSession builds a session over a throwaway directory. HOME is
// redirected by the caller when a test touches the template store.
func newReplTestSession(t *testing.T, target string) *replSession {
	t.Helper()
	s, err := replFlags{}.session(target)
	require.NoError(t, err)
	return s
}

func writeReplFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

// snapshotReplTree records every path under root with its content, so a test can
// prove that a session left the target directory byte-for-byte unchanged.
func snapshotReplTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			snapshot[rel+string(filepath.Separator)] = "<dir>"
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		snapshot[rel] = string(data)
		return nil
	}))
	return snapshot
}

// captureReplStdout collects what printDryRun writes, since it prints straight
// to os.Stdout rather than through the session's writer.
func captureReplStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	// Drain concurrently: a pipe has a finite buffer, so a plan larger than that
	// would block printDryRun forever if we only read once fn has returned.
	drained := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		drained <- string(out)
	}()

	fn()

	require.NoError(t, w.Close())
	os.Stdout = old
	out := <-drained
	require.NoError(t, r.Close())
	return out
}

// useTempHome points ~/.chest at a scratch directory so template tests never
// touch the developer's real home. Windows resolves the home dir from
// USERPROFILE rather than HOME, so both are set.
func useTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// TestReplPreviewLeavesTargetUnchanged drives a full scripted session and then
// proves the single most important property of this feature: previewing a plan
// mutates nothing on disk.
func TestReplPreviewLeavesTargetUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeReplFixture(t, dir, "clip.mp4", "fake mp4 bytes")
	writeReplFixture(t, dir, "photo.png", "fake png bytes")
	before := snapshotReplTree(t, dir)

	s := newReplTestSession(t, dir)
	var out bytes.Buffer

	dryRun := captureReplStdout(t, func() {
		s.run(strings.NewReader("list\nadd type=Video -> Videos\nlist\nscan\npreview\nquit\n"), &out)
	})

	session := out.String()
	assert.Contains(t, session, "No rules loaded", "list on an empty rule set should say so")
	assert.Contains(t, session, "added", "add should confirm the new rule")
	assert.Contains(t, session, "scanned 2 file(s)", "scan should re-read the target")

	assert.Contains(t, dryRun, "Videos", "preview should route the mp4 into Videos")
	assert.Contains(t, dryRun, "1 files would be moved", "only the mp4 matches type=Video")
	assert.Contains(t, dryRun, "No changes made.")

	assert.Equal(t, before, snapshotReplTree(t, dir), "preview and rule edits must not touch the target directory")
}

// TestReplUnknownCommandContinues proves a bad command does not end the session.
func TestReplUnknownCommandContinues(t *testing.T) {
	s := newReplTestSession(t, t.TempDir())
	var out bytes.Buffer

	s.run(strings.NewReader("bogus\nadd *.mp4 -> Videos\nquit\n"), &out)

	assert.Contains(t, out.String(), "unknown command 'bogus'")
	require.Len(t, s.rules, 1, "the loop must keep processing after an unknown command")
	assert.Equal(t, "Videos", s.rules[0].Destination)
}

// TestReplMalformedAddContinues proves a rule parse error is reported inline
// and the rejected rule is not stored.
func TestReplMalformedAddContinues(t *testing.T) {
	s := newReplTestSession(t, t.TempDir())
	var out bytes.Buffer

	s.run(strings.NewReader("add this-is-not-a-rule\nadd type=Video -> Videos\nexit\n"), &out)

	assert.Contains(t, out.String(), "invalid rule syntax")
	require.Len(t, s.rules, 1, "only the valid rule should be kept")
	assert.Equal(t, "Videos", s.rules[0].Destination)
}

// TestReplApplyWithoutConfirmationMovesNothing proves the answer to the
// confirmation prompt is read from the same buffered reader the loop uses, and
// that declining leaves the target untouched.
func TestReplApplyWithoutConfirmationMovesNothing(t *testing.T) {
	dir := t.TempDir()
	writeReplFixture(t, dir, "clip.mp4", "fake mp4 bytes")
	before := snapshotReplTree(t, dir)

	s := newReplTestSession(t, dir)
	var out bytes.Buffer

	s.run(strings.NewReader("add *.mp4 -> Videos\napply\nn\nquit\n"), &out)

	assert.Contains(t, out.String(), "Aborted. Nothing was moved.")
	assert.Equal(t, before, snapshotReplTree(t, dir), "declining the confirmation must not move any file")
}

// TestReplSaveLoadRoundTripsHelperFields proves the TOML template store is
// lossless for the convenience fields that rendered rule strings would drop.
func TestReplSaveLoadRoundTripsHelperFields(t *testing.T) {
	home := useTempHome(t)
	dir := t.TempDir()

	s := newReplTestSession(t, dir)
	seed := []models.Rule{
		{
			Name:        "Big video",
			Priority:    42,
			Destination: "Videos/Large",
			MinSize:     "10MB",
			MaxSize:     "1GB",
			Type:        "Video",
			Extensions:  []string{"mp4", "mkv"},
			Conditions: []models.Condition{
				{Field: models.FieldName, Operator: models.OpStartsWith, Value: "clip"},
			},
		},
	}
	s.rules = seed

	var out bytes.Buffer
	s.run(strings.NewReader("save bigfiles\ntemplates\nrm 1\nlist\nload bigfiles\nquit\n"), &out)

	session := out.String()
	assert.Contains(t, session, "bigfiles", "templates should list the saved template")
	assert.Contains(t, session, "No rules loaded", "rm should empty the rule set")
	assert.Contains(t, session, "loaded 1 rule(s)", "load should restore the rule set")
	require.Equal(t, seed, s.rules, "TOML round-trip must preserve every rule field")

	saved, err := os.ReadFile(filepath.Join(home, ".chest", "templates", "bigfiles.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(saved), "min_size", "the helper field must be persisted, not just re-derived")
}

// TestReplTemplateNameSanitization covers the path-traversal guard on save.
func TestReplTemplateNameSanitization(t *testing.T) {
	tests := []struct {
		name     string
		wantErr  string
		template string
	}{
		{name: "parent traversal", template: "../evil", wantErr: "invalid template name"},
		{name: "nested path", template: "a/b", wantErr: "invalid template name"},
		{name: "windows separator", template: `a\b`, wantErr: "invalid template name"},
		{name: "bare parent", template: "..", wantErr: "invalid template name"},
		{name: "empty", template: "", wantErr: "template name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := useTempHome(t)
			dir := t.TempDir()

			s := newReplTestSession(t, dir)
			s.rules = []models.Rule{{Name: "keep", Priority: 1, Destination: "Keep"}}

			var out bytes.Buffer
			s.run(strings.NewReader("save "+tt.template+"\nquit\n"), &out)

			assert.Contains(t, out.String(), tt.wantErr)
			assert.NoDirExists(t, filepath.Join(home, ".chest", "templates"),
				"a rejected name must not create anything under the home directory")
		})
	}
}

// TestReplEditReplacesRuleInPlace checks 1-based indexing for edit.
func TestReplEditReplacesRule(t *testing.T) {
	s := newReplTestSession(t, t.TempDir())
	var out bytes.Buffer

	s.run(strings.NewReader("add *.mp4 -> Videos\nedit 1 *.png -> Pictures\nlist\nquit\n"), &out)

	require.Len(t, s.rules, 1)
	assert.Equal(t, "Pictures", s.rules[0].Destination, "edit should replace rule 1, not append")
	assert.Contains(t, out.String(), "replaced rule 1")
}

// TestReplEditRejectsOutOfRangeIndex covers the index bounds check.
func TestReplEditRejectsOutOfRangeIndex(t *testing.T) {
	s := newReplTestSession(t, t.TempDir())
	var out bytes.Buffer

	s.run(strings.NewReader("add *.mp4 -> Videos\nedit 7 *.png -> Pictures\nquit\n"), &out)

	assert.Contains(t, out.String(), "no such rule")
	assert.Equal(t, "Videos", s.rules[0].Destination)
}

// TestReplPresetCommand covers both preset listing and preset loading.
func TestReplPresetCommand(t *testing.T) {
	s := newReplTestSession(t, t.TempDir())
	var out bytes.Buffer

	s.run(strings.NewReader("preset\npreset media\nlist\nquit\n"), &out)

	assert.Contains(t, out.String(), "presets: downloads, media, documents, developer, photos")
	assert.Contains(t, out.String(), "'media' (4 rule(s))", "preset should load the media rule set")
	assert.NotEmpty(t, s.rules, "preset should replace the rule set")
}

// TestReplScanRefreshesTheFileList proves a file created after startup is only
// visible after an explicit scan.
func TestReplScanRefreshesTheFileList(t *testing.T) {
	dir := t.TempDir()
	writeReplFixture(t, dir, "first.mp4", "one")

	s := newReplTestSession(t, dir)
	require.Len(t, s.files, 1)

	writeReplFixture(t, dir, "second.mp4", "two")
	require.Len(t, s.files, 1, "the cached scan should not see the new file yet")

	var out bytes.Buffer
	s.run(strings.NewReader("scan\nquit\n"), &out)

	assert.Contains(t, out.String(), "scanned 2 file(s)")
	assert.Len(t, s.files, 2)
}

// TestReplEndsOnEOF proves Ctrl-D (or a piped script with no trailing newline)
// leaves the loop instead of spinning.
func TestReplEndsOnEOF(t *testing.T) {
	s := newReplTestSession(t, t.TempDir())
	var out bytes.Buffer

	s.run(strings.NewReader("list"), &out)

	assert.Contains(t, out.String(), "No rules loaded")
}

// TestReplRefusesSystemPath checks the command-level guard on a root volume.
func TestReplRefusesSystemPath(t *testing.T) {
	root := "/"
	if vol := filepath.VolumeName(os.TempDir()); vol != "" {
		root = vol + string(filepath.Separator)
	}

	cmd := newReplCmd()
	cmd.SetArgs([]string{root})
	err := cmd.Execute()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

// TestReplRejectsInvalidCollisionPolicy keeps flag validation consistent with sort.
func TestReplRejectsInvalidCollisionPolicy(t *testing.T) {
	cmd := newReplCmd()
	cmd.SetArgs([]string{t.TempDir(), "--collision", "explode"})
	err := cmd.Execute()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid collision policy")
}

// TestReplRejectsUnknownPreset guards the -p path.
func TestReplRejectsUnknownPreset(t *testing.T) {
	cmd := newReplCmd()
	cmd.SetArgs([]string{t.TempDir(), "--preset", "nope"})
	err := cmd.Execute()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown preset")
}
