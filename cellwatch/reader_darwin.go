//go:build darwin

package cellwatch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// macOS exposes no battery API to user space. The only route is the IORegistry,
// queried with ioreg, whose output format Apple does not document and has
// changed across releases. The reader is therefore written to fail closed: a
// stream it cannot recognise becomes an error, never a zero-valued Status,
// because the zero Status is a confident "0%" rather than an obvious failure.
const darwinSource = "ioreg AppleSmartBattery"

// ioregTimeout bounds the child process. The command answers in tens of
// milliseconds; two seconds is two orders of magnitude of slack, and it exists
// so that a wedged IOKit call stalls this reading rather than the sampler loop
// that calls it. Without it a single stuck read blocks every later one, since a
// collector that has stopped advancing is indistinguishable from one that has.
const ioregTimeout = 2 * time.Second

// maxPlausibleMinutes bounds a TimeRemaining value before it is multiplied into
// seconds. It is the package-wide one-week-plus ceiling expressed in minutes;
// the two negative sentinels are rejected separately, by sign.
const maxPlausibleMinutes = (1 << 31) / 60

// darwinReader reads AppleSmartBattery from the IORegistry on demand.
type darwinReader struct{}

// Read runs ioreg and interprets the AppleSmartBattery entry.
//
// A Mac with no battery, which is a mini or a studio, prints nothing at all and
// is reported as absent with a nil error. Anything else that cannot be read is
// an error.
func (darwinReader) Read() (Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ioregTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ioreg", "-r", "-c", "AppleSmartBattery").Output()
	if err != nil {
		return Status{}, fmt.Errorf("cellwatch: ioreg: %w", err)
	}
	text := string(out)

	// With no battery of that class the command prints nothing, so the class
	// name itself is the test for whether the machine has a pack at all.
	if !strings.Contains(text, "AppleSmartBattery") {
		return Status{Source: darwinSource}, nil
	}

	f := parseIOReg(text)
	if _, ok := ioregNumber(f, "CurrentCapacity"); !ok {
		if _, ok := ioregNumber(f, "MaxCapacity"); !ok {
			return Status{}, errors.New("cellwatch: ioreg AppleSmartBattery output carried no recognisable capacity field")
		}
	}

	s := Status{Source: darwinSource, Present: true, Count: 1, Cap: CapCount}

	if ext, ok := ioregYes(f, "ExternalConnected"); ok {
		if ext {
			s.AC = ACOnline
		} else {
			s.AC = ACOffline
		}
		s.Cap |= CapACState
	}

	// IsCharging and FullyCharged are the platform's own flags, so direction is
	// taken from them rather than from any reading's sign. macOS publishes no
	// status word, so Status is left empty rather than filled with a phrase of
	// this reader's own invention; the two booleans and AC carry the same
	// information without claiming to be Apple's wording.
	if charging, ok := ioregYes(f, "IsCharging"); ok {
		s.Charging = charging
	}
	if full, ok := ioregYes(f, "FullyCharged"); ok {
		s.Full = full
	}

	// AppleRawMaxCapacity is the pack's raw full charge and is the only honest
	// denominator for remaining charge: MaxCapacity is the capacity the system
	// is willing to treat as full, which health management lowers over a pack's
	// life, and dividing by it reports a healthy pack as permanently full.
	raw := 0.0
	if v, ok := ioregNumber(f, "AppleRawMaxCapacity"); ok && v > 0 {
		raw = v
	} else if v, ok := ioregNumber(f, "MaxCapacity"); ok && v > 0 {
		raw = v
	}
	if cur, ok := ioregNumber(f, "CurrentCapacity"); ok && raw > 0 {
		s.ChargePct = cur * 100 / raw
		s.ChargeKnown = true
		s.Cap |= CapChargePct
	}

	volts := 0.0
	if v, ok := ioregNumber(f, "Voltage"); ok && v > 0 {
		volts = v / 1000
	}

	if amps, ok := ioregNumber(f, "Amperage"); ok && volts > 0 {
		// W = V x A, with both operands taken as magnitudes. The sign of
		// Amperage is undocumented and does not even hold a consistent meaning
		// across models: some report charge positive and discharge negative,
		// some invert it. Reading direction from it produces a pack that appears
		// to discharge while plugged in. abs() makes the figure a magnitude and
		// leaves the direction to IsCharging, which Apple does define.
		watts := volts * math.Abs(amps) / 1000
		// On Apple silicon Amperage reads zero while the machine is on its
		// pack, because the current sensor is not populated on that path. A
		// genuine zero there would claim the machine draws no power at all,
		// which is exactly the kind of confident wrong answer this package
		// exists to avoid, so it is left un-known.
		if watts > 0 || s.Charging {
			s.Watts = watts
			s.WattsKnown = true
			s.Wattage = WattageIOReg
			s.Cap |= CapWatts
		}
	}

	// Capacities are in milliamp-hours. Reporting them as watt-hours would
	// inflate every figure by the pack voltage, about twelvefold on a typical
	// laptop, so both are converted with the voltage read above. Without a
	// voltage the conversion is not possible and the capacity stays un-known.
	if mAh, ok := ioregNumber(f, "MaxCapacity"); ok && volts > 0 {
		s.FullWh = mAh / 1000 * volts
		s.FullWhKnown = true
		s.Cap |= CapFullCapacity
	} else if mAh, ok := ioregNumber(f, "AppleRawMaxCapacity"); ok && volts > 0 {
		s.FullWh = mAh / 1000 * volts
		s.FullWhKnown = true
		s.Cap |= CapFullCapacity
	}
	if mAh, ok := ioregNumber(f, "DesignCapacity"); ok && volts > 0 {
		s.DesignWh = mAh / 1000 * volts
		s.DesignWhKnown = true
		s.Cap |= CapDesignCapacity
	}

	if s.FullWhKnown && s.DesignWhKnown && s.DesignWh > 0 {
		s.HealthPct = s.FullWh / s.DesignWh * 100
		s.HealthKnown = true
		s.Cap |= CapHealth
	}

	if c, ok := ioregNumber(f, "CycleCount"); ok && c >= 0 {
		s.Cycles = int(c)
		s.CyclesKnown = true
		s.Cap |= CapCycleCount
	}

	// TimeRemaining is in minutes and uses -1 for "no estimate" and -2 for
	// "no battery". Both are negatives, so a sign test rejects them together
	// with any other negative. The sentinel matters because -1 would otherwise
	// become -60 seconds, a runtime estimate that reads as an overflow.
	//
	// While the pack is charging this is the time until full, not the time
	// until empty, which is the platform's meaning and not a misreading here.
	// It is reported as given because Charging is reported alongside it, so a
	// caller can tell which of the two the figure describes.
	if m, ok := ioregNumber(f, "TimeRemaining"); ok && m >= 0 && m < maxPlausibleMinutes {
		s.TimeToEmptyS = int64(m * 60)
		s.TimeToEmptyKnown = true
		s.Cap |= CapTimeRemaining
	}

	// Temperature is in hundredths of a degree Celsius. It is cosmetic and is
	// used in no calculation.
	if t, ok := ioregNumber(f, "Temperature"); ok {
		s.TemperatureC = t / 100
		s.TemperatureKnown = true
		s.Cap |= CapTemperature
	}

	return s, nil
}

