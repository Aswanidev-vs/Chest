package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	var (
		shellName  string
		install    bool
		outputPath string
	)

	cmd := &cobra.Command{
		Use:   "completion [shell]",
		Short: "Install tab-completion so your shell autocompletes chest (bash, zsh, fish)",
		Long: `Give your shell tab-completion for chest: type "chest s" and press Tab to
complete to sort, or "chest sort --p" and let it complete --preset - the same
autocomplete you get with git or docker. It saves typing and helps you discover
chest's subcommands and flags.

The quickest setup is a one-liner:
    chest completion --install
which detects your shell, writes the script to the standard location for that
shell, and wires it into your shell's startup file so completion loads in every
new terminal. If detection can't tell (for example on Windows), pass the shell:
    chest completion zsh --install

Without --install the script is just printed to stdout, so you can inspect it or
save it wherever you like:
    chest completion bash > _chest`,
		Example: `  chest completion --install       Detect your shell, install, and wire it up (recommended)
  chest completion zsh --install      Install zsh completion permanently
  chest completion fish --install     Install fish completion permanently
  chest completion bash > _chest      Print the bash script to stdout to inspect or save
  chest completion zsh -o _chest      Write the zsh script to a custom path`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shell := shellName
			if len(args) > 0 {
				shell = strings.ToLower(strings.TrimSpace(args[0]))
			}
			// With --install, infer the shell from $SHELL when it wasn't given,
			// so the recommended `chest completion --install` one-liner works.
			if shell == "" && install {
				shell = detectShell()
			}
			if shell == "" {
				return fmt.Errorf("missing shell. Run `chest completion --install` to auto-detect, or pass it explicitly: chest completion <bash|zsh|fish> [--install] [--output <file>]")
			}
			if shell != "bash" && shell != "zsh" && shell != "fish" {
				return fmt.Errorf("unsupported shell %q. Choose from: bash, zsh, fish", shell)
			}

			home, herr := os.UserHomeDir()
			if herr != nil {
				home = "."
			}

			// Resolve where the script goes: --install picks the canonical
			// per-shell location, otherwise --output (or stdout by default).
			var writePath string
			var loadLine string
			if install {
				scriptPath, loadLineInfo, err := installTarget(shell, home)
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
					return err
				}
				writePath = scriptPath
				loadLine = loadLineInfo
			} else if outputPath != "" {
				writePath = outputPath
			}

			// Generate the completion script into an in-memory buffer, then write it
			// out. (Cobra's Gen* helpers write through an io.Writer; on Windows
			// writing directly to a freshly opened *os.File fails with EINVAL,
			// so we buffer first and use os.WriteFile / the stdout File Write.)
			var buf bytes.Buffer
			if err := generateCompletion(cmd, shell, &buf); err != nil {
				return err
			}

			if writePath != "" {
				if err := os.WriteFile(writePath, buf.Bytes(), 0644); err != nil {
					return err
				}
			} else {
				if _, werr := os.Stdout.Write(buf.Bytes()); werr != nil {
					return werr
				}
				_ = os.Stdout.Sync()
			}

			if install {
				profile := installProfile(shell, home)
				if profile != "" && loadLine != "" {
					existing, rerr := os.ReadFile(profile)
					if rerr == nil && strings.Contains(string(existing), "# enable chest tab completion") {
						fmt.Printf("  Already configured in %s\n", profile)
					} else {
						if existing == nil {
							existing = []byte("")
						}
						if err := os.WriteFile(profile, append(existing, []byte(loadLine)...), 0644); err != nil {
							return err
						}
						fmt.Printf("  Added to %s\n", profile)
					}
				}
				fmt.Printf("  Installed %s completion -> %s\n", shell, writePath)
				fmt.Println("  Restart your shell (or run `source <profile>`), then type `chest ` and press Tab to try it.")
			} else if writePath != "" {
				fmt.Printf("Wrote %s completion script to %s\n", shell, writePath)
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&install, "install", "i", false, "Detect your shell (or use the shell argument) and install completion permanently")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Write the completion script to this file instead of stdout")
	cmd.Flags().StringVar(&shellName, "shell", "", "Shell to generate for: bash, zsh, fish (auto-detected when using --install)")

	return cmd
}

// generateCompletion writes the completion script for the given shell to buf.
func generateCompletion(cmd *cobra.Command, shell string, buf *bytes.Buffer) error {
	switch shell {
	case "bash": return cmd.Root().GenBashCompletion(buf)
	case "zsh":  return cmd.Root().GenZshCompletion(buf)
	case "fish": return cmd.Root().GenFishCompletion(buf, false)
	default:     return fmt.Errorf("unsupported shell %q", shell)
	}
}

// installTarget resolves the canonical per-shell completion script path and, for
// shells that do not auto-load it, the line that must be appended to the
// user's profile so tab completion works in every new session.
func installTarget(shell, home string) (string, string, error) {
	switch shell {
	case "bash":
		scriptPath := filepath.Join(home, ".local", "share", "bash-completion", "completions", "chest")
		load := fmt.Sprintf("\n# enable chest tab completion\nsource %s\n", shellQuote(scriptPath))
		return scriptPath, load, nil
	case "zsh":
		dir := filepath.Join(home, ".zsh", "completions")
		scriptPath := filepath.Join(dir, "_chest")
		load := fmt.Sprintf("\n# enable chest tab completion\nfpath=(%s $fpath)\nautoload -U compinit && compinit\n", shellQuote(dir))
		return scriptPath, load, nil
	case "fish":
		// Fish automatically loads *.fish files from this directory; no profile edit needed.
		scriptPath := filepath.Join(home, ".config", "fish", "completions", "chest.fish")
		return scriptPath, "", nil
	default:
		return "", "", fmt.Errorf("unsupported shell %q", shell)
	}
}

// installProfile returns the shell config file that must load the completion.
func installProfile(shell, home string) string {
	switch shell {
	case "bash": return filepath.Join(home, ".bashrc")
	case "zsh":  return filepath.Join(home, ".zshrc")
	default:     return "" // fish auto-loads; no profile edit needed
	}
}

// detectShell guesses the user's shell from the SHELL environment variable.
func detectShell() string {
	val := strings.ToLower(os.Getenv("SHELL"))
	if strings.Contains(val, "fish") {
		return "fish"
	}
	if strings.Contains(val, "zsh") {
		return "zsh"
	}
	if strings.Contains(val, "bash") {
		return "bash"
	}
	return ""
}

// shellQuote single-quotes a path for safe use inside a shell script line.
func shellQuote(p string) string {
	return "'" + p + "'"
}