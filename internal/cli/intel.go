package cli

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/indexer"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/spf13/cobra"
)

// refreshIndexBeforeReport decides whether an indexing walk is needed before a
// report command reads the cache, and runs it if so. It returns the target it
// walked ("" when skipped). It runs (with stale-row & exclusion purge) when:
//   - the user passed an explicit path (auto-refresh of that scope), or
//   - --refresh was given (forced), or
//   - the cache is empty (so a cleared/refreshed cache is repopulated).
//
// A bare (no-path) command can never re-walk the whole multi-root index, so for
// the empty/forced cases it targets the current working directory.
func refreshIndexBeforeReport(store *indexer.Store, root string, computeHashes bool, except []string, force bool) (string, error) {
	need := force || root != ""
	if !need {
		empty, err := store.IsEmpty()
		if err != nil {
			return "", err
		}
		need = empty
	}
	if !need {
		return "", nil
	}

	target := root
	if target == "" {
		if abs, aerr := filepath.Abs("."); aerr == nil {
			target = abs
		} else {
			target = "."
		}
	}

	if _, err := indexWithProgress(store, target, computeHashes, except); err != nil {
		return "", err
	}
	return target, nil
}

// emitReport writes a command's machine-readable report. v is serialized as
// JSON when asJSON is set; otherwise csvRows is written as CSV. The format is
// resolved as asJSON || !asCSV so that --out alone (no explicit --json/--csv)
// defaults to JSON; callers must reject the --json/--csv combination before
// calling. A non-empty out writes the bytes to that file (truncating) and
// prints a short confirmation to w; otherwise the bytes go to w.
func emitReport(w io.Writer, v any, csvRows [][]string, asJSON, asCSV bool, out string) error {
	data, err := renderReport(v, csvRows, asJSON || !asCSV)
	if err != nil {
		return err
	}
	if out != "" {
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(w, "wrote %d bytes to %s\n", len(data), out)
		return nil
	}
	_, err = w.Write(data)
	return err
}

// renderReport serializes v as two-space-indented JSON with a trailing newline,
// or the given csvRows as CSV, returning the exact bytes to emit.
func renderReport(v any, csvRows [][]string, asJSON bool) ([]byte, error) {
	if asJSON {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(b, '\n'), nil
	}
	var buf bytes.Buffer
	if err := csv.NewWriter(&buf).WriteAll(csvRows); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// csvRowsForStats builds the category CSV table: a header, one row per category
// (sorted so output is reproducible despite random map order), and a TOTAL row.
func csvRowsForStats(summary indexer.StatsSummary) [][]string {
	rows := [][]string{{"category", "files", "size_bytes"}}
	cats := make([]string, 0, len(summary.CategoryCounts))
	for c := range summary.CategoryCounts {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		rows = append(rows, []string{
			c,
			strconv.Itoa(summary.CategoryCounts[c]),
			strconv.FormatInt(summary.CategorySizes[c], 10),
		})
	}
	rows = append(rows, []string{"TOTAL", strconv.Itoa(summary.TotalFiles), strconv.FormatInt(summary.TotalSize, 10)})
	return rows
}

// csvRowsForDuplicates builds the duplicate CSV table: a header plus one row
// per file across all duplicate groups.
func csvRowsForDuplicates(groups []indexer.DuplicateGroup) [][]string {
	rows := [][]string{{"hash", "size_bytes", "path"}}
	for _, g := range groups {
		for _, f := range g.Files {
			rows = append(rows, []string{g.Hash, strconv.FormatInt(g.Size, 10), f.Path})
		}
	}
	return rows
}

func newIndexCmd() *cobra.Command {
	var (
		computeHashes bool
		clearIndex    bool
		except        []string
		watch         bool
		debounce      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "index [path]",
		Short: "Create, update, or clear local CHEST file index in SQLite",
		Long: "Scan a directory into the local SQLite index.\n\n" +
			"With --watch the index is kept in sync continuously: filesystem changes are\n" +
			"debounced and folded back into the index until Ctrl+C is pressed.",
		Example: "  chest index ./Downloads\n" +
			"  chest index --watch --debounce 250ms ./Projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := indexer.OpenOrCreate()
			if err != nil {
				return err
			}
			defer store.Close()

			if clearIndex {
				if err := store.ClearIndex(); err != nil {
					return fmt.Errorf("failed clearing index: %w", err)
				}
				fmt.Println("\x1b[1;38;5;82m✔ SQLite cache cleared successfully (files table emptied).\x1b[0m")
				return nil
			}

			path := "."
			if len(args) > 0 {
				path = args[0]
			}
			if abs, err := filepath.Abs(filepath.Clean(path)); err == nil {
				path = abs
			}

			// Continuous mode owns its own banner, signal handling and loop; the
			// one-shot path below is untouched.
			if watch {
				return runIndexWatch(cmd.Context(), cmd.OutOrStdout(), store, path, computeHashes, except, debounce)
			}

			fmt.Printf("\x1b[38;5;254mIndexing directory \x1b[38;5;220m%s\x1b[0m...\n", path)
			start := time.Now()

			count, err := indexWithProgress(store, path, computeHashes, except)
			if err != nil {
				return err
			}

			fmt.Printf("\x1b[1;38;5;82m✔ Indexed %d files\x1b[0m in %v\n", count, time.Since(start).Round(time.Millisecond))
			return nil
		},
	}

	cmd.Flags().BoolVar(&computeHashes, "hash", false, "Compute cryptographic SHA256 hashes during indexing")
	cmd.Flags().BoolVar(&clearIndex, "clear", false, "Clear and truncate cached file index records")
	cmd.Flags().StringSliceVar(&except, "except", nil,
		"Comma-separated directories/files to skip, e.g. node_modules,venv,.git (glob patterns allowed)")
	cmd.Flags().BoolVar(&watch, "watch", false, "Keep the index up to date continuously as files change (Ctrl+C to stop)")
	cmd.Flags().DurationVar(&debounce, "debounce", DefaultIndexDebounce, "How long to wait for a burst of file changes to settle before re-indexing (--watch)")
	return cmd
}