// parseIOReg turns the ioreg stream into a flat map of key to value.
//
// The format is one `"Key" = value` pair per line, but the lines that open and
// close the entry are not, and values are occasionally quoted. Nothing about
// that is guaranteed, so the split is on the first = with both sides trimmed of
// surrounding whitespace and quotes, and any line without an = is skipped.
func parseIOReg(text string) map[string]string {
	f := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key := strings.Trim(strings.TrimSpace(k), `"`)
		if key == "" {
			continue
		}
		f[key] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return f
}

// ioregNumber returns a numeric field and whether it was present and numeric.
func ioregNumber(f map[string]string, key string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(f[key]), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// ioregYes returns a boolean field and whether the stream actually said so.
//
// Only Yes and No count as an answer. Defaulting an unrecognised value to false
// would report a machine as not charging when it is, which is the sort of
// plausible wrong answer this reader exists to avoid.
func ioregYes(f map[string]string, key string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(f[key])) {
	case "yes":
		return true, true
	case "no":
		return false, true
	}
	return false, false
}

func newReader() (Reader, error) { return darwinReader{}, nil }

func init() {
	// CapStatusWord is absent because macOS publishes flags rather than a
	// status string; synthesising one would present this reader's wording as
	// Apple's. The floor is otherwise everything the class can supply, which is
	// more than any other supported platform reports.
	capabilityFloor = CapChargePct | CapACState | CapWatts | CapFullCapacity |
		CapDesignCapacity | CapHealth | CapCycleCount | CapTimeRemaining |
		CapTemperature | CapCount
}
