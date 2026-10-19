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
	// The tree command reads — and with --implicit writes — ~/.chest/
	// tree_flags.toml; point it at a scratch home so no fixture run touches
	// the developer's saved lists or inherits them.
	hermeticHome(t)
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
	// Directories first at every level, as `tree` does, then descending size
	// within each group.
	want := []string{"docs", "skipme", "empty", "big.bin", "a.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("size order = %v, want %v", got, want)
	}

	byName := treeJSON(t, root, "--depth", "1", "--sort", "name")
	got = got[:0]
	for _, c := range byName.Node.Children {
		got = append(got, c.Name)
	}
	want = []string{"docs", "empty", "skipme", "a.txt", "big.bin"}
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

// TestTreeUnreadDirectoriesNeverClaimZeroSize covers the size cell for a
// directory the walk stopped above. Its size is unknown, not zero, so drawing
// "0 B" there would claim a directory that holds files is empty.
func TestTreeUnreadDirectoriesNeverClaimZeroSize(t *testing.T) {
	root := treeFixture(t)

	out, _, err := cliRun(t, newTreeCmd(), root, "--depth", "1")
	if err != nil {
		t.Fatalf("chest tree: %v", err)
	}

	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "below depth limit") {
			continue
		}
		if strings.Contains(line, "0 B") && strings.Contains(line, "█") {
			t.Errorf("unread directory drew a zero-size bar as though it were measured: %q", line)
		}
	}
}