func newCleanCmd() *cobra.Command {
	var (
		purgeAll     bool
		clearHistory bool
		purgeBattery bool
		yes          bool
	)

	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Delete cached index database and temporary data",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := indexer.OpenOrCreate()
			if err != nil {
				return err
			}

			if purgeAll {
				// Confirmation prompt
				if !yes {
					what := "This will permanently delete ~/.chest/index.db"
					if clearHistory {
						what += " and ~/.chest/history.json"
					}
					fmt.Printf("\x1b[1;38;5;214m⚠ %s.\x1b[0m\n\nContinue? [y/N]: ", what)
					reader := bufio.NewReader(os.Stdin)
					resp, _ := reader.ReadString('\n')
					resp = strings.TrimSpace(strings.ToLower(resp))
					if resp != "y" && resp != "yes" {
						fmt.Println("Operation aborted.")
						return nil
					}
				}
				if err := store.DeleteDB(); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("failed removing db: %w", err)
				}
				fmt.Println("\x1b[1;38;5;82m✔ Completely removed ~/.chest/index.db database file.\x1b[0m")
			} else {
				// Confirmation prompt for index clear
				if !yes {
					what := "This will clear the cached file index"
					if clearHistory {
						what += " and ~/.chest/history.json"
					}
					fmt.Printf("\x1b[1;38;5;214m⚠ %s.\x1b[0m\n\nContinue? [y/N]: ", what)
					reader := bufio.NewReader(os.Stdin)
					resp, _ := reader.ReadString('\n')
					resp = strings.TrimSpace(strings.ToLower(resp))
					if resp != "y" && resp != "yes" {
						fmt.Println("Operation aborted.")
						return nil
					}
				}

				if err := store.ClearIndex(); err != nil {
					_ = store.Close()
					return err
				}
				fmt.Println("\x1b[1;38;5;82m✔ Cleaned index cache in SQLite. (Use --all to delete entire database file)\x1b[0m")
			}
			_ = store.Close()

			if clearHistory {
				if mgr, err := history.DefaultManager(); err == nil {
					_ = mgr.ClearHistory()
				}
				fmt.Println("\x1b[1;38;5;82m✔ Cleared undo history (~/.chest/history.json).\x1b[0m")
			}

			// Battery history is opt-in to delete, and deliberately not implied
			// by --all. The file index is a cache that the next command rebuilds
			// in seconds; recorded battery history is months of measurement that
			// nothing can recreate. A destructive flag has to name the thing it
			// destroys rather than sweeping it up behind a word like "all".
			if purgeBattery {
				if !yes {
					fmt.Printf("\x1b[1;38;5;214m⚠ This will permanently delete %s, which holds recorded battery history that cannot be recovered.\x1b[0m\n\nContinue? [y/N]: ", batteryDBPath())
					reader := bufio.NewReader(os.Stdin)
					resp, _ := reader.ReadString('\n')
					if resp := strings.TrimSpace(strings.ToLower(resp)); resp != "y" && resp != "yes" {
						fmt.Println("Operation aborted.")
						return nil
					}
				}
				// A machine that never recorded anything has no file to remove,
				// and that is a success rather than something to report as an
				// error the user has to think about.
				if err := os.Remove(batteryDBPath()); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("failed removing battery history: %w", err)
				}
				fmt.Println("\x1b[1;38;5;82m✔ Deleted recorded battery history.\x1b[0m")
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&purgeAll, "all", false, "Completely delete the ~/.chest/index.db database file")
	cmd.Flags().BoolVar(&clearHistory, "history", false, "Also delete the undo history file (~/.chest/history.json)")
	cmd.Flags().BoolVar(&purgeBattery, "battery", false, "Also delete recorded battery history (~/.chest/battery.db); not implied by --all")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompt")
	return cmd
}

// batteryDBPath is the single place the history file's location is decided, so
// the command that writes it and the command that deletes it cannot disagree.
func batteryDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".chest", "battery.db")
}

