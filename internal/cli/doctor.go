package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/Aswanidev-vs/chest/internal/config"
	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/manifest"
	"github.com/Aswanidev-vs/chest/internal/presets"
	"github.com/Aswanidev-vs/chest/internal/rules"
	"github.com/Aswanidev-vs/chest/internal/scheduler"
	_ "github.com/ncruces/go-sqlite3/driver" // read-only access to the CHEST index
	"github.com/spf13/cobra"
)

// doctorSampleLimit bounds how many example paths a single check prints. A
// health check that dumps ten thousand stale rows has stopped being a health
// check; the count beside the sample is what carries the real magnitude.
const doctorSampleLimit = 5

// Check statuses. "ok" is a clean result; "warn" is something a person should
// look at; "fail" is something that will break the next CHEST run.
const (
	doctorOK   = "ok"
	doctorWarn = "warn"
	doctorFail = "fail"
)

// doctorCheck is one line of the health checklist.
type doctorCheck struct {
	Name string `json:"name"`
	// Status is ok, warn or fail.
	Status string `json:"status"`
	// Count is how many items the check looked at, so "checked 12 directories,
	// 1 unreadable" is distinguishable from "checked nothing".
	Count int `json:"count"`
	// Detail is one line naming the outcome.
	Detail string `json:"detail"`
	// Samples are up to doctorSampleLimit example paths.
	Samples []string `json:"samples,omitempty"`
}

// doctorReport is the whole `chest doctor` result.
type doctorReport struct {
	Root string `json:"root"`
	// Checks is the checklist, in a fixed order so two runs over an unchanged
	// machine produce the same document.
	Checks []doctorCheck `json:"checks"`
	// FilesOnDisk and IndexedFiles say how much of the tree each side of the
	// freshness comparison could see.
	FilesOnDisk  int    `json:"files_on_disk"`
	IndexedFiles int    `json:"indexed_files"`
	Warnings     int    `json:"warnings"`
	Failures     int    `json:"failures"`
	Unchecked    int    `json:"unchecked"`
	Healthy      bool   `json:"healthy"`
	ReadOnlyNote string `json:"read_only_note"`
}

