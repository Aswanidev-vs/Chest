package main

// rich-meta is an example CHEST plugin demonstrating the "metadata"
// capability introduced with protocol version 2.
//
// It inspects bounded samples of scanned files and can contribute:
//   - Format detection for camera RAW containers the built-in signature
//     extractor does not know (CR3, RAF, ORF, RW2, X3F).
//   - Embedded capture dates parsed from XMP sidecars
//     (photoshop:DateCreated), so photos sort by the day they were taken.
//   - Arbitrary metadata fields merged into the file record.
//
// Build & install:
//
//	go build -o rich-meta.exe ./examples/plugins/rich-meta
//	# place the binary + manifest.json in ~/.chest/plugins/rich-meta/
//
// `chest sort --verbose` then reports:
//
//	[PLUGIN] Enriched N file(s) via 1 metadata plugin(s)

import (
	"bytes"
	"encoding/binary"
	"strings"
	"time"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/plugin"
	hplugin "github.com/hashicorp/go-plugin"
)

// RichMetaPlugin implements the MetadataService interface.
type RichMetaPlugin struct{}

// GetInfo returns plugin metadata and declared capabilities.
func (p *RichMetaPlugin) GetInfo() (plugin.Manifest, error) {
	return plugin.Manifest{
		Name:         "rich-meta",
		Version:      "1.0.0",
		Description:  "Camera RAW format sniffing and XMP capture-date extraction",
		Author:       "CHEST Community",
		Capabilities: []string{plugin.MetadataCapability},
		Binary:       "rich-meta",
	}, nil
}

// Inspect enriches a bounded file sample. Returning an empty
// models.FileMetadata signals that this plugin has nothing to add and CHEST
// falls back to its built-in detection.
func (p *RichMetaPlugin) Inspect(sample models.FileSample) (models.FileMetadata, error) {
	if strings.EqualFold(sample.Extension, "xmp") {
		return p.inspectXMP(sample), nil
	}
	return p.inspectRaw(sample), nil
}

// inspectRaw sniffs camera RAW container signatures in the sample head.
func (p *RichMetaPlugin) inspectRaw(sample models.FileSample) models.FileMetadata {
	head := sample.Head
	if len(head) < 12 {
		return models.FileMetadata{}
	}

	format := detectRawFormat(head)
	if format == "" {
		return models.FileMetadata{}
	}

	meta := models.FileMetadata{
		Format:   format,
		MIMEType: "image/x-" + strings.ToLower(format),
		Category: "Image",
		Fields: map[string]string{
			"raw_container": rawContainer(format),
		},
	}
	return meta
}

// detectRawFormat matches well-known RAW magics against the sample head.
func detectRawFormat(head []byte) string {
	switch {
	// Canon CR3: ISO Base Media File Format box with ftyp/crx brand.
	case len(head) >= 12 && string(head[4:8]) == "ftyp" && bytes.HasPrefix(head[8:], []byte("crx")):
		return "CR3"
	// Fujifilm RAF: fixed ASCII header.
	case len(head) >= 16 && bytes.HasPrefix(head, []byte("FUJIFILMCCD-RAW ")):
		return "RAF"
	// Olympus ORF: TIFF-style byte order marks with "OR" version word.
	case len(head) >= 8 && (bytes.HasPrefix(head, []byte("IIRO")) || bytes.HasPrefix(head, []byte("MMOR"))):
		return "ORF"
	// Panasonic RW2: "IIU\0" with a 0x55 version word.
	case len(head) >= 8 && bytes.HasPrefix(head, []byte("IIU\x00")) && binary.LittleEndian.Uint16(head[4:6]) == 0x0055:
		return "RW2"
	// Foveon X3F: "FOVb" magic.
	case bytes.HasPrefix(head, []byte("FOVb")):
		return "X3F"
	default:
		return ""
	}
}

// rawContainer names the underlying container family for a RAW format.
func rawContainer(format string) string {
	switch format {
	case "CR3":
		return "ISO-BMFF/CRX"
	case "RAF":
		return "FUJIFILM-CCD"
	case "ORF", "RW2":
		return "TIFF"
	case "X3F":
		return "FOVb"
	default:
		return ""
	}
}

// inspectXMP extracts the capture date from an XMP sidecar's
// photoshop:DateCreated property so date-based sorting uses the moment the
// photo was taken rather than the sidecar's write time.
func (p *RichMetaPlugin) inspectXMP(sample models.FileSample) models.FileMetadata {
	raw := extractXMPValue(sample.Head, "photoshop", "DateCreated")
	if raw == "" {
		return models.FileMetadata{}
	}

	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return models.FileMetadata{
				TakenDate:  &ts,
				DateSource: "xmp:photoshop:DateCreated",
				Fields: map[string]string{
					"xmp_date_created": raw,
				},
			}
		}
	}
	return models.FileMetadata{}
}

// extractXMPValue pulls a property value from XMP data in both common
// encodings: the attribute form (photoshop:DateCreated="2021-11-17") and the
// element form (<photoshop:DateCreated>2021-11-17</photoshop:DateCreated>).
func extractXMPValue(data []byte, namespace, property string) string {
	attr := []byte(namespace + ":" + property + "=\"")
	if idx := bytes.Index(data, attr); idx >= 0 {
		rest := data[idx+len(attr):]
		if end := bytes.IndexByte(rest, '"'); end > 0 {
			return string(rest[:end])
		}
	}

	open := []byte("<" + namespace + ":" + property + ">")
	close_ := []byte("</" + namespace + ":" + property + ">")
	if start := bytes.Index(data, open); start >= 0 {
		rest := data[start+len(open):]
		if end := bytes.Index(rest, close_); end > 0 {
			return strings.TrimSpace(string(rest[:end]))
		}
	}
	return ""
}

func main() {
	hplugin.Serve(&hplugin.ServeConfig{
		HandshakeConfig: plugin.HandshakeConfig,
		Plugins: map[string]hplugin.Plugin{
			"metadata": &plugin.MetadataPluginRPC{
				Impl: &RichMetaPlugin{},
			},
		},
	})
}
