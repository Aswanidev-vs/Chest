package indexer

import (
	"fmt"
	"image"
	_ "image/gif" // stdlib decoders only — no new module dependency
	_ "image/jpeg"
	_ "image/png"
	"math"
	"math/bits"
	"os"
	"runtime"
	"strings"

	"golang.org/x/sync/errgroup"
)

// Perceptual-hash geometry. The image is reduced to a phashGrid x phashGrid
// grayscale thumbnail, transformed with a 2-D DCT, and reduced to the
// phashBlock x phashBlock low-frequency corner of that transform — 8*8 = 64
// coefficients, one bit each.
const (
	phashGrid  = 32
	phashBlock = 8
)

// DefaultSimilarThreshold is the Hamming distance at which two perceptual
// hashes are treated as "the same picture, differently encoded or rescaled".
// 10/64 is the conventional default: low enough to reject genuinely different
// images, high enough to survive re-encoding, resizing and mild edits.
const DefaultSimilarThreshold = 10

// imageExtensions are the indexed extensions FindSimilarImages will decode.
// They are deliberately restricted to formats the standard library can decode:
// a perceptual hash of a file we cannot read is meaningless, and guessing
// with a third-party decoder would add a dependency for no benefit.
var imageExtensions = []string{"jpg", "jpeg", "png", "gif"}

// SimilarImage is one indexed image together with its perceptual hash.
type SimilarImage struct {
	Path string `json:"path"`
	Size int64  `json:"size_bytes"`
	// Hash is the 16-character hex rendering of the 64-bit perceptual hash.
	Hash string `json:"phash"`
	// Distance is the Hamming distance to the group's representative.
	Distance int `json:"distance"`
}

// SimilarGroup is a set of images whose perceptual hashes are within the
// configured threshold of one another. These are near-duplicates, NOT byte
// identical files: every member is a distinct file whose content may differ.
type SimilarGroup struct {
	Hash string `json:"phash"`
	// MaxDistance is the largest distance from the representative observed in
	// this group.
	MaxDistance int            `json:"max_distance"`
	Size        int64          `json:"size_bytes"`
	Files       []SimilarImage `json:"files"`
}

// phashEntry is the internal, unformatted form of a hashed image.
type phashEntry struct {
	path string
	size int64
	hash uint64
}

// HammingDistance returns the number of differing bits between two 64-bit
// perceptual hashes.
func HammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// FormatHash renders a perceptual hash as 16 hex characters.
func FormatHash(h uint64) string {
	const hexDigits = "0123456789abcdef"
	var buf [16]byte
	for i := 0; i < 16; i++ {
		buf[15-i] = hexDigits[(h>>(4*i))&0xf]
	}
	return string(buf[:])
}

// dctCos holds cos((2x+1) * u * PI / (2 * phashGrid)) for the separable 2-D
// DCT-II. It is computed once at package init: 32x32 floats is a rounding
// error next to the decode cost, and recomputing it per image would dominate
// the runtime for small images.
var dctCos = func() [phashGrid][phashGrid]float64 {
	var table [phashGrid][phashGrid]float64
	for x := 0; x < phashGrid; x++ {
		for u := 0; u < phashGrid; u++ {
			table[x][u] = math.Cos(float64(2*x+1) * float64(u) * math.Pi / (2 * phashGrid))
		}
	}
	return table
}()

// PerceptualHashFile decodes an image from disk and returns its 64-bit
// perceptual hash. The file is only ever read.
func PerceptualHashFile(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return 0, fmt.Errorf("failed decoding image: %w", err)
	}
	return PerceptualHash(img), nil
}