func newStatsCmd() *cobra.Command {
	var (
		except     []string
		refresh    bool
		jsonOutput bool
		csvOutput  bool
		outPath    string
		trend      bool
		day        bool
		week       bool
		month      bool
	)

	cmd := &cobra.Command{
		Use:   "stats [path]",
		Short: "Display storage and category statistics from local index",
		Long: "Display storage and category statistics from local index.\n\n" +
			"Every run records a snapshot of the totals. With --trend those snapshots are\n" +
			"compared over a --day, --week or --month window to show how storage grew.",
		Example: "  chest stats\n" +
			"  chest stats ./Downloads --trend --week",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput && csvOutput {
				return errors.New("choose one of --json or --csv")
			}
			period, err := resolveTrendPeriod(day, week, month, trend)
			if err != nil {
				return err
			}
			store, err := indexer.OpenOrCreate()
			if err != nil {
				return err
			}
			defer store.Close()

			root := ""
			if len(args) > 0 {
				root = args[0]
				if abs, err := filepath.Abs(filepath.Clean(root)); err == nil {
					root = abs
				}
			}

			// Auto-refresh a scoped path; re-populate an empty cache from the CWD;
			// --refresh forces a CWD re-scan+purge for bare runs.
			if _, err := refreshIndexBeforeReport(store, root, false, except, refresh); err != nil {
				return err
			}

			summary, err := store.GetStats(root)
			if err != nil {
				return err
			}

			// Record the snapshot on every run, not only on --trend: that is what
			// builds the history a later trend reads. Recording is best-effort
			// here so a snapshot failure can never break the plain report; in
			// --trend mode it is reported, because the requested report is
			// exactly the thing that failed.
			snapshot, snapErr := store.RecordStorageSnapshot(root, summary)

			if trend {
				if snapErr != nil {
					return fmt.Errorf("failed recording storage snapshot: %w", snapErr)
				}
				report, err := store.StorageTrend(root, period, snapshot.CapturedAt)
				if err != nil {
					return err
				}
				if jsonOutput || csvOutput || outPath != "" {
					return emitReport(cmd.OutOrStdout(), report, csvRowsForTrends(report), jsonOutput, csvOutput, outPath)
				}
				printTrends(cmd.OutOrStdout(), report)
				return nil
			}

			if jsonOutput || csvOutput || outPath != "" {
				return emitReport(cmd.OutOrStdout(), summary, csvRowsForStats(summary), jsonOutput, csvOutput, outPath)
			}

			fmt.Println("\n\x1b[1;38;5;255mCHEST STATS\x1b[0m")
			fmt.Println("─────────────────────────────────────────────────────────────")
			fmt.Printf("  \x1b[38;5;246mTotal Files:\x1b[0m   \x1b[1;38;5;82m%d\x1b[0m\n", summary.TotalFiles)
			fmt.Printf("  \x1b[38;5;246mTotal Size:\x1b[0m    \x1b[1;38;5;220m%s\x1b[0m\n\n", formatFileSize(summary.TotalSize))

			fmt.Println("  \x1b[1;38;5;254mCATEGORY BREAKDOWN:\x1b[0m")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			for cat, count := range summary.CategoryCounts {
				size := summary.CategorySizes[cat]
				fmt.Fprintf(w, "    %-12s\t%d files\t%s\n", strings.ToUpper(cat), count, formatFileSize(size))
			}
			_ = w.Flush()

			if len(summary.LargestFiles) > 0 {
				fmt.Println("\n  \x1b[1;38;5;254mLARGEST FILES:\x1b[0m")
				for i, f := range summary.LargestFiles {
					if i >= 5 {
						break
					}
					fmt.Printf("    %d. %s \x1b[38;5;246m(%s)\x1b[0m\n", i+1, f.Path, formatFileSize(f.Size))
				}
			}

			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&except, "except", nil,
		"Comma-separated directories/files to skip, e.g. node_modules,venv,.git (glob patterns allowed)")
	cmd.Flags().BoolVar(&refresh, "refresh", false,
		"Force a fresh re-index (with stale/excluded-row purge) before reporting")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output the index summary as JSON instead of the human report")
	cmd.Flags().BoolVar(&csvOutput, "csv", false, "Output the category table as CSV instead of the human report")
	cmd.Flags().StringVar(&outPath, "out", "", "Write the machine-readable report (JSON by default) to FILE instead of stdout")
	cmd.Flags().BoolVar(&trend, "trend", false, "Report how storage grew since a previous run (default window: --month)")
	cmd.Flags().BoolVar(&day, "day", false, "Compare snapshots over the last day (requires --trend)")
	cmd.Flags().BoolVar(&week, "week", false, "Compare snapshots over the last 7 days (requires --trend)")
	cmd.Flags().BoolVar(&month, "month", false, "Compare snapshots over the last 30 days (requires --trend; default)")
	return cmd
}

// resolveTrendPeriod validates the mutually-exclusive trend window flags.
// A window without --trend is an error rather than a silent no-op: the user
// asked for a comparison and would otherwise get an ordinary stats report.
func resolveTrendPeriod(day, week, month, trend bool) (indexer.TrendPeriod, error) {
	chosen := make([]string, 0, 3)
	if day {
		chosen = append(chosen, "--day")
	}
	if week {
		chosen = append(chosen, "--week")
	}
	if month {
		chosen = append(chosen, "--month")
	}
	if len(chosen) > 1 {
		return "", fmt.Errorf("choose one of %s", strings.Join(chosen, ", "))
	}
	if len(chosen) == 1 && !trend {
		return "", fmt.Errorf("%s requires --trend", chosen[0])
	}
	if !trend {
		return indexer.DefaultTrendPeriod, nil
	}
	switch {
	case day:
		return indexer.TrendPeriodDay, nil
	case week:
		return indexer.TrendPeriodWeek, nil
	default:
		return indexer.TrendPeriodMonth, nil
	}
}

