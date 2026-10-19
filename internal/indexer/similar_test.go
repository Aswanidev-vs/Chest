package indexer

import (
	"bytes"
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file covers both of the read-only intelligence features that ship as
// new indexer capabilities: perceptual near-duplicate hashing (similar.go) and
// storage trend snapshots (trends.go).

// --- synthetic image helpers -------------------------------------------------

// gradientImage builds a smooth diagonal ramp. Its spectrum is concentrated in
// the lowest frequencies, which is what perceptual hashing keys on.
func gradientImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8((x*255/max(1, w-1) + y*255/max(1, h-1)) / 2)
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return img
}

// blockImage builds a coarse block pattern: energy sits at high spatial
// frequencies, so it hashes far away from a gradient.
func blockImage(w, h, block int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			on := (x/block+y/block)%2 == 0
			v := uint8(0)
			if on {
				v = 255
			}
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return img
}

// noiseImage builds a deterministic pseudo-random texture.
func noiseImage(w, h int, seed int64) *image.RGBA {
	rng := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := rng.Uint32() % 256
			img.Set(x, y, color.RGBA{uint8(v), uint8(v), uint8(v), 255})
		}
	}
	return img
}

// writePNG encodes img to path and returns the path.
func writePNG(t *testing.T, path string, img image.Image) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
	return path
}

// --- perceptual hash ---------------------------------------------------------