// PerceptualHash computes a 64-bit perceptual hash of an already-decoded
// image: grayscale, 32x32 area-average downsample, 2-D DCT, median threshold
// over the low-frequency corner (excluding DC).
//
// The median is taken over the 63 non-DC coefficients so the overall
// brightness of the picture does not dominate the threshold; every one of the
// 64 coefficients is then compared against it to produce the signature.
func PerceptualHash(img image.Image) uint64 {
	pixels := grayscaleDownsample(img, phashGrid, phashGrid)
	coeffs := dct2D(pixels)

	low := make([]float64, 0, phashBlock*phashBlock-1)
	for y := 0; y < phashBlock; y++ {
		for x := 0; x < phashBlock; x++ {
			if x == 0 && y == 0 {
				continue // DC term: average brightness, not structure
			}
			low = append(low, coeffs[y*phashGrid+x])
		}
	}
	// Insertion sort: 63 elements, and it keeps the median deterministic.
	for i := 1; i < len(low); i++ {
		for j := i; j > 0 && low[j] < low[j-1]; j-- {
			low[j], low[j-1] = low[j-1], low[j]
		}
	}
	if len(low) == 0 {
		return 0
	}
	median := low[len(low)/2]

	var h uint64
	var idx uint
	for y := 0; y < phashBlock; y++ {
		for x := 0; x < phashBlock; x++ {
			if coeffs[y*phashGrid+x] > median {
				h |= 1 << (63 - idx)
			}
			idx++
		}
	}
	return h
}

// grayscaleDownsample reduces any image to a w*h grid of luma values using
// area averaging. Sampling is capped per cell so a 100-megapixel photo costs
// the same as a thumbnail.
func grayscaleDownsample(img image.Image, w, h int) []float64 {
	b := img.Bounds()
	dw, dh := b.Dx(), b.Dy()
	out := make([]float64, w*h)
	if dw <= 0 || dh <= 0 {
		return out
	}

	const maxSamplesPerAxis = 8
	for gy := 0; gy < h; gy++ {
		y0 := b.Min.Y + gy*dh/h
		y1 := b.Min.Y + (gy+1)*dh/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		stepY := (y1 - y0) / maxSamplesPerAxis
		if stepY < 1 {
			stepY = 1
		}
		for gx := 0; gx < w; gx++ {
			x0 := b.Min.X + gx*dw/w
			x1 := b.Min.X + (gx+1)*dw/w
			if x1 <= x0 {
				x1 = x0 + 1
			}
			stepX := (x1 - x0) / maxSamplesPerAxis
			if stepX < 1 {
				stepX = 1
			}

			var sum float64
			var n int
			for y := y0; y < y1; y += stepY {
				for x := x0; x < x1; x += stepX {
					r, g, bb, _ := img.At(x, y).RGBA()
					sum += 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bb)
					n++
				}
			}
			if n > 0 {
				out[gy*w+gx] = sum / float64(n)
			}
		}
	}
	return out
}

// dct2D computes the separable 2-D DCT-II of a phashGrid x phashGrid block.
// Rows are transformed first, then columns; the result is unnormalized,
// which is irrelevant for a median threshold.
func dct2D(pixels []float64) []float64 {
	rows := make([]float64, phashGrid*phashGrid)
	cols := make([]float64, phashGrid*phashGrid)

	for y := 0; y < phashGrid; y++ {
		base := y * phashGrid
		for u := 0; u < phashGrid; u++ {
			var s float64
			for x := 0; x < phashGrid; x++ {
				s += pixels[base+x] * dctCos[x][u]
			}
			rows[base+u] = s
		}
	}

	for u := 0; u < phashGrid; u++ {
		for v := 0; v < phashGrid; v++ {
			var s float64
			for y := 0; y < phashGrid; y++ {
				s += rows[y*phashGrid+u] * dctCos[y][v]
			}
			cols[v*phashGrid+u] = s
		}
	}
	return cols
}

