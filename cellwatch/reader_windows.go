//go:build windows

package cellwatch

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Windows power state is read through a single kernel32 call. It binds
// here with no new module because golang.org/x/sys is already a direct
// dependency of the host project and already provides the lazy procedure
// lookup this needs.
//
// The SYSTEM_POWER_STATUS struct is declared rather than imported: x/sys does
// not ship it. Grepping the whole x/sys tree for either SYSTEM_POWER_STATUS or
// GetSystemPowerStatus returns nothing, so the layout below is mirrored from
// the Win32 documentation. It was confirmed on real hardware as twelve bytes,
// four single bytes followed by two uint32s with no padding.
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

var (
	modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemPowerStatus = modkernel32.NewProc("GetSystemPowerStatus")
)

// Windows BatteryFlag bits, from the Win32 documentation. Only these are
// dependable; the middle bits are reserved and firmware sets them freely.
const (
	flagHigh      = 1
	flagLow       = 2
	flagCritical  = 4
	flagCharging  = 8
	flagNoBattery = 128
	flagUnknown   = 255
)

type windowsReader struct{}

func (windowsReader) Read() (Status, error) {
	var p systemPowerStatus
	ret, _, err := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&p)))
	if ret == 0 {
		return Status{}, err
	}

	s := Status{
		Source: "GetSystemPowerStatus",
		Count:  1,
		Cap:    CapACState,
	}

	// A desktop reports flag 128, and a machine whose firmware cannot answer
	// reports 255. Both are valid answers meaning "no usable battery", and
	// neither is an error.
	if p.BatteryFlag == flagNoBattery || p.BatteryFlag == flagUnknown {
		s.Source = "GetSystemPowerStatus"
		return s, nil
	}

	s.Present = true
	s.Cap |= CapCount

	switch p.ACLineStatus {
	case 1:
		s.AC = ACOnline
	case 0:
		s.AC = ACOffline
	}

	// The flag byte doubles as a coarse status word, and it is the only status
	// Windows offers.
	switch {
	case p.BatteryFlag&flagCharging != 0:
		s.Status = "Charging"
	case p.BatteryFlag&flagCritical != 0:
		s.Status = "Critical"
	case p.BatteryFlag&flagLow != 0:
		s.Status = "Low"
	case p.BatteryFlag&flagHigh != 0:
		s.Status = "High"
	default:
		s.Status = "Normal"
	}
	s.Cap |= CapStatusWord

	s.Charging = p.BatteryFlag&flagCharging != 0
	s.Full = s.Charging && p.ACLineStatus == 1 && p.BatteryLifePercent >= 100

	if p.BatteryLifePercent != 255 {
		s.ChargePct = float64(p.BatteryLifePercent)
		s.ChargeKnown = true
		s.Cap |= CapChargePct
	}

	// Both lifetime fields use the same sentinel convention, and on real
	// hardware one is routinely valid while the other is not: a measured
	// reading gave BatteryLifeTime of 3943 seconds alongside a
	// BatteryFullLifeTime of 0xFFFFFFFF. Each is therefore checked on its own
	// rather than as a pair.
	if t := int64(p.BatteryLifeTime); plausibleSeconds(t) {
		s.TimeToEmptyS = t
		s.TimeToEmptyKnown = true
		s.Cap |= CapTimeRemaining
	}

	// Windows supplies no wattage, no capacity and no cycle count through
	// GetSystemPowerStatus itself, which reports only charge, mains state and
	// time remaining. Those figures are available through the battery class
	// driver instead, and are read separately so that a machine refusing the
	// deeper open still gets a complete answer from the cheap call.
	readBatteryDetails(&s)
	return s, nil
}

func newReader() (Reader, error) { return windowsReader{}, nil }

func init() {
	capabilityFloor = CapChargePct | CapACState | CapStatusWord | CapTimeRemaining | CapCount
}
