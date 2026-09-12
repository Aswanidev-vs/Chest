package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/rpc"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/BurntSushi/toml"
	hplugin "github.com/hashicorp/go-plugin"
)

// HandshakeConfig ensures plugin and host agree on protocol.
// ProtocolVersion 2 adds the metadata capability (Inspect RPC); plugins built
// against protocol 1 must be recompiled.
var HandshakeConfig = hplugin.HandshakeConfig{
	ProtocolVersion:  2,
	MagicCookieKey:   "CHEST_PLUGIN",
	MagicCookieValue: "CHEST_IN_A_CHEST",
}

// PluginMap maps plugin types to implementations
var PluginMap = map[string]hplugin.Plugin{
	"classifier": &ClassifierPluginRPC{},
}

// mergedPluginMap builds the full set of served plugin types for host
// clients, combining the classifier and metadata protocols so a single
// plugin binary can expose both capabilities over one connection.
func mergedPluginMap() map[string]hplugin.Plugin {
	m := make(map[string]hplugin.Plugin, len(PluginMap)+len(MetadataPluginMap))
	for name, p := range PluginMap {
		m[name] = p
	}
	for name, p := range MetadataPluginMap {
		m[name] = p
	}
	return m
}

// Manifest represents plugin metadata stored in manifest.json, manifest.toml, or returned by plugin
type Manifest struct {
	Name         string   `json:"name" toml:"name"`
	Version      string   `json:"version" toml:"version"`
	Description  string   `json:"description" toml:"description"`
	Author       string   `json:"author,omitempty" toml:"author,omitempty"`
	Capabilities []string `json:"capabilities" toml:"capabilities"` // e.g. ["classifier", "metadata", "preset", "rule"]
	Binary       string   `json:"binary" toml:"binary"`             // executable filename
}

// ClassifierService is the interface exposed by plugins
type ClassifierService interface {
	GetInfo() (Manifest, error)
	Classify(filename string, ext string) (string, error)
	GetCustomRules() ([]models.Rule, error)
}

// RPC Server/Client implementation
type ClassifierRPCClient struct {
	client *rpc.Client
}

func (c *ClassifierRPCClient) GetInfo() (Manifest, error) {
	var resp Manifest
	err := c.client.Call("Plugin.GetInfo", new(interface{}), &resp)
	return resp, err
}

func (c *ClassifierRPCClient) Classify(filename string, ext string) (string, error) {
	args := []string{filename, ext}
	var resp string
	err := c.client.Call("Plugin.Classify", args, &resp)
	return resp, err
}

func (c *ClassifierRPCClient) GetCustomRules() ([]models.Rule, error) {
	var resp []models.Rule
	err := c.client.Call("Plugin.GetCustomRules", new(interface{}), &resp)
	return resp, err
}

type ClassifierRPCServer struct {
	Impl ClassifierService
}

func (s *ClassifierRPCServer) GetInfo(args interface{}, resp *Manifest) error {
	m, err := s.Impl.GetInfo()
	if err != nil {
		return err
	}
	*resp = m
	return nil
}

func (s *ClassifierRPCServer) Classify(args []string, resp *string) error {
	if len(args) < 2 {
		return fmt.Errorf("invalid args")
	}
	cat, err := s.Impl.Classify(args[0], args[1])
	if err != nil {
		return err
	}
	*resp = cat
	return nil
}

func (s *ClassifierRPCServer) GetCustomRules(args interface{}, resp *[]models.Rule) error {
	rules, err := s.Impl.GetCustomRules()
	if err != nil {
		return err
	}
	*resp = rules
	return nil
}

type ClassifierPluginRPC struct {
	Impl ClassifierService
}

func (p *ClassifierPluginRPC) Server(*hplugin.MuxBroker) (interface{}, error) {
	return &ClassifierRPCServer{Impl: p.Impl}, nil
}

func (p *ClassifierPluginRPC) Client(b *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &ClassifierRPCClient{client: c}, nil
}

// Manager discovers and manages local plugins
type Manager struct {
	pluginDir string
	clients   map[string]*hplugin.Client
	mu        sync.Mutex
}

// NewManager creates a plugin manager pointing to target directory (default ~/.chest/plugins)
func NewManager(customDir ...string) (*Manager, error) {
	dir := ""
	if len(customDir) > 0 && customDir[0] != "" {
		dir = customDir[0]
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".chest", "plugins")
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	return &Manager{
		pluginDir: dir,
		clients:   make(map[string]*hplugin.Client),
	}, nil
}

// GetPluginDir returns plugin storage folder
func (m *Manager) GetPluginDir() string {
	return m.pluginDir
}

// readManifest tries reading manifest.toml first, then manifest.json
func readManifest(dir string) (*Manifest, error) {
	// Try manifest.toml first
	tomlPath := filepath.Join(dir, "manifest.toml")
	if data, err := os.ReadFile(tomlPath); err == nil {
		var mf Manifest
		if err := toml.Unmarshal(data, &mf); err == nil {
			return &mf, nil
		}
	}

	// Try manifest.json
	jsonPath := filepath.Join(dir, "manifest.json")
	if data, err := os.ReadFile(jsonPath); err == nil {
		var mf Manifest
		if err := json.Unmarshal(data, &mf); err == nil {
			return &mf, nil
		}
	}

	return nil, fmt.Errorf("plugin manifest not found (checked manifest.toml and manifest.json in %s)", dir)
}

