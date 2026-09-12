package scanner

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanner(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "chest_scanner_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create normal file
	normalFile := filepath.Join(tempDir, "file1.txt")
	if err := os.WriteFile(normalFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create dotfile (hidden on Unix)
	dotFile := filepath.Join(tempDir, ".dotfile.txt")
	if err := os.WriteFile(dotFile, []byte("hidden"), 0644); err != nil {
		t.Fatal(err)
	}

	// Subdir
	subDir := filepath.Join(tempDir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	subFile := filepath.Join(subDir, "file2.pdf")
	if err := os.WriteFile(subFile, []byte("%PDF-1.4"), 0644); err != nil {
		t.Fatal(err)
	}

	// Test 1: Scan excluding hidden
	s := New(ScanOptions{
		Recursive:     true,
		IncludeHidden: false,
	})

	files, err := s.Scan(tempDir)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("expected 2 files without hidden, got %d", len(files))
	}

	// Test 2: Scan including hidden
	sHidden := New(ScanOptions{
		Recursive:     true,
		IncludeHidden: true,
	})

	filesHidden, err := sHidden.Scan(tempDir)
	if err != nil {
		t.Fatalf("scan with hidden failed: %v", err)
	}

	if len(filesHidden) != 3 {
		t.Errorf("expected 3 files including hidden, got %d", len(filesHidden))
	}
}

func TestScannerExtractDates(t *testing.T) {
	tempDir := t.TempDir()
	date := []byte("2022:05:06 07:08:09\x00")
	exif := buildTIFFWithExifDate(date)
	payload := append([]byte("Exif\x00\x00"), exif...)
	app1 := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(app1[:2], uint16(len(app1)))
	copy(app1[2:], payload)
	jpeg := append([]byte{0xff, 0xd8, 0xff, 0xe1}, app1...)
	jpeg = append(jpeg, 0xff, 0xd9)
	path := filepath.Join(tempDir, "photo.jpg")
	if err := os.WriteFile(path, jpeg, 0644); err != nil {
		t.Fatalf("write jpeg: %v", err)
	}

	files, err := New(ScanOptions{ExtractDates: true}).Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	if files[0].TakenDate == nil {
		t.Fatal("TakenDate is nil")
	}
	if files[0].TakenDate.Year() != 2022 || files[0].TakenDate.Month() != time.May || files[0].TakenDate.Day() != 6 {
		t.Fatalf("TakenDate = %v, want 2022-05-06", *files[0].TakenDate)
	}
	if files[0].TakenDateSource != "exif" {
		t.Fatalf("TakenDateSource = %q, want exif", files[0].TakenDateSource)
	}
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
