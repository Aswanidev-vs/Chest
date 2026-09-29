//go:build windows

package cellwatch

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// The deep Windows battery figures come from the battery class driver, reached
// by enumerating the GUID_DEVICE_BATTERY device interfaces, opening the first
// one, and issuing IOCTLs on the resulting handle. That path is unprivileged:
// an ordinary process on this machine read cycle count, both capacities, the
// present charge, voltage and the native power-state word with no elevation.
//
// It is implemented here rather than delegated to a library because the two
// obvious alternatives both lose something. github.com/distatus/battery issues
// the same IOCTLs and then drops bi.CycleCount on the floor, because its
// exported Battery struct has no field for it, and it was last released in
// September 2023 with a BREAKING.md. A dependency that computes an answer and
// then discards it cannot be extended from outside.
//
// The sequence below was verified against real hardware before being written
// here. Two details are load-bearing and neither is obvious:
//
//   - SP_DEVICE_INTERFACE_DATA.cbSize must be 32. The size the documentation
//     implies is 24, and 24 fails; the correct value is unsafe.Sizeof of the
//     struct as declared, which is what the code uses.
//   - The detail buffer is a DWORD cbSize followed by the UTF-16 device path,
//     so the path begins at element 2 of a []uint16 and the buffer must be
//     allocated from the size the probe call reports. The probe call is made
//     with a null output buffer and its ERROR_INSUFFICIENT_BUFFER is the
//     expected answer, not a failure.
//
// The earlier hand-rolled attempt at this failed at exactly this point, with
// SetupDiGetDeviceInterfaceDetailW returning INVALID_USER_BUFFER. The working
// sequence is this one, and it differs from the failing one only in that the
// device-interface struct is declared with the same field order and the same
// trailing uintptr the successful call used.

const (
	ioctlBatteryQueryTag         = 0x294040
	ioctlBatteryQueryInformation = 0x294044
	ioctlBatteryQueryStatus      = 0x29404C
)

// BATTERY_QUERY_INFORMATION_LEVEL values used here.
const (
	queryInformationBasic = 0
)

// BATTERY_STATUS.PowerState bits, from the Win32 documentation.
const (
	powerCharging    = 0x00000001
	powerDischarging = 0x00000002
	powerCritical    = 0x00000004
)

// Sentinels the driver returns in place of a value it cannot supply.
const (
	unknownCapacity = 0xFFFFFFFF
	unknownRate     = -0x80000000
)

type batteryQueryInformation struct {
	BatteryTag       uint32
	InformationLevel int32
	AtRate           int32
}

// batteryInformation mirrors BATTERY_INFORMATION. The field order and widths
// are the driver's, not a choice, and the trailing CycleCount is the reason
// this file exists: it is the field every other binding throws away.
type batteryInformation struct {
	Capabilities        uint32
	Technology          uint8
	Reserved            [3]uint8
	Chemistry           [4]uint8
	DesignedCapacity    uint32
	FullChargedCapacity uint32
	DefaultAlert1       uint32
	DefaultAlert2       uint32
	CriticalBias        uint32
	CycleCount          uint32
}

type batteryWaitStatus struct {
	BatteryTag   uint32
	Timeout      uint32
	PowerState   uint32
	LowCapacity  uint32
	HighCapacity uint32
}

