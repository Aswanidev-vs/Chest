package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/models"
)

// movedFile creates a file at dst (inside dir), standing in for a file CHEST
// has just organized there, and returns the original and destination paths of
// the move that would have put it there.
func movedFile(t *testing.T, dir, folder, name, content string) (string, string) {
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

func mustExist(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s: %s should exist: %v", why, path, err)
	}
}

func mustNotExist(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s: %s should not exist (stat err=%v)", why, path, err)
	}
}

// TestCheckpointUndoToRoundTrip is the headline case: take a checkpoint, sort
// two runs' worth of files, then undo --to and get every file back exactly
// where it started, with nothing left behind at the destinations.
func TestCheckpointUndoToRoundTrip(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	// A run before the checkpoint: it must survive `undo --to` untouched.
	oldSrc, oldDst := movedFile(t, dir, "Docs", "old.txt", "old")
	if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: oldSrc, Destination: oldDst, Reason: "test"}}); err != nil {
		t.Fatalf("record before checkpoint: %v", err)
	}

	cp, err := mgr.AddCheckpoint("before-import")
	if err != nil {
		t.Fatalf("add checkpoint: %v", err)
	}
	if cp.Name != "before-import" {
		t.Errorf("checkpoint name = %q, want %q", cp.Name, "before-import")
	}
	if cp.EntryID != 1 {
		t.Errorf("checkpoint entry id = %d, want 1", cp.EntryID)
	}
	if cp.ID != 1 {
		t.Errorf("checkpoint id = %d, want 1", cp.ID)
	}

	// Two runs after the checkpoint, in different folders.
	srcA, dstA := movedFile(t, dir, "Images", "a.png", "a")
	srcB, dstB := movedFile(t, dir, "Videos", "b.mp4", "b")
	first, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: srcA, Destination: dstA, Reason: "test"}})
	if err != nil {
		t.Fatalf("record run 1: %v", err)
	}
	srcC, dstC := movedFile(t, dir, "Docs", "c.pdf", "c")
	second, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: srcB, Destination: dstB, Reason: "test"}, {OriginalSource: srcC, Destination: dstC, Reason: "test"}})
	if err != nil {
		t.Fatalf("record run 2: %v", err)
	}

	res, err := mgr.UndoToCheckpoint("before-import")
	if err != nil {
		t.Fatalf("undo --to: %v", err)
	}
	if res.Files != 3 {
		t.Errorf("restored files = %d, want 3", res.Files)
	}
	if len(res.UndoneIDs) != 2 || res.UndoneIDs[0] != first.ID || res.UndoneIDs[1] != second.ID {
		t.Errorf("undone ids = %v, want [%d %d]", res.UndoneIDs, first.ID, second.ID)
	}

	for _, p := range []struct{ src, dst string }{
		{srcA, dstA}, {srcB, dstB}, {srcC, dstC},
	} {
		mustExist(t, p.src, "restored file")
		mustNotExist(t, p.dst, "destination must be empty after undo")
	}

	// The file from before the checkpoint is untouched by the revert.
	mustNotExist(t, oldSrc, "pre-checkpoint file must stay organized")
	mustExist(t, oldDst, "pre-checkpoint file must stay organized")

	entries, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries retained, got %d", len(entries))
	}
	if entries[0].Status != "Complete" {
		t.Errorf("entry before the checkpoint = %s, want Complete", entries[0].Status)
	}
	for _, e := range entries[1:] {
		if e.Status != "Undone" {
			t.Errorf("entry #%d = %s, want Undone", e.ID, e.Status)
		}
	}
}

