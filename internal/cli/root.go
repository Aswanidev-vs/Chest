package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "chest",
	Version: Version,
	Short:   "CHEST — Lightweight file organization tool inspired by Minecraft",
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

// Execute runs the root CLI command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Long = fmt.Sprintf("\n%s\n\nGive CHEST a folder, tell it how you want files organized, and CHEST puts them into the right compartments.\n", RenderChestLogo())
	rootCmd.AddCommand(newSortCmd())
	rootCmd.AddCommand(newSearchCmd())
	rootCmd.AddCommand(newHistoryCmd())
	rootCmd.AddCommand(newUndoCmd())
	rootCmd.AddCommand(newPresetCmd())
	rootCmd.AddCommand(newPluginCmd())
	rootCmd.AddCommand(newWatchCmd())
	rootCmd.AddCommand(newIndexCmd())
	rootCmd.AddCommand(newStatsCmd())
	rootCmd.AddCommand(newDuplicatesCmd())
	rootCmd.AddCommand(newAnalyzeCmd())
	rootCmd.AddCommand(newCleanCmd())
	rootCmd.AddCommand(newVersionCmd())
}