// batteryStatus mirrors BATTERY_STATUS. Rate is signed because the driver
// reports a negative value for a pack taking charge, and Design and Voltage
// arrive in millivolts while Capacity arrives in milliwatt-hours.
type batteryStatus struct {
	PowerState uint32
	Capacity   uint32
	Voltage    uint32
	Rate       int32
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type deviceInterfaceData struct {
	cbSize             uint32
	InterfaceClassGuid guid
	Flags              uint32
	Reserved           uint
}

var guidDeviceBattery = guid{
	Data1: 0x72631e54,
	Data2: 0x78A4,
	Data3: 0x11d0,
	Data4: [8]byte{0xbc, 0xf7, 0x00, 0xaa, 0x00, 0xb7, 0xb3, 0x2a},
}

var (
	modsetupapi                  = windows.NewLazySystemDLL("setupapi.dll")
	procSetupDiGetClassDevsW     = modsetupapi.NewProc("SetupDiGetClassDevsW")
	procSetupDiEnumInterfaces    = modsetupapi.NewProc("SetupDiEnumDeviceInterfaces")
	procSetupDiGetInterfaceDetW  = modsetupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procSetupDiDestroyDeviceList = modsetupapi.NewProc("SetupDiDestroyDeviceInfoList")
)

// enumDeviceInterfaces returns the device path of the idx'th battery interface.
// A zero idx with no error is the only success that matters here; an empty path
// with a nil error means the driver enumerated no device, which is the ordinary
// state of a desktop and is not an error.
func enumDeviceInterfaces(idx uintptr) (path []uint16, err error) {
	hdev, _, errno := procSetupDiGetClassDevsW.Call(
		uintptr(unsafe.Pointer(&guidDeviceBattery)),
		0, 0,
		0x2|0x10, // DIGCF_PRESENT|DIGCF_DEVICEINTERFACE
	)
	if hdev == ^uintptr(0) || hdev == uintptr(windows.InvalidHandle) {
		return nil, errno
	}
	defer procSetupDiDestroyDeviceList.Call(hdev)

	var did deviceInterfaceData
	did.cbSize = uint32(unsafe.Sizeof(did))

	r, _, errno := procSetupDiEnumInterfaces.Call(
		hdev, 0,
		uintptr(unsafe.Pointer(&guidDeviceBattery)),
		idx,
		uintptr(unsafe.Pointer(&did)),
	)
	if r == 0 {
		return nil, errno
	}

	// The size probe passes no output buffer. ERROR_INSUFFICIENT_BUFFER is the
	// documented success for this call, so it is not treated as a failure.
	var required uint32
	r, _, errno = procSetupDiGetInterfaceDetW.Call(
		hdev, uintptr(unsafe.Pointer(&did)), 0, 0,
		uintptr(unsafe.Pointer(&required)), 0,
	)
	// LazyProc.Call reports success as errno == ERROR_SUCCESS, and
	// ERROR_INSUFFICIENT_BUFFER is the expected answer to this probe.
	if r == 0 && errno != windows.ERROR_INSUFFICIENT_BUFFER {
		return nil, errno
	}
	if required == 0 {
		return nil, windows.ERROR_INVALID_DATA
	}

	// The detail struct is a DWORD followed by the path, so the path starts at
	// element 2 of a uint16 slice and cbSize must be written into element 0.
	buf := make([]uint16, required/2)
	cbSize := (*uint32)(unsafe.Pointer(&buf[0]))
	if unsafe.Sizeof(uintptr(0)) == 8 {
		*cbSize = 8
	} else {
		*cbSize = 6
	}
	r, _, errno = procSetupDiGetInterfaceDetW.Call(
		hdev, uintptr(unsafe.Pointer(&did)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(required),
		uintptr(unsafe.Pointer(&required)), 0,
	)
	if r == 0 {
		return nil, errno
	}
	return buf[2:], nil
}

// batteryClass is one reading of the battery class driver.
type batteryClass struct {
	DesignWh   float64
	FullWh     float64
	CurrentWh  float64
	Volts      float64
	Watts      float64
	Cycles     int
	PowerState uint32
	RateKnown  bool
}

// queryBatteryClass reads the first battery the driver enumerates. The class
// driver reports capacity in milliwatt-hours and rate in milliwatts, so both
// are converted to the watt-hour and watt units this package uses throughout.
//
// A negative Rate means the pack is taking charge. The magnitude is the useful
// figure either way, because direction is already established by the charging
// flag, whose sign convention is not consistent across platforms.
func queryBatteryClass() (batteryClass, error) {
	var out batteryClass

	path, err := enumDeviceInterfaces(0)
	if err != nil {
		return out, err
	}

	handle, err := windows.CreateFile(
		&path[0],
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0,
	)
	if err != nil {
		return out, err
	}
	defer windows.CloseHandle(handle)

	var tag uint32
	var n uint32
	if err := windows.DeviceIoControl(
		handle, ioctlBatteryQueryTag,
		(*byte)(unsafe.Pointer(&n)), uint32(unsafe.Sizeof(n)),
		(*byte)(unsafe.Pointer(&tag)), uint32(unsafe.Sizeof(tag)),
		&n, nil,
	); err != nil {
		return out, err
	}
	if tag == 0 {
		return out, windows.ERROR_NOT_FOUND
	}

	var info batteryInformation
	q := batteryQueryInformation{BatteryTag: tag, InformationLevel: queryInformationBasic}
	if err := windows.DeviceIoControl(
		handle, ioctlBatteryQueryInformation,
		(*byte)(unsafe.Pointer(&q)), uint32(unsafe.Sizeof(q)),
		(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)),
		&n, nil,
	); err != nil {
		return out, err
	}

	out.DesignWh = float64(info.DesignedCapacity) / 1000
	out.FullWh = float64(info.FullChargedCapacity) / 1000
	out.Cycles = int(info.CycleCount)

	var st batteryStatus
	ws := batteryWaitStatus{BatteryTag: tag}
	if err := windows.DeviceIoControl(
		handle, ioctlBatteryQueryStatus,
		(*byte)(unsafe.Pointer(&ws)), uint32(unsafe.Sizeof(ws)),
		(*byte)(unsafe.Pointer(&st)), uint32(unsafe.Sizeof(st)),
		&n, nil,
	); err != nil {
		return out, err
	}

	out.PowerState = st.PowerState
	if st.Capacity != unknownCapacity {
		out.CurrentWh = float64(st.Capacity) / 1000
	}
	out.Volts = float64(st.Voltage) / 1000
	if st.Rate != unknownRate && st.Rate != 0 {
		out.Watts = abs(float64(st.Rate)) / 1000
		out.RateKnown = true
	}
	return out, nil
}

// readBatteryDetails enriches a Status with the figures only the battery class
// driver holds. It never fails the caller's read: every field is optional, and a
// failure leaves the capability unset rather than reporting a zero.
func readBatteryDetails(s *Status) {
	b, err := queryBatteryClass()
	if err != nil {
		return
	}

	if b.DesignWh > 0 {
		s.DesignWh = b.DesignWh
		s.DesignWhKnown = true
		s.Cap |= CapDesignCapacity
	}
	if b.FullWh > 0 {
		s.FullWh = b.FullWh
		s.FullWhKnown = true
		s.Cap |= CapFullCapacity
	}
	// Health is only meaningful against a design capacity the machine actually
	// reported. A ratio against a zero or invented design figure is a number
	// with no meaning, so it is omitted rather than printed.
	if s.DesignWhKnown && s.FullWhKnown && s.DesignWh > 0 {
		s.HealthPct = s.FullWh / s.DesignWh * 100
		s.HealthKnown = true
		s.Cap |= CapHealth
	}
	if b.Cycles > 0 {
		s.Cycles = b.Cycles
		s.CyclesKnown = true
		s.Cap |= CapCycleCount
	}
	if b.RateKnown {
		s.Watts = b.Watts
		s.WattsKnown = true
		s.Wattage = WattageDevice
		s.Cap |= CapWatts
	}

	// The class driver reports a native power-state word, which is a better
	// answer than the flag byte the status word was synthesised from.
	// GetSystemPowerStatus collapses "discharging" into a low-battery warning
	// and cannot distinguish a full pack on mains from a stalled charge, whereas
	// the driver names the state directly. It replaces the synthesised word only
	// when it actually recognised one, so an unrecognised value leaves the
	// caller's existing word intact rather than blanking it.
	if w := powerStateWord(b.PowerState); w != "" {
		s.Status = w
	}

	// The driver reports present capacity even when the firmware declines to
	// estimate a time remaining, so the two together yield a figure that does
	// not depend on the firmware's willingness to guess. The division is only
	// valid while discharging: a charging pack's capacity is rising, not
	// falling, and capacity over charge rate is not a duration.
	if !s.TimeToEmptyKnown && b.RateKnown && b.Watts > 0 &&
		s.Present && s.AC == ACOffline && !s.Charging {
		if hours := b.CurrentWh / b.Watts; plausibleSeconds(int64(hours * 3600)) {
			s.TimeToEmptyS = int64(hours * 3600)
			s.TimeToEmptyKnown = true
			s.Cap |= CapTimeRemaining
		}
	}
}

// powerStateWord maps the driver's native power-state bits to a status word.
// An unrecognised value yields an empty string, which the caller treats as "no
// opinion" rather than as "unknown": the two are different facts, and only the
// platform can say which one it meant.
func powerStateWord(state uint32) string {
	switch {
	case state&powerCharging != 0:
		return "Charging"
	case state&powerDischarging != 0:
		return "Discharging"
	case state&powerCritical != 0:
		return "Critical"
	case state == 0:
		// The driver sets no bit at all when the pack is full and idle on mains.
		return "Full"
	default:
		return ""
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
