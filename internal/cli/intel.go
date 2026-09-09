package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Aswanidev-vs/chest/internal/indexer"
	"github.com/spf13/cobra"
)

func newIndexCmd() *cobra.Command {
	var (
		computeHashes bool
		clearIndex    bool
	)

	cmd := &cobra.Command{
		Use:   "index [path]",
		Short: "Create, update, or clear local CHEST file index in SQLite",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := indexer.OpenOrCreate()
			if err != nil {
				return err
			}
			defer store.Close()

			if clearIndex {
				if err := store.ClearIndex(); err != nil {
					return fmt.Errorf("failed clearing index: %w", err)
				}
				fmt.Println("\x1b[1;38;5;82m✔ SQLite cache cleared successfully (files table emptied).\x1b[0m")
				return nil
			}

			path := "."
			if len(args) > 0 {
				path = args[0]
			}

			fmt.Printf("\x1b[38;5;254mIndexing directory \x1b[38;5;220m%s\x1b[0m...\n", path)
			start := time.Now()

			count, err := indexWithProgress(store, path, computeHashes)
			if err != nil {
				return err
			}

			fmt.Printf("\x1b[1;38;5;82m✔ Indexed %d files\x1b[0m in %v\n", count, time.Since(start).Round(time.Millisecond))
			return nil
		},
	}

	cmd.Flags().BoolVar(&computeHashes, "hash", false, "Compute cryptographic SHA256 hashes during indexing")
	cmd.Flags().BoolVar(&clearIndex, "clear", false, "Clear and truncate cached file index records")
	return cmd
}

func newCleanCmd() *cobra.Command {
	var (
		purgeAll bool
		yes      bool
	)

	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Delete cached index database and temporary data",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := indexer.OpenOrCreate()
			if err != nil {
				return err
			}

			if purgeAll {
				// Confirmation prompt
				if !yes {
					fmt.Print("\x1b[1;38;5;214m⚠ This will permanently delete ~/.chest/index.db.\x1b[0m\n\nContinue? [y/N]: ")
					reader := bufio.NewReader(os.Stdin)
					resp, _ := reader.ReadString('\n')
					resp = strings.TrimSpace(strings.ToLower(resp))
					if resp != "y" && resp != "yes" {
						fmt.Println("Operation aborted.")
						return nil
					}
				}
				if err := store.DeleteDB(); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("failed removing db: %w", err)
				}
				fmt.Println("\x1b[1;38;5;82m✔ Completely removed ~/.chest/index.db database file.\x1b[0m")
				return nil
			}

			// Confirmation prompt for index clear
			if !yes {
				fmt.Print("\x1b[1;38;5;214m⚠ This will clear the cached file index.\x1b[0m\n\nContinue? [y/N]: ")
				reader := bufio.NewReader(os.Stdin)
				resp, _ := reader.ReadString('\n')
				resp = strings.TrimSpace(strings.ToLower(resp))
				if resp != "y" && resp != "yes" {
					fmt.Println("Operation aborted.")
					return nil
				}
			}

			if err := store.ClearIndex(); err != nil {
				_ = store.Close()
				return err
			}
			_ = store.Close()

			fmt.Println("\x1b[1;38;5;82m✔ Cleaned index cache in SQLite. (Use --all to delete entire database file)\x1b[0m")
			return nil
		},
	}

	cmd.Flags().BoolVar(&purgeAll, "all", false, "Completely delete the ~/.chest/index.db database file")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompt")
	return cmd
}

func newStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats [path]",
		Short: "Display storage and category statistics from local index",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := indexer.OpenOrCreate()
			if err != nil {
				return err
			}
			defer store.Close()

			root := ""
			if len(args) > 0 {
				root = args[0]
			}
			summary, err := store.GetStats(root)
			if err != nil {
				return err
			}

			fmt.Println("\n\x1b[1;38;5;255mCHEST STATS\x1b[0m")
			fmt.Println("─────────────────────────────────────────────────────────────")
			fmt.Printf("  \x1b[38;5;246mTotal Files:\x1b[0m   \x1b[1;38;5;82m%d\x1b[0m\n", summary.TotalFiles)
			fmt.Printf("  \x1b[38;5;246mTotal Size:\x1b[0m    \x1b[1;38;5;220m%s\x1b[0m\n\n", formatFileSize(summary.TotalSize))

			fmt.Println("  \x1b[1;38;5;254mCATEGORY BREAKDOWN:\x1b[0m")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			for cat, count := range summary.CategoryCounts {
				size := summary.CategorySizes[cat]
				fmt.Fprintf(w, "    %-12s\t%d files\t%s\n", strings.ToUpper(cat), count, formatFileSize(size))
			}
			_ = w.Flush()

			if len(summary.LargestFiles) > 0 {
				fmt.Println("\n  \x1b[1;38;5;254mLARGEST FILES:\x1b[0m")
				for i, f := range summary.LargestFiles {
					if i >= 5 {
						break
					}
					fmt.Printf("    %d. %s \x1b[38;5;246m(%s)\x1b[0m\n", i+1, f.RelPath, formatFileSize(f.Size))
				}
			}

			fmt.Println()
			return nil
		},
	}
}

