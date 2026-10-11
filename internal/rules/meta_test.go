package rules

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetaConditionParsing pins the DSL spellings that reach embedded metadata
// fields: bare well-known keys and the generic meta.<key> prefix.
func TestMetaConditionParsing(t *testing.T) {
	tests := []struct {
		name string
		rule string
		want models.Condition
	}{
		{name: "camera", rule: "camera=Canon -> Camera", want: models.Condition{Field: "meta:camera", Operator: models.OpEqual, Value: "Canon"}},
		{name: "artist quoted", rule: `artist="Daft Punk" -> Music`, want: models.Condition{Field: "meta:artist", Operator: models.OpEqual, Value: "Daft Punk"}},
		{name: "artist single quoted", rule: `artist='Daft Punk' -> Music`, want: models.Condition{Field: "meta:artist", Operator: models.OpEqual, Value: "Daft Punk"}},
		{name: "codec", rule: "codec=h264 -> H264", want: models.Condition{Field: "meta:codec", Operator: models.OpEqual, Value: "h264"}},
		{name: "album", rule: "album=Random Access Memories -> Album", want: models.Condition{Field: "meta:album", Operator: models.OpEqual, Value: "Random Access Memories"}},
		{name: "genre", rule: "genre=Jazz -> Jazz", want: models.Condition{Field: "meta:genre", Operator: models.OpEqual, Value: "Jazz"}},
		{name: "title", rule: "title=Holiday -> Titles", want: models.Condition{Field: "meta:title", Operator: models.OpEqual, Value: "Holiday"}},
		{name: "composer", rule: "composer=Bach -> Bach", want: models.Condition{Field: "meta:composer", Operator: models.OpEqual, Value: "Bach"}},
		{name: "encoder", rule: "encoder=Lavf58 -> Encoded", want: models.Condition{Field: "meta:encoder", Operator: models.OpEqual, Value: "Lavf58"}},
		{name: "software", rule: "software=GIMP -> GIMP", want: models.Condition{Field: "meta:software", Operator: models.OpEqual, Value: "GIMP"}},
		{name: "duration", rule: "duration=120 -> Short", want: models.Condition{Field: "meta:duration", Operator: models.OpEqual, Value: "120"}},
		{name: "width", rule: "width=1920 -> HD", want: models.Condition{Field: "meta:width", Operator: models.OpEqual, Value: "1920"}},
		{name: "height", rule: "height=1080 -> HD", want: models.Condition{Field: "meta:height", Operator: models.OpEqual, Value: "1080"}},
		{name: "comment", rule: "comment=family -> Commented", want: models.Condition{Field: "meta:comment", Operator: models.OpEqual, Value: "family"}},
		{name: "generic prefix", rule: "meta.comment=hello -> Commented", want: models.Condition{Field: "meta:comment", Operator: models.OpEqual, Value: "hello"}},
		{name: "generic unknown key", rule: "meta.engine=v1 -> Engines", want: models.Condition{Field: "meta:engine", Operator: models.OpEqual, Value: "v1"}},
		{name: "regex", rule: "camera=~'^Canon' -> Camera", want: models.Condition{Field: "meta:camera", Operator: models.OpRegex, Value: "^Canon"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := ParseRule(tt.rule, 10)
			require.NoError(t, err)
			require.Len(t, rule.Conditions, 1)
			assert.Equal(t, tt.want, rule.Conditions[0])
			assert.Empty(t, ValidateRule(rule))
		})
	}
}

