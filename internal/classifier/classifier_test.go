package classifier

import "testing"

func TestClassifyExtension(t *testing.T) {
	tests := []struct {
		ext      string
		expected string
	}{
		{"jpg", TypeImage},
		{"PNG", TypeImage},
		{"mp4", TypeVideo},
		{"MKV", TypeVideo},
		{"mp3", TypeAudio},
		{"pdf", TypeDocument},
		{"zip", TypeArchive},
		{"exe", TypeExecutable},
		{"go", TypeCode},
		{"random_ext_xyz", TypeOther},
	}

	for _, tc := range tests {
		got := ClassifyExtension(tc.ext)
		if got != tc.expected {
			t.Errorf("ClassifyExtension(%q) = %q; want %q", tc.ext, got, tc.expected)
		}
	}
}
