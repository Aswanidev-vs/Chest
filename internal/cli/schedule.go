package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/scheduler"
	"github.com/spf13/cobra"
)

// newScheduleCmd groups the recurring-run subcommands under "chest schedule",
// complementing the foreground "chest watch" with one-shot periodic runs.
func newScheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Run CHEST on a cron schedule (e.g. a nightly sort)",
		Long: `Schedule recurring one-shot organizing runs.

Unlike 'chest watch' — which needs a foreground process — a schedule is handed
to the operating system's own scheduler (crontab on Linux/macOS, Task Scheduler
on Windows) and fires on its own, then exits.

Examples:
  chest schedule add --cron '0 2 * * *' --preset downloads ~/Downloads
  chest schedule add --cron '0 3 * * 1' --rule '*.tmp -> Temp' ~/Desktop
  chest schedule list
  chest schedule run-now s1
  chest schedule remove s1`,
	}
	cmd.AddCommand(newScheduleAddCmd())
	cmd.AddCommand(newScheduleListCmd())
	cmd.AddCommand(newScheduleRemoveCmd())
	cmd.AddCommand(newScheduleRunNowCmd())
	return cmd
}

func newScheduleAddCmd() *cobra.Command {
	var (
		cronExpr  string
		name      string
		preset    string
		rules     []string
		printCron bool
	)

	cmd := &cobra.Command{
		Use:   "add [flags] DIRECTORY",
		Short: "Register a directory for recurring organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]

			if err := scheduler.ValidateCron(cronExpr); err != nil {
				return err
			}
			if err := filesystem.CheckSafeDirectory(dir, false); err != nil {
				return err
			}

			binPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolving CHEST executable path: %w", err)
			}

			s := scheduler.Schedule{
				Name:      name,
				Cron:      cronExpr,
				Directory: dir,
				Preset:    preset,
				Rules:     rules,
			}

			// Escape hatch for platforms/expressions the installer cannot map:
			// emit the crontab line and leave the OS untouched.
			if printCron {
				fmt.Println(scheduler.CrontabLine(s, binPath))
				return nil
			}

			mgr, err := scheduler.DefaultManager()
			if err != nil {
				return err
			}
			id, err := mgr.Add(s)
			if err != nil {
				return err
			}
			s.ID = id

			if err := scheduler.NewOSInstaller().Install(s, binPath); err != nil {
				return fmt.Errorf("saved schedule %s but registering it with the OS failed: %w (remove it with 'chest schedule remove %s', or retry with --print-cron)", id, err, id)
			}

			fmt.Printf("\x1b[1;38;5;82m✓ Scheduled %q registered.\x1b[0m\n", id)
			fmt.Printf("   Schedule:  \x1b[38;5;220m%s\x1b[0m\n", s.Cron)
			fmt.Printf("   Directory: \x1b[38;5;220m%s\x1b[0m\n", s.Directory)
			if s.Preset != "" {
				fmt.Printf("   Preset:    \x1b[38;5;220m%s\x1b[0m\n", s.Preset)
			}
			for _, r := range s.Rules {
				fmt.Printf("   Rule:      \x1b[38;5;220m%s\x1b[0m\n", r)
			}
			fmt.Printf("   Registered: \x1b[38;5;246m%s\x1b[0m\n", scheduler.CrontabLine(s, binPath))
			fmt.Printf("   Preview with 'chest schedule run-now %s', or undo with 'chest schedule remove %s'.\n", id, id)
			return nil
		},
	}

	cmd.Flags().StringVar(&cronExpr, "cron", "", "5-field cron expression (minute hour day-of-month month day-of-week)")
	cmd.Flags().StringVar(&name, "name", "", "Optional label for this schedule")
	cmd.Flags().StringVarP(&preset, "preset", "p", "", "Preset to apply")
	cmd.Flags().StringArrayVarP(&rules, "rule", "r", nil, "Custom rule (repeatable), e.g. '*.tmp -> Temp'")
	cmd.Flags().BoolVar(&printCron, "print-cron", false, "Print the crontab line instead of installing it (for unsupported platforms or complex expressions)")
	_ = cmd.MarkFlagRequired("cron")

	return cmd
}

func newScheduleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved schedules",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := scheduler.DefaultManager()
			if err != nil {
				return err
			}
			schedules, err := mgr.List()
			if err != nil {
				return err
			}
			if len(schedules) == 0 {
				fmt.Println("No schedules saved. Add one with 'chest schedule add'.")
				return nil
			}

			fmt.Println("CHEST SCHEDULES")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "ID\tCRON\tDIRECTORY\tPRESET\tNAME")
			for _, s := range schedules {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.ID, s.Cron, s.Directory, dashIfEmpty(s.Preset), dashIfEmpty(s.Name))
			}
			return w.Flush()
		},
	}
}

func newScheduleRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>",
		Short: "Unregister a schedule and delete it from the store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			mgr, err := scheduler.DefaultManager()
			if err != nil {
				return err
			}
			s, err := mgr.Get(id)
			if err != nil {
				return err
			}

			if err := scheduler.NewOSInstaller().Uninstall(id); err != nil {
				return fmt.Errorf("unregistering schedule %s from the OS failed (store unchanged): %w", id, err)
			}
			if err := mgr.Remove(id); err != nil {
				return err
			}

			fmt.Printf("\x1b[38;5;196m✖ Removed schedule %q\x1b[0m (\x1b[38;5;246m%s %s\x1b[0m).\n", id, s.Cron, s.Directory)
			return nil
		},
	}
}

func newScheduleRunNowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run-now <id>",
		Short: "Execute a saved schedule immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := scheduler.DefaultManager()
			if err != nil {
				return err
			}
			s, err := mgr.Get(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("\x1b[38;5;75m▶ Running schedule %q\x1b[0m (\x1b[38;5;246m%s %s\x1b[0m)...\n", s.ID, s.Cron, s.Directory)
			if err := scheduler.RunNow(*s); err != nil {
				return fmt.Errorf("run-now failed: %w", err)
			}
			return nil
		},
	}
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
