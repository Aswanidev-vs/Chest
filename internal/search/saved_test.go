package search

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// useSavedSearchHome points ~/.chest at a scratch directory so saved-search
// tests never touch the developer's real home. Windows resolves the home dir
// from USERPROFILE rather than HOME, so both are set.
func useSavedSearchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestSanitizeSavedName(t *testing.T) {
	home := useSavedSearchHome(t)

	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"simple", "movies", "movies", false},
		{"spaces are fine", "big videos", "big videos", false},
		{"surrounding space is trimmed", "  movies  ", "movies", false},
		{"dots inside the name", "videos.2024", "videos.2024", false},
		{"empty", "", "", true},
		{"whitespace only", "   ", "", true},
		{"current dir", ".", "", true},
		{"parent dir", "..", "", true},
		{"unix traversal", "../evil", "", true},
		{"unix traversal nested", "a/../../evil", "", true},
		{"windows traversal", `..\evil`, "", true},
		{"forward slash", "sub/dir", "", true},
		{"back slash", `sub\dir`, "", true},
		{"drive separator", "C:name", "", true},
		{"leading separator", "/etc/passwd", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizeSavedName(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("SanitizeSavedName(%q) = %q, want error", tt.raw, got)
				}
				if got != "" {
					t.Fatalf("SanitizeSavedName(%q) returned %q alongside an error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("SanitizeSavedName(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("SanitizeSavedName(%q) = %q, want %q", tt.raw, got, tt.want)
			}

			// A valid name must resolve inside the searches directory.
			path, err := SavedSearchPath(tt.raw)
			if err != nil {
				t.Fatalf("SavedSearchPath(%q): %v", tt.raw, err)
			}
			wantPath := filepath.Join(home, ".chest", "searches", tt.want+".toml")
			if path != wantPath {
				t.Fatalf("SavedSearchPath(%q) = %q, want %q", tt.raw, path, wantPath)
			}
		})
	}
}

// TestSavedSearchPathRejectsTraversal pins the security property: no name can
// produce a path outside ~/.chest/searches, whatever separators it carries.
func TestSavedSearchPathRejectsTraversal(t *testing.T) {
	home := useSavedSearchHome(t)
	root := filepath.Join(home, ".chest", "searches")

	for _, raw := range []string{"../evil", "..", ".", `..\..\evil`, "/etc/passwd", `C:\Windows\evil`, "sub/../.."} {
		path, err := SavedSearchPath(raw)
		if err == nil {
			t.Fatalf("SavedSearchPath(%q) = %q, want error", raw, path)
		}
		if filepath.Dir(path) != root && path != "" {
			t.Fatalf("SavedSearchPath(%q) escaped to %q", raw, path)
		}
	}
}

func TestSaveQueryRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		query SavedQuery
	}{
		{
			name:  "every parameter set",
			query: SavedQuery{Name: "full", Pattern: "movie", RootPath: "/data/media", Type: "video", Extensions: []string{"mkv", "mp4"}, SizeCondition: ">1GB", DateCondition: "<30d", ContentQuery: "TODO", IgnoreCase: false, Regex: true, Exact: false, Fuzzy: false, IncludeHidden: true, Limit: 25},
		},
		{
			name:  "pattern only",
			query: SavedQuery{Name: "pattern-only", Pattern: "invoice", RootPath: "."},
		},
		{
			name:  "non zero defaults",
			query: SavedQuery{Name: "defaults", RootPath: "/tmp", IgnoreCase: true, Fuzzy: true},
		},
		{
			name:  "limit with no filters",
			query: SavedQuery{Name: "top-10", RootPath: "/photos", Limit: 10, IncludeHidden: true},
		},
		{
			name:  "name needing trimming",
			query: SavedQuery{Name: "  spaced  ", RootPath: "/tmp", Limit: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useSavedSearchHome(t)

			path, err := SaveQuery(tt.query)
			if err != nil {
				t.Fatalf("SaveQuery: %v", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("saved file not written: %v", err)
			}

			want := tt.query
			want.Name = strings.TrimSpace(tt.query.Name)
			got, err := LoadQuery(want.Name)
			if err != nil {
				t.Fatalf("LoadQuery: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip changed the query:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

// TestSavedQueryTOMLShape locks the on-disk format: readable key names, a
// comment telling the user how to replay it, and no runtime wiring leaking in.
func TestSavedQueryTOMLShape(t *testing.T) {
	useSavedSearchHome(t)

	path, err := SaveQuery(SavedQuery{
		Name: "big-videos", Pattern: "movie", RootPath: "/data", Type: "video",
		Extensions: []string{"mkv", "mp4"}, Limit: 5, IgnoreCase: true, Fuzzy: true,
	})
	if err != nil {
		t.Fatalf("SaveQuery: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	body := string(data)

	for _, want := range []string{
		"# CHEST saved search. Restore with: chest search --load big-videos",
		`name = "big-videos"`,
		`pattern = "movie"`,
		`root_path = "/data"`,
		`type = "video"`,
		`size_condition = ""`,
		`ignore_case = true`,
		`fuzzy = true`,
		`limit = 5`,
		"mkv",
		"mp4",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("saved TOML missing %q:\n%s", want, body)
		}
	}
}

// TestSavedQueryOptionsRoundTrip proves the persisted fields are exactly the
// fields the engine consumes: Options() must not drop or invent a parameter.
func TestSavedQueryOptionsRoundTrip(t *testing.T) {
	q := SavedQuery{
		Name: "x", Pattern: "movie", RootPath: "/data", Type: "video",
		Extensions: []string{"mkv"}, SizeCondition: ">1GB", DateCondition: "<30d",
		ContentQuery: "TODO", IgnoreCase: false, Regex: true, Exact: false,
		Fuzzy: false, IncludeHidden: true, Limit: 7,
	}
	progress := func(scanned, matched int) {}
	opts := q.Options(nil, progress)

	if opts.Pattern != q.Pattern || opts.RootPath != q.RootPath || opts.Type != q.Type ||
		opts.SizeCondition != q.SizeCondition || opts.DateCondition != q.DateCondition ||
		opts.ContentQuery != q.ContentQuery || opts.IgnoreCase != q.IgnoreCase ||
		opts.Regex != q.Regex || opts.Exact != q.Exact || opts.Fuzzy != q.Fuzzy ||
		opts.IncludeHidden != q.IncludeHidden || opts.Limit != q.Limit {
		t.Fatalf("Options did not carry the query through:\n%+v", opts)
	}
	if !reflect.DeepEqual(opts.Extensions, q.Extensions) {
		t.Fatalf("Extensions = %v, want %v", opts.Extensions, q.Extensions)
	}
	if opts.ClassifyFunc != nil || opts.Progress == nil {
		t.Fatalf("runtime wiring not wired through Options: %+v", opts)
	}
}

func TestLoadQueryMissingNamesTheFilePath(t *testing.T) {
	home := useSavedSearchHome(t)

	_, err := LoadQuery("never-saved")
	if err == nil {
		t.Fatal("LoadQuery of a missing name returned no error")
	}
	wantPath := filepath.Join(home, ".chest", "searches", "never-saved.toml")
	if !strings.Contains(err.Error(), wantPath) {
		t.Fatalf("error %q does not name the path it looked for (%s)", err, wantPath)
	}
}

// TestLoadQueryMissingListsWhatExists keeps the error useful once there are
// saved searches to choose from.
func TestLoadQueryMissingListsWhatExists(t *testing.T) {
	useSavedSearchHome(t)

	for _, name := range []string{"alpha", "beta"} {
		if _, err := SaveQuery(SavedQuery{Name: name}); err != nil {
			t.Fatalf("SaveQuery(%q): %v", name, err)
		}
	}

	_, err := LoadQuery("gamma")
	if err == nil {
		t.Fatal("LoadQuery of a missing name returned no error")
	}
	if !strings.Contains(err.Error(), "alpha, beta") {
		t.Fatalf("error %q does not list the saved searches", err)
	}
}

func TestLoadQueryRejectsTraversalName(t *testing.T) {
	useSavedSearchHome(t)

	for _, raw := range []string{"../evil", "..", `..\evil`, "sub/name", ""} {
		if _, err := LoadQuery(raw); err == nil {
			t.Fatalf("LoadQuery(%q) returned no error", raw)
		}
	}
}

func TestLoadQueryRejectsCorruptFile(t *testing.T) {
	home := useSavedSearchHome(t)
	dir := filepath.Join(home, ".chest", "searches")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bad := filepath.Join(dir, "broken.toml")
	if err := os.WriteFile(bad, []byte("this is = not = toml\n\t[[["), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadQuery("broken")
	if err == nil {
		t.Fatal("LoadQuery of a corrupt file returned no error")
	}
	if !strings.Contains(err.Error(), bad) {
		t.Fatalf("error %q does not name the corrupt file", err)
	}
}

// TestSaveQueryOverwrites proves re-saving edits a search in place instead of
// failing or accumulating files.
func TestSaveQueryOverwrites(t *testing.T) {
	useSavedSearchHome(t)

	if _, err := SaveQuery(SavedQuery{Name: "dup", Limit: 1}); err != nil {
		t.Fatalf("first SaveQuery: %v", err)
	}
	if _, err := SaveQuery(SavedQuery{Name: "dup", Limit: 9}); err != nil {
		t.Fatalf("second SaveQuery: %v", err)
	}

	got, err := LoadQuery("dup")
	if err != nil {
		t.Fatalf("LoadQuery: %v", err)
	}
	if got.Limit != 9 {
		t.Fatalf("Limit = %d, want 9 (the later save)", got.Limit)
	}

	names, err := ListSavedQueries()
	if err != nil {
		t.Fatalf("ListSavedQueries: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"dup"}) {
		t.Fatalf("ListSavedQueries = %v, want [dup]", names)
	}
}

func TestListSavedQueries(t *testing.T) {
	home := useSavedSearchHome(t)
	dir := filepath.Join(home, ".chest", "searches")

	names, err := ListSavedQueries()
	if err != nil {
		t.Fatalf("ListSavedQueries on a missing dir: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("ListSavedQueries on a missing dir = %v, want empty", names)
	}

	if err := os.MkdirAll(filepath.Join(dir, "videos"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"zebra.toml", "alpha.toml", "notes.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got, err := ListSavedQueries()
	if err != nil {
		t.Fatalf("ListSavedQueries: %v", err)
	}
	// Sorted, .toml-only, directories skipped.
	want := []string{"alpha", "zebra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListSavedQueries = %v, want %v", got, want)
	}
}

// TestLoadQueryNameComesFromTheRequest keeps the filename authoritative: a file
// edited to claim a different name still loads under the name asked for.
func TestLoadQueryNameComesFromTheRequest(t *testing.T) {
	useSavedSearchHome(t)

	if _, err := SaveQuery(SavedQuery{Name: "alpha", Limit: 3}); err != nil {
		t.Fatalf("SaveQuery: %v", err)
	}
	renamed := filepath.Join(filepath.Join(mustHome(t), ".chest", "searches"), "beta.toml")
	if err := os.Rename(
		filepath.Join(mustHome(t), ".chest", "searches", "alpha.toml"),
		renamed,
	); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got, err := LoadQuery("beta")
	if err != nil {
		t.Fatalf("LoadQuery: %v", err)
	}
	if got.Name != "beta" || got.Limit != 3 {
		t.Fatalf("LoadQuery = %#v, want name beta with limit 3", got)
	}
}

func mustHome(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	return home
}
