package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/Aswanidev-vs/chest/internal/history"
)

func newUndoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "undo [operation-id]",
		Short: "Undo the latest operation or a specific operation by ID",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := history.DefaultManager()
			if err != nil {
				return err
			}

			targetID := 0
			if len(args) > 0 {
				parsed, err := strconv.Atoi(args[0])
				if err != nil {
					return fmt.Errorf("invalid operation ID '%s'", args[0])
				}
				targetID = parsed
			}

			entry, count, err := mgr.Undo(targetID)
			if err != nil {
				return fmt.Errorf("undo failed: %w", err)
			}

			fmt.Printf("CHEST UNDO\n────────────────────────\n")
			fmt.Printf("Reversed operation #%d (%s)\n", entry.ID, entry.Directory)
			fmt.Printf("Restored %d files to original locations.\n", count)
			return nil
		},
	}
}