// csvRowsForTrends builds the trend CSV table: a header, one row per category
// (sorted for reproducible output), and a TOTAL row carrying the whole-index
// growth figures.
func csvRowsForTrends(report indexer.StorageTrend) [][]string {
	rows := [][]string{{"category", "files_then", "files_now", "files_delta", "size_then_bytes", "size_now_bytes", "size_delta_bytes"}}
	for _, c := range report.Categories {
		rows = append(rows, []string{
			c.Category,
			strconv.Itoa(c.FilesThen),
			strconv.Itoa(c.FilesNow),
			strconv.Itoa(c.FilesDelta),
			strconv.FormatInt(c.SizeThen, 10),
			strconv.FormatInt(c.SizeNow, 10),
			strconv.FormatInt(c.SizeDelta, 10),
		})
	}
	rows = append(rows, []string{
		"TOTAL",
		strconv.Itoa(report.TotalFilesThen),
		strconv.Itoa(report.TotalFilesNow),
		strconv.Itoa(report.TotalFilesDelta),
		strconv.FormatInt(report.TotalSizeThen, 10),
		strconv.FormatInt(report.TotalSizeNow, 10),
		strconv.FormatInt(report.TotalSizeDelta, 10),
	})
	return rows
}

// signedSize renders a byte delta with an explicit sign so growth and shrink
// are distinguishable at a glance.
func signedSize(delta int64) string {
	if delta > 0 {
		return "+" + formatFileSize(delta)
	}
	if delta < 0 {
		return "-" + formatFileSize(-delta)
	}
	return "0 B"
}

// signedCount renders a file-count delta with an explicit sign.
func signedCount(delta int) string {
	if delta > 0 {
		return fmt.Sprintf("+%d", delta)
	}
	return fmt.Sprintf("%d", delta)
}

// printTrends renders the human-readable growth report.
func printTrends(w io.Writer, report indexer.StorageTrend) {
	scope := report.Scope
	if scope == "" {
		scope = "the whole index"
	}
	fmt.Fprintf(w, "\n\x1b[1;38;5;255mCHEST STORAGE TREND\x1b[0m \x1b[38;5;246m(%s, %s)\x1b[0m\n", report.Period, scope)
	fmt.Fprint(w, "─────────────────────────────────────────────────────────────\n")
	fmt.Fprintf(w, "  \x1b[38;5;246mSnapshots recorded:\x1b[0m %d\n", report.Snapshots)
	fmt.Fprintf(w, "  \x1b[38;5;246mWindow start:\x1b[0m       %s\n", report.Since.Format("2006-01-02 15:04"))

	if !report.HasBaseline || report.Now == nil {
		fmt.Fprintln(w)
		fmt.Fprint(w, "  \x1b[38;5;214mNot enough history yet.\x1b[0m\n")
		fmt.Fprintf(w, "  %s\n", "Run `chest stats` again later to record a second snapshot.")
		fmt.Fprintln(w)
		return
	}

	fmt.Fprintf(w, "  \x1b[38;5;246mThen:\x1b[0m            %s \x1b[38;5;246macross %d files\x1b[0m  \x1b[38;5;246m(%s)\x1b[0m\n",
		formatFileSize(report.TotalSizeThen), report.TotalFilesThen, report.Then.CapturedAt.Format("2006-01-02 15:04"))
	fmt.Fprintf(w, "  \x1b[38;5;246mNow:\x1b[0m             %s \x1b[38;5;246macross %d files\x1b[0m  \x1b[38;5;246m(%s)\x1b[0m\n",
		formatFileSize(report.TotalSizeNow), report.TotalFilesNow, report.Now.CapturedAt.Format("2006-01-02 15:04"))
	fmt.Fprintf(w, "  \x1b[38;5;246mDelta:\x1b[0m           %s (%s) across %s files\n",
		signedSize(report.TotalSizeDelta), fmt.Sprintf("%+.1f%%", report.TotalGrowthPct), signedCount(report.TotalFilesDelta))

	if len(report.Categories) == 0 {
		fmt.Println()
		return
	}

	fmt.Fprint(w, "\n  \x1b[38;5;254mCATEGORY GROWTH:\x1b[0m\n")
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "    %s\t%s\t%s\t%s\n", "CATEGORY", "NOW", "THEN", "DELTA")
	for _, c := range report.Categories {
		fmt.Fprintf(tw, "    %s\t%s\t%s\t%s\n",
			strings.ToUpper(c.Category),
			formatFileSize(c.SizeNow),
			formatFileSize(c.SizeThen),
			signedSize(c.SizeDelta))
	}
	_ = tw.Flush()

	fmt.Fprintln(w)
}

