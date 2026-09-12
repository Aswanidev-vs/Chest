package plugin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/models"
)

// fakeMetadataService is an in-process MetadataService used to exercise
// EnrichWithPlugins without spawning plugin processes.
type fakeMetadataService struct {
	result  models.FileMetadata
	err     error
	seenReq models.FileSample
	calls   int
}

func (f *fakeMetadataService) GetInfo() (Manifest, error) {
	return Manifest{Name: "fake"}, nil
}

func (f *fakeMetadataService) Inspect(sample models.FileSample) (models.FileMetadata, error) {
	f.seenReq = sample
	f.calls++
	if f.err != nil {
		return models.FileMetadata{}, f.err
	}
	return f.result, nil
}

func TestEnrichWithPluginsMergesFirstWins(t *testing.T) {
	taken := time.Date(2024, 5, 1, 10, 30, 0, 0, time.UTC)
	first := &fakeMetadataService{result: models.FileMetadata{
		Format:     "CR3",
		Category:   "Image",
		TakenDate:  &taken,
		DateSource: "xmp:test",
		Fields:     map[string]string{"lens": "RF 50mm"},
	}}
	second := &fakeMetadataService{result: models.FileMetadata{
		Format:   "RAF",         // ignored: first plugin wins
		MIMEType: "image/x-raf", // fills the gap
		Fields:   map[string]string{"iso": "400"},
	}}

	merged := EnrichWithPlugins([]MetadataService{first, second}, models.FileSample{Name: "IMG_0001.CRW"})

	if merged.Format != "CR3" {
		t.Errorf("Format: want CR3 (first plugin wins), got %q", merged.Format)
	}
	if merged.MIMEType != "image/x-raf" {
		t.Errorf("MIMEType: want gap filled by second plugin, got %q", merged.MIMEType)
	}
	if merged.Category != "Image" {
		t.Errorf("Category: want Image, got %q", merged.Category)
	}
	if merged.TakenDate == nil || !merged.TakenDate.Equal(taken) {
		t.Errorf("TakenDate: want %v, got %v", taken, merged.TakenDate)
	}
	if merged.DateSource != "xmp:test" {
		t.Errorf("DateSource: want xmp:test, got %q", merged.DateSource)
	}
	if merged.Fields["lens"] != "RF 50mm" || merged.Fields["iso"] != "400" {
		t.Errorf("Fields: want both merged, got %v", merged.Fields)
	}
	if !merged.HasContent() {
		t.Error("merged result should report HasContent")
	}
	if first.calls != 1 || second.calls != 1 {
		t.Errorf("both plugins should be called once, got %d and %d", first.calls, second.calls)
	}
}

func TestEnrichWithPluginsSkipsErrorsAndEmpty(t *testing.T) {
	broken := &fakeMetadataService{err: errors.New("plugin crashed")}
	empty := &fakeMetadataService{}
	useful := &fakeMetadataService{result: models.FileMetadata{Format: "X3F", Category: "Image"}}

	merged := EnrichWithPlugins([]MetadataService{broken, empty, useful}, models.FileSample{})

	if merged.Format != "X3F" {
		t.Errorf("Format: broken and empty plugins should be skipped, got %q", merged.Format)
	}
	if empty.calls != 1 {
		t.Error("empty plugin should still be consulted")
	}
	if merged.Fields != nil {
		t.Errorf("no plugin returned fields, want nil map, got %v", merged.Fields)
	}
}

func TestEnrichWithPluginsNoServices(t *testing.T) {
	merged := EnrichWithPlugins(nil, models.FileSample{Name: "a.raw"})
	if merged.HasContent() {
		t.Errorf("no services should yield empty metadata, got %+v", merged)
	}
}

func TestFileMetadataHasContent(t *testing.T) {
	empty := models.FileMetadata{}
	if empty.HasContent() {
		t.Error("empty metadata should not report content")
	}
	date := time.Now()
	cases := []models.FileMetadata{
		{Format: "PDF"},
		{MIMEType: "application/pdf"},
		{Category: "Document"},
		{TakenDate: &date},
		{Fields: map[string]string{"k": "v"}},
		{Fields: map[string]string{"k": ""}}, // blank field values do not count
	}
	for i, c := range cases {
		want := i < 5
		if c.HasContent() != want {
			t.Errorf("case %d: HasContent() = %v, want %v", i, c.HasContent(), want)
		}
	}
}

func TestSampleFromFileBoundedHeadTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.bin")

	// 10 KiB file: head 4 KiB of 'H', tail 1 KiB of 'T', middle 'M'.
	content := make([]byte, 10<<10)
	for i := range content[:4<<10] {
		content[i] = 'H'
	}
	for i := 4 << 10; i < len(content)-1<<10; i++ {
		content[i] = 'M'
	}
	for i := len(content) - 1<<10; i < len(content); i++ {
		content[i] = 'T'
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("write sample: %v", err)
	}

	sample := SampleFromFile(models.File{Path: path, Name: "sample.bin", Extension: "bin", Size: int64(len(content))})
	if len(sample.Head) != 4<<10 {
		t.Fatalf("Head: want 4096 bytes, got %d", len(sample.Head))
	}
	if sample.Head[0] != 'H' || sample.Head[len(sample.Head)-1] != 'H' {
		t.Error("Head should contain the leading bytes")
	}
	if len(sample.Tail) != 1<<10 {
		t.Fatalf("Tail: want 1024 bytes, got %d", len(sample.Tail))
	}
	if sample.Tail[0] != 'T' || sample.Tail[len(sample.Tail)-1] != 'T' {
		t.Error("Tail should contain the trailing bytes")
	}
	if sample.Size != int64(len(content)) || sample.Name != "sample.bin" || sample.Extension != "bin" {
		t.Errorf("sample fields not populated: %+v", sample)
	}
}

func TestSampleFromFileSmallFileNoTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiny.txt")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("write sample: %v", err)
	}

	sample := SampleFromFile(models.File{Path: path, Name: "tiny.txt", Extension: "txt", Size: 5})
	if string(sample.Head) != "hello" {
		t.Errorf("Head: want %q, got %q", "hello", sample.Head)
	}
	if sample.Tail != nil {
		t.Errorf("Tail: small file should skip tail read, got %v", sample.Tail)
	}
}

func TestSampleFromFileMissingFile(t *testing.T) {
	sample := SampleFromFile(models.File{Path: filepath.Join(t.TempDir(), "nope.bin"), Size: 10})
	if sample.Head != nil || sample.Tail != nil {
		t.Errorf("missing file should yield empty content, got head=%d tail=%d", len(sample.Head), len(sample.Tail))
	}
}

func TestHasCapability(t *testing.T) {
	caps := []string{"classifier", "metadata", "rule"}
	if !hasCapability(caps, "metadata") {
		t.Error("hasCapability should find metadata")
	}
	if !hasCapability(caps, "classifier") {
		t.Error("hasCapability should find classifier")
	}
	if hasCapability(caps, "preset") {
		t.Error("hasCapability should not find missing capability")
	}
	if hasCapability(nil, "metadata") {
		t.Error("nil capabilities should match nothing")
	}
}

func TestLoadAllMetadataSkipsBrokenPlugins(t *testing.T) {
	tempDir := t.TempDir()
	pluginHome := filepath.Join(tempDir, "installed")
	srcDir := filepath.Join(tempDir, "source-rich")
	_ = os.MkdirAll(srcDir, 0755)

	mgr, err := NewManager(pluginHome)
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}

	// Manifest declares metadata capability but the binary does not exist,
	// so LoadMetadata fails and LoadAllMetadata must skip it gracefully.
	manifest := Manifest{
		Name:         "rich-meta",
		Version:      "1.0.0",
		Description:  "broken metadata plugin",
		Capabilities: []string{MetadataCapability},
		Binary:       "rich-meta",
	}
	mfData, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(srcDir, "manifest.json"), mfData, 0644)
	if _, err := mgr.Install(srcDir); err != nil {
		t.Fatalf("install plugin: %v", err)
	}

	services, cleanup := mgr.LoadAllMetadata()
	if len(services) != 0 {
		t.Errorf("broken plugin should be skipped, got %d services", len(services))
	}
	cleanup() // must be safe to call
}
