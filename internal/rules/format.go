package rules

import (
	"strings"

	"github.com/Aswanidev-vs/chest/internal/models"
)

// FormatRule renders a Rule back into the human-readable rule syntax shown in
// the CLI, e.g. "type=Video && size>1GB -> Videos/Large".
//
// Round trip: for a rule whose Conditions are populated,
// ParseRule(FormatRule(r), 0) yields the same Conditions and Destination, except
// for two limitations inherited from ParseRule. It reads a left-hand side free
// of '=', '>', '<' and '&&' as a bare glob, so a rule whose only clause uses a
// word operator does not survive. And mapField has no 'mod_time' case, so a rule
// using models.FieldModTime renders to text ParseRule rejects outright. Rules
// built from the preset helper fields (Type, Extensions, MinSize, MaxSize) fall
// back to rendering those helpers as clauses, which is display-only and
// deliberately lossy — the rendered string parses, but not back into the helpers.
func FormatRule(r models.Rule) string {
	var clauses []string

	for _, c := range r.Conditions {
		switch c.Operator {
		case models.OpGlob:
			clauses = append(clauses, c.Value)
		case models.OpRegex:
			clauses = append(clauses, string(c.Field)+"=~"+c.Value)
		case models.OpContains, models.OpStartsWith, models.OpEndsWith:
			clauses = append(clauses, string(c.Field)+" "+string(c.Operator)+" "+c.Value)
		default:
			// mapField accepts aliases (ext, filename, modtime, ...) on input, but
			// output always uses the canonical TargetField so one field has one spelling.
			clauses = append(clauses, string(c.Field)+string(c.Operator)+c.Value)
		}
	}

	if len(clauses) == 0 {
		if r.Type != "" {
			clauses = append(clauses, "type="+r.Type)
		}
		for _, e := range r.Extensions {
			// Repeated extension= clauses are AND-ed by matchRule, while the preset
			// helper Extensions is an OR-list, so this rendering is lossy.
			clauses = append(clauses, "extension=."+strings.TrimPrefix(e, "."))
		}
		if r.MinSize != "" {
			clauses = append(clauses, "size>="+r.MinSize)
		}
		if r.MaxSize != "" {
			clauses = append(clauses, "size<="+r.MaxSize)
		}
	}

	if len(clauses) == 0 {
		return r.Destination
	}

	return strings.Join(clauses, " && ") + " -> " + r.Destination
}