func newDuplicatesCmd() *cobra.Command {
	var (
		except     []string
		jsonOutput bool
		csvOutput  bool
		outPath    string
		similar    bool
		threshold  int
		quarantine string
		yes        bool
	)

	cmd := &cobra.Command{
		Use:   "duplicates [path] [--except dir,glob]",
		Short: "Find duplicate files using content hashes",
		Long: "Find duplicate files using content hashes.\n\n" +
			"--similar additionally reports near-duplicate images, which are compared by\n" +
			"perceptual hash rather than by bytes, so a resized or re-encoded copy of a\n" +
			"picture is found next to the original.\n\n" +
			"--quarantine <dir> is an explicit, opt-in action: after confirmation it MOVES\n" +
			"the extra copies of each duplicate set into <dir> and records the moves in the\n" +
			"undo history. It never deletes anything, and `chest undo` puts every file back.",
		Example: "  chest duplicates ./Photos\n" +
			"  chest duplicates ./Photos --similar --threshold 8\n" +
			"  chest duplicates ./Photos --quarantine ./dup-staging -y",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput && csvOutput {
				return errors.New("choose one of --json or --csv")
			}
			// Quarantine moves files; a report format describes them. Running both
			// from one invocation would either hide what was moved or hide the
			// groups that were not, so the combination is refused outright.
			if quarantine != "" && (jsonOutput || csvOutput || outPath != "") {
				return errors.New("--quarantine cannot be combined with --json, --csv or --out; run the report and the quarantine separately")
			}
			if threshold <= 0 {
				threshold = indexer.DefaultSimilarThreshold
			}

			store, err := indexer.OpenOrCreate()
			if err != nil {
				return err
			}
			defer store.Close()

			root := ""
			if len(args) > 0 {
				root = args[0]
				// Canonicalize to an absolute path so the exact string we index
				// under is the same one FindDuplicates() scopes its query by.
				// Mixing spellings (./x vs E:\x vs x) creates disjoint row sets,
				// so the report can leak rows from an earlier scan of another dir.
				if abs, err := filepath.Abs(filepath.Clean(root)); err == nil {
					root = abs
				}
				if _, err := indexWithProgress(store, root, true, except); err != nil {
					return err
				}
			}

			groups, err := store.FindDuplicates(root)
			if err != nil {
				return err
			}

			// Near-duplicate detection is a separate pass: it decodes images and
			// must never be mixed into, or reported as, the byte-identical
			// SHA-256 groups above. It only runs when it is actually wanted —
			// decoding a whole photo library is far too expensive to pay for a
			// report nobody asked for.
			var similarGroups []indexer.SimilarGroup
			if similar {
				similarGroups, err = store.FindSimilarImages(root, threshold)
				if err != nil {
					return err
				}
			}

			if quarantine != "" {
				return runQuarantine(cmd, store, root, quarantine, groups, similarGroups, similar, yes)
			}

			if similar {
				payload, rows := duplicatesReportPayload(groups, similarGroups, true)
				if jsonOutput || csvOutput || outPath != "" {
					return emitReport(cmd.OutOrStdout(), payload, rows, jsonOutput, csvOutput, outPath)
				}
				printDuplicates(cmd.OutOrStdout(), groups, similarGroups)
				return nil
			}

			if jsonOutput || csvOutput || outPath != "" {
				if groups == nil {
					groups = []indexer.DuplicateGroup{}
				}
				return emitReport(cmd.OutOrStdout(), groups, csvRowsForDuplicates(groups), jsonOutput, csvOutput, outPath)
			}

			if len(groups) == 0 {
				fmt.Println("\x1b[38;5;82m✔ No duplicate files found.\x1b[0m")
				return nil
			}

			var totalWasted int64
			for _, g := range groups {
				totalWasted += g.WastedSize
			}

			fmt.Printf("\n\x1b[1;38;5;214mFound %d duplicate sets\x1b[0m (Wasted space: \x1b[1;38;5;196m%s\x1b[0m)\n",
				len(groups), formatFileSize(totalWasted))
			fmt.Println("─────────────────────────────────────────────────────────────")

			for i, g := range groups {
				fmt.Printf("\n  \x1b[1;38;5;220mSet #%d\x1b[0m (Size: %s each, Hash: %s...):\n",
					i+1, formatFileSize(g.Size), g.Hash[:12])
				for _, f := range g.Files {
					fmt.Printf("    • %s\n", f.Path)
				}
			}

			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&except, "except", nil,
		"Comma-separated directories/files to skip, e.g. node_modules,venv,.git (glob patterns allowed)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output duplicate groups as JSON instead of the human report")
	cmd.Flags().BoolVar(&csvOutput, "csv", false, "Output duplicates as CSV (hash,size_bytes,path) instead of the human report")
	cmd.Flags().StringVar(&outPath, "out", "", "Write the machine-readable report (JSON by default) to FILE instead of stdout")
	cmd.Flags().BoolVar(&similar, "similar", false, "Also report near-duplicate images found by perceptual hashing")
	cmd.Flags().IntVar(&threshold, "threshold", indexer.DefaultSimilarThreshold, "Hamming distance (0-64) at which two image hashes count as near-duplicates")
	cmd.Flags().StringVar(&quarantine, "quarantine", "", "After confirmation, MOVE duplicate copies into this staging directory (reversible with 'chest undo')")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the quarantine confirmation prompt")
	return cmd
}

// duplicatesReport is the machine-readable shape used only when --similar is
// given. Without --similar the command keeps emitting the bare
// []DuplicateGroup array it always has, so existing JSON consumers are
// untouched.
type duplicatesReport struct {
	Exact   []indexer.DuplicateGroup `json:"exact"`
	Similar []indexer.SimilarGroup   `json:"similar"`
}

// csvRowsForSimilar builds the near-duplicate CSV table. The columns are
// deliberately distinct from the exact-duplicate table's so the two never get
// confused: a perceptual hash is not a content hash, and a Hamming distance is
// not a wasted byte count.
func csvRowsForSimilar(groups []indexer.SimilarGroup) [][]string {
	rows := [][]string{{"phash", "max_distance", "size_bytes", "path"}}
	for _, g := range groups {
		for _, f := range g.Files {
			rows = append(rows, []string{f.Hash, strconv.Itoa(f.Distance), strconv.FormatInt(f.Size, 10), f.Path})
		}
	}
	return rows
}

// csvRowsForSimilarMode is the combined table: a `kind` column tells a parser
// which of the two reports a row came from, and the distance column is empty
// for exact rows because byte-identical files have no distance to report.
func csvRowsForSimilarMode(groups []indexer.DuplicateGroup, similar []indexer.SimilarGroup) [][]string {
	rows := [][]string{{"kind", "hash", "size_bytes", "distance", "path"}}
	for _, g := range groups {
		for _, f := range g.Files {
			rows = append(rows, []string{"exact", g.Hash, strconv.FormatInt(g.Size, 10), "", f.Path})
		}
	}
	for _, g := range similar {
		for _, f := range g.Files {
			rows = append(rows, []string{"similar", f.Hash, strconv.FormatInt(f.Size, 10), strconv.Itoa(f.Distance), f.Path})
		}
	}
	return rows
}

