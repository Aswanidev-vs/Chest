package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/Aswanidev-vs/chest/internal/classifier"
	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/organizer"
	"github.com/Aswanidev-vs/chest/internal/planner"
	"github.com/Aswanidev-vs/chest/internal/presets"
	"github.com/Aswanidev-vs/chest/internal/rules"
	"github.com/Aswanidev-vs/chest/internal/scanner"
)

type sortFlags struct {
	byType         bool
	byFormat       bool
	bySize         bool
	byDate         bool
	into           string
	preset         string
	dryRun         bool
	yes            bool
	recursive      bool
	verbose        bool
	quiet          bool
	exclude        string
	hidden         bool
	customRules    []string
	followSymlinks bool
	collision      string
}

func newSortCmd() *cobra.Command {
	f := sortFlags{}

	cmd := &cobra.Command{
		Use:   "sort [path]",
		Short: "Organize files in the target or current directory",
		Long:  "Sort files according to presets, criteria flags (-t, -f, -s, -d) or custom rules.",
		RunE: func(cmd *cobra.Command, args []string) error {
			targetDir := "."
			if len(args) > 0 {
				targetDir = args[0]
			}
			absTarget, err := filepath.Abs(targetDir)
			if err != nil {
				return fmt.Errorf("invalid target directory: %w", err)
			}

			// Validate collision policy
			collisionPolicy := models.CollisionPolicy(strings.ToLower(f.collision))
			if collisionPolicy == "" {
				collisionPolicy = models.CollisionSkip
			}
			switch collisionPolicy {
			case models.CollisionSkip, models.CollisionRename, models.CollisionReplace, models.CollisionAbort:
			default:
				return fmt.Errorf("invalid collision policy '%s'. Valid: skip, rename, replace, abort", f.collision)
			}

			// Assemble rules
			var ruleList []models.Rule

			// 1. If preset specified, load it
			if f.preset != "" {
				pRules, err := presets.LoadPreset(f.preset)
				if err != nil {
					return err
				}
				ruleList = append(ruleList, pRules...)
			}

			// 2. Custom rule strings
			for i, rStr := range f.customRules {
				r, err := rules.ParseRule(rStr, 100-i)
				if err != nil {
					return fmt.Errorf("error parsing --rule: %w", err)
				}
				ruleList = append(ruleList, r)
			}

			// 3. Flags-based automatic rule generation
			if f.byType {
				// Categorize by file types
				categories := []string{
					classifier.TypeImage, classifier.TypeVideo, classifier.TypeAudio,
					classifier.TypeDocument, classifier.TypeArchive, classifier.TypeExecutable,
					classifier.TypeCode, classifier.TypeOther,
				}
				for _, cat := range categories {
					ruleList = append(ruleList, models.Rule{
						Name:        "Type: " + cat,
						Priority:    20,
						Destination: cat + "s",
						Conditions: []models.Condition{
							{Field: models.FieldType, Operator: models.OpEqual, Value: cat},
						},
					})
				}
			}

			if f.byFormat {
				// Extension rule fallback: files go into folder named after uppercase extension
				ruleList = append(ruleList, models.Rule{
					Name:        "Format/Extension",
					Priority:    10,
					Destination: "{ext}", // handled or common types
				})
			}

			if f.bySize {
				ruleList = append(ruleList,
					models.Rule{
						Name:        "Large Files (>1GB)",
						Priority:    25,
						Destination: "Large Files",
						MinSize:     "1GB",
					},
					models.Rule{
						Name:        "Medium Files (100MB-1GB)",
						Priority:    24,
						Destination: "Medium Files",
						MinSize:     "100MB",
						MaxSize:     "1GB",
					},
					models.Rule{
						Name:        "Small Files (<100MB)",
						Priority:    23,
						Destination: "Small Files",
						MaxSize:     "100MB",
					},
				)
			}

			if f.byDate {
				// Group into year folders based on mod time
				currentYear := "2026"
				ruleList = append(ruleList,
					models.Rule{
						Name:        "Old Files (<2025)",
						Priority:    22,
						Destination: "Archive_Pre_2025",
						Conditions: []models.Condition{
							{Field: models.FieldDate, Operator: models.OpLessThan, Value: "2025-01-01"},
						},
					},
					models.Rule{
						Name:        "Year 2025",
						Priority:    21,
						Destination: "2025",
						Conditions: []models.Condition{
							{Field: models.FieldDate, Operator: models.OpGreaterEq, Value: "2025-01-01"},
							{Field: models.FieldDate, Operator: models.OpLessThan, Value: "2026-01-01"},
						},
					},
					models.Rule{
						Name:        "Recent (" + currentYear + ")",
						Priority:    20,
						Destination: currentYear,
						Conditions: []models.Condition{
							{Field: models.FieldDate, Operator: models.OpGreaterEq, Value: "2026-01-01"},
						},
					},
				)
			}

			// If no rule specified, default to downloads preset
			if len(ruleList) == 0 {
				defaultRules, err := presets.LoadPreset("downloads")
				if err == nil {
					ruleList = defaultRules
				}
			}

			// Dynamic destination handling for {ext}
			var expandedRules []models.Rule
			for _, r := range ruleList {
				if r.Destination == "{ext}" {
					commonExts := []string{
						"jpg", "png", "pdf", "mp4", "zip", "txt", "docx", "mp3", "exe",
					}
					for _, ext := range commonExts {
						expandedRules = append(expandedRules, models.Rule{
							Name:        fmt.Sprintf("Ext: %s", strings.ToUpper(ext)),
							Priority:    r.Priority,
							Destination: strings.ToUpper(ext),
							Conditions: []models.Condition{
								{Field: models.FieldExtension, Operator: models.OpEqual, Value: ext},
							},
						})
					}
				} else {
					expandedRules = append(expandedRules, r)
				}
			}
			ruleList = expandedRules

			// Initialize scanner
			var exclusions []string
			if f.exclude != "" {
				exclusions = append(exclusions, f.exclude)
			}

			sc := scanner.New(scanner.ScanOptions{
				Recursive:      f.recursive,
				IncludeHidden:  f.hidden,
				FollowSymlinks: f.followSymlinks,
				Exclusions:     exclusions,
			})

			if f.verbose {
				fmt.Printf("[SCAN] %s\n", absTarget)
			}

			files, err := sc.Scan(absTarget)
			if err != nil {
				return fmt.Errorf("error scanning %s: %w", absTarget, err)
			}

			engine := rules.NewEngine(ruleList)
			pl := planner.New(engine, f.into, collisionPolicy)

			plan, err := pl.Plan(absTarget, files)
			if err != nil {
				return err
			}

			// Check if anything to do
			if len(plan.Operations) == 0 {
				if !f.quiet {
					fmt.Println("No files matched for reorganization.")
				}
				return nil
			}

			// Handle Dry Run
			if f.dryRun {
				printDryRun(plan, absTarget)
				return nil
			}

			// Interactive Confirmation
			if !f.yes {
				fmt.Printf("\nCHEST\n\n%d files will be moved.\n%d folders will be created.\n\nContinue? [y/N]: ",
					len(plan.Operations), len(plan.FoldersToCreate))
				reader := bufio.NewReader(os.Stdin)
				resp, _ := reader.ReadString('\n')
				resp = strings.TrimSpace(strings.ToLower(resp))
				if resp != "y" && resp != "yes" {
					fmt.Println("Operation aborted.")
					return nil
				}
			}

			// Execute Plan
			histMgr, _ := history.DefaultManager()
			progressFn := func(op models.Operation, idx, total int) {
				if f.verbose {
					relSrc, _ := filepath.Rel(absTarget, op.Source)
					relDst, _ := filepath.Rel(absTarget, op.Destination)
					fmt.Printf("[MATCH] %s -> %s (%s)\n", relSrc, relDst, op.Reason)
					fmt.Printf("[MOVE] %s\n", filepath.Base(op.Source))
				}
			}

			result, err := organizer.Execute(plan, histMgr, progressFn)
			if err != nil {
				return fmt.Errorf("execution error: %w", err)
			}

			if !f.quiet {
				fmt.Printf("\n[DONE] %d files organized into %d folders.\n", result.FilesMoved, result.FoldersCreated)
				if result.HistoryID > 0 {
					fmt.Printf("History recorded as operation #%d (use 'chest undo' to revert).\n", result.HistoryID)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&f.byType, "type", "t", false, "Sort by file type")
	cmd.Flags().BoolVarP(&f.byFormat, "format", "f", false, "Sort by extension/format")
	cmd.Flags().BoolVarP(&f.bySize, "size", "s", false, "Sort by file size")
	cmd.Flags().BoolVarP(&f.byDate, "date", "d", false, "Sort by date")
	cmd.Flags().StringVarP(&f.into, "into", "i", "", "Custom destination folder")
	cmd.Flags().StringVarP(&f.preset, "preset", "p", "", "Use predefined preset (downloads, media, documents, developer, photos)")
	cmd.Flags().BoolVarP(&f.dryRun, "dry-run", "n", false, "Preview changes without modifying filesystem")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().BoolVarP(&f.recursive, "recursive", "r", false, "Include subdirectories")
	cmd.Flags().BoolVarP(&f.verbose, "verbose", "v", false, "Detailed output")
	cmd.Flags().BoolVarP(&f.quiet, "quiet", "q", false, "Minimal output")
	cmd.Flags().StringVarP(&f.exclude, "exclude", "x", "", "Exclude files or folders (e.g. '.git,node_modules')")
	cmd.Flags().BoolVar(&f.hidden, "hidden", false, "Include hidden files")
	cmd.Flags().StringArrayVar(&f.customRules, "rule", nil, "Custom rule e.g. 'type=video && size>1GB -> Videos/Large'")
	cmd.Flags().BoolVar(&f.followSymlinks, "follow-symlinks", false, "Follow symbolic links")
	cmd.Flags().StringVar(&f.collision, "collision", "skip", "Collision policy: skip, rename, replace, abort")

	return cmd
}

func printDryRun(plan models.Plan, root string) {
	fmt.Println("CHEST PLAN")
	fmt.Println("────────────────────────")

	for _, op := range plan.Operations {
		relSrc, _ := filepath.Rel(root, op.Source)
		relDst, _ := filepath.Rel(root, op.Destination)

		fmt.Printf("\n%s\n", filepath.Base(op.Source))
		fmt.Printf("  FROM: %s\n", relSrc)
		fmt.Printf("  TO:   %s\n", relDst)
		fmt.Printf("  WHY:  %s\n", op.Reason)
		if op.IsCollision {
			fmt.Printf("  NOTE: Collision handled via policy '%s'\n", op.Action)
		}
	}

	fmt.Println("────────────────────────")
	fmt.Printf("%d files would be moved.\n", len(plan.Operations))
	if len(plan.FoldersToCreate) > 0 {
		fmt.Printf("%d folders would be created.\n", len(plan.FoldersToCreate))
	}
	fmt.Println("No changes made.")
}
