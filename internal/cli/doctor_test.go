package cli

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/indexer"
)

// hermeticHome points CHEST's state directory at a scratch path for the
// duration of a test. Without it a doctor run would read — and a helper like
// history.DefaultManager would create — the developer's real ~/.chest, which is
// exactly the side effect the command is supposed to avoid.
func hermeticHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)        // unix
	t.Setenv("USERPROFILE", home) // windows
	return home
}

// doctorFixture writes a small tree and returns its root.
func doctorFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"a.txt":            "alpha",
		"sub/b.txt":        "beta",
		"sub/deep/c.txt":   "gamma",
		"empty-dir-marker": "",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if err := os.Remove(filepath.Join(root, "empty-dir-marker")); err != nil {
		t.Fatal(err)
	}
	return root
}

func doctorJSON(t *testing.T, root string, args ...string) (*doctorReport, string) {
	t.Helper()
	stdout, stderr, err := cliRun(t, newDoctorCmd(), append([]string{root, "--json"}, args...)...)
	if err != nil {
		t.Fatalf("chest doctor --json %v: %v\n%s", args, err, stderr)
	}
	var rep doctorReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, stdout)
	}
	return &rep, stdout
}

func checkByName(t *testing.T, rep *doctorReport, name string) doctorCheck {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not found in %+v", name, rep.Checks)
	return doctorCheck{}
}

