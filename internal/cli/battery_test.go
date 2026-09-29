package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/cellwatch"
)

// A reading shaped like the one a real Windows laptop produced during
// development: charge known, no wattage, no capacity, time remaining present.
func windowsLike() cellwatch.Status {
	return cellwatch.Status{
		Present:          true,
		Count:            1,
		Source:           "GetSystemPowerStatus",
		AC:               cellwatch.ACOffline,
		Status:           "Normal",
		ChargePct:        39,
		ChargeKnown:      true,
		TimeToEmptyS:     2400,
		TimeToEmptyKnown: true,
		Cap:              cellwatch.CapChargePct | cellwatch.CapACState | cellwatch.CapStatusWord | cellwatch.CapTimeRemaining | cellwatch.CapCount,
	}
}

func TestBuildRowsOmitsUnreportedFields(t *testing.T) {
	rows := buildBatteryRows(windowsLike(), cellwatch.Rate{Reason: "single reading"}, 0)
	labels := map[string]bool{}
	for _, r := range rows {
		labels[r.Label] = true
	}

	if !labels["Charge"] {
		t.Error("charge should be present when the platform reported it")
	}
	// Windows reports no wattage and no capacity through the unprivileged path.
	// Printing a zero there would be a confident lie.
	for _, absent := range []string{"Power draw", "Battery health", "Charge cycles"} {
		if labels[absent] {
			t.Errorf("%q must be omitted, not shown as zero, when the platform did not report it", absent)
		}
	}
	if !labels["Not reported"] {
		t.Error("the report should name what the platform could not supply")
	}
}

func TestBuildRowsReportsDesktop(t *testing.T) {
	rows := buildBatteryRows(cellwatch.Status{Source: "GetSystemPowerStatus"}, cellwatch.Rate{}, 0)
	if len(rows) != 1 || rows[0].Value != "none detected" {
		t.Errorf("a desktop should report one clear row, got %+v", rows)
	}
}

func TestLowThresholdMarksCharge(t *testing.T) {
	s := windowsLike()
	s.ChargePct = 18
	rows := buildBatteryRows(s, cellwatch.Rate{}, 20)

	var charge string
	for _, r := range rows {
		if r.Label == "Charge" {
			charge = r.Value
		}
	}
	if !strings.Contains(charge, "LOW") {
		t.Errorf("charge %q should be marked LOW at or below the threshold", charge)
	}
}

func TestRateIsLabelledAsEstimate(t *testing.T) {
	r := cellwatch.Rate{Stable: true, PctPerHour: 51.4, RawPctPerHour: 51.4, Span: 10 * time.Minute, Samples: 300}
	rows := buildBatteryRows(windowsLike(), r, 0)

	var value, detail string
	for _, row := range rows {
		if row.Label == "Drain rate" {
			value, detail = row.Value, row.Detail
		}
	}
	if !strings.Contains(value, "~") {
		t.Errorf("an estimated rate should carry a tilde, got %q", value)
	}
	if !strings.Contains(detail, "ESTIMATE") {
		t.Errorf("the detail should say the figure is an estimate, got %q", detail)
	}
}

func TestUnstableRateIsNotANumber(t *testing.T) {
	// The regression this guards: a short window used to produce a confident
	// rate from a single quantisation step.
	rows := buildBatteryRows(windowsLike(), cellwatch.Rate{Reason: "window too short"}, 0)
	for _, r := range rows {
		if r.Label == "Drain rate" {
			if r.Value != "n/a" {
				t.Errorf("an unstable rate must render as n/a, got %q", r.Value)
			}
			if !strings.Contains(r.Detail, "window too short") {
				t.Errorf("n/a should explain itself, got %q", r.Detail)
			}
		}
	}
}

func TestChargingRateIsNegativeVerb(t *testing.T) {
	s := windowsLike()
	s.AC = cellwatch.ACOnline
	s.Charging = true
	r := cellwatch.Rate{Stable: true, PctPerHour: -30, RawPctPerHour: -30, Span: time.Hour, Samples: 100}
	rows := buildBatteryRows(s, r, 0)

	for _, row := range rows {
		if row.Label == "Drain rate" && !strings.Contains(row.Value, "charging") {
			t.Errorf("a negative rate should read as charging, got %q", row.Value)
		}
	}
}

func TestPrintBatteryTableRenders(t *testing.T) {
	var buf bytes.Buffer
	printBatteryTable(&buf, windowsLike(), cellwatch.Rate{Reason: "single reading"}, 0)
	out := buf.String()

	for _, want := range []string{"CHEST BATTERY", "GetSystemPowerStatus", "Charge", "39%", "on battery"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q\n%s", want, out)
		}
	}
	// A zero-filled row is the failure mode this command exists to avoid.
	if strings.Contains(out, "0.0 W") {
		t.Error("table must not print a wattage the platform never reported")
	}
}

