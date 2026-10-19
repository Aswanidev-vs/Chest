package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// treeFixture writes a fixed tree whose sizes make the rolled-up totals
// checkable by hand:
//
//	a.txt        5 bytes
//	big.bin    100 bytes
//	docs/       30 bytes   (readme.md 10 + sub/deep.txt 20)
//	skipme/      1 byte
//	empty/       0 bytes
func treeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]int{
		"a.txt":             5,
		"big.bin":           100,
		"docs/readme.md":    10,
		"docs/sub/deep.txt": 20,
		"skipme/x.txt":      1,
	}
	for rel, size := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, make([]byte, size), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// treeJSON runs `chest tree --json` and decodes the report.
func treeJSON(t *testing.T, root string, args ...string) *treeReport {
	t.Helper()
	stdout, stderr, err := cliRun(t, newTreeCmd(), append([]string{root, "--json"}, args...)...)
	if err != nil {
		t.Fatalf("chest tree --json %v: %v\n%s", args, err, stderr)
	}
	var rep treeReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, stdout)
	}
	return &rep
}

func TestTreeRespectsDepth(t *testing.T) {
	root := treeFixture(t)

	// depth 0 stops at the root: its own files are counted and its directories
	// are named, but nothing below it is read.
	deep0 := treeJSON(t, root, "--depth", "0")
	if len(deep0.Node.Children) != 5 {
		t.Errorf("depth 0 rendered %d children, want 5 (the root's own entries)", len(deep0.Node.Children))
	}
	for _, c := range deep0.Node.Children {
		if !c.IsFile && (c.Expanded || len(c.Children) > 0) {
			t.Errorf("depth 0 expanded %q, want it left unread", c.Name)
		}
	}
	if deep0.TotalBytes != 105 {
		t.Errorf("depth 0 total = %d, want 105 (the root's own files)", deep0.TotalBytes)
	}
	if deep0.NotExpandedDirs != 3 {
		t.Errorf("depth 0 NotExpandedDirs = %d, want 3", deep0.NotExpandedDirs)
	}
	if deep0.Note == "" {
		t.Error("a truncated total must carry a note saying so")
	}

	// depth 1 expands the top level. docs contributes its own readme.md but not
	// the contents of docs/sub, which sits below the limit.
	deep1 := treeJSON(t, root, "--depth", "1")
	if len(deep1.Node.Children) != 5 {
		t.Errorf("depth 1 rendered %d children, want 5", len(deep1.Node.Children))
	}
	if deep1.TotalBytes != 116 {
		t.Errorf("depth 1 total = %d, want 116 (docs/sub left unread)", deep1.TotalBytes)
	}
	if deep1.NotExpandedDirs != 1 {
		t.Errorf("depth 1 NotExpandedDirs = %d, want 1 (docs/sub)", deep1.NotExpandedDirs)
	}

	// depth 2 reaches the deepest level this fixture has: docs/sub is a
	// two-level-deep directory, so nothing is left unread.
	deep2 := treeJSON(t, root, "--depth", "2")
	if !hasPath(deep2.Node, filepath.Join(root, "docs", "sub", "deep.txt")) {
		t.Error("depth 2 did not reach docs/sub/deep.txt")
	}
	if deep2.NotExpandedDirs != 0 {
		t.Errorf("depth 2 NotExpandedDirs = %d, want 0", deep2.NotExpandedDirs)
	}
	if deep2.TotalBytes != 136 {
		t.Errorf("depth 2 total = %d, want 136", deep2.TotalBytes)
	}

	// depth 3 reaches the deepest file in the fixture.
	deep3 := treeJSON(t, root, "--depth", "3")
	if deep3.NotExpandedDirs != 0 {
		t.Errorf("depth 3 NotExpandedDirs = %d, want 0", deep3.NotExpandedDirs)
	}
	if deep3.TotalBytes != 136 {
		t.Errorf("depth 3 total = %d, want 136", deep3.TotalBytes)
	}
	if !hasPath(deep3.Node, filepath.Join(root, "docs", "sub", "deep.txt")) {
		t.Error("depth 3 did not reach docs/sub/deep.txt")
	}
}