func newDoctorCmd() *cobra.Command {
	var (
		jsonOutput bool
		csvOutput  bool
		outPath    string
		strict     bool
		exclude    []string
	)

	cmd := &cobra.Command{
		Use:   "doctor [path]",
		Short: "Check CHEST's index, history, schedules and rules for problems",
		Long: `Run a read-only health check over everything CHEST keeps state in.

One pass reports:
  · index freshness  — rows whose files no longer exist, and files on disk
                        that the index has never seen
  · orphan history   — recorded moves whose files are no longer where they were
  · directories      — folders that could not be read during the walk
  · symlinks         — links whose target is gone
  · schedules        — saved jobs with an invalid cron, missing directory or
                        unknown preset
  · rules            — malformed chest.toml, presets and --rule strings

Nothing is written, moved, repaired or re-indexed. The SQLite index is opened
with mode=ro, so the database file itself cannot be modified even in principle,
and running this command twice in a row cannot make a finding worse. Reading
CHEST's own state may create its state directory (~/.chest) if it is not there
yet — that is where the history and schedule files live, and the public readers
for them expect that directory to exist.

Warnings are informational: the command exits 0 even when it finds problems.
Pass --strict to exit non-zero on any warning or failure, which is what a CI or
pre-flight check wants.`,
		Example: `  chest doctor
  chest doctor ~/Documents
  chest doctor . --json
  chest doctor --csv --out health.csv
  chest doctor --strict`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput && csvOutput {
				return errors.New("choose one of --json or --csv")
			}
			root := "."
			if len(args) > 0 {
				root = args[0]
			}
			abs, err := filepath.Abs(filepath.Clean(root))
			if err != nil {
				return fmt.Errorf("resolving %s: %w", root, err)
			}

			report, err := runDoctor(abs, exclude)
			if err != nil {
				return err
			}

			machine := jsonOutput || csvOutput || outPath != ""
			if machine {
				if err := emitReport(cmd.OutOrStdout(), report, csvRowsForDoctor(report), jsonOutput, csvOutput, outPath); err != nil {
					return err
				}
			} else {
				printDoctorReport(cmd.OutOrStdout(), report)
			}

			if strict && (report.Warnings > 0 || report.Failures > 0) {
				return fmt.Errorf("doctor found %d warning(s) and %d failure(s) under --strict", report.Warnings, report.Failures)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output the health report as JSON instead of the human checklist")
	cmd.Flags().BoolVar(&csvOutput, "csv", false, "Output the checklist as CSV instead of the human checklist")
	cmd.Flags().StringVar(&outPath, "out", "", "Write the machine-readable report (JSON by default) to FILE instead of stdout")
	cmd.Flags().BoolVar(&strict, "strict", false, "Exit non-zero when any check warns or fails")
	cmd.Flags().StringSliceVarP(&exclude, "exclude", "x", nil,
		"Comma-separated directories/files to skip during the disk walk, e.g. node_modules,.git")
	return cmd
}

// runDoctor performs every check and assembles the report. The order of the
// appended checks is the order they print in.
func runDoctor(root string, exclude []string) (*doctorReport, error) {
	rep := &doctorReport{
		Root:         root,
		ReadOnlyNote: "no writes: the index is opened mode=ro; no file is written, moved or deleted",
	}

	walk := walkTreeForDoctor(root, exclude)
	rep.FilesOnDisk = walk.files
	rep.Unchecked = walk.excluded

	freshness, indexed := checkIndexFreshness(root, walk.onDisk)
	rep.IndexedFiles = indexed
	rep.Checks = append(rep.Checks, checkIndexPresence(), freshness)

	rep.Checks = append(rep.Checks,
		checkHistory(),
		checkDirectories(walk),
		checkSymlinks(walk),
		checkSchedules(),
		checkRules(root),
	)

	for _, c := range rep.Checks {
		switch c.Status {
		case doctorFail:
			rep.Failures++
		case doctorWarn:
			rep.Warnings++
		}
	}
	rep.Healthy = rep.Warnings == 0 && rep.Failures == 0
	return rep, nil
}

// doctorWalk is what the disk pass observed, kept so the index comparison and
// the directory/symlink checks can share one traversal instead of each walking
// the tree again.
type doctorWalk struct {
	// onDisk is the set of absolute file paths the walk saw.
	onDisk map[string]struct{}
	files  int
	// dirs is every directory the walk read.
	dirs int
	// unreadable lists paths whose listing or metadata could not be read.
	unreadable []string
	// brokenSymlinks lists links whose target does not resolve.
	brokenSymlinks []string
	// excluded counts entries dropped by -x/--exclude.
	excluded int
}

// walkTreeForDoctor reads the tree once, read-only: listings and metadata, no
// file contents and no symlink following.
func walkTreeForDoctor(root string, exclude []string) *doctorWalk {
	w := &doctorWalk{onDisk: make(map[string]struct{})}
	excl := manifest.NewExcluder(exclude)

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			w.unreadable = append(w.unreadable, path)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		if excl.Match(d.Name(), filepath.ToSlash(rel)) {
			w.excluded++
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			w.dirs++
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			w.unreadable = append(w.unreadable, path)
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if _, serr := os.Stat(path); serr != nil {
				w.brokenSymlinks = append(w.brokenSymlinks, path)
			}
			// A link is not a file the index would hold, so it is counted as a
			// link and left out of the file set.
			return nil
		}
		w.onDisk[path] = struct{}{}
		w.files++
		return nil
	})

	sort.Strings(w.unreadable)
	sort.Strings(w.brokenSymlinks)
	return w
}

// checkIndexPresence reports whether there is an index at all. It never creates
// one: running the health check on a fresh machine must not leave CHEST state
// behind as a side effect.
func checkIndexPresence() doctorCheck {
	path := indexDBPath()
	if _, err := os.Stat(path); err != nil {
		return doctorCheck{
			Name:   "index",
			Status: doctorWarn,
			Detail: fmt.Sprintf("no index at %s — run 'chest index <dir>' to build one", path),
		}
	}
	return doctorCheck{
		Name:   "index",
		Status: doctorOK,
		Detail: fmt.Sprintf("found at %s", path),
	}
}

