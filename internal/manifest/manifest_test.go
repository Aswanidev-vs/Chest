package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTree materialises a fixed fixture and returns its root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

func fixture() map[string]string {
	return map[string]string{
		"a.txt":       "alpha",
		"b.txt":       "beta",
		"sub/c.txt":   "gamma",
		"sub/d.bin":   "\x00\x01\x02",
		"deep/e/f.md": "delta",
	}
}

func TestGenerateWalksEveryFile(t *testing.T) {
	root := writeTree(t, fixture())

	m, err := Generate(Options{Root: root})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	want := []string{"a.txt", "b.txt", "deep/e/f.md", "sub/c.txt", "sub/d.bin"}
	got := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		got = append(got, f.Path)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	if m.FileCount != len(want) {
		t.Errorf("FileCount = %d, want %d", m.FileCount, len(want))
	}
	if m.Algorithm != Algorithm || m.Version != Version {
		t.Errorf("header = %q v%d, want %q v%d", m.Algorithm, m.Version, Algorithm, Version)
	}
	if m.SkippedCount != 0 {
		t.Errorf("SkippedCount = %d, want 0", m.SkippedCount)
	}
	// Every digest is a full lowercase hex SHA-256.
	for _, f := range m.Files {
		if len(f.SHA256) != 64 {
			t.Errorf("%s: digest %q is not 64 hex characters", f.Path, f.SHA256)
		}
	}
}

