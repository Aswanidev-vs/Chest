package scanner

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScanPopulatesMetadata proves the rules engine can match embedded
// metadata: a scanned file carries the extractor's Fields, which is what
// conditions like camera= and meta.<key>= read.
func TestScanPopulatesMetadata(t *testing.T) {
	dir := t.TempDir()

	var mp3 []byte
	mp3 = append(mp3, []byte("ID3")...)
	mp3 = append(mp3, []byte{3, 0, 0}...)
	frames := id3Frame("TIT2", append([]byte{0x00}, []byte("Hello")...))
	frames = append(frames, id3Frame("TPE1", append([]byte{0x00}, []byte("Daft Punk")...))...)
	mp3 = append(mp3, []byte{
		byte((len(frames) >> 21) & 0x7f),
		byte((len(frames) >> 14) & 0x7f),
		byte((len(frames) >> 7) & 0x7f),
		byte(len(frames) & 0x7f),
	}...)
	mp3 = append(mp3, frames...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "song.mp3"), mp3, 0644))

	files, err := New(ScanOptions{}).Scan(dir)
	require.NoError(t, err)
	require.Len(t, files, 1)

	assert.Equal(t, "Hello", files[0].Metadata["title"])
	assert.Equal(t, "Daft Punk", files[0].Metadata["artist"])
}

func id3Frame(id string, payload []byte) []byte {
	out := make([]byte, 10+len(payload))
	copy(out[0:4], id)
	binary.BigEndian.PutUint32(out[4:8], uint32(len(payload)))
	copy(out[10:], payload)
	return out
}
