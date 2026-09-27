package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/organizer"
	"github.com/Aswanidev-vs/chest/internal/planner"
	"github.com/Aswanidev-vs/chest/internal/presets"
	"github.com/Aswanidev-vs/chest/internal/rules"
	"github.com/Aswanidev-vs/chest/internal/scanner"
	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

type replFlags struct {
	preset    string
	recursive bool
	hidden    bool
	into      string
	collision string
	exclude   string
	yes       bool
}

// replTemplate is the on-disk shape of a saved rule set. Rules are persisted as
// TOML rather than as rendered rule strings because rendering drops the
// Extensions/MinSize/MaxSize/Type helper fields.
type replTemplate struct {
	Rules []models.Rule `toml:"rules"`
}

// replSession holds everything one interactive session needs. The rule slice is
// the single source of truth: every preview and apply re-plans from it, so no
// plan is ever cached across a mutation.
type replSession struct {
	root      string
	into      string
	exclude   string
	recursive bool
	hidden    bool
	yes       bool
	collision models.CollisionPolicy

	files []models.File
	rules []models.Rule

	out io.Writer
	in  *bufio.Reader
}

func newReplCmd() *cobra.Command {
	f := replFlags{}

	cmd := &cobra.Command{
		Use:   "repl [path]",
		Short: "Build sorting rules interactively with a live preview",
		Long:  "Scan a directory, then add, edit and preview rules from a prompt. 'preview' shows exactly what 'apply' would move; it never touches the filesystem.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetDir := "."
			if len(args) > 0 {
				targetDir = args[0]
			}
			s, err := f.session(targetDir)
			if err != nil {
				return err
			}
			s.run(os.Stdin, os.Stdout)
			return nil
		},
	}

	cmd.Flags().StringVarP(&f.preset, "preset", "p", "", "Start with a predefined preset (downloads, media, documents, developer, photos)")
	cmd.Flags().BoolVarP(&f.recursive, "recursive", "r", false, "Include subdirectories")
	cmd.Flags().BoolVar(&f.hidden, "hidden", false, "Include hidden files")
	cmd.Flags().StringVarP(&f.into, "into", "i", "", "Custom destination folder")
	cmd.Flags().StringVar(&f.collision, "collision", "skip", "Collision policy: skip, rename, replace, abort")
	cmd.Flags().StringVarP(&f.exclude, "exclude", "x", "", "Exclude files or folders (e.g. '.git,node_modules')")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "Skip confirmation prompt on apply")

	return cmd
}

func (f replFlags) session(targetDir string) (*replSession, error) {
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return nil, fmt.Errorf("invalid target directory: %w", err)
	}

	// Hard refuse a protected path: repl has no --allow-system escape hatch, so
	// there is no way to let the user talk their way past the guard.
	if isSys, reason := filesystem.IsSystemPath(absTarget); isSys {
		return nil, fmt.Errorf("access denied: '%s' is located in protected %s.\nCHEST refuses to build rules against OS or system directories.", absTarget, reason)
	}

	collisionPolicy := models.CollisionPolicy(strings.ToLower(f.collision))
	if collisionPolicy == "" {
		collisionPolicy = models.CollisionSkip
	}
	switch collisionPolicy {
	case models.CollisionSkip, models.CollisionRename, models.CollisionReplace, models.CollisionAbort:
	default:
		return nil, fmt.Errorf("invalid collision policy '%s'. Valid: skip, rename, replace, abort", f.collision)
	}

	s := &replSession{
		root:      absTarget,
		into:      f.into,
		exclude:   f.exclude,
		recursive: f.recursive,
		hidden:    f.hidden,
		yes:       f.yes,
		collision: collisionPolicy,
	}

	if f.preset != "" {
		presetRules, err := presets.LoadPreset(f.preset)
		if err != nil {
			return nil, err
		}
		s.rules = presetRules
	}

	files, err := s.scanTarget()
	if err != nil {
		return nil, fmt.Errorf("error scanning %s: %w", absTarget, err)
	}
	s.files = files

	return s, nil
}

