package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Aswanidev-vs/chest/internal/classifier"
	"github.com/Aswanidev-vs/chest/internal/config"
	"github.com/Aswanidev-vs/chest/internal/metadata"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/planner"
	"github.com/Aswanidev-vs/chest/internal/rules"
	"github.com/spf13/cobra"
)

type ruleCheckFlags struct {
	byType      bool
	byFormat    bool
	bySize      bool
	byDate      bool
	preset      string
	customRules []string
}

func newRuleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule",
		Short: "Rule utilities",
	}
	cmd.AddCommand(newRuleCheckCmd())
	cmd.AddCommand(newRuleEvalCmd())
	return cmd
}

func newRuleCheckCmd() *cobra.Command {
	f := ruleCheckFlags{}

	cmd := &cobra.Command{
		Use:   "check [directory]",
		Short: "Validate a rule set without scanning or moving files",
		Long: `Validate the rules that would apply to a directory — from flags, presets
and any chest.toml discovered by walking up from the target — and report every
problem that would otherwise be silently ignored at evaluation time (unparseable
size values, unsupported field/operator combinations, invalid regexes).`,
		Example: `  chest rule check ~/Downloads -p media
  chest rule check ~/Code --rule "type=video && size>1GB -> Videos/Large"
  chest rule check . --rule "ext=jpg min_size=abc -> Images"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) > 0 {
				target = args[0]
			}

			ruleList, _, err := config.Resolve(config.Sources{
				Target:      target,
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

			if len(ruleList) == 0 {
				fmt.Println("No rules resolved for this directory.")
				return nil
			}

			problems := rules.ValidateRules(ruleList)
			if len(problems) == 0 {
				fmt.Printf("\x1b[1;38;5;82m✔ %d rule(s) validated, no problems found.\x1b[0m\n", len(ruleList))
				return nil
			}

			fmt.Printf("\x1b[1;38;5;196m%d problem(s) found in %d rule(s):\x1b[0m\n", len(problems), len(ruleList))
			for _, p := range problems {
				fmt.Printf("  \x1b[38;5;214m•\x1b[0m %s\n", p)
			}
			return fmt.Errorf("%d invalid rule(s)", len(problems))
		},
	}

	bindRuleFlags(cmd, &f, "Preset whose rules to validate", "Custom rule string to validate")

	return cmd
}

// bindRuleFlags registers the flags every rule subcommand understands, so
// `rule check` and `rule eval` can never drift apart on what they accept. The
// two help strings are passed in because each subcommand describes what it does
// with the rules differently ("validate" vs "evaluate").
func bindRuleFlags(cmd *cobra.Command, f *ruleCheckFlags, presetUsage, ruleUsage string) {
	cmd.Flags().BoolVarP(&f.byType, "type", "t", false, "Include the type-category rules (-t)")
	cmd.Flags().BoolVarP(&f.byFormat, "format", "f", false, "Include the format/extension rules (-f)")
	cmd.Flags().BoolVarP(&f.bySize, "size", "s", false, "Include the size-bucket rules (-s)")
	cmd.Flags().BoolVarP(&f.byDate, "date", "d", false, "Include the date rules (-d)")
	cmd.Flags().StringVarP(&f.preset, "preset", "p", "", presetUsage)
	cmd.Flags().StringArrayVarP(&f.customRules, "rule", "r", nil, ruleUsage)
}

func newRuleEvalCmd() *cobra.Command {
	f := ruleCheckFlags{}

	cmd := &cobra.Command{
		Use:   "eval <file>",
		Short: "Explain which rule matches a file, why, and where it would go",
		Long: `Resolve the rule set that would apply to a single file — from flags, presets
and any chest.toml discovered by walking up from the file's directory — and print
the rule set, which rule matched (its index and name), the destination that would
be produced with every placeholder expanded, and the reason, the same WHY line
'chest sort --dry-run' prints.

This is read-only: nothing is scanned, no folder is created and no file is
moved. 'rule check' validates rule syntax; 'rule eval' validates behavior.
A file that matches no rule is reported as such and is not an error.`,
		Example: `  chest rule eval ~/Downloads/report.pdf -t
  chest rule eval ./clip.mp4 -p media
  chest rule eval ./notes.txt -r "type=document && size<1MB -> Docs/Small"
  chest rule eval ./mystery.bin --rule "ext=jpg -> Images"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()

			path, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("invalid file path: %w", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("cannot read %s: %w", args[0], err)
			}
			if info.IsDir() {
				return fmt.Errorf("%s is a directory; rule eval needs a single file", args[0])
			}

			// The file's own directory is the config discovery root, exactly as
			// `sort` would use the directory it scans.
			root := filepath.Dir(path)
			ruleList, classify, err := config.Resolve(config.Sources{
				Target:      root,
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

			file := inspectFileForEval(path, info, classify)

			fmt.Fprintln(w, "CHEST RULE EVAL")
			fmt.Fprintln(w, "────────────────────────")
			printEvalFileFacts(w, file, root)
			printEvalRuleSet(w, ruleList)

			if len(ruleList) == 0 {
				fmt.Fprint(w, "\n\x1b[38;5;250mno rules resolved, so no rule can match this file.\x1b[0m\n")
				return nil
			}

			match, reason, matched := matchRuleInOrder(ruleList, file)
			if !matched {
				fmt.Fprint(w, "\n\x1b[1;38;5;214mNO RULE MATCHED\x1b[0m \x1b[38;5;250m— 'chest sort' would leave this file in place.\x1b[0m\n")
				fmt.Fprint(w, "\x1b[38;5;250mNothing was moved; this was a read-only evaluation.\x1b[0m\n")
				return nil
			}

			destination, note := evalDestinationFor(root, ruleList, file, match)
			fmt.Fprintf(w, "\n\x1b[1;38;5;82mMATCH\x1b[0m   rule #%d \x1b[1;38;5;82m%s\x1b[0m\n", match.index, match.rule.Name)
			fmt.Fprintf(w, "TO     %s\n", destination)
			fmt.Fprintf(w, "WHY    %s\n", reason)
			if match.rule.Destination != "" {
				fmt.Fprintf(w, "       \x1b[38;5;250mroute %q \x1b[38;5;246m(priority %d)\x1b[0m\n", match.rule.Destination, match.rule.Priority)
			}
			if note != "" {
				fmt.Fprintf(w, "       \x1b[38;5;246m%s\x1b[0m\n", note)
			}
			fmt.Fprint(w, "\n\x1b[38;5;250mNothing was moved; this was a read-only evaluation.\x1b[0m\n")
			return nil
		},
	}

	bindRuleFlags(cmd, &f, "Preset whose rules to evaluate", "Custom rule string to evaluate")

	return cmd
}

// evalMatch is the rule the engine settled on, together with its position in
// the evaluation order (the priority-sorted order the engine walks).
type evalMatch struct {
	index int
	rule  models.Rule
}

func printEvalFileFacts(w io.Writer, file models.File, root string) {
	category := file.Category
	if category == "" {
		category = "(uncategorized)"
	}
	fmt.Fprintf(w, "FILE   %s\n", file.Name)
	fmt.Fprintf(w, "  path  %s\n", file.Path)
	fmt.Fprintf(w, "  type  \x1b[38;5;250m%s (ext %q, %s, modified %s)\x1b[0m\n",
		category, file.Extension, formatFileSize(file.Size), file.ModTime.Format("2006-01-02 15:04"))
	fmt.Fprintf(w, "  root  \x1b[38;5;250m%s\x1b[0m\n\n", root)
}

func printEvalRuleSet(w io.Writer, ruleList []models.Rule) {
	ordered := engineRuleOrder(ruleList)
	fmt.Fprintf(w, "RULE SET \x1b[38;5;250m%d rule(s), in evaluation order\x1b[0m\n", len(ordered))
	for i, r := range ordered {
		fmt.Fprintf(w, "  \x1b[38;5;250m[%d] p%-4d %-40s -> %s\x1b[0m\n", i, r.Priority, truncateRuleLabel(r.Name), r.Destination)
	}
}

func truncateRuleLabel(name string) string {
	name = strings.ReplaceAll(name, "\n", " ")
	if len(name) > 40 {
		return name[:39] + "…"
	}
	return name
}

// engineRuleOrder reproduces the ordering rules.NewEngine applies internally:
// a stable sort by descending priority. The engine does not expose its slice,
// so the same order is rebuilt here to number the rules the way they are
// actually tried.
func engineRuleOrder(ruleList []models.Rule) []models.Rule {
	ordered := make([]models.Rule, len(ruleList))
	copy(ordered, ruleList)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Priority > ordered[j].Priority
	})
	return ordered
}

