package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/pflag"
)

// treeFlags is the standing side of `chest tree`'s exclusion rules, stored as
// TOML at ~/.chest/tree_flags.toml. --exclude is deliberately not part of it: a
// skip for one run and a standing decision are different things, and only the
// standing ones belong on disk.
type treeFlags struct {
	// Implicit names are skipped by every run until --remove takes them off.
	Implicit []string `toml:"implicit"`
	// Explicit names are drawn by every run even when they are implicit or
	// caught by --exclude: showing beats hiding.
	Explicit []string `toml:"explicit"`
}

// treeImplicitDefault seeds the implicit list while no file exists yet, so
// .git is skipped without ever being typed. The first save writes it down
// like any other entry, which is what lets `--remove .git` work afterwards.
var treeImplicitDefault = []string{".git"}

// treeFlagsPath is where the saved lists live, following the same ~/.chest
// convention as history, index.db and saved searches.
func treeFlagsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home directory: %w", err)
	}
	return filepath.Join(home, ".chest", "tree_flags.toml"), nil
}

// loadTreeFlags reads the saved lists. No file yet means the defaults: the
// absence of state is not the absence of rules. A file that exists but cannot
// be read or parsed is an error naming it — silently drawing a tree from the
// wrong exclusions is worse than refusing to draw one.
func loadTreeFlags() (treeFlags, error) {
	path, err := treeFlagsPath()
	if err != nil {
		return treeFlags{Implicit: append([]string(nil), treeImplicitDefault...)}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return treeFlags{Implicit: append([]string(nil), treeImplicitDefault...)}, nil
		}
		return treeFlags{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	var f treeFlags
	if _, err := toml.Decode(string(data), &f); err != nil {
		return treeFlags{}, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	return f, nil
}

// saveTreeFlags writes the lists back to ~/.chest/tree_flags.toml and returns
// the path written. An existing file is replaced wholesale: this command is
// the owner of the file, and merging a stale copy would resurrect names the
// user just removed.
func saveTreeFlags(f treeFlags) (string, error) {
	path, err := treeFlagsPath()
	if err != nil {
		return "", err
	}
	if f.Implicit == nil {
		f.Implicit = []string{}
	}
	if f.Explicit == nil {
		f.Explicit = []string{}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", dir, err)
	}
	var buf bytes.Buffer
	// A one-line header keeps the file self-explanatory to anyone who opens
	// it in an editor, the same reason saved searches are commented.
	fmt.Fprintln(&buf, "# CHEST tree flags. Maintained by: chest tree --implicit | --explicit | --remove")
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return "", fmt.Errorf("cannot encode tree flags: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("cannot write %s: %w", path, err)
	}
	return path, nil
}

// addTreeFlag appends the names not already on the list. Matching is
// case-insensitive because Excluder matches that way too: "NodeModules" and
// "node_modules" are the same rule to every part of the system that reads it.
func addTreeFlag(list, names []string) []string {
	for _, name := range names {
		if !containsTreeFlag(list, name) {
			list = append(list, name)
		}
	}
	return list
}

// removeTreeFlag drops every mention of names from the list. Removing a name
// that was never saved is a no-op, not an error: the next run shows it either
// way and there is nothing the user must fix.
func removeTreeFlag(list, names []string) []string {
	var kept []string
	for _, v := range list {
		if !containsTreeFlag(names, v) {
			kept = append(kept, v)
		}
	}
	return kept
}

func containsTreeFlag(list []string, name string) bool {
	name = strings.TrimSpace(name)
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), name) {
			return true
		}
	}
	return false
}

// treeFlagsEqual reports whether two flag files carry the same rules; it is
// what decides whether a run has anything to write. Order counts, because both
// lists are only ever appended to or filtered in place, so equal rules arrive
// in equal order.
func treeFlagsEqual(a, b treeFlags) bool {
	return sameTreeList(a.Implicit, b.Implicit) && sameTreeList(a.Explicit, b.Explicit)
}

func sameTreeList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(strings.TrimSpace(a[i]), strings.TrimSpace(b[i])) {
			return false
		}
	}
	return true
}

// treeFlagEvent records one write to a list flag together with the number of
// bare (non-flag) arguments pflag had consumed at that moment. The gap between
// consecutive events is what ties trailing bare arguments — which pflag hands
// back as plain positionals — to the flag they were written after.
type treeFlagEvent struct {
	flag  string
	split int
}

// treeListFlag is the pflag.Value behind --exclude, --implicit, --explicit and
// --remove. pflag ever hands a value flag a single argument, so the canonical
// form `--exclude a b c` leaves b and c as positionals; recording the
// positional count at Set time is how the command recovers them afterwards.
type treeListFlag struct {
	name   string
	dst    *[]string
	events *[]treeFlagEvent
	fs     *pflag.FlagSet
}

// String renders the values as a comma-separated list, the same shape
// pflag's own stringSlice uses; it is also the default shown in --help.
func (v *treeListFlag) String() string {
	if v.dst == nil {
		return ""
	}
	return strings.Join(*v.dst, ",")
}

// Set accepts one argument, which may itself be a comma-separated run, so the
// older `--exclude a,b` form keeps working alongside `--exclude a b c`.
func (v *treeListFlag) Set(raw string) error {
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			*v.dst = append(*v.dst, p)
		}
	}
	*v.events = append(*v.events, treeFlagEvent{flag: v.name, split: len(v.fs.Args())})
	return nil
}

// Type names the flag's value shape; pflag prints it as <strings> in --help.
func (v *treeListFlag) Type() string { return "stringSlice" }

// resolveTreeArgs works out which positional argument is the directory and
// which ones are trailing values of a list flag. pflag returns the bare
// arguments without their positions, but every event remembers how many were
// already seen when it fired, so each argument belongs to the last flag
// written before it. The rules, in order:
//
//   - No list flag: the one bare argument is the directory, as it always was;
//     a second one has nowhere to go and is rejected by name.
//   - A single bare argument with nothing in front of the first list flag: it
//     is the directory — `chest tree -x node_modules <path>` worked before
//     these flags took multiple values, and must keep working.
//   - Otherwise: the directory is the first bare argument when it precedes the
//     first list flag, and the current directory when it does not; every
//     remaining bare argument goes to the flag it followed.
func resolveTreeArgs(pos []string, events []treeFlagEvent, dst map[string]*[]string) (string, error) {
	if len(pos) == 0 {
		return ".", nil
	}
	if len(events) == 0 {
		if len(pos) > 1 {
			return "", fmt.Errorf("unexpected argument %q: the only positional argument is the path; put names after --exclude, --implicit, --explicit or --remove", pos[1])
		}
		return pos[0], nil
	}
	if len(pos) == 1 && events[0].split == 0 {
		return pos[0], nil
	}
	start := 0
	path := "."
	if events[0].split > 0 {
		if events[0].split > 1 {
			return "", fmt.Errorf("unexpected argument %q: the directory comes first; names go after --exclude, --implicit, --explicit or --remove", pos[1])
		}
		path = pos[0]
		start = 1
	}
	for j := start; j < len(pos); j++ {
		// The last flag fired at or before this argument is the one it
		// trails; with equal splits the nearer flag wins, which is what
		// "listed after" means to a reader of the command line.
		name := ""
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].split <= j {
				name = events[i].flag
				break
			}
		}
		slot, ok := dst[name]
		if !ok {
			return "", fmt.Errorf("cannot attach argument %q to a list flag", pos[j])
		}
		*slot = append(*slot, pos[j])
	}
	return path, nil
}