// checkIndexFreshness compares the cached rows against the disk in both
// directions: rows whose files are gone, and files the index never recorded.
// It opens the database read-only and issues SELECTs only, so a diagnostic run
// cannot leave a write-ahead log or a migration behind.
func checkIndexFreshness(root string, onDisk map[string]struct{}) (doctorCheck, int) {
	path := indexDBPath()
	if _, err := os.Stat(path); err != nil {
		return doctorCheck{
			Name:   "index-freshness",
			Status: doctorWarn,
			Count:  len(onDisk),
			Detail: "skipped: no index to compare against",
		}, 0
	}

	db, err := openIndexReadOnly(path)
	if err != nil {
		return doctorCheck{
			Name:   "index-freshness",
			Status: doctorFail,
			Detail: fmt.Sprintf("cannot read the index: %v", err),
		}, 0
	}
	defer db.Close()

	rows, err := db.Query("SELECT path FROM files")
	if err != nil {
		return doctorCheck{
			Name:   "index-freshness",
			Status: doctorFail,
			Detail: fmt.Sprintf("cannot read the files table: %v", err),
		}, 0
	}
	defer rows.Close()

	var indexedPaths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err == nil {
			indexedPaths = append(indexedPaths, p)
		}
	}
	if err := rows.Err(); err != nil {
		return doctorCheck{
			Name:   "index-freshness",
			Status: doctorFail,
			Detail: fmt.Sprintf("reading the files table failed: %v", err),
		}, len(indexedPaths)
	}

	// The index may cover several roots, so staleness is decided by existence,
	// not by membership of the tree this run walked. Scoping it to <path> would
	// report every row from an unrelated folder as missing.
	indexed := make(map[string]struct{}, len(indexedPaths))
	var stale []string
	for _, p := range indexedPaths {
		clean := filepath.Clean(p)
		indexed[clean] = struct{}{}
		if _, err := os.Lstat(clean); err != nil {
			stale = append(stale, clean)
		}
	}

	// The other direction: files this run saw that no row names. The row paths
	// are already in memory, so this is a map lookup per file rather than a
	// query per file — a doctor run over a large tree stays a walk, not a
	// thousand round trips.
	var unindexed []string
	for p := range onDisk {
		if !underRoot(root, p) {
			continue
		}
		if _, ok := indexed[filepath.Clean(p)]; !ok {
			unindexed = append(unindexed, p)
		}
	}
	sort.Strings(stale)
	sort.Strings(unindexed)

	switch {
	case len(stale) == 0 && len(unindexed) == 0:
		return doctorCheck{
			Name:   "index-freshness",
			Status: doctorOK,
			Count:  len(indexedPaths),
			Detail: fmt.Sprintf("%d indexed rows, all present; %d files under %s, all indexed", len(indexedPaths), len(onDisk), root),
		}, len(indexedPaths)
	case len(stale) == 0:
		return doctorCheck{
			Name:    "index-freshness",
			Status:  doctorWarn,
			Count:   len(indexedPaths),
			Detail:  fmt.Sprintf("%d file(s) under %s are not in the index — run 'chest index %s'", len(unindexed), root, root),
			Samples: samplePaths(unindexed),
		}, len(indexedPaths)
	case len(unindexed) == 0:
		return doctorCheck{
			Name:    "index-freshness",
			Status:  doctorWarn,
			Count:   len(indexedPaths),
			Detail:  fmt.Sprintf("%d indexed row(s) point at files that no longer exist — run 'chest index %s' to purge them", len(stale), root),
			Samples: samplePaths(stale),
		}, len(indexedPaths)
	default:
		return doctorCheck{
			Name:    "index-freshness",
			Status:  doctorWarn,
			Count:   len(indexedPaths),
			Detail:  fmt.Sprintf("%d stale row(s) and %d unindexed file(s) under %s — run 'chest index %s'", len(stale), len(unindexed), root, root),
			Samples: samplePaths(append(append([]string{}, stale...), unindexed...)),
		}, len(indexedPaths)
	}
}