// TestUndoToCheckpointRefusesAndMovesNothing is the safety case: one file of
// the batch cannot be restored, so the whole batch must be refused and every
// other file left exactly where it is.
func TestUndoToCheckpointRefusesAndMovesNothing(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	cp, err := mgr.AddCheckpoint("start")
	if err != nil {
		t.Fatal(err)
	}
	_ = cp

	goodSrc, goodDst := movedFile(t, dir, "Docs", "good.txt", "good")
	if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: goodSrc, Destination: goodDst, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}

	// The second run's second file has since been deleted: the batch is no
	// longer fully restorable.
	vanishSrc, vanishDst := movedFile(t, dir, "Videos", "vanish.mp4", "gone")
	keptSrc, keptDst := movedFile(t, dir, "Docs", "kept.txt", "kept")
	if _, err := mgr.Record(dir, []models.HistoryOperation{
		{OriginalSource: vanishSrc, Destination: vanishDst, Reason: "test"},
		{OriginalSource: keptSrc, Destination: keptDst, Reason: "test"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(vanishDst); err != nil {
		t.Fatal(err)
	}

	res, err := mgr.UndoToCheckpoint("start")
	if err == nil {
		t.Fatalf("expected the batch to be refused, got result %+v", res)
	}
	if !strings.Contains(err.Error(), vanishDst) || !strings.Contains(err.Error(), "no longer exists at destination") {
		t.Errorf("refusal should name the un-restorable file and why, got: %v", err)
	}
	if !strings.Contains(err.Error(), "start") {
		t.Errorf("refusal should name the checkpoint, got: %v", err)
	}

	// Nothing moved: the healthy files of both runs are still organized.
	mustNotExist(t, goodSrc, "refused batch must not restore anything")
	mustExist(t, goodDst, "refused batch must not touch healthy files")
	mustNotExist(t, keptSrc, "refused batch must not touch healthy files")
	mustExist(t, keptDst, "refused batch must not touch healthy files")

	entries, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Status != "Complete" {
			t.Errorf("entry #%d = %s, want Complete after a refused batch", e.ID, e.Status)
		}
	}
}

// TestUndoToCheckpointRefusesOnOccupiedSource covers the second refusal
// reason: something new now sits where a file would be put back.
func TestUndoToCheckpointRefusesOnOccupiedSource(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	if _, err := mgr.AddCheckpoint("start"); err != nil {
		t.Fatal(err)
	}
	src, dst := movedFile(t, dir, "Docs", "clash.txt", "organized")
	if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}
	// A new file of the same name appeared at the original location.
	if err := os.WriteFile(src, []byte("new file"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.UndoToCheckpoint("start")
	if err == nil {
		t.Fatal("expected a refusal when the original location is occupied")
	}
	if !strings.Contains(err.Error(), "already occupied") {
		t.Errorf("error should say the location is occupied, got: %v", err)
	}
	mustExist(t, dst, "refused batch must leave the organized file alone")
	if data, _ := os.ReadFile(src); string(data) != "new file" {
		t.Errorf("the occupying file must be untouched, got %q", data)
	}
}

// TestUndoToCheckpointRefusesChainedMove covers a file that was moved twice:
// both operations cannot be reverted in one batch because the second one's
// source is the first one's destination.
func TestUndoToCheckpointRefusesChainedMove(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	if _, err := mgr.AddCheckpoint("start"); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(dir, "chain.txt")
	mid := filepath.Join(dir, "Docs", "chain.txt")
	end := filepath.Join(dir, "Archive", "chain.txt")
	for _, p := range []string{mid, end} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(end, []byte("chain"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: mid, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: mid, Destination: end, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.UndoToCheckpoint("start")
	if err == nil {
		t.Fatal("expected a refusal for a chained move")
	}
	if !strings.Contains(err.Error(), "cannot be reverted as one batch") {
		t.Errorf("error should explain the chain, got: %v", err)
	}
	if !strings.Contains(err.Error(), mid) {
		t.Errorf("error should name the contested path, got: %v", err)
	}
	mustExist(t, end, "refused batch must leave the file where it is")
	mustNotExist(t, mid, "refused batch must not restore into the middle location")
	mustNotExist(t, src, "refused batch must not restore to the original location")
}

// TestUndoToCheckpointErrors checks the two "nothing to do" refusals and the
// unknown-label one.
func TestUndoToCheckpointErrors(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	if _, err := mgr.UndoToCheckpoint("nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown checkpoint error = %v, want 'not found'", err)
	}

	if _, err := mgr.AddCheckpoint("fresh"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.UndoToCheckpoint("fresh"); err == nil || !strings.Contains(err.Error(), "no operations recorded after") {
		t.Errorf("empty batch error = %v, want 'no operations recorded after'", err)
	}

	src, dst := movedFile(t, dir, "Docs", "x.txt", "x")
	if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.UndoToCheckpoint("fresh"); err != nil {
		t.Fatalf("first undo: %v", err)
	}
	if _, err := mgr.UndoToCheckpoint("fresh"); err == nil || !strings.Contains(err.Error(), "already undone") {
		t.Errorf("repeat undo error = %v, want 'already undone'", err)
	}
}

// TestUndoToCheckpointSkipsAlreadyUndoneEntries checks that a run undone on
// its own does not block a later revert of everything after the checkpoint.
func TestUndoToCheckpointSkipsAlreadyUndoneEntries(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	if _, err := mgr.AddCheckpoint("start"); err != nil {
		t.Fatal(err)
	}
	srcA, dstA := movedFile(t, dir, "Docs", "a.txt", "a")
	first, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: srcA, Destination: dstA, Reason: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	srcB, dstB := movedFile(t, dir, "Docs", "b.txt", "b")
	second, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: srcB, Destination: dstB, Reason: "test"}})
	if err != nil {
		t.Fatal(err)
	}

	// Undo the first run on its own.
	if _, _, err := mgr.Undo(first.ID); err != nil {
		t.Fatalf("single undo: %v", err)
	}

	res, err := mgr.UndoToCheckpoint("start")
	if err != nil {
		t.Fatalf("undo --to: %v", err)
	}
	if len(res.UndoneIDs) != 1 || res.UndoneIDs[0] != second.ID {
		t.Errorf("undone ids = %v, want only [%d]", res.UndoneIDs, second.ID)
	}
	if len(res.AlreadyUndone) != 1 || res.AlreadyUndone[0] != first.ID {
		t.Errorf("already undone = %v, want [%d]", res.AlreadyUndone, first.ID)
	}
	mustExist(t, srcA, "the earlier undo already restored this")
	mustExist(t, srcB, "the batch restored this")
}

// TestUndoToCheckpointUsesNewestMarkerForAReusedLabel documents that undoing
// to a reused label targets the most recent marker.
func TestUndoToCheckpointUsesNewestMarkerForAReusedLabel(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	if _, err := mgr.AddCheckpoint("nightly"); err != nil {
		t.Fatal(err)
	}
	srcA, dstA := movedFile(t, dir, "Docs", "a.txt", "a")
	if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: srcA, Destination: dstA, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddCheckpoint("nightly"); err != nil {
		t.Fatal(err)
	}
	srcB, dstB := movedFile(t, dir, "Docs", "b.txt", "b")
	if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: srcB, Destination: dstB, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}

	res, err := mgr.UndoToCheckpoint("nightly")
	if err != nil {
		t.Fatalf("undo --to: %v", err)
	}
	if len(res.UndoneIDs) != 1 {
		t.Fatalf("expected only the newest marker to select a batch, got %v", res.UndoneIDs)
	}
	if res.EntryID != 1 {
		t.Errorf("selected anchor entry = #%d, want #1 (the newest marker's anchor)", res.EntryID)
	}
	mustExist(t, srcB, "the newest marker selects the newest run")
	mustNotExist(t, dstB, "the newest run is reverted")
	mustNotExist(t, srcA, "the earlier run stays organized")
	mustExist(t, dstA, "the earlier run stays organized")
}

