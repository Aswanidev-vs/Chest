// Package dates mines calendar dates out of filenames, so camera and
// screenshot names can drive date-based destinations when embedded metadata
// is missing.
package dates

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var monthNames = map[string]time.Month{
	"jan": time.January,
	"feb": time.February,
	"mar": time.March,
	"apr": time.April,
	"may": time.May,
	"jun": time.June,
	"jul": time.July,
	"aug": time.August,
	"sep": time.September,
	"oct": time.October,
	"nov": time.November,
	"dec": time.December,
}

type datePattern struct {
	re    *regexp.Regexp
	parse func(groups []string) (time.Time, bool)
}

// Separator forms come first so an ambiguous stem prefers the explicit
// spelling; the bare 8-digit run last so "08012025" style IDs are only used
// when no spelled-out date exists.
var patterns = []datePattern{
	{re: regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})`), parse: ymd},
	{re: regexp.MustCompile(`(\d{4})_(\d{2})_(\d{2})`), parse: ymd},
	{re: regexp.MustCompile(`(\d{4})\.(\d{2})\.(\d{2})`), parse: ymd},
	{re: regexp.MustCompile(`(\d{4})-([A-Za-z]{3})-(\d{2})`), parse: yMonthD},
	{re: regexp.MustCompile(`(\d{4})([A-Za-z]{3})(\d{2})`), parse: yMonthD},
	{re: regexp.MustCompile(`(\d{2})-(\d{2})-(\d{4})`), parse: dMY},
	{re: regexp.MustCompile(`([A-Za-z]{3}) (\d{1,2}) (\d{4})`), parse: monthDY},
	{re: regexp.MustCompile(`(\d{1,2}) ([A-Za-z]{3}) (\d{4})`), parse: dMonthY},
	{re: regexp.MustCompile(`(\d{8})`), parse: compact},
}

// FromFilename returns the first date found scanning the filename stem left
// to right. Matches must be bounded by non-digits (or the stem ends) so phone
// numbers and long identifiers cannot produce an 8-digit pseudo-date, and only
// years from 1990 to 2100 with a valid month and day are accepted.
func FromFilename(name string) (time.Time, bool) {
	stem := filenameStem(name)

	var (
		best      time.Time
		bestStart = -1
	)
	for _, pattern := range patterns {
		for _, loc := range pattern.re.FindAllStringSubmatchIndex(stem, -1) {
			start, end := loc[0], loc[1]
			if bestStart != -1 && start >= bestStart {
				continue
			}
			if !boundedByNonDigit(stem, start, end) {
				continue
			}
			var groups []string
			for i := 2; i < len(loc); i += 2 {
				groups = append(groups, stem[loc[i]:loc[i+1]])
			}
			parsed, ok := pattern.parse(groups)
			if !ok {
				continue
			}
			best, bestStart = parsed, start
		}
	}
	return best, bestStart != -1
}

func filenameStem(name string) string {
	base := filepath.Base(filepath.FromSlash(name))
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func boundedByNonDigit(stem string, start, end int) bool {
	if start > 0 && isDigit(stem[start-1]) {
		return false
	}
	return end == len(stem) || !isDigit(stem[end])
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func validDate(year int, month time.Month, day int) (time.Time, bool) {
	if year < 1990 || year > 2100 || month < time.January || month > time.December {
		return time.Time{}, false
	}
	// time.Date normalizes out-of-range days (Feb 30 becomes Mar 2), so the
	// round-trip check rejects them.
	d := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if d.Year() != year || d.Month() != month || d.Day() != day {
		return time.Time{}, false
	}
	return d, true
}

func ymd(g []string) (time.Time, bool) {
	year, _ := strconv.Atoi(g[0])
	month, _ := strconv.Atoi(g[1])
	day, _ := strconv.Atoi(g[2])
	return validDate(year, time.Month(month), day)
}

func compact(g []string) (time.Time, bool) {
	digits := g[0]
	year, _ := strconv.Atoi(digits[:4])
	month, _ := strconv.Atoi(digits[4:6])
	day, _ := strconv.Atoi(digits[6:8])
	return validDate(year, time.Month(month), day)
}

func yMonthD(g []string) (time.Time, bool) {
	year, _ := strconv.Atoi(g[0])
	month, ok := monthNames[strings.ToLower(g[1])]
	if !ok {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(g[2])
	return validDate(year, month, day)
}

func dMY(g []string) (time.Time, bool) {
	day, _ := strconv.Atoi(g[0])
	month, _ := strconv.Atoi(g[1])
	year, _ := strconv.Atoi(g[2])
	return validDate(year, time.Month(month), day)
}

func monthDY(g []string) (time.Time, bool) {
	month, ok := monthNames[strings.ToLower(g[0])]
	if !ok {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(g[1])
	year, _ := strconv.Atoi(g[2])
	return validDate(year, month, day)
}

func dMonthY(g []string) (time.Time, bool) {
	day, _ := strconv.Atoi(g[0])
	month, ok := monthNames[strings.ToLower(g[1])]
	if !ok {
		return time.Time{}, false
	}
	year, _ := strconv.Atoi(g[2])
	return validDate(year, month, day)
}
