package cli

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// usePlanTestHome points ~/.chest at a scratch directory so history written by
// `plan apply` never lands in the developer's real home, and so a personal
// ~/.chest/chest.toml cannot leak rules into these tests. Windows resolves the
// home directory from USERPROFILE rather than HOME, so both are set.
func usePlanTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func writePlanFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// snapshotPlanTree records every path under root with its content, so a test can
// prove that a command left the directory byte-for-byte unchanged. Directories
// are recorded as an explicit marker so a created folder shows up as a diff.
func snapshotPlanTree(t *testing.T, root string) map[string]string {
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

// runPlan executes a `chest plan` invocation the way a user would type it and
// returns everything it printed.
func runPlan(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := newPlanCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func writePlanJSON(t *testing.T, dir, name string, plan models.Plan) string {
	t.Helper()
	data, err := json.MarshalIndent(plan, "", "  ")
	require.NoError(t, err)
	return writePlanFixture(t, dir, name, string(data))
}

// TestPlanOutIsReadOnlyAndWritesValidArtifact is the core safety property of
// `chest plan --out`: it produces exactly the JSON `sort --dry-run --json`
// prints, and leaves the target directory byte-identical — no folders created,
// nothing moved.
func TestPlanOutIsReadOnlyAndWritesValidArtifact(t *testing.T) {
	usePlanTestHome(t)

	root := t.TempDir()
	writePlanFixture(t, root, "notes.txt", "hello notes")
	writePlanFixture(t, root, "photo.jpg", "\xFF\xD8\xFF\xE0 not really a jpeg")
	// The artifact lives outside the scanned directory so it cannot perturb
	// the snapshot it is compared against.
	out := filepath.Join(t.TempDir(), "plan.json")

	before := snapshotPlanTree(t, root)

	stdout, err := runPlan(t, root, "--type", "--out", out)
	require.NoError(t, err)
	assert.Contains(t, stdout, "plan saved")

	assert.Equal(t, before, snapshotPlanTree(t, root), "chest plan must not create folders or move files")

	data, err := os.ReadFile(out)
	require.NoError(t, err)
	var plan models.Plan
	require.NoError(t, json.Unmarshal(data, &plan), "artifact must parse as a models.Plan")

	assert.Equal(t, root, plan.RootPath)
	assert.Len(t, plan.Operations, 2)
	assert.Equal(t, 2, plan.TotalFiles)

	// The plan describes folders and destinations that do not exist yet, and
	// creating them is apply's job, not plan's.
	assert.NotEmpty(t, plan.FoldersToCreate)
	for _, folder := range plan.FoldersToCreate {
		assert.NoDirExists(t, folder, "chest plan must not create folders")
	}
	for _, op := range plan.Operations {
		assert.FileExists(t, op.Source)
		assert.NoFileExists(t, op.Destination)
		assert.NotEmpty(t, op.Reason, "every operation carries the WHY line")
	}
}

// TestPlanOutWithoutOutFlagStreamsJSON keeps `chest plan` usable as a pipe
// source, which is also how a plan gets reviewed before it is saved.
func TestPlanOutWithoutOutFlagStreamsJSON(t *testing.T) {
	usePlanTestHome(t)

	root := t.TempDir()
	writePlanFixture(t, root, "notes.txt", "hello notes")

	stdout, err := runPlan(t, root, "--rule", "*.txt -> Docs")
	require.NoError(t, err)

	var plan models.Plan
	require.NoError(t, json.Unmarshal([]byte(stdout), &plan))
	require.Len(t, plan.Operations, 1)
	assert.Equal(t, filepath.Join(root, "Docs", "notes.txt"), plan.Operations[0].Destination)
}

// TestPlanApplyMovesFilesRecordsHistoryAndUndoRestores walks the whole
// artifact lifecycle: save -> apply -> history entry -> undo puts every file
// back where it started.
func TestPlanApplyMovesFilesRecordsHistoryAndUndoRestores(t *testing.T) {
	usePlanTestHome(t)

	root := t.TempDir()
	notes := writePlanFixture(t, root, "notes.txt", "hello notes")
	photo := writePlanFixture(t, root, "photo.jpg", "\xFF\xD8\xFF\xE0 not really a jpeg")

	out := filepath.Join(t.TempDir(), "plan.json")
	_, err := runPlan(t, root, "--type", "--out", out)
	require.NoError(t, err)

	stdout, err := runPlan(t, "apply", out, "--yes")
	require.NoError(t, err)
	assert.Contains(t, stdout, "History recorded as operation #")

	assert.NoFileExists(t, notes)
	assert.NoFileExists(t, photo)
	assert.FileExists(t, filepath.Join(root, "Documents", "notes.txt"))
	assert.FileExists(t, filepath.Join(root, "Images", "photo.jpg"))

	mgr, err := history.DefaultManager()
	require.NoError(t, err)
	entries, err := mgr.List()
	require.NoError(t, err)
	require.Len(t, entries, 1, "applying a plan must record exactly one history entry")
	assert.Equal(t, "Complete", entries[0].Status)
	assert.Equal(t, root, entries[0].Directory)
	assert.Len(t, entries[0].Operations, 2)

	undo := newUndoCmd()
	var buf bytes.Buffer
	undo.SetOut(&buf)
	undo.SetErr(&buf)
	undo.SetArgs(nil)
	require.NoError(t, undo.Execute())

	assert.FileExists(t, notes)
	assert.FileExists(t, photo)
	assert.Equal(t, "hello notes", readFileString(t, notes))
	assert.NoDirExists(t, filepath.Join(root, "Images"))
}

// TestPlanApplyRefusesWholeBatchWhenASourceIsMissing is the all-or-nothing
// guarantee: one vanished file rejects the batch, so nothing is moved at all.
func TestPlanApplyRefusesWholeBatchWhenASourceIsMissing(t *testing.T) {
	usePlanTestHome(t)

	root := t.TempDir()
	missing := writePlanFixture(t, root, "gone.txt", "i will disappear")
	keeper := writePlanFixture(t, root, "stays.txt", "i am still here")

	out := filepath.Join(t.TempDir(), "plan.json")
	_, err := runPlan(t, root, "--rule", "ext=txt -> Docs", "--out", out)
	require.NoError(t, err)

	require.NoError(t, os.Remove(missing))
	before := snapshotPlanTree(t, root)

	stdout, err := runPlan(t, "apply", out, "--yes")
	require.Error(t, err, "a plan with a missing source must be refused")
	assert.Contains(t, err.Error(), "refusing to apply plan")
	assert.Contains(t, err.Error(), missing)
	assert.Contains(t, stdout, "no files were moved")

	assert.Equal(t, before, snapshotPlanTree(t, root), "a refused batch must not move a single file")
	assert.FileExists(t, keeper)
	assert.NoDirExists(t, filepath.Join(root, "Docs"))

	mgr, listErr := history.DefaultManager()
	require.NoError(t, listErr)
	entries, err := mgr.List()
	require.NoError(t, err)
	assert.Empty(t, entries, "a refused batch must not record history")
}

// TestPlanApplySkipsOccupiedDestinations covers the state that changes between
// saving and applying: a destination that is free at plan time but taken by the
// time the plan runs is skipped rather than silently overwritten.
func TestPlanApplySkipsOccupiedDestinations(t *testing.T) {
	usePlanTestHome(t)

	root := t.TempDir()
	source := writePlanFixture(t, root, "notes.txt", "the original notes")

	out := filepath.Join(t.TempDir(), "plan.json")
	_, err := runPlan(t, root, "--rule", "ext=txt -> Docs", "--out", out)
	require.NoError(t, err)

	// Somebody else gets there first.
	destDir := filepath.Join(root, "Docs")
	require.NoError(t, os.MkdirAll(destDir, 0o755))
	occupied := writePlanFixture(t, destDir, "notes.txt", "someone else's notes")

	stdout, err := runPlan(t, "apply", out, "--yes")
	require.NoError(t, err)
	assert.Contains(t, stdout, "skipped")

	assert.FileExists(t, source, "the source is left untouched when its destination is taken")
	assert.Equal(t, "someone else's notes", readFileString(t, occupied), "an existing destination file is never overwritten")
}

// TestPlanApplyRejectsUnreadablePlanFile keeps a typo from executing something
// unintended.
func TestPlanApplyRejectsUnreadablePlanFile(t *testing.T) {
	usePlanTestHome(t)

	dir := t.TempDir()
	broken := writePlanFixture(t, dir, "broken.json", "{not json")

	_, err := runPlan(t, "apply", broken, "--yes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid plan file")

	_, err = runPlan(t, "apply", filepath.Join(dir, "missing.json"), "--yes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read plan")
}

// TestPlanApplyEmptyPlanIsANoOp keeps a plan that matched nothing from looking
// like a failure.
func TestPlanApplyEmptyPlanIsANoOp(t *testing.T) {
	usePlanTestHome(t)

	root := t.TempDir()
	writePlanFixture(t, root, "notes.txt", "hello notes")
	out := writePlanJSON(t, t.TempDir(), "empty.json", models.Plan{RootPath: root})

	stdout, err := runPlan(t, "apply", out, "--yes")
	require.NoError(t, err)
	assert.Contains(t, stdout, "no operations")

	assert.FileExists(t, filepath.Join(root, "notes.txt"))
}

func TestPlanDiffDetectsAddedRemovedAndChangedOps(t *testing.T) {
	usePlanTestHome(t)

	root := t.TempDir()
	dir := t.TempDir()
	op := func(name, dest, reason string) models.Operation {
		return models.Operation{
			Source:      filepath.Join(root, name),
			Destination: filepath.Join(root, dest),
			Reason:      reason,
			Action:      models.CollisionRename,
		}
	}

	before := writePlanJSON(t, dir, "before.json", models.Plan{
		RootPath: root,
		Operations: []models.Operation{
			op("a.txt", "Docs/a.txt", "extension = txt"),
			op("b.txt", "Docs/b.txt", "extension = txt"),
			op("d.txt", "Docs/d.txt", "extension = txt"),
		},
		FoldersToCreate: []string{filepath.Join(root, "Docs")},
	})
	after := writePlanJSON(t, dir, "after.json", models.Plan{
		RootPath: root,
		Operations: []models.Operation{
			op("a.txt", "Docs/a.txt", "extension = txt"),   // unchanged
			op("b.txt", "Archive/b.txt", "type = video"),   // changed
			op("c.txt", "Images/c.txt", "extension = jpg"), // added
		},
		FoldersToCreate: []string{filepath.Join(root, "Archive")},
	})

	stdout, err := runPlan(t, "diff", before, after)
	require.NoError(t, err)
	assert.Contains(t, stdout, "1 added")
	assert.Contains(t, stdout, "1 removed")
	assert.Contains(t, stdout, "1 changed")
	assert.Contains(t, stdout, "c.txt")
	assert.Contains(t, stdout, "d.txt")
	assert.Contains(t, stdout, "Archive")
	assert.Contains(t, stdout, filepath.Join(root, "Docs", "b.txt"))

	// Read-only: comparing two plans must not execute or rewrite them.
	dataBefore, readErr := os.ReadFile(before)
	require.NoError(t, readErr)
	assert.Contains(t, string(dataBefore), "d.txt")
}

func TestPlanDiffJSONIsMachineReadable(t *testing.T) {
	usePlanTestHome(t)

	root := t.TempDir()
	dir := t.TempDir()
	same := models.Operation{Source: filepath.Join(root, "a.txt"), Destination: filepath.Join(root, "Docs", "a.txt")}
	other := models.Operation{Source: filepath.Join(root, "b.txt"), Destination: filepath.Join(root, "Docs", "b.txt")}

	before := writePlanJSON(t, dir, "before.json", models.Plan{RootPath: root, Operations: []models.Operation{same, other}})
	after := writePlanJSON(t, dir, "after.json", models.Plan{RootPath: root, Operations: []models.Operation{same}})

	stdout, err := runPlan(t, "diff", before, after, "--json")
	require.NoError(t, err)

	var diff planDiff
	require.NoError(t, json.Unmarshal([]byte(stdout), &diff))
	assert.Len(t, diff.Removed, 1)
	assert.Equal(t, other.Source, diff.Removed[0].Source)
	assert.Len(t, diff.Added, 0)
	assert.Len(t, diff.Changed, 0)
	assert.Equal(t, 1, diff.Unchanged)
}

func TestPlanDiffIdenticalPlans(t *testing.T) {
	usePlanTestHome(t)

	root := t.TempDir()
	dir := t.TempDir()
	plan := models.Plan{
		RootPath:   root,
		Operations: []models.Operation{{Source: filepath.Join(root, "a.txt"), Destination: filepath.Join(root, "Docs", "a.txt")}},
	}
	a := writePlanJSON(t, dir, "a.json", plan)
	b := writePlanJSON(t, dir, "b.json", plan)

	stdout, err := runPlan(t, "diff", a, b)
	require.NoError(t, err)
	assert.Contains(t, stdout, "plans are identical")
}

func TestPlanSubcommandsAreRegistered(t *testing.T) {
	names := map[string]bool{}
	for _, c := range newPlanCmd().Commands() {
		names[c.Name()] = true
	}
	assert.True(t, names["apply"], "chest plan apply must be reachable")
	assert.True(t, names["diff"], "chest plan diff must be reachable")
}

// TestPlanRejectsBadFlagValues mirrors sort's validation so a typo fails the
// same way whether the user typed `chest sort` or `chest plan`.
func TestPlanRejectsBadFlagValues(t *testing.T) {
	usePlanTestHome(t)
	root := t.TempDir()

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"collision", []string{root, "--collision", "explode"}, "invalid collision policy"},
		{"date source", []string{root, "--date", "--date-source", "created"}, "invalid date source"},
		{"date granularity", []string{root, "--date", "--date-granularity", "week"}, "invalid date granularity"},
		{"month format", []string{root, "--date", "--month-format", "roman"}, "invalid month format"},
		{"name and into", []string{root, "--name", "A", "--into", "B"}, "cannot combine --name and --into"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runPlan(t, tt.args...)
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), tt.want),
				"error %q should mention %q", err.Error(), tt.want)
		})
	}
}