// TestCheckpointIsPurelyAdditive proves recording a marker moves nothing.
func TestCheckpointIsPurelyAdditive(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	src, dst := movedFile(t, dir, "Docs", "a.txt", "a")
	if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}
	before, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.AddCheckpoint("marker"); err != nil {
		t.Fatal(err)
	}

	mustNotExist(t, src, "a checkpoint must not move files")
	mustExist(t, dst, "a checkpoint must not move files")
	after, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("a checkpoint added %d history entries, want 0", len(after)-len(before))
	}
}

// TestAddCheckpointRejectsEmptyName covers the label validation.
func TestAddCheckpointRejectsEmptyName(t *testing.T) {
	mgr := NewManager(filepath.Join(t.TempDir(), "history.json"))
	for _, name := range []string{"", "   ", "\t\n"} {
		if _, err := mgr.AddCheckpoint(name); err == nil || !strings.Contains(err.Error(), "must not be empty") {
			t.Errorf("AddCheckpoint(%q) error = %v, want 'must not be empty'", name, err)
		}
	}
	if got, _ := mgr.AddCheckpoint("  padded  "); got.Name != "padded" {
		t.Errorf("label was not trimmed: %q", got.Name)
	}
}

// TestCheckpointIDsAreUniqueAndIndependentOfEntries proves a marker ID can
// never be read as a history entry ID.
func TestCheckpointIDsAreUniqueAndIndependentOfEntries(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	// Interleaved so each marker anchors to a different entry, and long enough
	// that the entry sequence has run past the marker's own.
	for i := 0; i < 3; i++ {
		src, dst := movedFile(t, dir, "Docs", string(rune('a'+i))+".txt", "x")
		if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mgr.AddCheckpoint("marker"); err != nil {
		t.Fatal(err)
	}
	for i := 3; i < 6; i++ {
		src, dst := movedFile(t, dir, "Docs", string(rune('a'+i))+".txt", "x")
		if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mgr.AddCheckpoint("marker"); err != nil {
		t.Fatal(err)
	}

	entries, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := mgr.ListCheckpoints()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 6 || len(checkpoints) != 2 {
		t.Fatalf("entries=%d checkpoints=%d, want 6 and 2", len(entries), len(checkpoints))
	}

	entryIDs := map[int]bool{}
	for _, e := range entries {
		entryIDs[e.ID] = true
	}
	seen := map[int]bool{}
	for _, c := range checkpoints {
		if seen[c.ID] {
			t.Errorf("duplicate checkpoint ID %d", c.ID)
		}
		seen[c.ID] = true
	}
	if len(seen) != len(checkpoints) {
		t.Errorf("checkpoint IDs are not unique: %v", seen)
	}
	if len(entryIDs) != 6 {
		t.Errorf("expected 6 distinct entry IDs, got %v", entryIDs)
	}
	// The marker ID runs on its own sequence, so it never inherits the entry
	// counter and `undo --to <id>` can never be confused with a marker.
	if checkpoints[0].ID != 1 || checkpoints[1].ID != 2 {
		t.Errorf("checkpoint IDs = [%d %d], want [1 2] on their own sequence", checkpoints[0].ID, checkpoints[1].ID)
	}
	if checkpoints[0].EntryID != 3 || checkpoints[1].EntryID != 6 {
		t.Errorf("anchors = [%d %d], want [3 6]", checkpoints[0].EntryID, checkpoints[1].EntryID)
	}
}

// TestListCheckpointsIsOldestFirst pins the documented ordering.
func TestListCheckpointsIsOldestFirst(t *testing.T) {
	mgr := NewManager(filepath.Join(t.TempDir(), "history.json"))
	for _, name := range []string{"first", "second", "third"} {
		if _, err := mgr.AddCheckpoint(name); err != nil {
			t.Fatal(err)
		}
	}
	got, err := mgr.ListCheckpoints()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("got %d checkpoints, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("checkpoint %d = %q, want %q", i, got[i].Name, want[i])
		}
	}
}

// TestUndoToCheckpointPrunedHistoryStillResolves proves that pruning entries
// does not strand a checkpoint: the marker still selects "everything after
// entry N" from whatever entries survive.
func TestUndoToCheckpointPrunedHistoryStillResolves(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	if _, err := mgr.AddCheckpoint("start"); err != nil {
		t.Fatal(err)
	}
	src, dst := movedFile(t, dir, "Docs", "a.txt", "a")
	if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.Prune(1); err != nil {
		t.Fatalf("prune: %v", err)
	}
	checkpoints, err := mgr.ListCheckpoints()
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 1 {
		t.Fatalf("pruning dropped the checkpoint, leaving %d", len(checkpoints))
	}

	if _, err := mgr.UndoToCheckpoint("start"); err != nil {
		t.Fatalf("undo --to after prune: %v", err)
	}
	mustExist(t, src, "restored after pruning")
}

// TestPruneKeepsExactlyTheNewestN is the rotation case.
func TestPruneKeepsExactlyTheNewestN(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	const total = 5
	for i := 0; i < total; i++ {
		name := string(rune('a' + i))
		src := filepath.Join(dir, name+".txt")
		dst := filepath.Join(dir, "Docs", name+".txt")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := mgr.Prune(2)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 3 {
		t.Errorf("removed = %d, want 3", removed)
	}

	entries, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("kept %d entries, want 2", len(entries))
	}
	if entries[0].ID != 4 || entries[1].ID != 5 {
		t.Errorf("kept entries %d and %d, want the newest (4 and 5)", entries[0].ID, entries[1].ID)
	}
	// Pruned IDs are never handed out again.
	src, dst := filepath.Join(dir, "f.txt"), filepath.Join(dir, "Docs", "f.txt")
	next, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != 6 {
		t.Errorf("next entry ID = %d, want 6 (IDs must not be reused)", next.ID)
	}
}

// TestPruneNeverRemovesTheNewestEntry checks the floor: keeping fewer entries
// than exist still leaves the newest, and keeping more removes nothing.
func TestPruneNeverRemovesTheNewestEntry(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	src := filepath.Join(dir, "a.txt")
	dst := filepath.Join(dir, "Docs", "a.txt")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatal(err)
		}
	}

	if removed, err := mgr.Prune(1); err != nil || removed != 2 {
		t.Errorf("Prune(1) = %d, %v; want 2, nil", removed, err)
	}
	entries, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != 3 {
		t.Fatalf("entries after Prune(1) = %+v, want only the newest (#3)", entries)
	}

	if removed, err := mgr.Prune(100); err != nil || removed != 0 {
		t.Errorf("Prune(100) = %d, %v; want 0, nil", removed, err)
	}
	if _, err := mgr.Prune(0); err == nil {
		t.Error("Prune(0) must be refused: it would drop the newest entry too")
	}
	if _, err := mgr.Prune(-3); err == nil {
		t.Error("Prune(-3) must be refused")
	}
}

