package metadata

import (
	"path/filepath"
	"strings"
)

func registerBuiltinExtractors() {
	extractorsMu.Lock()
	defer extractorsMu.Unlock()
	extractors = append(extractors,
		NewFuncExtractor("gocut", gocutMatch, extractGoCut),
		NewFuncExtractor("media", mediaMatch, extractMedia),
		NewFuncExtractor("pdf", pdfMatch, extractPDF),
		NewFuncExtractor("archive", archiveMatch, extractArchive),
		NewFuncExtractor("audio", audioMatch, extractAudio),
	)
}

func detectFormat(sample Sample) Result {
	format, mime, category := formatFromMagic(sample.Head)
	if format == "" {
		format, mime, category = formatFromExtension(sample.Extension)
	}
	if format == "" {
		format, mime, category = formatFromExtension(normalizeExtension(filepath.Ext(sample.Path)))
	}
	if format == "" && looksLikeText(sample.Head) {
		format, mime, category = "Text", "text/plain", "Document"
	}
	if format == "" {
		format, mime, category = "Unknown", "application/octet-stream", "Other"
	}
	return Result{Format: format, MIMEType: mime, Category: category}
}

func formatFromMagic(data []byte) (string, string, string) {
	switch {
	case hasPrefix(data, 0xff, 0xd8, 0xff):
		return "JPEG", "image/jpeg", "Image"
	case hasPrefix(data, 0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a):
		return "PNG", "image/png", "Image"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "GIF", "image/gif", "Image"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "WebP", "image/webp", "Image"
	case isTIFF(data):
		return "TIFF", "image/tiff", "Image"
	case hasPrefix(data, 'B', 'M'):
		return "BMP", "image/bmp", "Image"
	case len(data) >= 4 && string(data[:4]) == "\x00\x00\x01\x00":
		return "ICO", "image/x-icon", "Image"
	case len(data) >= 5 && string(data[:5]) == "%PDF-":
		return "PDF", "application/pdf", "Document"
	case isZIP(data):
		return "ZIP", "application/zip", "Archive"
	case hasPrefix(data, 0x1f, 0x8b):
		return "GZIP", "application/gzip", "Archive"
	case isTAR(data):
		return "TAR", "application/x-tar", "Archive"
	case hasPrefix(data, 0x37, 0x7a, 0xbc, 0xaf, 0x27, 0x1c):
		return "7z", "application/x-7z-compressed", "Archive"
	case len(data) >= 7 && string(data[:7]) == "Rar!\x1a\x07\x00":
		return "RAR", "application/vnd.rar", "Archive"
	case len(data) >= 32774 && string(data[32769:32774]) == "CD001":
		return "ISO", "application/x-iso9660-image", "Archive"
	case isRIFF(data, "WAVE"):
		return "WAV", "audio/wav", "Audio"
	case isRIFF(data, "AVI "):
		return "AVI", "video/x-msvideo", "Video"
	case isMP3(data):
		return "MP3", "audio/mpeg", "Audio"
	case hasPrefix(data, 'f', 'L', 'a', 'C'):
		return "FLAC", "audio/flac", "Audio"
	case len(data) >= 4 && string(data[:4]) == "OggS":
		return "OGG", "application/ogg", "Audio"
	case isQuickTime(data):
		return "QuickTime", "video/quicktime", "Video"
	case isMPEG(data):
		return "MPEG", "video/mpeg", "Video"
	case hasPrefix(data, 0x7f, 'E', 'L', 'F'):
		return "ELF", "application/x-elf", "Executable"
	case hasPrefix(data, 'M', 'Z'):
		return "PE", "application/vnd.microsoft.portable-executable", "Executable"
	case isMachO(data):
		return "Mach-O", "application/x-mach-binary", "Executable"
	}
	return "", "", ""
}

