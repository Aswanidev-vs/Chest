package classifier

import (
	"path/filepath"
	"strings"
)

// Categories
const (
	TypeImage      = "Image"
	TypeVideo      = "Video"
	TypeAudio      = "Audio"
	TypeDocument   = "Document"
	TypeArchive    = "Archive"
	TypeExecutable = "Executable"
	TypeCode       = "Code"
	TypeOther      = "Other"
)

var extensionMap = map[string]string{
	// Images
	"jpg": TypeImage, "jpeg": TypeImage, "png": TypeImage, "gif": TypeImage,
	"webp": TypeImage, "svg": TypeImage, "bmp": TypeImage, "tiff": TypeImage,
	"ico": TypeImage, "psd": TypeImage, "raw": TypeImage, "heic": TypeImage,

	// Videos
	"mp4": TypeVideo, "mkv": TypeVideo, "avi": TypeVideo, "mov": TypeVideo,
	"wmv": TypeVideo, "flv": TypeVideo, "webm": TypeVideo, "m4v": TypeVideo,
	"3gp": TypeVideo,

	// Audio
	"mp3": TypeAudio, "wav": TypeAudio, "flac": TypeAudio, "aac": TypeAudio,
	"ogg": TypeAudio, "wma": TypeAudio, "m4a": TypeAudio, "opus": TypeAudio,
	"mid": TypeAudio, "midi": TypeAudio,

	// Documents
	"pdf": TypeDocument, "doc": TypeDocument, "docx": TypeDocument,
	"txt": TypeDocument, "rtf": TypeDocument, "odt": TypeDocument,
	"xls": TypeDocument, "xlsx": TypeDocument, "ods": TypeDocument,
	"ppt": TypeDocument, "pptx": TypeDocument, "odp": TypeDocument,
	"csv": TypeDocument, "md": TypeDocument, "epub": TypeDocument,

	// Archives
	"zip": TypeArchive, "rar": TypeArchive, "7z": TypeArchive, "tar": TypeArchive,
	"gz": TypeArchive, "bz2": TypeArchive, "xz": TypeArchive, "tgz": TypeArchive,
	"iso": TypeArchive,

	// Executables
	"exe": TypeExecutable, "msi": TypeExecutable, "dmg": TypeExecutable,
	"pkg": TypeExecutable, "deb": TypeExecutable, "rpm": TypeExecutable,
	"apk": TypeExecutable, "bin": TypeExecutable, "run": TypeExecutable,

	// Code
	"go": TypeCode, "py": TypeCode, "js": TypeCode, "ts": TypeCode,
	"jsx": TypeCode, "tsx": TypeCode, "html": TypeCode, "css": TypeCode,
	"scss": TypeCode, "json": TypeCode, "xml": TypeCode, "yaml": TypeCode,
	"yml": TypeCode, "toml": TypeCode, "c": TypeCode, "cpp": TypeCode,
	"h": TypeCode, "hpp": TypeCode, "java": TypeCode, "kt": TypeCode,
	"rs": TypeCode, "php": TypeCode, "rb": TypeCode, "sh": TypeCode,
	"bat": TypeCode, "ps1": TypeCode, "sql": TypeCode,
}

// ClassifyExtension returns high-level Category name for extension (e.g. "mp4" -> "Video")
func ClassifyExtension(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if cat, ok := extensionMap[ext]; ok {
		return cat
	}
	return TypeOther
}

// ClassifyFilename returns high-level Category name for filename
func ClassifyFilename(filename string) string {
	ext := filepath.Ext(filename)
	return ClassifyExtension(ext)
}