// TestMaxEntriesCapsWhatRecordStores proves the cap is enforced on every
// record, so the file cannot grow without bound.
func TestMaxEntriesCapsWhatRecordStores(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	mgr := NewManager(path)
	if removed, err := mgr.SetMaxEntries(3); err != nil || removed != 0 {
		t.Fatalf("SetMaxEntries(3) = %d, %v; want 0, nil", removed, err)
	}
	if got := mgr.MaxEntries(); got != 3 {
		t.Errorf("MaxEntries() = %d, want 3", got)
	}
	if _, err := mgr.SetMaxEntries(0); err == nil {
		t.Error("SetMaxEntries(0) must be refused")
	}

	for i := 0; i < 8; i++ {
		name := string(rune('a' + i))
		src := filepath.Join(dir, name+".txt")
		dst := filepath.Join(dir, "Docs", name+".txt")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	entries, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("stored %d entries, want the capped 3", len(entries))
	}
	if entries[2].ID != 8 {
		t.Errorf("newest stored entry = #%d, want #8 (the newest is never pruned)", entries[2].ID)
	}

	// The cap is stored with the history it bounds, so a fresh manager — the
	// situation of the next chest run — enforces it without being told.
	other := NewManager(path)
	for i := 0; i < 4; i++ {
		src := filepath.Join(dir, "x"+string(rune('a'+i))+".txt")
		dst := filepath.Join(dir, "Docs", "x"+string(rune('a'+i))+".txt")
		if _, err := other.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err = other.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("a new manager recorded past the stored cap: %d entries", len(entries))
	}
	if entries[2].ID != 12 {
		t.Errorf("newest entry = #%d, want #12", entries[2].ID)
	}
}

