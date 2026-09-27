package rules

import (
	"testing"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatRule(t *testing.T) {
	tests := []struct {
		name string
		rule string
		want string
	}{
		{
			name: "doc example with two conditions",
			rule: "type=video && size>1GB -> Videos/Large",
			want: "type=video && size>1GB -> Videos/Large",
		},
		{
			name: "single condition",
			rule: "size>500MB -> Huge",
			want: "size>500MB -> Huge",
		},
		{
			name: "multi condition",
			rule: "name=~'^IMG_' && taken_date=2024-01 -> Camera/2024",
			want: "name=~^IMG_ && taken_date=2024-01 -> Camera/2024",
		},
		{
			name: "bare glob",
			rule: "*.mp4 -> Videos",
			want: "*.mp4 -> Videos",
		},
		{
			name: "word operator alongside a symbolic operator",
			rule: "type=video && name starts_with IMG -> Videos",
			want: "type=video && name starts_with IMG -> Videos",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := ParseRule(tt.rule, 7)
			require.NoError(t, err)

			assert.Equal(t, tt.want, FormatRule(rule))
		})
	}
}

func TestFormatRuleBareDestination(t *testing.T) {
	// These are the rule shapes sort.go builds for --by-format and --by-date:
	// no conditions and no helpers, so the destination is the whole display.
	tests := []struct {
		name string
		rule models.Rule
		want string
	}{
		{"extension placeholder", models.Rule{Destination: "{ext}"}, "{ext}"},
		{"date placeholder", models.Rule{Destination: "{date}"}, "{date}"},
		{"named destination", models.Rule{Destination: "Documents"}, "Documents"},
		{
			name: "empty helpers are not rendered as clauses",
			rule: models.Rule{Destination: "{ext}", Type: "", Extensions: nil, MinSize: "", MaxSize: ""},
			want: "{ext}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatRule(tt.rule))
		})
	}
}

func TestFormatRuleOmitsNameAndPriority(t *testing.T) {
	rule := models.Rule{
		Name:        "Huge Files (>1GB)",
		Priority:    25,
		Destination: "Huge Files",
		Conditions:  []models.Condition{{Field: models.FieldSize, Operator: models.OpGreaterEq, Value: "1GB"}},
	}

	got := FormatRule(rule)
	assert.Equal(t, "size>=1GB -> Huge Files", got)
	assert.NotContains(t, got, "Huge Files (>1GB)")
	assert.NotContains(t, got, "25")
}

func TestFormatRuleRoundTrip(t *testing.T) {
	rules := []string{
		"type=video && size>1GB -> Videos/Large",
		"size>500MB -> Huge",
		"name=~'^IMG_' && taken_date=2024-01 -> Camera/2024",
		"*.mp4 -> Videos",
		"extension=.mkv -> Videos",
		"type=video && name starts_with IMG -> Videos",
		"type=video && name ends_with _tmp -> Scratch",
		"date=2024 && mime contains text -> Docs",
		"size>=1GB && size<=10GB -> Sized",
		"date!=2024-01 -> Newer",
		"type=image && type!=video && size>10MB -> Photos",
	}

	for _, ruleStr := range rules {
		t.Run(ruleStr, func(t *testing.T) {
			original, err := ParseRule(ruleStr, 100)
			require.NoError(t, err)
			require.NotEmpty(t, original.Conditions)

			formatted := FormatRule(original)
			parsed, err := ParseRule(formatted, 0)
			require.NoError(t, err, "formatted rule must re-parse: %q", formatted)

			assert.Equal(t, original.Conditions, parsed.Conditions)
			assert.Equal(t, original.Destination, parsed.Destination)
		})
	}
}

func TestFormatRuleHelperFields(t *testing.T) {
	tests := []struct {
		name string
		rule models.Rule
		want string
	}{
		{
			name: "type only",
			rule: models.Rule{Type: "video", Destination: "Videos"},
			want: "type=video -> Videos",
		},
		{
			name: "extensions only",
			rule: models.Rule{Extensions: []string{"mp4", "mkv"}, Destination: "Videos"},
			want: "extension=.mp4 && extension=.mkv -> Videos",
		},
		{
			name: "extensions already dotted are not doubled",
			rule: models.Rule{Extensions: []string{".mp4", ".mkv"}, Destination: "Videos"},
			want: "extension=.mp4 && extension=.mkv -> Videos",
		},
		{
			name: "min size only",
			rule: models.Rule{MinSize: "1GB", Destination: "Huge Files"},
			want: "size>=1GB -> Huge Files",
		},
		{
			name: "max size only",
			rule: models.Rule{MaxSize: "1MB", Destination: "Tiny Files"},
			want: "size<=1MB -> Tiny Files",
		},
		{
			name: "size range",
			rule: models.Rule{MinSize: "100MB", MaxSize: "1GB", Destination: "Large Files"},
			want: "size>=100MB && size<=1GB -> Large Files",
		},
		{
			name: "all helpers",
			rule: models.Rule{
				Type:        "video",
				Extensions:  []string{"mp4", ".mkv"},
				MinSize:     "1GB",
				MaxSize:     "10GB",
				Destination: "Videos/Large",
			},
			want: "type=video && extension=.mp4 && extension=.mkv && size>=1GB && size<=10GB -> Videos/Large",
		},
		{
			name: "conditions take precedence over helpers",
			rule: models.Rule{
				Conditions:  []models.Condition{{Field: models.FieldName, Operator: models.OpGlob, Value: "*.pdf"}},
				Type:        "video",
				MinSize:     "1GB",
				Destination: "Docs",
			},
			want: "*.pdf -> Docs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatRule(tt.rule))
		})
	}
}

