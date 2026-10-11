package rules

import (
	"fmt"
	"regexp"

	"github.com/Aswanidev-vs/chest/internal/models"
)

// Operators a field cannot act on are silently dropped by evaluateCondition,
// so the sets below mirror exactly what it implements.
var (
	sizeOperators = []models.Operator{
		models.OpEqual, models.OpNotEqual,
		models.OpGreaterThan, models.OpGreaterEq, models.OpLessThan, models.OpLessEq,
	}
	dateOperators = []models.Operator{
		models.OpEqual, models.OpNotEqual,
		models.OpGreaterThan, models.OpGreaterEq, models.OpLessThan, models.OpLessEq,
	}
	stringOperators = []models.Operator{
		models.OpEqual, models.OpNotEqual, models.OpContains,
		models.OpStartsWith, models.OpEndsWith, models.OpGlob, models.OpRegex,
	}
)

var fieldOperators = map[models.TargetField][]models.Operator{
	models.FieldExtension: {models.OpEqual, models.OpNotEqual, models.OpGlob},
	models.FieldFormat:    {models.OpEqual, models.OpNotEqual, models.OpGlob},
	models.FieldType:      {models.OpEqual, models.OpNotEqual},
	models.FieldSize:      sizeOperators,
	models.FieldName:      stringOperators,
	models.FieldDate:      dateOperators,
	models.FieldModTime:   dateOperators,
	models.FieldTakenDate: dateOperators,
	models.FieldMIME:      {models.OpEqual, models.OpNotEqual, models.OpContains, models.OpStartsWith},
}

// ValidateRule reports every problem that makes a rule evaluate incorrectly:
// unparseable helper sizes, unknown fields, operators a field does not support,
// unparseable size values and regexes that do not compile. A nil result means
// the rule is safe to evaluate.
func ValidateRule(r models.Rule) []error {
	var errs []error

	if r.MinSize != "" {
		if _, err := ParseSize(r.MinSize); err != nil {
			errs = append(errs, fmt.Errorf("rule %q: invalid min_size %q: %v", ruleLabel(r), r.MinSize, err))
		}
	}
	if r.MaxSize != "" {
		if _, err := ParseSize(r.MaxSize); err != nil {
			errs = append(errs, fmt.Errorf("rule %q: invalid max_size %q: %v", ruleLabel(r), r.MaxSize, err))
		}
	}
	for _, cond := range r.Conditions {
		errs = append(errs, validateCondition(ruleLabel(r), cond)...)
	}
	return errs
}

// ValidateRules aggregates the problems of every rule, preserving rule order.
func ValidateRules(rs []models.Rule) []error {
	var errs []error
	for _, r := range rs {
		errs = append(errs, ValidateRule(r)...)
	}
	return errs
}

func validateCondition(label string, cond models.Condition) []error {
	ops, ok := operatorsFor(cond.Field)
	if !ok {
		return []error{fmt.Errorf("rule %s: condition field %q is not supported", label, cond.Field)}
	}

	if !supportsOperator(ops, cond.Operator) {
		return []error{fmt.Errorf("rule %s: operator %q is not supported for field %q", label, cond.Operator, cond.Field)}
	}

	if cond.Operator == models.OpRegex {
		if _, err := regexp.Compile(cond.Value); err != nil {
			return []error{fmt.Errorf("rule %s: invalid regex %q for field %q: %v", label, cond.Value, cond.Field, err)}
		}
	}

	if cond.Field == models.FieldSize {
		if _, err := ParseSize(cond.Value); err != nil {
			return []error{fmt.Errorf("rule %s: invalid size %q: %v", label, cond.Value, err)}
		}
	}

	return nil
}

func operatorsFor(field models.TargetField) ([]models.Operator, bool) {
	if ops, ok := fieldOperators[field]; ok {
		return ops, true
	}
	// Embedded metadata keys are free-form strings, so they take the
	// name-field operators.
	if _, ok := metaFieldKey(field); ok {
		return stringOperators, true
	}
	return nil, false
}

func supportsOperator(ops []models.Operator, op models.Operator) bool {
	for _, candidate := range ops {
		if candidate == op {
			return true
		}
	}
	return false
}

// ruleLabel names the offending rule. It prefers the name, falls back to the
// destination, then to a placeholder, because a rule assembled in code (rather
// than by the CLI or a preset) may carry neither.
func ruleLabel(r models.Rule) string {
	if r.Name != "" {
		return fmt.Sprintf("%q", r.Name)
	}
	if r.Destination != "" {
		return fmt.Sprintf("%q", r.Destination)
	}
	return "unnamed rule"
}