// TestSetMaxEntriesPrunesImmediately covers the one-shot prune a lower cap
// implies.
func TestSetMaxEntriesPrunesImmediately(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	for i := 0; i < 5; i++ {
		src := filepath.Join(dir, "f"+string(rune('a'+i))+".txt")
		dst := filepath.Join(dir, "Docs", "f"+string(rune('a'+i))+".txt")
		if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := mgr.SetMaxEntries(2)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Errorf("removed = %d, want 3", removed)
	}
	entries, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[1].ID != 5 {
		t.Errorf("entries = %+v, want the newest two ending at #5", entries)
	}
}

// TestNoMaxEntriesKeepsEverything documents that the default is unlimited, so
// existing installs see no change at all.
func TestNoMaxEntriesKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	for i := 0; i < 5; i++ {
		src := filepath.Join(dir, "f"+string(rune('a'+i))+".txt")
		dst := filepath.Join(dir, "Docs", "f"+string(rune('a'+i))+".txt")
		if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := mgr.MaxEntries(); got != 0 {
		t.Errorf("default MaxEntries = %d, want 0 (unlimited)", got)
	}
	entries, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Errorf("kept %d of 5 entries without a cap", len(entries))
	}
}

// TestLoadLegacyHistoryFileWithoutCheckpoints is the backward-compatibility
// guarantee: a history.json written before checkpoints existed has no
// "checkpoints" and no "max_entries" key, and must load unchanged.
func TestLoadLegacyHistoryFileWithoutCheckpoints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	// Byte-for-byte the shape an older CHEST wrote: only last_id and entries.
	legacy := `{
  "last_id": 2,
  "entries": [
    {
      "id": 1,
      "timestamp": "2026-01-02T15:04:00Z",
      "directory": "` + filepath.ToSlash(dir) + `",
      "operations": [
        {
          "original_source": "` + filepath.ToSlash(filepath.Join(dir, "a.txt")) + `",
          "destination": "` + filepath.ToSlash(filepath.Join(dir, "Docs", "a.txt")) + `",
          "reason": "extension"
        }
      ],
      "files_count": 1,
      "status": "Complete"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(path)
	entries, err := mgr.List()
	if err != nil {
		t.Fatalf("legacy file must load: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != 1 || entries[0].Status != "Complete" {
		t.Fatalf("legacy entries = %+v, want the single recorded entry", entries)
	}

	checkpoints, err := mgr.ListCheckpoints()
	if err != nil {
		t.Fatalf("legacy checkpoints: %v", err)
	}
	if len(checkpoints) != 0 {
		t.Errorf("legacy file reported %d checkpoints, want 0", len(checkpoints))
	}

	// Reading it must not rewrite the file into a new shape either.
	if _, _, err := mgr.Undo(1); err == nil {
		t.Fatal("undo expected to fail here (no real files on disk)")
	}

	// A checkpoint can be added to the legacy store, and it round-trips.
	cp, err := mgr.AddCheckpoint("on-legacy")
	if err != nil {
		t.Fatalf("add checkpoint to legacy store: %v", err)
	}
	if cp.EntryID != 2 {
		t.Errorf("checkpoint anchored to entry #%d, want #2 (LastID of the legacy file)", cp.EntryID)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Checkpoints []struct {
			Name    string    `json:"name"`
			ID      int       `json:"id"`
			EntryID int       `json:"entry_id"`
			Stamp   time.Time `json:"timestamp"`
		} `json:"checkpoints"`
		MaxEntries int `json:"max_entries"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("rewritten file is not valid JSON: %v", err)
	}
	if len(probe.Checkpoints) != 1 || probe.Checkpoints[0].Name != "on-legacy" {
		t.Errorf("checkpoints in rewritten file = %+v, want the one just added", probe.Checkpoints)
	}
	if probe.MaxEntries != 0 {
		t.Errorf("max_entries = %d, want 0 (absent means unlimited)", probe.MaxEntries)
	}
}

