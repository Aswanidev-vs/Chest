package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/spf13/cobra"
)

func newUndoCmd() *cobra.Command {
	var allowSystem bool

	undoCmd := &cobra.Command{
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

			fmt.Println("\x1b[1;38;5;82m✔ Undo history cache cleared successfully.\x1b[0m")
			return nil
		},
	}

	cacheCmd.Flags().BoolVar(&clearCache, "clear", false, "Clear all undo history")
	undoCmd.AddCommand(cacheCmd)

	return undoCmd
}
