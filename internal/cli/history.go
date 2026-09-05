package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/Aswanidev-vs/chest/internal/history"
)

func newHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "Show previous file organization operations",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := history.DefaultManager()
			if err != nil {
				return err
			}

			entries, err := mgr.List()
			if err != nil {
				return err
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
}