// duplicatesReportPayload resolves the (JSON value, CSV rows) pair for a
// duplicates run. With similarMode off this is byte-for-byte what the command
// produced before near-duplicate detection existed.
func duplicatesReportPayload(groups []indexer.DuplicateGroup, similar []indexer.SimilarGroup, similarMode bool) (any, [][]string) {
	if groups == nil {
		groups = []indexer.DuplicateGroup{}
	}
	if !similarMode {
		return groups, csvRowsForDuplicates(groups)
	}
	if similar == nil {
		similar = []indexer.SimilarGroup{}
	}
	return duplicatesReport{Exact: groups, Similar: similar}, csvRowsForSimilarMode(groups, similar)
}

// printDuplicates renders the combined human report, keeping the two findings
// in clearly separate sections: byte-identical files and visually-alike
// pictures are different findings and must never be read as one list.
func printDuplicates(w io.Writer, groups []indexer.DuplicateGroup, similar []indexer.SimilarGroup) {
	var totalWasted int64
	for _, g := range groups {
		totalWasted += g.WastedSize
	}

	fmt.Fprint(w, "\n")
	if len(groups) == 0 {
		fmt.Fprint(w, "  \x1b[38;5;82m✔ No byte-identical duplicates found.\x1b[0m\n")
	} else {
		fmt.Fprintf(w, "\x1b[1;38;5;214mFound %d duplicate sets\x1b[0m (Wasted space: \x1b[1;38;5;196m%s\x1b[0m)\n",
			len(groups), formatFileSize(totalWasted))
		fmt.Fprint(w, "─────────────────────────────────────────────────────────────\n")
		fmt.Fprint(w, "\x1b[38;5;246m  EXACT DUPLICATES (identical bytes, SHA-256)\x1b[0m\n")
		for i, g := range groups {
			fmt.Fprintf(w, "\n  \x1b[38;5;220mSet #%d\x1b[0m (Size: %s each, Hash: %s...):\n",
				i+1, formatFileSize(g.Size), g.Hash[:12])
			for _, f := range g.Files {
				fmt.Fprintf(w, "    • %s\n", f.Path)
			}
		}
	}

	if len(similar) == 0 {
		fmt.Fprint(w, "\n  \x1b[38;5;82m✔ No near-duplicate images found.\x1b[0m\n")
	} else {
		fmt.Fprint(w, "\n─────────────────────────────────────────────────────────────\n")
		fmt.Fprintf(w, "\x1b[1;38;5;214mFound %d near-duplicate image set(s)\x1b[0m \x1b[38;5;246m(perceptual hash)\x1b[0m\n", len(similar))
		fmt.Fprint(w, "─────────────────────────────────────────────────────────────\n")
		fmt.Fprint(w, "\x1b[38;5;246m  NEAR-DUPLICATE IMAGES (look alike, NOT byte-identical)\x1b[0m\n")
		for i, g := range similar {
			fmt.Fprintf(w, "\n  \x1b[38;5;220mSet #%d\x1b[0m (%d image(s), pHash %s, max distance %d):\n",
				i+1, len(g.Files), g.Hash, g.MaxDistance)
			for _, f := range g.Files {
				fmt.Fprintf(w, "    • %s \x1b[38;5;246m(pHash %s, distance %d, %s)\x1b[0m\n",
					f.Path, f.Hash, f.Distance, formatFileSize(f.Size))
			}
		}
	}
	fmt.Fprintln(w)
}

// quarantinePlan is the set of moves a quarantine run will perform, in order.
// Nothing is executed by planning; the plan is also what gets printed to the
// user before they confirm.
type quarantinePlan struct {
	Ops []models.HistoryOperation
}

// planQuarantine decides which copies of a duplicate set get staged.
//
// The first file of each set (in sorted path order) is the survivor: it stays
// exactly where it is. Every other member is staged into its own numbered
// subdirectory of the quarantine folder so that two copies sharing a filename
// cannot collide. Near-duplicate image sets are only staged when the user
// explicitly asked for them with --similar, because a re-encoded picture is
// not a duplicate of the original and quietly moving one would surprise
// anybody.
func planQuarantine(quarantineDir string, groups []indexer.DuplicateGroup, similar []indexer.SimilarGroup, includeSimilar bool) quarantinePlan {
	plan := quarantinePlan{}
	staged := make(map[string]struct{})

	addSet := func(setName, kind string, paths []string) {
		kept := false
		idx := 0
		for _, p := range paths {
			if p == "" {
				continue
			}
			if _, dup := staged[p]; dup {
				continue
			}
			if !kept {
				kept = true
				continue
			}
			idx++
			staged[p] = struct{}{}
			plan.Ops = append(plan.Ops, models.HistoryOperation{
				OriginalSource: p,
				Destination:    filepath.Join(quarantineDir, setName, strconv.Itoa(idx), filepath.Base(p)),
				Reason:         fmt.Sprintf("duplicate copy (%s) from set %s", kind, setName),
			})
		}
	}

	for i, g := range groups {
		paths := make([]string, 0, len(g.Files))
		for _, f := range g.Files {
			paths = append(paths, f.Path)
		}
		sort.Strings(paths)
		addSet(fmt.Sprintf("exact-%02d", i+1), "identical SHA-256", paths)
	}
	if includeSimilar {
		for i, g := range similar {
			paths := make([]string, 0, len(g.Files))
			for _, f := range g.Files {
				paths = append(paths, f.Path)
			}
			sort.Strings(paths)
			addSet(fmt.Sprintf("similar-%02d", i+1), "perceptual hash", paths)
		}
	}
	return plan
}

