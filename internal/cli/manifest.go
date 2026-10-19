package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Aswanidev-vs/chest/internal/manifest"
	"github.com/Aswanidev-vs/chest/internal/rules"
	"github.com/spf13/cobra"
)

// ExitCodeIntegrityFailure is the process status `chest verify` asks for when
// the tree no longer matches its manifest.
//
// The command returns an integrityError rather than calling os.Exit itself, so
// Execute() can map it the same way it maps thresholdError to 3. Registering it
// in root.go takes one branch:
//
//	if IsIntegrityFailure(err) {
//		os.Exit(ExitCodeIntegrityFailure)
//	}
//
// 4 is distinct from every other status the CLI uses: 0 success, 1 a command
// that could not run, 2 unused, 3 a battery threshold that was met. A script
// checking a backup needs "it ran, and it failed" to be tellable apart from
// "it ran and found nothing wrong" — that is the whole point of the code.
const ExitCodeIntegrityFailure = 4

// integrityError reports that verification completed but found discrepancies.
// It is a successful measurement that did not pass, in the same sense that
// thresholdError is a successful measurement that met a bound.
type integrityError struct {
	modified int
	missing  int
	added    int
}

func (e *integrityError) Error() string {
	return fmt.Sprintf("integrity check failed: %d modified, %d missing, %d added (exit code %d)",
		e.modified, e.missing, e.added, ExitCodeIntegrityFailure)
}

// IsIntegrityFailure reports whether err is the "the tree does not match its
// manifest" verdict, for callers that want to branch on it rather than on the
// exit status.
func IsIntegrityFailure(err error) bool {
	var ie *integrityError
	return errors.As(err, &ie)
}

func newManifestCmd() *cobra.Command {
	var (
		outPath   string
		maxSize   string
		exclude   []string
		csvOutput bool
	)

	cmd := &cobra.Command{
		Use:   "manifest <dir>",
		Short: "Hash a directory into a SHA-256 inventory for backup verification",
		Long: `Hash every file under a directory into a SHA-256 inventory.

The manifest records each file's path, size, modification time and digest, so a
later 'chest verify' can say exactly what changed. Both commands are read-only:
nothing under <dir> is opened for writing, moved or deleted.

Files larger than --max-size are not hashed. They are counted and listed in the
manifest's "skipped" list rather than dropped, so an inventory never looks
complete while quietly covering part of the tree. Symlinks are reported, not
followed, because the digest of a link's target says nothing about the tree the
manifest describes.

The document carries no run timestamp, so re-generating it over an unchanged
tree produces byte-identical output that can be diffed against the previous one.`,
		Example: `  chest manifest ~/Documents --out manifest.json
  chest manifest . --csv --out files.csv
  chest manifest /backup -x node_modules,.git --max-size 2GB`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, err := parseMaxSizeFlag(maxSize)
			if err != nil {
				return err
			}
			root := args[0]

			m, err := manifest.Generate(manifest.Options{
				Root:    root,
				MaxSize: limit,
				Exclude: exclude,
				// Keeping the document itself out of its own inventory is what
				// lets a manifest live beside the files it describes.
				Ignore: ignoredDocPath(root, outPath),
			})
			if err != nil {
				return err
			}

			// The summary goes to stderr so stdout carries only the document:
			// `chest manifest . > manifest.json` then still tells the user what
			// it just wrote, without corrupting the redirect.
			printManifestSummary(cmd.ErrOrStderr(), m, outPath)

			if err := emitReport(cmd.OutOrStdout(), m, manifest.CSVRows(m), false, csvOutput, outPath); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&outPath, "out", "o", "", "Write the manifest document to FILE (JSON, or CSV with --csv)")
	cmd.Flags().StringVar(&maxSize, "max-size", "500MB", "Skip hashing files larger than this (e.g. 500MB, 2GB)")
	cmd.Flags().StringSliceVarP(&exclude, "exclude", "x", nil,
		"Comma-separated directories/files to skip, e.g. node_modules,venv,.git (glob patterns allowed)")
	cmd.Flags().BoolVar(&csvOutput, "csv", false, "Emit the inventory as CSV instead of JSON")
	return cmd
}

