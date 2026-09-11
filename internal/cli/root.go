package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var rootCmd = &cobra.Command{
	Use:     "chest",
	Version: Version,
	Short:   "CHEST — Lightweight file organization tool inspired by Minecraft",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return detectFlagTypos(cmd, os.Args[1:])
	},
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

// detectFlagTypos protects against mistyped single-dash flags such as
// "chest sort -dry", where the user meant the long flag "--dry-run".
// pflag would otherwise interpret "-dry" as the shorthand cluster
// "-d -r -y" (all valid shorthands on sort), silently enabling --date,
// --recursive and --yes — which also skips the confirmation prompt —
// and execute the command without any warning.
//
// Any single-dash token with more than one character that prefix-matches
// a long flag name of the resolved command is rejected with a suggestion.
func detectFlagTypos(cmd *cobra.Command, rawArgs []string) error {
	flags := cmd.Flags()
	for i := 0; i < len(rawArgs); i++ {
		arg := rawArgs[i]
		if arg == "--" {
			break // everything after -- is positional
		}
		// Only consider single-dash tokens with more than one character.
		if len(arg) < 3 || arg[0] != '-' || arg[1] == '-' {
			continue
		}
		token := arg[1:]
		// If the first character is a shorthand that expects a value
		// (e.g. -pdownloads), the rest of the token is its value, not flags.
		if fl := flags.ShorthandLookup(token[:1]); fl != nil && fl.NoOptDefVal == "" {
			continue
		}
		var matches []string
		flags.VisitAll(func(fl *pflag.Flag) {
			if strings.HasPrefix(fl.Name, token) {
				matches = append(matches, "--"+fl.Name)
			}
		})
		if len(matches) > 0 {
			return fmt.Errorf("unknown flag %q. Did you mean %q?", arg, matches[0])
		}
	}
	return nil
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
	rootCmd.AddCommand(newCompletionCmd())
	rootCmd.AddCommand(newManCmd())
	rootCmd.AddCommand(newUpdateCmd())
	rootCmd.AddCommand(newSpeedtestCmd())
}
