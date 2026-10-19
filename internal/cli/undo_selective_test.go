package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/models"
)

// decodeJSON unmarshals a command's JSON report, failing the test with the
// offending output when it is not the expected shape.
func decodeJSON(t *testing.T, out string, v any) error {
	t.Helper()
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("report is not valid JSON: %v\n%s", err, out)
	}
	return nil
}

func TestParseUndoArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		to      string
		files   []string
		wantID  int
		wantErr string
	}{
		{"no arguments undoes the latest", nil, "", nil, 0, ""},
		{"an id selects that operation", []string{"7"}, "", nil, 7, ""},
		{"a non-numeric id is rejected", []string{"abc"}, "", nil, 0, "invalid operation ID 'abc'"},
		{"checkpoint mode takes no id", nil, "before", nil, 0, ""},
		{"checkpoint mode rejects an id", []string{"3"}, "before", nil, 0, "--to cannot be combined with an operation ID"},
		{"checkpoint mode rejects --file", nil, "before", []string{"a.txt"}, 0, "--to cannot be combined with --file"},
		{"--file requires an id", nil, "", []string{"a.txt"}, 0, "--file requires an operation ID"},
		{"--file with an id is fine", []string{"3"}, "", []string{"a.txt"}, 3, ""},
		{"--file may be repeated", []string{"3"}, "", []string{"a.txt", "b.txt"}, 3, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := parseUndoArgs(tt.args, tt.to, tt.files)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tt.wantID {
				t.Errorf("target ID = %d, want %d", id, tt.wantID)
			}
		})
	}
}

func TestUndoFlagsRegistered(t *testing.T) {
	cmd := newUndoCmd()

	to := cmd.Flags().Lookup("to")
	if to == nil || to.Shorthand != "t" {
		t.Errorf("--to missing or shorthand changed: %+v", to)
	}
	file := cmd.Flags().Lookup("file")
	if file == nil || file.Shorthand != "f" {
		t.Errorf("--file missing or shorthand changed: %+v", file)
	}
	// The pre-existing flag keeps its name and its zero value.
	allow := cmd.Flags().Lookup("allow-system")
	if allow == nil || allow.DefValue != "false" {
		t.Errorf("--allow-system changed: %+v", allow)
	}
	// The pre-existing subcommand is still reachable.
	findSubcommand(t, cmd, "cache")
}

// selectiveUndoFixture records one operation moving three files and returns
// its ID together with the paths it staged.
type selectiveUndoFixture struct {
	entryID int
	manager *history.Manager
	work    string
	files   map[string][2]string // name -> {original, destination}
}

// newSelectiveUndoFixture builds an isolated home with a real history file
// describing three organized files, so the command under test moves real bytes.
func newSelectiveUndoFixture(t *testing.T) *selectiveUndoFixture {
	t.Helper()
	home := checkpointTestHome(t)
	work := t.TempDir()
	mgr := history.NewManager(historyPath(t, home))

	f := &selectiveUndoFixture{manager: mgr, work: work, files: map[string][2]string{}}
	var ops []models.HistoryOperation
	for _, name := range []string{"report.pdf", "notes.md", "photo.png"} {
		src, dst := stageOrganizedFile(t, work, "Docs", name, "contents of "+name)
		ops = append(ops, models.HistoryOperation{OriginalSource: src, Destination: dst, Reason: "test"})
		f.files[name] = [2]string{src, dst}
	}
	entry, err := mgr.Record(work, ops)
	if err != nil {
		t.Fatal(err)
	}
	f.entryID = entry.ID
	return f
}

// assertAt checks where one of the fixture's files is. "original" means back at
// the path it came from, "organized" means still at its destination, and
// "restored" means the destination is empty again.
func assertAt(t *testing.T, f *selectiveUndoFixture, name, where string) {
	t.Helper()
	pair := f.files[name]
	src, dst := pair[0], pair[1]
	_, srcErr := os.Stat(src)
	_, dstErr := os.Stat(dst)
	switch where {
	case "original":
		if srcErr != nil {
			t.Errorf("%s should be back at %s: %v", name, src, srcErr)
		}
	case "organized":
		if dstErr != nil {
			t.Errorf("%s should still be organized at %s: %v", name, dst, dstErr)
		}
	case "restored":
		if !os.IsNotExist(dstErr) {
			t.Errorf("%s should have left %s (stat err=%v)", name, dst, dstErr)
		}
	default:
		t.Fatalf("unknown location %q", where)
	}
}