func formatFromExtension(ext string) (string, string, string) {
	ext = normalizeExtension(ext)
	switch ext {
	case "jpg", "jpeg", "jpe":
		return "JPEG", "image/jpeg", "Image"
	case "png":
		return "PNG", "image/png", "Image"
	case "gif":
		return "GIF", "image/gif", "Image"
	case "webp":
		return "WebP", "image/webp", "Image"
	case "tif", "tiff":
		return "TIFF", "image/tiff", "Image"
	case "bmp":
		return "BMP", "image/bmp", "Image"
	case "ico", "cur":
		return "ICO", "image/x-icon", "Image"
	case "heic", "heif", "avif":
		return "HEIF", "image/heif", "Image"
	case "pdf":
		return "PDF", "application/pdf", "Document"
	case "zip":
		return "ZIP", "application/zip", "Archive"
	case "gz":
		return "GZIP", "application/gzip", "Archive"
	case "tar":
		return "TAR", "application/x-tar", "Archive"
	case "7z":
		return "7z", "application/x-7z-compressed", "Archive"
	case "rar":
		return "RAR", "application/vnd.rar", "Archive"
	case "iso":
		return "ISO", "application/x-iso9660-image", "Archive"
	case "wav", "wave":
		return "WAV", "audio/wav", "Audio"
	case "avi":
		return "AVI", "video/x-msvideo", "Video"
	case "mp3":
		return "MP3", "audio/mpeg", "Audio"
	case "flac":
		return "FLAC", "audio/flac", "Audio"
	case "ogg", "oga":
		return "OGG", "application/ogg", "Audio"
	case "opus":
		return "Opus", "audio/ogg", "Audio"
	case "mp4", "m4v", "mov", "m4a", "3gp", "3g2":
		return "QuickTime", "video/quicktime", "Video"
	case "mpg", "mpeg", "mpe":
		return "MPEG", "video/mpeg", "Video"
	case "mkv", "webm":
		return "Matroska", "video/x-matroska", "Video"
	case "exe", "dll", "sys":
		return "PE", "application/vnd.microsoft.portable-executable", "Executable"
	case "elf":
		return "ELF", "application/x-elf", "Executable"
	case "app":
		return "Mach-O", "application/x-mach-binary", "Executable"
	case "txt", "text", "log":
		return "Text", "text/plain", "Document"
	case "md", "markdown":
		return "Markdown", "text/markdown", "Document"
	case "json":
		return "JSON", "application/json", "Data"
	case "xml":
		return "XML", "application/xml", "Data"
	case "csv", "tsv":
		return "CSV", "text/csv", "Data"
	case "html", "htm":
		return "HTML", "text/html", "Document"
	case "go":
		return "Go", "text/x-go", "Code"
	case "py":
		return "Python", "text/x-python", "Code"
	case "js", "mjs", "cjs":
		return "JavaScript", "text/javascript", "Code"
	case "ts", "tsx":
		return "TypeScript", "text/typescript", "Code"
	case "java":
		return "Java", "text/x-java-source", "Code"
	case "c", "h", "cc", "cpp", "cxx":
		return "C/C++", "text/x-c", "Code"
	case "rs":
		return "Rust", "text/x-rust", "Code"
	case "cs":
		return "C#", "text/x-csharp", "Code"
	case "php":
		return "PHP", "text/x-php", "Code"
	case "rb":
		return "Ruby", "text/x-ruby", "Code"
	case "sh", "bash":
		return "Shell", "text/x-sh", "Code"
	case "yaml", "yml":
		return "YAML", "application/yaml", "Data"
	case "toml":
		return "TOML", "application/toml", "Data"
	case "doc", "docx":
		return "Word", "application/msword", "Document"
	case "xls", "xlsx":
		return "Excel", "application/vnd.ms-excel", "Document"
	case "ppt", "pptx":
		return "PowerPoint", "application/vnd.ms-powerpoint", "Document"
	case "odt":
		return "ODF", "application/vnd.oasis.opendocument.text", "Document"
	case "ods":
		return "ODF", "application/vnd.oasis.opendocument.spreadsheet", "Document"
	case "odp":
		return "ODF", "application/vnd.oasis.opendocument.presentation", "Document"
	case "epub":
		return "EPUB", "application/epub+zip", "Document"
	case "gocut":
		return "GoCut", "application/x-gocut-project", "Video"
	}
	return "", "", ""
}

func looksLikeText(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	printable := 0
	for _, b := range data {
		if b == 0 {
			return false
		}
		if b == '\t' || b == '\n' || b == '\r' || (b >= 0x20 && b <= 0x7e) || b >= 0xa0 {
			printable++
		}
	}
	return printable*10 >= len(data)*8
}

func isTIFF(data []byte) bool {
	return len(data) >= 8 && ((data[0] == 'I' && data[1] == 'I' && data[2] == 42 && data[3] == 0) || (data[0] == 'M' && data[1] == 0 && data[2] == 0 && data[3] == 42))
}

func isZIP(data []byte) bool {
	return len(data) >= 4 && (string(data[:4]) == "PK\x03\x04" || string(data[:4]) == "PK\x05\x06" || string(data[:4]) == "PK\x07\x08")
}

func isTAR(data []byte) bool {
	return len(data) >= 262 && string(data[257:262]) == "ustar"
}

func isRIFF(data []byte, kind string) bool {
	return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == kind
}

func isMP3(data []byte) bool {
	if len(data) >= 3 && string(data[:3]) == "ID3" {
		return true
	}
	return len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0
}

func isQuickTime(data []byte) bool {
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return false
	}
	brand := strings.ToLower(string(data[8:12]))
	switch brand {
	case "qt  ", "mp41", "mp42", "isom", "iso2", "avc1", "msnv", "mmp4", "dash":
		return true
	}
	for i := 12; i+4 <= len(data); i += 4 {
		if string(data[i:i+4]) == "qt  " || string(data[i:i+4]) == "mp41" || string(data[i:i+4]) == "mp42" || string(data[i:i+4]) == "isom" || string(data[i:i+4]) == "iso2" {
			return true
		}
	}
	return false
}

func isMPEG(data []byte) bool {
	return len(data) >= 4 && data[0] == 0 && data[1] == 0x1 && data[2] >= 0xb0 && data[2] <= 0xbf
}

func isMachO(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	switch string(data[:4]) {
	case "\xfe\xed\xfa\xce", "\xce\xfa\xed\xfe", "\xfe\xed\xfa\xcf", "\xcf\xfa\xed\xfe", "\xca\xfe\xba\xbe", "\xbe\xba\xfe\xca":
		return true
	}
	return false
}
