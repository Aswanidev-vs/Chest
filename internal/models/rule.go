package models

// TargetField defines what attribute of the file the condition inspects.
type TargetField string

const (
	FieldExtension TargetField = "extension"
	FieldFormat    TargetField = "format"
	FieldType      TargetField = "type"
	FieldSize      TargetField = "size"
	FieldName      TargetField = "name"
	FieldDate      TargetField = "date"
	FieldModTime   TargetField = "mod_time"
)

// Operator defines comparison operator.
type Operator string

const (
	OpEqual       Operator = "="
	OpNotEqual    Operator = "!="
	OpGreaterThan Operator = ">"
	OpLessThan    Operator = "<"
	OpGreaterEq   Operator = ">="
	OpLessEq      Operator = "<="
	OpContains    Operator = "contains"
	OpStartsWith  Operator = "starts_with"
	OpEndsWith    Operator = "ends_with"
	OpGlob        Operator = "glob"
	OpRegex       Operator = "regex"
)

// Condition defines an evaluation rule for a file.
type Condition struct {
	Field    TargetField `json:"field" toml:"field"`
	Operator Operator    `json:"operator" toml:"operator"`
	Value    string      `json:"value" toml:"value"`
}

// Rule defines sorting rule.
type Rule struct {
	Name        string      `json:"name" toml:"name"`
	Priority    int         `json:"priority" toml:"priority"`
	Conditions  []Condition `json:"conditions" toml:"conditions"`
	Destination string      `json:"destination" toml:"destination"`
	Extensions  []string    `json:"extensions,omitempty" toml:"extensions,omitempty"` // convenience helper in presets
	MinSize     string      `json:"min_size,omitempty" toml:"min_size,omitempty"`     // convenience helper in presets
	MaxSize     string      `json:"max_size,omitempty" toml:"max_size,omitempty"`     // convenience helper in presets
	Type        string      `json:"type,omitempty" toml:"type,omitempty"`             // convenience helper in presets
}
