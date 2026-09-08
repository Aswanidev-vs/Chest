package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/watcher"
	"github.com/spf13/cobra"
)

func newWatchCmd() *cobra.Command {
	var (
		presetName  string
		rulesList   []string
		debounce    time.Duration
		dryRun      bool
		initial     bool
		allowSystem bool
	)

	cmd := &cobra.Command{
		Use:   "watch [directory]",
		Short: "Continuously monitor and automatically organize incoming files",
		Long: `Watch a folder in real-time. When new files are downloaded or copied,
CHEST automatically applies preset or custom rules and organizes them into compartments.
Use --initial to also sort files already present before starting to watch.`,
		Example: `  chest watch ~/Downloads
  chest watch ~/Downloads --preset media
  chest watch ~/Desktop --initial
  chest watch ~/Desktop --rule "*.png -> Screenshots"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			if err := filesystem.CheckSafeDirectory(dir, allowSystem); err != nil {
				return err
			}

			w, err := watcher.New(watcher.WatchOptions{
				Directory: dir,
				Preset:    presetName,
				Rules:     rulesList,
				Debounce:  debounce,
				DryRun:    dryRun,
				Initial:   initial,
			})
			if err != nil {
				return err
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			go func() {
				<-sigChan
				fmt.Println("\n\x1b[38;5;214mStopping CHEST watch...\x1b[0m")
				cancel()
			}()

			fmt.Println(RenderChestLogo())
			fmt.Printf("\n\x1b[1;38;5;82m👀 Watching for file changes in:\x1b[0m \x1b[38;5;220m%s\x1b[0m\n", dir)
			if presetName != "" {
				fmt.Printf("   Preset: \x1b[38;5;75m%s\x1b[0m\n", presetName)
			}
			if initial {
				fmt.Println("   Sorting existing files first (--initial)...")
			}
			fmt.Println("   Press Ctrl+C to stop.")

			// Flush so the banner appears even when stdout is piped or redirected
			// to a log file (block-buffered) before the watcher blocks below.
			_ = os.Stdout.Sync()

			return w.Start(ctx, func(ev watcher.Event) {
				if ev.Kind == watcher.KindOrganized {
					if ev.Err != nil {
						fmt.Printf("   \x1b[38;5;196m✖ Error organizing %s: %v\x1b[0m\n", ev.Name, ev.Err)
					} else if ev.Name != "" {
						fmt.Printf("   \x1b[38;5;82m⚡ Organized:\x1b[0m %s \x1b[38;5;246m→\x1b[0m \x1b[38;5;220m%s\x1b[0m\n", ev.Name, ev.Dest)
					}
				} else if ev.Kind == watcher.KindCreated {
					fmt.Printf("   \x1b[38;5;75m📁 Folder created:\x1b[0m %s\n", ev.Name)
				} else if ev.Kind == watcher.KindDeleted {
					fmt.Printf("   \x1b[38;5;196m🗑 Folder removed:\x1b[0m %s\n", ev.Name)
				} else if ev.Kind == watcher.KindError && ev.Err != nil {
					fmt.Printf("   \x1b[38;5;196m✖ %v\x1b[0m\n", ev.Err)
				}
				_ = os.Stdout.Sync()
			})
		},
	}

	cmd.Flags().StringVarP(&presetName, "preset", "p", "downloads", "Preset to apply")
	cmd.Flags().StringArrayVarP(&rulesList, "rule", "r", nil, "Custom rule string")
	cmd.Flags().DurationVarP(&debounce, "debounce", "d", 500*time.Millisecond, "Settle duration before moving file")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Preview what watch would organize")
	cmd.Flags().BoolVarP(&initial, "initial", "i", false, "Sort existing files first, then watch for new ones")
	cmd.Flags().BoolVar(&allowSystem, "allow-system", false, "Allow monitoring and moving files in protected system directories")

	return cmd
}