func TestUndoFileRevertsOnlyTheMatchingBasename(t *testing.T) {
	f := newSelectiveUndoFixture(t)

	out, err := runCommand(t, newUndoCmd(), "1", "--file", "report.pdf")
	if err != nil {
		t.Fatalf("undo --file: %v\n%s", err, out)
	}
	if !strings.Contains(out, "report.pdf") {
		t.Errorf("output should name the restored file:\n%s", out)
	}

	assertAt(t, f, "report.pdf", "original")
	assertAt(t, f, "report.pdf", "restored")
	assertAt(t, f, "notes.md", "organized")
	assertAt(t, f, "photo.png", "organized")

	// The rest of the entry is still undoable, both file by file and as a whole.
	out, err = runCommand(t, newUndoCmd(), "1", "--file", "notes.md")
	if err != nil {
		t.Fatalf("second selective undo: %v\n%s", err, out)
	}
	assertAt(t, f, "notes.md", "original")
	assertAt(t, f, "notes.md", "restored")

	// What is left of the entry is still undoable as a whole, and only it.
	if _, err := runCommand(t, newUndoCmd(), "1"); err != nil {
		t.Fatalf("plain undo of the remainder: %v", err)
	}
	assertAt(t, f, "photo.png", "original")
	assertAt(t, f, "photo.png", "restored")
}

func TestUndoFileReportsUnmatchedNames(t *testing.T) {
	f := newSelectiveUndoFixture(t)

	out, err := runCommand(t, newUndoCmd(), "1", "--file", "report.pdf", "--file", "missing.zip")
	if err != nil {
		t.Fatalf("undo --file: %v\n%s", err, out)
	}
	if !strings.Contains(out, "missing.zip") {
		t.Errorf("an unmatched name must be reported, not swallowed:\n%s", out)
	}
	if !strings.Contains(out, "report.pdf") {
		t.Errorf("output should name the restored file:\n%s", out)
	}

	// The matched file was still reverted.
	assertAt(t, f, "report.pdf", "original")
	assertAt(t, f, "notes.md", "organized")
	assertAt(t, f, "photo.png", "organized")
}

func TestUndoFileWithNoMatchMovesNothing(t *testing.T) {
	f := newSelectiveUndoFixture(t)

	out, err := runCommand(t, newUndoCmd(), "1", "--file", "absent.txt")
	if err == nil {
		t.Fatalf("expected a clear error, got output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "absent.txt") {
		t.Errorf("error should name the file that was not found: %v", err)
	}
	// The refusal lists what the operation actually moved, so a typo is fixable.
	for _, want := range []string{"report.pdf", "notes.md", "photo.png"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should list the files the operation moved (%s): %v", want, err)
		}
	}

	for name := range f.files {
		assertAt(t, f, name, "organized")
	}
}

func TestUndoFileRefusesUnknownOperation(t *testing.T) {
	newSelectiveUndoFixture(t)

	_, err := runCommand(t, newUndoCmd(), "99", "--file", "report.pdf")
	if err == nil || !strings.Contains(err.Error(), "history operation #99 not found") {
		t.Errorf("error = %v, want the unknown-operation message", err)
	}
}

func TestUndoFileJSONReport(t *testing.T) {
	newSelectiveUndoFixture(t)

	out, err := runCommand(t, newUndoCmd(), "1", "--file", "report.pdf", "--json")
	if err != nil {
		t.Fatalf("undo --file --json: %v\n%s", err, out)
	}
	var res history.UndoFilesResult
	if err := decodeJSON(t, out, &res); err != nil {
		return
	}
	if res.EntryID != 1 {
		t.Errorf("entry_id = %d, want 1", res.EntryID)
	}
	if res.Files != 1 {
		t.Errorf("files = %d, want 1", res.Files)
	}
	if len(res.Restored) != 1 || res.Restored[0] != "report.pdf" {
		t.Errorf("restored = %v, want [report.pdf]", res.Restored)
	}
	if res.Remaining != 2 {
		t.Errorf("remaining = %d, want 2", res.Remaining)
	}
}

