package metadata

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegisterCustomExtractor(t *testing.T) {
	name := "test-custom-gctf"
	defer Unregister(name)

	RegisterCustom(name,
		func(s Sample) bool {
			return s.Size >= 8 && string(s.Head[:8]) == "GCTF\x00\x01\x02\x03"
		},
		func(s Sample) (Result, error) {
			return Result{
				Format:   "CustomGame",
				MIMEType: "application/x-custom-game",
				Category: "Other",
				Fields:   map[string]string{"engine": "v1"},
			}, nil
		})

	path := filepath.Join(t.TempDir(), "level.gctf")
	os.WriteFile(path, []byte("GCTF\x00\x01\x02\x03 payload"), 0644)

	res, err := Extract(path, "gctf")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if res.Format != "CustomGame" {
		t.Fatalf("Format = %q, want CustomGame", res.Format)
	}
	if res.Fields["engine"] != "v1" {
		t.Fatalf("Fields[engine] = %q, want v1", res.Fields["engine"])
	}
}

func TestExtractPDF(t *testing.T) {
	pdf := "%PDF-1.4\n" +
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
		"3 0 obj\n<< /Title (My Report) /Author (Alice) /CreationDate (D:20210506123045Z) >>\nendobj\n" +
		"trailer\n<< /Root 1 0 R /Info 3 0 R >>\n" +
		"startxref\n0\n%%EOF\n"

	path := filepath.Join(t.TempDir(), "doc.pdf")
	os.WriteFile(path, []byte(pdf), 0644)

	res, err := Extract(path, "pdf")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if res.Format != "PDF" {
		t.Fatalf("Format = %q, want PDF", res.Format)
	}
	if res.Fields["title"] != "My Report" {
		t.Fatalf("title = %q, want My Report", res.Fields["title"])
	}
	if res.Fields["author"] != "Alice" {
		t.Fatalf("author = %q, want Alice", res.Fields["author"])
	}
	if res.Date.Source != "pdf" || res.Date.Date.Year() != 2021 {
		t.Fatalf("date = %v src=%q, want 2021 source pdf", res.Date.Date, res.Date.Source)
	}
}

func TestExtractID3Audio(t *testing.T) {
	buf := &bytes.Buffer{}
	// ID3v2.3 header
	buf.WriteString("ID3")
	buf.Write([]byte{3, 0, 0})
	frames := id3Frame("TIT2", append([]byte{0x00}, []byte("Hello")...))
	frames = append(frames, id3Frame("TPE1", append([]byte{0x00}, []byte("Artist")...))...)
	// synchsafe size
	size := len(frames)
	buf.Write([]byte{
		byte((size >> 21) & 0x7f),
		byte((size >> 14) & 0x7f),
		byte((size >> 7) & 0x7f),
		byte(size & 0x7f),
	})
	buf.Write(frames)

	path := filepath.Join(t.TempDir(), "song.mp3")
	os.WriteFile(path, buf.Bytes(), 0644)

	res, err := Extract(path, "mp3")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if res.Format != "MP3" {
		t.Fatalf("Format = %q, want MP3", res.Format)
	}
	if res.Fields["title"] != "Hello" {
		t.Fatalf("title = %q, want Hello", res.Fields["title"])
	}
	if res.Fields["artist"] != "Artist" {
		t.Fatalf("artist = %q, want Artist", res.Fields["artist"])
	}
}

func TestExtractOOXMLZip(t *testing.T) {
	var data bytes.Buffer
	data.WriteString("PK\x03\x04") // local file header magic
	data.WriteString("0000000000[Content_Types].xml")
	data.WriteString(`<?xml version="1.0"?>
<cp:coreProperties xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties">
<dc:title>Project Notes</dc:title>
<dc:creator>Bob</dc:creator>
<dcterms:created>2020-01-02T03:04:05Z</dcterms:created>
</cp:coreProperties>`)
	data.WriteString("PK\x05\x06") // end of central directory

	path := filepath.Join(t.TempDir(), "notes.docx")
	os.WriteFile(path, data.Bytes(), 0644)

	res, err := Extract(path, "docx")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if res.Format != "OOXML" {
		t.Fatalf("Format = %q, want OOXML", res.Format)
	}
	if res.Fields["title"] != "Project Notes" {
		t.Fatalf("title = %q, want Project Notes", res.Fields["title"])
	}
	if res.Fields["creator"] != "Bob" {
		t.Fatalf("creator = %q, want Bob", res.Fields["creator"])
	}
	if res.Date.Source != "OOXML" || res.Date.Date.Year() != 2020 {
		t.Fatalf("date = %v src=%q, want 2020 source OOXML", res.Date.Date, res.Date.Source)
	}
}

func TestDetectFormats(t *testing.T) {
	cases := []struct {
		name   string
		head   []byte
		ext    string
		format string
	}{
		{"jpeg", []byte{0xff, 0xd8, 0xff, 0xe0}, "jpg", "JPEG"},
		{"png", []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}, "png", "PNG"},
		{"pdf", []byte("%PDF-1.7"), "pdf", "PDF"},
		{"zip", []byte("PK\x03\x04"), "zip", "ZIP"},
		{"id3", []byte("ID3\x03\x00\x00\x00"), "mp3", "MP3"},
		{"elf", []byte{0x7f, 'E', 'L', 'F'}, "elf", "ELF"},
		{"text", []byte("hello world\nplain text\n"), "txt", "Text"},
	}
	for _, c := range cases {
		got := detectFormat(Sample{Head: c.head, Extension: c.ext})
		if got.Format != c.format {
			t.Errorf("%s: Format = %q, want %q", c.name, got.Format, c.format)
		}
	}
}

func TestExtractMediaQuickTime(t *testing.T) {
	creation := time.Date(2023, 4, 5, 6, 7, 8, 0, time.UTC)
	seconds := uint32(creation.Sub(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)).Seconds())
	mvhd := append([]byte{0, 0, 0, 0}, make([]byte, 16)...)
	binary.BigEndian.PutUint32(mvhd[4:8], seconds)
	box := buildBox("moov", buildBox("mvhd", mvhd))

	path := filepath.Join(t.TempDir(), "video.mp4")
	os.WriteFile(path, box, 0644)

	res, err := Extract(path, "mp4")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if res.Date.Source != "quicktime" || !res.Date.Date.Equal(creation) {
		t.Fatalf("date = %v src=%q, want %v source quicktime", res.Date.Date, res.Date.Source, creation)
	}
}

func id3Frame(id string, payload []byte) []byte {
	out := make([]byte, 10+len(payload))
	copy(out[0:4], id)
	binary.BigEndian.PutUint32(out[4:8], uint32(len(payload)))
	copy(out[10:], payload)
	return out
}