func newVerifyCmd() *cobra.Command {
	var (
		manifestPath string
		maxSize      string
		exclude      []string
		jsonOutput   bool
		csvOutput    bool
		outPath      string
	)

	cmd := &cobra.Command{
		Use:   "verify <dir>",
		Short: "Re-hash a directory and report OK, MODIFIED, MISSING and ADDED files",
		Long: `Check a directory against a manifest produced by 'chest manifest'.

Every recorded file is re-hashed and compared: OK when the digest still matches,
MODIFIED when it does not, MISSING when the file is gone. Files present on disk
but absent from the manifest are reported as ADDED. Read-only throughout.

The command exits with status ` + fmt.Sprint(ExitCodeIntegrityFailure) + ` when any
file is modified, missing or added, so a backup job can gate on it without
parsing text. Files that were never hashed — over --max-size, or symlinks — are
reported as UNCHECKED and counted; they do not fail the run, but they are never
hidden either.

A manifest entry that would resolve outside <dir> (an absolute path, or one
containing "..") is refused outright: the document is data, and data must not
be able to turn an integrity check into an arbitrary file reader.`,
		Example: `  chest verify ~/Documents --manifest manifest.json
  chest verify . --manifest manifest.json --json --out verify.json
  chest verify /backup --manifest manifest.json -x node_modules`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput && csvOutput {
				return errors.New("choose one of --json or --csv")
			}
			if strings.TrimSpace(manifestPath) == "" {
				return errors.New("verify needs --manifest FILE")
			}
			limit, err := parseMaxSizeFlag(maxSize)
			if err != nil {
				return err
			}
			root := args[0]

			m, err := manifest.Load(manifestPath)
			if err != nil {
				return err
			}

			res, err := manifest.Verify(manifest.Options{
				Root:    root,
				MaxSize: limit,
				Exclude: exclude,
				Ignore:  ignoredDocPath(root, manifestPath),
			}, m)
			if err != nil {
				return err
			}

			machine := jsonOutput || csvOutput || outPath != ""
			if machine {
				if err := emitReport(cmd.OutOrStdout(), res, manifest.CSVStatusRows(res), jsonOutput, csvOutput, outPath); err != nil {
					return err
				}
			} else {
				printVerifyReport(cmd.OutOrStdout(), res, manifestPath)
			}

			if !res.Intact {
				return &integrityError{modified: res.Modified, missing: res.Missing, added: res.Added}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&manifestPath, "manifest", "", "Manifest document to verify against (required)")
	cmd.Flags().StringVar(&maxSize, "max-size", "500MB", "Skip hashing files larger than this (e.g. 500MB, 2GB)")
	cmd.Flags().StringSliceVarP(&exclude, "exclude", "x", nil,
		"Comma-separated directories/files to skip, e.g. node_modules,venv,.git (glob patterns allowed)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output the verification result as JSON instead of the human report")
	cmd.Flags().BoolVar(&csvOutput, "csv", false, "Output the discrepancies as CSV instead of the human report")
	cmd.Flags().StringVar(&outPath, "out", "", "Write the machine-readable report (JSON by default) to FILE instead of stdout")
	return cmd
}

// parseMaxSizeFlag turns --max-size into bytes using the same size grammar the
// rules engine already accepts ("500MB", "2GB", "1024"), so one string means one
// thing everywhere in CHEST.
func parseMaxSizeFlag(v string) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return manifest.DefaultMaxSize, nil
	}
	n, err := rules.ParseSize(v)
	if err != nil {
		return 0, fmt.Errorf("invalid --max-size %q: %w", v, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("invalid --max-size %q: must be greater than zero", v)
	}
	return n, nil
}

