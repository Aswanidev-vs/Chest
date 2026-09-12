package metadata

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxMetadataHead = 8 << 20
	maxMetadataTail = 1 << 20
)

// DateMetadata is embedded media date information read from a file.
type DateMetadata struct {
	Date   time.Time
	Source string
}

// Result contains format detection and the metadata extracted from a file.
type Result struct {
	Format   string            `json:"format"`
	MIMEType string            `json:"mime_type,omitempty"`
	Category string            `json:"category,omitempty"`
	Date     DateMetadata      `json:"date,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
}

// Sample is the bounded file view supplied to metadata extractors.
type Sample struct {
	Path      string
	Extension string
	Head      []byte
	Tail      []byte
	Size      int64
}

// MatchFunc reports whether an extractor can handle a sample.
type MatchFunc func(Sample) bool

// ExtractFunc extracts metadata from a sample.
type ExtractFunc func(Sample) (Result, error)

// Extractor is the in-process extension point for built-in and custom formats.
type Extractor interface {
	Name() string
	Match(Sample) bool
	Extract(Sample) (Result, error)
}

// FuncExtractor adapts functions to Extractor.
type FuncExtractor struct {
	name    string
	match   MatchFunc
	extract ExtractFunc
}

// NewFuncExtractor creates an extractor from match and extraction functions.
func NewFuncExtractor(name string, match MatchFunc, extract ExtractFunc) Extractor {
	return &FuncExtractor{name: name, match: match, extract: extract}
}

// Name returns the extractor name.
func (e *FuncExtractor) Name() string {
	if e == nil {
		return ""
	}
	return e.name
}

// Match reports whether the extractor handles the sample.
func (e *FuncExtractor) Match(sample Sample) bool {
	return e != nil && e.match != nil && e.match(sample)
}

// Extract runs the extractor function.
func (e *FuncExtractor) Extract(sample Sample) (Result, error) {
	if e == nil || e.extract == nil {
		return Result{}, fmt.Errorf("extractor %q has no extraction function", e.Name())
	}
	return e.extract(sample)
}

var (
	extractorsMu sync.RWMutex
	extractors   []Extractor
)

func init() {
	registerBuiltinExtractors()
}

// Register adds a custom extractor ahead of built-ins so domain formats can
// override signature or extension detection.
func Register(extractor Extractor) {
	if extractor == nil || extractor.Name() == "" {
		return
	}
	extractorsMu.Lock()
	extractors = append([]Extractor{extractor}, extractors...)
	extractorsMu.Unlock()
}

// RegisterCustom registers a named custom extractor from functions.
func RegisterCustom(name string, match MatchFunc, extract ExtractFunc) {
	Register(NewFuncExtractor(name, match, extract))
}

// Unregister removes extractors with the supplied name.
func Unregister(name string) {
	if name == "" {
		return
	}
	extractorsMu.Lock()
	defer extractorsMu.Unlock()
	kept := make([]Extractor, 0, len(extractors))
	for _, extractor := range extractors {
		if extractor.Name() != name {
			kept = append(kept, extractor)
		}
	}
	extractors = kept
}

// Extract detects a file's format and extracts metadata using the first
// matching built-in or custom extractor.
func Extract(path, ext string) (Result, error) {
	sample, err := readSample(path)
	if err != nil {
		return Result{}, err
	}
	sample.Extension = normalizeExtension(ext)
	if sample.Extension == "" {
		sample.Extension = normalizeExtension(filepath.Ext(path))
	}

	extractorsMu.RLock()
	registered := append([]Extractor(nil), extractors...)
	extractorsMu.RUnlock()

	for _, extractor := range registered {
		if !extractor.Match(sample) {
			continue
		}
		result, err := extractor.Extract(sample)
		if err != nil {
			return Result{}, err
		}
		return finalizeResult(result, sample), nil
	}

	return finalizeResult(Result{}, sample), nil
}

// Read returns embedded taken/creation date metadata for supported files.
func Read(path, ext string) (DateMetadata, bool, error) {
	result, err := Extract(path, ext)
	if err != nil {
		return DateMetadata{}, false, err
	}
	if result.Date.Date.IsZero() {
		return DateMetadata{}, false, nil
	}
	return result.Date, true, nil
}

func readSample(path string) (Sample, error) {
	file, err := os.Open(path)
	if err != nil {
		return Sample{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Sample{}, err
	}
	size := info.Size()
	if size < 0 {
		return Sample{}, fmt.Errorf("invalid file size for %q", path)
	}

	headSize := size
	if headSize > maxMetadataHead {
		headSize = maxMetadataHead
	}
	head := make([]byte, int(headSize))
	n, err := io.ReadFull(file, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return Sample{}, err
	}
	head = head[:n]

	var tail []byte
	if size > int64(len(head)) {
		tailSize := size
		if tailSize > maxMetadataTail {
			tailSize = maxMetadataTail
		}
		tail = make([]byte, int(tailSize))
		if _, err := file.ReadAt(tail, size-tailSize); err != nil {
			return Sample{}, err
		}
	}

	return Sample{Path: path, Head: head, Tail: tail, Size: size}, nil
}

func finalizeResult(result Result, sample Sample) Result {
	detected := detectFormat(sample)
	if result.Format == "" {
		result.Format = detected.Format
	}
	if result.MIMEType == "" {
		result.MIMEType = detected.MIMEType
	}
	if result.Category == "" {
		result.Category = detected.Category
	}
	if result.Fields == nil && detected.Fields != nil {
		result.Fields = detected.Fields
	}
	if result.Format == "" {
		result.Format = "Unknown"
	}
	return result
}

func setDate(result *Result, date time.Time, source string) {
	if date.IsZero() || !result.Date.Date.IsZero() {
		return
	}
	result.Date = DateMetadata{Date: date, Source: source}
}

func addField(result *Result, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if result.Fields == nil {
		result.Fields = make(map[string]string)
	}
	result.Fields[key] = value
}

func normalizeExtension(ext string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
}

func sampleBytes(sample Sample) []byte {
	if sample.Size <= int64(len(sample.Head)+len(sample.Tail)) {
		data := make([]byte, 0, len(sample.Head)+len(sample.Tail))
		data = append(data, sample.Head...)
		data = append(data, sample.Tail...)
		return data
	}
	return sample.Head
}

func hasPrefix(data []byte, prefix ...byte) bool {
	if len(data) < len(prefix) {
		return false
	}
	for i, b := range prefix {
		if data[i] != b {
			return false
		}
	}
	return true
}

func indexOf(data []byte, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i+len(needle) <= len(data); i++ {
		if string(data[i:i+len(needle)]) == string(needle) {
			return i
		}
	}
	return -1
}
