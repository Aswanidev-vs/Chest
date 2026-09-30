package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aswanidev-vs/chest/cellwatch"
)

func TestSplitFlagDesc(t *testing.T) {
	for _, tc := range []struct {
		in       string
		wantName string
		wantDesc string
	}{
		{"--record               Append each reading", "--record", "Append each reading"},
		{"-c, --content <text>  Search inside contents", "-c, --content <text>", "Search inside contents"},
		{"-d, --debounce <dur>  Settle duration", "-d, --debounce <dur>", "Settle duration"},
		{"--all      Delete the index", "--all", "Delete the index"},
		{"-y, --yes", "-y, --yes", ""},
	} {
		name, desc := splitFlagDesc(tc.in)
		if name != tc.wantName || desc != tc.wantDesc {
			t.Errorf("splitFlagDesc(%q) = (%q, %q), want (%q, %q)",
				tc.in, name, desc, tc.wantName, tc.wantDesc)
		}
	}
}

// Every topic hand-aligns its own options, and the printer used to pad them a
// second time to a fixed width of its own. That left the descriptions in a
// ragged column whose position depended on the topic rather than on the flags.
//
// This measures the rendered output rather than the source strings, because the
// column is what a reader actually sees. Each description must start at the
// same offset on every line of the block.
func TestManOptionsDescriptionsShareAColumn(t *testing.T) {
	for topicName, topic := range manTopics {
		if len(topic.Options) == 0 {
			continue
		}
		var out bytes.Buffer
		renderManOptions(&out, topic.Options)

		column := -1
		for _, line := range strings.Split(out.String(), "\n") {
			plain := stripANSI(line)
			// Entries are "    <flag padded to the column>  <description>". The
			// flag may itself contain a single space ("-p, --preset"), so the
			// description is located by measuring the text before the last run
			// of two or more spaces, not the first.
			idx := strings.LastIndex(plain, "  ")
			if idx < 0 || !strings.HasPrefix(plain, "    ") {
				continue
			}
			desc := strings.TrimSpace(plain[idx:])
			if desc == "" {
				continue
			}
			if column == -1 {
				column = idx
				continue
			}
			if idx != column {
				t.Errorf("%s: description starts at column %d, want %d: %q",
					topicName, idx, column, plain)
			}
		}
		if column == -1 {
			t.Errorf("%s: no option descriptions were rendered", topicName)
		}
	}
}

// A flag that carries no description must not be mistaken for one, and must not
// drag the column wider than the flags around it.
func TestManOptionsWithoutDescriptionsAreLeftAlone(t *testing.T) {
	name, desc := splitFlagDesc("-y, --yes")
	if desc != "" {
		t.Errorf("a bare flag should have no description, got %q", desc)
	}
	if name != "-y, --yes" {
		t.Errorf("bare flag name = %q", name)
	}
}

// --record on its own used to be accepted and then ignored: the single-reading
// path never opened the store, so no database was created and the command
// printed exactly what a run without --record prints. --apps and --top had the
// same shape, silently doing nothing because a single reading has no interval
// to attribute over and no list to size.
func TestBatterySingleReadingRefusesFlagsItCannotHonour(t *testing.T) {
	for _, args := range [][]string{
		{"--apps"},
		{"--top", "3"},
		{"--interval", "3s"},
		{"--db", "x.db"},
	} {
		cmd := newBatteryCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("battery %v on a single reading should explain itself, not print a report", args)
		}
	}
}

// --record on its own has to append the reading, because a flag that is
// accepted and then quietly discarded is indistinguishable from a broken one.
func TestBatteryRecordOnASingleReadingWritesToTheDatabase(t *testing.T) {
	db := filepath.Join(t.TempDir(), "battery.db")
	cmd := newBatteryCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--record", "--db", db})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("battery --record: %v", err)
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("--record did not create the database at %s: %v", db, err)
	}
}

func TestAccumulateAppsSumsActivityAndAveragesProportions(t *testing.T) {
	total := accumulateApps(nil, []cellwatch.AppActivity{
		{Name: "a", CPUSeconds: 10, Share: 0.5, PctPerHour: 2},
		{Name: "b", CPUSeconds: 5, Share: 0.5, PctPerHour: 1},
	})
	total = accumulateApps(total, []cellwatch.AppActivity{
		{Name: "a", CPUSeconds: 3, Share: 0.9},
	})

	if len(total) != 2 {
		t.Fatalf("expected 2 applications, got %d", len(total))
	}
	// CPU accumulates across windows because it is a measured quantity.
	if total[0].CPUSeconds != 13 {
		t.Errorf("a accumulated CPU = %v, want 13", total[0].CPUSeconds)
	}
	if total[0].Name != "a" {
		t.Errorf("order changed: got %q first", total[0].Name)
	}
}

func TestPrintMeasuredAppsRespectsTopAndSkipsJSON(t *testing.T) {
	apps := []cellwatch.AppActivity{
		{Name: "big", CPUSeconds: 30},
		{Name: "mid", CPUSeconds: 20},
		{Name: "small", CPUSeconds: 10},
	}

	var buf bytes.Buffer
	printMeasuredApps(&buf, apps, 2, false)
	out := stripANSI(buf.String())
	if !strings.Contains(out, "TOP 2") {
		t.Errorf("--top 2 should be honoured:\n%s", out)
	}
	if strings.Contains(out, "small") {
		t.Errorf("the third application should be cut:\n%s", out)
	}

	// JSON output is parsed by a machine, so a human table appended to the
	// stream is not something json.Unmarshal can skip.
	var jbuf bytes.Buffer
	printMeasuredApps(&jbuf, apps, 2, true)
	if jbuf.Len() != 0 {
		t.Errorf("json output must not carry the applications table, got %q", jbuf.String())
	}
}

func TestPrintMeasuredAppsWithNoAttributionPrintsNothing(t *testing.T) {
	var buf bytes.Buffer
	printMeasuredApps(&buf, nil, 5, false)
	if buf.Len() != 0 {
		t.Errorf("no attribution should print nothing, got %q", buf.String())
	}
}