// TestTreeDirectoriesAreListedBeforeFiles pins the `tree` convention: a reader
// scanning a level meets every directory before any file.
func TestTreeDirectoriesAreListedBeforeFiles(t *testing.T) {
	root := treeFixture(t)

	out, _, err := cliRun(t, newTreeCmd(), root, "--sort", "name")
	if err != nil {
		t.Fatalf("chest tree: %v", err)
	}

	lines := strings.Split(out, "\n")
	dirs := map[string]bool{"docs": true, "empty": true, "skipme": true}
	seenFile := false
	for _, line := range lines {
		// Only top-level entries start with exactly two spaces and a branch.
		if !strings.HasPrefix(line, "  ├") && !strings.HasPrefix(line, "  └") {
			continue
		}
		name := strings.Fields(strings.TrimLeft(line, " │├└─"))[0]
		if dirs[name] {
			if seenFile {
				t.Errorf("directory %q was listed after a file:\n%s", name, out)
				break
			}
			continue
		}
		seenFile = true
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
	for _, name := range []string{"depth", "sort", "json", "exclude", "implicit", "explicit", "remove"} {
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
	// Expanded directories: these stand for folders the walk actually read, so
	// their Size is a real measurement and the bar is meaningful.
	full := treeSizeCell(&treeNode{Size: 136, Expanded: true}, 136)
	half := treeSizeCell(&treeNode{Size: 68, Expanded: true}, 136)
	zero := treeSizeCell(&treeNode{Size: 0, Expanded: true}, 136)

	// A directory the walk never read has an unknown size, not a zero one, so
	// it must draw no bar at all rather than an empty-looking zero.
	unread := treeSizeCell(&treeNode{Size: 0, Expanded: false}, 136)
	if unread != "" {
		t.Errorf("an unread directory drew a size instead of leaving the column empty: %q", unread)
	}

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

// --exclude takes a whole run of names after the flag, as documented, and the
// older comma form keeps working beside it.
func TestTreeExcludeTakesMultipleSpaceSeparatedNames(t *testing.T) {
	root := treeFixture(t)

	rep := treeJSON(t, root, "--depth", "3", "--exclude", "big.bin", "skipme", "a.txt")
	for _, name := range []string{"big.bin", "skipme", "a.txt"} {
		if findNodeQuiet(rep.Node, name) != nil {
			t.Errorf("--exclude did not drop %q", name)
		}
	}
	if rep.Excluded != 3 {
		t.Errorf("Excluded = %d, want 3", rep.Excluded)
	}
	if rep.TotalBytes != 30 {
		t.Errorf("total = %d, want 30 (136 minus big.bin, skipme and a.txt)", rep.TotalBytes)
	}

	comma := treeJSON(t, root, "--depth", "3", "-x", "big.bin,skipme")
	if comma.Excluded != 2 {
		t.Errorf("-x a,b excluded %d entries, want 2", comma.Excluded)
	}
}

// .git is on the implicit list out of the box — nobody has to type it — and
// --explicit puts a named entry back on the drawing even so.
func TestTreeGitIsImplicitlyExcludedAndExplicitlyShown(t *testing.T) {
	root := treeFixture(t)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), make([]byte, 8), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := treeJSON(t, root, "--depth", "3")
	if findNodeQuiet(rep.Node, ".git") != nil {
		t.Error(".git must be excluded without being named as an argument")
	}
	if rep.Excluded != 1 {
		t.Errorf("Excluded = %d, want 1", rep.Excluded)
	}
	if rep.TotalBytes != 136 {
		t.Errorf("total = %d, want 136 (the .git file is not counted)", rep.TotalBytes)
	}

	shown := treeJSON(t, root, "--depth", "3", "--explicit", ".git")
	if findNodeQuiet(shown.Node, ".git") == nil {
		t.Error("--explicit did not draw an implicitly excluded name")
	}
	if shown.TotalBytes != 144 {
		t.Errorf("total = %d, want 144 (.git back in the sum)", shown.TotalBytes)
	}
	if shown.Excluded != 0 {
		t.Errorf("Excluded = %d, want 0", shown.Excluded)
	}
}

// --implicit saves the name where the next run will find it, skips it right
// away, and says that it wrote the file.
func TestTreeImplicitIsSavedAndAppliesToTheNextRun(t *testing.T) {
	root := treeFixture(t)

	out, _, err := cliRun(t, newTreeCmd(), root, "--implicit", "skipme")
	if err != nil {
		t.Fatalf("chest tree --implicit: %v", err)
	}
	if strings.Contains(out, "skipme") {
		t.Errorf("--implicit should skip the name it just saved:\n%s", out)
	}
	if !strings.Contains(out, "saved") {
		t.Errorf("a run that changed the saved lists must say so:\n%s", out)
	}

	path, err := treeFlagsPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("saved lists not written: %v", err)
	}
	for _, want := range []string{"skipme", ".git"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("saved lists are missing %q:\n%s", want, data)
		}
	}

	// The next run, with no flags at all, still skips it.
	rep := treeJSON(t, root, "--depth", "3")
	if findNodeQuiet(rep.Node, "skipme") != nil {
		t.Error("--implicit must hold for the next run without the flag")
	}
	if rep.TotalBytes != 135 {
		t.Errorf("total = %d, want 135", rep.TotalBytes)
	}
}

// --explicit is saved the same way --implicit is, and wins over it both on
// the run that sets it and on every run after.
func TestTreeExplicitIsSavedAndBeatsImplicit(t *testing.T) {
	root := treeFixture(t)
	if _, _, err := cliRun(t, newTreeCmd(), root, "--implicit", "skipme"); err != nil {
		t.Fatalf("chest tree --implicit: %v", err)
	}

	shown := treeJSON(t, root, "--depth", "3", "--explicit", "skipme")
	if findNodeQuiet(shown.Node, "skipme") == nil {
		t.Error("--explicit did not show a name that was implicit")
	}

	rep := treeJSON(t, root, "--depth", "3")
	if findNodeQuiet(rep.Node, "skipme") == nil {
		t.Error("--explicit must persist into later runs")
	}
}

