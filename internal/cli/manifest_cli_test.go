package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/manifest"
	"github.com/spf13/cobra"
)

// cliRun executes one cobra command with captured output, the way a user would
// invoke it. Everything the command prints — stdout, stderr and the returned
// error — is returned so a test can assert on the whole interaction.
func cliRun(t *testing.T, cmd *cobra.Command, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	// Cobra prints usage and "Error: …" to the output writer when a command
	// fails. Silencing both keeps the buffers to what the command itself wrote,
	// which is what these tests assert on.
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// manifestFixture writes a small deterministic tree and returns its root.
func manifestFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"a.txt":      "alpha",
		"b.txt":      "beta",
		"sub/c.txt":  "gamma",
		"sub/d/e.md": "delta",
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
	return root
}

// TestManifestThenVerifyClean is the whole point of the pair: build an
// inventory, change nothing, and the check passes.
func TestManifestThenVerifyClean(t *testing.T) {
	root := manifestFixture(t)
	doc := filepath.Join(t.TempDir(), "manifest.json")

	if _, _, err := cliRun(t, newManifestCmd(), root, "--out", doc); err != nil {
		t.Fatalf("chest manifest: %v", err)
	}
	if _, err := os.Stat(doc); err != nil {
		t.Fatalf("manifest document was not written: %v", err)
	}

	stdout, stderr, err := cliRun(t, newVerifyCmd(), root, "--manifest", doc)
	if err != nil {
		t.Fatalf("chest verify on an unchanged tree = %v, want nil\n%s%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "OK") {
		t.Errorf("verify output does not report the OK count:\n%s", stdout)
	}
	if IsIntegrityFailure(err) {
		t.Error("IsIntegrityFailure(nil) = true, want false")
	}
}

// The summary has to reach the user without corrupting a redirect: the
// document goes to stdout, the prose to stderr.
func TestManifestSummaryGoesToStderr(t *testing.T) {
	root := manifestFixture(t)

	stdout, stderr, err := cliRun(t, newManifestCmd(), root)
	if err != nil {
		t.Fatalf("chest manifest: %v", err)
	}
	var m manifest.Manifest
	if err := json.Unmarshal([]byte(stdout), &m); err != nil {
		t.Fatalf("stdout is not a manifest document: %v\n%s", err, stdout)
	}
	if m.FileCount != 4 {
		t.Errorf("FileCount = %d, want 4", m.FileCount)
	}
	if !strings.Contains(stderr, "Manifest built") {
		t.Errorf("summary missing from stderr:\n%s", stderr)
	}
}

func TestManifestCSVOutput(t *testing.T) {
	root := manifestFixture(t)

	stdout, _, err := cliRun(t, newManifestCmd(), root, "--csv")
	if err != nil {
		t.Fatalf("chest manifest --csv: %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(stdout)).ReadAll()
	if err != nil {
		t.Fatalf("CSV parse: %v\n%s", err, stdout)
	}
	if len(rows) != 5 { // header + 4 files
		t.Fatalf("rows = %d, want 5", len(rows))
	}
	want := []string{"path", "size", "mtime_unix", "sha256", "status"}
	if !reflect.DeepEqual(rows[0], want) {
		t.Errorf("header = %v, want %v", rows[0], want)
	}
	if !strings.HasPrefix(rows[1][0], "a.txt") {
		t.Errorf("first data row path = %q, want a.txt", rows[1][0])
	}
}

// A tree whose document lives inside it must still verify clean: the manifest
// is not a file that appeared after the backup was taken.
func TestManifestInsideTreeStillVerifies(t *testing.T) {
	root := manifestFixture(t)
	doc := filepath.Join(root, "manifest.json")

	if _, _, err := cliRun(t, newManifestCmd(), root, "--out", doc); err != nil {
		t.Fatalf("chest manifest: %v", err)
	}
	if _, _, err := cliRun(t, newVerifyCmd(), root, "--manifest", doc); err != nil {
		t.Fatalf("verify with the manifest inside the tree = %v, want nil", err)
	}
}

// Modification, deletion and addition each have to surface, and the command has
// to fail so a backup job notices.
func TestVerifyReportsIntegrityFailure(t *testing.T) {
	root := manifestFixture(t)
	doc := filepath.Join(t.TempDir(), "manifest.json")

	if _, _, err := cliRun(t, newManifestCmd(), root, "--out", doc); err != nil {
		t.Fatalf("chest manifest: %v", err)
	}

	// Same length, different bytes: only a digest comparison sees this.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("ALPHA"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "extra.txt"), []byte("surprise"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := cliRun(t, newVerifyCmd(), root, "--manifest", doc, "--json")
	if err == nil {
		t.Fatalf("verify after three discrepancies = nil, want an integrity failure\n%s", stdout)
	}
	if !IsIntegrityFailure(err) {
		t.Fatalf("error = %v, want an integrity failure", err)
	}
	if !strings.Contains(err.Error(), "exit code 4") {
		t.Errorf("error %q does not name the exit code", err)
	}

	var res manifest.Result
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, stdout)
	}
	if res.Modified != 1 || res.Missing != 1 || res.Added != 1 {
		t.Fatalf("counts = %d/%d/%d, want 1/1/1", res.Modified, res.Missing, res.Added)
	}
	if res.Intact {
		t.Error("Intact = true, want false")
	}
	want := map[string]manifest.Status{
		"a.txt":         manifest.StatusModified,
		"b.txt":         manifest.StatusMissing,
		"sub/extra.txt": manifest.StatusAdded,
	}
	got := map[string]manifest.Status{}
	for _, d := range res.Files {
		got[d.Path] = d.Status
	}
	for path, status := range want {
		if got[path] != status {
			t.Errorf("%s = %q, want %q", path, got[path], status)
		}
	}
}

func TestVerifyMissingManifestIsACleanError(t *testing.T) {
	root := manifestFixture(t)
	missing := filepath.Join(t.TempDir(), "nope.json")

	_, _, err := cliRun(t, newVerifyCmd(), root, "--manifest", missing)
	if err == nil {
		t.Fatal("verify against a missing manifest = nil, want an error")
	}
	if !strings.Contains(err.Error(), "reading manifest") {
		t.Fatalf("error = %v, want it to name the unreadable manifest", err)
	}
	if IsIntegrityFailure(err) {
		t.Error("a missing manifest must not be reported as an integrity failure; the check never ran")
	}
}

// A manifest is data. An entry pointing outside the scanned directory has to be
// refused rather than followed.
func TestVerifyRejectsManifestPathTraversal(t *testing.T) {
	root := manifestFixture(t)
	doc := filepath.Join(t.TempDir(), "manifest.json")

	m := &manifest.Manifest{
		Version:   manifest.Version,
		Algorithm: manifest.Algorithm,
		Root:      root,
		MaxSize:   manifest.DefaultMaxSize,
		Files: []manifest.Entry{
			{Path: "../../escape.txt", Size: 1, SHA256: strings.Repeat("a", 64)},
		},
	}
	if err := manifest.WriteJSON(mustCreate(t, doc), m); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	_, _, err := cliRun(t, newVerifyCmd(), root, "--manifest", doc)
	if err == nil {
		t.Fatal("verify accepted a traversing manifest, want an error")
	}
	if !strings.Contains(err.Error(), "escapes the scanned directory") {
		t.Fatalf("error = %v, want it to name the traversal", err)
	}
}

func TestVerifyRejectsForeignDocument(t *testing.T) {
	root := manifestFixture(t)
	doc := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(doc, []byte(`{"note":"this is not a manifest"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := cliRun(t, newVerifyCmd(), root, "--manifest", doc)
	if err == nil || !strings.Contains(err.Error(), "not a chest manifest") {
		t.Fatalf("error = %v, want a clear rejection", err)
	}
}

func TestVerifyRequiresManifestFlag(t *testing.T) {
	root := manifestFixture(t)

	_, _, err := cliRun(t, newVerifyCmd(), root)
	if err == nil || !strings.Contains(err.Error(), "--manifest") {
		t.Fatalf("error = %v, want a message naming --manifest", err)
	}
}

func TestVerifyExcludesJSONAndCSV(t *testing.T) {
	root := manifestFixture(t)
	doc := filepath.Join(t.TempDir(), "manifest.json")

	_, _, err := cliRun(t, newVerifyCmd(), root, "--manifest", doc, "--json", "--csv")
	if err == nil || !strings.Contains(err.Error(), "choose one of --json or --csv") {
		t.Fatalf("error = %v, want the shared --json/--csv rejection", err)
	}
}

// Neither command may touch the tree it reads. A manifest tool that rewrote a
// timestamp or truncated a file would be worse than no tool at all.
func TestManifestAndVerifyAreReadOnly(t *testing.T) {
	root := manifestFixture(t)
	before := treeSnapshot(t, root)
	doc := filepath.Join(t.TempDir(), "manifest.json")

	if _, _, err := cliRun(t, newManifestCmd(), root, "--out", doc); err != nil {
		t.Fatalf("chest manifest: %v", err)
	}
	if _, _, err := cliRun(t, newVerifyCmd(), root, "--manifest", doc); err != nil {
		t.Fatalf("chest verify: %v", err)
	}

	if after := treeSnapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("the scanned tree changed:\nbefore %v\nafter  %v", before, after)
	}
}

func TestVerifyOutWritesReportAndStillFails(t *testing.T) {
	root := manifestFixture(t)
	doc := filepath.Join(t.TempDir(), "manifest.json")
	report := filepath.Join(t.TempDir(), "verify.json")

	if _, _, err := cliRun(t, newManifestCmd(), root, "--out", doc); err != nil {
		t.Fatalf("chest manifest: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal(err)
	}

	_, _, err := cliRun(t, newVerifyCmd(), root, "--manifest", doc, "--out", report)
	if !IsIntegrityFailure(err) {
		t.Fatalf("error = %v, want an integrity failure", err)
	}
	data, rerr := os.ReadFile(report)
	if rerr != nil {
		t.Fatalf("report not written: %v", rerr)
	}
	var res manifest.Result
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("report is not JSON: %v", err)
	}
	if res.Missing != 1 {
		t.Errorf("report says Missing = %d, want 1", res.Missing)
	}
}

func TestManifestExclusionsAreApplied(t *testing.T) {
	root := manifestFixture(t)

	stdout, _, err := cliRun(t, newManifestCmd(), root, "-x", "sub")
	if err != nil {
		t.Fatalf("chest manifest -x sub: %v", err)
	}
	var m manifest.Manifest
	if err := json.Unmarshal([]byte(stdout), &m); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	for _, f := range m.Files {
		if strings.HasPrefix(f.Path, "sub/") {
			t.Errorf("%s survived -x sub", f.Path)
		}
	}
	if m.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", m.FileCount)
	}
}

func TestParseMaxSizeFlag(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"", manifest.DefaultMaxSize, false},
		{"500MB", 500 * 1024 * 1024, false},
		{"1GB", 1024 * 1024 * 1024, false},
		{" 2gb ", 2 * 1024 * 1024 * 1024, false},
		{"1024", 1024, false},
		{"0", 0, true},
		{"banana", 0, true},
	}
	for _, tt := range tests {
		got, err := parseMaxSizeFlag(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseMaxSizeFlag(%q) = %d, want an error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseMaxSizeFlag(%q) error = %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseMaxSizeFlag(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestManifestMaxSizeIsReportedNotHidden(t *testing.T) {
	root := manifestFixture(t)

	// Every fixture file is under 6 bytes, so a 3-byte gate skips them all.
	stdout, stderr, err := cliRun(t, newManifestCmd(), root, "--max-size", "3")
	if err != nil {
		t.Fatalf("chest manifest --max-size 3: %v", err)
	}
	var m manifest.Manifest
	if err := json.Unmarshal([]byte(stdout), &m); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if m.TooLargeCount == 0 {
		t.Error("TooLargeCount = 0, want the skipped files counted")
	}
	if !strings.Contains(stderr, "Skipped:") {
		t.Errorf("the summary does not report the skipped count:\n%s", stderr)
	}
}

func TestIgnoredDocPath(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "backup")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "manifest.json")
	outside := filepath.Join(base, "manifest.json")

	got := ignoredDocPath(root, inside)
	if len(got) != 1 || !strings.EqualFold(got[0], inside) {
		t.Errorf("ignoredDocPath(inside) = %v, want [%s]", got, inside)
	}
	if got := ignoredDocPath(root, outside); got != nil {
		t.Errorf("ignoredDocPath(outside) = %v, want nil", got)
	}
	if got := ignoredDocPath(root, ""); got != nil {
		t.Errorf("ignoredDocPath(empty) = %v, want nil", got)
	}
}

func TestManifestFlagsRegistered(t *testing.T) {
	manifestCmd := newManifestCmd()
	for _, name := range []string{"out", "max-size", "exclude", "csv"} {
		if manifestCmd.Flags().Lookup(name) == nil {
			t.Errorf("manifest is missing --%s", name)
		}
	}
	if manifestCmd.Flags().ShorthandLookup("o") == nil {
		t.Error("manifest is missing -o")
	}
	if manifestCmd.Flags().ShorthandLookup("x") == nil {
		t.Error("manifest is missing -x")
	}

	verifyCmd := newVerifyCmd()
	for _, name := range []string{"manifest", "max-size", "exclude", "json", "csv", "out"} {
		if verifyCmd.Flags().Lookup(name) == nil {
			t.Errorf("verify is missing --%s", name)
		}
	}
	if verifyCmd.Flags().ShorthandLookup("x") == nil {
		t.Error("verify is missing -x")
	}
	if verifyCmd.Args == nil {
		t.Error("verify declares no Args validator")
	}
}

func TestManifestArgsAreValidated(t *testing.T) {
	if _, _, err := cliRun(t, newManifestCmd()); err == nil {
		t.Error("chest manifest with no directory = nil, want an error")
	}
	if _, _, err := cliRun(t, newManifestCmd(), "a", "b"); err == nil {
		t.Error("chest manifest with two directories = nil, want an error")
	}
	if _, _, err := cliRun(t, newVerifyCmd()); err == nil {
		t.Error("chest verify with no directory = nil, want an error")
	}
}

// mustCreate opens a file for writing, failing the test if it cannot.
func mustCreate(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// treeSnapshot records every file's digest so a test can prove a read-only
// command left the tree byte-identical.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		if info.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}
