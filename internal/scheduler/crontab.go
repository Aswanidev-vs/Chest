package scheduler

import "strings"

const (
	blockStart = "# >>> chest >>>"
	blockEnd   = "# <<< chest <<<"
	markerTag  = "# chest:"
)

// CrontabLine renders a schedule as a single crontab line (no ownership
// marker), e.g. for chest schedule add --print-cron.
func CrontabLine(s Schedule, binPath string) string {
	return crontabLine(s, binPath, "")
}

// crontabLine renders the crontab entry. When marker is non-empty it is
// appended as a trailing shell comment carrying the schedule id, which lets
// uninstall remove exactly one chest entry without disturbing the others.
func crontabLine(s Schedule, binPath, marker string) string {
	parts := []string{shellQuote(binPath)}
	for _, a := range s.Args() {
		parts = append(parts, shellQuote(a))
	}
	line := s.Cron + " " + strings.Join(parts, " ")
	if marker != "" {
		line += "  " + markerTag + marker
	}
	return line
}

// shellQuote leaves a token untouched when it contains no shell metacharacters
// (so a simple "0 2 * * * /usr/bin/chest sort /home/u -y" reads cleanly) and
// otherwise wraps it in single quotes, escaping embedded single quotes with the
// standard close-escape-reopen idiom so the value survives the shell intact.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"\\$`*?()[]{}|&;<>#~!\n") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// lineTag extracts the schedule id from a managed crontab line, or "" if the
// line carries no chest marker.
func lineTag(line string) string {
	i := strings.Index(line, markerTag)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(line[i+len(markerTag):])
}

// splitManagedBlock separates a crontab dump into the body of its chest-managed
// block (the lines between the markers, excluding the markers) and the rest of
// the crontab with that block removed.
func splitManagedBlock(content string) (body []string, outer string) {
	var kept []string
	inBlock := false
	for _, ln := range strings.Split(content, "\n") {
		switch strings.TrimSpace(ln) {
		case blockStart:
			inBlock = true
		case blockEnd:
			inBlock = false
		default:
			if !inBlock {
				kept = append(kept, ln)
			} else if strings.TrimSpace(ln) != "" {
				body = append(body, ln)
			}
		}
	}
	return body, strings.Join(kept, "\n")
}

// joinManagedBlock rebuilds a crontab dump: the outer lines followed by a
// freshly delimited block containing lines (the block is omitted when empty).
func joinManagedBlock(outer string, lines []string) string {
	var b strings.Builder
	base := strings.TrimRight(outer, "\n")
	if base != "" {
		b.WriteString(base)
		b.WriteByte('\n')
	}
	if len(lines) > 0 {
		b.WriteString(blockStart + "\n")
		for _, ln := range lines {
			b.WriteString(ln + "\n")
		}
		b.WriteString(blockEnd + "\n")
	}
	return b.String()
}

// appendManagedLine adds line to the managed block, first dropping any stale
// entry that carries the same id so a re-install is idempotent rather than a
// duplicate.
func appendManagedLine(content, line, id string) string {
	body, outer := splitManagedBlock(content)
	kept := make([]string, 0, len(body)+1)
	for _, b := range body {
		if lineTag(b) != id {
			kept = append(kept, b)
		}
	}
	return joinManagedBlock(outer, append(kept, line))
}

// removeManagedLine drops the managed entry tagged id, leaving other entries
// untouched.
func removeManagedLine(content, id string) string {
	body, outer := splitManagedBlock(content)
	kept := make([]string, 0, len(body))
	for _, b := range body {
		if lineTag(b) != id {
			kept = append(kept, b)
		}
	}
	return joinManagedBlock(outer, kept)
}

// stripManagedBlock is exported for tests/debugging: it returns content with
// the entire chest-managed block (and its markers) removed.
func stripManagedBlock(content string) string {
	_, outer := splitManagedBlock(content)
	base := strings.TrimRight(outer, "\n")
	if base == "" {
		return ""
	}
	return base + "\n"
}