func newDuplicatesCmd() *cobra.Command {
	var except []string

	cmd := &cobra.Command{
		Use:   "duplicates [path] [--except dir,glob]",
		Short: "Find duplicate files using content hashes",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := indexer.OpenOrCreate()
			if err != nil {
				return err
			}
			defer store.Close()

			root := ""
			if len(args) > 0 {
				root = args[0]
				// Canonicalize to an absolute path so the exact string we index
				// under is the same one FindDuplicates() scopes its query by.
				// Mixing spellings (./x vs E:\x vs x) creates disjoint row sets,
				// so the report can leak rows from an earlier scan of another dir.
				if abs, err := filepath.Abs(filepath.Clean(root)); err == nil {
					root = abs
				}
				_, _ = indexWithProgress(store, root, true, except)
			}

			groups, err := store.FindDuplicates(root)
			if err != nil {
				return err
			}

			if len(groups) == 0 {
				fmt.Println("\x1b[38;5;82m✔ No duplicate files found.\x1b[0m")
				return nil
			}

			var totalWasted int64
			for _, g := range groups {
				totalWasted += g.WastedSize
			}

			fmt.Printf("\n\x1b[1;38;5;214mFound %d duplicate sets\x1b[0m (Wasted space: \x1b[1;38;5;196m%s\x1b[0m)\n",
				len(groups), formatFileSize(totalWasted))
			fmt.Println("─────────────────────────────────────────────────────────────")

			for i, g := range groups {
				fmt.Printf("\n  \x1b[1;38;5;220mSet #%d\x1b[0m (Size: %s each, Hash: %s...):\n",
					i+1, formatFileSize(g.Size), g.Hash[:12])
				for _, f := range g.Files {
					fmt.Printf("    • %s\n", f.Path)
				}
			}

			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&except, "except", nil,
		"Comma-separated directories/files to skip, e.g. node_modules,venv,.git (glob patterns allowed)")
	return cmd
}

func newAnalyzeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "analyze [path]",
		Short: "Analyze indexed filesystem data and report storage intelligence",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Time the whole run (including any indexing walk) so report
			// generation shows how long the command took end-to-end.
			start := time.Now()

			store, err := indexer.OpenOrCreate()
			if err != nil {
				return err
			}
			defer store.Close()

			root := ""
			if len(args) > 0 {
				// Hash during the progress-covered walk (computeHashes=true) so the
				// duplicate phase in Analyze() reads no file contents — it becomes a
				// pure SQL GROUP BY on stored hashes. Unchanged files are still
				// skipped incrementally and reuse their cached hash.
				root = args[0]
				if abs, err := filepath.Abs(filepath.Clean(root)); err == nil {
					root = abs
				}
				_, _ = indexWithProgress(store, root, true)
			}

			// Analyze() is not instant: computing stats, scanning the duplicate
			// groups (hashing any stale-size candidates) and detecting old/empty
			// files all happen here. Show what's running so the screen isn't blank
			// between the progress walk finishing and the report being printed.
			fmt.Println()
			if root == "" {
				fmt.Println("  \x1b[1;38;5;214mComputing storage insights...\x1b[0m")
			} else {
				fmt.Printf("  \x1b[1;38;5;214mComputing storage insights for %s...\x1b[0m\n", root)
			}
			report, err := analyzeWithProgress(store, root)
			if err != nil {
				return err
			}

			fmt.Println("\n\x1b[1;38;5;255mCHEST ANALYSIS REPORT\x1b[0m")
			fmt.Println("─────────────────────────────────────────────────────────────")
			fmt.Printf("  \x1b[38;5;246mFiles Scanned:\x1b[0m     %d\n", report.Stats.TotalFiles)
			fmt.Printf("  \x1b[38;5;246mTotal Storage:\x1b[0m     %s\n", formatFileSize(report.Stats.TotalSize))
			fmt.Printf("  \x1b[38;5;246mDuplicate Sets:\x1b[0m    %d\n", len(report.DuplicateGroups))
			fmt.Printf("  \x1b[38;5;246mOld Files (>180d):\x1b[0m %d\n", len(report.OldFiles))
			fmt.Printf("  \x1b[38;5;246mEmpty Files (0B):\x1b[0m  %d\n", len(report.EmptyFiles))

			if len(report.Stats.LargestFiles) > 0 {
				fmt.Println("\n  \x1b[1;38;5;254mTop Storage Consumers:\x1b[0m")
				for i, f := range report.Stats.LargestFiles {
					if i >= 3 {
						break
					}
					fmt.Printf("    • %s (%s)\n", f.Name, formatFileSize(f.Size))
				}
			}

			fmt.Println()
			fmt.Printf("  \x1b[38;5;246mCompleted in:\x1b[0m      %v\n", time.Since(start).Round(time.Millisecond))
			fmt.Println("\x1b[1;38;5;82m✔ Analysis complete.\x1b[0m")
			return nil
		},
	}
}