func hasPath(n *treeNode, path string) bool {
	if n.Path == path {
		return true
	}
	for _, c := range n.Children {
		if hasPath(c, path) {
			return true
		}
	}
	return false
}

func TestTreeRolledUpSizes(t *testing.T) {
	root := treeFixture(t)
	rep := treeJSON(t, root, "--depth", "3")

	docs := findNode(t, rep.Node, "docs")
	if docs.Size != 30 {
		t.Errorf("docs size = %d, want 30 (its own 10 plus sub's 20)", docs.Size)
	}
	if docs.Files != 2 {
		t.Errorf("docs files = %d, want 2", docs.Files)
	}
	if rep.FilesRead != 5 {
		t.Errorf("FilesRead = %d, want 5", rep.FilesRead)
	}
	if rep.DirsRead != 4 {
		t.Errorf("DirsRead = %d, want 4 (docs, docs/sub, skipme, empty)", rep.DirsRead)
	}
	empty := findNode(t, rep.Node, "empty")
	if empty.Size != 0 || empty.Files != 0 {
		t.Errorf("empty = %+v, want zero size and zero files", empty)
	}
	if !empty.Expanded {
		t.Error("empty folder is not marked expanded; an empty folder and an unread one are different facts")
	}
}

// Size order puts the heaviest first; name order is alphabetical. Both fall
// back to a full compare so equal entries cannot reorder between runs.
func TestTreeSortOrders(t *testing.T) {
	root := treeFixture(t)

	bySize := treeJSON(t, root, "--depth", "1")
	var got []string
	for _, c := range bySize.Node.Children {
		got = append(got, c.Name)
	}
	want := []string{"big.bin", "docs", "a.txt", "skipme", "empty"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("size order = %v, want %v", got, want)
	}

	byName := treeJSON(t, root, "--depth", "1", "--sort", "name")
	got = got[:0]
	for _, c := range byName.Node.Children {
		got = append(got, c.Name)
	}
	want = []string{"a.txt", "big.bin", "docs", "empty", "skipme"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("name order = %v, want %v", got, want)
	}
}

func findNode(t *testing.T, n *treeNode, name string) *treeNode {
	t.Helper()
	if n.Name == name {
		return n
	}
	for _, c := range n.Children {
		if found := findNodeQuiet(c, name); found != nil {
			return found
		}
	}
	t.Fatalf("node %q not found under %q", name, n.Name)
	return nil
}

func findNodeQuiet(n *treeNode, name string) *treeNode {
	if n.Name == name {
		return n
	}
	for _, c := range n.Children {
		if found := findNodeQuiet(c, name); found != nil {
			return found
		}
	}
	return nil
}

func TestTreeExcludeDropsEntriesAndCountsThem(t *testing.T) {
	root := treeFixture(t)

	rep := treeJSON(t, root, "--depth", "3", "-x", "skipme")
	if findNodeQuiet(rep.Node, "skipme") != nil {
		t.Error("skipme survived -x")
	}
	if rep.Excluded != 1 {
		t.Errorf("Excluded = %d, want 1", rep.Excluded)
	}
	if rep.TotalBytes != 135 {
		t.Errorf("total = %d, want 135 (the excluded file is not counted)", rep.TotalBytes)
	}
}

