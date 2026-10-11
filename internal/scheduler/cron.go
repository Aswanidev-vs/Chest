package scheduler

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidateCron checks a standard 5-field cron expression
// (minute hour day-of-month month day-of-week). Each field may be '*', a
// number, a range a-b, a step */n or a-b/n, or a comma-separated list of those.
// It is intentionally dependency-free.
func ValidateCron(expr string) error {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("invalid cron %q: expected 5 fields (minute hour day-of-month month day-of-week), got %d", expr, len(fields))
	}
	specs := []struct {
		name   string
		minErr int
		maxVal int
	}{
		{"minute", 0, 59},
		{"hour", 0, 23},
		{"day-of-month", 1, 31},
		{"month", 1, 12},
		{"day-of-week", 0, 7},
	}
	for i, spec := range specs {
		if err := validateField(fields[i], spec.name, spec.minErr, spec.maxVal); err != nil {
			return err
		}
	}
	return nil
}

func validateField(field, name string, lo, hi int) error {
	if field == "" {
		return fmt.Errorf("invalid cron: %s field is empty", name)
	}
	for _, part := range strings.Split(field, ",") {
		if err := validateFieldPart(part, name, lo, hi); err != nil {
			return err
		}
	}
	return nil
}

func validateFieldPart(part, name string, lo, hi int) error {
	spec := part
	step := ""
	if i := strings.Index(part, "/"); i >= 0 {
		spec, step = part[:i], part[i+1:]
	}
	if spec == "" {
		return fmt.Errorf("invalid cron: %s field %q has no value before the step", name, part)
	}
	if step != "" {
		n, err := strconv.Atoi(step)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid cron: %s field %q has a non-positive step %q", name, part, step)
		}
	}

	if spec == "*" {
		return nil
	}

	if i := strings.Index(spec, "-"); i >= 0 {
		startVal, err1 := strconv.Atoi(spec[:i])
		endVal, err2 := strconv.Atoi(spec[i+1:])
		if err1 != nil || err2 != nil {
			return fmt.Errorf("invalid cron: %s field %q has a malformed range", name, part)
		}
		if startVal < lo || startVal > hi || endVal < lo || endVal > hi {
			return fmt.Errorf("invalid cron: %s field %q range %d-%d is outside %d-%d", name, part, startVal, endVal, lo, hi)
		}
		if startVal > endVal {
			return fmt.Errorf("invalid cron: %s field %q range start %d is greater than end %d", name, part, startVal, endVal)
		}
		return nil
	}

	v, err := strconv.Atoi(spec)
	if err != nil {
		return fmt.Errorf("invalid cron: %s field %q has an invalid value %q", name, part, spec)
	}
	if v < lo || v > hi {
		return fmt.Errorf("invalid cron: %s field %q value %d is outside %d-%d", name, part, v, lo, hi)
	}
	return nil
}

// dailyHourMinute reports whether expr is a plain daily schedule (fields 3-5
// are all '*') and, if so, returns the hour and minute from fields 2 and 1.
// Used to map onto a Windows Task Scheduler DAILY trigger.
func dailyHourMinute(expr string) (hour, minute int, ok bool) {
	f := strings.Fields(expr)
	if len(f) != 5 || f[2] != "*" || f[3] != "*" || f[4] != "*" {
		return 0, 0, false
	}
	min, okMin := simpleInt(f[0])
	hr, okHour := simpleInt(f[1])
	if !okMin || !okHour {
		return 0, 0, false
	}
	if hr < 0 || hr > 23 || min < 0 || min > 59 {
		return 0, 0, false
	}
	return hr, min, true
}

func simpleInt(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}