// Generate must never touch the tree it inventories.
func TestGenerateIsReadOnly(t *testing.T) {
	root := writeTree(t, fixture())
	before := snapshot(t, root)

	if _, err := Generate(Options{Root: root}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("tree changed:\nbefore %v\nafter  %v", before, after)
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	root := writeTree(t, fixture())

	var buf1, buf2 bytes.Buffer
	m1, err := Generate(Options{Root: root})
	if err != nil {
		t.Fatalf("Generate 1: %v", err)
	}
	if err := WriteJSON(&buf1, m1); err != nil {
		t.Fatalf("WriteJSON 1: %v", err)
	}
	m2, err := Generate(Options{Root: root})
	if err != nil {
		t.Fatalf("Generate 2: %v", err)
	}
	if err := WriteJSON(&buf2, m2); err != nil {
		t.Fatalf("WriteJSON 2: %v", err)
	}
	if buf1.String() != buf2.String() {
		t.Fatalf("two runs over the same tree produced different manifests:\n%s\n---\n%s", buf1.String(), buf2.String())
	}
	// No wall-clock field may creep into the document, or "identical bytes =>
	// identical manifest" stops being true.
	if strings.Contains(buf1.String(), "created_at") {
		t.Errorf("manifest carries a creation timestamp, which makes it irreproducible")
	}
}

func TestGenerateHonoursExclusions(t *testing.T) {
	root := writeTree(t, fixture())

	m, err := Generate(Options{Root: root, Exclude: []string{"node_modules", "*.bin"}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, f := range m.Files {
		if f.Path == "sub/d.bin" {
			t.Errorf("glob exclusion *.bin did not drop %s", f.Path)
		}
	}
	// An excluded name is dropped at any depth, like the indexer's --except.
	for _, f := range m.Files {
		if strings.Contains(f.Path, "node_modules") {
			t.Errorf("excluded path survived: %s", f.Path)
		}
	}
}

// The size gate must skip, not truncate: the skipped file is counted and named.
func TestGenerateSkipsOversizedFiles(t *testing.T) {
	root := writeTree(t, fixture())

	m, err := Generate(Options{Root: root, MaxSize: 3})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if m.TooLargeCount == 0 {
		t.Fatalf("expected oversized files to be skipped, manifest: %+v", m)
	}
	if m.SkippedCount == 0 {
		t.Fatalf("SkippedCount = 0, want a reported count of skipped files")
	}
	var named bool
	for _, s := range m.Skipped {
		if s.Reason != ReasonTooLarge {
			continue
		}
		named = true
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(s.Path))); err != nil {
			t.Errorf("skipped entry %s does not exist on disk", s.Path)
		}
	}
	if !named {
		t.Error("no skipped entry carried the too-large reason")
	}
	// A file that is never hashed must never be recorded as hashed.
	for _, f := range m.Files {
		if f.Size > 3 {
			t.Errorf("%s is %d bytes but was recorded as hashed", f.Path, f.Size)
		}
	}
}

func TestVerifyCleanRun(t *testing.T) {
	root := writeTree(t, fixture())
	m, err := Generate(Options{Root: root})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	res, err := Verify(Options{Root: root}, m)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.Intact {
		t.Fatalf("Intact = false, want true (discrepancies: %+v)", res.Files)
	}
	if res.OK != len(fixture()) {
		t.Errorf("OK = %d, want %d", res.OK, len(fixture()))
	}
	if res.Modified+res.Missing+res.Added != 0 {
		t.Errorf("discrepancies = %d, want 0", res.DiscrepancyCount())
	}
	if len(res.Files) != 0 {
		t.Errorf("Files = %v, want only non-OK entries (none)", res.Files)
	}
}

func TestVerifyReportsModifiedMissingAdded(t *testing.T) {
	root := writeTree(t, fixture())
	m, err := Generate(Options{Root: root})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Same size, different content: only a digest comparison can see this.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("ALPHA"), 0o644); err != nil {
		t.Fatalf("modify a.txt: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "b.txt")); err != nil {
		t.Fatalf("remove b.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "extra.txt"), []byte("surprise"), 0o644); err != nil {
		t.Fatalf("add extra.txt: %v", err)
	}

	res, err := Verify(Options{Root: root}, m)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Intact {
		t.Fatal("Intact = true after three discrepancies, want false")
	}
	if res.Modified != 1 || res.Missing != 1 || res.Added != 1 {
		t.Fatalf("counts = modified %d missing %d added %d, want 1/1/1", res.Modified, res.Missing, res.Added)
	}

	got := map[string]Status{}
	for _, d := range res.Files {
		got[d.Path] = d.Status
	}
	for path, want := range map[string]Status{
		"a.txt":     StatusModified,
		"b.txt":     StatusMissing,
		"extra.txt": StatusAdded,
	} {
		if got[path] != want {
			t.Errorf("%s = %q, want %q", path, got[path], want)
		}
	}

	// A MODIFIED verdict must carry both digests, otherwise the report cannot
	// tell a reader what changed.
	for _, d := range res.Files {
		if d.Status != StatusModified {
			continue
		}
		if d.ExpectedSHA256 == "" || d.ActualSHA256 == "" || d.ExpectedSHA256 == d.ActualSHA256 {
			t.Errorf("MODIFIED %s: expected %q actual %q, want two differing digests", d.Path, d.ExpectedSHA256, d.ActualSHA256)
		}
	}
}

// A manifest is data, and data can be edited. An entry that would resolve
// outside the scanned directory has to be refused, not followed.
func TestVerifyRejectsPathTraversal(t *testing.T) {
	root := writeTree(t, fixture())
	m, err := Generate(Options{Root: root})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	m.Files = append(m.Files, Entry{Path: "../outside.txt", Size: 1, SHA256: strings.Repeat("a", 64)})

	_, err = Verify(Options{Root: root}, m)
	if err == nil {
		t.Fatal("Verify accepted a traversing entry, want an error")
	}
	if !strings.Contains(err.Error(), "escapes the scanned directory") {
		t.Fatalf("error = %v, want it to name the traversal", err)
	}
}

func TestVerifyRejectsAbsoluteEntry(t *testing.T) {
	root := writeTree(t, fixture())
	m := &Manifest{Version: Version, Algorithm: Algorithm, Root: root, Files: []Entry{{Path: "/etc/hosts"}}}
	if _, err := Verify(Options{Root: root}, m); err == nil {
		t.Fatal("Verify accepted an absolute entry, want an error")
	}
}

func TestVerifyRefusesNilManifest(t *testing.T) {
	if _, err := Verify(Options{Root: t.TempDir()}, nil); err == nil {
		t.Fatal("Verify(nil) = nil error, want a clean error")
	}
}

func TestVerifyReportsUnreadableAndUnchecked(t *testing.T) {
	root := writeTree(t, fixture())
	m, err := Generate(Options{Root: root, MaxSize: 3})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// The same gate on the verify side means the same files stay unchecked
	// rather than turning into spurious MODIFIED verdicts.
	res, err := Verify(Options{Root: root}, m)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Modified != 0 {
		t.Errorf("Modified = %d, want 0 (skipped files must not read as modified)", res.Modified)
	}
	if res.Unchecked == 0 {
		t.Errorf("Unchecked = 0, want the oversized files reported as unchecked")
	}
	// A file that was never hashed is not a discrepancy, so the run does not
	// fail; but the partial coverage must be countable, not invisible.
	if !res.Intact {
		t.Errorf("Intact = false, want true: unchecked files are not discrepancies (%+v)", res.Files)
	}
	var sawUnchecked bool
	for _, d := range res.Files {
		if d.Status == StatusUnchecked {
			sawUnchecked = true
		}
	}
	if !sawUnchecked {
		t.Error("no diff carried the UNCHECKED status for a file that was never hashed")
	}
}

// Generating into the scanned tree must not make the tree permanently
// unverifiable: the manifest document is excluded from the added-file scan.
func TestVerifyIgnoresTheManifestDocument(t *testing.T) {
	root := writeTree(t, fixture())
	m, err := Generate(Options{Root: root})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	var buf bytes.Buffer
	if err := WriteJSON(&buf, m); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if err := os.WriteFile(manifestPath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	res, err := Verify(Options{Root: root, Ignore: []string{manifestPath}}, m)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Added != 0 {
		t.Fatalf("Added = %d, want 0 (the manifest itself must be ignored): %+v", res.Added, res.Files)
	}
}

func TestLoadRoundTrip(t *testing.T) {
	root := writeTree(t, fixture())
	m, err := Generate(Options{Root: root})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	var buf bytes.Buffer
	if err := WriteJSON(&buf, m); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	back, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(back.Files, m.Files) {
		t.Errorf("round-tripped files differ:\n got %+v\nwant %+v", back.Files, m.Files)
	}
	if back.MaxSize != m.MaxSize {
		t.Errorf("MaxSize = %d, want %d", back.MaxSize, m.MaxSize)
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file", func(t *testing.T) {
		_, err := Load(filepath.Join(dir, "nope.json"))
		if err == nil {
			t.Fatal("Load(missing) = nil error, want a clean error")
		}
		if !strings.Contains(err.Error(), "reading manifest") {
			t.Errorf("error = %v, want it to name the read failure", err)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("error = %v, want it to wrap os.ErrNotExist so callers can test for it", err)
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		p := filepath.Join(dir, "bad.json")
		if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "parsing manifest") {
			t.Fatalf("Load(malformed) error = %v, want a parse error", err)
		}
	})

	t.Run("foreign document", func(t *testing.T) {
		p := filepath.Join(dir, "other.json")
		if err := os.WriteFile(p, []byte(`{"files":[]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		if err == nil || !strings.Contains(err.Error(), "not a chest manifest") {
			t.Fatalf("Load(foreign) error = %v, want a clear rejection", err)
		}
	})

	t.Run("future version", func(t *testing.T) {
		p := filepath.Join(dir, "future.json")
		body := `{"version":99,"algorithm":"sha256","files":[]}`
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		if err == nil || !strings.Contains(err.Error(), "unsupported version") {
			t.Fatalf("Load(future) error = %v, want a version error", err)
		}
	})
}

func TestGenerateRejectsNonDirectory(t *testing.T) {
	root := writeTree(t, fixture())
	file := filepath.Join(root, "a.txt")

	if _, err := Generate(Options{Root: file}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("Generate(file) error = %v, want \"not a directory\"", err)
	}
	if _, err := Generate(Options{Root: filepath.Join(root, "ghost")}); err == nil {
		t.Fatal("Generate(missing dir) = nil error, want an error")
	}
	if _, err := Generate(Options{Root: "  "}); err == nil {
		t.Fatal("Generate(blank) = nil error, want an error")
	}
}

func TestCSVRowsCoverSkippedFiles(t *testing.T) {
	root := writeTree(t, fixture())
	m, err := Generate(Options{Root: root, MaxSize: 3})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	rows := CSVRows(m)
	if len(rows) != 1+len(m.Files)+len(m.Skipped) {
		t.Fatalf("rows = %d, want 1 header + %d files + %d skipped", len(rows), len(m.Files), len(m.Skipped))
	}
	header := rows[0]
	want := []string{"path", "size", "mtime_unix", "sha256", "status"}
	if !reflect.DeepEqual(header, want) {
		t.Fatalf("header = %v, want %v", header, want)
	}
	var sawSkip bool
	for _, r := range rows[1:] {
		if r[4] != "hashed" {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Error("no CSV row carried a non-hashed status")
	}
}

func TestCSVStatusRowsIncludeTotals(t *testing.T) {
	res := &Result{OK: 3, Modified: 1, Files: []Diff{{Path: "a.txt", Status: StatusModified}}}
	rows := CSVStatusRows(res)
	if len(rows) != 1+len(res.Files)+6 {
		t.Fatalf("rows = %d, want 1 header + %d files + 6 totals", len(rows), len(res.Files))
	}
	if got := rows[1]; got[1] != string(StatusModified) {
		t.Errorf("first data row status = %q, want %q", got[1], StatusModified)
	}
}

func TestExcluderMatching(t *testing.T) {
	e := NewExcluder([]string{"Node_Modules, *.tmp , build/cache"})
	tests := []struct {
		name, rel string
		want      bool
	}{
		{"src", "src", false},
		{"node_modules", "a/b/node_modules", true},
		{"NODE_MODULES", "a/b/node_modules", true},
		{"notes.TMP", "notes.TMP", true},
		{"cache", "build/cache", true},
		{"other", "other", false},
	}
	for _, tt := range tests {
		if got := e.Match(tt.name, tt.rel); got != tt.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tt.name, tt.rel, got, tt.want)
		}
	}

	// A nil excluder excludes nothing, which is what every command without
	// -x relies on.
	var nilExcl *Excluder
	if nilExcl.Match("anything", "anything") {
		t.Error("nil Excluder matched, want no exclusions")
	}
	if (&Excluder{}).Match("anything", "anything") {
		t.Error("empty Excluder matched, want no exclusions")
	}
}

func TestManifestJSONFieldNames(t *testing.T) {
	root := writeTree(t, map[string]string{"a.txt": "alpha"})
	m, err := Generate(Options{Root: root})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, m); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var doc struct {
		Files []struct {
			Path      string `json:"path"`
			Size      int64  `json:"size"`
			MTimeUnix int64  `json:"mtime_unix"`
			SHA256    string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(doc.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(doc.Files))
	}
	f := doc.Files[0]
	if f.Path != "a.txt" || f.Size != 5 || f.MTimeUnix == 0 || f.SHA256 == "" {
		t.Fatalf("entry = %+v, want path/size/mtime_unix/sha256 populated", f)
	}
}

// snapshot records every file's path, size and digest so a test can prove the
// walk did not modify anything it inventoried.
func snapshot(t *testing.T, root string) map[string]string {
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
			out[rel] = "dir"
			return nil
		}
		sum, herr := hashFile(path)
		if herr != nil {
			return herr
		}
		out[rel] = sum
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}
