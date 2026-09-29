// Package cellwatch reads battery state from the host operating system and
// records it, so that charge, drain rate and per-application activity can be
// reported over time.
//
// The package is deliberately honest about what it cannot know. No mainstream
// operating system exposes true per-application battery drain, and the ones
// that come closest either require administrator rights or an undocumented
// file format. Rather than fill those gaps with invented numbers, every reading
// is returned alongside a Capability bitmask describing what the platform
// actually reported. A field the platform could not supply is left un-known;
// it is never reported as zero.
//
// The package imports nothing outside the standard library and its own
// dependencies, so it can be split out of a host project without dragging that
// project along.
package cellwatch

import "errors"

var (
	// ErrUnsupported reports that the current platform has no reader. It is a
	// normal answer on a platform outside the supported set, not a bug.
	ErrUnsupported = errors.New("cellwatch: battery monitoring is not supported on this platform")

	// ErrNoBattery reports that the platform answered correctly but found no
	// battery, which is the ordinary state of a desktop.
	ErrNoBattery = errors.New("cellwatch: no battery present")

	// ErrNeedsElevation reports that the data exists but reading it requires
	// privileges this process does not have.
	ErrNeedsElevation = errors.New("cellwatch: reading this data requires elevation")
)

// ACState is the mains-line state of the machine.
type ACState int

const (
	// ACUnknown means the platform could not determine the mains state.
	ACUnknown ACState = iota
	// ACOffline means the machine is running on battery.
	ACOffline
	// ACOnline means the machine is running on mains.
	ACOnline
)

func (a ACState) String() string {
	switch a {
	case ACOnline:
		return "AC"
	case ACOffline:
		return "battery"
	default:
		return "unknown"
	}
}

// WattageSource says where a wattage figure came from, because the sources are
// not equally trustworthy. A caller that wants to weigh a number needs this.
type WattageSource uint8

const (
	// WattageNone means no wattage was determined.
	WattageNone WattageSource = iota
	// WattageSysfs is a direct read of power_now on Linux.
	WattageSysfs
	// WattageIOReg is Voltage x Amperage from the macOS IORegistry.
	WattageIOReg
	// WattageChargeDelta is derived by dividing capacity lost over time by
	// elapsed time. It is only as precise as the platform's charge counter,
	// which on Windows is quantised to whole percentage points.
	WattageChargeDelta
	// WattageDevice is a charge rate read directly from the battery
	// controller. On Windows it comes from the battery class driver rather
	// than from the system power status call, which is why it is reported
	// separately: it is the one source that is a measurement rather than a
	// derivation, and a caller ranking sources should be able to prefer it.
	WattageDevice
)

func (w WattageSource) String() string {
	switch w {
	case WattageSysfs:
		return "power_now"
	case WattageIOReg:
		return "voltage_x_amperage"
	case WattageChargeDelta:
		return "charge_delta"
	case WattageDevice:
		return "battery_controller"
	default:
		return "none"
	}
}

// Status is a single reading of the machine's battery state.
//
// Every measured quantity has a companion Known flag. This is the central
// honesty invariant of the package: a platform that cannot report watts leaves
// WattsKnown false and Watts at zero, which is distinguishable from a machine
// genuinely drawing no power. Collapsing the two would let a caller print a
// confident "0 W" for a value nobody measured.
type Status struct {
	// Present is false when the platform reported no battery, which is the
	// normal answer for a desktop rather than a failure.
	Present bool

	// Count is the number of batteries the platform reported. Windows
	// GetSystemPowerStatus reports only the first pack, so a two-battery
	// machine reads 1.
	Count int

	// Source names the mechanism that produced this reading, for example
	// "GetSystemPowerStatus". It is a diagnostic for humans and logs, not a
	// stable API; callers should not switch on it.
	Source string

	// AC is the mains-line state.
	AC ACState

	// Status is the platform's own status word verbatim: "Charging",
	// "Discharging", "Full", "Not charging" or "Unknown". Empty when the
	// platform has no such word.
	Status string

	// Charging is taken from the platform's own charging flag. It is never
	// inferred from the sign of a current reading, because that sign
	// convention is not consistent across platforms.
	Charging bool

	// Full is true when the platform reports the pack fully charged.
	Full bool

	// ChargePct is charge remaining, 0 to 100.
	ChargePct   float64
	ChargeKnown bool

	// Watts is instantaneous power draw for the whole machine. Consult
	// Wattage to judge how much it is worth.
	Watts      float64
	WattsKnown bool
	Wattage    WattageSource

	// FullWh is present full-charge capacity in watt-hours.
	FullWh      float64
	FullWhKnown bool

	// DesignWh is factory design capacity in watt-hours.
	DesignWh      float64
	DesignWhKnown bool

	// HealthPct is FullWh as a percentage of DesignWh.
	HealthPct   float64
	HealthKnown bool

	// Cycles is the completed charge-cycle count. Absent on most platforms.
	Cycles      int
	CyclesKnown bool

	// TimeToEmptyS is the platform's own time-remaining estimate in seconds.
	// Firmware reports an unsigned sentinel rather than a negative number when
	// the value is meaningless, so readers must reject anything at or above
	// 1<<31 before trusting it.
	TimeToEmptyS     int64
	TimeToEmptyKnown bool

	// TemperatureC is pack temperature where the platform exposes it. It is
	// cosmetic and is not used in any calculation.
	TemperatureC     float64
	TemperatureKnown bool

	// Cap is the set of fields this platform actually reported.
	Cap Capability
}

// Charging reports whether the pack is taking charge, treating a full pack on
// mains as not charging, which is what the platform means by the two states.
func (s Status) IsCharging() bool { return s.Charging && !s.Full }

// Draining reports whether the machine is consuming stored charge right now.
func (s Status) Draining() bool {
	return s.Present && s.AC == ACOffline && !s.Charging
}

// Has reports whether the platform supplied the given capability.
func (s Status) Has(c Capability) bool { return s.Cap&c != 0 }

// maxPlausibleSeconds bounds a time-remaining value. Every platform this
// package supports signals "unknown" with an unsigned sentinel near the top of
// the range rather than a negative number, so a raw uint32 must never be cast
// straight to a duration. On real Windows hardware a single reading returned a
// valid BatteryLifeTime of 3943 seconds alongside a BatteryFullLifeTime of
// 0xFFFFFFFF, which is why each field is checked on its own.
const maxPlausibleSeconds = 1 << 31

// plausibleSeconds reports whether a raw time-remaining reading is a real
// measurement rather than a sentinel or a signed value that has wrapped.
func plausibleSeconds(v int64) bool { return v > 0 && v < maxPlausibleSeconds }

// Reader reads battery state from the host. Implementations are supplied per
// platform by the build-tagged files in this package.
type Reader interface {
	// Read returns the current state. A machine with no battery returns a
	// Status with Present false and a nil error: that is an answer, not a
	// failure. A read that genuinely could not be performed returns an error.
	Read() (Status, error)
}

// NewReader returns a Reader for the host platform. It reports ErrUnsupported
// on a platform this package has no reader for, which is a normal answer
// rather than a failure: a caller can fall back to whatever it was going to do
// on a machine it cannot read.
func NewReader() (Reader, error) { return newReader() }