// checkHistory looks for recorded moves whose files are no longer where the
// entry says they are. Such an entry cannot be undone, which is exactly the
// situation where a user is most likely to need to undo.
func checkHistory() doctorCheck {
	mgr, err := history.DefaultManager()
	if err != nil {
		return doctorCheck{
			Name:   "history",
			Status: doctorWarn,
			Detail: fmt.Sprintf("cannot open the history store: %v", err),
		}
	}
	entries, err := mgr.List()
	if err != nil {
		return doctorCheck{
			Name:   "history",
			Status: doctorFail,
			Detail: fmt.Sprintf("cannot read the history store: %v", err),
		}
	}
	if len(entries) == 0 {
		return doctorCheck{
			Name:   "history",
			Status: doctorOK,
			Detail: "no recorded moves",
		}
	}

	var orphaned []string
	ops := 0
	for _, e := range entries {
		ops += len(e.Operations)
		for _, op := range e.Operations {
			// An undone entry is only undoable again if the file came back to
			// where it started, so the two states check opposite ends.
			want := op.Destination
			if e.Status == "Undone" {
				want = op.OriginalSource
			}
			if want == "" {
				continue
			}
			if _, err := os.Lstat(want); err != nil {
				orphaned = append(orphaned, fmt.Sprintf("#%d %s", e.ID, want))
			}
		}
	}
	if len(orphaned) == 0 {
		return doctorCheck{
			Name:   "history",
			Status: doctorOK,
			Count:  len(entries),
			Detail: fmt.Sprintf("%d entr(ies), %d operation(s), all targets present", len(entries), ops),
		}
	}
	return doctorCheck{
		Name:    "history",
		Status:  doctorWarn,
		Count:   len(entries),
		Detail:  fmt.Sprintf("%d of %d operation(s) in %d entr(ies) point at files that are gone — those entries cannot be undone", len(orphaned), ops, len(entries)),
		Samples: samplePaths(orphaned),
	}
}

func checkDirectories(w *doctorWalk) doctorCheck {
	if len(w.unreadable) > 0 {
		return doctorCheck{
			Name:    "directories",
			Status:  doctorWarn,
			Count:   w.dirs + len(w.unreadable),
			Detail:  fmt.Sprintf("%d directory listing(s) could not be read; %d folder(s) were walked", len(w.unreadable), w.dirs),
			Samples: samplePaths(w.unreadable),
		}
	}
	return doctorCheck{
		Name:   "directories",
		Status: doctorOK,
		Count:  w.dirs,
		Detail: fmt.Sprintf("%d folder(s) walked, all readable", w.dirs),
	}
}

func checkSymlinks(w *doctorWalk) doctorCheck {
	if len(w.brokenSymlinks) > 0 {
		return doctorCheck{
			Name:    "symlinks",
			Status:  doctorWarn,
			Count:   len(w.brokenSymlinks),
			Detail:  fmt.Sprintf("%d broken symlink(s) — the target does not exist", len(w.brokenSymlinks)),
			Samples: samplePaths(w.brokenSymlinks),
		}
	}
	return doctorCheck{
		Name:   "symlinks",
		Status: doctorOK,
		Detail: "no broken symlinks",
	}
}

