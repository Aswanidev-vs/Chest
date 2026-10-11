package scheduler

import "testing"

func TestValidateCron(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{"daily at 2am", "0 2 * * *", false},
		{"every 5 minutes", "*/5 * * * *", false},
		{"weekday nights", "0 0 * * 1-5", false},
		{"yearly", "0 0 1 1 *", false},
		{"step over range", "23 0-20/2 * * *", false},
		{"comma list minute", "5,10,15 * * * *", false},
		{"month end", "0 0 31 12 *", false},
		{"sunday as 0", "0 4 * * 0", false},
		{"sunday as 7", "0 4 * * 7", false},
		{"list plus range", "*/30 2 1,15 * 0-6", false},

		{"empty", "", true},
		{"too few fields", "* * * *", true},
		{"too many fields", "* * * * * *", true},
		{"minute high", "60 * * * *", true},
		{"hour high", "* 24 * * *", true},
		{"dom zero", "* * 0 * *", true},
		{"dom high", "* * 32 * *", true},
		{"month zero", "* * * 0 *", true},
		{"month high", "* * * 13 *", true},
		{"dow high", "* * * * 8", true},
		{"range reversed", "5-2 * * * *", true},
		{"malformed range", "1-2-3 * * * *", true},
		{"dangling range", "1- * * * *", true},
		{"double slash", "1//2 * * * *", true},
		{"zero step", "*/0 * * * *", true},
		{"empty list element", "1,,2 * * * *", true},
		{"non numeric", "a * * * *", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCron(tt.expr)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateCron(%q) = nil, want error", tt.expr)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateCron(%q) = %v, want nil", tt.expr, err)
			}
		})
	}
}

func TestDailyHourMinute(t *testing.T) {
	tests := []struct {
		expr   string
		hour   int
		minute int
		ok     bool
	}{
		{"0 2 * * *", 2, 0, true},
		{"30 5 * * *", 5, 30, true},
		{"59 23 * * *", 23, 59, true},
		{"0 2 * * 1", 0, 0, false},
		{"0 2 1 * *", 0, 0, false},
		{"0 2 * 6 *", 0, 0, false},
		{"*/5 * * * *", 0, 0, false},
		{"* 2 * * *", 0, 0, false},
		{"* * * *", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			hour, minute, ok := dailyHourMinute(tt.expr)
			if ok != tt.ok || (ok && (hour != tt.hour || minute != tt.minute)) {
				t.Fatalf("dailyHourMinute(%q) = (%d, %d, %v), want (%d, %d, %v)",
					tt.expr, hour, minute, ok, tt.hour, tt.minute, tt.ok)
			}
		})
	}
}