func TestSimilarHashIdenticalImages(t *testing.T) {
	img := gradientImage(64, 64)
	want := PerceptualHash(img)

	// Same pixels hashed again must be bit-for-bit the same hash.
	assert.Equal(t, want, PerceptualHash(img), "hashing is deterministic")

	// Same picture after a PNG encode/decode round trip.
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	decoded, _, err := image.Decode(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	assert.Equal(t, want, PerceptualHash(decoded), "re-encoded copy must hash identically")

	// The same bytes written twice to disk and read back.
	dir := t.TempDir()
	a := writePNG(t, filepath.Join(dir, "a.png"), img)
	b := filepath.Join(dir, "b.png")
	require.NoError(t, os.WriteFile(b, buf.Bytes(), 0o644))

	ha, err := PerceptualHashFile(a)
	require.NoError(t, err)
	hb, err := PerceptualHashFile(b)
	require.NoError(t, err)
	assert.Equal(t, ha, hb)
	assert.Equal(t, want, ha)
}

func TestSimilarHashDifferentImages(t *testing.T) {
	gradient := PerceptualHash(gradientImage(64, 64))
	blocks := PerceptualHash(blockImage(64, 64, 4))
	noise := PerceptualHash(noiseImage(64, 64, 42))

	assert.NotEqual(t, gradient, blocks, "a block pattern must not hash like a gradient")
	assert.NotEqual(t, gradient, noise, "a noise texture must not hash like a gradient")
	assert.NotEqual(t, blocks, noise, "unrelated textures must not collide")

	// The separation has to be wide enough that the default threshold keeps
	// them apart — otherwise every --similar run would merge every image.
	assert.Greater(t, HammingDistance(gradient, blocks), DefaultSimilarThreshold,
		"gradient and blocks must be farther apart than the default threshold")
	assert.Greater(t, HammingDistance(gradient, noise), DefaultSimilarThreshold,
		"gradient and noise must be farther apart than the default threshold")
}

func TestSimilarHammingDistance(t *testing.T) {
	assert.Equal(t, 0, HammingDistance(0, 0))
	assert.Equal(t, 64, HammingDistance(0, ^uint64(0)))
	assert.Equal(t, 0, HammingDistance(0xDEADBEEFCAFEBABE, 0xDEADBEEFCAFEBABE))
	assert.Equal(t, 1, HammingDistance(0b1011, 0b0011))
	assert.Equal(t, 2, HammingDistance(0b0000, 0b0011))
	assert.Equal(t, 32, HammingDistance(0, 0xFFFFFFFF))
}

func TestSimilarHashFormatting(t *testing.T) {
	assert.Equal(t, "0000000000000000", FormatHash(0))
	assert.Equal(t, "ffffffffffffffff", FormatHash(^uint64(0)))
	assert.Equal(t, "00000000000000ff", FormatHash(255))
	assert.Len(t, FormatHash(PerceptualHash(gradientImage(32, 32))), 16)
}

// TestSimilarGroupingByHamming proves grouping is driven by Hamming distance:
// entries within the threshold land in one group, entries beyond it do not,
// and lone entries are never reported as a group of one.
func TestSimilarGroupingByHamming(t *testing.T) {
	// 0xFF and 0xF0 differ in 4 bits; 0x00 differs from both in 8 and 8.
	entries := []phashEntry{
		{path: "/img/a.png", size: 10, hash: 0xFF},
		{path: "/img/b.png", size: 11, hash: 0xF0},
		{path: "/img/c.png", size: 12, hash: 0x00},
	}

	groups := groupByDistance(entries, 4)
	require.Len(t, groups, 1, "0x00 and 0xF0 are within 4 bits; 0xFF is 8 away")
	assert.Equal(t, "0000000000000000", groups[0].Hash, "the lowest hash becomes the representative")
	require.Len(t, groups[0].Files, 2)
	assert.Equal(t, 4, groups[0].MaxDistance)

	// A generous threshold pulls the third image in.
	all := groupByDistance(entries, 8)
	require.Len(t, all, 1)
	require.Len(t, all[0].Files, 3)

	// A single image is not a duplicate of anything.
	lonely := groupByDistance(entries[:1], 64)
	assert.Empty(t, lonely)
}

// TestSimilarFindSimilarImages exercises the whole read path: real PNG files on
// disk, indexed by the store, then grouped.
func TestSimilarFindSimilarImages(t *testing.T) {
	store := newTestStore(t)
	root := t.TempDir()

	// Two byte-different copies of the same picture (different PNG encoders
	// produce different bytes) plus one unrelated picture.
	src := gradientImage(96, 96)
	a := writePNG(t, filepath.Join(root, "a.png"), src)
	b := writePNG(t, filepath.Join(root, "b.png"), src)
	c := writePNG(t, filepath.Join(root, "c.png"), blockImage(96, 96, 6))

	_, err := store.IndexDirectory(root, false, nil)
	require.NoError(t, err)

	groups, err := store.FindSimilarImages(root, DefaultSimilarThreshold)
	require.NoError(t, err)
	require.Len(t, groups, 1, "exactly the two copies of the same picture should group")
	assert.Equal(t, 2, len(groups[0].Files))
	assert.LessOrEqual(t, groups[0].MaxDistance, DefaultSimilarThreshold)

	paths := map[string]bool{}
	for _, f := range groups[0].Files {
		paths[f.Path] = true
		assert.Len(t, f.Hash, 16, "each file reports its own perceptual hash")
	}
	assert.True(t, paths[a], "a.png should be in the group")
	assert.True(t, paths[b], "b.png should be in the group")
	assert.False(t, paths[c], "the block pattern is a different picture")

	// Scoping narrows the query; an unrelated root finds nothing.
	other := t.TempDir()
	writePNG(t, filepath.Join(other, "only.png"), noiseImage(96, 96, 7))
	_, err = store.IndexDirectory(other, false, nil)
	require.NoError(t, err)
	scoped, err := store.FindSimilarImages(other, DefaultSimilarThreshold)
	require.NoError(t, err)
	assert.Empty(t, scoped)
}

// TestSimilarDetectionIsReadOnly pins the safety invariant: finding
// near-duplicates never creates, modifies, renames or removes a file.
func TestSimilarDetectionIsReadOnly(t *testing.T) {
	store := newTestStore(t)
	root := t.TempDir()

	src := gradientImage(64, 64)
	p1 := writePNG(t, filepath.Join(root, "one.png"), src)
	p2 := writePNG(t, filepath.Join(root, "two.png"), src)

	type state struct {
		size    int64
		modTime time.Time
	}
	snapshot := func(path string) state {
		info, err := os.Stat(path)
		require.NoError(t, err)
		return state{size: info.Size(), modTime: info.ModTime()}
	}
	before := map[string]state{p1: snapshot(p1), p2: snapshot(p2)}

	_, err := store.IndexDirectory(root, false, nil)
	require.NoError(t, err)
	_, err = store.FindSimilarImages(root, DefaultSimilarThreshold)
	require.NoError(t, err)

	after := map[string]state{p1: snapshot(p1), p2: snapshot(p2)}
	assert.Equal(t, before, after, "detection must leave every file untouched")

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "detection must not write anything into the scanned folder")
}

// --- storage trends ----------------------------------------------------------