// TestPlanSystemPathGuardIsNotWeakened keeps the OS-directory protection in
// force. The probe paths deliberately do not exist: the guard keys off the
// path prefix, so it fires before any scan starts, and a regression would fail
// fast instead of walking a real system directory.
func TestPlanSystemPathGuardIsNotWeakened(t *testing.T) {
	usePlanTestHome(t)

	probe := systemPathProbe(t)
	isSys, _ := filesystem.IsSystemPath(probe)
	require.True(t, isSys, "test probe %s must be inside a protected directory", probe)

	_, err := buildPlanArtifact(planFlags{out: filepath.Join(t.TempDir(), "plan.json")}, probe)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")

	// And the same protection applies when executing a saved plan.
	plan := models.Plan{
		RootPath: probe,
		Operations: []models.Operation{{
			Source:      filepath.Join(probe, "a.txt"),
			Destination: filepath.Join(probe, "Docs", "a.txt"),
		}},
	}
	_, _, err = preflightPlan(plan, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "protected")

	// --allow-system clears the protection block; whatever stops the batch
	// next (here: the missing source) is a different, expected complaint.
	_, _, err = preflightPlan(plan, true)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "protected")
	assert.Contains(t, err.Error(), "source no longer exists")
}

// systemPathProbe returns a path inside an OS-protected directory that does
// not exist, mirroring how filesystem.IsSystemPath decides what is protected on
// each platform.
func systemPathProbe(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		base := os.Getenv("WINDIR")
		if base == "" {
			base = os.Getenv("SystemRoot")
		}
		if base == "" {
			base = `C:\Windows`
		}
		return filepath.Join(base, "chest-plan-guard-probe", "does-not-exist")
	}
	return filepath.Join("/etc", "chest-plan-guard-probe", "does-not-exist")
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
