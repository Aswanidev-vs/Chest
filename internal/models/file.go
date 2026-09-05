package models

import "time"

// File represents a file inspected by CHEST.
type File struct {
	Path      string    `json:"path"`      // Absolute or full path
	RelPath   string    `json:"rel_path"`  // Relative path from chest root
	Name      string    `json:"name"`      // Filename including extension
	Extension string    `json:"extension"` // Lowercase extension without dot (e.g. "mp4", "png")
	Size      int64     `json:"size"`      // Size in bytes
	ModTime   time.Time `json:"mod_time"`  // Last modification time
	IsDir     bool      `json:"is_dir"`    // Is directory
	IsHidden  bool      `json:"is_hidden"` // Is hidden or system file
	MIMEType  string    `json:"mime_type"` // MIME type (if available)
	Category  string    `json:"category"`  // High-level category (Image, Video, etc.)
}