func TestTrendSnapshotDelta(t *testing.T) {
	store := newTestStore(t)
	root := t.TempDir()

	a := filepath.Join(root, "a.txt")
	require.NoError(t, os.WriteFile(a, []byte("aaaa"), 0o644)) // 4 bytes
	_, err := store.IndexDirectory(root, false, nil)
	require.NoError(t, err)

	first, err := store.GetStats(root)
	require.NoError(t, err)
	require.Equal(t, 1, first.TotalFiles)
	require.Equal(t, int64(4), first.TotalSize)
	_, err = store.RecordStorageSnapshot(root, first)
	require.NoError(t, err)

	// Grow: one file gets bigger, another appears.
	require.NoError(t, os.WriteFile(a, []byte("aaaaaaaaaa"), 0o644)) // 10 bytes
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("bbb"), 0o644))
	_, err = store.IndexDirectory(root, false, nil)
	require.NoError(t, err)

	second, err := store.GetStats(root)
	require.NoError(t, err)
	require.Equal(t, 2, second.TotalFiles)
	require.Equal(t, int64(13), second.TotalSize)
	_, err = store.RecordStorageSnapshot(root, second)
	require.NoError(t, err)

	trend, err := store.StorageTrend(root, TrendPeriodMonth, time.Now())
	require.NoError(t, err)

	assert.True(t, trend.HasBaseline)
	assert.Equal(t, 2, trend.Snapshots)
	assert.Equal(t, TrendPeriodMonth, trend.Period)
	assert.Equal(t, 30, trend.WindowDays)
	assert.Equal(t, int64(4), trend.TotalSizeThen)
	assert.Equal(t, int64(13), trend.TotalSizeNow)
	assert.Equal(t, int64(9), trend.TotalSizeDelta)
	assert.Equal(t, 1, trend.TotalFilesThen)
	assert.Equal(t, 2, trend.TotalFilesNow)
	assert.Equal(t, 1, trend.TotalFilesDelta)
	require.NotNil(t, trend.Now)
	assert.Equal(t, int64(13), trend.Now.TotalSize)
	require.NotNil(t, trend.Then)
	assert.Equal(t, int64(4), trend.Then.TotalSize)

	// Per-category growth is reported alongside the total.
	require.NotEmpty(t, trend.Categories)
	assert.Equal(t, "Document", trend.Categories[0].Category)
	assert.Equal(t, int64(4), trend.Categories[0].SizeThen)
	assert.Equal(t, int64(13), trend.Categories[0].SizeNow)
	assert.Equal(t, int64(9), trend.Categories[0].SizeDelta)
	assert.Equal(t, 1, trend.Categories[0].FilesDelta)
}

func TestTrendShrinkIsNegative(t *testing.T) {
	store := newTestStore(t)
	root := t.TempDir()

	f := filepath.Join(root, "big.txt")
	require.NoError(t, os.WriteFile(f, []byte("0123456789"), 0o644))
	_, err := store.IndexDirectory(root, false, nil)
	require.NoError(t, err)
	summary, err := store.GetStats(root)
	require.NoError(t, err)
	_, err = store.RecordStorageSnapshot(root, summary)
	require.NoError(t, err)

	require.NoError(t, os.Remove(f))
	_, err = store.IndexDirectory(root, false, nil)
	require.NoError(t, err)
	summary, err = store.GetStats(root)
	require.NoError(t, err)
	_, err = store.RecordStorageSnapshot(root, summary)
	require.NoError(t, err)

	trend, err := store.StorageTrend(root, TrendPeriodMonth, time.Now())
	require.NoError(t, err)
	require.True(t, trend.HasBaseline)
	assert.Equal(t, int64(-10), trend.TotalSizeDelta)
	assert.Equal(t, -1, trend.TotalFilesDelta)
	assert.Equal(t, -100.0, trend.TotalGrowthPct)
}

// TestTrendSingleSnapshotHasNoBaseline covers the very first run on a fresh
// database: there is nothing to compare against yet, and the report must say
// so instead of inventing a zero delta.
func TestTrendSingleSnapshotHasNoBaseline(t *testing.T) {
	store := newTestStore(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644))
	_, err := store.IndexDirectory(root, false, nil)
	require.NoError(t, err)
	summary, err := store.GetStats(root)
	require.NoError(t, err)
	snap, err := store.RecordStorageSnapshot(root, summary)
	require.NoError(t, err)

	trend, err := store.StorageTrend(root, TrendPeriodDay, snap.CapturedAt)
	require.NoError(t, err)
	assert.False(t, trend.HasBaseline)
	assert.Nil(t, trend.Then)
	require.NotNil(t, trend.Now)
	assert.Equal(t, 1, trend.TotalFilesNow)
	assert.Equal(t, int64(1), trend.TotalSizeNow)
	assert.Equal(t, int64(0), trend.TotalSizeDelta)
}

