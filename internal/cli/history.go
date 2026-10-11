package cli

import (
	"errors"
	"fmt"
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
	)

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show previous file organization operations",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput && csvOutput {
				return errors.New("choose one of --json or --csv")
			}

			mgr, err := history.DefaultManager()
			if err != nil {
				return err
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
	return cmd
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
