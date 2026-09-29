//go:build linux

package cellwatch

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The Linux power supply class is the whole of the platform reader. Every
// attribute is one file under /sys/class/power_supply/<name>/, in units fixed by
// the kernel ABI rather than by the vendor, and a driver may implement any
// subset of them. Attributes are therefore optional by construction: the kernel
// documents power_now and cycle_count as frequently absent, and a missing file
// is the normal case rather than a fault. Every read here returns ok=false for
// that case and the caller leaves the field un-known.
const sysfsPowerSupply = "/sys/class/power_supply"

// linuxReader reads the power supply class directly. It holds no state because
// sysfs is read fresh on every call; caching would only add a way to report a
// stale figure as a current one.
type linuxReader struct{}

// Read enumerates the power supply class and reports the first battery it
// finds.
//
// A machine with no battery of any kind is a desktop and returns a nil error
// with Present false, because that is the answer to the question rather than a
// failure to answer it.
func (linuxReader) Read() (Status, error) {
	s := Status{Source: "sysfs"}

	if ac, ok := readACState(sysfsPowerSupply); ok {
		s.AC = ac
		s.Cap |= CapACState
	}

	dir, err := findBatteryDir(sysfsPowerSupply)
	if err != nil {
		return Status{}, err
	}
	if dir == "" {
		return s, nil
	}

	// present is 0 when the pack is physically out of the machine, which is not
	// the same as the machine having no battery: the slot, the driver and the
	// remaining attributes all still exist and still read sensibly. Reporting
	// the dock-out as absent is right, but reporting the remaining attributes
	// of an absent pack would not be, so nothing below is read.
	if p, ok := readAttrInt(dir, "present"); ok && p == 0 {
		return s, nil
	}

	// A machine with several packs is reported as having one, because this
	// reader describes the first entry and a Count of 3 beside the first pack's
	// figures would imply the other two were measured too.
	s.Present = true
	s.Count = 1
	s.Cap |= CapCount

	// The kernel fixes the status word to exactly these five strings. Anything
	// else is a driver inventing its own and is dropped rather than passed
	// through, because a consumer comparing against "Charging" would silently
	// not match it and would report the pack as idle.
	switch word := readAttrText(dir, "status"); word {
	case "Unknown", "Charging", "Discharging", "Not charging", "Full":
		s.Status = word
		s.Cap |= CapStatusWord
		s.Charging = word == "Charging"
		s.Full = word == "Full"
	}

	if pct, ok := readAttrInt(dir, "capacity"); ok {
		// capacity is a percentage and the kernel does not enforce that: some
		// firmware returns 101 or 105, which must not reach a display or a
		// charge-delta estimate as an over-full pack. A negative value is
		// nonsense rather than an over-report and is left un-known.
		if pct > 100 {
			pct = 100
		}
		if pct >= 0 {
			s.ChargePct = float64(pct)
			s.ChargeKnown = true
			s.Cap |= CapChargePct
		}
	}

	energy, charge := capacityFamily(dir)
	switch {
	case energy:
		if s.FullWh, s.FullWhKnown = energyWh(dir, "energy_full"); s.FullWhKnown {
			s.Cap |= CapFullCapacity
		}
		if s.DesignWh, s.DesignWhKnown = energyWh(dir, "energy_full_design"); s.DesignWhKnown {
			s.Cap |= CapDesignCapacity
		}
	case charge:
		if s.FullWh, s.FullWhKnown = chargeWh(dir, "charge_full"); s.FullWhKnown {
			s.Cap |= CapFullCapacity
		}
		if s.DesignWh, s.DesignWhKnown = chargeWh(dir, "charge_full_design"); s.DesignWhKnown {
			s.Cap |= CapDesignCapacity
		}
	}

	// power_now is already a power in microwatts, so it needs no companion
	// attribute and no conversion, and using it cannot blend the two families.
	if p, ok := readAttrInt(dir, "power_now"); ok {
		s.Watts = math.Abs(float64(p)) / 1e6
		s.WattsKnown = true
		s.Wattage = WattageSysfs
		s.Cap |= CapWatts
	} else if charge {
		// With no power_now the only measurement left on a charge-family driver
		// is current_now, which is signed: the kernel reports it negative while
		// the pack discharges. The sign is taken for magnitude only. Direction
		// comes from the status word, which is the attribute whose meaning the
		// kernel actually documents.
		if i, ok := readAttrInt(dir, "current_now"); ok {
			if v, vok := readAttrInt(dir, "voltage_now"); vok {
				s.Watts = math.Abs(float64(i)/1e6) * float64(v) / 1e6
				s.WattsKnown = true
				s.Wattage = WattageSysfs
				s.Cap |= CapWatts
			}
		}
	}

	if s.FullWhKnown && s.DesignWhKnown && s.DesignWh > 0 {
		s.HealthPct = s.FullWh / s.DesignWh * 100
		s.HealthKnown = true
		s.Cap |= CapHealth
	}

	// Some drivers store the cycle count in a 32-bit field and use its maximum
	// as a stand-in for "unknown". Believing that sentinel would report a pack
	// as four billion cycles old, which is a plausible-looking number rather
	// than an obvious failure.
	if c, ok := readAttrInt(dir, "cycle_count"); ok && c >= 0 && c < maxReportedCycles {
		s.Cycles = int(c)
		s.CyclesKnown = true
		s.Cap |= CapCycleCount
	}

	// temp is in tenths of a degree Celsius. It is recorded because the platform
	// offers it and is used in no calculation, here or anywhere else.
	if t, ok := readAttrInt(dir, "temp"); ok {
		s.TemperatureC = float64(t) / 10
		s.TemperatureKnown = true
		s.Cap |= CapTemperature
	}

	// No driver exposes a time-remaining attribute, so there is no native
	// estimate to report. A rate derived from the charge window is available
	// instead and is attributable to WattageChargeDelta.
	return s, nil
}

