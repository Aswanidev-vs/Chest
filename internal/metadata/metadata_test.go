package metadata

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadEXIFDate(t *testing.T) {
	date := []byte("2024:07:08 09:10:11\x00")
	tiff := buildTIFFWithExifDate(date)
	jpeg := append([]byte{0xff, 0xd8}, buildAPP1(tiff)...)
	jpeg = append(jpeg, 0xff, 0xd9)

	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, jpeg, 0644); err != nil {
		t.Fatalf("write jpeg: %v", err)
	}

	got, ok, err := Read(path, "jpg")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !ok {
		t.Fatal("Read() ok = false, want true")
	}
	if got.Source != "exif" {
		t.Fatalf("Source = %q, want exif", got.Source)
	}
	if got.Date.Year() != 2024 || got.Date.Month() != 7 || got.Date.Day() != 8 {
		t.Fatalf("Date = %v, want 2024-07-08", got.Date)
	}
}

func TestReadMissingMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, []byte{0xff, 0xd8, 0xff, 0xd9}, 0644); err != nil {
		t.Fatalf("write jpeg: %v", err)
	}

	_, ok, err := Read(path, "jpg")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if ok {
		t.Fatal("Read() ok = true, want false")
	}
}

func TestReadQuickTimeDate(t *testing.T) {
	creation := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
	seconds := uint32(creation.Sub(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)).Seconds())
	mvhd := append([]byte{0, 0, 0, 0}, make([]byte, 16)...)
	binary.BigEndian.PutUint32(mvhd[4:8], seconds)
	box := buildBox("moov", buildBox("mvhd", mvhd))

	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, box, 0644); err != nil {
		t.Fatalf("write mp4: %v", err)
	}

	got, ok, err := Read(path, "mp4")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !ok {
		t.Fatal("Read() ok = false, want true")
	}
	if got.Source != "quicktime" {
		t.Fatalf("Source = %q, want quicktime", got.Source)
	}
	if !got.Date.Equal(creation) {
		t.Fatalf("Date = %v, want %v", got.Date, creation)
	}
}

func buildAPP1(tiff []byte) []byte {
	payload := append([]byte("Exif\x00\x00"), tiff...)
	box := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(box[:2], uint16(len(box)))
	copy(box[2:], payload)
	return append([]byte{0xff, 0xe1}, box...)
}

func buildBox(boxType string, payload []byte) []byte {
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box[:4], uint32(len(box)))
	copy(box[4:8], boxType)
	copy(box[8:], payload)
	return box
}

func buildTIFFWithExifDate(dateValue []byte) []byte {
	ifd0Offset := 8
	exifIFDOffset := ifd0Offset + 2 + 12 + 2
	exifEntryOffset := exifIFDOffset + 2
	exifDateOffset := exifEntryOffset + 12
	tiff := make([]byte, exifDateOffset+len(dateValue))
	copy(tiff[:2], "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], uint32(ifd0Offset))

	tiff[ifd0Offset] = 1
	entry := ifd0Offset + 2
	binary.LittleEndian.PutUint16(tiff[entry:entry+2], 0x8769)
	binary.LittleEndian.PutUint16(tiff[entry+2:entry+4], 4)
	binary.LittleEndian.PutUint32(tiff[entry+4:entry+8], 1)
	binary.LittleEndian.PutUint32(tiff[entry+8:entry+12], uint32(exifIFDOffset))

	tiff[exifIFDOffset] = 1
	binary.LittleEndian.PutUint16(tiff[exifEntryOffset:exifEntryOffset+2], 0x9003)
	binary.LittleEndian.PutUint16(tiff[exifEntryOffset+2:exifEntryOffset+4], 2)
	binary.LittleEndian.PutUint32(tiff[exifEntryOffset+4:exifEntryOffset+8], uint32(len(dateValue)))
	binary.LittleEndian.PutUint32(tiff[exifEntryOffset+8:exifEntryOffset+12], uint32(exifDateOffset))
	copy(tiff[exifDateOffset:], dateValue)
	return tiff
}
