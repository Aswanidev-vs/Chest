package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/spf13/cobra"
)

// checkpointTestHome points os.UserHomeDir at a throwaway directory and
// returns it, so a test writing history never touches the developer's real
// ~/.chest. USERPROFILE is what os.UserHomeDir consults on Windows, HOME
// everywhere else, so both are redirected.
func checkpointTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// runCommand executes a freshly built command with captured output. The
// commands are rebuilt per call because cobra binds flags to the closure
// variables at construction time.
func runCommand(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// historyPath is the store a checkpoint/undo test writes through.
func historyPath(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".chest", "history.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// jsonPayload returns the JSON document a command printed, skipping any human
// lines that came before it. It scans line by line rather than looking for the
// first bracket, because an ANSI escape sequence contains one itself.
func jsonPayload(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "[") || strings.HasPrefix(line, "{") {
			return strings.Join(lines[i:], "\n")
		}
	}
	t.Fatalf("no JSON document in output:\n%s", out)
	return ""
}

// stageOrganizedFile writes content to dir/folder/name and returns the
// original/destination pair CHEST would have recorded for moving it there.
func stageOrganizedFile(t *testing.T, dir, folder, name, content string) (string, string) {
	t.Helper()
	src := filepath.Join(dir, name)
	dst := filepath.Join(dir, folder, name)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return src, dst
}

func TestCheckpointFlagsRegistered(t *testing.T) {
	cmd := newCheckpointCmd()

	if fl := cmd.Flags().Lookup("name"); fl == nil || fl.Shorthand != "n" {
		t.Errorf("--name missing or shorthand changed: %+v", fl)
	}
	if fl := cmd.Flags().Lookup("json"); fl == nil {
		t.Error("checkpoint is missing --json")
	}
	if fl := cmd.Flags().Lookup("csv"); fl == nil {
		t.Error("checkpoint is missing --csv")
	}

	list := findSubcommand(t, cmd, "list")
	for _, name := range []string{"json", "csv", "out"} {
		if list.Flags().Lookup(name) == nil {
			t.Errorf("checkpoint list is missing --%s", name)
		}
	}
}

func findSubcommand(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, sub := range parent.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	t.Fatalf("%s has no %q subcommand", parent.Name(), name)
	return nil
}

func TestCheckpointRecordsMarkerWithoutMovingFiles(t *testing.T) {
	home := checkpointTestHome(t)
	work := t.TempDir()
	src, dst := stageOrganizedFile(t, work, "Docs", "a.txt", "a")

	mgr := history.NewManager(historyPath(t, home))
	if _, err := mgr.Record(work, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}

	out, err := runCommand(t, newCheckpointCmd(), "--name", "before-import")
	if err != nil {
		t.Fatalf("checkpoint: %v\n%s", err, out)
	}
	if !strings.Contains(out, "before-import") {
		t.Errorf("output should name the checkpoint: %q", out)
	}
	if !strings.Contains(out, "chest undo --to before-import") {
		t.Errorf("output should show how to use the marker: %q", out)
	}

	// A marker moves nothing.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("checkpoint moved a file: %s exists", src)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("checkpoint disturbed the organized file: %v", err)
	}

	checkpoints, err := mgr.ListCheckpoints()
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 1 {
		t.Fatalf("stored %d checkpoints, want 1", len(checkpoints))
	}
	if checkpoints[0].Name != "before-import" || checkpoints[0].EntryID != 1 || checkpoints[0].ID != 1 {
		t.Errorf("stored checkpoint = %+v, want name before-import anchored to entry 1", checkpoints[0])
	}

	// Recording a marker must not invent history entries.
	entries, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("history grew to %d entries, want 1", len(entries))
	}
}

