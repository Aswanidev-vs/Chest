package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	gosort "sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aswanidev-vs/chest/internal/config"
	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/organizer"
	"github.com/Aswanidev-vs/chest/internal/planner"
	"github.com/Aswanidev-vs/chest/internal/rules"
	"github.com/Aswanidev-vs/chest/internal/scanner"
	"github.com/spf13/cobra"
)

type renameFlags struct {
	to              string
	dateSource      string
	dateGranularity string
	monthFormat     string
	collision       string
	preset          string
	customRules     []string
	byType          bool
	byFormat        bool
	bySize          bool
	byDate          bool
	dryRun          bool
	jsonPlan        bool
	yes             bool
	recursive       bool
	verbose         bool
	quiet           bool
	exclude         string
	hidden          bool
	followSymlinks  bool
	allowSystem     bool
}

func newRenameCmd() *cobra.Command {
	f := renameFlags{}

	cmd := &cobra.Command{
		Use:   "rename [path]",
		Short: "Rename files in place using a template",
		Long: `Rename files without moving them out of their folder.

Every scanned file is renamed to the --to template, which accepts the same
placeholders CHEST already uses for destinations:

  {ext}    lowercase extension without its dot, e.g. jpg — write "photo.{ext}"
           for "photo.jpg" (empty when the file has no extension)
  {date}   year, year-month or year-month-day depending on --date-granularity
  {year}   four digit year
  {month}  month, as Jan or 01 depending on --month-format
  {day}    two digit day of month

The template must not contain path separators — this command renames in place
and never moves a file between directories. Where --to produces a name that is
already taken, the collision policy decides what happens; the default walks the
"photo (1).jpg" chain so nothing is ever overwritten silently.

Pass --rule, --preset or -t/-f/-s/-d to rename only the files those rules match;
with none of them every scanned file is renamed.`,
		Example: `  chest rename . --to "{year}-{month}-{day}.{ext}"
  chest rename ~/Pictures -r --to "IMG_{year}{month}{day}.{ext}" --date-source filename
  chest rename Downloads --to "{year}-{month}.{ext}" --rule "ext=jpg -> Photos"
  chest rename . --to "scan-{year}.{ext}" --dry-run`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			targetDir := "."
			if len(args) > 0 {
				targetDir = args[0]
			}
			absTarget, err := filepath.Abs(targetDir)
			if err != nil {
				return fmt.Errorf("invalid target directory: %w", err)
			}

			// Guard: System and OS path protection, same policy as `sort`.
			if isSys, reason := filesystem.IsSystemPath(absTarget); isSys {
				if !f.allowSystem {
					return fmt.Errorf("access denied: '%s' is located in protected %s.\nCHEST restricts modifications to OS and system directories.\nUse --allow-system to override if intentional", absTarget, reason)
				}
				if !f.yes {
					fmt.Fprintf(out, "\n\x1b[1;38;5;196m⚠ CRITICAL SAFETY WARNING:\x1b[0m\n")
					fmt.Fprintf(out, "You are targeting protected %s:\n  \x1b[38;5;220m%s\x1b[0m\n", reason, absTarget)
					fmt.Fprintf(out, "Renaming files here may alter OS/system files!\n\n")
					fmt.Fprint(out, "Are you sure you want to proceed with system path modifications? [y/N]: ")
					resp, _ := bufio.NewReader(os.Stdin).ReadString('\n')
					resp = strings.TrimSpace(strings.ToLower(resp))
					if resp != "y" && resp != "yes" {
						fmt.Fprintln(out, "Operation aborted for safety.")
						return nil
					}
				}
			}

			if strings.TrimSpace(f.to) == "" {
				return fmt.Errorf("--to is required: give the rename template (e.g. --to \"{year}-{month}-{day}.{ext}\")")
			}
			if err := validateRenameTemplate(f.to); err != nil {
				return err
			}

			collisionPolicy, err := normalizeCollisionPolicy(f.collision)
			if err != nil {
				return err
			}
			if collisionPolicy == "" {
				// Rename resolves names against other files that are usually
				// still on disk, so the implicit planner default (auto-rename on
				// intra-plan conflicts only) is not protective enough here:
				// default to the explicit rename policy so an in-place rename
				// can never land on an occupied name.
				collisionPolicy = models.CollisionRename
			}

			dateSource, err := normalizeDateSource(f.dateSource)
			if err != nil {
				return err
			}
			dateGranularity, err := normalizeDateGranularity(f.dateGranularity)
			if err != nil {
				return err
			}
			monthFormat, err := normalizeMonthFormat(f.monthFormat)
			if err != nil {
				return err
			}

			// The rule engine only selects WHICH files get renamed; --to always
			// decides the new name. With no selection flag at all every scanned
			// file is renamed, which is why no engine is built in that case.
			var engine *rules.Engine
			if f.preset != "" || len(f.customRules) > 0 || f.byType || f.byFormat || f.bySize || f.byDate {
				ruleList, _, err := config.Resolve(config.Sources{
					Target:      absTarget,
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
				engine = rules.NewEngine(ruleList)
			}

			var exclusions []string
			if f.exclude != "" {
				exclusions = append(exclusions, f.exclude)
			}

			sc := scanner.New(scanner.ScanOptions{
				Recursive:      f.recursive,
				IncludeHidden:  f.hidden,
				FollowSymlinks: f.followSymlinks,
				Exclusions:     exclusions,
				ExtractDates:   dateSource != "modified",
			})

			if f.verbose {
				fmt.Fprintf(out, "[SCAN] %s\n", absTarget)
			}

			files, err := sc.Scan(absTarget)
			if err != nil {
				return fmt.Errorf("error scanning %s: %w", absTarget, err)
			}

			plan, problems, err := buildRenamePlan(absTarget, files, renameOptions{
				Template:        f.to,
				Engine:          engine,
				DateSource:      dateSource,
				DateGranularity: dateGranularity,
				MonthFormat:     monthFormat,
				CollisionPolicy: collisionPolicy,
			})
			if err != nil {
				return err
			}

			// Files the template could not turn into a usable name are reported
			// rather than silently left behind.
			for _, problem := range problems {
				fmt.Fprintf(errOut, "\x1b[38;5;196m✖ %s\x1b[0m\n", problem)
			}

			if len(plan.Operations) == 0 {
				if !f.quiet {
					fmt.Fprintln(out, "No files matched for renaming.")
				}
				return nil
			}

			if f.dryRun {
				if f.jsonPlan {
					data, err := json.MarshalIndent(plan, "", "  ")
					if err != nil {
						return fmt.Errorf("error serializing plan: %w", err)
					}
					fmt.Fprintln(out, string(data))
					return nil
				}
				printRenameDryRun(out, plan, absTarget)
				return nil
			}

			if !f.yes {
				fmt.Fprintf(out, "\nCHEST RENAME PLAN\n\n%d file(s) will be renamed in place.\n", len(plan.Operations))
				fmt.Fprintln(out, "\nDestination summary:")
				destCounts := make(map[string]int)
				for _, op := range plan.Operations {
					destCounts[filepath.Dir(op.Destination)]++
				}
				dests := make([]string, 0, len(destCounts))
				for d := range destCounts {
					dests = append(dests, d)
				}
				gosort.Strings(dests)
				for _, d := range dests {
					fmt.Fprintf(out, "  %-28s %d file(s)\n", d, destCounts[d])
				}
				if len(plan.Conflicts) > 0 {
					printRenameConflicts(out, plan, absTarget)
				}
				fmt.Fprint(out, "\nContinue? [y/N]: ")
				resp, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				resp = strings.TrimSpace(strings.ToLower(resp))
				if resp != "y" && resp != "yes" {
					fmt.Fprintln(out, "Operation aborted. (Use --dry-run to preview, or -y to skip this prompt.)")
					return nil
				}
			}

			// History is recorded with OriginalSource = the old path and
			// Destination = the new path, which is exactly the shape `chest undo`
			// reverses.
			histMgr, _ := history.DefaultManager()
			var bar *progressBar
			if !f.verbose && !f.quiet {
				bar = newProgressBar(len(plan.Operations))
			}
			progressFn := func(op models.Operation, idx, total int) {
				if f.verbose {
					fmt.Fprintf(out, "[RENAME] %s -> %s (%s)\n", filepath.Base(op.Source), filepath.Base(op.Destination), op.Reason)
				} else if bar != nil {
					bar.advance(idx)
				}
			}

			result, err := organizer.Execute(plan, histMgr, progressFn)
			if bar != nil {
				bar.finish()
			}
			if err != nil {
				return fmt.Errorf("execution error: %w", err)
			}

			if !f.quiet {
				fmt.Fprintf(out, "\n\x1b[1;38;5;82m✔\x1b[0m [DONE] %d file(s) renamed in place.\n", result.FilesMoved)
				if result.HistoryID > 0 {
					fmt.Fprintf(out, "History recorded as operation #%d (use 'chest undo' to revert).\n", result.HistoryID)
				}
				for _, e := range result.Errors {
					fmt.Fprintf(out, "\x1b[38;5;196m✖ %s\x1b[0m\n", e)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&f.to, "to", "", "Rename template, e.g. \"{year}-{month}-{day}.{ext}\" (placeholders: {ext}, {date}, {year}, {month}, {day})")
	cmd.Flags().StringVar(&f.dateSource, "date-source", "modified", "Date source for the template's date placeholders: modified, auto, taken, filename")
	cmd.Flags().StringVar(&f.dateGranularity, "date-granularity", "year", "Granularity of {date}: year, month, day")
	cmd.Flags().StringVar(&f.monthFormat, "month-format", "name", "Format of {month}: name (Jan), number (01)")
	cmd.Flags().StringVar(&f.collision, "collision", "", "Collision policy: skip, rename, replace, abort (default: rename)")
	cmd.Flags().StringVarP(&f.preset, "preset", "p", "", "Rename only the files these predefined rules match (downloads, media, documents, developer, photos)")
	cmd.Flags().StringArrayVar(&f.customRules, "rule", nil, "Rename only the files matching this rule, e.g. 'ext=jpg -> Photos'")
	cmd.Flags().BoolVarP(&f.byType, "type", "t", false, "Rename only files whose type the -t rules match")
	cmd.Flags().BoolVarP(&f.byFormat, "format", "f", false, "Rename only files whose format the -f rules match")
	cmd.Flags().BoolVarP(&f.bySize, "size", "s", false, "Rename only files whose size the -s rules match")
	cmd.Flags().BoolVarP(&f.byDate, "date", "d", false, "Rename only files the -d date rules match")
	cmd.Flags().BoolVarP(&f.dryRun, "dry-run", "n", false, "Preview changes without modifying filesystem")
	cmd.Flags().BoolVar(&f.jsonPlan, "json", false, "With --dry-run, print the plan as JSON (operations, conflicts)")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().BoolVarP(&f.recursive, "recursive", "r", false, "Include subdirectories")
	cmd.Flags().BoolVarP(&f.verbose, "verbose", "v", false, "Detailed output")
	cmd.Flags().BoolVarP(&f.quiet, "quiet", "q", false, "Minimal output")
	cmd.Flags().StringVarP(&f.exclude, "exclude", "x", "", "Exclude files or folders (e.g. '.git,node_modules')")
	cmd.Flags().BoolVar(&f.hidden, "hidden", false, "Include hidden files")
	cmd.Flags().BoolVar(&f.followSymlinks, "follow-symlinks", false, "Follow symbolic links")
	cmd.Flags().BoolVar(&f.allowSystem, "allow-system", false, "Allow modifications in protected OS or system directories")

	return cmd
}

// renameOptions is everything buildRenamePlan needs, kept separate from the
// cobra flags so the planning logic can be exercised directly.
type renameOptions struct {
	Template        string
	Engine          *rules.Engine // nil renames every scanned file
	DateSource      string
	DateGranularity string
	MonthFormat     string
	CollisionPolicy models.CollisionPolicy
}

// buildRenamePlan turns scanned files into an in-place rename plan. It never
// creates a folder and never targets a path outside the source file's own
// directory, so the plan can only ever be applied as renames. The returned
// problem strings explain files that were left untouched, and the error is
// non-nil only for the abort collision policy.
func buildRenamePlan(root string, files []models.File, opts renameOptions) (models.Plan, []string, error) {
	plan := models.Plan{RootPath: root}
	var problems []string
	// claimed maps a destination name (case-folded) to the file that already
	// claimed it, so an in-plan collision can be reported like a disk one.
	claimed := make(map[string]string)

	for _, file := range files {
		if file.IsDir {
			continue
		}
		plan.TotalFiles++

		// Optional rule selection: without an engine every file is renamed.
		if opts.Engine != nil {
			if _, _, matched := opts.Engine.Evaluate(file); !matched {
				plan.SkippedFiles++
				continue
			}
		}

		date, ok := planner.ResolveDate(file, opts.DateSource)
		if !ok {
			plan.SkippedFiles++
			problems = append(problems, fmt.Sprintf("%s has no embedded date for --date-source %s; left unchanged", file.Name, opts.DateSource))
			continue
		}

		newName := expandRenameTemplate(opts.Template, file, date, opts.DateGranularity, opts.MonthFormat)
		if newName == "" || newName == "." || newName == ".." {
			plan.SkippedFiles++
			problems = append(problems, fmt.Sprintf("template produced an unusable name for %s; left unchanged", file.Name))
			continue
		}

		srcDir := filepath.Dir(file.Path)
		targetFile := filepath.Join(srcDir, newName)

		// Hard invariant: a rename never leaves its directory. The template is
		// validated for separators up front, and this re-check guarantees it
		// even if a placeholder ever grows one.
		if filesystem.CaseKey(filepath.Dir(targetFile)) != filesystem.CaseKey(srcDir) {
			plan.SkippedFiles++
			problems = append(problems, fmt.Sprintf("template would move %s out of its folder; left unchanged", file.Name))
			continue
		}

		if filesystem.CaseKey(targetFile) == filesystem.CaseKey(file.Path) {
			// Nothing to do: either the name is identical, or it differs only by
			// case on a case-insensitive filesystem where a rename is a no-op.
			plan.SkippedFiles++
			continue
		}

		action := opts.CollisionPolicy
		actualTarget := targetFile
		firstClaimer, claimedInPlan := claimed[filesystem.CaseKey(targetFile)]
		onDisk := filesystem.Exists(targetFile)
		isCollision := onDisk || claimedInPlan
		reason := fmt.Sprintf("rename template %s", opts.Template)

		if isCollision {
			// With an on-disk collision there is no plan operation to blame, so
			// the occupied path itself stands in as the first claimant.
			claimant := firstClaimer
			if claimant == "" {
				claimant = targetFile
			}
			switch opts.CollisionPolicy {
			case models.CollisionSkip:
				plan.Conflicts = append(plan.Conflicts, models.PlanConflict{
					Target:     file.Path,
					Sources:    []string{claimant, file.Path},
					Resolution: "target name already taken; file left unchanged under skip policy",
				})
				plan.SkippedFiles++
				continue
			case models.CollisionRename:
				actualTarget = resolveRenameTarget(targetFile, claimed)
				plan.Conflicts = append(plan.Conflicts, models.PlanConflict{
					Target:     actualTarget,
					Sources:    []string{claimant, file.Path},
					Resolution: "auto-renamed to " + filepath.Base(actualTarget),
				})
			case models.CollisionReplace:
				plan.Conflicts = append(plan.Conflicts, models.PlanConflict{
					Target:     targetFile,
					Sources:    []string{claimant, file.Path},
					Resolution: "overwrite under replace policy",
				})
			case models.CollisionAbort:
				return plan, problems, fmt.Errorf("rename collision detected: '%s' already exists in '%s'", newName, srcDir)
			}
		}

		claimed[filesystem.CaseKey(actualTarget)] = file.Path

		plan.Operations = append(plan.Operations, models.Operation{
			Source:      file.Path,
			Destination: actualTarget,
			Reason:      reason,
			Action:      action,
			IsCollision: isCollision,
		})
	}

	return plan, problems, nil
}

// validateRenameTemplate rejects templates that could move a file out of its
// folder, which `chest rename` never does.
func validateRenameTemplate(tpl string) error {
	if strings.ContainsAny(tpl, `/\`) {
		return fmt.Errorf("invalid rename template: path separators are not allowed, 'chest rename' only renames files in place")
	}
	return nil
}

// expandRenameTemplate renders the --to template for a single file. It reuses
// the placeholder vocabulary the planner already applies to destinations —
// {ext} carries no dot there either, so a template writes "photo.{ext}" — but
// joins the date parts with "-" (a filename cannot contain a separator) and
// renders {ext} lowercase, matching how the resulting names read.
func expandRenameTemplate(tpl string, file models.File, date time.Time, granularity, monthFormat string) string {
	name := tpl

	ext := strings.ToLower(file.Extension)
	name = strings.ReplaceAll(name, "{ext}", ext)
	if ext == "" {
		// The file has no extension, so "photo.{ext}" would leave a trailing
		// dot, which Windows silently strips anyway. Drop it instead.
		name = strings.TrimSuffix(name, ".")
	}

	granularity = strings.ToLower(granularity)
	if granularity == "" {
		granularity = "year"
	}
	month := date.Format("Jan")
	if strings.ToLower(monthFormat) == "number" {
		month = fmt.Sprintf("%02d", int(date.Month()))
	}
	year := strconv.Itoa(date.Year())
	day := fmt.Sprintf("%02d", date.Day())

	datePart := year
	switch granularity {
	case "month":
		datePart = year + "-" + month
	case "day":
		datePart = year + "-" + month + "-" + day
	}

	name = strings.ReplaceAll(name, "{date}", datePart)
	name = strings.ReplaceAll(name, "{year}", year)
	name = strings.ReplaceAll(name, "{month}", month)
	name = strings.ReplaceAll(name, "{day}", day)
	return name
}

// resolveRenameTarget returns a destination that is free on disk and not
// already claimed by an earlier rename in this run. filesystem.ResolveCollision
// owns the disk-aware half of the walk (photo.jpg -> photo (1).jpg); because it
// is disk-aware only, the very same " (n)" chain is continued here whenever the
// name it produced is claimed by a rename that has not happened yet.
func resolveRenameTarget(target string, claimed map[string]string) string {
	candidate := target
	for counter := 1; ; counter++ {
		free := filesystem.ResolveCollision(candidate)
		if _, taken := claimed[filesystem.CaseKey(free)]; !taken {
			return free
		}
		candidate = collisionVariant(free, counter)
	}
}

// collisionVariant returns the "stem (n).ext" form of path, replacing any
// suffix already present so repeated resolution cannot drift into
// "name (1) (2).ext".
func collisionVariant(path string, n int) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if open := strings.LastIndex(stem, " ("); open >= 0 && strings.HasSuffix(stem, ")") {
		if _, err := strconv.Atoi(stem[open+2 : len(stem)-1]); err == nil {
			stem = stem[:open]
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, n, ext))
}

// printRenameDryRun previews a rename plan in the same FROM/TO shape as the
// sort dry run, with the rename count instead of a move count.
func printRenameDryRun(w io.Writer, plan models.Plan, root string) {
	fmt.Fprintln(w, "CHEST RENAME PLAN")
	fmt.Fprintln(w, "────────────────────────")

	for _, op := range plan.Operations {
		relSrc, _ := filepath.Rel(root, op.Source)
		relDst, _ := filepath.Rel(root, op.Destination)
		fmt.Fprintf(w, "\n%s\n", filepath.Base(op.Source))
		fmt.Fprintf(w, "  FROM: %s\n", relSrc)
		fmt.Fprintf(w, "  TO:   %s\n", relDst)
		fmt.Fprintf(w, "  WHY:  %s\n", op.Reason)
		if op.IsCollision {
			fmt.Fprintf(w, "  NOTE: Collision handled via policy '%s'\n", op.Action)
		}
	}

	fmt.Fprintln(w, "────────────────────────")
	fmt.Fprintf(w, "%d files would be renamed.\n", len(plan.Operations))
	printRenameConflicts(w, plan, root)
	fmt.Fprintln(w, "No changes made.")
}

func printRenameConflicts(w io.Writer, plan models.Plan, root string) {
	if len(plan.Conflicts) == 0 {
		return
	}
	fmt.Fprintf(w, "\n\x1b[1;38;5;214m%d conflict(s) detected during planning:\x1b[0m\n", len(plan.Conflicts))
	for _, c := range plan.Conflicts {
		relTarget, relErr := filepath.Rel(root, c.Target)
		if relErr != nil {
			relTarget = c.Target
		}
		fmt.Fprintf(w, "  \x1b[1;38;5;220m%s\x1b[0m\n", relTarget)
		for _, src := range c.Sources {
			relSrc, relErr := filepath.Rel(root, src)
			if relErr != nil {
				relSrc = src
			}
			fmt.Fprintf(w, "    • %s\n", relSrc)
		}
		fmt.Fprintf(w, "    \x1b[38;5;246m→ %s\x1b[0m\n", c.Resolution)
	}
}
