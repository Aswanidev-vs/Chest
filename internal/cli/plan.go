package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// planFlags mirrors the subset of `sort`'s flags that shapes a plan. Keeping the
// two in lockstep is what makes `chest plan --out` and `chest sort --dry-run
// --json` produce the same artifact for the same directory.
type planFlags struct {
	byType          bool
	byFormat        bool
	bySize          bool
	byDate          bool
	dateSource      string
	dateGranularity string
	monthFormat     string
	into            string
	name            string
	preset          string
	recursive       bool
	followSymlinks  bool
	exclude         string
	hidden          bool
	customRules     []string
	collision       string
	allowSystem     bool
	out             string
}

func newPlanCmd() *cobra.Command {
	f := planFlags{}

	cmd := &cobra.Command{
		Use:   "plan [path]",
		Short: "Save an organization plan as a JSON artifact (read-only)",
		Long: `Build the same plan 'chest sort' would execute for a directory — same rules,
same precedence, same destinations — and write it to a file as JSON.

Saving a plan is strictly read-only: no folder is created and no file is moved.
The artifact is a reviewable, versionable description of the work:

  chest plan --out plan.json   write the plan to a file (JSON to stdout without --out)
  chest plan diff a.json b.json  compare two saved plans
  chest plan apply plan.json     execute a saved plan (asks for confirmation)

'chest plan apply' is the only one of the three that modifies anything, and it
records history so 'chest undo' reverts it.`,
		Example: `  chest plan ~/Downloads -t -f --out plan.json
  chest plan . -p media --recursive --out plan.json
  chest plan . --rule "type=video && size>1GB -> Videos/Large" --out plan.json
  chest plan diff before.json after.json
  chest plan apply plan.json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) > 0 {
				target = args[0]
			}

			plan, err := buildPlanArtifact(f, target)
			if err != nil {
				return err
			}

			data, err := marshalPlanArtifact(plan)
			if err != nil {
				return err
			}

			if f.out == "" {
				_, err := cmd.OutOrStdout().Write(data)
				return err
			}
			if err := os.WriteFile(f.out, data, 0o644); err != nil {
				return fmt.Errorf("failed writing plan to %s: %w", f.out, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\x1b[1;38;5;82m✔ plan saved\x1b[0m to %s \x1b[38;5;250m(%d operation(s), %d folder(s))\x1b[0m\n",
				f.out, len(plan.Operations), len(plan.FoldersToCreate))
			fmt.Fprintf(cmd.OutOrStdout(), "\x1b[38;5;250mNo files were moved. Run 'chest plan apply %s' to execute it.\x1b[0m\n", f.out)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&f.byType, "type", "t", false, "Sort by file type")
	cmd.Flags().BoolVarP(&f.byFormat, "format", "f", false, "Boolean; group files into folders by their real extension (MP4/, MP3/, PNG/). Take no value")
	cmd.Flags().BoolVarP(&f.bySize, "size", "s", false, "Sort by file size")
	cmd.Flags().BoolVarP(&f.byDate, "date", "d", false, "Sort by date")
	cmd.Flags().StringVar(&f.dateSource, "date-source", "modified", "Date source for --date and date placeholders: modified, auto, taken, filename")
	cmd.Flags().StringVar(&f.dateGranularity, "date-granularity", "year", "Date folder granularity for --date and date placeholders: year, month, day")
	cmd.Flags().StringVar(&f.monthFormat, "month-format", "name", "Month folder format for --date and {month} placeholder: name (Jan, Feb), number (01, 02)")
	cmd.Flags().StringVarP(&f.into, "into", "i", "", "Custom destination folder")
	cmd.Flags().StringVar(&f.name, "name", "", "Move all matched files flat into this folder inside the target directory; cannot be combined with --into")
	cmd.Flags().StringVarP(&f.preset, "preset", "p", "", "Use predefined preset (downloads, media, documents, developer, photos)")
	cmd.Flags().StringVar(&f.out, "out", "", "Write the plan JSON to FILE instead of stdout")
	cmd.Flags().BoolVarP(&f.recursive, "recursive", "r", false, "Include subdirectories")
	cmd.Flags().BoolVar(&f.followSymlinks, "follow-symlinks", false, "Follow symbolic links")
	cmd.Flags().StringArrayVar(&f.customRules, "rule", nil, "Custom rule e.g. 'type=video && size>1GB -> Videos/Large'")
	cmd.Flags().StringVarP(&f.exclude, "exclude", "x", "", "Exclude files or folders (e.g. '.git,node_modules')")
	cmd.Flags().BoolVar(&f.hidden, "hidden", false, "Include hidden files")
	cmd.Flags().StringVar(&f.collision, "collision", "", "Collision policy: skip, rename, replace, abort (default: auto-rename on conflicts)")
	cmd.Flags().BoolVar(&f.allowSystem, "allow-system", false, "Allow planning protected OS or system directories")

	cmd.AddCommand(newPlanApplyCmd())
	cmd.AddCommand(newPlanDiffCmd())

	return cmd
}

// buildPlanArtifact runs the sort pipeline — resolve rules, scan, classify,
// plan — and returns the resulting plan without executing a single operation.
// Every step is a read: config.Load and the scanner read files, and the planner
// only computes paths and stats them.
func buildPlanArtifact(f planFlags, targetDir string) (models.Plan, error) {
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return models.Plan{}, fmt.Errorf("invalid target directory: %w", err)
	}

	// Same guard `sort` applies. Saving a plan changes nothing, but a plan is
	// meant to be applied, and letting the artifact be produced for a location
	// that could never be executed would be a trap.
	if isSys, reason := filesystem.IsSystemPath(absTarget); isSys && !f.allowSystem {
		return models.Plan{}, fmt.Errorf("access denied: '%s' is located in protected %s.\nCHEST restricts modifications to OS and system directories.\nUse --allow-system to override if intentional", absTarget, reason)
	}
	if f.into != "" {
		if absInto, err := filepath.Abs(f.into); err == nil {
			if isSys, reason := filesystem.IsSystemPath(absInto); isSys && !f.allowSystem {
				return models.Plan{}, fmt.Errorf("access denied: destination '%s' is located in protected %s.\nUse --allow-system to override", absInto, reason)
			}
		}
	}
	if f.name != "" && f.into != "" {
		return models.Plan{}, fmt.Errorf("cannot combine --name and --into; choose one destination")
	}

	collisionPolicy, err := parseCollisionPolicy(f.collision)
	if err != nil {
		return models.Plan{}, err
	}
	dateSource, dateGranularity, monthFormat, err := parseDateFlags(f.dateSource, f.dateGranularity, f.monthFormat)
	if err != nil {
		return models.Plan{}, err
	}

	// Exactly the resolver call `sort` makes, so the rule set — and therefore
	// the plan — is identical for the same directory and flags.
	ruleList, classifyFunc, err := config.Resolve(config.Sources{
		Target:      absTarget,
		Preset:      f.preset,
		RuleStrings: f.customRules,
		ByType:      f.byType,
		ByFormat:    f.byFormat,
		BySize:      f.bySize,
		ByDate:      f.byDate,
	}, config.Hooks{})
	if err != nil {
		return models.Plan{}, err
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
		ClassifyFunc:   classifyFunc,
		ExtractDates:   dateSource != "modified",
	})

	files, err := sc.Scan(absTarget)
	if err != nil {
		return models.Plan{}, fmt.Errorf("error scanning %s: %w", absTarget, err)
	}

	engine := rules.NewEngine(expandExtDestinationRules(ruleList, files))

	baseDest, flat := f.into, false
	if f.name != "" {
		baseDest = filepath.Join(absTarget, f.name)
		flat = true
	}

	var pl *planner.Planner
	opts := planner.WithDate(dateSource, dateGranularity, monthFormat)
	if flat {
		pl = planner.NewFlat(engine, baseDest, collisionPolicy, opts)
	} else {
		pl = planner.New(engine, baseDest, collisionPolicy, opts)
	}

	return pl.Plan(absTarget, files)
}

// expandExtDestinationRules turns the {ext} destination into one concrete rule
// per extension found by the scan, mirroring what `sort` does with the -f
// flag. Kept here rather than shared because sort.go is not ours to refactor.
func expandExtDestinationRules(ruleList []models.Rule, files []models.File) []models.Rule {
	var expandedRules []models.Rule
	for _, r := range ruleList {
		if r.Destination != "{ext}" {
			expandedRules = append(expandedRules, r)
			continue
		}

		extSet := make(map[string]bool)
		for _, file := range files {
			if file.Extension != "" {
				extSet[strings.ToLower(file.Extension)] = true
			}
		}
		// Baseline fallback extensions
		commonExts := []string{
			"jpg", "jpeg", "png", "gif", "webp", "svg", "pdf", "mp4", "mkv", "avi", "mov", "zip", "tar", "gz", "7z", "rar", "txt", "md", "docx", "xlsx", "pptx", "mp3", "wav", "flac", "aac", "ogg", "exe", "msi", "iso", "json", "xml", "csv", "go", "py", "js", "ts", "html", "css",
		}
		for _, ext := range commonExts {
			extSet[ext] = true
		}
		for ext := range extSet {
			expandedRules = append(expandedRules, models.Rule{
				Name:        fmt.Sprintf("Ext: %s", strings.ToUpper(ext)),
				Priority:    r.Priority,
				Destination: strings.ToUpper(ext),
				Conditions: []models.Condition{
					{Field: models.FieldExtension, Operator: models.OpEqual, Value: ext},
				},
			})
		}
	}
	return expandedRules
}

// parseCollisionPolicy validates --collision the way `sort` does: an empty value
// is forwarded verbatim so the planner applies its implicit auto-rename default.
func parseCollisionPolicy(value string) (models.CollisionPolicy, error) {
	policy := models.CollisionPolicy(strings.ToLower(value))
	if policy == "" {
		return "", nil
	}
	switch policy {
	case models.CollisionSkip, models.CollisionRename, models.CollisionReplace, models.CollisionAbort:
		return policy, nil
	}
	return "", fmt.Errorf("invalid collision policy '%s'. Valid: skip, rename, replace, abort", value)
}

// parseDateFlags normalizes and validates the date trio, filling in the same
// defaults `sort` uses when the flags are left alone.
func parseDateFlags(source, granularity, monthFormat string) (string, string, string, error) {
	source = strings.ToLower(source)
	if source == "" {
		source = "modified"
	}
	switch source {
	case "modified", "auto", "taken", "filename":
	default:
		return "", "", "", fmt.Errorf("invalid date source '%s'. Valid: modified, auto, taken, filename", source)
	}

	granularity = strings.ToLower(granularity)
	if granularity == "" {
		granularity = "year"
	}
	switch granularity {
	case "year", "month", "day":
	default:
		return "", "", "", fmt.Errorf("invalid date granularity '%s'. Valid: year, month, day", granularity)
	}

	monthFormat = strings.ToLower(monthFormat)
	if monthFormat == "" {
		monthFormat = "name"
	}
	switch monthFormat {
	case "name", "number":
	default:
		return "", "", "", fmt.Errorf("invalid month format '%s'. Valid: name, number", monthFormat)
	}

	return source, granularity, monthFormat, nil
}

// marshalPlanArtifact renders a plan exactly as `sort --dry-run --json` does,
// plus the trailing newline that makes the artifact a well-behaved text file.
func marshalPlanArtifact(plan models.Plan) ([]byte, error) {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("error serializing plan: %w", err)
	}
	return append(data, '\n'), nil
}

func loadPlanArtifact(path string) (models.Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return models.Plan{}, fmt.Errorf("cannot read plan %s: %w", path, err)
	}
	var plan models.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return models.Plan{}, fmt.Errorf("invalid plan file %s: %w", path, err)
	}
	if plan.RootPath == "" && len(plan.Operations) > 0 {
		return models.Plan{}, fmt.Errorf("invalid plan file %s: root_path is missing", path)
	}
	return plan, nil
}

func newPlanApplyCmd() *cobra.Command {
	var (
		yes         bool
		allowSystem bool
	)

	cmd := &cobra.Command{
		Use:   "apply <plan.json>",
		Short: "Execute a plan saved by 'chest plan'",
		Long: `Execute a plan artifact produced by 'chest plan --out'.

Every operation is checked before anything moves: if a source file has vanished
since the plan was saved, or the plan points into a protected system directory,
the whole batch is refused rather than half-applied. Operations whose
destination is now occupied are skipped and reported individually.

Applying records history, so 'chest undo' restores every file the plan moved.`,
		Example: `  chest plan apply plan.json
  chest plan apply plan.json --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()

			plan, err := loadPlanArtifact(args[0])
			if err != nil {
				return err
			}
			if len(plan.Operations) == 0 {
				fmt.Fprintln(w, "Plan contains no operations. Nothing to apply.")
				return nil
			}

			// All-or-nothing pre-flight: a single blocker rejects the batch
			// before organizer.Execute is ever reached.
			ready, skipped, err := preflightPlan(plan, allowSystem)
			if err != nil {
				return err
			}
			for _, note := range skipped {
				fmt.Fprintf(w, "\x1b[38;5;250m  skipped %s\x1b[0m\n", note)
			}
			if len(ready.Operations) == 0 {
				fmt.Fprintln(w, "Every operation in this plan was skipped. Nothing to apply.")
				return nil
			}

			if !yes {
				printPlanApplySummary(w, ready, len(skipped))
				fmt.Fprint(w, "\nContinue? [y/N]: ")
				resp, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				resp = strings.TrimSpace(strings.ToLower(resp))
				if resp != "y" && resp != "yes" {
					fmt.Fprintln(w, "Operation aborted. (Use -y to skip this prompt.)")
					return nil
				}
			}

			histMgr, err := history.DefaultManager()
			if err != nil {
				return fmt.Errorf("failed opening history store: %w", err)
			}

			bar := newProgressBar(len(ready.Operations))
			result, err := organizer.Execute(ready, histMgr, func(op models.Operation, idx, total int) {
				bar.advance(idx)
			})
			bar.finish()
			if err != nil {
				return fmt.Errorf("execution error: %w", err)
			}

			fmt.Fprintf(w, "\n\x1b[1;38;5;82m✔ [DONE]\x1b[0m %d file(s) organized into %d folder(s).\n", result.FilesMoved, result.FoldersCreated)
			if result.HistoryID > 0 {
				fmt.Fprintf(w, "History recorded as operation #%d (use 'chest undo' to revert).\n", result.HistoryID)
			}
			if len(skipped) > 0 {
				fmt.Fprintf(w, "\x1b[38;5;246m%d operation(s) were skipped and left untouched.\x1b[0m\n", len(skipped))
			}
			for _, e := range result.Errors {
				fmt.Fprintf(w, "\x1b[38;5;196m✖ %s\x1b[0m\n", e)
			}
			if len(result.Errors) > 0 {
				fmt.Fprintf(w, "\x1b[1;38;5;214m%d file(s) could not be organized; the run is recorded as partial in history.\x1b[0m\n", len(result.Errors))
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&allowSystem, "allow-system", false, "Allow applying a plan that targets protected OS or system directories")
	return cmd
}

// preflightPlan validates a saved plan against the filesystem as it is right
// now and returns the subset that may be executed. Two outcomes are possible:
//
//   - error: something blocks the batch (missing source, protected path). The
//     caller aborts without moving anything.
//   - success: the executable plan plus a note per operation that is dropped
//     because the world changed since the plan was saved.
//
// The returned plan's FoldersToCreate is recomputed from the surviving
// operations so a skipped move never leaves an empty folder behind.
func preflightPlan(plan models.Plan, allowSystem bool) (models.Plan, []string, error) {
	var blocked []string
	var skipped []string

	if !allowSystem {
		if isSys, reason := filesystem.IsSystemPath(plan.RootPath); isSys {
			blocked = append(blocked, fmt.Sprintf("plan root '%s' is located in protected %s", plan.RootPath, reason))
		}
	}

	ready := make([]models.Operation, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		if !allowSystem {
			if isSys, reason := filesystem.IsSystemPath(op.Source); isSys {
				blocked = append(blocked, fmt.Sprintf("source '%s' is located in protected %s", op.Source, reason))
				continue
			}
			if isSys, reason := filesystem.IsSystemPath(op.Destination); isSys {
				blocked = append(blocked, fmt.Sprintf("destination '%s' is located in protected %s", op.Destination, reason))
				continue
			}
		}
		if filepath.Clean(op.Source) == filepath.Clean(op.Destination) {
			skipped = append(skipped, fmt.Sprintf("%s: source and destination are the same path", op.Source))
			continue
		}
		if !filesystem.Exists(op.Source) {
			blocked = append(blocked, fmt.Sprintf("source no longer exists: %s", op.Source))
			continue
		}
		// A destination that is occupied now was free when the plan was saved.
		// Overwriting it would destroy a file the plan never saw, so it is
		// skipped unless the plan explicitly asked for replace semantics.
		if op.Action != models.CollisionReplace && filesystem.Exists(op.Destination) {
			skipped = append(skipped, fmt.Sprintf("%s: destination '%s' already exists on disk", filepath.Base(op.Source), op.Destination))
			continue
		}
		ready = append(ready, op)
	}

	if len(blocked) > 0 {
		return models.Plan{}, nil, planBlockedError(len(blocked), blocked)
	}

	out := plan
	out.Operations = ready
	out.FoldersToCreate = planFoldersFor(ready)
	return out, skipped, nil
}

// planBlockedError builds the single error that aborts a whole plan batch,
// listing what stopped it.
func planBlockedError(count int, blocked []string) error {
	const maxListed = 10
	var b strings.Builder
	fmt.Fprintf(&b, "refusing to apply plan: %d blocking problem(s), no files were moved", count)
	for i, p := range blocked {
		if i == maxListed {
			fmt.Fprintf(&b, "\n  … and %d more", count-maxListed)
			break
		}
		fmt.Fprintf(&b, "\n  • %s", p)
	}
	return errors.New(b.String())
}

// planFoldersFor lists the not-yet-existing parent directories of a set of
// operations, matching what the planner records in FoldersToCreate.
func planFoldersFor(ops []models.Operation) []string {
	folders := make(map[string]bool)
	for _, op := range ops {
		dir := filepath.Dir(op.Destination)
		if !filesystem.Exists(dir) {
			folders[dir] = true
		}
	}
	out := make([]string, 0, len(folders))
	for dir := range folders {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

func printPlanApplySummary(w io.Writer, plan models.Plan, skipped int) {
	fmt.Fprintf(w, "\nCHEST PLAN APPLY\n\n%d file(s) will be moved.\n", len(plan.Operations))

	destCounts := make(map[string]int)
	for _, op := range plan.Operations {
		relDst, err := filepath.Rel(plan.RootPath, op.Destination)
		if err != nil {
			relDst = op.Destination
		}
		destCounts[filepath.Dir(relDst)]++
	}
	dests := make([]string, 0, len(destCounts))
	for d := range destCounts {
		dests = append(dests, d)
	}
	sort.Strings(dests)
	fmt.Fprintln(w, "\nDestination summary:")
	for _, d := range dests {
		fmt.Fprintf(w, "  %-28s %d file(s)\n", d, destCounts[d])
	}

	if len(plan.FoldersToCreate) > 0 {
		fmt.Fprintf(w, "\n%d folder(s) will be created.\n", len(plan.FoldersToCreate))
	}
	if skipped > 0 {
		fmt.Fprintf(w, "\x1b[38;5;246m%d operation(s) will be skipped.\x1b[0m\n", skipped)
	}
}

// planOpChange is one operation that exists in both plans but differs.
type planOpChange struct {
	Source string           `json:"source"`
	Before models.Operation `json:"before"`
	After  models.Operation `json:"after"`
}

// planDiff is the machine-readable result of comparing two plan artifacts.
type planDiff struct {
	From           string             `json:"from"`
	To             string             `json:"to"`
	Added          []models.Operation `json:"added"`
	Removed        []models.Operation `json:"removed"`
	Changed        []planOpChange     `json:"changed"`
	Unchanged      int                `json:"unchanged"`
	FoldersAdded   []string           `json:"folders_added"`
	FoldersRemoved []string           `json:"folders_removed"`
}

// diffPlans keys operations by their (case-folded) source path, which is the
// only stable identity an operation carries across two runs.
func diffPlans(from, to string, a, b models.Plan) planDiff {
	aOps := indexPlanOps(a)
	bOps := indexPlanOps(b)

	d := planDiff{From: from, To: to, Added: []models.Operation{}, Removed: []models.Operation{}, Changed: []planOpChange{}}
	for key, opA := range aOps {
		opB, ok := bOps[key]
		if !ok {
			d.Removed = append(d.Removed, opA)
			continue
		}
		if opsEqual(opA, opB) {
			d.Unchanged++
			continue
		}
		d.Changed = append(d.Changed, planOpChange{Source: opA.Source, Before: opA, After: opB})
	}
	for key, opB := range bOps {
		if _, ok := aOps[key]; !ok {
			d.Added = append(d.Added, opB)
		}
	}
	sort.Slice(d.Added, func(i, j int) bool { return d.Added[i].Source < d.Added[j].Source })
	sort.Slice(d.Removed, func(i, j int) bool { return d.Removed[i].Source < d.Removed[j].Source })
	sort.Slice(d.Changed, func(i, j int) bool { return d.Changed[i].Source < d.Changed[j].Source })

	d.FoldersAdded = folderDelta(a.FoldersToCreate, b.FoldersToCreate)
	d.FoldersRemoved = folderDelta(b.FoldersToCreate, a.FoldersToCreate)
	return d
}

func indexPlanOps(p models.Plan) map[string]models.Operation {
	out := make(map[string]models.Operation, len(p.Operations))
	for _, op := range p.Operations {
		key := filesystem.CaseKey(op.Source)
		if _, seen := out[key]; !seen {
			out[key] = op
		}
	}
	return out
}

func opsEqual(a, b models.Operation) bool {
	return a.Destination == b.Destination &&
		a.Reason == b.Reason &&
		a.Action == b.Action &&
		a.IsCollision == b.IsCollision
}

func folderDelta(from, to []string) []string {
	have := make(map[string]bool, len(to))
	for _, f := range to {
		have[filesystem.CaseKey(f)] = true
	}
	out := []string{}
	for _, f := range from {
		if !have[filesystem.CaseKey(f)] {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func csvRowsForPlanDiff(d planDiff) [][]string {
	rows := [][]string{{"change", "source", "before_destination", "after_destination", "reason"}}
	for _, op := range d.Added {
		rows = append(rows, []string{"added", op.Source, "", op.Destination, op.Reason})
	}
	for _, op := range d.Removed {
		rows = append(rows, []string{"removed", op.Source, op.Destination, "", op.Reason})
	}
	for _, c := range d.Changed {
		rows = append(rows, []string{"changed", c.Source, c.Before.Destination, c.After.Destination, c.After.Reason})
	}
	return rows
}

func newPlanDiffCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "diff <before.json> <after.json>",
		Short: "Compare two saved plans and report added, removed and changed operations",
		Long: `Compare two plan artifacts written by 'chest plan --out' and report what
changed between them: operations added, operations removed, and operations that
still exist but point somewhere else (or carry a different reason or collision
policy).

This is a read-only comparison; neither file is executed or modified.`,
		Example: `  chest plan diff before.json after.json
  chest plan diff before.json after.json --json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			before, err := loadPlanArtifact(args[0])
			if err != nil {
				return err
			}
			after, err := loadPlanArtifact(args[1])
			if err != nil {
				return err
			}

			d := diffPlans(args[0], args[1], before, after)
			if asJSON {
				return emitReport(cmd.OutOrStdout(), d, csvRowsForPlanDiff(d), true, false, "")
			}
			printPlanDiff(cmd.OutOrStdout(), d)
			return nil
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the diff as JSON instead of the human report")
	return cmd
}

func printPlanDiff(w io.Writer, d planDiff) {
	fmt.Fprint(w, "CHEST PLAN DIFF\n────────────────────────\n")
	fmt.Fprintf(w, "FROM  \x1b[38;5;250m%s\x1b[0m\n", d.From)
	fmt.Fprintf(w, "TO    \x1b[38;5;250m%s\x1b[0m\n\n", d.To)

	if len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0 {
		fmt.Fprintf(w, "\x1b[1;38;5;82m✔ plans are identical\x1b[0m \x1b[38;5;250m(%d unchanged operation(s))\x1b[0m\n", d.Unchanged)
		return
	}

	if len(d.Added) > 0 {
		fmt.Fprintf(w, "\x1b[1;38;5;82m+ %d added\x1b[0m\n", len(d.Added))
		for _, op := range d.Added {
			fmt.Fprintf(w, "  \x1b[38;5;82m✔\x1b[0m %s \x1b[38;5;250m-> %s\x1b[0m\n", op.Source, op.Destination)
		}
	}
	if len(d.Removed) > 0 {
		fmt.Fprintf(w, "\x1b[1;38;5;214m- %d removed\x1b[0m\n", len(d.Removed))
		for _, op := range d.Removed {
			fmt.Fprintf(w, "  \x1b[38;5;214m✖\x1b[0m %s \x1b[38;5;250m(-> %s)\x1b[0m\n", op.Source, op.Destination)
		}
	}
	if len(d.Changed) > 0 {
		fmt.Fprintf(w, "\x1b[1;38;5;214m~ %d changed\x1b[0m\n", len(d.Changed))
		for _, c := range d.Changed {
			fmt.Fprintf(w, "  \x1b[38;5;214m~\x1b[0m %s\n", c.Source)
			fmt.Fprintf(w, "      \x1b[38;5;250m- %s\x1b[0m\n", c.Before.Destination)
			fmt.Fprintf(w, "      \x1b[38;5;250m+ %s\x1b[0m\n", c.After.Destination)
			if c.Before.Reason != c.After.Reason {
				fmt.Fprintf(w, "      \x1b[38;5;250mwhy: %s -> %s\x1b[0m\n", c.Before.Reason, c.After.Reason)
			}
		}
	}

	if len(d.FoldersAdded) > 0 || len(d.FoldersRemoved) > 0 {
		fmt.Fprint(w, "\n\x1b[38;5;246mfolders\x1b[0m\n")
		for _, f := range d.FoldersAdded {
			fmt.Fprintf(w, "  \x1b[38;5;82m+\x1b[0m %s\n", f)
		}
		for _, f := range d.FoldersRemoved {
			fmt.Fprintf(w, "  \x1b[38;5;214m-\x1b[0m %s\n", f)
		}
	}

	fmt.Fprintf(w, "\n\x1b[38;5;246m%d unchanged, %d added, %d removed, %d changed\x1b[0m\n",
		d.Unchanged, len(d.Added), len(d.Removed), len(d.Changed))
}