func TestParseRuleUnknownFieldStillErrors(t *testing.T) {
	_, err := ParseRule("color=red -> Colors", 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field 'color'")
}

// TestMetaConditionMatching exercises the string semantics shared with the
// name field, including the case-insensitivity and missing-key behavior.
func TestMetaConditionMatching(t *testing.T) {
	file := models.File{
		Name: "IMG_0001.CR2",
		Metadata: map[string]string{
			"camera":  "Canon EOS R5",
			"artist":  "Daft Punk",
			"codec":   "h264",
			"width":   "8192",
			"engine":  "",
			"comment": "family holiday",
		},
	}

	tests := []struct {
		name string
		cond models.Condition
		want bool
	}{
		{name: "equal hit", cond: models.Condition{Field: "meta:camera", Operator: models.OpEqual, Value: "canon eos r5"}, want: true},
		{name: "equal exact case", cond: models.Condition{Field: "meta:camera", Operator: models.OpEqual, Value: "Canon EOS R5"}, want: true},
		{name: "equal miss", cond: models.Condition{Field: "meta:camera", Operator: models.OpEqual, Value: "Nikon"}, want: false},
		{name: "not equal", cond: models.Condition{Field: "meta:camera", Operator: models.OpNotEqual, Value: "Nikon"}, want: true},
		{name: "contains", cond: models.Condition{Field: "meta:artist", Operator: models.OpContains, Value: "PUNK"}, want: true},
		{name: "contains miss", cond: models.Condition{Field: "meta:artist", Operator: models.OpContains, Value: "Beatles"}, want: false},
		{name: "starts_with", cond: models.Condition{Field: "meta:camera", Operator: models.OpStartsWith, Value: "canon"}, want: true},
		{name: "ends_with", cond: models.Condition{Field: "meta:codec", Operator: models.OpEndsWith, Value: "264"}, want: true},
		{name: "glob", cond: models.Condition{Field: "meta:camera", Operator: models.OpGlob, Value: "Canon*"}, want: true},
		{name: "glob miss", cond: models.Condition{Field: "meta:camera", Operator: models.OpGlob, Value: "Nikon*"}, want: false},
		{name: "regex", cond: models.Condition{Field: "meta:camera", Operator: models.OpRegex, Value: `^Canon EOS R\d$`}, want: true},
		{name: "regex miss", cond: models.Condition{Field: "meta:camera", Operator: models.OpRegex, Value: `^Nikon`}, want: false},
		{name: "empty value equals empty key", cond: models.Condition{Field: "meta:engine", Operator: models.OpEqual, Value: ""}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, reason := evaluateCondition(tt.cond, file)
			assert.Equal(t, tt.want, matched)
			if tt.want {
				assert.Contains(t, reason, "meta:")
			}
		})
	}
}

// TestMetaMissingKeyNeverMatches pinned by the "exists" semantics: a missing
// key reads as the empty string, so != matches and = does not.
func TestMetaMissingKey(t *testing.T) {
	file := models.File{Name: "song.mp3", Metadata: map[string]string{"artist": "Daft Punk"}}

	missing := models.Condition{Field: "meta:album", Operator: models.OpNotEqual, Value: "anything"}
	matched, _ := evaluateCondition(missing, file)
	assert.True(t, matched, "!= must match a file whose metadata lacks the key")

	missingEqual := models.Condition{Field: "meta:album", Operator: models.OpEqual, Value: "anything"}
	matched, _ = evaluateCondition(missingEqual, file)
	assert.False(t, matched, "= must not match a file whose metadata lacks the key")

	absentEqual := models.Condition{Field: "meta:album", Operator: models.OpEqual, Value: ""}
	matched, _ = evaluateCondition(absentEqual, file)
	assert.True(t, matched, "a missing key is the empty string")

	nilMetadata := models.File{Name: "song.mp3"}
	matched, _ = evaluateCondition(models.Condition{Field: "meta:artist", Operator: models.OpContains, Value: "Daft"}, nilMetadata)
	assert.False(t, matched, "a nil Metadata map must not panic and must not match")
}

func TestMetaConditionEndToEnd(t *testing.T) {
	rule, err := ParseRule(`camera contains Canon && meta.comment contains holiday -> Photos/Canon`, 10)
	require.NoError(t, err)
	require.Empty(t, ValidateRule(rule))

	engine := NewEngine([]models.Rule{rule})

	match, reason, ok := engine.Evaluate(models.File{
		Name:      "IMG_0001.CR2",
		Extension: "cr2",
		Category:  "Image",
		Metadata:  map[string]string{"camera": "Canon EOS R5", "comment": "family holiday"},
	})
	require.True(t, ok)
	assert.Equal(t, "Photos/Canon", match.Destination)
	assert.Contains(t, reason, "meta:camera")
	assert.Contains(t, reason, "meta:comment")

	_, _, ok = engine.Evaluate(models.File{Name: "IMG_0002.CR2", Extension: "cr2", Category: "Image"})
	assert.False(t, ok, "file without camera metadata must not match")

	_, _, ok = engine.Evaluate(models.File{
		Name:     "IMG_0003.CR2",
		Metadata: map[string]string{"camera": "Nikon", "comment": "family holiday"},
	})
	assert.False(t, ok, "file with another camera must not match")
}

