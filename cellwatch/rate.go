package cellwatch

import (
	"math"
	"time"
)

// Rate estimation is the most error-prone thing this package does, and the
// reason is a property of the platform rather than of the code.
//
// Windows reports charge as a whole number of percentage points and no wattage
// at all. A 50 Wh pack drawing 10 W loses 0.2% per second, which is below the
// one-point reporting quantum. So a two-sample delta taken one second apart
// almost always reads zero, and the occasional sample that straddles a boundary
// reads a full point in two seconds, which extrapolates to a nonsensical rate.
// Both answers are confident and both are wrong, and a naive implementation
// alternates between them.
//
// Measured on real hardware in September 2026: a machine moving at about one
// percentage point per seventy seconds. That measurement is why the defaults
// below span minutes rather than seconds. Estimating over a shorter window than
// the quantisation does not average the noise out, it manufactures it.

// RateParams configures the estimator. The defaults are set for the measured
// platform behaviour rather than for a round number.
type RateParams struct {
	// MinSpan is the shortest interval over which a rate is considered
	// measured. Below this the result is a quantiser reading.
	MinSpan time.Duration

	// MinSamples is the smallest number of readings that can produce a rate.
	MinSamples int

	// Alpha is the weight given to a new observation in the exponential
	// moving average applied to the displayed figure. Lower is smoother.
	Alpha float64
}

// DefaultRateParams spans roughly ten minutes at the default sample interval,
// which is comfortably longer than the seventy-second quantum measured on
// Windows hardware. An estimator tuned to react quickly is an estimator that
// reports fiction.
func DefaultRateParams(interval time.Duration) RateParams {
	return RateParams{
		MinSpan:    10 * interval,
		MinSamples: 12,
		Alpha:      0.25,
	}
}

// Sample is one observation in a rate window.
type Sample struct {
	At     time.Time
	Pct    float64
	AC     ACState
	Charge bool
}

// Rate is an estimated rate of change of charge, in percentage points per hour.
// Positive means the pack is losing charge.
type Rate struct {
	// PctPerHour is the smoothed estimate used for display.
	PctPerHour float64

	// RawPctPerHour is the un-smoothed figure computed across the window
	// endpoints. It is reported alongside the smoothed one so a consumer can
	// see how much smoothing was applied, rather than being handed a number
	// with no indication that it is a moving average.
	RawPctPerHour float64

	// Stable reports whether enough history accumulated to trust the figure.
	Stable bool

	// Span is the interval the estimate was computed over.
	Span time.Duration

	// Samples is how many readings contributed.
	Samples int

	// Reason explains a non-Stable result in a form fit for display, so a UI
	// can say why it is waiting rather than showing a wrong number.
	Reason string
}

// rateWindow accumulates samples and estimates the rate of change of charge.
type rateWindow struct {
	samples []Sample
	ema     float64
	primed  bool
}

// add appends a sample, discarding anything that cannot contribute. A sample
// with no charge reading carries no rate information, and a gap larger than a
// few intervals invalidates the window because the machine may have slept.
func (w *rateWindow) add(s Sample, maxGap time.Duration) {
	if !s.At.IsZero() && math.IsNaN(s.Pct) {
		return
	}
	if n := len(w.samples); n > 0 {
		if gap := s.At.Sub(w.samples[n-1].At); gap <= 0 || gap > maxGap {
			w.reset()
		}
	}
	w.samples = append(w.samples, s)
}

// reset drops the accumulated history, which is what a sleep, a suspend or a
// change of power source requires. Bridging any of those with an estimator
// would average two different physical situations into one number.
func (w *rateWindow) reset() {
	w.samples = w.samples[:0]
	w.ema = 0
	w.primed = false
}

// rate computes the current estimate over the retained window.
func (w *rateWindow) rate(p RateParams) Rate {
	r := Rate{Samples: len(w.samples)}
	if len(w.samples) < 2 {
		r.Reason = "collecting samples"
		return r
	}

	first, last := w.samples[0], w.samples[len(w.samples)-1]
	span := last.At.Sub(first.At)
	r.Span = span

	// Reject a window that spans a change of power source. The charge curve is
	// genuinely discontinuous at that point and no estimator is entitled to
	// bridge it.
	for _, s := range w.samples[1:] {
		if s.AC != first.AC || s.Charge != first.Charge {
			r.Reason = "power source changed, restarting window"
			return r
		}
	}

	if r.Samples < p.MinSamples {
		r.Reason = "collecting samples"
		return r
	}
	if span < p.MinSpan {
		r.Reason = "window too short to be meaningful"
		return r
	}

	// A rising charge is a negative drain, which callers want to see as such.
	delta := first.Pct - last.Pct
	raw := delta / span.Hours()
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		r.Reason = "insufficient change to measure"
		return r
	}

	// A zero delta is a real observation, not a missing one, so a full pack on
	// mains is a measured zero rather than a failure to measure. The two mean
	// opposite things to a user and must not be rendered alike.
	r.Stable = true
	r.RawPctPerHour = raw
	if !w.primed {
		w.ema, w.primed = raw, true
	} else {
		w.ema = p.Alpha*raw + (1-p.Alpha)*w.ema
	}
	r.PctPerHour = w.ema
	return r
}

// RateEstimator accumulates readings and reports the rate of change of charge.
// It is the exported form of the window logic above, for callers that sample
// over time rather than analysing a slice they already hold.
//
// The zero value is not usable; construct one with NewRateEstimator so the
// window parameters are explicit rather than silently defaulted.
type RateEstimator struct {
	w   rateWindow
	p   RateParams
	gap time.Duration
}

// NewRateEstimator returns an estimator sampling at the given interval. The
// window is sized from that interval, and is deliberately long: a window
// shorter than the platform's charge quantisation does not average the noise
// away, it manufactures it.
func NewRateEstimator(interval time.Duration) *RateEstimator {
	return &RateEstimator{
		p:   DefaultRateParams(interval),
		gap: max(10*interval, 2*time.Minute),
	}
}

// Add records one reading, discarding the window if the gap since the previous
// reading indicates a sleep or a suspend. Averaging across one of those
// produces a rate for a machine that never existed.
func (e *RateEstimator) Add(s Sample) { e.w.add(s, e.gap) }

// Rate returns the current estimate and why it is or is not trustworthy.
func (e *RateEstimator) Rate() Rate { return e.w.rate(e.p) }

// Reset drops accumulated history, which a caller does after a power source
// change or when it wants a fresh measurement.
func (e *RateEstimator) Reset() { e.w.reset() }
