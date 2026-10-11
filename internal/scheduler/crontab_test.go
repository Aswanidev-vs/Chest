package scheduler

import (
	"strings"
	"testing"
)

func TestSplitAndStripManagedBlock(t *testing.T) {
	content := "# keep me\nMAIL TO=root\n" +
		blockStart + "\n" +
		"0 2 * * * /bin/chest sort /a -y  # chest:s1\n" +
		"30 3 * * * /bin/chest sort /b -y  # chest:s2\n" +
		blockEnd + "\n"

	body, outer := splitManagedBlock(content)
	if len(body) != 2 {
		t.Fatalf("body has %d lines, want 2: %q", len(body), body)
	}
	if lineTag(body[0]) != "s1" || lineTag(body[1]) != "s2" {
		t.Fatalf("tags not extracted: %q", body)
	}
	if !strings.Contains(outer, "# keep me") || !strings.Contains(outer, "MAIL TO=root") {
		t.Fatalf("outer lost user lines: %q", outer)
	}

	stripped := stripManagedBlock(content)
	if !strings.Contains(stripped, "# keep me") {
		t.Fatalf("stripManagedBlock dropped user content: %q", stripped)
	}
	if strings.Contains(stripped, blockStart) || strings.Contains(stripped, "chest:s1") {
		t.Fatalf("stripManagedBlock left block behind: %q", stripped)
	}
}

func TestAppendManagedLine(t *testing.T) {
	got := appendManagedLine("", "0 2 * * * /bin/chest sort /a -y  # chest:s1", "s1")
	if !strings.Contains(got, blockStart) || !strings.Contains(got, blockEnd) {
		t.Fatalf("block markers missing: %q", got)
	}

	got = appendManagedLine(got, "30 3 * * * /bin/chest sort /b -y  # chest:s2", "s2")
	if !strings.Contains(got, "chest:s1") || !strings.Contains(got, "chest:s2") {
		t.Fatalf("append dropped an existing entry: %q", got)
	}
	if strings.Count(got, "chest:s1") != 1 {
		t.Fatalf("expected exactly one s1 entry: %q", got)
	}

	// Re-installing the same id must replace, not duplicate.
	got = appendManagedLine(got, "0 5 * * * /bin/chest sort /a2 -y  # chest:s1", "s1")
	if strings.Count(got, "chest:s1") != 1 {
		t.Fatalf("re-install duplicated s1: %q", got)
	}
	if !strings.Contains(got, "sort /a2") {
		t.Fatalf("re-install did not update the line: %q", got)
	}
}

func TestRemoveManagedLine(t *testing.T) {
	content := "# user line\n" + blockStart + "\n" +
		"0 2 * * * /bin/chest sort /a -y  # chest:s1\n" +
		"30 3 * * * /bin/chest sort /b -y  # chest:s2\n" +
		blockEnd + "\n"

	got := removeManagedLine(content, "s1")
	if strings.Contains(got, "chest:s1") {
		t.Fatalf("s1 not removed: %q", got)
	}
	if !strings.Contains(got, "chest:s2") {
		t.Fatalf("s2 wrongly removed: %q", got)
	}
	if !strings.Contains(got, "# user line") {
		t.Fatalf("user line lost: %q", got)
	}

	// Removing the last entry must drop the whole block but keep user lines.
	got = removeManagedLine(got, "s2")
	if strings.Contains(got, blockStart) || strings.Contains(got, "chest:") {
		t.Fatalf("block not removed when empty: %q", got)
	}
	if !strings.Contains(got, "# user line") {
		t.Fatalf("user line lost when block became empty: %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/usr/bin/chest", "/usr/bin/chest"},
		{"/home/me/Downloads", "/home/me/Downloads"},
		{"-y", "-y"},
		{"*.tmp -> Temp", "'*.tmp -> Temp'"},
		{"/home/me/My Downloads", "'/home/me/My Downloads'"},
		{"it's", `'it'\''s'`},
	}
	for _, tt := range tests {
		if got := shellQuote(tt.in); got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCrontabLine(t *testing.T) {
	s := Schedule{ID: "s7", Cron: "0 2 * * *", Directory: "/home/me/Downloads", Preset: "downloads"}

	if got, want := CrontabLine(s, "/usr/bin/chest"), "0 2 * * * /usr/bin/chest sort /home/me/Downloads --preset downloads -y"; got != want {
		t.Errorf("CrontabLine()\n got: %s\nwant: %s", got, want)
	}
	if !strings.Contains(crontabLine(s, "/usr/bin/chest", "s7"), "  # chest:s7") {
		t.Errorf("marker line missing id comment: %q", crontabLine(s, "/usr/bin/chest", "s7"))
	}
}
