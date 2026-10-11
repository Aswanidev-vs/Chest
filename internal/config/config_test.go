package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/plugin"
)

// isolateHome redirects the home directory to a fresh empty temp dir so the
// user config is never read from the real ~/.chest during a test. It returns
// the isolated home path.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // Windows: os.UserHomeDir reads this
	t.Setenv("HOME", home)        // non-Windows: portable
	return home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const projectTOML = `
[[rules]]
name = "ProjectRule"
priority = 50
destination = "ProjectDest"
`

func TestLoadWalkUpDiscovery(t *testing.T) {
	isolateHome(t)

	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	nested := filepath.Join(proj, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(proj, FileName), projectTOML)

	cfg, err := Load(nested) // start deep; should find proj/chest.toml walking up
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].Destination != "ProjectDest" {
		t.Fatalf("expected the project rule to be discovered, got %+v", cfg.Rules)
	}
}

func TestLoadNoConfigIsNotAnError(t *testing.T) {
	isolateHome(t)

	root := t.TempDir() // no chest.toml anywhere above an isolated home
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load() with no config should not error, got %v", err)
	}
	if len(cfg.Rules) != 0 || cfg.DefaultPreset != "" {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
}

func TestLoadMalformedTOMLError(t *testing.T) {
	isolateHome(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, FileName)
	writeFile(t, cfgPath, "default_preset = \"unterminated\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected an error for malformed TOML")
	}
	if !strings.Contains(err.Error(), cfgPath) {
		t.Fatalf("error should name the offending file %q, got %v", cfgPath, err)
	}
}

func TestLoadMalformedUserTOMLError(t *testing.T) {
	home := isolateHome(t)
	writeFile(t, filepath.Join(home, ".chest", FileName), "[[rules]]\nname = \n")

	_, err := Load(t.TempDir())
	if err == nil {
		t.Fatal("expected an error for malformed user TOML")
	}
	if !strings.Contains(err.Error(), ".chest") {
		t.Fatalf("error should name the user config file, got %v", err)
	}
}

func TestResolveProjectOverUserPrecedence(t *testing.T) {
	home := isolateHome(t)
	writeFile(t, filepath.Join(home, ".chest", FileName), `
[[rules]]
name = "UserRule"
priority = 50
destination = "UserDest"
`)

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, FileName), projectTOML)

	rules, _, err := Resolve(Sources{Target: proj}, Hooks{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	projIdx, userIdx := -1, -1
	for i, r := range rules {
		switch r.Destination {
		case "ProjectDest":
			projIdx = i
		case "UserDest":
			userIdx = i
		}
	}
	if projIdx == -1 || userIdx == -1 {
		t.Fatalf("expected both project and user rules, got %+v", rules)
	}
	// Earlier entries win equal-priority ties, so the project rule (higher
	// precedence) must come before the user rule.
	if projIdx > userIdx {
		t.Fatalf("project rule (idx %d) must precede user rule (idx %d)", projIdx, userIdx)
	}
}

func TestResolveOrderingPluginLastAndRuleBeforeFlag(t *testing.T) {
	isolateHome(t)

	proj := t.TempDir() // isolated: no config on the walk-up path
	pluginRule := models.Rule{Name: "PluginRule", Priority: 100, Destination: "PluginDest"}

	rules, _, err := Resolve(
		Sources{Target: proj, RuleStrings: []string{"a.txt -> A"}, ByType: true},
		Hooks{PluginRules: []models.Rule{pluginRule}},
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if len(rules) == 0 {
		t.Fatal("expected some rules")
	}
	last := rules[len(rules)-1]
	if last.Name != "PluginRule" || last.Destination != "PluginDest" {
		t.Fatalf("plugin rule must be last, got %q", last.Name)
	}

	ruleIdx, flagIdx := -1, -1
	for i, r := range rules {
		if r.Destination == "A" { // --rule "a.txt -> A"
			ruleIdx = i
		}
		if r.Name == "Type: Image" { // a flag-generated rule
			flagIdx = i
		}
	}
	if ruleIdx == -1 || flagIdx == -1 {
		t.Fatalf("expected both a --rule and a flag rule, got %+v", rules)
	}
	if ruleIdx > flagIdx {
		t.Fatalf("--rule (idx %d) must come before flag rules (idx %d)", ruleIdx, flagIdx)
	}
}

// fakeClassifier is a stub ClassifierService for exercising the plugin
// fallback in the composite classifier.
type fakeClassifier struct{ out string }

func (f fakeClassifier) GetInfo() (plugin.Manifest, error)             { return plugin.Manifest{}, nil }
func (f fakeClassifier) Classify(filename, ext string) (string, error) { return f.out, nil }
func (f fakeClassifier) GetCustomRules() ([]models.Rule, error)        { return nil, nil }

func TestResolveClassifyFallbackChain(t *testing.T) {
	isolateHome(t)
	proj := t.TempDir()

	// No plugins: built-in classifier only, "Other" for unknown extensions.
	_, classify, err := Resolve(Sources{Target: proj}, Hooks{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got := classify("clip.mp4", "mp4"); got != "Video" {
		t.Errorf("classify mp4 = %q, want Video", got)
	}
	if got := classify("mystery.zzz", "zzz"); got != "Other" {
		t.Errorf("classify unknown ext with no plugin = %q, want Other", got)
	}

	// With a plugin: built-in still wins for known types, plugin answers the
	// ones the built-in leaves as Other.
	svc := fakeClassifier{out: "Widget"}
	_, classify2, err := Resolve(
		Sources{Target: proj, RuleStrings: []string{"*.zzz -> Widgets"}},
		Hooks{PluginServices: []plugin.ClassifierService{svc}},
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got := classify2("clip.mp4", "mp4"); got != "Video" {
		t.Errorf("built-in classifier must win first, got %q", got)
	}
	if got := classify2("thing.zzz", "zzz"); got != "Widget" {
		t.Errorf("plugin fallback for unknown ext = %q, want Widget", got)
	}
}

func TestNormalizeConfigPriorities(t *testing.T) {
	rules := []models.Rule{
		{Name: "kept", Priority: 30, Destination: "K"},
		{Name: "unset0", Priority: 0, Destination: "U0"},
		{Name: "unsetNeg", Priority: -5, Destination: "UN"},
	}
	NormalizeConfigPriorities(rules)

	if rules[0].Priority != 30 {
		t.Errorf("explicit priority must be kept, got %d", rules[0].Priority)
	}
	if rules[1].Priority != 100 || rules[2].Priority != 99 {
		t.Errorf("unset priorities must be assigned 100 then 99, got %d and %d", rules[1].Priority, rules[2].Priority)
	}
}
