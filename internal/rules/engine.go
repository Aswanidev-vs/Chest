package rules

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aswanidev-vs/chest/internal/models"
)

// Engine evaluates rules against files
type Engine struct {
	rules []models.Rule
}

// NewEngine creates a new Rule Engine
func NewEngine(rules []models.Rule) *Engine {
	// Sort rules descending by priority
	sorted := make([]models.Rule, len(rules))
	copy(sorted, rules)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})
	return &Engine{rules: sorted}
}

// Evaluate finds the highest priority matching rule for a file
func (e *Engine) Evaluate(file models.File) (*models.Rule, string, bool) {
	for _, rule := range e.rules {
		if matched, reason := matchRule(rule, file); matched {
			return &rule, reason, true
		}
	}
	return nil, "", false
}

func matchRule(rule models.Rule, file models.File) (bool, string) {
	var reasons []string

	// Check helper extensions
	if len(rule.Extensions) > 0 {
		matched := false
		for _, ext := range rule.Extensions {
			if strings.EqualFold(file.Extension, strings.TrimPrefix(ext, ".")) {
				matched = true
				break
			}
		}
		if !matched {
			return false, ""
		}
		reasons = append(reasons, fmt.Sprintf("extension = %s", file.Extension))
	}

	// Check helper Type
	if rule.Type != "" {
		if !strings.EqualFold(file.Category, rule.Type) {
			return false, ""
		}
		reasons = append(reasons, fmt.Sprintf("type = %s", file.Category))
	}

	// Check helper MinSize
	if rule.MinSize != "" {
		minBytes, err := ParseSize(rule.MinSize)
		if err == nil {
			if file.Size < minBytes {
				return false, ""
			}
			reasons = append(reasons, fmt.Sprintf("size >= %s", rule.MinSize))
		}
	}

	// Check helper MaxSize
	if rule.MaxSize != "" {
		maxBytes, err := ParseSize(rule.MaxSize)
		if err == nil {
			if file.Size > maxBytes {
				return false, ""
			}
			reasons = append(reasons, fmt.Sprintf("size <= %s", rule.MaxSize))
		}
	}

	// Check explicit conditions
	for _, cond := range rule.Conditions {
		matched, reason := evaluateCondition(cond, file)
		if !matched {
			return false, ""
		}
		reasons = append(reasons, reason)
	}

	if len(reasons) == 0 {
		reasons = append(reasons, rule.Name)
	}

	return true, strings.Join(reasons, ", ")
}

