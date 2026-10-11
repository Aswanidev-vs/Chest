// Package config loads chest.toml configuration files and resolves the final
// rule set used by the file-organizer pipeline.
//
// A chest.toml declares an optional default preset plus [[rules]] entries. Two
// files are consulted: the "user" config at ~/.chest/chest.toml and the
// "project" config, located by walking up from the target directory to the
// filesystem root (the first chest.toml found wins). On top of those, the
// command-line --rule strings, preset and flag-generated rules are layered by
// precedence; Resolve assembles the final slice shared by sort and watch.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/BurntSushi/toml"
)

// FileName is the per-directory configuration file chest looks for.
const FileName = "chest.toml"

// Config is the decoded schema of a single chest.toml file. The toml tags on
// models.Rule drive the [[rules]] table array (name, priority, destination,
// conditions[{field,operator,value}], extensions, min_size, max_size, type).
type Config struct {
	DefaultPreset string        `toml:"default_preset"`
	Rules         []models.Rule `toml:"rules"`
}

// Load finds and merges the project and user configs for targetDir. It walks up
// from targetDir to the filesystem root and treats the first chest.toml found
// as the project config; it also reads ~/.chest/chest.toml as the user config.
// Missing files are not an error.
//
// The returned Config merges both sources with the project rules taking
// precedence: the rule engine breaks equal-priority ties by slice order and
// evaluates earlier entries first, so project rules are placed before user
// rules. DefaultPreset prefers the project config's value, then the user's.
// Malformed TOML returns an error that names the offending file path.
func Load(targetDir string) (Config, error) {
	var project, user Config

	if pp := findProject(targetDir); pp != "" {
		if err := decode(pp, &project); err != nil {
			return Config{}, err
		}
	}
	if up := userPath(); up != "" && fileExists(up) {
		if err := decode(up, &user); err != nil {
			return Config{}, err
		}
	}

	merged := Config{DefaultPreset: project.DefaultPreset}
	if merged.DefaultPreset == "" {
		merged.DefaultPreset = user.DefaultPreset
	}
	merged.Rules = make([]models.Rule, 0, len(project.Rules)+len(user.Rules))
	merged.Rules = append(merged.Rules, project.Rules...) // project wins ties
	merged.Rules = append(merged.Rules, user.Rules...)
	return merged, nil
}

// findProject walks up from targetDir and returns the path of the first
// chest.toml found, or "" when none exists on the path to the root.
func findProject(targetDir string) string {
	dir, err := filepath.Abs(targetDir)
	if err != nil {
		return ""
	}
	dir = filepath.Clean(dir)
	for {
		candidate := filepath.Join(dir, FileName)
		if fileExists(candidate) {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached the filesystem root
			return ""
		}
		dir = parent
	}
}

// userPath returns ~/.chest/chest.toml, or "" when the home directory is
// unavailable.
func userPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".chest", FileName)
}

// decode reads and parses a single chest.toml, wrapping any failure with the
// file path so the user knows which file is malformed.
func decode(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config %s: %w", path, err)
	}
	if _, err := toml.Decode(string(data), cfg); err != nil {
		return fmt.Errorf("failed to parse config %s: %w", path, err)
	}
	return nil
}

// NormalizeConfigPriorities replaces a zero-or-negative priority (the TOML zero
// value when a rule omits "priority") with a synthetic descending priority
// starting at 100, mirroring the --rule "100-i" convention and keeping the
// unset rules above the preset baseline. Rules with an explicit priority keep
// it. Applied in place so the author's declaration order is preserved among the
// unset rules.
func NormalizeConfigPriorities(rules []models.Rule) {
	next := 100
	for i := range rules {
		if rules[i].Priority <= 0 {
			rules[i].Priority = next
			next--
		}
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
