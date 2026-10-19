package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/indexer"
	"github.com/Aswanidev-vs/chest/internal/models"
)

// --- flags -------------------------------------------------------------------

func TestSimilarFlagsRegistered(t *testing.T) {
	cmd := newDuplicatesCmd()

	similar := cmd.Flags().Lookup("similar")
	if similar == nil {
		t.Fatal("duplicates is missing --similar flag")
	}
	if similar.DefValue != "false" {
		t.Errorf("--similar default = %q, want false", similar.DefValue)
	}

	threshold := cmd.Flags().Lookup("threshold")
	if threshold == nil {
		t.Fatal("duplicates is missing --threshold flag")
	}
	if threshold.DefValue != "10" {
		t.Errorf("--threshold default = %q, want 10", threshold.DefValue)
	}

	quarantine := cmd.Flags().Lookup("quarantine")
	if quarantine == nil {
		t.Fatal("duplicates is missing --quarantine flag")
	}
	if quarantine.DefValue != "" {
		t.Errorf("--quarantine default = %q, want empty", quarantine.DefValue)
	}

	yes := cmd.Flags().Lookup("yes")
	if yes == nil {
		t.Fatal("duplicates is missing --yes flag")
	}
	if yes.Shorthand != "y" {
		t.Errorf("--yes shorthand = %q, want y", yes.Shorthand)
	}
}

// TestQuarantineRejectsReportModes: quarantine moves files while a report mode
// only describes them. Combining them is refused before anything is opened.
func TestQuarantineRejectsReportModes(t *testing.T) {
	for _, flag := range []string{"--json", "--csv", "--out"} {
		t.Run(flag, func(t *testing.T) {
			cmd := newDuplicatesCmd()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs([]string{"--quarantine", "staging", flag, "out.json"})
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected an error when --quarantine is combined with a report mode")
			}
			if !strings.Contains(err.Error(), "--quarantine cannot be combined") {
				t.Fatalf("error = %v, want it to mention the --quarantine conflict", err)
			}
		})
	}
}

// --- report shapes -----------------------------------------------------------

// TestDuplicatesReportPayloadUnchangedWithoutSimilar pins the regression
// guarantee: without --similar the machine-readable output is exactly what it
// always was — a bare array of duplicate groups, in the original CSV shape.
func TestDuplicatesReportPayloadUnchangedWithoutSimilar(t *testing.T) {
	groups := []indexer.DuplicateGroup{
		{Hash: "abc", Size: 10, Files: []indexer.IndexedFile{{Path: "/x/a"}, {Path: "/x/b"}}},
	}
	similar := []indexer.SimilarGroup{
		{Hash: "0123456789abcdef", Files: []indexer.SimilarImage{{Path: "/y/c", Hash: "0123456789abcdef"}}},
	}

	payload, rows := duplicatesReportPayload(groups, similar, false)

	list, ok := payload.([]indexer.DuplicateGroup)
	if !ok {
		t.Fatalf("payload type = %T, want []indexer.DuplicateGroup", payload)
	}
	if len(list) != 1 || list[0].Hash != "abc" {
		t.Fatalf("payload = %+v, want the exact groups unchanged", list)
	}

	want := [][]string{
		{"hash", "size_bytes", "path"},
		{"abc", "10", "/x/a"},
		{"abc", "10", "/x/b"},
	}
	if len(rows) != len(want) {
		t.Fatalf("csv rows = %v, want %v", rows, want)
	}
	for i := range want {
		if strings.Join(rows[i], ",") != strings.Join(want[i], ",") {
			t.Fatalf("csv row %d = %v, want %v", i, rows[i], want[i])
		}
	}

	// A nil group slice must still marshal as [], not null.
	empty, _ := duplicatesReportPayload(nil, nil, false)
	if _, ok := empty.([]indexer.DuplicateGroup); !ok {
		t.Fatalf("empty payload type = %T, want []indexer.DuplicateGroup", empty)
	}
}

