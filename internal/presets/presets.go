package presets

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/Aswanidev-vs/chest/internal/models"
)

//go:embed toml/downloads.toml
var downloadsPreset []byte

//go:embed toml/media.toml
var mediaPreset []byte

//go:embed toml/documents.toml
var documentsPreset []byte

//go:embed toml/developer.toml
var developerPreset []byte

//go:embed toml/photos.toml
var photosPreset []byte

// PresetConfig represents TOML preset schema
type PresetConfig struct {
	Preset struct {
		Name        string `toml:"name"`
		Description string `toml:"description"`
	} `toml:"preset"`
	Rules []models.Rule `toml:"rules"`
}

// AvailablePresets returns list of built-in preset names
func AvailablePresets() []string {
	return []string{
		"downloads",
		"media",
		"documents",
		"developer",
		"photos",
	}
}

// LoadPreset loads preset rules by name
func LoadPreset(name string) ([]models.Rule, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	var data []byte

	switch name {
	case "downloads":
		data = downloadsPreset
	case "media":
		data = mediaPreset
	case "documents":
		data = documentsPreset
	case "developer":
		data = developerPreset
	case "photos":
		data = photosPreset
	default:
		return nil, fmt.Errorf("unknown preset '%s'. Available: %s", name, strings.Join(AvailablePresets(), ", "))
	}

	var cfg PresetConfig
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse preset '%s': %w", name, err)
	}

	return cfg.Rules, nil
}