func TestPrintBatteryJSONMarksProvenance(t *testing.T) {
	var buf bytes.Buffer
	s := windowsLike()
	if err := printBatteryJSON(&buf, s, cellwatch.Rate{Reason: "single reading"}); err != nil {
		t.Fatalf("printBatteryJSON: %v", err)
	}
	out := buf.String()

	// The measured value is present and unadorned.
	if !strings.Contains(out, `"charge_pct": 39`) {
		t.Errorf("charge should be reported as measured\n%s", out)
	}
	// The derived value is null and says why, rather than being invented.
	if !strings.Contains(out, `"drain_pct_per_hour_estimate": null`) {
		t.Errorf("an unavailable rate must be null, not zero\n%s", out)
	}
	if !strings.Contains(out, "rate_unavailable_reason") {
		t.Error("JSON should explain why the rate is absent")
	}
	// The capability list is the machine-readable form of "this platform cannot
	// tell us that", which a table would otherwise only imply.
	if !strings.Contains(out, "not_reported_by_platform") {
		t.Error("JSON should list what the platform does not report")
	}
}

func TestPrintBatteryJSONIncludesEstimateSuffix(t *testing.T) {
	var buf bytes.Buffer
	r := cellwatch.Rate{Stable: true, PctPerHour: 51.4, RawPctPerHour: 51.4, Span: 600, Samples: 300}
	if err := printBatteryJSON(&buf, windowsLike(), r); err != nil {
		t.Fatalf("printBatteryJSON: %v", err)
	}
	out := buf.String()

	// The field name itself must carry the warning, so a consumer cannot strip
	// the provenance field and still believe the number is a measurement.
	if !strings.Contains(out, "drain_pct_per_hour_estimate") {
		t.Errorf("the rate field should be named as an estimate\n%s", out)
	}
	if !strings.Contains(out, `"rate_provenance": "estimated"`) {
		t.Error("rate provenance should be stated explicitly")
	}
}

func TestBatteryExitThreshold(t *testing.T) {
	s := windowsLike()
	s.ChargePct = 12

	if err := batteryExit(s, 15); err == nil {
		t.Error("charge at or below the threshold must produce an error carrying the exit code")
	}
	if err := batteryExit(s, 5); err != nil {
		t.Errorf("charge above the threshold must succeed, got %v", err)
	}
	// An unknown charge cannot trip a threshold, or the command would report a
	// failure on a platform that could not answer the question.
	s.ChargeKnown = false
	if err := batteryExit(s, 15); err != nil {
		t.Errorf("an unknown charge must not trip a threshold, got %v", err)
	}
}

func TestMissingCapabilitiesNamesGaps(t *testing.T) {
	got := missingCapabilities(cellwatch.CapChargePct | cellwatch.CapACState)
	for _, want := range []string{"wattage", "health", "cycle count"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing capabilities should include %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "charge") {
		t.Errorf("a reported capability must not be listed as missing, got %q", got)
	}
}

// frameRewind reads the cursor-up count a redraw frame begins with, and returns
// 0 when the frame does not rewind.
func frameRewind(frame string) int {
	i := strings.Index(frame, "\x1b[")
	if i < 0 {
		return 0
	}
	rest := frame[i+2:]
	end := strings.IndexByte(rest, 'A')
	if end < 0 {
		return 0
	}
	n := 0
	for _, c := range rest[:end] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func TestPrintBatteryLiveRewindsExactlyWhatItOccupied(t *testing.T) {
	// The live view redraws in place. If the rewind count does not equal the
	// number of lines the previous frame occupied, the block creeps down the
	// screen on every refresh until it scrolls away.
	s := windowsLike()
	rate := cellwatch.Rate{Reason: "collecting samples"}

	var first bytes.Buffer
	printBatteryLive(&first, s, rate, true)

	var second bytes.Buffer
	printBatteryLive(&second, s, rate, false)

	occupied := strings.Count(first.String(), "\n") + 1
	if got := frameRewind(second.String()); got != occupied {
		t.Errorf("frame rewinds %d lines but occupies %d", got, occupied)
	}
}

func TestPrintBatteryLiveFirstFrameDoesNotRewind(t *testing.T) {
	// Nothing has been drawn yet, so rewinding would put the cursor above the
	// top of the terminal and print the reading over the shell prompt.
	var buf bytes.Buffer
	printBatteryLive(&buf, windowsLike(), cellwatch.Rate{Reason: "collecting samples"}, true)
	if got := frameRewind(buf.String()); got != 0 {
		t.Errorf("the first frame rewound %d lines, want 0", got)
	}
}

func TestPrintBatteryLiveNeverHidesTheCursor(t *testing.T) {
	// Hiding the cursor and never restoring it leaves the user's shell with an
	// invisible prompt after the command exits.
	var buf bytes.Buffer
	printBatteryLive(&buf, windowsLike(), cellwatch.Rate{Reason: "collecting samples"}, true)
	printBatteryLive(&buf, windowsLike(), cellwatch.Rate{Reason: "collecting samples"}, false)
	if strings.Contains(buf.String(), "?25l") {
		t.Error("the live view must not hide the cursor")
	}
}

func TestIsTerminalRejectsAClosedPipe(t *testing.T) {
	// The live view is skipped off a terminal, so a pipe must not be mistaken
	// for one: escape sequences in a log file are unreadable.
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("a regular file must not be detected as a terminal")
	}
	if isTerminal(nil) {
		t.Error("a nil file must not be detected as a terminal")
	}
}