// matchRuleInOrder finds the first rule in engine order that matches the file
// and returns its index and name. Each candidate is evaluated by a one-rule
// engine, which is exactly how rules.NewEngine would evaluate it inside the
// full set, so the answer matches Engine.Evaluate without needing the engine's
// private slice.
func matchRuleInOrder(ruleList []models.Rule, file models.File) (evalMatch, string, bool) {
	for i, r := range engineRuleOrder(ruleList) {
		if _, reason, ok := rules.NewEngine([]models.Rule{r}).Evaluate(file); ok {
			return evalMatch{index: i, rule: r}, reason, true
		}
	}
	return evalMatch{}, "", false
}

// evalDestinationFor reports where the matched rule would send the file. The
// planner is asked first so the answer is exactly what `chest sort` would
// produce; it returns no operation when the file already sits at its
// destination or that destination is occupied, in which case the placeholders
// are expanded locally for display.
func evalDestinationFor(root string, ruleList []models.Rule, file models.File, match evalMatch) (string, string) {
	pl := planner.New(rules.NewEngine(ruleList), "", "", planner.WithDate("modified", "year", "name"))
	plan, err := pl.Plan(root, []models.File{file})
	if err == nil && len(plan.Operations) > 0 {
		note := ""
		if plan.Operations[0].IsCollision {
			note = "destination is already occupied; the file would be renamed or skipped"
		}
		return relOrAbs(root, plan.Operations[0].Destination), note
	}
	return relOrAbs(root, expandRuleDestination(match.rule.Destination, file, root)), ""
}

