package dates

import (
	"testing"
	"time"
)

func TestFromFilename(t *testing.T) {
	tests := []struct {
		name string
		file string
		want time.Time
		ok   bool
	}{
		{"compact YYYYMMDD between separators", "IMG_20230115_123456.jpg", time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"whatsapp style date time", "WhatsApp Image 2024-05-01 at 10.00.00.jpg", time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), true},
		{"screenshot style", "Screenshot_20250126-093000.png", time.Date(2025, 1, 26, 0, 0, 0, 0, time.UTC), true},
		{"YYYY-Mon-DD", "report-2023-Jan-15.pdf", time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"YYYYMonDD", "2025Jan15_foo.txt", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"YYYY_MM_DD", "photo_2023_08_15.jpg", time.Date(2023, 8, 15, 0, 0, 0, 0, time.UTC), true},
		{"YYYY.MM.DD", "photo_2023.08.15.jpg", time.Date(2023, 8, 15, 0, 0, 0, 0, time.UTC), true},
		{"DD-MM-YYYY", "holiday 15-01-2025.bin", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"Mon DD YYYY", "Jan 15 2025.jpg", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"DD Mon YYYY", "15 Jan 2025.jpg", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"lowercase month name", "party 04 jul 2020.png", time.Date(2020, 7, 4, 0, 0, 0, 0, time.UTC), true},
		{"leap day", "20240229.jpg", time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), true},
		{"directory components ignored", "sub/dir/IMG_20230115.jpg", time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"first match scanning left to right", "IMG_20230115_20240102.jpg", time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"earliest match across pattern kinds", "2025Jan15 then 2023-01-01.txt", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"invalid month falls through to later date", "IMG_20251301_20250115.jpg", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"no date in name", "notes.txt", time.Time{}, false},
		{"plain counter digits", "IMG_0001.jpg", time.Time{}, false},
		{"month out of range", "IMG_20251301.jpg", time.Time{}, false},
		{"day out of range", "IMG_20230230.jpg", time.Time{}, false},
		{"february 29 in a non-leap year", "IMG_20230229.jpg", time.Time{}, false},
		{"year below range", "IMG_19891231.jpg", time.Time{}, false},
		{"year above range", "IMG_21010101.jpg", time.Time{}, false},
		{"phone number digit run", "contact_5551234567.jpg", time.Time{}, false},
		{"nine digit run is not an 8-digit date", "serial_120250115.jpg", time.Time{}, false},
		{"digits glued to the date", "x12023-01-15", time.Time{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FromFilename(tt.file)
			if ok != tt.ok {
				t.Fatalf("FromFilename(%q) ok = %v, want %v", tt.file, ok, tt.ok)
			}
			if ok && !got.Equal(tt.want) {
				t.Fatalf("FromFilename(%q) = %v, want %v", tt.file, got, tt.want)
			}
		})
	}
}