// TestDuplicatesReportPayloadWithSimilar: with --similar the report gains a
// wrapper so exact and near-duplicate findings stay distinguishable.
func TestDuplicatesReportPayloadWithSimilar(t *testing.T) {
	groups := []indexer.DuplicateGroup{
		{Hash: "abc", Size: 10, Files: []indexer.IndexedFile{{Path: "/x/a"}, {Path: "/x/b"}}},
	}
	similar := []indexer.SimilarGroup{
		{Hash: "0123456789abcdef", MaxDistance: 3, Files: []indexer.SimilarImage{
			{Path: "/y/c", Hash: "0123456789abcdef", Distance: 0},
			{Path: "/y/d", Hash: "0123456789abcdee", Distance: 3},
		}},
	}

	payload, rows := duplicatesReportPayload(groups, similar, true)
	report, ok := payload.(duplicatesReport)
	if !ok {
		t.Fatalf("payload type = %T, want duplicatesReport", payload)
	}
	if len(report.Exact) != 1 || len(report.Similar) != 1 {
		t.Fatalf("report = %+v, want 1 exact group and 1 similar group", report)
	}

	// Every row is labelled so a parser can tell the two findings apart.
	if rows[0][0] != "kind" {
		t.Fatalf("header = %v, want a leading kind column", rows[0])
	}
	if rows[1][0] != "exact" || rows[3][0] != "similar" {
		t.Fatalf("rows are not labelled by kind: %v", rows)
	}
	if rows[4][3] != "3" {
		t.Fatalf("similar row distance = %q, want 3", rows[4][3])
	}
}