// TestCheckpointKeyIsOmittedWhenEmpty proves an untouched legacy store is
// still written without the new keys.
func TestCheckpointKeyIsOmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	if _, err := mgr.Record(dir, []models.HistoryOperation{
		{OriginalSource: filepath.Join(dir, "a.txt"), Destination: filepath.Join(dir, "Docs", "a.txt"), Reason: "test"},
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "checkpoints") {
		t.Errorf("a store with no checkpoints must not carry the key:\n%s", data)
	}
	if strings.Contains(string(data), "max_entries") {
		t.Errorf("a store with no cap must not carry the key:\n%s", data)
	}
}

// TestUndoToCheckpointUnderConcurrency exercises the manager from several
// goroutines; run with -race it proves the new code shares the lock correctly.
func TestUndoToCheckpointUnderConcurrency(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "history.json"))

	if _, err := mgr.AddCheckpoint("start"); err != nil {
		t.Fatal(err)
	}
	src, dst := movedFile(t, dir, "Docs", "a.txt", "a")
	if _, err := mgr.Record(dir, []models.HistoryOperation{{OriginalSource: src, Destination: dst, Reason: "test"}}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 4 {
			case 0:
				_, _ = mgr.List()
			case 1:
				_, _ = mgr.ListCheckpoints()
			case 2:
				_, _ = mgr.AddCheckpoint("concurrent")
			case 3:
				_, _ = mgr.Prune(5)
			}
		}(i)
	}
	wg.Wait()

	// Exactly one goroutine can win the revert; the rest must report it as
	// already undone rather than moving anything twice.
	if _, err := mgr.UndoToCheckpoint("start"); err != nil {
		t.Fatalf("undo --to: %v", err)
	}
	mustExist(t, src, "restored exactly once")
	if _, err := mgr.UndoToCheckpoint("start"); err == nil {
		t.Error("a second undo --to must refuse rather than move files again")
	}
	mustExist(t, src, "still restored")
}
