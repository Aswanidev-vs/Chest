package classifier

import (
	"net/http"
	"os"
	"strings"
)

// SniffMIME reads up to 512 bytes of a file to detect its actual MIME content type
func SniffMIME(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return ""
	}

	contentType := http.DetectContentType(buf[:n])
	// Strip extra params like "; charset=utf-8"
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = strings.TrimSpace(contentType[:idx])
	}

	return contentType
}

// CategoryFromMIME maps common MIME types into CHEST categories
func CategoryFromMIME(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "Images"
	case strings.HasPrefix(mime, "video/"):
		return "Videos"
	case strings.HasPrefix(mime, "audio/"):
		return "Audio"
	case strings.HasPrefix(mime, "text/"), mime == "application/pdf", mime == "application/json":
		return "Documents"
	case mime == "application/zip", mime == "application/x-tar", mime == "application/gzip":
		return "Archives"
	default:
		return "Other"
	}
}