// The same tree must draw the same tree twice. Anything that leaked map order or
// wall-clock state into the renderer would show up here.
func TestTreeOutputIsDeterministic(t *testing.T) {
	root := treeFixture(t)

	first, _, err := cliRun(t, newTreeCmd(), root, "--depth", "3")
	if err != nil {
		t.Fatalf("chest tree: %v", err)
	}
	second, _, err := cliRun(t, newTreeCmd(), root, "--depth", "3")
	if err != nil {
		t.Fatalf("chest tree (again): %v", err)
	}
	if first != second {
		t.Fatalf("two runs drew different trees:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if !strings.Contains(first, treeBranch) || !strings.Contains(first, treeLast) {
		t.Errorf("tree is not drawn with box-drawing connectors:\n%s", first)
	}
	// The chart renderer's bar is what marks each row's share.
	if !strings.Contains(first, chartFull) || !strings.Contains(first, chartEmpty) {
		t.Errorf("tree rows carry no size bar:\n%s", first)
	}
	if !strings.Contains(first, formatFileSize(136)) {
		t.Errorf("tree does not report the rolled-up total:\n%s", first)
	}
}

func TestTreeTruncationIsStatedInTheDrawing(t *testing.T) {
	root := treeFixture(t)

	out, _, err := cliRun(t, newTreeCmd(), root, "--depth", "1")
	if err != nil {
		t.Fatalf("chest tree: %v", err)
	}
	if !strings.Contains(out, "below depth") {
		t.Errorf("a truncated tree does not say what it left unread:\n%s", out)
	}
}

func TestTreeFlagValidation(t *testing.T) {
	root := treeFixture(t)

	if _, _, err := cliRun(t, newTreeCmd(), root, "--depth", "-1"); err == nil ||
		!strings.Contains(err.Error(), "--depth") {
		t.Errorf("negative depth error = %v, want a --depth message", err)
	}
	if _, _, err := cliRun(t, newTreeCmd(), root, "--sort", "colour"); err == nil ||
		!strings.Contains(err.Error(), "--sort") {
		t.Errorf("bad sort error = %v, want a --sort message", err)
	}

	file := filepath.Join(root, "a.txt")
	if _, _, err := cliRun(t, newTreeCmd(), file); err == nil ||
		!strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file argument error = %v, want \"not a directory\"", err)
	}
	if _, _, err := cliRun(t, newTreeCmd(), filepath.Join(root, "ghost")); err == nil {
		t.Error("missing directory = nil error, want an error")
	}
}

func TestTreeFlagsRegistered(t *testing.T) {
	cmd := newTreeCmd()
	for _, name := range []string{"depth", "sort", "json", "exclude"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("tree is missing --%s", name)
		}
	}
	if cmd.Flags().ShorthandLookup("x") == nil {
		t.Error("tree is missing -x")
	}
	if def := cmd.Flags().Lookup("depth").DefValue; def != "3" {
		t.Errorf("--depth default = %q, want 3", def)
	}
	if def := cmd.Flags().Lookup("sort").DefValue; def != "size" {
		t.Errorf("--sort default = %q, want size", def)
	}
}

func TestTreeSizeBarScalesAgainstTheWholeTree(t *testing.T) {
	full := treeSizeCell(&treeNode{Size: 136}, 136)
	half := treeSizeCell(&treeNode{Size: 68}, 136)
	zero := treeSizeCell(&treeNode{Size: 0}, 136)

	if !strings.Contains(full, strings.Repeat(chartFull, treeBarWidth)) {
		t.Errorf("a directory holding the whole tree does not draw a full bar: %q", full)
	}
	if strings.Count(full, chartFull) == strings.Count(half, chartFull) {
		t.Errorf("half and full drew the same number of cells: %q vs %q", full, half)
	}
	if strings.Count(zero, chartFull) != 0 {
		t.Errorf("an empty directory drew a filled bar: %q", zero)
	}
}

func TestTreeSizeKeyIsDescending(t *testing.T) {
	small := &treeNode{Name: "z", Size: 1}
	large := &treeNode{Name: "a", Size: 2}
	if !(treeKey(small, "size") > treeKey(large, "size")) {
		t.Fatalf("size keys are not descending: small=%q large=%q", treeKey(small, "size"), treeKey(large, "size"))
	}
	// Equal sizes must fall back to the name so the order cannot wobble.
	a1 := &treeNode{Name: "a", Size: 5}
	a2 := &treeNode{Name: "b", Size: 5}
	if treeKey(a1, "size") >= treeKey(a2, "size") {
		t.Fatalf("equal sizes do not break the tie by name: %q vs %q", treeKey(a1, "size"), treeKey(a2, "size"))
	}
}