// ignoredDocPath returns the absolute path of the manifest document when it
// lives inside the directory being inventoried, and nothing otherwise. Keeping
// the document out of its own inventory is what makes it legal to write
// manifest.json next to the files it describes and verify the result later.
func ignoredDocPath(root, doc string) []string {
	if doc == "" || root == "" {
		return nil
	}
	abs, err := filepath.Abs(filepath.Clean(doc))
	if err != nil {
		return nil
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	return []string{abs}
}

// printManifestSummary reports what the walk covered and what it left out. It
// goes to stderr so that stdout stays a valid manifest document.
func printManifestSummary(w io.Writer, m *manifest.Manifest, outPath string) {
	verb := "stdout"
	if outPath != "" {
		verb = outPath
	}
	fmt.Fprintf(w, "\n\x1b[1;38;5;82m✔ Manifest built\x1b[0m  \x1b[38;5;246m%s → %s\x1b[0m\n",
		formatFileSize(m.TotalBytes), verb)
	fmt.Fprintf(w, "  \x1b[38;5;246mHashed:\x1b[0m  %d files\n", m.FileCount)
	if m.SkippedCount == 0 {
		fmt.Fprintf(w, "  \x1b[38;5;246mSkipped:\x1b[0m  none\n")
	} else {
		fmt.Fprintf(w, "  \x1b[38;5;246mSkipped:\x1b[0m  %d \x1b[38;5;246m(%s)\x1b[0m\n",
			m.SkippedCount, formatFileSize(m.SkippedBytes))
		for _, s := range m.Skipped {
			fmt.Fprintf(w, "    \x1b[38;5;246m· %s — %s\x1b[0m\n", s.Path, s.Reason)
		}
	}
	fmt.Fprintln(w)
}

// printVerifyReport is the human checklist for a verification run: the counts
// first, then every file that is not OK.
func printVerifyReport(w io.Writer, res *manifest.Result, manifestPath string) {
	fmt.Fprintln(w, "\n\x1b[1;38;5;255mCHEST VERIFY\x1b[0m")
	fmt.Fprintln(w, "─────────────────────────────────────────────────────────────")
	fmt.Fprintf(w, "  \x1b[38;5;246mDirectory:\x1b[0m  %s\n", res.Root)
	fmt.Fprintf(w, "  \x1b[38;5;246mManifest:\x1b[0m  %s \x1b[38;5;246m(%d files recorded)\x1b[0m\n",
		manifestPath, res.ManifestFiles)
	fmt.Fprintf(w, "  \x1b[38;5;246mVerified:\x1b[0m  %s in %d files\n\n",
		formatFileSize(res.VerifiedBytes), res.OK)

	row := func(label, colour string, n int) {
		fmt.Fprintf(w, "  %s%-9s\x1b[0m %s%d\x1b[0m\n", colour, label, "\x1b[38;5;246m", n)
	}
	row("OK", "\x1b[1;38;5;82m", res.OK)
	row("MODIFIED", "\x1b[1;38;5;214m", res.Modified)
	row("MISSING", "\x1b[38;5;196m", res.Missing)
	row("ADDED", "\x1b[38;5;196m", res.Added)
	if res.Unchecked > 0 {
		row("UNCHECKED", "\x1b[38;5;246m", res.Unchecked)
	}
	if res.Unreadable > 0 {
		row("UNREADABLE", "\x1b[38;5;196m", res.Unreadable)
	}

	if len(res.Files) > 0 {
		fmt.Fprintln(w, "\n  \x1b[1;38;5;214mDIFFERENCES:\x1b[0m")
		for _, d := range res.Files {
			colour := "\x1b[38;5;246m"
			if d.Status == manifest.StatusModified || d.Status == manifest.StatusMissing {
				colour = "\x1b[38;5;196m"
			}
			fmt.Fprintf(w, "    %s%-9s\x1b[0m %s \x1b[38;5;246m(%s)\x1b[0m\n",
				colour, string(d.Status), d.Path, formatFileSize(d.Size))
			if d.Detail != "" {
				fmt.Fprintf(w, "              \x1b[38;5;246m%s\x1b[0m\n", d.Detail)
			}
		}
	}

	fmt.Fprintln(w)
	if res.Intact {
		fmt.Fprintf(w, "\x1b[1;38;5;82m✔ Verified: every file matches the manifest.\x1b[0m\n\n")
	} else {
		fmt.Fprintf(w, "\x1b[38;5;196m✘ Integrity failure: %d file(s) differ from the manifest (exit code %d).\x1b[0m\n\n",
			res.DiscrepancyCount(), ExitCodeIntegrityFailure)
	}
}