func TestTrendScopedSnapshotsAreIndependent(t *testing.T) {
	store := newTestStore(t)
	one := t.TempDir()
	two := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(one, "a.txt"), []byte("aa"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(two, "b.txt"), []byte("bbbb"), 0o644))
	_, err := store.IndexDirectory(one, false, nil)
	require.NoError(t, err)
	_, err = store.IndexDirectory(two, false, nil)
	require.NoError(t, err)

	for _, scope := range []string{one, two} {
		summary, err := store.GetStats(scope)
		require.NoError(t, err)
		_, err = store.RecordStorageSnapshot(scope, summary)
		require.NoError(t, err)
	}

	first, err := store.StorageTrend(one, TrendPeriodMonth, time.Now())
	require.NoError(t, err)
	second, err := store.StorageTrend(two, TrendPeriodMonth, time.Now())
	require.NoError(t, err)

	assert.Equal(t, int64(2), first.TotalSizeNow)
	assert.Equal(t, int64(4), second.TotalSizeNow)
	assert.Equal(t, 1, first.Snapshots)
	assert.Equal(t, 1, second.Snapshots)
}

func TestTrendPeriodParsing(t *testing.T) {
	tests := []struct {
		in      string
		want    TrendPeriod
		wantErr bool
	}{
		{"", DefaultTrendPeriod, false},
		{"day", TrendPeriodDay, false},
		{"WEEK", TrendPeriodWeek, false},
		{" month ", TrendPeriodMonth, false},
		{"year", "", true},
		{"decade", "", true},
	}
	for _, tt := range tests {
		got, err := ParseTrendPeriod(tt.in)
		if tt.wantErr {
			assert.Error(t, err, "ParseTrendPeriod(%q)", tt.in)
			continue
		}
		require.NoError(t, err, "ParseTrendPeriod(%q)", tt.in)
		assert.Equal(t, tt.want, got, "ParseTrendPeriod(%q)", tt.in)
	}

	assert.Equal(t, 24*time.Hour, TrendPeriodDay.Duration())
	assert.Equal(t, 7*24*time.Hour, TrendPeriodWeek.Duration())
	assert.Equal(t, 30*24*time.Hour, TrendPeriodMonth.Duration())
}

// TestTrendSchemaOpensExistingDatabase is the upgrade-safety test: a database
// written before storage trends existed must open, keep its rows, and gain the
// new table without any destructive ALTER.
func TestTrendSchemaOpensExistingDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	// Build a database with only the pre-trends schema.
	legacy, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = legacy.Exec(`CREATE TABLE files (
		path TEXT PRIMARY KEY, rel_path TEXT, name TEXT, extension TEXT,
		size INTEGER, mod_time INTEGER, category TEXT, hash TEXT)`)
	require.NoError(t, err)
	_, err = legacy.Exec(`INSERT INTO files VALUES ('/old/a.txt', 'a.txt', 'a.txt', 'txt', 7, 0, 'documents', '')`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	store, err := OpenOrCreate(dbPath)
	require.NoError(t, err, "an existing index.db must open unchanged")
	defer store.Close()

	// The pre-existing row survives.
	var n int
	require.NoError(t, store.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&n))
	assert.Equal(t, 1, n)

	// The new table exists and accepts snapshots.
	snap, err := store.RecordStorageSnapshot("", StatsSummary{TotalFiles: 1, TotalSize: 7, CategoryCounts: map[string]int{"documents": 1}, CategorySizes: map[string]int64{"documents": 7}})
	require.NoError(t, err)
	assert.Equal(t, 1, snap.TotalFiles)

	// Reopening an already-upgraded database is idempotent.
	require.NoError(t, store.Close())
	again, err := OpenOrCreate(dbPath)
	require.NoError(t, err)
	defer again.Close()
	var snaps int
	require.NoError(t, again.db.QueryRow("SELECT COUNT(*) FROM storage_snapshots").Scan(&snaps))
	assert.Equal(t, 1, snaps)
}
