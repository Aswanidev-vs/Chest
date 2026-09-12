package plugin

import (
	"fmt"
	"io"
	"net/rpc"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Aswanidev-vs/chest/internal/models"
	hplugin "github.com/hashicorp/go-plugin"
)

// MetadataCapability is the manifest capability string that enables the
// metadata protocol. Plugins declaring it must serve the "metadata" plugin
// from their Serve config.
const MetadataCapability = "metadata"

// MetadataPluginMap is the plugin-map entry hosts and plugins merge into
// their Serve config to expose the metadata protocol alongside the
// classifier protocol.
var MetadataPluginMap = map[string]hplugin.Plugin{
	"metadata": &MetadataPluginRPC{},
}

// MetadataService is the interface exposed by plugins with the "metadata"
// capability. GetInfo mirrors ClassifierService.GetInfo so hosts can verify
// identity over RPC; Inspect enriches a bounded file sample with format,
// MIME, category, embedded dates and arbitrary metadata fields.
type MetadataService interface {
	GetInfo() (Manifest, error)
	Inspect(sample models.FileSample) (models.FileMetadata, error)
}

// MetadataRPCClient is the host-side RPC implementation of MetadataService.
type MetadataRPCClient struct {
	client *rpc.Client
}

func (c *MetadataRPCClient) GetInfo() (Manifest, error) {
	var resp Manifest
	err := c.client.Call("Plugin.GetInfo", new(interface{}), &resp)
	return resp, err
}

func (c *MetadataRPCClient) Inspect(sample models.FileSample) (models.FileMetadata, error) {
	var resp models.FileMetadata
	err := c.client.Call("Plugin.Inspect", sample, &resp)
	return resp, err
}

// MetadataRPCServer is the plugin-side RPC server wrapping a MetadataService.
type MetadataRPCServer struct {
	Impl MetadataService
}

func (s *MetadataRPCServer) GetInfo(args interface{}, resp *Manifest) error {
	m, err := s.Impl.GetInfo()
	if err != nil {
		return err
	}
	*resp = m
	return nil
}

func (s *MetadataRPCServer) Inspect(sample models.FileSample, resp *models.FileMetadata) error {
	meta, err := s.Impl.Inspect(sample)
	if err != nil {
		return err
	}
	*resp = meta
	return nil
}

// MetadataPluginRPC adapts MetadataService to the go-plugin machinery.
type MetadataPluginRPC struct {
	Impl MetadataService
}

func (p *MetadataPluginRPC) Server(*hplugin.MuxBroker) (interface{}, error) {
	return &MetadataRPCServer{Impl: p.Impl}, nil
}

func (p *MetadataPluginRPC) Client(b *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &MetadataRPCClient{client: c}, nil
}

// LoadAllMetadata spawns every installed plugin declaring the "metadata"
// capability and returns ready-to-use services plus a single cleanup that
// kills all spawned plugin processes. Broken plugins are skipped silently so
// one bad install never blocks a sort or search run.
func (m *Manager) LoadAllMetadata() ([]MetadataService, func()) {
	var services []MetadataService
	var cleanups []func()

	plugins, err := m.List()
	if err != nil {
		return nil, func() {}
	}

	for _, p := range plugins {
		if !hasCapability(p.Capabilities, MetadataCapability) {
			continue
		}
		svc, cleanup, err := m.LoadMetadata(p.Name)
		if err != nil {
			continue // skip broken plugins silently
		}
		services = append(services, svc)
		cleanups = append(cleanups, cleanup)
	}

	cleanupAll := func() {
		for _, fn := range cleanups {
			fn()
		}
	}
	return services, cleanupAll
}

// LoadMetadata spawns a single metadata plugin process and returns its
// client plus a cleanup function that terminates the process.
func (m *Manager) LoadMetadata(name string) (MetadataService, func(), error) {
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
		return nil, nil, fmt.Errorf("failed to connect to metadata plugin %s: %w", name, err)
	}

	raw, err := rpcClient.Dispense("metadata")
	if err != nil {
		client.Kill()
		return nil, nil, fmt.Errorf("failed to dispense metadata service: %w", err)
	}

	svc, ok := raw.(MetadataService)
	if !ok {
		client.Kill()
		return nil, nil, fmt.Errorf("invalid metadata plugin service interface")
	}

	cleanup := func() {
		client.Kill()
	}
	return svc, cleanup, nil
}

// EnrichWithPlugins runs a file sample through each loaded metadata plugin
// in order, merging the first non-empty value produced for every field.
// Fields set by an earlier plugin win; later plugins only fill gaps. The
// returned models.FileMetadata reports only values that came from plugins,
// so callers can distinguish plugin output from built-in detection. Plugin
// errors are ignored — enrichment is best-effort by design.
func EnrichWithPlugins(services []MetadataService, sample models.FileSample) models.FileMetadata {
	var merged models.FileMetadata
	fields := make(map[string]string)

	for _, svc := range services {
		meta, err := svc.Inspect(sample)
		if err != nil {
			continue
		}
		if merged.Format == "" {
			merged.Format = meta.Format
		}
		if merged.MIMEType == "" {
			merged.MIMEType = meta.MIMEType
		}
		if merged.Category == "" {
			merged.Category = meta.Category
		}
		if merged.TakenDate == nil {
			merged.TakenDate = meta.TakenDate
		}
		if merged.DateSource == "" {
			merged.DateSource = meta.DateSource
		}
		for k, v := range meta.Fields {
			if v == "" {
				continue
			}
			if _, exists := fields[k]; !exists {
				fields[k] = v
			}
		}
	}

	if len(fields) > 0 {
		merged.Fields = fields
	}
	return merged
}

// hasCapability reports whether the manifest declares the given capability.
func hasCapability(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

const (
	// pluginSampleHeadBytes and pluginSampleTailBytes bound how much file
	// content is read for metadata samples. They are small enough to keep
	// RPC payloads cheap while still exposing container headers and
	// end-of-file atoms/indexes to plugins.
	pluginSampleHeadBytes = 4 << 10 // 4 KiB
	pluginSampleTailBytes = 1 << 10 // 1 KiB
)

// SampleFromFile builds a bounded models.FileSample for a scanned file,
// reading the first pluginSampleHeadBytes and last pluginSampleTailBytes of
// file content so plugins can sniff signatures. Read errors are non-fatal:
// the sample is returned without content bytes if the file cannot be read.
func SampleFromFile(file models.File) models.FileSample {
	sample := models.FileSample{
		Path:      file.Path,
		Name:      file.Name,
		Extension: file.Extension,
		Format:    file.Format,
		MIMEType:  file.MIMEType,
		Size:      file.Size,
	}
	if file.Size <= 0 {
		return sample
	}

	f, err := os.Open(file.Path)
	if err != nil {
		return sample
	}
	defer f.Close()

	head := make([]byte, min64(pluginSampleHeadBytes, file.Size))
	if n, _ := io.ReadFull(f, head); n > 0 {
		sample.Head = head[:n]
	}

	if file.Size > pluginSampleHeadBytes {
		tailOffset := file.Size - min64(pluginSampleTailBytes, file.Size)
		if _, err := f.Seek(tailOffset, io.SeekStart); err == nil {
			tail := make([]byte, pluginSampleTailBytes)
			if n, _ := f.Read(tail); n > 0 {
				sample.Tail = tail[:n]
			}
		}
	}
	return sample
}

// min64 returns the smaller of two int64 values.
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