func (s *replSession) scanTarget() ([]models.File, error) {
	var exclusions []string
	if s.exclude != "" {
		exclusions = append(exclusions, s.exclude)
	}
	sc := scanner.New(scanner.ScanOptions{
		Recursive:     s.recursive,
		IncludeHidden: s.hidden,
		Exclusions:    exclusions,
	})
	return sc.Scan(s.root)
}

// plan rebuilds the engine and planner from the current rule set on every call:
// NewEngine snapshots its input, so a planner built before an edit would keep
// evaluating the old rules.
func (s *replSession) plan() (models.Plan, error) {
	engine := rules.NewEngine(s.rules)
	return planner.New(engine, s.into, s.collision).Plan(s.root, s.files)
}

func (s *replSession) run(in io.Reader, out io.Writer) {
	// One reader for the whole session: constructing a fresh bufio.Reader per
	// read would silently discard bytes already buffered by the previous one.
	s.in = bufio.NewReader(in)
	s.out = out

	fmt.Fprintf(out, "%sCHEST rules builder%s — %s (%d file(s) scanned)\nType 'help' for commands.\n",
		chestPrimary, chestReset, s.root, len(s.files))

	for {
		fmt.Fprintf(out, "%schest>%s ", chestPrimary, chestReset)

		line, err := s.in.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if !s.dispatch(trimmed) {
				return
			}
		}

		if err != nil {
			fmt.Fprintln(out)
			return
		}
	}
}

// dispatch returns false when the session should end.
func (s *replSession) dispatch(line string) bool {
	fields := strings.Fields(line)
	verb := strings.ToLower(fields[0])
	arg := strings.TrimSpace(line[len(fields[0]):])

	switch verb {
	case "help":
		s.printHelp()
	case "list":
		s.showRules()
	case "add":
		s.addRule(arg)
	case "edit":
		s.editRule(arg)
	case "rm":
		s.removeRule(arg)
	case "preset":
		s.loadPreset(arg)
	case "scan":
		s.rescan()
	case "preview":
		s.preview()
	case "apply":
		s.apply()
	case "save":
		s.saveTemplate(arg)
	case "load":
		s.loadTemplate(arg)
	case "templates":
		s.listTemplates()
	case "quit", "exit":
		return false
	default:
		s.errf("unknown command '%s' — type 'help' for the command list", verb)
	}
	return true
}

const replHelpText = `help                      Show command help
list                      Show the current rule set
add <rule>                Add a rule, e.g. add type=Video && size>1GB -> Videos/Large
edit <n> <rule>           Replace the 1-based rule n
rm <n>                    Remove the 1-based rule n
preset [name]             Load a built-in preset; no arg lists the available ones
scan                      Re-scan the target directory
preview                   Re-plan and show what would be moved (moves nothing)
apply                     Execute the current plan, after a [y/N] confirm
save <name>               Write current rules to ~/.chest/templates/<name>.toml
load <name>               Replace current rules from ~/.chest/templates/<name>.toml
templates                 List saved templates
quit / exit               Leave the builder`

func (s *replSession) printHelp() {
	fmt.Fprintf(s.out, "%sCHEST repl commands%s\n\n%s\n\n", chestGold, chestReset, replHelpText)
	fmt.Fprintf(s.out, "Conditions: %stype, ext/extension, format, size, name, date, taken_date, mime%s\n",
		chestDim, chestReset)
}

func (s *replSession) showRules() {
	if len(s.rules) == 0 {
		fmt.Fprintln(s.out, "No rules loaded. Use 'add <rule>' or 'preset [name]'.")
		return
	}
	fmt.Fprintf(s.out, "%d rule(s):\n", len(s.rules))
	for i, r := range s.rules {
		fmt.Fprintf(s.out, "  %s%2d.%s %s\n", chestCyan, i+1, chestReset, rules.FormatRule(r))
	}
}