// List installed plugins by reading manifest.toml or manifest.json in each plugin folder
func (m *Manager) List() ([]Manifest, error) {
	entries, err := os.ReadDir(m.pluginDir)
	if err != nil {
		return nil, err
	}

	var manifests []Manifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		mf, err := readManifest(filepath.Join(m.pluginDir, entry.Name()))
		if err == nil {
			if mf.Name == "" {
				mf.Name = entry.Name()
			}
			manifests = append(manifests, *mf)
		}
	}
	return manifests, nil
}

// GetInfo retrieves manifest for specific plugin
func (m *Manager) GetInfo(name string) (*Manifest, error) {
	mf, err := readManifest(filepath.Join(m.pluginDir, name))
	if err != nil {
		return nil, fmt.Errorf("plugin '%s' not found or missing manifest (manifest.toml or manifest.json)", name)
	}
	return mf, nil
}

// Install installs a plugin from a local directory containing manifest.toml or manifest.json
func (m *Manager) Install(sourcePath string) (*Manifest, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("source path '%s' not accessible: %w", sourcePath, err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("source must be a directory containing binary and manifest.toml or manifest.json")
	}

	mf, err := readManifest(sourcePath)
	if err != nil {
		return nil, err
	}

	if mf.Name == "" {
		return nil, fmt.Errorf("manifest requires 'name'")
	}

	destDir := filepath.Join(m.pluginDir, mf.Name)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, err
	}

	// Copy files from source to destDir
	entries, err := os.ReadDir(sourcePath)
	if err != nil {
		return nil, err
	}

	for _, e := range entries {
		src := filepath.Join(sourcePath, e.Name())
		dst := filepath.Join(destDir, e.Name())
		if err := copyFile(src, dst); err != nil {
			return nil, err
		}
	}

	return mf, nil
}

// Remove uninstalls a plugin
func (m *Manager) Remove(name string) error {
	pluginPath := filepath.Join(m.pluginDir, name)
	if _, err := os.Stat(pluginPath); os.IsNotExist(err) {
		return fmt.Errorf("plugin '%s' does not exist", name)
	}
	return os.RemoveAll(pluginPath)
}

// LoadClassifier spawns external plugin process and returns client
func (m *Manager) LoadClassifier(name string) (ClassifierService, func(), error) {
	mf, err := m.GetInfo(name)
	if err != nil {
		return nil, nil, err
	}

	binPath := filepath.Join(m.pluginDir, name, mf.Binary)
	if _, err := os.Stat(binPath); err != nil {
		// Try .exe on Windows
		if winBin := binPath + ".exe"; fileExists(winBin) {
			binPath = winBin
		} else {
			return nil, nil, fmt.Errorf("plugin binary not found at %s", binPath)
		}
	}

	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: HandshakeConfig,
		Plugins:         mergedPluginMap(),
		Cmd:             exec.Command(binPath),
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, nil, fmt.Errorf("failed to connect to plugin %s: %w", name, err)
	}

	raw, err := rpcClient.Dispense("classifier")
	if err != nil {
		client.Kill()
		return nil, nil, fmt.Errorf("failed to dispense classifier service: %w", err)
	}

	classifier, ok := raw.(ClassifierService)
	if !ok {
		client.Kill()
		return nil, nil, fmt.Errorf("invalid plugin service interface")
	}

	cleanup := func() {
		client.Kill()
	}

	return classifier, cleanup, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// LoadAllClassifiers discovers all installed plugins with "classifier" capability,
// spawns each one, collects custom rules via GetCustomRules(), and returns:
//   - services: slice of active ClassifierService instances
//   - rules: aggregated custom rules from all plugins
//   - cleanup: function to kill all spawned plugin processes
func (m *Manager) LoadAllClassifiers() ([]ClassifierService, []models.Rule, func()) {
	var services []ClassifierService
	var cleanups []func()
	var extraRules []models.Rule

	plugins, err := m.List()
	if err != nil {
		return nil, nil, func() {}
	}

	for _, p := range plugins {
		if !hasCapability(p.Capabilities, "classifier") {
			continue
		}

		svc, cleanup, err := m.LoadClassifier(p.Name)
		if err != nil {
			continue // skip broken plugins silently
		}
		services = append(services, svc)
		cleanups = append(cleanups, cleanup)

		// Collect custom rules injected by this plugin
		if rules, err := svc.GetCustomRules(); err == nil && len(rules) > 0 {
			extraRules = append(extraRules, rules...)
		}
	}

	cleanupAll := func() {
		for _, fn := range cleanups {
			fn()
		}
	}

	return services, extraRules, cleanupAll
}

// ClassifyWithPlugins tries each loaded plugin service to classify a file.
// Returns the first non-empty, non-"Other" result, or empty string if no plugin matched.
func ClassifyWithPlugins(services []ClassifierService, filename, ext string) string {
	for _, svc := range services {
		if result, err := svc.Classify(filename, ext); err == nil && result != "" && result != "Other" {
			return result
		}
	}
	return ""
}