// checkSchedules validates every saved job: the cron expression parses, the
// directory it targets still exists, and its preset still resolves. A schedule
// that fails any of these will fail silently at 2am.
func checkSchedules() doctorCheck {
	mgr, err := scheduler.DefaultManager()
	if err != nil {
		return doctorCheck{
			Name:   "schedules",
			Status: doctorWarn,
			Detail: fmt.Sprintf("cannot open the schedule store: %v", err),
		}
	}
	schedules, err := mgr.List()
	if err != nil {
		return doctorCheck{
			Name:   "schedules",
			Status: doctorFail,
			Detail: fmt.Sprintf("cannot read the schedule store: %v", err),
		}
	}
	if len(schedules) == 0 {
		return doctorCheck{
			Name:   "schedules",
			Status: doctorOK,
			Detail: "no schedules saved",
		}
	}

	var problems []string
	for _, s := range schedules {
		if err := scheduler.ValidateCron(s.Cron); err != nil {
			problems = append(problems, fmt.Sprintf("%s invalid cron: %v", s.ID, err))
		}
		if s.Directory == "" {
			problems = append(problems, fmt.Sprintf("%s has no directory", s.ID))
		} else if info, err := os.Stat(s.Directory); err != nil || !info.IsDir() {
			problems = append(problems, fmt.Sprintf("%s directory %s is gone", s.ID, s.Directory))
		}
		if s.Preset != "" {
			if _, err := presets.LoadPreset(s.Preset); err != nil {
				problems = append(problems, fmt.Sprintf("%s unknown preset %q", s.ID, s.Preset))
			}
		}
	}
	if len(problems) == 0 {
		return doctorCheck{
			Name:   "schedules",
			Status: doctorOK,
			Count:  len(schedules),
			Detail: fmt.Sprintf("%d schedule(s), all valid", len(schedules)),
		}
	}
	return doctorCheck{
		Name:    "schedules",
		Status:  doctorFail,
		Count:   len(schedules),
		Detail:  fmt.Sprintf("%d of %d schedule(s) will fail when they fire", len(problems), len(schedules)),
		Samples: samplePaths(problems),
	}
}

// checkRules resolves the same rule set `chest sort` would build for this
// directory and validates it, so a typo in chest.toml surfaces here instead of
// silently sorting every file into the wrong folder.
func checkRules(root string) doctorCheck {
	resolved, _, err := config.Resolve(config.Sources{Target: root}, config.Hooks{})
	if err != nil {
		return doctorCheck{
			Name:   "rules",
			Status: doctorFail,
			Detail: fmt.Sprintf("cannot resolve the rule set: %v", err),
		}
	}
	problems := rules.ValidateRules(resolved)
	if len(problems) > 0 {
		samples := make([]string, 0, len(problems))
		for _, p := range problems {
			samples = append(samples, p.Error())
		}
		return doctorCheck{
			Name:    "rules",
			Status:  doctorFail,
			Count:   len(resolved),
			Detail:  fmt.Sprintf("%d invalid rule(s) among %d resolved", len(problems), len(resolved)),
			Samples: samplePaths(samples),
		}
	}
	return doctorCheck{
		Name:   "rules",
		Status: doctorOK,
		Count:  len(resolved),
		Detail: fmt.Sprintf("%d rule(s) resolved for %s, all valid", len(resolved), root),
	}
}

// csvRowsForDoctor builds the checklist CSV: one row per check, then a TOTAL
// row, so the export answers the same question the printed checklist does.
func csvRowsForDoctor(rep *doctorReport) [][]string {
	rows := [][]string{{"check", "status", "count", "detail", "samples"}}
	for _, c := range rep.Checks {
		rows = append(rows, []string{
			c.Name, c.Status, strconv.Itoa(c.Count), c.Detail, strings.Join(c.Samples, " | "),
		})
	}
	rows = append(rows, []string{
		"TOTAL", "warnings", strconv.Itoa(rep.Warnings), "checks reporting a warning", "",
	})
	rows = append(rows, []string{
		"TOTAL", "failures", strconv.Itoa(rep.Failures), "checks reporting a failure", "",
	})
	return rows
}

