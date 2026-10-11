package config

import (
	"fmt"

	"github.com/Aswanidev-vs/chest/internal/classifier"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/plugin"
	"github.com/Aswanidev-vs/chest/internal/presets"
	"github.com/Aswanidev-vs/chest/internal/rules"
)

// Sources carries everything the command line contributed to rule resolution.
type Sources struct {
	Target      string   // directory the config walk-up starts from
	Preset      string   // --preset value (may be empty)
	RuleStrings []string // raw --rule strings
	ByType      bool     // -t
	ByFormat    bool     // -f
	BySize      bool     // -s
	ByDate      bool     // -d
}

// Hooks carries plugin-provided rules and services that the resolver layers on
// last. EnrichFunc is not consumed here; callers wire enrichment into the
// scanner and watcher independently.
type Hooks struct {
	PluginServices []plugin.ClassifierService
	PluginRules    []models.Rule
	// ClassifyFunc, when set, overrides the built-in classifier chain the
	// resolver builds by default (built-in classifier first, then plugin
	// fallback). Both callers leave it nil and use the composite.
	ClassifyFunc func(filename, ext string) string
}

// Resolve assembles the final ordered rule slice shared by sort and watch.
// Precedence, from lowest to highest when two rules carry the same priority:
// plugin rules < preset rules < user config < project config < --rule strings
// < flag-generated rules. Because the engine breaks equal-priority ties by
// slice order (earlier entries win), that precedence is expressed as the slice
// [flag, --rule, project-config, user-config, preset, plugin]. --rule and flag
// rules never share a priority (100-i vs 10-25), so their relative order does
// not change real matches.
//
// It also returns the composite classifier: built-in classifier.ClassifyExtension
// first, then a plugin fallback when plugin services are present.
func Resolve(src Sources, hooks Hooks) ([]models.Rule, func(filename, ext string) string, error) {
	cfg, err := Load(src.Target)
	if err != nil {
		return nil, nil, err
	}
	configRules := cfg.Rules
	NormalizeConfigPriorities(configRules)

	// Determine the preset: an explicit --preset wins, then the config's
	// default_preset, then the historical downloads default when nothing else
	// was specified at all.
	presetName := src.Preset
	if presetName == "" {
		presetName = cfg.DefaultPreset
	}
	if presetName == "" && len(configRules) == 0 && len(src.RuleStrings) == 0 &&
		!src.ByType && !src.ByFormat && !src.BySize && !src.ByDate {
		presetName = "downloads"
	}
	var presetRules []models.Rule
	if presetName != "" {
		presetRules, err = presets.LoadPreset(presetName)
		if err != nil {
			return nil, nil, err
		}
	}

	// --rule strings get priority 100-i, matching the historical behavior.
	var ruleStrings []models.Rule
	for i, s := range src.RuleStrings {
		r, err := rules.ParseRule(s, 100-i)
		if err != nil {
			return nil, nil, fmt.Errorf("error parsing --rule: %w", err)
		}
		ruleStrings = append(ruleStrings, r)
	}

	flagRules := buildFlagRules(src)

	out := make([]models.Rule, 0, len(ruleStrings)+len(flagRules)+len(configRules)+len(presetRules)+len(hooks.PluginRules))
	out = append(out, ruleStrings...)       // --rule (highest precedence at ties)
	out = append(out, flagRules...)         // -t/-f/-s/-d flag rules
	out = append(out, configRules...)       // project config, then user config
	out = append(out, presetRules...)       // preset rules
	out = append(out, hooks.PluginRules...) // plugin rules last (lowest tie priority)

	classify := hooks.ClassifyFunc
	if classify == nil {
		classify = NewCompositeClassifier(hooks.PluginServices)
	}
	return out, classify, nil
}

// buildFlagRules reproduces the flag-driven automatic rules the sort command
// builds today; their internal order and priorities are unchanged.
func buildFlagRules(src Sources) []models.Rule {
	var out []models.Rule
	if src.ByType {
		categories := []string{
			classifier.TypeImage, classifier.TypeVideo, classifier.TypeAudio,
			classifier.TypeDocument, classifier.TypeArchive, classifier.TypeExecutable,
			classifier.TypeCode, classifier.TypeOther,
		}
		for _, cat := range categories {
			out = append(out, models.Rule{
				Name:        "Type: " + cat,
				Priority:    20,
				Destination: cat + "s",
				Conditions: []models.Condition{
					{Field: models.FieldType, Operator: models.OpEqual, Value: cat},
				},
			})
		}
	}
	if src.ByFormat {
		out = append(out, models.Rule{Name: "Format/Extension", Priority: 10, Destination: "{ext}"})
	}
	if src.BySize {
		out = append(out,
			models.Rule{Name: "Huge Files (>1GB)", Priority: 25, Destination: "Huge Files", MinSize: "1GB"},
			models.Rule{Name: "Large Files (100MB-1GB)", Priority: 24, Destination: "Large Files", MinSize: "100MB", MaxSize: "1GB"},
			models.Rule{Name: "Medium Files (10MB-100MB)", Priority: 23, Destination: "Medium Files", MinSize: "10MB", MaxSize: "100MB"},
			models.Rule{Name: "Small Files (1MB-10MB)", Priority: 22, Destination: "Small Files", MinSize: "1MB", MaxSize: "10MB"},
			models.Rule{Name: "Tiny Files (<1MB)", Priority: 21, Destination: "Tiny Files", MaxSize: "1MB"},
		)
	}
	if src.ByDate {
		out = append(out, models.Rule{Name: "Date", Priority: 20, Destination: "{date}"})
	}
	return out
}

// NewCompositeClassifier returns a classifier that consults the built-in
// extension classifier first and falls back to the supplied plugin services
// when the built-in result is empty or "Other". A nil service slice yields a
// built-in-only classifier.
func NewCompositeClassifier(services []plugin.ClassifierService) func(filename, ext string) string {
	return func(filename, ext string) string {
		cat := classifier.ClassifyExtension(ext)
		if cat != classifier.TypeOther && cat != "" {
			return cat
		}
		if len(services) > 0 {
			if pluginCat := plugin.ClassifyWithPlugins(services, filename, ext); pluginCat != "" {
				return pluginCat
			}
		}
		return cat
	}
}
