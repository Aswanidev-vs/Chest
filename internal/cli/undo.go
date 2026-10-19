package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/spf13/cobra"
)

func newUndoCmd() *cobra.Command {
	var (
		allowSystem bool
		toLabel     string
		fileNames   []string
		jsonOutput  bool
		csvOutput   bool
		outPath     string
	)

	undoCmd := &cobra.Command{
		Use:   "undo [operation-id]",
		Short: "Undo the latest operation, a specific operation by ID, or everything after a checkpoint",
		Long: `Undo moves files back to the locations they came from.

Without flags it reverses the latest operation, or the one named by ID.

--to <label> reverses every operation recorded after the checkpoint of that
label as one batch. The whole batch is checked before anything is moved: if a
single file cannot be put back, nothing is moved at all and the reason is
reported. Operations that were already undone are left alone rather than
failing the batch.

--file <name> reverses only the files of one operation whose name matches,
leaving the rest of that operation in place so it can still be undone. The flag
may be repeated. A name the operation never moved is reported as unmatched, and
if nothing matches, nothing is moved.

--allow-system lifts the guard that refuses to restore files inside OS and
system directories.`,
		Example: `  chest undo
  chest undo 42
  chest checkpoint --name before-import
  chest undo --to before-import
  chest undo 42 --file report.pdf
  chest undo 42 --file report.pdf --file notes.md`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput && csvOutput {
				return errors.New("choose one of --json or --csv")
			}

			mgr, err := history.DefaultManager()
			if err != nil {
				return err
			}

			// The flag combination is settled before a single file is touched, so
			// a contradictory invocation cannot half-run.
			targetID, err := parseUndoArgs(args, toLabel, fileNames)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()

			switch {
			case toLabel != "":
				res, err := mgr.UndoToCheckpoint(toLabel, allowSystem)
				if err != nil {
					return fmt.Errorf("undo failed: %w", err)
				}
				if jsonOutput || csvOutput || outPath != "" {
					return emitReport(w, res, csvRowsForUndoTo(res), jsonOutput, csvOutput, outPath)
				}
				printUndoToResult(w, res)
				return nil

			case len(fileNames) > 0:
				res, err := mgr.UndoFiles(targetID, fileNames, allowSystem)
				if err != nil {
					return fmt.Errorf("undo failed: %w", err)
				}
				if jsonOutput || csvOutput || outPath != "" {
					return emitReport(w, res, csvRowsForUndoFiles(res), jsonOutput, csvOutput, outPath)
				}
				printUndoFilesResult(w, res)
				return nil
			}

			entry, count, err := mgr.Undo(targetID, allowSystem)
			if err != nil {
				return fmt.Errorf("undo failed: %w", err)
			}

			fmt.Printf("CHEST UNDO\n────────────────────────\n")
			fmt.Printf("Reversed operation #%d (%s)\n", entry.ID, entry.Directory)
			fmt.Printf("Restored %d files to original locations.\n", count)
			return nil
		},
	}

	undoCmd.Flags().BoolVar(&allowSystem, "allow-system", false, "Allow undo restoring files in protected system directories")
	undoCmd.Flags().StringVarP(&toLabel, "to", "t", "", "Revert every operation recorded after the checkpoint with this label, as one atomic batch")
	undoCmd.Flags().StringArrayVarP(&fileNames, "file", "f", nil, "Undo only the files of the operation whose basename matches (repeatable)")
	undoCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output the result as JSON instead of the human summary")
	undoCmd.Flags().BoolVar(&csvOutput, "csv", false, "Output the result as CSV instead of the human summary")
	undoCmd.Flags().StringVar(&outPath, "out", "", "Write the machine-readable report (JSON by default) to FILE instead of stdout")

	// Subcommand: chest undo cache [--clear]
	var clearCache bool

	cacheCmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage undo history cache",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := history.DefaultManager()
			if err != nil {
				return err
			}

			if !clearCache {
				entries, err := mgr.List()
				if err != nil {
					return err
				}

				fmt.Printf("\x1b[1;38;5;255mCHEST UNDO CACHE\x1b[0m\n")
				fmt.Println("────────────────────────")
				fmt.Printf("  \x1b[38;5;246mOperations stored:\x1b[0m  \x1b[1;38;5;82m%d\x1b[0m\n", len(entries))
				fmt.Println("\n  Use \x1b[38;5;220m--clear\x1b[0m to remove all undo history.")
				return nil
			}

			// Confirmation before clearing
			fmt.Print("\x1b[1;38;5;214m⚠ This will permanently delete all undo history.\x1b[0m\n\nContinue? [y/N]: ")
			reader := bufio.NewReader(os.Stdin)
			resp, _ := reader.ReadString('\n')
			resp = strings.TrimSpace(strings.ToLower(resp))
			if resp != "y" && resp != "yes" {
				fmt.Println("Operation aborted.")
				return nil
			}

			if err := mgr.ClearHistory(); err != nil {
				return err
			}

			fmt.Println("\x1b[38;5;82m✔ Undo history cache cleared successfully.\x1b[0m")
			return nil
		},
	}

	cacheCmd.Flags().BoolVar(&clearCache, "clear", false, "Clear all undo history")
	undoCmd.AddCommand(cacheCmd)

	return undoCmd
}

