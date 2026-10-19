package search

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// SavedQuery is a search kept on disk: the complete set of query parameters, so
// the same walk and the same filters can be replayed later without retyping the
// command. It is stored as TOML at ~/.chest/searches/<name>.toml.
//
// Runtime wiring is deliberately absent: the classifier override and the
// progress callback belong to the process doing the search, not to the query.
type SavedQuery struct {
	Name          string   `toml:"name"`
	Pattern       string   `toml:"pattern"`
	RootPath      string   `toml:"root_path"`
	Type          string   `toml:"type"`
	Extensions    []string `toml:"extensions"`
	SizeCondition string   `toml:"size_condition"`
	DateCondition string   `toml:"date_condition"`
	ContentQuery  string   `toml:"content_query"`
	IgnoreCase    bool     `toml:"ignore_case"`
	Regex         bool     `toml:"regex"`
	Exact         bool     `toml:"exact"`
	Fuzzy         bool     `toml:"fuzzy"`
	IncludeHidden bool     `toml:"include_hidden"`
	Limit         int      `toml:"limit"`
}

// Options turns a saved query back into engine options. classify and progress
// are supplied by the caller (plugin-backed classification and the live
// spinner) because they are runtime wiring rather than persisted state.
func (q SavedQuery) Options(
	classify func(filename, ext string) string,
	progress func(scanned, matched int),
) SearchOptions {
	return SearchOptions{
		Pattern:       q.Pattern,
		RootPath:      q.RootPath,
		Type:          q.Type,
		Extensions:    q.Extensions,
		SizeCondition: q.SizeCondition,
		DateCondition: q.DateCondition,
		ContentQuery:  q.ContentQuery,
		IgnoreCase:    q.IgnoreCase,
		Regex:         q.Regex,
		Exact:         q.Exact,
		Fuzzy:         q.Fuzzy,
		IncludeHidden: q.IncludeHidden,
		Limit:         q.Limit,
		ClassifyFunc:  classify,
		Progress:      progress,
	}
}

// SearchesDir returns the directory holding saved searches (~/.chest/searches).
func SearchesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home directory: %w", err)
	}
	return filepath.Join(home, ".chest", "searches"), nil
}

// SanitizeSavedName rejects names that could escape the searches directory.
// Refusing beats rewriting: filepath.Base would turn "../evil" into a
// valid-looking "evil" and let the traversal attempt through looking benign.
// ':' is rejected too because Windows reads it as a drive separator or an
// alternate-data-stream marker, which would make save and load disagree about
// the same name.
func SanitizeSavedName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("saved search name is required")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\:") {
		return "", fmt.Errorf("invalid saved search name '%s': use a plain name with no path separators or colons", name)
	}
	return name, nil
}

// SavedSearchPath returns the file a saved search lives in, rejecting unsafe
// names before any path is built from them.
func SavedSearchPath(name string) (string, error) {
	clean, err := SanitizeSavedName(name)
	if err != nil {
		return "", err
	}
	dir, err := SearchesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clean+".toml"), nil
}

// SaveQuery writes q to ~/.chest/searches/<name>.toml (creating the directory
// when needed) and returns the path written. An existing search of the same
// name is overwritten, so re-saving edits it in place.
func SaveQuery(q SavedQuery) (string, error) {
	name, err := SanitizeSavedName(q.Name)
	if err != nil {
		return "", err
	}
	q.Name = name

	path, err := SavedSearchPath(name)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", dir, err)
	}

	var buf bytes.Buffer
	// A one-line comment keeps the file self-explanatory to anyone who opens
	// it in an editor, which is the whole point of storing it as TOML.
	fmt.Fprintf(&buf, "# CHEST saved search. Restore with: chest search --load %s\n", name)
	if err := toml.NewEncoder(&buf).Encode(q); err != nil {
		return "", fmt.Errorf("cannot encode saved search '%s': %w", name, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("cannot write %s: %w", path, err)
	}
	return path, nil
}

// LoadQuery reads a saved search by name. A missing search names the exact
// path it looked for, so the error tells the user where to put one.
func LoadQuery(name string) (SavedQuery, error) {
	path, err := SavedSearchPath(name)
	if err != nil {
		return SavedQuery{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if names, lerr := ListSavedQueries(); lerr == nil && len(names) > 0 {
				return SavedQuery{}, fmt.Errorf("no saved search '%s' at %s (saved: %s)", name, path, strings.Join(names, ", "))
			}
			return SavedQuery{}, fmt.Errorf("no saved search '%s' at %s", name, path)
		}
		return SavedQuery{}, fmt.Errorf("cannot read %s: %w", path, err)
	}

	var q SavedQuery
	if _, err := toml.Decode(string(data), &q); err != nil {
		return SavedQuery{}, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	// The filename is the identity: a file moved or hand-edited still loads
	// under the name the user asked for.
	q.Name = strings.TrimSuffix(name, ".toml")
	return q, nil
}

// ListSavedQueries returns the saved search names in sorted order, ignoring
// anything that is not a .toml file. A missing directory means no saved
// searches yet, which is not an error.
func ListSavedQueries() ([]string, error) {
	dir, err := SearchesDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.EqualFold(filepath.Ext(e.Name()), ".toml") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
	}
	sort.Strings(names)
	return names, nil
}