func evaluateCondition(cond models.Condition, file models.File) (bool, string) {
	switch cond.Field {
	case models.FieldExtension, models.FieldFormat:
		val := strings.ToLower(strings.TrimPrefix(cond.Value, "."))
		subject := file.Extension
		label := "extension"
		if cond.Field == models.FieldFormat {
			subject = file.Format
			label = "format"
		}
		matched := false
		switch cond.Operator {
		case models.OpEqual:
			matched = strings.EqualFold(subject, val)
		case models.OpNotEqual:
			matched = !strings.EqualFold(subject, val)
		case models.OpGlob:
			m, _ := filepath.Match(val, subject)
			matched = m
		}
		return matched, fmt.Sprintf("%s %s %s", label, cond.Operator, cond.Value)

	case models.FieldType:
		matched := false
		switch cond.Operator {
		case models.OpEqual:
			matched = strings.EqualFold(file.Category, cond.Value)
		case models.OpNotEqual:
			matched = !strings.EqualFold(file.Category, cond.Value)
		}
		return matched, fmt.Sprintf("type %s %s", cond.Operator, cond.Value)

	case models.FieldSize:
		bytesVal, err := ParseSize(cond.Value)
		if err != nil {
			return false, ""
		}
		matched := false
		switch cond.Operator {
		case models.OpGreaterThan:
			matched = file.Size > bytesVal
		case models.OpGreaterEq:
			matched = file.Size >= bytesVal
		case models.OpLessThan:
			matched = file.Size < bytesVal
		case models.OpLessEq:
			matched = file.Size <= bytesVal
		case models.OpEqual:
			matched = file.Size == bytesVal
		case models.OpNotEqual:
			matched = file.Size != bytesVal
		}
		return matched, fmt.Sprintf("size %s %s", cond.Operator, cond.Value)

	case models.FieldName:
		matched := false
		switch cond.Operator {
		case models.OpEqual:
			matched = strings.EqualFold(file.Name, cond.Value)
		case models.OpContains:
			matched = strings.Contains(strings.ToLower(file.Name), strings.ToLower(cond.Value))
		case models.OpStartsWith:
			matched = strings.HasPrefix(strings.ToLower(file.Name), strings.ToLower(cond.Value))
		case models.OpEndsWith:
			matched = strings.HasSuffix(strings.ToLower(file.Name), strings.ToLower(cond.Value))
		case models.OpGlob:
			m, _ := filepath.Match(strings.ToLower(cond.Value), strings.ToLower(file.Name))
			matched = m
		case models.OpRegex:
			r, err := regexp.Compile(cond.Value)
			if err == nil {
				matched = r.MatchString(file.Name)
			}
		}
		return matched, fmt.Sprintf("name %s %s", cond.Operator, cond.Value)

	case models.FieldDate, models.FieldModTime:
		return evaluateDateCondition(cond, file.ModTime)

	case models.FieldTakenDate:
		if file.TakenDate == nil {
			return false, ""
		}
		return evaluateDateCondition(cond, *file.TakenDate)

	case models.FieldMIME:
		matched := false
		switch cond.Operator {
		case models.OpEqual:
			matched = strings.EqualFold(file.MIMEType, cond.Value)
		case models.OpNotEqual:
			matched = !strings.EqualFold(file.MIMEType, cond.Value)
		case models.OpContains:
			matched = strings.Contains(strings.ToLower(file.MIMEType), strings.ToLower(cond.Value))
		case models.OpStartsWith:
			matched = strings.HasPrefix(strings.ToLower(file.MIMEType), strings.ToLower(cond.Value))
		}
		return matched, fmt.Sprintf("mime %s %s", cond.Operator, cond.Value)
	}

	return false, ""
}

func evaluateDateCondition(cond models.Condition, fileDate time.Time) (bool, string) {
	start, end, ok := parseDateRange(cond.Value)
	if !ok {
		return false, ""
	}

	matched := false
	switch cond.Operator {
	case models.OpEqual:
		if end != nil {
			matched = !fileDate.Before(start) && fileDate.Before(*end)
		} else {
			matched = fileDate.Equal(start)
		}
	case models.OpNotEqual:
		if end != nil {
			matched = fileDate.Before(start) || !fileDate.Before(*end)
		} else {
			matched = !fileDate.Equal(start)
		}
	case models.OpGreaterThan:
		if end != nil {
			matched = !fileDate.Before(*end)
		} else {
			matched = fileDate.After(start)
		}
	case models.OpGreaterEq:
		matched = !fileDate.Before(start)
	case models.OpLessThan:
		matched = fileDate.Before(start)
	case models.OpLessEq:
		if end != nil {
			matched = fileDate.Before(*end)
		} else {
			matched = fileDate.Before(start) || fileDate.Equal(start)
		}
	}
	return matched, fmt.Sprintf("date %s %s", cond.Operator, cond.Value)
}

func parseDateRange(value string) (time.Time, *time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil, false
	}

	if full, err := time.Parse(time.RFC3339, value); err == nil {
		return full, nil, true
	}

	layouts := []string{"2006", "2006-01", "2006-01-02"}
	for _, layout := range layouts {
		start, err := time.ParseInLocation(layout, value, time.Local)
		if err != nil {
			continue
		}
		end := start
		switch layout {
		case "2006":
			end = start.AddDate(1, 0, 0)
		case "2006-01":
			end = start.AddDate(0, 1, 0)
		default:
			end = start.AddDate(0, 0, 1)
		}
		return start, &end, true
	}

	return time.Time{}, nil, false
}