func TestUndoToJSONReport(t *testing.T) {
	home := checkpointTestHome(t)
	work := t.TempDir()
	mgr := history.NewManager(historyPath(t, home))
	if _, err := mgr.AddCheckpoint("before"); err != nil {
		t.Fatal(err)
	}
	src, dst := stageOrganizedFile(t, work, "Docs", "a.txt", "a")
	if _, err := mgr.Record(work, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}

	out, err := runCommand(t, newUndoCmd(), "--to", "before", "--json")
	if err != nil {
		t.Fatalf("undo --to --json: %v\n%s", err, out)
	}
	var res history.UndoToResult
	if err := decodeJSON(t, out, &res); err != nil {
		return
	}
	if res.Checkpoint != "before" || res.Files != 1 || len(res.UndoneIDs) != 1 || res.UndoneIDs[0] != 1 {
		t.Errorf("unexpected result: %+v", res)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("file should be restored at %s: %v", src, err)
	}
}

func TestUndoToUnknownCheckpoint(t *testing.T) {
	home := checkpointTestHome(t)
	mgr := history.NewManager(historyPath(t, home))
	if _, err := mgr.Record(t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}

	out, err := runCommand(t, newUndoCmd(), "--to", "nowhere")
	if err == nil {
		t.Fatalf("expected an error, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "checkpoint 'nowhere' not found") {
		t.Errorf("error = %v, want the unknown-checkpoint message", err)
	}
}

func TestUndoToRefusedBatchMovesNothingThroughTheCLI(t *testing.T) {
	home := checkpointTestHome(t)
	work := t.TempDir()
	mgr := history.NewManager(historyPath(t, home))
	if _, err := mgr.AddCheckpoint("before"); err != nil {
		t.Fatal(err)
	}

	// Two runs; the second one's file has since been deleted.
	srcA, dstA := stageOrganizedFile(t, work, "Docs", "a.txt", "a")
	if _, err := mgr.Record(work, []models.HistoryOperation{{OriginalSource: srcA, Destination: dstA, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}
	srcB, dstB := stageOrganizedFile(t, work, "Docs", "b.txt", "b")
	if _, err := mgr.Record(work, []models.HistoryOperation{{OriginalSource: srcB, Destination: dstB, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dstB); err != nil {
		t.Fatal(err)
	}

	out, err := runCommand(t, newUndoCmd(), "--to", "before")
	if err == nil {
		t.Fatalf("expected the batch to be refused, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), dstB) {
		t.Errorf("error should name the file that cannot be restored: %v", err)
	}
	// Not one file moved, in either run.
	if _, err := os.Stat(srcA); !os.IsNotExist(err) {
		t.Errorf("%s must stay put after a refused batch", srcA)
	}
	if _, err := os.Stat(dstA); err != nil {
		t.Errorf("the healthy file must stay organized: %v", err)
	}
}

func TestUndoRejectsConflictingReportFlags(t *testing.T) {
	checkpointTestHome(t)

	_, err := runCommand(t, newUndoCmd(), "--json", "--csv")
	if err == nil || !strings.Contains(err.Error(), "choose one of --json or --csv") {
		t.Errorf("error = %v, want the standard conflict error", err)
	}
}

func TestCsvRowsForUndoFiles(t *testing.T) {
	rows := csvRowsForUndoFiles(&history.UndoFilesResult{
		EntryID:   4,
		Requested: []string{"a.txt", "ghost.txt"},
		Restored:  []string{"a.txt"},
		Remaining: 2,
		Files:     1,
	})
	want := [][]string{
		{"entry_id", "file", "result"},
		{"4", "a.txt", "restored"},
		{"4", "ghost.txt", "unmatched"},
	}
	if strings.Join(rows[0], ",") != strings.Join(want[0], ",") ||
		strings.Join(rows[1], ",") != strings.Join(want[1], ",") ||
		strings.Join(rows[2], ",") != strings.Join(want[2], ",") {
		t.Fatalf("csvRowsForUndoFiles mismatch:\n got %v\nwant %v", rows, want)
	}
}

func TestCsvRowsForUndoTo(t *testing.T) {
	rows := csvRowsForUndoTo(&history.UndoToResult{
		Checkpoint:    "before",
		EntryID:       2,
		UndoneIDs:     []int{3, 4},
		AlreadyUndone: []int{5},
		Files:         7,
	})
	want := [][]string{
		{"checkpoint", "entry_id", "undone_ids", "already_undone", "files"},
		{"before", "2", "3 4", "1", "7"},
	}
	if strings.Join(rows[0], ",") != strings.Join(want[0], ",") ||
		strings.Join(rows[1], ",") != strings.Join(want[1], ",") {
		t.Fatalf("csvRowsForUndoTo mismatch:\n got %v\nwant %v", rows, want)
	}
}
