package cli

import (
	"strings"
	"testing"
)

func TestSortDateFlagValidation(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErrPart string
	}{
		{
			name:        "invalid source",
			args:        []string{"--date", "--date-source", "created", "--dry-run"},
			wantErrPart: "invalid date source",
		},
		{
			name:        "invalid granularity",
			args:        []string{"--date", "--date-granularity", "week", "--dry-run"},
			wantErrPart: "invalid date granularity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newSortCmd()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
				t.Fatalf("Execute() error = %v, want error containing %q", err, tt.wantErrPart)
			}
		})
	}
}