// confirmQuarantine asks the user to approve a set of moves. It reads from in
// and writes to out so the prompt can be exercised without a terminal.
func confirmQuarantine(in io.Reader, out io.Writer, quarantineDir string, count int) bool {
	fmt.Fprintf(out, "\n\x1b[1;38;5;214m⚠ %d duplicate copy/copies will be MOVED into:\x1b[0m\n    \x1b[38;5;220m%s\x1b[0m\n",
		count, quarantineDir)
	fmt.Fprint(out, "  Nothing is deleted. Every file can be restored with 'chest undo'.\n\n")
	fmt.Fprint(out, "Continue? [y/N]: ")

	reader := bufio.NewReader(in)
	resp, _ := reader.ReadString('\n')
	resp = strings.TrimSpace(strings.ToLower(resp))
	if resp != "y" && resp != "yes" {
		fmt.Fprintln(out, "Operation aborted.")
		return false
	}
	return true
}

// executeQuarantine performs the planned moves and records them in the undo
// history. Files are moved, never removed, and only the moves that actually
// succeeded are recorded — an undo entry must never point at a file that did
// not move.
func executeQuarantine(hist *history.Manager, quarantineDir string, plan quarantinePlan) (*models.HistoryEntry, int, []string, error) {
	if err := filesystem.EnsureDir(quarantineDir); err != nil {
		return nil, 0, nil, fmt.Errorf("failed creating quarantine directory: %w", err)
	}

	var (
		moved []models.HistoryOperation
		errs  []string
	)
	for _, op := range plan.Ops {
		if !filesystem.Exists(op.OriginalSource) {
			errs = append(errs, fmt.Sprintf("%s no longer exists", op.OriginalSource))
			continue
		}
		// Never clobber a file already sitting in the staging directory.
		dst := filesystem.ResolveCollision(op.Destination)
		if err := filesystem.MoveFile(op.OriginalSource, dst); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", op.OriginalSource, err))
			continue
		}
		op.Destination = dst
		moved = append(moved, op)
	}

	if len(moved) == 0 && len(errs) == 0 {
		return nil, 0, nil, nil
	}
	entry, err := hist.Record(quarantineDir, moved, errs...)
	if err != nil {
		return nil, len(moved), errs, fmt.Errorf("failed recording undo history: %w", err)
	}
	return entry, len(moved), errs, nil
}

// pathInsideDir reports whether candidate sits inside dir (or is dir itself).
func pathInsideDir(dir, candidate string) bool {
	if dir == "" {
		return false
	}
	d := filepath.Clean(dir)
	c := filepath.Clean(candidate)
	if c == d {
		return true
	}
	return strings.HasPrefix(c, d+string(filepath.Separator))
}

// runQuarantine is the opt-in, reversible cleanup path: it stages duplicate
// copies into a directory the user names and records every move so `chest
// undo` can put them all back. Detection alone never triggers it.
func runQuarantine(cmd *cobra.Command, store *indexer.Store, root, quarantineDir string, groups []indexer.DuplicateGroup, similar []indexer.SimilarGroup, includeSimilar, yes bool) error {
	w := cmd.OutOrStdout()

	if strings.TrimSpace(quarantineDir) == "" {
		return errors.New("--quarantine requires a directory path")
	}
	abs, err := filepath.Abs(filepath.Clean(quarantineDir))
	if err != nil {
		return fmt.Errorf("invalid quarantine directory: %w", err)
	}
	if root == "" {
		if cwd, aerr := filepath.Abs("."); aerr == nil {
			root = cwd
		}
	}
	// Staging inside the scanned tree would make the staged copies themselves
	// duplicates on the next run, growing the folder without bound.
	if pathInsideDir(root, abs) {
		return fmt.Errorf("quarantine directory '%s' must be outside the scanned path '%s'", abs, root)
	}

	plan := planQuarantine(abs, groups, similar, includeSimilar)
	if len(plan.Ops) == 0 {
		fmt.Fprint(w, "\n\x1b[38;5;82m✔ Nothing to quarantine: no duplicate copies were found.\x1b[0m\n")
		return nil
	}

	if !yes {
		fmt.Fprintf(w, "\n\x1b[1;38;5;255mCHEST QUARANTINE\x1b[0m\n")
		fmt.Fprint(w, "─────────────────────────────────────────────────────────────\n")
		for _, op := range plan.Ops {
			fmt.Fprintf(w, "  \x1b[38;5;246mMOVE\x1b[0m %s\n    \x1b[38;5;246m  →\x1b[0m %s\n", op.OriginalSource, op.Destination)
		}
		if !confirmQuarantine(os.Stdin, w, abs, len(plan.Ops)) {
			return nil
		}
	}

	hist, err := history.DefaultManager()
	if err != nil {
		return fmt.Errorf("failed opening undo history: %w", err)
	}
	entry, moved, errs, err := executeQuarantine(hist, abs, plan)
	if err != nil {
		return err
	}
	if moved == 0 {
		fmt.Fprint(w, "\n\x1b[38;5;214m⚠ No files were moved.\x1b[0m\n")
		for _, e := range errs {
			fmt.Fprintf(w, "  \x1b[38;5;196m✖ %s\x1b[0m\n", e)
		}
		return nil
	}

	fmt.Fprintf(w, "\n\x1b[1;38;5;82m✔ Quarantined %d file(s)\x1b[0m into \x1b[38;5;220m%s\x1b[0m\n", moved, abs)
	for _, e := range errs {
		fmt.Fprintf(w, "  \x1b[38;5;196m✖ %s\x1b[0m\n", e)
	}
	if len(errs) > 0 {
		fmt.Fprintf(w, "  \x1b[38;5;214m%d file(s) could not be moved; the run is recorded as partial.\x1b[0m\n", len(errs))
	}
	if entry != nil && entry.ID > 0 {
		fmt.Fprintf(w, "  \x1b[38;5;246mRecorded as history #%d — restore every file with 'chest undo'.\x1b[0m\n", entry.ID)
	}
	fmt.Fprintln(w)

	// The moved files are now stale rows; refresh the scoped index so the next
	// report describes the tree as it actually is.
	if store != nil && root != "" {
		if _, ierr := indexWithProgress(store, root, true); ierr != nil {
			fmt.Fprintf(w, "  \x1b[38;5;196m✖ could not refresh the index: %v\x1b[0m\n", ierr)
		}
	}
	return nil
}