func TestCsvRowsForSimilar(t *testing.T) {
	groups := []indexer.SimilarGroup{
		{Hash: "abc", MaxDistance: 4, Files: []indexer.SimilarImage{
			{Path: "/x/a", Size: 10, Hash: "abc", Distance: 0},
			{Path: "/x/b", Size: 11, Hash: "abcd", Distance: 4},
		}},
	}
	want := [][]string{
		{"phash", "max_distance", "size_bytes", "path"},
		{"abc", "0", "10", "/x/a"},
		{"abcd", "4", "11", "/x/b"},
	}
	got := csvRowsForSimilar(groups)
	if len(got) != len(want) {
		t.Fatalf("csvRowsForSimilar = %v, want %v", got, want)
	}
	for i := range want {
		if strings.Join(got[i], ",") != strings.Join(want[i], ",") {
			t.Fatalf("row %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// --- quarantine --------------------------------------------------------------

// TestQuarantineRefusesWithoutConfirmation covers both halves of "ask first":
// a bare Enter declines, and an explicit "n" declines.
func TestQuarantineRefusesWithoutConfirmation(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", "N\n", "no\n", "maybe\n"} {
		t.Run(strings.TrimSpace(answer)+"/"+answer, func(t *testing.T) {
			var out bytes.Buffer
			if confirmQuarantine(strings.NewReader(answer), &out, filepath.Join("staging"), 3) {
				t.Fatalf("answer %q must decline the quarantine", answer)
			}
			if !strings.Contains(out.String(), "Operation aborted.") {
				t.Fatalf("prompt output = %q, want an abort notice", out.String())
			}
			// The prompt must state that nothing is deleted.
			if !strings.Contains(out.String(), "Nothing is deleted") {
				t.Fatalf("prompt output = %q, want an explicit no-deletion statement", out.String())
			}
		})
	}
}

func TestQuarantineConfirmationAccepted(t *testing.T) {
	for _, answer := range []string{"y\n", "Y\n", "yes\n", "YES\n"} {
		var out bytes.Buffer
		if !confirmQuarantine(strings.NewReader(answer), &out, filepath.Join("staging"), 1) {
			t.Errorf("answer %q must accept the quarantine", answer)
		}
	}
}

// TestQuarantinePlanKeepsOneCopy: for each duplicate set exactly one copy
// survives in place and the rest are staged.
func TestQuarantinePlanKeepsOneCopy(t *testing.T) {
	dir := filepath.Join("staging")
	groups := []indexer.DuplicateGroup{
		{Hash: "abc", Files: []indexer.IndexedFile{{Path: "/x/b.txt"}, {Path: "/x/a.txt"}, {Path: "/x/c.txt"}}},
		{Hash: "def", Files: []indexer.IndexedFile{{Path: "/y/d.txt"}}}, // singleton: nothing to stage
	}

	plan := planQuarantine(dir, groups, nil, false)
	if len(plan.Ops) != 2 {
		t.Fatalf("plan has %d ops, want 2 (a.txt survives, b.txt and c.txt are staged): %+v", len(plan.Ops), plan.Ops)
	}
	// a.txt is the lowest-sorted path and therefore the survivor.
	if plan.Ops[0].OriginalSource != "/x/b.txt" || plan.Ops[1].OriginalSource != "/x/c.txt" {
		t.Fatalf("staged the wrong files: %+v", plan.Ops)
	}
	// Staged copies live under their own numbered subdirectory of the staging
	// folder so same-named files can never collide.
	if !strings.HasPrefix(plan.Ops[0].Destination, filepath.Join(dir, "exact-01")) {
		t.Fatalf("destination = %q, want a per-set folder inside the staging dir", plan.Ops[0].Destination)
	}
	if filepath.Dir(plan.Ops[0].Destination) == filepath.Dir(plan.Ops[1].Destination) {
		t.Fatalf("staged copies must not share a folder: %+v", plan.Ops)
	}

	// Near-duplicate groups are only staged when explicitly requested.
	similar := []indexer.SimilarGroup{
		{Hash: "ff", Files: []indexer.SimilarImage{{Path: "/img/a.png"}, {Path: "/img/b.png"}}},
	}
	if got := planQuarantine(dir, groups, similar, false); len(got.Ops) != 2 {
		t.Fatalf("near-duplicates must not be staged without --similar: %+v", got.Ops)
	}
	withSimilar := planQuarantine(dir, groups, similar, true)
	if len(withSimilar.Ops) != 3 {
		t.Fatalf("with --similar the image copy must be staged too: %+v", withSimilar.Ops)
	}
}

// TestQuarantineMovesFilesAndUndoRestores is the end-to-end guarantee: -y moves
// duplicates into the staging directory, nothing is deleted, and the undo
// history records enough to put every file back.
func TestQuarantineMovesFilesAndUndoRestores(t *testing.T) {
	src := t.TempDir()
	staging := filepath.Join(t.TempDir(), "staging")
	histFile := filepath.Join(t.TempDir(), "history.json")
	hist := history.NewManager(histFile)

	content := []byte("identical bytes across every copy")
	paths := []string{
		filepath.Join(src, "keep.txt"),
		filepath.Join(src, "copy1.txt"),
		filepath.Join(src, "copy2.txt"),
	}
	for _, p := range paths {
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	groups := []indexer.DuplicateGroup{{
		Hash: "deadbeef",
		Size: int64(len(content)),
		Files: []indexer.IndexedFile{
			{Path: paths[0]}, {Path: paths[1]}, {Path: paths[2]},
		},
	}}

	plan := planQuarantine(staging, groups, nil, false)
	if len(plan.Ops) != 2 {
		t.Fatalf("expected 2 files to stage, got %d", len(plan.Ops))
	}

	entry, moved, errs, err := executeQuarantine(hist, staging, plan)
	if err != nil {
		t.Fatalf("executeQuarantine: %v", err)
	}
	if moved != 2 || len(errs) != 0 {
		t.Fatalf("moved=%d errs=%v, want 2 moved and no errors", moved, errs)
	}
	if entry == nil || entry.ID == 0 {
		t.Fatalf("quarantine must be recorded in history, got %+v", entry)
	}

	// One copy stays put; the other two are staged, not deleted. The survivor
	// is the first path in sorted order, so the choice is deterministic.
	remain, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read source dir: %v", err)
	}
	if len(remain) != 1 {
		t.Fatalf("source folder holds %d file(s), want exactly 1 survivor", len(remain))
	}
	survivor := filepath.Join(src, remain[0].Name())
	if survivor != paths[1] {
		t.Fatalf("survivor = %q, want the lowest-sorted path %q", survivor, paths[1])
	}
	for _, staged := range []string{paths[0], paths[2]} {
		if _, err := os.Stat(staged); !os.IsNotExist(err) {
			t.Fatalf("%s should have been moved out of the source folder", staged)
		}
	}
	stagedCount := 0
	_ = filepath.Walk(staging, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			stagedCount++
		}
		return nil
	})
	if stagedCount != 2 {
		t.Fatalf("found %d staged file(s), want 2 — nothing may be deleted", stagedCount)
	}

	// History records every move with enough detail to reverse it.
	entries, err := hist.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || len(entries[0].Operations) != 2 {
		t.Fatalf("history = %+v, want a single entry with 2 operations", entries)
	}
	for _, op := range entries[0].Operations {
		if op.OriginalSource == "" || op.Destination == "" {
			t.Fatalf("history operation is missing a path: %+v", op)
		}
		if _, err := os.Stat(op.Destination); err != nil {
			t.Fatalf("recorded destination %s is missing: %v", op.Destination, err)
		}
	}

	// Undo puts everything back.
	undone, restored, err := hist.Undo(entry.ID)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if restored != 2 {
		t.Fatalf("restored %d file(s), want 2", restored)
	}
	if undone.Status != "Undone" {
		t.Fatalf("entry status = %q, want Undone", undone.Status)
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("undo must restore %s: %v", p, err)
		}
		if !bytes.Equal(data, content) {
			t.Fatalf("restored %s has wrong contents: %q", p, data)
		}
	}
}

