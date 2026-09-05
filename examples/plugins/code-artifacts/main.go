package main

import (
	"strings"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/plugin"
	hplugin "github.com/hashicorp/go-plugin"
)

// CodeArtifactsPlugin implements the ClassifierService interface
type CodeArtifactsPlugin struct{}

// GetInfo returns plugin metadata and capabilities
func (p *CodeArtifactsPlugin) GetInfo() (plugin.Manifest, error) {
	return plugin.Manifest{
		Name:         "code-artifacts",
		Version:      "1.0.0",
		Description:  "Classifies developer build artifacts, WASM binaries, and coverage reports",
		Author:       "CHEST Community",
		Capabilities: []string{"classifier", "rule"},
		Binary:       "code-artifacts",
	}, nil
}

// Classify extends CHEST's built-in classifier with custom categories.
// Returning "" signals CHEST to fall back to its internal classifier.
func (p *CodeArtifactsPlugin) Classify(filename string, ext string) (string, error) {
	lowerName := strings.ToLower(filename)

	if strings.HasSuffix(lowerName, ".coverage.out") || strings.HasSuffix(lowerName, ".lcov") || lowerName == "coverage.html" {
		return "CoverageReports", nil
	}

	if ext == "wasm" {
		return "WebAssembly", nil
	}

	if strings.HasSuffix(lowerName, ".d.ts.map") {
		return "TypeMaps", nil
	}

	return "", nil
}

// GetCustomRules injects dynamic sorting rules directly into chest sort and chest watch.
func (p *CodeArtifactsPlugin) GetCustomRules() ([]models.Rule, error) {
	return []models.Rule{
		{
			Name:        "WebAssembly Binaries",
			Priority:    80,
			Destination: "Build/Wasm",
			Conditions: []models.Condition{
				{Field: models.FieldExtension, Operator: models.OpEqual, Value: "wasm"},
			},
		},
		{
			Name:        "Test Coverage Reports",
			Priority:    75,
			Destination: "Reports/Coverage",
			Conditions: []models.Condition{
				{Field: models.FieldType, Operator: models.OpEqual, Value: "CoverageReports"},
			},
		},
	}, nil
}

func main() {
	hplugin.Serve(&hplugin.ServeConfig{
		HandshakeConfig: plugin.HandshakeConfig,
		Plugins: map[string]hplugin.Plugin{
			"classifier": &plugin.ClassifierPluginRPC{
				Impl: &CodeArtifactsPlugin{},
			},
		},
	})
}