func TestMetaConditionRoundTripsThroughFormatRule(t *testing.T) {
	for _, ruleStr := range []string{
		"camera=Canon -> Camera",
		"meta.comment=hello -> Commented",
		`artist="Daft Punk" -> Music`,
		"codec=h264 -> H264",
	} {
		t.Run(ruleStr, func(t *testing.T) {
			original, err := ParseRule(ruleStr, 10)
			require.NoError(t, err)

			formatted := FormatRule(original)
			parsed, err := ParseRule(formatted, 0)
			require.NoError(t, err, "formatted rule must re-parse: %q", formatted)

			assert.Equal(t, original.Conditions, parsed.Conditions)
			assert.Equal(t, original.Destination, parsed.Destination)
		})
	}
}

// TestMatchRuleIgnoresUnparseableSize guards the fix for a rule whose helper
// min_size/max_size does not parse: it must match nothing rather than
// everything.
func TestMatchRuleIgnoresUnparseableSize(t *testing.T) {
	file := models.File{Name: "clip.mkv", Extension: "mkv", Category: "video", Size: 10 * 1024 * 1024}

	tests := []struct {
		name string
		rule models.Rule
		want bool
	}{
		{
			name: "invalid min_size never matches",
			rule: models.Rule{Name: "Broken", MinSize: "abc", Destination: "Dest"},
			want: false,
		},
		{
			name: "invalid max_size never matches",
			rule: models.Rule{Name: "Broken", MaxSize: "abc", Destination: "Dest"},
			want: false,
		},
		{
			name: "invalid min_size still blocks matching conditions",
			rule: models.Rule{
				Name:        "Broken",
				MinSize:     "abc",
				Conditions:  []models.Condition{{Field: models.FieldExtension, Operator: models.OpEqual, Value: "mkv"}},
				Destination: "Dest",
			},
			want: false,
		},
		{
			name: "valid min_size still matches",
			rule: models.Rule{Name: "Fine", MinSize: "1MB", Destination: "Dest"},
			want: true,
		},
		{
			name: "valid max_size rejects oversized file",
			rule: models.Rule{Name: "Fine", MaxSize: "1MB", Destination: "Dest"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, _ := matchRule(tt.rule, file)
			assert.Equal(t, tt.want, matched)
		})
	}
}

func TestMatchRuleUnparseableSizeRejectedByValidateRule(t *testing.T) {
	rule := models.Rule{Name: "Broken", MinSize: "abc", Destination: "Dest"}

	require.NotEmpty(t, ValidateRule(rule))
	matched, _ := matchRule(rule, models.File{Name: "anything"})
	assert.False(t, matched, "an invalid rule must not match anything")
}

// TestMetaConditionMatchesScannedFile closes the loop from scan to rule: the
// scanner must hand the engine the extractor's metadata fields, which is what
// makes camera= / artist= work on real files rather than hand-built records.
func TestMetaConditionMatchesScannedFile(t *testing.T) {
	dir := t.TempDir()

	var mp3 []byte
	mp3 = append(mp3, []byte("ID3")...)
	mp3 = append(mp3, []byte{3, 0, 0}...)
	frames := id3TestFrame("TPE1", append([]byte{0x00}, []byte("Daft Punk")...))
	mp3 = append(mp3, []byte{
		byte((len(frames) >> 21) & 0x7f),
		byte((len(frames) >> 14) & 0x7f),
		byte((len(frames) >> 7) & 0x7f),
		byte(len(frames) & 0x7f),
	}...)
	mp3 = append(mp3, frames...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "song.mp3"), mp3, 0644))

	files, err := scanner.New(scanner.ScanOptions{}).Scan(dir)
	require.NoError(t, err)
	require.Len(t, files, 1)

	rule, err := ParseRule(`artist="Daft Punk" -> Music/Daft Punk`, 10)
	require.NoError(t, err)
	require.Empty(t, ValidateRule(rule))

	matched, reason, ok := NewEngine([]models.Rule{rule}).Evaluate(files[0])
	require.True(t, ok)
	assert.Equal(t, "Music/Daft Punk", matched.Destination)
	assert.Contains(t, reason, "meta:artist")
}

func id3TestFrame(id string, payload []byte) []byte {
	out := make([]byte, 10+len(payload))
	copy(out[0:4], id)
	binary.BigEndian.PutUint32(out[4:8], uint32(len(payload)))
	copy(out[10:], payload)
	return out
}
