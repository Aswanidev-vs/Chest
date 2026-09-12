package models

import "time"

// FileSample is the bounded view of a file sent to metadata plugins for
// inspection. Head/Tail carry the first and last few KiB of file content so
// plugins can sniff signatures without reading the whole file over RPC.
type FileSample struct {
	Path      string `json:"path"`                // Absolute path on disk
	Name      string `json:"name"`                // Filename including extension
	Extension string `json:"extension"`           // Lowercase extension without dot
	Format    string `json:"format,omitempty"`    // Format detected by built-in extractors (may be empty)
	MIMEType  string `json:"mime_type,omitempty"` // MIME type detected by built-in extractors (may be empty)
	Size      int64  `json:"size"`                // Size in bytes
	Head      []byte `json:"head,omitempty"`      // First bytes of file content (bounded)
	Tail      []byte `json:"tail,omitempty"`      // Last bytes of file content (bounded)
}

// FileMetadata is the enriched metadata a metadata plugin may return for a
// sample. Every field is optional: empty strings and a zero TakenDate leave
// the built-in detection untouched. Fields merge into the file record's
// metadata map and become available to rule conditions and the index.
type FileMetadata struct {
	Format     string            `json:"format,omitempty"`      // e.g. "CR3", "PhotoshopDocument"
	MIMEType   string            `json:"mime_type,omitempty"`   // e.g. "image/x-canon-cr3"
	Category   string            `json:"category,omitempty"`    // e.g. "Image", "Video", "Document"
	TakenDate  *time.Time        `json:"taken_date,omitempty"`  // Embedded capture/creation date
	DateSource string            `json:"date_source,omitempty"` // Label describing where TakenDate came from
	Fields     map[string]string `json:"fields,omitempty"`      // Extra metadata key/value pairs
}

// HasContent reports whether any enrichment was actually produced. Empty
// results and blank-only field values let the host skip work entirely.
func (m FileMetadata) HasContent() bool {
	if m.Format != "" || m.MIMEType != "" || m.Category != "" ||
		(m.TakenDate != nil && !m.TakenDate.IsZero()) {
		return true
	}
	for _, v := range m.Fields {
		if v != "" {
			return true
		}
	}
	return false
}
