package cellwatch

// Capability is a bitmask describing what the running platform was able to
// report. It exists so that a caller can ask rather than guess: a report that
// shows an empty health field is visibly a limitation, not a machine with a
// perfect battery.
//
// A platform reader must set only the bits it actually read. Padding the mask
// to look complete would defeat the purpose of having it.
type Capability uint32

const (
	// CapChargePct reports charge remaining as a percentage.
	CapChargePct Capability = 1 << iota
	// CapACState reports whether the machine is on mains.
	CapACState
	// CapStatusWord reports the platform's own status string verbatim.
	CapStatusWord
	// CapWatts reports instantaneous power draw.
	CapWatts
	// CapFullCapacity reports present full-charge capacity in watt-hours.
	CapFullCapacity
	// CapDesignCapacity reports factory design capacity in watt-hours.
	CapDesignCapacity
	// CapHealth reports full capacity as a percentage of design.
	CapHealth
	// CapCycleCount reports completed charge cycles.
	CapCycleCount
	// CapTimeRemaining reports the platform's own time-remaining estimate.
	CapTimeRemaining
	// CapTemperature reports pack temperature.
	CapTemperature
	// CapCount reports how many batteries the platform could enumerate.
	CapCount

	// capLast is a sentinel for the end of the assignable range. Adding a
	// capability above it would silently collide with it.
	capLast
)

// String renders the set capabilities in a stable order, for diagnostics and
// for the JSON payload so that a consumer can see what the platform lacked.
func (c Capability) String() string {
	if c == 0 {
		return "none"
	}
	names := []struct {
		bit  Capability
		name string
	}{
		{CapChargePct, "charge_pct"},
		{CapACState, "ac_state"},
		{CapStatusWord, "status_word"},
		{CapWatts, "watts"},
		{CapFullCapacity, "full_capacity"},
		{CapDesignCapacity, "design_capacity"},
		{CapHealth, "health"},
		{CapCycleCount, "cycle_count"},
		{CapTimeRemaining, "time_remaining"},
		{CapTemperature, "temperature"},
		{CapCount, "count"},
	}
	out := ""
	for _, n := range names {
		if c&n.bit == 0 {
			continue
		}
		if out != "" {
			out += "|"
		}
		out += n.name
	}
	return out
}

// Has reports whether every bit in want is set.
func (c Capability) Has(want Capability) bool { return c&want == want }

// With returns c with the given bits set, used by readers as they accumulate.
func (c Capability) With(bits Capability) Capability { return c | bits }

// PlatformCapabilities reports what this build of cellwatch can ever read,
// before any particular machine is consulted. It is the ceiling that
// PlatformSupport then narrows: a build for linux knows it can read sysfs, but
// whether the running kernel exposes power_now is a question about the machine.
//
// CapAppEnergy is deliberately absent. No supported platform reports true
// per-application battery energy through any interface cellwatch can read
// without administrator rights. Per-application figures in this package are
// apportioned from CPU and I/O activity and are estimates; naming the absence
// here is what keeps that from being forgotten.
func PlatformCapabilities() Capability {
	return capabilityFloor
}

// capabilityFloor is set per build by the platform files. It is declared here
// so the package compiles on every platform, including those with no reader at
// all.
var capabilityFloor Capability
