package cli

import (
	"sync/atomic"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/plugin"
)

// metadataEnricher bundles the plugin-backed enrichment function used by
// sort and watch with an atomic enriched-file counter and a cleanup that
// terminates all spawned plugin processes.
type metadataEnricher struct {
	enriched func() int64
	enrich   func(file models.File) (models.File, bool)
	cleanup  func()
	services int
}

// loadMetadataEnricher spawns every installed metadata-capable plugin and
// returns an enricher suitable for scanner.ScanOptions.EnrichFunc and
// watcher.WatchOptions.EnrichFunc. Enrichment is best-effort: when no
// plugins are installed (or the plugin directory is unavailable) the
// enricher is a no-op and its cleanup is still safe to call.
func loadMetadataEnricher() *metadataEnricher {
	var counter int64
	e := &metadataEnricher{
		enriched: func() int64 { return atomic.LoadInt64(&counter) },
		enrich:   func(file models.File) (models.File, bool) { return file, false },
		cleanup:  func() {},
	}

	mgr, err := plugin.NewManager()
	if err != nil {
		return e
	}
	services, cleanup := mgr.LoadAllMetadata()
	e.services = len(services)
	e.cleanup = cleanup
	if len(services) == 0 {
		return e
	}

	e.enrich = func(file models.File) (models.File, bool) {
		meta := plugin.EnrichWithPlugins(services, plugin.SampleFromFile(file))
		if !meta.HasContent() {
			return file, false
		}
		updated := file
		if meta.Format != "" {
			updated.Format = meta.Format
		}
		if meta.MIMEType != "" {
			updated.MIMEType = meta.MIMEType
		}
		if meta.Category != "" {
			updated.Category = meta.Category
		}
		if meta.TakenDate != nil {
			updated.TakenDate = meta.TakenDate
			if meta.DateSource != "" {
				updated.TakenDateSource = meta.DateSource
			}
		}
		if len(meta.Fields) > 0 {
			if updated.Metadata == nil {
				updated.Metadata = make(map[string]string, len(meta.Fields))
			}
			for k, v := range meta.Fields {
				updated.Metadata[k] = v
			}
		}
		atomic.AddInt64(&counter, 1)
		return updated, true
	}
	return e
}
