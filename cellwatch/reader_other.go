//go:build !windows && !linux && !darwin

package cellwatch

// cellwatch ships readers for the three operating systems the host project
// releases for. This file exists so that a build for anything else still
// compiles and says so plainly, rather than failing at link time with an
// undefined newReader. It follows the same shape as directio_other.go, which
// returns an explicit error instead of pretending a capability exists.
func newReader() (Reader, error) { return nil, ErrUnsupported }

// init leaves capabilityFloor at zero, so PlatformCapabilities reports nothing
// on an unsupported platform. That is the honest answer and is the reason the
// variable is zero-valued rather than defaulted.
func init() { capabilityFloor = 0 }