func (s *replSession) addRule(arg string) {
	if arg == "" {
		s.errf("usage: add <rule>")
		return
	}
	// Earlier rules win, so a later, narrower rule is added with a lower priority.
	r, err := rules.ParseRule(arg, 100-len(s.rules))
	if err != nil {
		s.errf("%v", err)
		return
	}
	s.rules = append(s.rules, r)
	fmt.Fprintf(s.out, "%sadded%s %s\n", chestPrimary, chestReset, rules.FormatRule(r))
}

func (s *replSession) editRule(arg string) {
	if len(s.rules) == 0 {
		s.errf("no rules loaded")
		return
	}
	fields := strings.Fields(arg)
	if len(fields) < 2 {
		s.errf("usage: edit <n> <rule>")
		return
	}
	idx, err := ruleIndex(fields[0], len(s.rules))
	if err != nil {
		s.errf("%v", err)
		return
	}
	ruleStr := strings.TrimSpace(arg[len(fields[0]):])
	r, err := rules.ParseRule(ruleStr, s.rules[idx].Priority)
	if err != nil {
		s.errf("%v", err)
		return
	}
	s.rules[idx] = r
	fmt.Fprintf(s.out, "%sreplaced rule %d%s %s\n", chestPrimary, idx+1, chestReset, rules.FormatRule(r))
}

func (s *replSession) removeRule(arg string) {
	idx, err := ruleIndex(arg, len(s.rules))
	if err != nil {
		s.errf("%v", err)
		return
	}
	s.rules = append(s.rules[:idx], s.rules[idx+1:]...)
	fmt.Fprintf(s.out, "%sremoved rule %d%s (%d remaining)\n", chestPrimary, idx+1, chestReset, len(s.rules))
}

func (s *replSession) loadPreset(arg string) {
	if arg == "" {
		fmt.Fprintf(s.out, "presets: %s\n", strings.Join(presets.AvailablePresets(), ", "))
		return
	}
	presetRules, err := presets.LoadPreset(arg)
	if err != nil {
		s.errf("%v", err)
		return
	}
	s.rules = presetRules
	fmt.Fprintf(s.out, "%sloaded preset%s '%s' (%d rule(s))\n", chestPrimary, chestReset, strings.ToLower(arg), len(presetRules))
}

func (s *replSession) rescan() {
	files, err := s.scanTarget()
	if err != nil {
		s.errf("scan failed: %v", err)
		return
	}
	s.files = files
	fmt.Fprintf(s.out, "scanned %d file(s)\n", len(s.files))
}

func (s *replSession) preview() {
	plan, err := s.plan()
	if err != nil {
		s.errf("plan failed: %v", err)
		return
	}
	if len(plan.Operations) == 0 {
		fmt.Fprintln(s.out, "No files matched the current rules.")
		return
	}
	printDryRun(plan, s.root)
}

func (s *replSession) apply() {
	plan, err := s.plan()
	if err != nil {
		s.errf("plan failed: %v", err)
		return
	}
	if len(plan.Operations) == 0 {
		fmt.Fprintln(s.out, "No files matched the current rules — nothing to apply.")
		return
	}

	fmt.Fprintf(s.out, "\n%d file(s) will be moved.\n", len(plan.Operations))
	if !s.yes {
		fmt.Fprint(s.out, "Continue? [y/N]: ")
		resp, _ := s.in.ReadString('\n')
		resp = strings.TrimSpace(strings.ToLower(resp))
		if resp != "y" && resp != "yes" {
			fmt.Fprintln(s.out, "Aborted. Nothing was moved.")
			return
		}
	}

	histMgr, err := history.DefaultManager()
	if err != nil {
		// Execute tolerates a nil manager, so the moves would still happen with
		// nothing recorded — say so, because the result is then not undoable.
		fmt.Fprintf(s.out, "%swarning: history unavailable (%v) — this apply cannot be undone with 'chest undo'.\n", chestGold, err)
	}
	result, err := organizer.Execute(plan, histMgr, nil)
	if err != nil {
		s.errf("apply failed: %v", err)
		return
	}
	fmt.Fprintf(s.out, "[DONE] %d file(s) organized into %d folder(s).\n", result.FilesMoved, result.FoldersCreated)
	if result.HistoryID > 0 {
		fmt.Fprintf(s.out, "History recorded as operation #%d (use 'chest undo' to revert).\n", result.HistoryID)
	}

	// The sources are gone after a move, so the cached scan is stale from here on.
	files, err := s.scanTarget()
	if err != nil {
		s.errf("rescan failed: %v", err)
		return
	}
	s.files = files
}