// TestQuarantineNeverOverwritesStagedFile: a name already present in the
// staging directory must be kept, not clobbered by a new move.
func TestQuarantineNeverOverwritesStagedFile(t *testing.T) {
	src := t.TempDir()
	staging := t.TempDir()
	hist := history.NewManager(filepath.Join(t.TempDir(), "history.json"))

	existing := filepath.Join(staging, "occupant.txt")
	if err := os.WriteFile(existing, []byte("already here"), 0o644); err != nil {
		t.Fatalf("seed staging file: %v", err)
	}

	victim := filepath.Join(src, "victim.txt")
	if err := os.WriteFile(victim, []byte("new content"), 0o644); err != nil {
		t.Fatalf("write victim: %v", err)
	}

	plan := quarantinePlan{Ops: []models.HistoryOperation{
		{OriginalSource: victim, Destination: existing, Reason: "duplicate copy"},
	}}
	entry, moved, _, err := executeQuarantine(hist, staging, plan)
	if err != nil {
		t.Fatalf("executeQuarantine: %v", err)
	}
	if moved != 1 {
		t.Fatalf("moved = %d, want 1", moved)
	}

	kept, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("pre-existing staged file was removed: %v", err)
	}
	if string(kept) != "already here" {
		t.Fatalf("pre-existing staged file was overwritten: %q", kept)
	}
	if entry.Operations[0].Destination == existing {
		t.Fatalf("history must record the collision-resolved destination, got %q", entry.Operations[0].Destination)
	}
}

// TestQuarantineRequiresDirectoryOutsideScanRoot: staging inside the scanned
// tree would make the staged copies duplicates on the next run.
func TestPathInsideDir(t *testing.T) {
	root := filepath.Join("C:", "photos")
	if !pathInsideDir(root, root) {
		t.Error("a directory contains itself")
	}
	if !pathInsideDir(root, filepath.Join(root, "staging")) {
		t.Error("a subdirectory is inside the root")
	}
	if !pathInsideDir(root, filepath.Join(root, "a", "b")) {
		t.Error("a nested subdirectory is inside the root")
	}
	if pathInsideDir(root, filepath.Join("C:", "photos-archive")) {
		t.Error("a sibling sharing a name prefix is not inside the root")
	}
	if pathInsideDir("", root) {
		t.Error("an empty root contains nothing")
	}
}

// TestQuarantineRunNeedsHistory: undo is a hard requirement of quarantine, so
// an unusable history manager must stop the run before any file moves.
func TestQuarantinePlanIsEmptyForNoDuplicates(t *testing.T) {
	plan := planQuarantine(filepath.Join("staging"), nil, nil, true)
	if len(plan.Ops) != 0 {
		t.Fatalf("empty inputs must produce no operations, got %+v", plan.Ops)
	}
}