// --remove takes a name off either saved list, effective on the same run and
// on every run after, and leaves the rest of the list — the .git default —
// untouched.
func TestTreeRemoveTakesNamesOffTheSavedLists(t *testing.T) {
	root := treeFixture(t)
	if _, _, err := cliRun(t, newTreeCmd(), root, "--implicit", "skipme"); err != nil {
		t.Fatalf("chest tree --implicit: %v", err)
	}

	rep := treeJSON(t, root, "--depth", "3", "--remove", "skipme")
	if findNodeQuiet(rep.Node, "skipme") == nil {
		t.Error("--remove must take effect on the run that removes it")
	}

	path, err := treeFlagsPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("saved lists unreadable after --remove: %v", err)
	}
	if strings.Contains(string(data), "skipme") {
		t.Errorf("removed name is still on disk:\n%s", data)
	}
	if !strings.Contains(string(data), ".git") {
		t.Errorf("--remove must not take the .git default off:\n%s", data)
	}

	// With the name off the list it is an ordinary folder again; the file
	// check above already proves it is not being re-added behind the scenes.
	again := treeJSON(t, root, "--depth", "3")
	if findNodeQuiet(again.Node, "skipme") == nil {
		t.Error("a removed name is still hidden on the next run")
	}
}

// The built-in .git rule is an ordinary entry on the implicit list, so
// --remove can take it off too.
func TestTreeRemoveCanDropTheGitDefault(t *testing.T) {
	root := treeFixture(t)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), make([]byte, 8), 0o644); err != nil {
		t.Fatal(err)
	}

	plain := treeJSON(t, root, "--depth", "3")
	if findNodeQuiet(plain.Node, ".git") != nil {
		t.Fatal(".git should start excluded")
	}

	removed := treeJSON(t, root, "--depth", "3", "--remove", ".git")
	if findNodeQuiet(removed.Node, ".git") == nil {
		t.Error("--remove .git did not take effect")
	}
	again := treeJSON(t, root, "--depth", "3")
	if findNodeQuiet(again.Node, ".git") == nil {
		t.Error("the removed .git default came back on the next run")
	}
}

// Trailing names attach to the flag they were written after, not to whichever
// list flag happened to be parsed last overall.
func TestTreeTrailingNamesAttachToTheFlagTheyFollow(t *testing.T) {
	root := treeFixture(t)

	rep := treeJSON(t, root, "--depth", "3", "--exclude", "big.bin", "--implicit", "skipme", "a.txt")
	if rep.TotalBytes != 30 {
		t.Errorf("total = %d, want 30 (big.bin, skipme and a.txt all skipped)", rep.TotalBytes)
	}

	path, err := treeFlagsPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("saved lists not written: %v", err)
	}
	if strings.Contains(string(data), "big.bin") {
		t.Errorf("--exclude leaked into the saved lists:\n%s", data)
	}
	for _, want := range []string{"skipme", "a.txt"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("trailing name %q was not attached to --implicit:\n%s", want, data)
		}
	}
}

// A lone bare argument after a list flag is still the directory, the shape
// `chest tree -x <names> <path>` has always accepted.
func TestTreeDirectoryMayFollowListFlags(t *testing.T) {
	root := treeFixture(t)

	stdout, stderr, err := cliRun(t, newTreeCmd(), "--json", "--exclude", "skipme", root)
	if err != nil {
		t.Fatalf("chest tree --exclude skipme <path>: %v\n%s", err, stderr)
	}
	var rep treeReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, stdout)
	}
	if rep.Root != root {
		t.Errorf("Root = %q, want %q (the trailing argument is the directory)", rep.Root, root)
	}
	if findNodeQuiet(rep.Node, "skipme") != nil {
		t.Error("--exclude did not drop skipme")
	}
	if rep.TotalBytes != 135 {
		t.Errorf("total = %d, want 135", rep.TotalBytes)
	}
}

// The path is the only positional argument there is; a second one has nowhere
// to go, and the error says where the names belong instead.
func TestTreeRejectsStrayArguments(t *testing.T) {
	root := treeFixture(t)

	_, _, err := cliRun(t, newTreeCmd(), root, "stray")
	if err == nil {
		t.Fatal("a second positional argument must be rejected")
	}
	if !strings.Contains(err.Error(), "stray") {
		t.Errorf("error does not name the stray argument: %v", err)
	}
	if !strings.Contains(err.Error(), "--exclude") {
		t.Errorf("error should point at the flags that take names: %v", err)
	}
}
