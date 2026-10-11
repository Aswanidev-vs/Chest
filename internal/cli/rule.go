package cli

import (
	"fmt"

	"github.com/Aswanidev-vs/chest/internal/config"
	"github.com/Aswanidev-vs/chest/internal/rules"
	"github.com/spf13/cobra"
)

type ruleCheckFlags struct {
	byType      bool
	byFormat    bool
	bySize      bool
	byDate      bool
	preset      string
	customRules []string
}

func newRuleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule",
		Short: "Rule utilities",
	}
	cmd.AddCommand(newRuleCheckCmd())
	return cmd
}

func newRuleCheckCmd() *cobra.Command {
	f := ruleCheckFlags{}

	cmd := &cobra.Command{
		Use:   "check [directory]",
		Short: "Validate a rule set without scanning or moving files",
		Long: `Validate the rules that would apply to a directory — from flags, presets
and any chest.toml discovered by walking up from the target — and report every
problem that would otherwise be silently ignored at evaluation time (unparseable
size values, unsupported field/operator combinations, invalid regexes).`,
		Example: `  chest rule check ~/Downloads -p media
  chest rule check ~/Code --rule "type=video && size>1GB -> Videos/Large"
  chest rule check . --rule "ext=jpg min_size=abc -> Images"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) > 0 {
				target = args[0]
			}

			ruleList, _, err := config.Resolve(config.Sources{
				Target:      target,
				Preset:      f.preset,
				RuleStrings: f.customRules,
				ByType:      f.byType,
				ByFormat:    f.byFormat,
				BySize:      f.bySize,
				ByDate:      f.byDate,
			}, config.Hooks{})
			if err != nil {
				return err
			}

			if len(ruleList) == 0 {
				fmt.Println("No rules resolved for this directory.")
				return nil
			}

			problems := rules.ValidateRules(ruleList)
			if len(problems) == 0 {
				fmt.Printf("\x1b[1;38;5;82m✔ %d rule(s) validated, no problems found.\x1b[0m\n", len(ruleList))
				return nil
			}

			fmt.Printf("\x1b[1;38;5;196m%d problem(s) found in %d rule(s):\x1b[0m\n", len(problems), len(ruleList))
			for _, p := range problems {
				fmt.Printf("  \x1b[38;5;214m•\x1b[0m %s\n", p)
			}
			return fmt.Errorf("%d invalid rule(s)", len(problems))
		},
	}

	cmd.Flags().BoolVarP(&f.byType, "type", "t", false, "Include the type-category rules (-t)")
	cmd.Flags().BoolVarP(&f.byFormat, "format", "f", false, "Include the format/extension rules (-f)")
	cmd.Flags().BoolVarP(&f.bySize, "size", "s", false, "Include the size-bucket rules (-s)")
	cmd.Flags().BoolVarP(&f.byDate, "date", "d", false, "Include the date rules (-d)")
	cmd.Flags().StringVarP(&f.preset, "preset", "p", "", "Preset whose rules to validate")
	cmd.Flags().StringArrayVarP(&f.customRules, "rule", "r", nil, "Custom rule string to validate")

	return cmd
}