func (s *replSession) saveTemplate(arg string) {
	name, err := sanitizeTemplateName(arg)
	if err != nil {
		s.errf("%v", err)
		return
	}
	dir, err := templatesDir()
	if err != nil {
		s.errf("%v", err)
		return
	}
	if err := filesystem.EnsureDir(dir); err != nil {
		s.errf("cannot create %s: %v", dir, err)
		return
	}

	path := filepath.Join(dir, name+".toml")
	f, err := os.Create(path)
	if err != nil {
		s.errf("cannot write %s: %v", path, err)
		return
	}
	defer f.Close()

	if err := toml.NewEncoder(f).Encode(replTemplate{Rules: s.rules}); err != nil {
		s.errf("cannot encode %s: %v", path, err)
		return
	}
	fmt.Fprintf(s.out, "saved %d rule(s) to %s\n", len(s.rules), path)
}

func (s *replSession) loadTemplate(arg string) {
	name, err := sanitizeTemplateName(arg)
	if err != nil {
		s.errf("%v", err)
		return
	}
	dir, err := templatesDir()
	if err != nil {
		s.errf("%v", err)
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, name+".toml"))
	if err != nil {
		s.errf("cannot read template '%s': %v", name, err)
		return
	}
	var tpl replTemplate
	if _, err := toml.Decode(string(data), &tpl); err != nil {
		s.errf("cannot parse template '%s': %v", name, err)
		return
	}
	s.rules = tpl.Rules
	fmt.Fprintf(s.out, "loaded %d rule(s) from template '%s'\n", len(s.rules), name)
}

func (s *replSession) listTemplates() {
	dir, err := templatesDir()
	if err != nil {
		s.errf("%v", err)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintln(s.out, "No templates saved yet.")
		return
	}
	listed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		listed++
		fmt.Fprintf(s.out, "- %s\n", strings.TrimSuffix(e.Name(), ".toml"))
	}
	if listed == 0 {
		fmt.Fprintln(s.out, "No templates saved yet.")
	}
}

func (s *replSession) errf(format string, args ...any) {
	fmt.Fprintf(s.out, chestRed+format+chestReset+"\n", args...)
}

func ruleIndex(arg string, count int) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil || n < 1 || n > count {
		return 0, fmt.Errorf("no such rule: expected a number between 1 and %d", count)
	}
	return n - 1, nil
}

func templatesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home directory: %w", err)
	}
	return filepath.Join(home, ".chest", "templates"), nil
}

// sanitizeTemplateName keeps a user-supplied name from escaping the template
// directory. Refusing beats rewriting: filepath.Base would turn '../evil' into
// a valid-looking 'evil' and let the traversal attempt through looking benign.
// ':' is rejected because Windows reads it as a drive separator or an
// alternate-data-stream marker, which would make save and load disagree about
// the same name.
func sanitizeTemplateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("template name is required")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\:") {
		return "", fmt.Errorf("invalid template name '%s': use a plain name with no path separators or colons", name)
	}
	return name, nil
}
