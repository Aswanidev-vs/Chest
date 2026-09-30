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

// The banner is a box drawn with a fixed 32-column rule, so the title row has
// to be exactly as wide as the rules above and below it. Centring the title by
// padding only its left side -- which is what the table renderer used to do --
// pushes the closing bar past the border on every run.
func TestBatteryBannerRowsAreAllTheSameWidth(t *testing.T) {
	var buf bytes.Buffer
	printBatteryTable(&buf, windowsLike(), cellwatch.Rate{Reason: "single reading"}, 0)

	var rule, title int
	for i, line := range strings.Split(buf.String(), "\n") {
		plain := stripANSI(line)
		switch {
		case strings.Contains(plain, "CHEST BATTERY"):
			title = len([]rune(plain))
		case strings.HasPrefix(strings.TrimSpace(plain), "+") && rule == 0:
			rule = len([]rune(plain))
		}
		if title != 0 && rule != 0 {
			break
		}
		_ = i
	}
	if rule == 0 || title == 0 {
		t.Fatalf("banner not found (rule=%d title=%d)", rule, title)
	}
	if rule != title {
		t.Errorf("banner title is %d columns, rule is %d: the box is broken\n%s", title, rule, buf.String())
	}
}

func TestPrintBatteryHistoryBannerIsAligned(t *testing.T) {
	rep := &cellwatch.UsageReport{
		Range:    cellwatch.Range{From: time.Now().Add(-time.Hour), To: time.Now()},
		Coverage: cellwatch.Coverage{Samples: 3, Expected: 3, Pct: 100},
	}
	var buf bytes.Buffer
	printBatteryHistory(&buf, rep, cellwatch.PeriodMonth, 0, nil, nil)

	lines := strings.Split(buf.String(), "\n")
	if len(lines) < 4 {
		t.Fatalf("history banner too short:\n%s", buf.String())
	}
	rule := len([]rune(stripANSI(lines[1])))
	title := len([]rune(stripANSI(lines[2])))
	if rule != title {
		t.Errorf("history banner title is %d columns, rule is %d:\n%s", title, rule, buf.String())
	}
	if !strings.Contains(stripANSI(lines[2]), "BATTERY - MONTH") {
		t.Errorf("history banner missing title: %q", lines[2])
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
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
	// The live view redraws in place, so each frame must rewind by exactly the
	// number of lines the frame before it drew. Too few and the block creeps
	// down the screen; too many and it eats the line above it, which on a frame
	// that starts partway down the terminal is the user's own scrollback.
	s := windowsLike()
	rate := cellwatch.Rate{Reason: "collecting samples"}

	var first bytes.Buffer
	printBatteryLive(&first, s, rate, true, 0)

	// The frame emits one line per row and each ends in a newline, so the
	// cursor rests that many rows below where the frame started.
	drawn := strings.Count(first.String(), "\n")
	if drawn != liveFrameLines(s) {
		t.Errorf("liveFrameLines reports %d, but the frame emitted %d newlines",
			liveFrameLines(s), drawn)
	}

	var second bytes.Buffer
	printBatteryLive(&second, s, rate, false, liveFrameLines(s))

	if got := frameRewind(second.String()); got != drawn {
		t.Errorf("frame rewinds %d lines but the previous frame drew %d", got, drawn)
	}
}

func TestPrintBatteryLiveFirstFrameDoesNotRewind(t *testing.T) {
	// Nothing has been drawn yet, so rewinding would put the cursor above the
	// top of the terminal and print the reading over the shell prompt.
	var buf bytes.Buffer
	printBatteryLive(&buf, windowsLike(), cellwatch.Rate{Reason: "collecting samples"}, true, 0)
	if got := frameRewind(buf.String()); got != 0 {
		t.Errorf("the first frame rewound %d lines, want 0", got)
	}
}

func TestPrintBatteryLiveNeverHidesTheCursor(t *testing.T) {
	// Hiding the cursor and never restoring it leaves the user's shell with an
	// invisible prompt after the command exits.
	var buf bytes.Buffer
	printBatteryLive(&buf, windowsLike(), cellwatch.Rate{Reason: "collecting samples"}, true, 0)
	printBatteryLive(&buf, windowsLike(), cellwatch.Rate{Reason: "collecting samples"}, false, liveFrameLines(windowsLike()))
	if strings.Contains(buf.String(), "?25l") {
		t.Error("the live view must not hide the cursor")
	}
}

// The redraw has to survive the reading changing height between frames, which is
// what happens when the platform starts or stops reporting a field. A frame
// drawn at the taller height leaves residue underneath when the next one is
// shorter, and the user sees the same clock twice.
func TestPrintBatteryLiveClearsResidueWhenTheFrameShrinks(t *testing.T) {
	tall := windowsLike()
	tall.WattsKnown = true
	tall.Watts = 12.5
	short := windowsLike()
	rate := cellwatch.Rate{Reason: "collecting samples"}

	if liveFrameLines(tall) <= liveFrameLines(short) {
		t.Fatalf("this test needs a taller reading: tall=%d short=%d",
			liveFrameLines(tall), liveFrameLines(short))
	}

	var buf bytes.Buffer
	printBatteryLive(&buf, tall, rate, true, 0)
	printBatteryLive(&buf, short, rate, false, liveFrameLines(tall))

	// Every line the frame draws must be cleared before it is written, and
	// anything left below must be erased, or the taller frame's tail survives.
	if !strings.Contains(buf.String(), "\x1b[0J") {
		t.Error("a shrinking frame must erase what the taller frame left below it")
	}
	if n := strings.Count(buf.String(), "\x1b[2K"); n < liveFrameLines(short) {
		t.Errorf("frame cleared %d lines, want one per line drawn (%d)", n, liveFrameLines(short))
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

func TestValidateBatteryFlags(t *testing.T) {
	tests := []struct {
		name string
		in   batteryFlagInput
		// wantErr is the flag an accepted command must have complained about.
		// Empty means the input is valid.
		wantErr string
	}{
		{
			name: "the shipped defaults are accepted",
			in:   batteryFlagInput{top: 5, interval: 2 * time.Second},
		},
		{
			name:    "a negative low is refused",
			in:      batteryFlagInput{lowPct: -1, top: 5, interval: 2 * time.Second},
			wantErr: "--low",
		},
		{
			name:    "a low above 100 is refused",
			in:      batteryFlagInput{lowPct: 101, top: 5, interval: 2 * time.Second},
			wantErr: "--low",
		},
		{
			// 0 disables both flags, so it is the one value outside a
			// percentage's range that is still meaningful.
			name: "both ends of the percentage range are accepted",
			in:   batteryFlagInput{lowPct: 0, failUnder: 100, top: 1, interval: time.Nanosecond},
		},
		{
			name:    "a negative fail-under is refused rather than treated as disabled",
			in:      batteryFlagInput{failUnder: -1, top: 5, interval: 2 * time.Second},
			wantErr: "--fail-under",
		},
		{
			name:    "a fail-under above 100 is refused",
			in:      batteryFlagInput{failUnder: 101, top: 5, interval: 2 * time.Second},
			wantErr: "--fail-under",
		},
		{
			name:    "asking for zero applications is refused",
			in:      batteryFlagInput{top: 0, interval: 2 * time.Second},
			wantErr: "--top",
		},
		{
			name:    "a negative top is refused",
			in:      batteryFlagInput{top: -1, interval: 2 * time.Second},
			wantErr: "--top",
		},
		{
			// This is the one that used to reach time.NewTicker and panic.
			name:    "a zero interval is refused",
			in:      batteryFlagInput{top: 5, interval: 0},
			wantErr: "--interval",
		},
		{
			name:    "a negative interval is refused",
			in:      batteryFlagInput{top: 5, interval: -time.Second},
			wantErr: "--interval",
		},
		{
			name: "zero seconds is accepted and means a single reading",
			in:   batteryFlagInput{top: 5, seconds: 0, interval: 2 * time.Second},
		},
		{
			name:    "a negative window is refused rather than read as a single reading",
			in:      batteryFlagInput{top: 5, seconds: -5 * time.Second, interval: 2 * time.Second},
			wantErr: "--seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBatteryFlags(tt.in)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("these flags should be accepted, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the command should have been refused, want an error naming %s", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("the error should name %s, got %q", tt.wantErr, err)
			}
		})
	}
}

func TestBatteryCmdRefusesBadFlagsBeforeReadingTheMachine(t *testing.T) {
	// The check has to sit in RunE ahead of the reader, or the command still
	// reads the battery and opens the database before complaining about a flag
	// that was never going to be honoured.
	for _, tt := range []struct{ flag, value string }{
		{"low", "-5"},
		{"low", "101"},
		{"fail-under", "-1"},
		{"fail-under", "101"},
		{"top", "0"},
		{"top", "-1"},
		{"interval", "0"},
		{"interval", "-1s"},
		{"seconds", "-5s"},
	} {
		cmd := newBatteryCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		if err := cmd.Flags().Set(tt.flag, tt.value); err != nil {
			t.Fatalf("setting --%s=%s: %v", tt.flag, tt.value, err)
		}
		if err := cmd.RunE(cmd, nil); err == nil {
			t.Errorf("--%s=%s was accepted; it should be refused", tt.flag, tt.value)
		}
	}
}