func TestFormatRuleHelperFieldsAreLossy(t *testing.T) {
	// The preset helper Extensions is an OR-list; rendering it produces AND-ed
	// extension= clauses, so re-parsing is a display convenience, not a round trip.
	original := models.Rule{
		Extensions:  []string{"mp4", "mkv"},
		Destination: "Videos",
	}

	parsed, err := ParseRule(FormatRule(original), 0)
	require.NoError(t, err)
	assert.Empty(t, parsed.Extensions)
	assert.Equal(t, "Videos", parsed.Destination)
}

func TestFormatRuleOperators(t *testing.T) {
	tests := []struct {
		name string
		cond models.Condition
		want string
	}{
		{"equal", models.Condition{Field: models.FieldType, Operator: models.OpEqual, Value: "video"}, "type=video -> Dest"},
		{"not equal", models.Condition{Field: models.FieldType, Operator: models.OpNotEqual, Value: "video"}, "type!=video -> Dest"},
		{"greater than", models.Condition{Field: models.FieldSize, Operator: models.OpGreaterThan, Value: "1GB"}, "size>1GB -> Dest"},
		{"less than", models.Condition{Field: models.FieldSize, Operator: models.OpLessThan, Value: "1GB"}, "size<1GB -> Dest"},
		{"greater or equal", models.Condition{Field: models.FieldSize, Operator: models.OpGreaterEq, Value: "1GB"}, "size>=1GB -> Dest"},
		{"less or equal", models.Condition{Field: models.FieldSize, Operator: models.OpLessEq, Value: "1GB"}, "size<=1GB -> Dest"},
		{"contains", models.Condition{Field: models.FieldName, Operator: models.OpContains, Value: "report"}, "name contains report -> Dest"},
		{"starts with", models.Condition{Field: models.FieldName, Operator: models.OpStartsWith, Value: "IMG"}, "name starts_with IMG -> Dest"},
		{"ends with", models.Condition{Field: models.FieldName, Operator: models.OpEndsWith, Value: "_tmp"}, "name ends_with _tmp -> Dest"},
		{"glob", models.Condition{Field: models.FieldName, Operator: models.OpGlob, Value: "*.mp4"}, "*.mp4 -> Dest"},
		{"regex", models.Condition{Field: models.FieldName, Operator: models.OpRegex, Value: "^IMG_"}, "name=~^IMG_ -> Dest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatRule(models.Rule{
				Conditions:  []models.Condition{tt.cond},
				Destination: "Dest",
			})
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatRuleCanonicalFieldNames(t *testing.T) {
	fields := []models.TargetField{
		models.FieldExtension,
		models.FieldFormat,
		models.FieldType,
		models.FieldSize,
		models.FieldName,
		models.FieldDate,
		models.FieldTakenDate,
		models.FieldMIME,
	}

	for _, field := range fields {
		t.Run(string(field), func(t *testing.T) {
			got := FormatRule(models.Rule{
				Conditions:  []models.Condition{{Field: field, Operator: models.OpEqual, Value: "v"}},
				Destination: "Dest",
			})
			assert.Equal(t, string(field)+"=v -> Dest", got)
		})
	}
}

func TestFormatRuleEdgeCaseValues(t *testing.T) {
	tests := []struct {
		name string
		rule models.Rule
		want string
	}{
		{
			name: "empty value",
			rule: models.Rule{
				Conditions:  []models.Condition{{Field: models.FieldName, Operator: models.OpEqual, Value: ""}},
				Destination: "Unnamed",
			},
			want: "name= -> Unnamed",
		},
		{
			name: "value with spaces",
			rule: models.Rule{
				Conditions:  []models.Condition{{Field: models.FieldName, Operator: models.OpContains, Value: "my file"}},
				Destination: "Mine",
			},
			want: "name contains my file -> Mine",
		},
		{
			name: "empty rule",
			rule: models.Rule{},
			want: "",
		},
		{
			name: "nil conditions with empty destination",
			rule: models.Rule{Conditions: nil},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				assert.Equal(t, tt.want, FormatRule(tt.rule))
			})
		})
	}
}

func TestFormatRuleSoleWordOperatorIsReparsedAsGlob(t *testing.T) {
	// ParseRule treats a left-hand side free of '=', '>', '<' and '&&' as a bare
	// glob pattern, so a rule whose only clause uses a word operator cannot be
	// reconstructed from the rendered string. Pinned here because it is a parser
	// limitation, not a rendering one: the same clause round-trips as soon as any
	// sibling clause carries a symbolic operator (see TestFormatRuleRoundTrip).
	original := models.Rule{
		Conditions:  []models.Condition{{Field: models.FieldName, Operator: models.OpContains, Value: "my file"}},
		Destination: "Mine",
	}

	formatted := FormatRule(original)
	assert.Equal(t, "name contains my file -> Mine", formatted)

	parsed, err := ParseRule(formatted, 0)
	require.NoError(t, err)
	assert.Equal(t, models.Condition{Field: models.FieldName, Operator: models.OpGlob, Value: "name contains my file"}, parsed.Conditions[0])
	assert.Equal(t, original.Destination, parsed.Destination)
}

func TestFormatRuleEdgeCaseValuesReParse(t *testing.T) {
	empty := models.Rule{
		Conditions:  []models.Condition{{Field: models.FieldName, Operator: models.OpEqual, Value: ""}},
		Destination: "Unnamed",
	}
	formatted := FormatRule(empty)
	assert.Equal(t, "name= -> Unnamed", formatted)

	parsed, err := ParseRule(formatted, 0)
	require.NoError(t, err)
	assert.Equal(t, empty.Conditions, parsed.Conditions)
	assert.Equal(t, empty.Destination, parsed.Destination)
}