// expandRuleDestination mirrors planner.Plan for the one file being evaluated:
// {ext} plus the date placeholders, resolved against the root when the route is
// relative. Only reachable when the planner produced no operation for a matched
// file, so it is a display fallback, never the authority.
func expandRuleDestination(destination string, file models.File, root string) string {
	destDir := destination
	if strings.Contains(destDir, "{ext}") {
		ext := strings.ToUpper(file.Extension)
		if ext == "" {
			ext = "NO_EXT"
		}
		destDir = strings.ReplaceAll(destDir, "{ext}", ext)
	}
	if strings.Contains(destDir, "{date}") || strings.Contains(destDir, "{year}") ||
		strings.Contains(destDir, "{month}") || strings.Contains(destDir, "{day}") {
		date := file.ModTime
		month := date.Format("Jan")
		destDir = strings.ReplaceAll(destDir, "{date}", strconv.Itoa(date.Year()))
		destDir = strings.ReplaceAll(destDir, "{year}", strconv.Itoa(date.Year()))
		destDir = strings.ReplaceAll(destDir, "{month}", month)
		destDir = strings.ReplaceAll(destDir, "{day}", fmt.Sprintf("%02d", date.Day()))
	}
	if filepath.IsAbs(destDir) {
		return filepath.Join(destDir, file.Name)
	}
	return filepath.Join(root, destDir, file.Name)
}

func relOrAbs(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// inspectFileForEval builds the same file record the scanner would produce for
// this path — classification, signature metadata, taken date — so the rule
// engine sees exactly the input it sees during a real sort. Every step is a
// read; nothing is written or moved.
func inspectFileForEval(path string, info os.FileInfo, classify func(filename, ext string) string) models.File {
	name := filepath.Base(path)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))

	category := classifier.ClassifyExtension(ext)
	if classify != nil {
		category = classify(name, ext)
	}

	file := models.File{
		Path:      path,
		RelPath:   name,
		Name:      name,
		Extension: ext,
		Size:      info.Size(),
		ModTime:   info.ModTime(),
		Category:  category,
	}

	// Always extract: the scanner only does this when a date flag is set, but
	// taken_date, mime and meta.* rules are exactly the ones an evaluation is
	// most often used to debug.
	if meta, err := metadata.Extract(path, ext); err == nil {
		if meta.Format != "" {
			file.Format = meta.Format
		}
		if file.MIMEType == "" {
			file.MIMEType = meta.MIMEType
		}
		if file.Category == classifier.TypeOther && meta.Category != "" {
			file.Category = meta.Category
		}
		if len(meta.Fields) > 0 {
			file.Metadata = meta.Fields
		}
		if !meta.Date.Date.IsZero() {
			date := meta.Date.Date
			file.TakenDate = &date
			file.TakenDateSource = meta.Date.Source
		}
	}

	return file
}
