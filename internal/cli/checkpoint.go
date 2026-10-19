package cli

import (
	"errors"
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/spf13/cobra"
)

func newCheckpointCmd() *cobra.Command {
	var (
		name       string
		jsonOutput bool
		csvOutput  bool
		outPath    string
	)

	cmd := &cobra.Command{
		Use:   "checkpoint [name]",
		Short: "Mark the current history position with a named label",
		Long: `Record a named marker at the current end of history.

A checkpoint moves no files. It only remembers which operation history had
reached, so that

  chest undo --to <label>

can later revert every operation recorded after that point as one atomic batch:
either all of those files go back, or none of them do.

Give the label with --name or as a single argument. Use "chest checkpoint list"
to see the recorded labels. When a label is used twice, undoing to it targets
the most recent marker.`,
		Example: `  chest checkpoint --name before-cleanup
  chest checkpoint before-import
  chest checkpoint list
  chest checkpoint list --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The export flags belong to the report this command prints, not to
			// the marker; saying so is clearer than silently ignoring them.
			if jsonOutput || csvOutput || outPath != "" {
				return errors.New("--json, --csv and --out apply to 'chest checkpoint list'")
			}

			if name != "" && len(args) > 0 {
				return fmt.Errorf("checkpoint named twice: %q and %q, pass only one", name, args[0])
			}
			label := name
			if label == "" && len(args) > 0 {
				label = args[0]
			}
			if label == "" {
				return errors.New("checkpoint name required: use --name <label> or pass the label as an argument")
			}

			mgr, err := history.DefaultManager()
			if err != nil {
				return err
			}

			cp, err := mgr.AddCheckpoint(label)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "\x1b[1;38;5;82m✔\x1b[0m Checkpoint \x1b[1;38;5;82m'%s'\x1b[0m recorded at operation #%d (%s)\n", cp.Name, cp.EntryID, cp.Timestamp.Format("2006-01-02 15:04:05"))
			fmt.Fprintf(w, "  \x1b[38;5;246mrevert everything recorded after it with:\x1b[0m chest undo --to %s\n", cp.Name)
			return nil
		},
	}

	cmd.Flags().StringVarP(&name, "name", "n", "", "Label for the checkpoint (may also be given as a single argument)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output the result as JSON instead of the confirmation line")
	cmd.Flags().BoolVar(&csvOutput, "csv", false, "Output the result as CSV instead of the confirmation line")
	cmd.Flags().StringVar(&outPath, "out", "", "Write the machine-readable report (JSON by default) to FILE instead of stdout")

	var (
		listJSON bool
		listCSV  bool
		listOut  string
	)

	listCmd := &cobra.Command{
		Use:     "list",
		Short:   "List recorded checkpoints, newest first",
		Aliases: []string{"ls"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if listJSON && listCSV {
				return errors.New("choose one of --json or --csv")
			}

			mgr, err := history.DefaultManager()
			if err != nil {
				return err
			}

			checkpoints, err := mgr.ListCheckpoints()
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			if listJSON || listCSV || listOut != "" {
				return emitReport(w, checkpoints, csvRowsForCheckpoints(checkpoints), listJSON, listCSV, listOut)
			}

			if len(checkpoints) == 0 {
				fmt.Fprintln(w, "No checkpoints recorded.")
				fmt.Fprintln(w, "\x1b[38;5;246mRecord one with: chest checkpoint --name <label>\x1b[0m")
				return nil
			}

			fmt.Fprintln(w, "\x1b[1;38;5;255mCHEST CHECKPOINTS\x1b[0m")
			fmt.Fprintln(w, "────────────────────────")
			tw := tabwriter.NewWriter(w, 4, 8, 2, ' ', 0)
			fmt.Fprintln(tw, "LABEL\tTAKEN\tOPERATION")
			for i := len(checkpoints) - 1; i >= 0; i-- {
				c := checkpoints[i]
				fmt.Fprintf(tw, "%s\t%s\t#%d\n", c.Name, c.Timestamp.Format("2006-01-02 15:04:05"), c.EntryID)
			}
			return tw.Flush()
		},
	}

	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output the checkpoint list as JSON instead of the human table")
	listCmd.Flags().BoolVar(&listCSV, "csv", false, "Output the checkpoint list as CSV instead of the human table")
	listCmd.Flags().StringVar(&listOut, "out", "", "Write the machine-readable report (JSON by default) to FILE instead of stdout")

	cmd.AddCommand(listCmd)
	return cmd
}

// csvRowsForCheckpoints builds the checkpoint CSV table, newest first to match
// the human table.
func csvRowsForCheckpoints(checkpoints []history.Checkpoint) [][]string {
	rows := [][]string{{"id", "name", "timestamp", "entry_id"}}
	for i := len(checkpoints) - 1; i >= 0; i-- {
		c := checkpoints[i]
		rows = append(rows, []string{
			strconv.Itoa(c.ID),
			c.Name,
			c.Timestamp.Format("2006-01-02 15:04:05"),
			strconv.Itoa(c.EntryID),
		})
	}
	return rows
}