// maxReportedCycles is the largest cycle count believed. It is the 32-bit
// maximum, which a driver writes when it has no count to give.
const maxReportedCycles = 1<<32 - 1

// readACState reports the mains state from the supplies whose type is Mains.
//
// The directory name is never consulted, because the names in this tree are
// driver private and say nothing about what is feeding the machine: a dock's
// inbuilt supply appears as ACAD and a USB-C source as ADP1, and an ACAD entry
// exists whether or not mains is actually live. An ACAD that reads 0 while the
// machine runs happily on its pack is the ordinary case, so a name-driven
// reader reports mains online whenever a dock is plugged in.
//
// A supply that is present but offline is genuine battery operation, which is
// what the kernel is reporting. A machine exposing no Mains supply at all is
// left unknown rather than assumed to be on its pack.
func readACState(root string) (ACState, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ACUnknown, false
	}
	seen := false
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if readAttrText(dir, "type") != "Mains" {
			continue
		}
		seen = true
		if on, ok := readAttrInt(dir, "online"); ok && on == 1 {
			return ACOnline, true
		}
	}
	if seen {
		return ACOffline, true
	}
	return ACUnknown, false
}

// findBatteryDir returns the directory of the first supply whose type is
// exactly Battery, or "" when the machine has none.
//
// Entries here are symlinks into /sys/devices, so filtering on IsDir would
// reject every real battery and return a desktop answer for every laptop. The
// type file is the only discriminator the kernel guarantees.
func findBatteryDir(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("cellwatch: read %s: %w", root, err)
	}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if readAttrText(dir, "type") == "Battery" {
			return dir, nil
		}
	}
	return "", nil
}

// capacityFamily reports which of the two mutually exclusive capacity
// attribute sets the driver implements.
//
// They must never be combined. energy_* is in microwatt-hours and charge_* in
// microamp-hours, so dividing a charge_full by an energy_full_design produces
// a figure wrong by roughly the pack's nominal voltage, which is a large,
// plausible-looking error rather than an obvious one. When a driver exposes
// both, energy wins: energy_full is already an energy, so no voltage is needed
// and no conversion can go wrong.
func capacityFamily(dir string) (energy, charge bool) {
	for _, n := range []string{"energy_now", "energy_full", "energy_full_design"} {
		if _, ok := readAttrInt(dir, n); ok {
			energy = true
			break
		}
	}
	for _, n := range []string{"charge_now", "charge_full", "charge_full_design", "current_now"} {
		if _, ok := readAttrInt(dir, n); ok {
			charge = true
			break
		}
	}
	return energy, charge
}

// energyWh converts a microwatt-hour attribute to watt-hours, which is the unit
// Status is specified in.
func energyWh(dir, attr string) (float64, bool) {
	v, ok := readAttrInt(dir, attr)
	if !ok {
		return 0, false
	}
	return float64(v) / 1e6, true
}

// chargeWh converts a microamp-hour attribute to watt-hours using the pack's
// voltage at the moment of reading.
//
// Wh = Ah x V, with Ah = uAh/1e6 and V = uV/1e6. Without voltage_now the
// conversion is impossible, and the raw microamp-hours are left un-known rather
// than reported as if they were watt-hours.
func chargeWh(dir, attr string) (float64, bool) {
	ah, ok := readAttrInt(dir, attr)
	if !ok {
		return 0, false
	}
	v, ok := readAttrInt(dir, "voltage_now")
	if !ok {
		return 0, false
	}
	return float64(ah) / 1e6 * float64(v) / 1e6, true
}

// readAttrText reads a sysfs attribute as trimmed text, returning "" when the
// file is absent. Attributes in this tree are optional by construction, so an
// empty result is an ordinary answer and not an error.
func readAttrText(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readAttrInt reads a sysfs attribute as a signed integer. A missing or
// unparseable file yields ok false and no value: an attribute that does not
// parse is one this reader cannot use, which is indistinguishable from one that
// is not there.
func readAttrInt(dir, name string) (int64, bool) {
	v, err := strconv.ParseInt(readAttrText(dir, name), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func newReader() (Reader, error) { return linuxReader{}, nil }

func init() {
	// CapTimeRemaining is absent because no sysfs driver exposes a time-remaining
	// attribute, not because this reader forgot to look.
	capabilityFloor = CapChargePct | CapACState | CapStatusWord | CapWatts |
		CapFullCapacity | CapDesignCapacity | CapHealth | CapCycleCount |
		CapTemperature | CapCount
}
