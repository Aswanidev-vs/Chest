package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPluginManager(t *testing.T) {
	tempDir := t.TempDir()
	pluginHome := filepath.Join(tempDir, "installed")
	srcDir := filepath.Join(tempDir, "source-anime")
	_ = os.MkdirAll(srcDir, 0755)

	mgr, err := NewManager(pluginHome)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	manifest := Manifest{
		Name:         "anime",
		Version:      "1.0.0",
		Description:  "Anime and manga organizer plugin",
		Author:       "Chest Community",
		Capabilities: []string{"classifier", "rule"},
		Binary:       "anime-plugin",
	}

	mfData, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(srcDir, "manifest.json"), mfData, 0644)
	_ = os.WriteFile(filepath.Join(srcDir, "anime-plugin"), []byte("#!/bin/sh\nexit 0"), 0755)

	// 2. Test Install
	installed, err := mgr.Install(srcDir)
	if err != nil {
		t.Fatalf("Failed to install plugin: %v", err)
	}
	if installed.Name != "anime" {
		t.Errorf("Expected name anime, got %s", installed.Name)
	}

	// 3. Test List
	list, err := mgr.List()
	if err != nil {
		t.Fatalf("Failed to list plugins: %v", err)
	}
	if len(list) != 1 || list[0].Name != "anime" {
		t.Errorf("Expected 1 plugin in list, got %d", len(list))
	}

	// 4. Test Info
	info, err := mgr.GetInfo("anime")
	if err != nil {
		t.Fatalf("Failed to get plugin info: %v", err)
	}
	if info.Version != "1.0.0" {
		t.Errorf("Expected version 1.0.0, got %s", info.Version)
	}

	// 5. Test Remove
	if err := mgr.Remove("anime"); err != nil {
		t.Fatalf("Failed to remove plugin: %v", err)
	}

	listAfter, _ := mgr.List()
	if len(listAfter) != 0 {
		t.Errorf("Expected 0 plugins after removal, got %d", len(listAfter))
	}
}

func TestPluginTomlManifest(t *testing.T) {
	tempDir := t.TempDir()
	pluginHome := filepath.Join(tempDir, "installed")
	srcDir := filepath.Join(tempDir, "source-wasm")
	_ = os.MkdirAll(srcDir, 0755)

	mgr, err := NewManager(pluginHome)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	tomlContent := `name = "wasm-sorter"
version = "2.1.0"
description = "WebAssembly organizer plugin"
author = "Wasm Dev"
capabilities = ["classifier", "rule"]
binary = "wasm-sorter"
`
	_ = os.WriteFile(filepath.Join(srcDir, "manifest.toml"), []byte(tomlContent), 0644)
	_ = os.WriteFile(filepath.Join(srcDir, "wasm-sorter"), []byte("#!/bin/sh\nexit 0"), 0755)

	// Install from TOML manifest
	installed, err := mgr.Install(srcDir)
	if err != nil {
		t.Fatalf("Failed to install TOML plugin: %v", err)
	}
	if installed.Name != "wasm-sorter" || installed.Version != "2.1.0" {
		t.Errorf("Unexpected installed manifest: %+v", installed)
	}

	// Read via GetInfo
	info, err := mgr.GetInfo("wasm-sorter")
	if err != nil {
		t.Fatalf("Failed to get TOML plugin info: %v", err)
	}
	if info.Author != "Wasm Dev" {
		t.Errorf("Expected author 'Wasm Dev', got %s", info.Author)
	}
}