func TestCheckpointAcceptsPositionalLabel(t *testing.T) {
	home := checkpointTestHome(t)
	mgr := history.NewManager(historyPath(t, home))

	if _, err := runCommand(t, newCheckpointCmd(), "nightly"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	checkpoints, err := mgr.ListCheckpoints()
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 1 || checkpoints[0].Name != "nightly" {
		t.Fatalf("checkpoints = %+v, want one named nightly", checkpoints)
	}
}

func TestCheckpointArgumentErrors(t *testing.T) {
	checkpointTestHome(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no label", nil, "checkpoint name required"},
		{"empty label", []string{"--name", ""}, "checkpoint name required"},
		{"named twice", []string{"--name", "a", "b"}, "checkpoint named twice"},
		{"export flag belongs to list", []string{"--name", "a", "--json"}, "apply to 'chest checkpoint list'"},
		{"too many args", []string{"a", "b"}, "accepts at most 1 arg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runCommand(t, newCheckpointCmd(), tt.args...)
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestCheckpointListHumanAndJSON(t *testing.T) {
	home := checkpointTestHome(t)
	mgr := history.NewManager(historyPath(t, home))
	for _, name := range []string{"first", "second"} {
		if _, err := mgr.AddCheckpoint(name); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runCommand(t, newCheckpointCmd(), "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"LABEL", "second", "first"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
	// Newest first.
	if strings.Index(out, "second") > strings.Index(out, "first") {
		t.Errorf("checkpoints should be listed newest first:\n%s", out)
	}

	out, err = runCommand(t, newCheckpointCmd(), "list", "--json")
	if err != nil {
		t.Fatalf("list --json: %v", err)
	}
	var got []history.Checkpoint
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("list --json is not valid JSON: %v\n%s", err, out)
	}
	// The JSON report is the stored order, oldest first, matching how
	// `chest history --json` reports its own entries; only the human table and
	// the CSV are reversed for reading.
	want := []string{"first", "second"}
	if len(got) != 2 {
		t.Fatalf("got %d checkpoints in JSON, want 2", len(got))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("checkpoint %d = %q, want %q", i, got[i].Name, name)
		}
	}
	if got[0].ID == 0 || got[0].EntryID != 0 || got[0].Timestamp.IsZero() {
		t.Errorf("JSON checkpoint is missing id/entry_id/timestamp: %+v", got[0])
	}
}

func TestCheckpointListEmptyAndConflictingFlags(t *testing.T) {
	checkpointTestHome(t)

	out, err := runCommand(t, newCheckpointCmd(), "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "No checkpoints recorded") {
		t.Errorf("empty list should say so:\n%s", out)
	}
	if !strings.Contains(out, "chest checkpoint --name") {
		t.Errorf("empty list should hint at how to record one:\n%s", out)
	}

	if _, err := runCommand(t, newCheckpointCmd(), "list", "--json", "--csv"); err == nil ||
		!strings.Contains(err.Error(), "choose one of --json or --csv") {
		t.Errorf("--json with --csv error = %v, want the standard conflict error", err)
	}
}

func TestCsvRowsForCheckpoints(t *testing.T) {
	rows := csvRowsForCheckpoints([]history.Checkpoint{
		{Name: "older", ID: 1, EntryID: 3},
		{Name: "newer", ID: 2, EntryID: 7},
	})
	want := [][]string{
		{"id", "name", "timestamp", "entry_id"},
		{"2", "newer", rows[1][2], "7"},
		{"1", "older", rows[2][2], "3"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("csvRowsForCheckpoints mismatch:\n got %v\nwant %v", rows, want)
	}
}

func TestHistoryRetentionFlagsRegistered(t *testing.T) {
	cmd := newHistoryCmd()

	prune := cmd.Flags().Lookup("prune")
	if prune == nil || prune.Shorthand != "p" {
		t.Errorf("--prune missing or shorthand changed: %+v", prune)
	}
	keep := cmd.Flags().Lookup("keep")
	if keep == nil || keep.Shorthand != "k" {
		t.Errorf("--keep missing or shorthand changed: %+v", keep)
	}
	if keep.DefValue != "100" {
		t.Errorf("--keep default = %q, want 100", keep.DefValue)
	}
	maxEntries := cmd.Flags().Lookup("max-entries")
	if maxEntries == nil || maxEntries.Shorthand != "m" {
		t.Errorf("--max-entries missing or shorthand changed: %+v", maxEntries)
	}

	// The pre-existing report flags are untouched.
	for _, name := range []string{"json", "csv", "out"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("history lost its --%s flag", name)
		}
	}
}

func TestValidateRetentionFlags(t *testing.T) {
	tests := []struct {
		name           string
		prune, keepSet bool
		keep           int
		maxEntries     int
		maxEntriesSet  bool
		wantErr        string
	}{
		{"no flags at all", false, false, 100, 0, false, ""},
		{"prune with the default keep", true, false, 100, 0, false, ""},
		{"prune with an explicit keep", true, true, 25, 0, false, ""},
		{"keep below one is refused", true, false, 0, 0, false, "--keep must be at least 1"},
		{"negative keep is refused", true, false, -5, 0, false, "--keep must be at least 1"},
		{"keep without prune is refused", false, true, 10, 0, false, "--keep only applies together with --prune"},
		{"max-entries below one is refused", false, false, 100, 0, true, "--max-entries must be at least 1"},
		{"max-entries alone is fine", false, false, 100, 500, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRetentionFlags(tt.prune, tt.keep, tt.keepSet, tt.maxEntries, tt.maxEntriesSet)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestHistoryPruneKeepsExactlyTheNewestTwo drives the retention flags through
// the command and reads back what survived.
func TestHistoryPruneKeepsExactlyTheNewestTwo(t *testing.T) {
	home := checkpointTestHome(t)
	mgr := history.NewManager(historyPath(t, home))

	const total = 5
	for i := 0; i < total; i++ {
		name := string(rune('a' + i))
		src := filepath.Join(t.TempDir(), name+".txt")
		dst := filepath.Join(t.TempDir(), "Docs", name+".txt")
		if _, err := mgr.Record(t.TempDir(), []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runCommand(t, newHistoryCmd(), "--prune", "--keep", "2", "--json")
	if err != nil {
		t.Fatalf("history --prune: %v\n%s", err, out)
	}
	if !strings.Contains(out, "removed") {
		t.Errorf("prune should report how many entries it removed:\n%s", out)
	}
	if !strings.Contains(out, "3") {
		t.Errorf("prune should report the removed count of 3:\n%s", out)
	}

	// The JSON report is the list as it stands after pruning.
	payload := jsonPayload(t, out)
	var entries []models.HistoryEntry
	if err := json.Unmarshal([]byte(payload), &entries); err != nil {
		t.Fatalf("history --json payload is not valid JSON: %v\n%s", err, out)
	}
	if len(entries) != 2 {
		t.Fatalf("kept %d entries, want 2", len(entries))
	}
	if entries[0].ID != total-1 || entries[1].ID != total {
		t.Errorf("kept entries #%d and #%d, want the newest two (#%d and #%d)", entries[0].ID, entries[1].ID, total-1, total)
	}
}

// TestHistoryMaxEntriesIsStoredAndEnforced covers the persistent cap.
func TestHistoryMaxEntriesIsStoredAndEnforced(t *testing.T) {
	home := checkpointTestHome(t)
	path := historyPath(t, home)
	mgr := history.NewManager(path)

	for i := 0; i < 4; i++ {
		src := filepath.Join(t.TempDir(), "f.txt")
		dst := filepath.Join(t.TempDir(), "Docs", "f.txt")
		if _, err := mgr.Record(t.TempDir(), []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runCommand(t, newHistoryCmd(), "--max-entries", "2")
	if err != nil {
		t.Fatalf("history --max-entries: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Retention cap set") {
		t.Errorf("output should report the new cap:\n%s", out)
	}

	// The cap lives in the history file, so the next run inherits it.
	var probe struct {
		MaxEntries int `json:"max_entries"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.MaxEntries != 2 {
		t.Errorf("stored max_entries = %d, want 2", probe.MaxEntries)
	}

	fresh := history.NewManager(path)
	for i := 0; i < 3; i++ {
		src := filepath.Join(t.TempDir(), "g.txt")
		dst := filepath.Join(t.TempDir(), "Docs", "g.txt")
		if _, err := fresh.Record(t.TempDir(), []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := fresh.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("recorded past the stored cap: %d entries", len(entries))
	}
}

func TestHistoryWithoutRetentionFlagsIsUnchanged(t *testing.T) {
	home := checkpointTestHome(t)
	mgr := history.NewManager(historyPath(t, home))

	for i := 0; i < 3; i++ {
		src := filepath.Join(t.TempDir(), "f.txt")
		dst := filepath.Join(t.TempDir(), "Docs", "f.txt")
		if _, err := mgr.Record(t.TempDir(), []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runCommand(t, newHistoryCmd(), "--json")
	if err != nil {
		t.Fatalf("history --json: %v", err)
	}
	if strings.Contains(out, "pruned") || strings.Contains(out, "Retention cap") {
		t.Errorf("a plain history run must not prune anything:\n%s", out)
	}

	var entries []models.HistoryEntry
	if err := json.Unmarshal([]byte(jsonPayload(t, out)), &entries); err != nil {
		t.Fatalf("history --json is not valid JSON: %v\n%s", err, out)
	}
	if len(entries) != 3 {
		t.Errorf("kept %d of 3 entries, want all 3", len(entries))
	}
}