func newAnalyzeCmd() *cobra.Command {
	var (
		except     []string
		refresh    bool
		jsonOutput bool
		csvOutput  bool
		outPath    string
	)

	cmd := &cobra.Command{
		Use:   "analyze [path]",
		Short: "Analyze indexed filesystem data and report storage intelligence",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput && csvOutput {
				return errors.New("choose one of --json or --csv")
			}

			// Time the whole run (including any indexing walk) so report
			// generation shows how long the command took end-to-end.
			start := time.Now()

			store, err := indexer.OpenOrCreate()
			if err != nil {
				return err
			}
			defer store.Close()

			root := ""
			if len(args) > 0 {
				// Hash during the progress-covered walk (computeHashes=true) so the
				// duplicate phase in Analyze() reads no file contents — it becomes a
				// pure SQL GROUP BY on stored hashes. Unchanged files are still
				// skipped incrementally and reuse their cached hash.
				root = args[0]
				if abs, err := filepath.Abs(filepath.Clean(root)); err == nil {
					root = abs
				}
			}

			// Auto-refresh a scoped path; re-populate an empty cache from the CWD
			// (e.g. right after `chest clean`); --refresh forces a CWD re-scan+purge
			// for bare runs.
			if _, err := refreshIndexBeforeReport(store, root, true, except, refresh); err != nil {
				return err
			}

			machine := jsonOutput || csvOutput || outPath != ""

			// Analyze() is not instant: computing stats, scanning the duplicate
			// groups (hashing any stale-size candidates) and detecting old/empty
			// files all happen here. Show what's running so the screen isn't blank
			// between the progress walk finishing and the report being printed.
			if !machine {
				fmt.Println()
				if root == "" {
					fmt.Println("  \x1b[1;38;5;214mComputing storage insights...\x1b[0m")
				} else {
					fmt.Printf("  \x1b[1;38;5;214mComputing storage insights for %s...\x1b[0m\n", root)
				}
			}
			report, err := analyzeWithProgress(store, root)
			if err != nil {
				return err
			}

			if machine {
				return emitReport(cmd.OutOrStdout(), report, csvRowsForStats(report.Stats), jsonOutput, csvOutput, outPath)
			}

			fmt.Println("\n\x1b[1;38;5;255mCHEST ANALYSIS REPORT\x1b[0m")
			fmt.Println("─────────────────────────────────────────────────────────────")
			fmt.Printf("  \x1b[38;5;246mFiles Scanned:\x1b[0m     %d\n", report.Stats.TotalFiles)
			fmt.Printf("  \x1b[38;5;246mTotal Storage:\x1b[0m     %s\n", formatFileSize(report.Stats.TotalSize))
			fmt.Printf("  \x1b[38;5;246mDuplicate Sets:\x1b[0m    %d\n", len(report.DuplicateGroups))
			fmt.Printf("  \x1b[38;5;246mOld Files (>180d):\x1b[0m %d\n", len(report.OldFiles))
			fmt.Printf("  \x1b[38;5;246mEmpty Files (0B):\x1b[0m  %d\n", len(report.EmptyFiles))

			if len(report.Stats.LargestFiles) > 0 {
				fmt.Println("\n  \x1b[1;38;5;254mTop Storage Consumers:\x1b[0m")
				for i, f := range report.Stats.LargestFiles {
					if i >= 3 {
						break
					}
					fmt.Printf("    • %s (%s)\n", f.Path, formatFileSize(f.Size))
				}
			}

			fmt.Println()
			fmt.Printf("  \x1b[38;5;246mCompleted in:\x1b[0m      %v\n", time.Since(start).Round(time.Millisecond))
			fmt.Println("\x1b[1;38;5;82m✔ Analysis complete.\x1b[0m")
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&except, "except", nil,
		"Comma-separated directories/files to skip, e.g. node_modules,venv,.git (glob patterns allowed)")
	cmd.Flags().BoolVar(&refresh, "refresh", false,
		"Force a fresh re-index (with stale/excluded-row purge) before reporting")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output the analysis report as JSON instead of the human report")
	cmd.Flags().BoolVar(&csvOutput, "csv", false, "Output the category table as CSV instead of the human report")
	cmd.Flags().StringVar(&outPath, "out", "", "Write the machine-readable report (JSON by default) to FILE instead of stdout")
	return cmd
}