// printDoctorReport draws the checklist: a tick per clean check, a cross per
// problem, and the count beside every line so a clean check says what it
// actually looked at.
func printDoctorReport(w io.Writer, rep *doctorReport) {
	fmt.Fprintln(w, "\n\x1b[1;38;5;255mCHEST DOCTOR\x1b[0m")
	fmt.Fprintln(w, "─────────────────────────────────────────────────────────────")
	fmt.Fprintf(w, "  \x1b[38;5;246mDirectory:\x1b[0m %s\n", rep.Root)
	fmt.Fprintf(w, "  \x1b[38;5;246mWalked:\x1b[0m   %d files\n", rep.FilesOnDisk)
	fmt.Fprintf(w, "  \x1b[38;5;246mIndexed:\x1b[0m   %d rows\n\n", rep.IndexedFiles)

	for _, c := range rep.Checks {
		mark := "\x1b[1;38;5;82m✔"
		if c.Status == doctorFail {
			mark = "\x1b[38;5;196m✘"
		} else if c.Status == doctorWarn {
			mark = "\x1b[38;5;214m!"
		}
		fmt.Fprintf(w, "  %s %-18s \x1b[38;5;246m%s\x1b[0m\n", mark, c.Name, c.Detail)
		for _, s := range c.Samples {
			fmt.Fprintf(w, "      \x1b[38;5;246m· %s\x1b[0m\n", s)
		}
	}

	fmt.Fprintln(w)
	if rep.Unchecked > 0 {
		fmt.Fprintf(w, "  \x1b[38;5;246m· %d entries were excluded by -x/--exclude and not checked.\x1b[0m\n", rep.Unchecked)
	}
	if rep.Healthy {
		fmt.Fprintf(w, "\n\x1b[1;38;5;82m✔ Healthy.\x1b[0m \x1b[38;5;246m%s\x1b[0m\n\n", rep.ReadOnlyNote)
	} else {
		fmt.Fprintf(w, "\n\x1b[38;5;214m%d warning(s), %d failure(s).\x1b[0m \x1b[38;5;246m%s\x1b[0m\n\n",
			rep.Warnings, rep.Failures, rep.ReadOnlyNote)
	}
}

// samplePaths truncates a finding list to doctorSampleLimit entries and says
// how many were left out, so a bounded sample never reads as the whole answer.
func samplePaths(items []string) []string {
	if len(items) <= doctorSampleLimit {
		return items
	}
	out := append([]string{}, items[:doctorSampleLimit]...)
	return append(out, fmt.Sprintf("… and %d more", len(items)-doctorSampleLimit))
}

// indexDBPath is where CHEST keeps its SQLite index. It mirrors the path the
// indexer builds in OpenOrCreate; doctor has to name the file in its output and
// cannot ask the store, whose path is unexported.
func indexDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".chest", "index.db")
}

// openIndexReadOnly opens the SQLite index with mode=ro. Read-only here is not
// a promise made in the calling code — it is a property the database engine
// enforces, so the health check cannot become the thing that damages the cache
// it was asked to inspect.
//
// SQLite's own transient -wal/-shm sidecars may appear beside the database when
// a WAL-mode file is read; that is the engine's read path, not a modification,
// and index.db itself is left byte-identical. Skipping the WAL machinery with
// immutable=1 would avoid even that, but it would also make a committed-yet-
// uncheckpointed transaction invisible, so this code takes the correct read
// over the tidy one.
func openIndexReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", sqliteReadOnlyURI(path))
	if err != nil {
		return nil, err
	}
	// One connection: the driver serializes writes anyway, and a health check
	// gains nothing from concurrency it would only have to queue.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// sqliteReadOnlyURI renders a path as a SQLite URI carrying mode=ro.
//
// Windows paths are backslash-separated and drive-lettered, neither of which is
// valid URI syntax, so they are slash-normalized and passed through verbatim:
// "file:C:/Users/…/index.db?mode=ro" is the form SQLite's URI parser accepts on
// Windows (the "/C:/…" spelling is not — it parses "C:" as an authority). A
// POSIX path already begins with a slash, so the same concatenation yields the
// canonical "file:/home/…/index.db?mode=ro".
func sqliteReadOnlyURI(path string) string {
	return "file:" + filepath.ToSlash(filepath.Clean(path)) + "?mode=ro"
}

// underRoot reports whether path is root or sits beneath it. Only the tree the
// user named is compared against the index; a row belonging to another root is
// counted but never judged, because calling it missing would be a false alarm.
// Case is folded on the platforms whose filesystems ignore it, mirroring the
// indexer, so a differently-cased spelling of a path is not reported as drift.
func underRoot(root, path string) bool {
	if root == "" {
		return true
	}
	r := filepath.Clean(root)
	p := filepath.Clean(path)
	if p == r || strings.EqualFold(p, r) {
		return true
	}
	pref := r + string(filepath.Separator)
	fold := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	if fold {
		return len(p) > len(pref) && strings.EqualFold(p[:len(pref)], pref)
	}
	return strings.HasPrefix(p, pref)
}
