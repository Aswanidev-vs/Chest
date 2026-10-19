package cli

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/search"
)

// searchFixedTime pins modification times so every expectation below is a
// literal rather than "whatever the filesystem clock said".
var searchFixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// searchModTime renders a fixed instant the way the engine does: os.FileInfo
// reports modification times in the local zone, and the CSV column is
// time.RFC3339 of that same instant.
func searchModTime(t time.Time) string {
	return t.In(time.Local).Format(time.RFC3339)
}

var searchCSVHeader = []string{"category", "path", "rel_path", "size_bytes", "modified", "score", "content_line", "content_match"}

// useTempSearchHome points ~/.chest at a scratch directory so the saved-search
// tests never touch the developer's real home and the plugin manager finds an
// empty plugin set. Windows resolves the home directory from USERPROFILE
// rather than HOME, so both are set.
func useTempSearchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func writeSearchFixtureFile(t *testing.T, dir, name, content string, mod time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
	return path
}

// chdirToFixture moves the working directory into a fixture tree and returns
// the root every search below passes on the command line.
//
// The search skips entries marked hidden, and on Windows %LOCALAPPDATA% and
// %APPDATA% are marked hidden, so a fixture reached by absolute path under the
// user profile would walk to nothing. Searching "." keeps every fixture file
// visible and makes the reported paths relative, which is also what a real
// `chest search` in a directory prints.
func chdirToFixture(t *testing.T, dir string) {
	t.Helper()
	t.Chdir(dir)
}

// searchRunResult separates the three writers a search touches: os.Stdout (the
// human report), the command's own writer (machine reports), and stderr (the
// spinner and status lines). Keeping them apart is what lets these tests prove
// a new flag never leaks into the wrong stream.
type searchRunResult struct {
	stdout string
	out    string
	stderr string
	err    error
}

// runSearchCLI executes the search command with args and captures every stream.
// The human report is printed straight to os.Stdout, so stdout is captured
// through a pipe, drained on its own goroutine because the pipe buffer is
// finite.
func runSearchCLI(t *testing.T, args ...string) searchRunResult {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	drained := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		drained <- string(out)
	}()

	cmd := newSearchCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	execErr := cmd.Execute()

	os.Stdout = old
	_ = w.Close()
	stdout := <-drained
	_ = r.Close()

	return searchRunResult{stdout: stdout, out: out.String(), stderr: errBuf.String(), err: execErr}
}

// searchFixturePath is the absolute path column of a row when the search ran
// from the fixture directory with the root given as ".". The walker joins the
// root to each entry without cleaning, so the column keeps the ".\" prefix
// while the rel_path column does not.
func searchFixturePath(name ...string) string {
	return "." + string(filepath.Separator) + filepath.Join(name...)
}

func parseSearchCSV(t *testing.T, data string) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("parse csv %q: %v", data, err)
	}
	return rows
}

// TestSearchCSVOutputContent pins the CSV table the --csv flag produces.
func TestSearchCSVOutputContent(t *testing.T) {
	useTempSearchHome(t)
	dir := t.TempDir()
	writeSearchFixtureFile(t, dir, "alpha.txt", "alpha payload\n", searchFixedTime)
	writeSearchFixtureFile(t, dir, "notes.md", "TODO write docs\n", searchFixedTime.Add(time.Hour))
	writeSearchFixtureFile(t, dir, "clip.mp4", "fake video bytes\n", searchFixedTime.Add(2*time.Hour))
	chdirToFixture(t, dir)

	tests := []struct {
		name string
		args []string
		want [][]string
	}{
		{
			name: "extension filter",
			args: []string{"--csv", "--ext", "txt", "", "."},
			want: [][]string{
				searchCSVHeader,
				{"Document", searchFixturePath("alpha.txt"), "alpha.txt", strconv.Itoa(len("alpha payload\n")), searchModTime(searchFixedTime), "1", "0", ""},
			},
		},
		{
			name: "type filter",
			args: []string{"--csv", "--type", "video", "", "."},
			want: [][]string{
				searchCSVHeader,
				{"Video", searchFixturePath("clip.mp4"), "clip.mp4", strconv.Itoa(len("fake video bytes\n")), searchModTime(searchFixedTime.Add(2 * time.Hour)), "1", "0", ""},
			},
		},
		{
			name: "content grep reports the snippet and its line",
			args: []string{"--csv", "--content", "TODO", "", "."},
			want: [][]string{
				searchCSVHeader,
				{"Document", searchFixturePath("notes.md"), "notes.md", strconv.Itoa(len("TODO write docs\n")), searchModTime(searchFixedTime.Add(time.Hour)), "1", "1", "TODO write docs"},
			},
		},
		{
			name: "combined filters intersect",
			args: []string{"--csv", "--type", "document", "--ext", "md", "--limit", "5", "", "."},
			want: [][]string{
				searchCSVHeader,
				{"Document", searchFixturePath("notes.md"), "notes.md", strconv.Itoa(len("TODO write docs\n")), searchModTime(searchFixedTime.Add(time.Hour)), "1", "0", ""},
			},
		},
		{
			name: "no matches still emits the header",
			args: []string{"--csv", "--ext", "mkv", "", "."},
			want: [][]string{searchCSVHeader},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runSearchCLI(t, tt.args...)
			if res.err != nil {
				t.Fatalf("search: %v", res.err)
			}
			if got := parseSearchCSV(t, res.out); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("csv mismatch:\n got %v\nwant %v", got, tt.want)
			}
			if res.stdout != "" {
				t.Fatalf("--csv must not print the human report, got %q", res.stdout)
			}
		})
	}
}