// parseUndoArgs settles which of the three undo modes was asked for and
// resolves the operation ID argument. It only inspects flags and arguments:
// nothing is read or moved until the combination is known to be coherent.
//
// Without --to or --file it reproduces the original behaviour, including the
// "latest entry" default of ID 0 and the wording of a malformed ID.
func parseUndoArgs(args []string, toLabel string, files []string) (int, error) {
	if toLabel != "" {
		if len(files) > 0 {
			return 0, fmt.Errorf("--to cannot be combined with --file: --to reverts whole operations, --file reverts single files of one operation")
		}
		if len(args) > 0 {
			return 0, fmt.Errorf("--to cannot be combined with an operation ID: it reverts every operation recorded after the checkpoint")
		}
		return 0, nil
	}

	if len(files) > 0 && len(args) == 0 {
		return 0, fmt.Errorf("--file requires an operation ID (for example: chest undo 3 --file report.pdf)")
	}

	targetID := 0
	if len(args) > 0 {
		parsed, err := strconv.Atoi(args[0])
		if err != nil {
			return 0, fmt.Errorf("invalid operation ID '%s'", args[0])
		}
		targetID = parsed
	}
	return targetID, nil
}

// printUndoToResult reports an atomic revert to a checkpoint.
func printUndoToResult(w io.Writer, res *history.UndoToResult) {
	fmt.Fprintf(w, "\x1b[1;38;5;255mCHEST UNDO TO CHECKPOINT\x1b[0m\n")
	fmt.Fprintf(w, "────────────────────────\n")
	fmt.Fprintf(w, "Reverted everything recorded after checkpoint \x1b[1;38;5;82m'%s'\x1b[0m (entry #%d).\n", res.Checkpoint, res.EntryID)
	fmt.Fprintf(w, "Reversed %d operation(s), restored %d files to original locations.\n", len(res.UndoneIDs), res.Files)
	for _, id := range res.AlreadyUndone {
		fmt.Fprintf(w, "  \x1b[38;5;246moperation #%d was already undone, left as is\x1b[0m\n", id)
	}
}

// printUndoFilesResult reports a selective revert.
func printUndoFilesResult(w io.Writer, res *history.UndoFilesResult) {
	fmt.Fprintf(w, "\x1b[1;38;5;255mCHEST SELECTIVE UNDO\x1b[0m\n")
	fmt.Fprintf(w, "────────────────────────\n")
	fmt.Fprintf(w, "Reversed %d file(s) of operation #%d.\n", res.Files, res.EntryID)
	for _, name := range res.Restored {
		fmt.Fprintf(w, "  \x1b[1;38;5;82m✔\x1b[0m restored \x1b[38;5;246m%s\x1b[0m\n", name)
	}
	for _, name := range res.Unmatched {
		fmt.Fprintf(w, "  \x1b[38;5;196m✖ %s\x1b[0m was not moved by operation #%d, nothing reverted for it\n", name, res.EntryID)
	}
	if res.Remaining > 0 {
		fmt.Fprintf(w, "%d file(s) of operation #%d stay organized and can still be undone.\n", res.Remaining, res.EntryID)
	}
}

// csvRowsForUndoTo builds the checkpoint-revert CSV table.
func csvRowsForUndoTo(res *history.UndoToResult) [][]string {
	ids := make([]string, 0, len(res.UndoneIDs))
	for _, id := range res.UndoneIDs {
		ids = append(ids, strconv.Itoa(id))
	}
	return [][]string{
		{"checkpoint", "entry_id", "undone_ids", "already_undone", "files"},
		{
			res.Checkpoint,
			strconv.Itoa(res.EntryID),
			strings.Join(ids, " "),
			strconv.Itoa(len(res.AlreadyUndone)),
			strconv.Itoa(res.Files),
		},
	}
}

// csvRowsForUndoFiles builds the selective-undo CSV table, one row per
// requested file so an unmatched name is as visible as a restored one.
func csvRowsForUndoFiles(res *history.UndoFilesResult) [][]string {
	restored := make(map[string]bool, len(res.Restored))
	for _, name := range res.Restored {
		restored[name] = true
	}
	rows := [][]string{{"entry_id", "file", "result"}}
	for _, name := range res.Requested {
		status := "unmatched"
		if restored[name] {
			status = "restored"
		}
		rows = append(rows, []string{strconv.Itoa(res.EntryID), name, status})
	}
	return rows
}
