package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/spf13/cobra"
)

func newHistoryCmd() *cobra.Command {
	var (
		jsonOutput bool
		csvOutput  bool
		outPath    string
		prune      bool
		keep       int
		maxEntries int
	)

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show previous file organization operations",
		Long: `Show previous file organization operations.

--prune trims the list: the oldest entries are dropped so that only the newest
--keep remain, and the number removed is reported. The newest entries are never
dropped, and a pruned operation ID is never reused.

--max-entries does the same pruning and additionally stores the limit, so every
later run stops recording past it and the history file cannot grow without
bound. Use one of them, or both.`,
		Example: `  chest history
  chest history --json
  chest history --prune --keep 50
  chest history --max-entries 500
  chest history --prune --keep 200 --csv --out history.csv`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput && csvOutput {
				return errors.New("choose one of --json or --csv")
			}

			maxEntriesSet := cmd.Flags().Changed("max-entries")
			// The retention flags are settled before the store is opened, so a
			// contradictory or nonsensical limit changes nothing on disk.
			if err := validateRetentionFlags(prune, keep, cmd.Flags().Changed("keep"), maxEntries, maxEntriesSet); err != nil {
				return err
			}

			mgr, err := history.DefaultManager()
			if err != nil {
				return err
			}

			if prune || maxEntriesSet {
				if err := pruneHistory(cmd.OutOrStdout(), mgr, prune, keep, maxEntries, maxEntriesSet); err != nil {
					return err
				}
			}

			entries, err := mgr.List()
			if err != nil {
				return err
			}

			if jsonOutput || csvOutput || outPath != "" {
				return emitReport(cmd.OutOrStdout(), entries, csvRowsForHistory(entries), jsonOutput, csvOutput, outPath)
			}

			if len(entries) == 0 {
				fmt.Println("No history records found.")
				return nil
			}

			fmt.Println("CHEST HISTORY")
			fmt.Println("─────────────────────────────────────────────────────────────")
			w := tabwriter.NewWriter(os.Stdout, 4, 8, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tDATE\tFILES\tSTATUS\tDIRECTORY")

			for i := len(entries) - 1; i >= 0; i-- {
				e := entries[i]
				dateStr := e.Timestamp.Format("2006-01-02 15:04")
				fmt.Fprintf(w, "%d\t%s\t%d\t%s\t%s\n", e.ID, dateStr, e.FilesCount, e.Status, e.Directory)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output the history list as JSON instead of the human table")
	cmd.Flags().BoolVar(&csvOutput, "csv", false, "Output the history list as CSV instead of the human table")
	cmd.Flags().StringVar(&outPath, "out", "", "Write the machine-readable report (JSON by default) to FILE instead of stdout")
	cmd.Flags().BoolVarP(&prune, "prune", "p", false, "Drop the oldest history entries, keeping the newest --keep of them")
	cmd.Flags().IntVarP(&keep, "keep", "k", history.DefaultMaxEntries, "How many of the newest history entries --prune keeps")
	cmd.Flags().IntVarP(&maxEntries, "max-entries", "m", 0, "Prune down to N entries and store N as the limit every later run enforces")
	return cmd
}

// validateRetentionFlags rejects the retention combinations before anything is
// read or written. A limit below 1 would mean keeping nothing at all, which
// would drop the newest entry too, so it is refused rather than clamped.
func validateRetentionFlags(prune bool, keep int, keepSet bool, maxEntries int, maxEntriesSet bool) error {
	if maxEntriesSet && maxEntries < 1 {
		return fmt.Errorf("--max-entries must be at least 1, got %d", maxEntries)
	}
	if !prune {
		if keepSet {
			return errors.New("--keep only applies together with --prune")
		}
		return nil
	}
	if keep < 1 {
		return fmt.Errorf("--keep must be at least 1, got %d", keep)
	}
	return nil
}

// pruneHistory applies the requested retention and reports what it removed.
func pruneHistory(w io.Writer, mgr *history.Manager, prune bool, keep, maxEntries int, maxEntriesSet bool) error {
	if prune {
		removed, err := mgr.Prune(keep)
		if err != nil {
			return err
		}
		if removed == 0 {
			fmt.Fprintf(w, "\x1b[38;5;246mHistory already holds %d entries or fewer; nothing removed.\x1b[0m\n", keep)
		} else {
			fmt.Fprintf(w, "\x1b[1;38;5;82m✔\x1b[0m History pruned: removed \x1b[1;38;5;82m%d\x1b[0m old entry(s), kept the newest %d.\n", removed, keep)
		}
	}
	if maxEntriesSet {
		removed, err := mgr.SetMaxEntries(maxEntries)
		if err != nil {
			return err
		}
		if removed > 0 {
			fmt.Fprintf(w, "\x1b[1;38;5;82m✔\x1b[0m Retention cap set to \x1b[1;38;5;82m%d\x1b[0m entries; removed %d old entry(s).\n", maxEntries, removed)
		}
		fmt.Fprintf(w, "  \x1b[38;5;246mfrom now on CHEST keeps at most %d history entries.\x1b[0m\n", maxEntries)
	}
	return nil
}

// csvRowsForHistory builds the history CSV table: a header plus one row per
// entry, iterated newest-first to match the order of the human table (which
// walks entries in reverse).
func csvRowsForHistory(entries []models.HistoryEntry) [][]string {
	rows := [][]string{{"id", "timestamp", "files_count", "status", "directory"}}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		rows = append(rows, []string{
			strconv.Itoa(e.ID),
			e.Timestamp.Format("2006-01-02 15:04"),
			strconv.Itoa(e.FilesCount),
			e.Status,
			e.Directory,
		})
	}
	return rows
}