// FindSimilarImages groups indexed images whose perceptual hashes are within
// threshold bits of each other. When `root` is non-empty only that indexed
// folder subtree is considered.
//
// It is strictly read-only: every image is opened for reading and decoded in
// memory, nothing on disk is created, modified, renamed or removed.
func (s *Store) FindSimilarImages(root string, threshold int) ([]SimilarGroup, error) {
	if threshold <= 0 {
		threshold = DefaultSimilarThreshold
	}
	if threshold > 64 {
		threshold = 64
	}

	scope, scopeArgs := pathScope(root)
	query := "SELECT path, size FROM files WHERE size > 0"
	if scope != "" {
		query += " AND " + scope
	}
	query += " AND LOWER(extension) IN (" + placeholders(len(imageExtensions)) + ")"

	// Parameter order must follow the SQL text: the scope predicate comes
	// first, the extension list second.
	args := make([]any, 0, len(scopeArgs)+len(imageExtensions))
	args = append(args, scopeArgs...)
	for _, ext := range imageExtensions {
		args = append(args, ext)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		path string
		size int64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.path, &c.size); err == nil {
			candidates = append(candidates, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) < 2 {
		return nil, nil
	}

	// Hashing is CPU-bound (decode + DCT), so fan it out across the available
	// cores. Undecodable or vanished files are skipped rather than failing the
	// whole report: a corrupt JPEG must not hide every other near-duplicate.
	entries := make([]phashEntry, len(candidates))
	eg := errgroup.Group{}
	eg.SetLimit(runtime.GOMAXPROCS(0))
	for i := range candidates {
		i := i
		eg.Go(func() error {
			h, err := PerceptualHashFile(candidates[i].path)
			if err != nil {
				return nil
			}
			entries[i] = phashEntry{path: candidates[i].path, size: candidates[i].size, hash: h}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	hashed := make([]phashEntry, 0, len(entries))
	for _, e := range entries {
		if e.path == "" {
			continue
		}
		hashed = append(hashed, e)
	}
	return groupByDistance(hashed, threshold), nil
}

// groupByDistance clusters hashed images whose pairwise Hamming distance is
// within threshold. Clustering is greedy against each group's representative
// hash, which is what makes the grouping stable and cheap: an image joins the
// first group whose representative it matches, otherwise it seeds its own
// group. Single-member groups are dropped because a lone image is not a
// duplicate of anything.
func groupByDistance(entries []phashEntry, threshold int) []SimilarGroup {
	// Sort by hash so the result is deterministic for a given input set
	// regardless of the order rows came back from SQLite.
	sorted := make([]phashEntry, len(entries))
	copy(sorted, entries)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].hash < sorted[j-1].hash; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	// Within a hash, order by path so ties are stable too.
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].hash == sorted[j-1].hash && sorted[j].path < sorted[j-1].path; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	type cluster struct {
		rep       phashEntry
		members   []phashEntry
		distances []int
		maxDist   int
	}
	var clusters []*cluster

	for _, e := range sorted {
		matched := -1
		for i, c := range clusters {
			if HammingDistance(c.rep.hash, e.hash) <= threshold {
				matched = i
				break
			}
		}
		if matched < 0 {
			clusters = append(clusters, &cluster{rep: e})
			matched = len(clusters) - 1
		}
		c := clusters[matched]
		d := HammingDistance(c.rep.hash, e.hash)
		c.members = append(c.members, e)
		c.distances = append(c.distances, d)
		if d > c.maxDist {
			c.maxDist = d
		}
	}

	groups := make([]SimilarGroup, 0, len(clusters))
	for _, c := range clusters {
		if len(c.members) < 2 {
			continue
		}
		g := SimilarGroup{
			Hash:        FormatHash(c.rep.hash),
			MaxDistance: c.maxDist,
			Size:        c.rep.size,
			Files:       make([]SimilarImage, 0, len(c.members)),
		}
		for i, m := range c.members {
			g.Files = append(g.Files, SimilarImage{
				Path:     m.path,
				Size:     m.size,
				Hash:     FormatHash(m.hash),
				Distance: c.distances[i],
			})
		}
		groups = append(groups, g)
	}
	return groups
}

// placeholders builds "?,?,?" for an IN clause of n parameters.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