// TestSearchCSVOutFileMatchesStatsParity checks --out behaves like it does on
// stats/duplicates: the report lands in the file and only a short confirmation
// reaches the writer.
func TestSearchCSVOutFileMatchesStatsParity(t *testing.T) {
	useTempSearchHome(t)
	dir := t.TempDir()
	writeSearchFixtureFile(t, dir, "alpha.txt", "alpha payload\n", searchFixedTime)
	chdirToFixture(t, dir)
	out := filepath.Join(t.TempDir(), "matches.csv")

	res := runSearchCLI(t, "--csv", "--ext", "txt", "--out", out, "", ".")
	if res.err != nil {
		t.Fatalf("search: %v", res.err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	want := strings.Join([]string{
		strings.Join(searchCSVHeader, ","),
		fmt.Sprintf("Document,%s,alpha.txt,%d,%s,1,0,", searchFixturePath("alpha.txt"), len("alpha payload\n"), searchModTime(searchFixedTime)),
	}, "\n") + "\n"
	if string(data) != want {
		t.Fatalf("file content = %q, want %q", data, want)
	}

	wantConfirmation := fmt.Sprintf("wrote %d bytes to %s\n", len(data), out)
	if res.out != wantConfirmation {
		t.Fatalf("confirmation = %q, want %q", res.out, wantConfirmation)
	}
	if res.stdout != "" {
		t.Fatalf("--out must not also print the report, got %q", res.stdout)
	}
}

// TestSearchJSONStaysAvailable guards the pre-existing --json path against a
// regression from routing it through the shared report emitter.
func TestSearchJSONStaysAvailable(t *testing.T) {
	useTempSearchHome(t)
	dir := t.TempDir()
	writeSearchFixtureFile(t, dir, "alpha.txt", "alpha payload\n", searchFixedTime)
	chdirToFixture(t, dir)

	res := runSearchCLI(t, "--json", "--ext", "txt", "", ".")
	if res.err != nil {
		t.Fatalf("search: %v", res.err)
	}
	if !strings.HasPrefix(res.out, "[\n  {\n") || !strings.Contains(res.out, `"rel_path": "alpha.txt"`) {
		t.Fatalf("--json output changed shape:\n%s", res.out)
	}
	if res.stdout != "" {
		t.Fatalf("--json must not print the human report, got %q", res.stdout)
	}
}

func TestSearchJSONAndCSVAreMutuallyExclusive(t *testing.T) {
	useTempSearchHome(t)
	dir := t.TempDir()
	writeSearchFixtureFile(t, dir, "alpha.txt", "alpha payload\n", searchFixedTime)
	chdirToFixture(t, dir)

	tests := []struct {
		name string
		args []string
	}{
		{"both long flags", []string{"--json", "--csv", "", "."}},
		{"json shorthand with csv", []string{"-j", "--csv", "", "."}},
		{"json with csv after the path", []string{"", ".", "--json", "--csv"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runSearchCLI(t, tt.args...)
			if res.err == nil {
				t.Fatal("expected an error for --json --csv")
			}
			if res.err.Error() != "choose one of --json or --csv" {
				t.Fatalf("error = %q, want %q", res.err, "choose one of --json or --csv")
			}
		})
	}
}

func TestSearchSaveAndLoadAreMutuallyExclusive(t *testing.T) {
	useTempSearchHome(t)
	dir := t.TempDir()
	writeSearchFixtureFile(t, dir, "alpha.txt", "alpha payload\n", searchFixedTime)
	chdirToFixture(t, dir)

	res := runSearchCLI(t, "--save", "a", "--load", "b", "", ".")
	if res.err == nil {
		t.Fatal("expected an error for --save together with --load")
	}
	if res.err.Error() != "choose one of --save or --load" {
		t.Fatalf("error = %q, want %q", res.err, "choose one of --save or --load")
	}
}

// TestSearchSaveLoadRoundTrip is the core of saved searches: everything the
// user typed is persisted, and replaying the name reproduces the identical run.
func TestSearchSaveLoadRoundTrip(t *testing.T) {
	home := useTempSearchHome(t)
	dir := t.TempDir()
	writeSearchFixtureFile(t, filepath.Join(dir, "films"), "holiday.mkv", "video bytes one\n", searchFixedTime.Add(time.Hour))
	writeSearchFixtureFile(t, filepath.Join(dir, "films"), "holiday2.mkv", "video bytes two\n", searchFixedTime.Add(2*time.Hour))
	writeSearchFixtureFile(t, dir, "readme.txt", "not a film\n", searchFixedTime)
	chdirToFixture(t, dir)

	filters := []string{
		"--type", "video",
		"--size", ">1B",
		"--modified", "<2999-01-01",
		"--content", "video",
		"--ext", "mkv",
		"--limit", "1",
		"--hidden",
		"--fuzzy=false",
		"--ignore-case=false",
	}

	saveArgs := append([]string{"holiday", ".", "--save", "holidays"}, filters...)
	saved := runSearchCLI(t, saveArgs...)
	if saved.err != nil {
		t.Fatalf("--save: %v", saved.err)
	}

	want := search.SavedQuery{
		Name:          "holidays",
		Pattern:       "holiday",
		RootPath:      ".",
		Type:          "video",
		Extensions:    []string{"mkv"},
		SizeCondition: ">1B",
		DateCondition: "<2999-01-01",
		ContentQuery:  "video",
		IgnoreCase:    false,
		Regex:         false,
		Exact:         false,
		Fuzzy:         false,
		IncludeHidden: true,
		Limit:         1,
	}

	got, err := search.LoadQuery("holidays")
	if err != nil {
		t.Fatalf("LoadQuery: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("saved query mismatch:\n got %#v\nwant %#v", got, want)
	}
	if _, err := os.Stat(filepath.Join(home, ".chest", "searches", "holidays.toml")); err != nil {
		t.Fatalf("saved search not written where documented: %v", err)
	}

	// Replaying the name must reproduce the run the flags produced, byte for
	// byte: same root path, same filters, same order, same limit.
	directArgs := append([]string{"holiday", ".", "--csv"}, filters...)
	direct := runSearchCLI(t, directArgs...)
	if direct.err != nil {
		t.Fatalf("direct run: %v", direct.err)
	}
	replayed := runSearchCLI(t, "--load", "holidays", "--csv")
	if replayed.err != nil {
		t.Fatalf("--load: %v", replayed.err)
	}
	if replayed.out == "" {
		t.Fatal("--load produced no report")
	}
	if replayed.out != direct.out {
		t.Fatalf("replayed run differs from the flags it was saved from:\n got %q\nwant %q", replayed.out, direct.out)
	}
	if replayed.stdout != "" {
		t.Fatalf("--load --csv must not print the human report, got %q", replayed.stdout)
	}

	// --limit 1 keeps one row, and every field shows a restored filter: the
	// video type, the mkv extension, the size floor, the content grep and the
	// newest match first.
	rows := parseSearchCSV(t, replayed.out)
	if len(rows) != 2 {
		t.Fatalf("expected a header plus one row (--limit 1), got %d rows: %v", len(rows), rows)
	}
	older := []string{"Video", searchFixturePath("films", "holiday.mkv"), filepath.Join("films", "holiday.mkv"), strconv.Itoa(len("video bytes one\n")), searchModTime(searchFixedTime.Add(time.Hour)), rows[1][5], "1", "video bytes one"}
	newer := []string{"Video", searchFixturePath("films", "holiday2.mkv"), filepath.Join("films", "holiday2.mkv"), strconv.Itoa(len("video bytes two\n")), searchModTime(searchFixedTime.Add(2 * time.Hour)), rows[1][5], "1", "video bytes two"}
	if !reflect.DeepEqual(rows[1], older) && !reflect.DeepEqual(rows[1], newer) {
		t.Fatalf("row does not reflect the restored filters: %v", rows[1])
	}
}

// TestSearchLoadFlagsOverrideSavedValues proves a loaded query is a starting
// point, not a straitjacket: an explicitly typed flag still wins.
func TestSearchLoadFlagsOverrideSavedValues(t *testing.T) {
	useTempSearchHome(t)
	dir := t.TempDir()
	writeSearchFixtureFile(t, dir, "fresh.txt", "one\n", searchFixedTime)
	writeSearchFixtureFile(t, dir, "stale.txt", "two\n", searchFixedTime.Add(time.Hour))
	chdirToFixture(t, dir)

	if res := runSearchCLI(t, "", ".", "--save", "limited", "--limit", "1"); res.err != nil {
		t.Fatalf("--save: %v", res.err)
	}
	if res := runSearchCLI(t, "", ".", "--save", "whole", "--limit", "0"); res.err != nil {
		t.Fatalf("--save: %v", res.err)
	}

	// An explicit --limit beats the saved one...
	replayed := runSearchCLI(t, "--load", "whole", "--limit", "1", "--csv")
	if replayed.err != nil {
		t.Fatalf("--load: %v", replayed.err)
	}
	rows := parseSearchCSV(t, replayed.out)
	if len(rows) != 2 || rows[1][2] != "stale.txt" {
		t.Fatalf("explicit --limit ignored over the saved one: %v", rows)
	}

	// ...and the saved limit stands when no limit is typed.
	replayed = runSearchCLI(t, "--load", "limited", "--csv")
	if replayed.err != nil {
		t.Fatalf("--load: %v", replayed.err)
	}
	rows = parseSearchCSV(t, replayed.out)
	if len(rows) != 2 || rows[1][2] != "stale.txt" {
		t.Fatalf("saved limit not restored: %v", rows)
	}

	// A saved filter is not lost when the replay passes no arguments at all.
	replayed = runSearchCLI(t, "--load", "whole", "--csv")
	if replayed.err != nil {
		t.Fatalf("--load: %v", replayed.err)
	}
	if rows = parseSearchCSV(t, replayed.out); len(rows) != 3 {
		t.Fatalf("saved root path or limit lost on a bare --load: %v", rows)
	}
}

func TestSearchLoadMissingNameNamesThePath(t *testing.T) {
	home := useTempSearchHome(t)

	res := runSearchCLI(t, "--load", "never-saved")
	if res.err == nil {
		t.Fatal("--load of a missing name returned no error")
	}
	wantPath := filepath.Join(home, ".chest", "searches", "never-saved.toml")
	if !strings.Contains(res.err.Error(), wantPath) {
		t.Fatalf("error %q does not name the file it looked for (%s)", res.err, wantPath)
	}
}

func TestSearchSavedNameRejectsPathTraversal(t *testing.T) {
	home := useTempSearchHome(t)
	root := filepath.Join(home, ".chest", "searches")

	tests := []struct {
		name string
		args []string
	}{
		{"load parent traversal", []string{"--load", "../evil"}},
		{"load nested traversal", []string{"--load", "a/../../evil"}},
		{"load windows traversal", []string{"--load", `..\evil`}},
		{"load absolute path", []string{"--load", "/etc/passwd"}},
		{"load bare dotdot", []string{"--load", ".."}},
		{"save parent traversal", []string{"--save", "../evil"}},
		{"save forward slash", []string{"--save", "sub/dir"}},
		{"save backslash", []string{"--save", `sub\dir`}},
		{"save drive separator", []string{"--save", "C:evil"}},
		{"save blank name", []string{"--save", "   "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runSearchCLI(t, tt.args...)
			if res.err == nil {
				t.Fatalf("%v was accepted", tt.args)
			}
			msg := res.err.Error()
			if !strings.Contains(msg, "invalid saved search name") && !strings.Contains(msg, "name is required") {
				t.Fatalf("error %q does not explain the rejected name", msg)
			}
			// Nothing may be written outside the searches directory.
			if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.toml")); err == nil {
				t.Fatal("traversal wrote a file outside the searches directory")
			}
		})
	}
}

// TestSearchSaveWorksWithoutAnyMatches proves --save persists the query rather
// than the results: nothing matching must not stop the search being stored.
func TestSearchSaveWorksWithoutAnyMatches(t *testing.T) {
	home := useTempSearchHome(t)
	dir := t.TempDir()
	writeSearchFixtureFile(t, dir, "alpha.txt", "alpha payload\n", searchFixedTime)
	chdirToFixture(t, dir)

	res := runSearchCLI(t, "qqqzzzqqq", ".", "--save", "nothing-here")
	if res.err != nil {
		t.Fatalf("--save: %v", res.err)
	}
	if res.stdout != "\x1b[38;5;246mNo matching files found.\x1b[0m\n" {
		t.Fatalf("stdout = %q", res.stdout)
	}

	path := filepath.Join(home, ".chest", "searches", "nothing-here.toml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("query not saved even though nothing matched: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved query: %v", err)
	}
	if !strings.Contains(string(body), `pattern = "qqqzzzqqq"`) {
		t.Fatalf("saved query lost the pattern:\n%s", body)
	}
	if !strings.Contains(res.stderr, "saved search") || !strings.Contains(res.stderr, path) {
		t.Fatalf("stderr confirmation missing: %q", res.stderr)
	}
	if strings.Contains(res.stdout, "saved search") {
		t.Fatalf("save confirmation must not pollute stdout, got %q", res.stdout)
	}
}

// TestSearchDefaultOutputUnchanged is the golden test for the most important
// property of this feature: with no new flag, the human report is exactly the
// bytes it was before --csv, --out, --save and --load existed.
func TestSearchDefaultOutputUnchanged(t *testing.T) {
	useTempSearchHome(t)
	dir := t.TempDir()
	writeSearchFixtureFile(t, dir, "alpha.txt", "alpha payload\n", searchFixedTime)
	writeSearchFixtureFile(t, dir, "beta.txt", "beta\n", searchFixedTime.Add(time.Hour))
	chdirToFixture(t, dir)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "every txt file, newest first",
			args: []string{"--ext", "txt"},
			want: "\n\x1b[38;5;254mFound \x1b[1;38;5;82m2\x1b[0m\x1b[38;5;254m matches in \x1b[38;5;220m.\x1b[0m\n\n" +
				"  \x1b[38;5;75m[DOCUMENT ]\x1b[0m \x1b[1;38;5;255mbeta.txt\x1b[0m \x1b[38;5;245m(5 B)\x1b[0m\n" +
				"  \x1b[38;5;75m[DOCUMENT ]\x1b[0m \x1b[1;38;5;255malpha.txt\x1b[0m \x1b[38;5;245m(14 B)\x1b[0m\n" +
				"\n",
		},
		{
			name: "single match by pattern",
			args: []string{"alpha"},
			want: "\n\x1b[38;5;254mFound \x1b[1;38;5;82m1\x1b[0m\x1b[38;5;254m matches in \x1b[38;5;220m.\x1b[0m\n\n" +
				"  \x1b[38;5;75m[DOCUMENT ]\x1b[0m \x1b[1;38;5;255malpha.txt\x1b[0m \x1b[38;5;245m(14 B)\x1b[0m\n" +
				"\n",
		},
		{
			name: "explicit path argument",
			args: []string{"--ext", "txt", "", "."},
			want: "\n\x1b[38;5;254mFound \x1b[1;38;5;82m2\x1b[0m\x1b[38;5;254m matches in \x1b[38;5;220m.\x1b[0m\n\n" +
				"  \x1b[38;5;75m[DOCUMENT ]\x1b[0m \x1b[1;38;5;255mbeta.txt\x1b[0m \x1b[38;5;245m(5 B)\x1b[0m\n" +
				"  \x1b[38;5;75m[DOCUMENT ]\x1b[0m \x1b[1;38;5;255malpha.txt\x1b[0m \x1b[38;5;245m(14 B)\x1b[0m\n" +
				"\n",
		},
		{
			name: "no matches",
			args: []string{"--ext", "mkv"},
			want: "\x1b[38;5;246mNo matching files found.\x1b[0m\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runSearchCLI(t, tt.args...)
			if res.err != nil {
				t.Fatalf("search: %v", res.err)
			}
			if res.stdout != tt.want {
				t.Fatalf("human report changed:\n got %q\nwant %q", res.stdout, tt.want)
			}
			if res.out != "" {
				t.Fatalf("a plain search must write nothing to the report writer, got %q", res.out)
			}
		})
	}
}