func TestDoctorRunsOnATempTree(t *testing.T) {
	hermeticHome(t)
	root := doctorFixture(t)

	rep, stdout := doctorJSON(t, root)

	// Every promised check has to be there: a health check that quietly stops
	// checking something is worse than no health check.
	want := []string{"index", "index-freshness", "history", "directories", "symlinks", "schedules", "rules"}
	got := make([]string, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		got = append(got, c.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("checks = %v, want %v", got, want)
	}
	if rep.Root != root {
		t.Errorf("Root = %q, want %q", rep.Root, root)
	}
	if rep.FilesOnDisk != 3 {
		t.Errorf("FilesOnDisk = %d, want 3", rep.FilesOnDisk)
	}
	// A fresh machine has no index; that is a warning, not a crash.
	if c := checkByName(t, rep, "index"); c.Status != doctorWarn {
		t.Errorf("index check = %q, want %q on a machine with no cache", c.Status, doctorWarn)
	}
	if !strings.Contains(checkByName(t, rep, "index").Detail, "chest index") {
		t.Error("the index check does not say how to fix the problem")
	}
	if c := checkByName(t, rep, "directories"); c.Status != doctorOK || c.Count != 2 {
		t.Errorf("directories check = %+v, want ok over the 2 folders walked", c)
	}
	if c := checkByName(t, rep, "symlinks"); c.Status != doctorOK {
		t.Errorf("symlinks check = %+v, want ok", c)
	}
	if c := checkByName(t, rep, "schedules"); c.Status != doctorOK {
		t.Errorf("schedules check = %+v, want ok (no schedules saved)", c)
	}
	if c := checkByName(t, rep, "rules"); c.Status != doctorOK || c.Count == 0 {
		t.Errorf("rules check = %+v, want ok over a resolved rule set", c)
	}
	if rep.Healthy {
		t.Error("Healthy = true on a machine with no index, want false")
	}
	// The read-only promise is part of the output, not just the code.
	if rep.ReadOnlyNote == "" {
		t.Error("the report does not state that nothing was written")
	}
	if !strings.Contains(stdout, `"healthy"`) {
		t.Error("--json output does not carry the health verdict")
	}
}

// The safety invariant: a health check must leave CHEST's state directory
// exactly as it found it, including not creating a database it did not need.
func TestDoctorDoesNotCreateOrModifyTheIndex(t *testing.T) {
	home := hermeticHome(t)
	root := doctorFixture(t)
	stateDir := filepath.Join(home, ".chest")

	// No index yet: doctor must not make one.
	if _, _, err := cliRun(t, newDoctorCmd(), root); err != nil {
		t.Fatalf("chest doctor on a fresh machine: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "index.db")); !os.IsNotExist(err) {
		t.Fatalf("doctor created an index database: %v", err)
	}

	// Now build one, snapshot the directory, and run doctor again.
	store, err := indexer.OpenOrCreate()
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	if _, err := store.IndexDirectory(root, false, nil); err != nil {
		t.Fatalf("IndexDirectory: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	before := fileDigests(t, filepath.Join(stateDir, "index.db"))

	for i := 0; i < 2; i++ {
		if _, _, err := cliRun(t, newDoctorCmd(), root); err != nil {
			t.Fatalf("chest doctor: %v", err)
		}
	}

	// SQLite may create its own transient -wal/-shm sidecars when a WAL-mode
	// database is read; what must not change is the database itself.
	if after := fileDigests(t, filepath.Join(stateDir, "index.db")); !reflect.DeepEqual(before, after) {
		t.Fatalf("doctor rewrote the index:\nbefore %v\nafter  %v", before, after)
	}
}

// A row pointing at a file that is gone, and a file the index never saw, are
// the two halves of index drift and both have to be reported.
func TestDoctorDetectsIndexDrift(t *testing.T) {
	hermeticHome(t)
	root := doctorFixture(t)

	store, err := indexer.OpenOrCreate()
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	if _, err := store.IndexDirectory(root, false, nil); err != nil {
		t.Fatalf("IndexDirectory: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	clean, _ := doctorJSON(t, root)
	freshness := checkByName(t, clean, "index-freshness")
	if freshness.Status != doctorOK {
		t.Fatalf("freshness check on a just-built index = %+v, want ok", freshness)
	}
	if len(freshness.Samples) != 0 {
		t.Errorf("a clean check carries samples %v", freshness.Samples)
	}

	// Delete one indexed file and add one the index has never seen.
	if err := os.Remove(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "surprise.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	drifted, _ := doctorJSON(t, root)
	freshness = checkByName(t, drifted, "index-freshness")
	if freshness.Status != doctorWarn {
		t.Fatalf("freshness check after drift = %+v, want a warning", freshness)
	}
	if !strings.Contains(freshness.Detail, "stale") || !strings.Contains(freshness.Detail, "unindexed") {
		t.Errorf("detail = %q, want it to name both kinds of drift", freshness.Detail)
	}
	if len(freshness.Samples) == 0 {
		t.Error("drift reported without any example path")
	}
	if c := checkByName(t, drifted, "index"); c.Status != doctorOK {
		t.Errorf("index check = %+v, want ok once the database exists", c)
	}
}

func TestDoctorExitCodeDependsOnStrict(t *testing.T) {
	hermeticHome(t)
	root := doctorFixture(t)

	// Warnings exist (no index), so the default run still succeeds.
	if _, _, err := cliRun(t, newDoctorCmd(), root); err != nil {
		t.Fatalf("chest doctor with warnings = %v, want nil", err)
	}
	// --strict turns the same findings into a failure.
	_, _, err := cliRun(t, newDoctorCmd(), root, "--strict")
	if err == nil {
		t.Fatal("chest doctor --strict with warnings = nil, want an error")
	}
	if !strings.Contains(err.Error(), "--strict") {
		t.Errorf("error = %v, want it to name --strict", err)
	}
}

func TestDoctorStrictPassesOnAHealthyMachine(t *testing.T) {
	home := hermeticHome(t)
	root := doctorFixture(t)

	store, err := indexer.OpenOrCreate()
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	if _, err := store.IndexDirectory(root, false, nil); err != nil {
		t.Fatalf("IndexDirectory: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rep, _ := doctorJSON(t, root)
	if !rep.Healthy {
		t.Fatalf("report is unhealthy after a fresh index: %+v", rep.Checks)
	}
	if _, _, err := cliRun(t, newDoctorCmd(), root, "--strict"); err != nil {
		t.Fatalf("chest doctor --strict on a healthy machine = %v, want nil (%s)", err, home)
	}
}

func TestDoctorHumanChecklist(t *testing.T) {
	hermeticHome(t)
	root := doctorFixture(t)

	stdout, _, err := cliRun(t, newDoctorCmd(), root)
	if err != nil {
		t.Fatalf("chest doctor: %v", err)
	}
	if !strings.Contains(stdout, "CHEST DOCTOR") {
		t.Errorf("output is not a headered report:\n%s", stdout)
	}
	for _, name := range []string{"index", "index-freshness", "history", "directories", "symlinks", "schedules", "rules"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("check %q missing from the checklist:\n%s", name, stdout)
		}
	}
	// Both a tick and a cross: the tree has no index (warn) but readable
	// directories (ok), so the checklist has to show both marks.
	if !strings.Contains(stdout, "\x1b[1;38;5;82m✔") {
		t.Error("no ✔ for the checks that passed")
	}
	if !strings.Contains(stdout, "\x1b[38;5;214m!") {
		t.Error("no marker for the check that warned")
	}
	if !strings.Contains(stdout, "warning") {
		t.Errorf("summary does not count the warnings:\n%s", stdout)
	}
}

func TestDoctorCSVAndOut(t *testing.T) {
	hermeticHome(t)
	root := doctorFixture(t)

	stdout, _, err := cliRun(t, newDoctorCmd(), root, "--csv")
	if err != nil {
		t.Fatalf("chest doctor --csv: %v", err)
	}
	rows, perr := csv.NewReader(strings.NewReader(stdout)).ReadAll()
	if perr != nil {
		t.Fatalf("CSV parse: %v\n%s", perr, stdout)
	}
	if len(rows) < 9 { // header + 7 checks + 2 totals
		t.Fatalf("rows = %d, want at least 9", len(rows))
	}
	if rows[0][0] != "check" || rows[0][1] != "status" {
		t.Errorf("header = %v, want check,status,...", rows[0])
	}

	out := filepath.Join(t.TempDir(), "health.json")
	if _, _, err := cliRun(t, newDoctorCmd(), root, "--out", out); err != nil {
		t.Fatalf("chest doctor --out: %v", err)
	}
	data, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatalf("report not written: %v", rerr)
	}
	var rep doctorReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("--out file is not JSON: %v", err)
	}
	if len(rep.Checks) == 0 {
		t.Error("the exported report has no checks")
	}
}

func TestDoctorExcludesJSONAndCSV(t *testing.T) {
	hermeticHome(t)
	root := doctorFixture(t)

	_, _, err := cliRun(t, newDoctorCmd(), root, "--json", "--csv")
	if err == nil || !strings.Contains(err.Error(), "choose one of --json or --csv") {
		t.Fatalf("error = %v, want the shared --json/--csv rejection", err)
	}
}

func TestDoctorExcludeIsCounted(t *testing.T) {
	hermeticHome(t)
	root := doctorFixture(t)

	rep, _ := doctorJSON(t, root, "-x", "sub")
	if rep.FilesOnDisk != 1 {
		t.Errorf("FilesOnDisk = %d, want 1 with sub excluded", rep.FilesOnDisk)
	}
	if rep.Unchecked != 1 { // the pruned sub directory; its contents are never visited
		t.Errorf("Unchecked = %d, want 1", rep.Unchecked)
	}
}

func TestDoctorBrokenSymlinksAreReported(t *testing.T) {
	if runtime.GOOS != "linux" {
		// Creating a symlink on Windows needs a privilege or developer mode that
		// CI machines do not reliably grant; the check itself is exercised on
		// every platform by the walk, just not the fixture.
		t.Skip("symlink creation is not reliably permitted on this platform")
	}
	hermeticHome(t)
	root := doctorFixture(t)
	if err := os.Symlink(filepath.Join(root, "ghost.txt"), filepath.Join(root, "dangling")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	rep, _ := doctorJSON(t, root)
	c := checkByName(t, rep, "symlinks")
	if c.Status != doctorWarn || c.Count != 1 {
		t.Fatalf("symlinks check = %+v, want one broken link reported", c)
	}
	// A broken link is not a file, so it must not also show up as index drift.
	if rep.FilesOnDisk != 3 {
		t.Errorf("FilesOnDisk = %d, want 3 (the link is not a file)", rep.FilesOnDisk)
	}
}

func TestDoctorFlagsRegistered(t *testing.T) {
	cmd := newDoctorCmd()
	for _, name := range []string{"json", "csv", "out", "strict", "exclude"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("doctor is missing --%s", name)
		}
	}
	if cmd.Flags().ShorthandLookup("x") == nil {
		t.Error("doctor is missing -x")
	}
	if cmd.Args == nil {
		t.Error("doctor declares no Args validator")
	}
}

func TestDoctorRejectsExtraArguments(t *testing.T) {
	hermeticHome(t)
	root := doctorFixture(t)

	if _, _, err := cliRun(t, newDoctorCmd(), root, root); err == nil {
		t.Error("chest doctor with two paths = nil, want an error")
	}
}

func TestSqliteReadOnlyURI(t *testing.T) {
	uri := sqliteReadOnlyURI(filepath.Join("home", "user", "index.db"))
	if !strings.HasPrefix(uri, "file:") || !strings.HasSuffix(uri, "?mode=ro") {
		t.Errorf("uri = %q, want a file: URI carrying mode=ro", uri)
	}
	if strings.Contains(uri, `\`) {
		t.Errorf("uri = %q contains a backslash, which is not valid URI syntax", uri)
	}
	// The "/"+drive spelling is the one that fails on Windows: SQLite reads "C:"
	// as an authority. Guard against reintroducing it.
	if runtime.GOOS == "windows" && strings.HasPrefix(uri, "file:/") && len(uri) > 6 && uri[6] == ':' {
		t.Errorf("uri = %q uses the /C:/ form, which Windows SQLite rejects", uri)
	}
}

func TestUnderRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "data", "backup")
	sep := string(filepath.Separator)

	if !underRoot(root, filepath.Join(root, "a.txt")) {
		t.Error("a file directly under the root was judged outside it")
	}
	if !underRoot(root, root) {
		t.Error("the root itself was judged outside it")
	}
	if underRoot(root, filepath.Join(string(filepath.Separator), "data", "backup-two", "a.txt")) {
		t.Error("a sibling directory sharing a prefix was judged inside the root")
	}
	if underRoot(root, filepath.Join(string(filepath.Separator), "elsewhere", "a.txt")) {
		t.Error("an unrelated path was judged inside the root")
	}
	if !underRoot("", filepath.Join(root, "a.txt")) {
		t.Error("an empty root should scope to the whole index")
	}
	if !underRoot(root, root+sep+"nested") {
		t.Error("a nested directory was judged outside the root")
	}
}

func TestSamplePathsTruncatesAndSaysSo(t *testing.T) {
	if got := samplePaths([]string{"a", "b"}); len(got) != 2 {
		t.Errorf("samplePaths(2) = %v, want both entries", got)
	}
	var many []string
	for i := 0; i < doctorSampleLimit+4; i++ {
		many = append(many, "x")
	}
	got := samplePaths(many)
	if len(got) != doctorSampleLimit+1 {
		t.Fatalf("samplePaths(%d) = %d entries, want %d plus an ellipsis", len(many), len(got), doctorSampleLimit)
	}
	if !strings.Contains(got[len(got)-1], "and 4 more") {
		t.Errorf("the ellipsis does not say how many were left out: %q", got[len(got)-1])
	}
}

// dirListing records every file in a directory with its size.
func dirListing(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", e.Name(), err)
		}
		out[e.Name()] = info.Size()
	}
	return out
}

// fileDigests records a file's size and digest, so a test can prove a
// read-only command left a database byte-identical.
func fileDigests(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return map[string]string{
		"size":   strconv.Itoa(len(data)),
		"sha256": hex.EncodeToString(sum[:]),
	}
}
