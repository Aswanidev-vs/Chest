package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// updateModule is what "go install" builds to fetch the latest published CHEST.
const updateModule = "github.com/Aswanidev-vs/chest/cmd/chest@latest"

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update CHEST to the latest version",
		Long: `Reinstall CHEST from the latest published version.

Runs "go install github.com/Aswanidev-vs/chest/cmd/chest@latest", which rebuilds
the binary from source and replaces the copy in your Go bin directory. Because
the currently running process has already loaded the old binary, restart CHEST
after updating to start using the new version. Requires the Go toolchain.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return Update(cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

// Update reinstalls CHEST at the latest published version by invoking
// "go install" on the module's main package. While it runs, a spinner is shown
// on errOut; "go install"'s own output is captured and replayed once the
// spinner stops so the two never clobber each other on the same line.
func Update(out, errOut io.Writer) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go not found in PATH: %w.\nchest update uses \"go install\" and requires the Go toolchain; install it from https://go.dev/dl and try again", err)
	}

	var buf bytes.Buffer
	cmd := exec.Command("go", "install", updateModule)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Stdin = os.Stdin

	spinner := newSpinner(errOut, func() string {
		return "Updating CHEST to the latest version…"
	})
	err := cmd.Run()
	spinner.stop()

	// Replay whatever "go install" printed (usually nothing).
	if buf.Len() > 0 {
		_, _ = io.Copy(out, &buf)
	}
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Fprintln(out, "\nCHEST updated to the latest version. Restart chest to start using it.")
	return nil
}