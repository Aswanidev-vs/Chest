package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/Aswanidev-vs/chest/internal/presets"
)

func newPresetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "preset",
		Short: "Display available presets",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("CHEST PRESETS")
			fmt.Println("────────────────────────")
			for _, p := range presets.AvailablePresets() {
				fmt.Printf("- %s\n", p)
			}
			fmt.Println("\nUsage: chest sort -p <preset-name>")
		},
	}
}
