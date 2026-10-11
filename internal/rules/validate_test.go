package rules

import (
	"testing"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRule(t *testing.T) {
	tests := []struct {
		name    string
		rule    models.Rule
		wantErr string
	}{
		{
			name: "valid CLI rule",
			rule: models.Rule{
				Name:     "type=video && size>1GB -> Videos/Large",
				Priority: 100,
				Conditions: []models.Condition{
					{Field: models.FieldType, Operator: models.OpEqual, Value: "video"},
					{Field: models.FieldSize, Operator: models.OpGreaterThan, Value: "1GB"},
				},
				Destination: "Videos/Large",
			},
		},
		{
			name: "valid preset helpers",
			rule: models.Rule{
				Name:        "Movies and TV",
				Extensions:  []string{"mp4", "mkv"},
				MinSize:     "500MB",
				Destination: "Movies",
			},
		},
		{
			name:    "invalid min_size",
			rule:    models.Rule{Name: "Broken", MinSize: "abc", Destination: "Dest"},
			wantErr: `invalid min_size "abc"`,
		},
		{
			name:    "invalid max_size",
			rule:    models.Rule{Name: "Broken", MaxSize: "1XB", Destination: "Dest"},
			wantErr: `invalid max_size "1XB"`,
		},
		{
			name: "invalid size value in condition",
			rule: models.Rule{
				Name:        "Broken",
				Conditions:  []models.Condition{{Field: models.FieldSize, Operator: models.OpGreaterThan, Value: "big"}},
				Destination: "Dest",
			},
			wantErr: `invalid size "big"`,
		},
		{
			name: "unsupported field",
			rule: models.Rule{
				Name:        "Broken",
				Conditions:  []models.Condition{{Field: models.TargetField("color"), Operator: models.OpEqual, Value: "red"}},
				Destination: "Dest",
			},
			wantErr: `field "color" is not supported`,
		},
		{
			name: "size operator on non-size field",
			rule: models.Rule{
				Name:        "Broken",
				Conditions:  []models.Condition{{Field: models.FieldName, Operator: models.OpGreaterThan, Value: "5"}},
				Destination: "Dest",
			},
			wantErr: `operator ">" is not supported for field "name"`,
		},
		{
			name: "less-equal on size field",
			rule: models.Rule{
				Name:        "Broken",
				Conditions:  []models.Condition{{Field: models.FieldSize, Operator: models.OpLessEq, Value: "1GB"}},
				Destination: "Dest",
			},
		},
		{
			name: "string operator on size field",
			rule: models.Rule{
				Name:        "Broken",
				Conditions:  []models.Condition{{Field: models.FieldSize, Operator: models.OpContains, Value: "1GB"}},
				Destination: "Dest",
			},
			wantErr: `operator "contains" is not supported for field "size"`,
		},
		{
			name: "glob on type field",
			rule: models.Rule{
				Name:        "Broken",
				Conditions:  []models.Condition{{Field: models.FieldType, Operator: models.OpGlob, Value: "vid*"}},
				Destination: "Dest",
			},
			wantErr: `operator "glob" is not supported for field "type"`,
		},
		{
			name: "regex on date field",
			rule: models.Rule{
				Name:        "Broken",
				Conditions:  []models.Condition{{Field: models.FieldDate, Operator: models.OpRegex, Value: "2024"}},
				Destination: "Dest",
			},
			wantErr: `operator "regex" is not supported for field "date"`,
		},
		{
			name: "invalid regex",
			rule: models.Rule{
				Name:        "Broken",
				Conditions:  []models.Condition{{Field: models.FieldName, Operator: models.OpRegex, Value: "(["}},
				Destination: "Dest",
			},
			wantErr: `invalid regex "([" for field "name"`,
		},
		{
			name: "valid metadata field",
			rule: models.Rule{
				Name:        "Camera",
				Conditions:  []models.Condition{{Field: models.TargetField("meta:camera"), Operator: models.OpEqual, Value: "Canon"}},
				Destination: "Camera",
			},
		},
		{
			name: "unsupported operator on metadata field",
			rule: models.Rule{
				Name:        "Broken",
				Conditions:  []models.Condition{{Field: models.TargetField("meta:camera"), Operator: models.OpLessThan, Value: "5"}},
				Destination: "Dest",
			},
			wantErr: `operator "<" is not supported for field "meta:camera"`,
		},
		{
			name: "empty metadata key",
			rule: models.Rule{
				Name:        "Broken",
				Conditions:  []models.Condition{{Field: models.TargetField(metaPrefix), Operator: models.OpEqual, Value: "x"}},
				Destination: "Dest",
			},
			wantErr: `field "meta:" is not supported`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateRule(tt.rule)
			if tt.wantErr == "" {
				assert.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1)
			assert.Contains(t, errs[0].Error(), tt.wantErr)
		})
	}
}

func TestValidateRuleReportsEveryProblem(t *testing.T) {
	rule := models.Rule{
		Name:        "Broken",
		MinSize:     "abc",
		MaxSize:     "xyz",
		Destination: "Dest",
		Conditions: []models.Condition{
			{Field: models.FieldName, Operator: models.OpGlob, Value: "*.jpg"},
			{Field: models.FieldTakenDate, Operator: models.OpGlob, Value: "2024"},
			{Field: models.TargetField("color"), Operator: models.OpEqual, Value: "red"},
		},
	}

	errs := ValidateRule(rule)
	assert.Len(t, errs, 4)
}

func TestValidateRulesAggregates(t *testing.T) {
	valid, err := ParseRule("type=video && size>1GB -> Videos/Large", 50)
	require.NoError(t, err)
	invalid, err := ParseRule("type=video && size>huge -> Videos", 10)
	require.NoError(t, err)

	assert.Empty(t, ValidateRules([]models.Rule{valid}))

	errs := ValidateRules([]models.Rule{valid, invalid, invalid})
	require.Len(t, errs, 2)
	assert.Contains(t, errs[0].Error(), `invalid size "huge"`)
	assert.Contains(t, errs[1].Error(), `invalid size "huge"`)

	assert.Empty(t, ValidateRules(nil))
}

func TestValidateRuleAcceptsEveryParsedRuleField(t *testing.T) {
	rules := []string{
		"type=video && size>1GB -> Videos/Large",
		"*.mp4 -> Videos",
		"name=~'^IMG_' && taken_date=2024-01 -> Camera/2024",
		"date=2024 && mime contains text -> Docs",
		"format=pdf -> Pdfs",
		"extension=.mkv -> Videos",
		"name starts_with IMG && name ends_with _tmp -> Scratch",
		"modified>=2024-01-01 -> Recent",
		"camera=Canon -> Camera",
		`artist="Daft Punk" -> Music/Daft Punk`,
		"codec=h264 -> Video/h264",
		"meta.comment=hello -> Commented",
		"album contains Live -> Music/Live",
	}

	for _, ruleStr := range rules {
		t.Run(ruleStr, func(t *testing.T) {
			rule, err := ParseRule(ruleStr, 10)
			require.NoError(t, err)
			assert.Empty(t, ValidateRule(rule))
		})
	}
}