// ParseRule parses CLI rule string e.g.:
// "type=video && size>1GB -> Videos/Large"
// "name=~'^IMG_' -> Camera"
// "*.mp4 -> Videos"
// "size>500MB -> Huge"
func ParseRule(ruleStr string, priority int) (models.Rule, error) {
	parts := strings.Split(ruleStr, "->")
	if len(parts) != 2 {
		return models.Rule{}, fmt.Errorf("invalid rule syntax, missing '->': %s", ruleStr)
	}

	left := strings.TrimSpace(parts[0])
	dest := strings.TrimSpace(parts[1])
	if dest == "" {
		return models.Rule{}, fmt.Errorf("destination cannot be empty: %s", ruleStr)
	}

	rule := models.Rule{
		Name:        ruleStr,
		Priority:    priority,
		Destination: dest,
	}

	// Simple pattern shorthand like "*.mp4" or "sample.pdf"
	if !strings.Contains(left, "=") && !strings.Contains(left, ">") && !strings.Contains(left, "<") && !strings.Contains(left, "&&") {
		// Pattern glob
		rule.Conditions = append(rule.Conditions, models.Condition{
			Field:    models.FieldName,
			Operator: models.OpGlob,
			Value:    left,
		})
		return rule, nil
	}

	condClauses := strings.Split(left, "&&")
	for _, clause := range condClauses {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}

		cond, err := parseClause(clause)
		if err != nil {
			return models.Rule{}, err
		}
		rule.Conditions = append(rule.Conditions, cond)
	}

	return rule, nil
}

func parseClause(clause string) (models.Condition, error) {
	// Handle =~ for regex first
	if idx := strings.Index(clause, "=~"); idx != -1 {
		fieldStr := strings.TrimSpace(clause[:idx])
		valStr := strings.Trim(strings.TrimSpace(clause[idx+2:]), "\"'")
		field, err := mapField(fieldStr)
		if err != nil {
			return models.Condition{}, err
		}
		return models.Condition{
			Field:    field,
			Operator: models.OpRegex,
			Value:    valStr,
		}, nil
	}

	operators := []models.Operator{
		models.OpGreaterEq,
		models.OpLessEq,
		models.OpNotEqual,
		models.OpGreaterThan,
		models.OpLessThan,
		models.OpEqual,
		models.OpContains,
		models.OpStartsWith,
		models.OpEndsWith,
	}

	for _, op := range operators {
		opStr := string(op)
		idx := strings.Index(clause, opStr)
		if idx != -1 {
			fieldStr := strings.TrimSpace(clause[:idx])
			valStr := strings.TrimSpace(clause[idx+len(opStr):])
			valStr = strings.Trim(valStr, "\"'")

			field, err := mapField(fieldStr)
			if err != nil {
				return models.Condition{}, err
			}

			return models.Condition{
				Field:    field,
				Operator: op,
				Value:    valStr,
			}, nil
		}
	}

	return models.Condition{}, fmt.Errorf("unable to parse condition: %s", clause)
}

func mapField(fieldStr string) (models.TargetField, error) {
	switch strings.ToLower(fieldStr) {
	case "type":
		return models.FieldType, nil
	case "ext", "extension":
		return models.FieldExtension, nil
	case "format":
		return models.FieldFormat, nil
	case "size":
		return models.FieldSize, nil
	case "name", "filename":
		return models.FieldName, nil
	case "date", "modified", "modtime":
		return models.FieldDate, nil
	case "taken_date", "takendate":
		return models.FieldTakenDate, nil
	case "mime", "mimetype":
		return models.FieldMIME, nil
	default:
		return "", fmt.Errorf("unknown field '%s'", fieldStr)
	}
}

// ParseSize parses human readable byte sizes: "500KB", "1GB", "2.5MB", "1024"
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}

	multiplier := int64(1)
	cleanStr := s

	if strings.HasSuffix(s, "TB") {
		multiplier = 1024 * 1024 * 1024 * 1024
		cleanStr = strings.TrimSuffix(s, "TB")
	} else if strings.HasSuffix(s, "GB") {
		multiplier = 1024 * 1024 * 1024
		cleanStr = strings.TrimSuffix(s, "GB")
	} else if strings.HasSuffix(s, "MB") {
		multiplier = 1024 * 1024
		cleanStr = strings.TrimSuffix(s, "MB")
	} else if strings.HasSuffix(s, "KB") {
		multiplier = 1024
		cleanStr = strings.TrimSuffix(s, "KB")
	} else if strings.HasSuffix(s, "B") {
		multiplier = 1
		cleanStr = strings.TrimSuffix(s, "B")
	}

	cleanStr = strings.TrimSpace(cleanStr)
	val, err := strconv.ParseFloat(cleanStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size format: %s", s)
	}

	return int64(val * float64(multiplier)), nil
}
