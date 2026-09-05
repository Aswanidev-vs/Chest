package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Aswanidev-vs/chest/internal/plugin"
	"github.com/spf13/cobra"
)

func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage installed CHEST plugins",
		Long: `Manage CHEST external-process plugins.

Supports listing installed plugins, viewing detailed metadata, installing local plugins,
and removing plugins.`,
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}

	cmd.AddCommand(newPluginListCmd())
	cmd.AddCommand(newPluginInfoCmd())
	cmd.AddCommand(newPluginInstallCmd())
	cmd.AddCommand(newPluginRemoveCmd())

	return cmd
}

func newPluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all installed plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := plugin.NewManager()
			if err != nil {
				return err
			}

			plugins, err := mgr.List()
			if err != nil {
				return err
			}

			if len(plugins) == 0 {
				fmt.Printf("\x1b[38;5;246mNo plugins installed in %s\x1b[0m\n", mgr.GetPluginDir())
				return nil
			}

			fmt.Printf("\n\x1b[1;38;5;255mINSTALLED PLUGINS (%d)\x1b[0m\n", len(plugins))
			fmt.Println("─────────────────────────────────────────────────────────────")

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAME\tVERSION\tCAPABILITIES\tDESCRIPTION")
			for _, p := range plugins {
				caps := strings.Join(p.Capabilities, ", ")
				if caps == "" {
					caps = "-"
				}
				fmt.Fprintf(w, "\x1b[1;38;5;220m%s\x1b[0m\t\x1b[38;5;114m%s\x1b[0m\t\x1b[38;5;75m%s\x1b[0m\t%s\n",
					p.Name, p.Version, caps, p.Description)
			}
			_ = w.Flush()
			fmt.Println()
			return nil
		},
	}
}

func newPluginInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Display information about a specific plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := plugin.NewManager()
			if err != nil {
				return err
			}

			p, err := mgr.GetInfo(args[0])
			if err != nil {
				return err
			}

			fmt.Println("\n\x1b[1;38;5;255mPLUGIN DETAILS\x1b[0m")
			fmt.Println("─────────────────────────────────────────────────────────────")
			fmt.Printf("  \x1b[38;5;246mName:\x1b[0m         \x1b[1;38;5;220m%s\x1b[0m\n", p.Name)
			fmt.Printf("  \x1b[38;5;246mVersion:\x1b[0m      \x1b[38;5;114m%s\x1b[0m\n", p.Version)
			if p.Author != "" {
				fmt.Printf("  \x1b[38;5;246mAuthor:\x1b[0m       %s\n", p.Author)
			}
			fmt.Printf("  \x1b[38;5;246mCapabilities:\x1b[0m \x1b[38;5;75m%s\x1b[0m\n", strings.Join(p.Capabilities, ", "))
			fmt.Printf("  \x1b[38;5;246mDescription:\x1b[0m  %s\n", p.Description)
			fmt.Println()
			return nil
		},
	}
}

func newPluginInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <source_dir>",
		Short: "Install a plugin from a local directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := plugin.NewManager()
			if err != nil {
				return err
			}

			p, err := mgr.Install(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("\x1b[1;38;5;82m✔ Successfully installed plugin '%s' (v%s)\x1b[0m\n", p.Name, p.Version)
			return nil
		},
	}
}

func newPluginRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Uninstall a plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := plugin.NewManager()
			if err != nil {
				return err
			}

			if err := mgr.Remove(args[0]); err != nil {
				return err
			}

			fmt.Printf("\x1b[1;38;5;82m✔ Successfully removed plugin '%s'\x1b[0m\n", args[0])
			return nil
		},
	}
}
