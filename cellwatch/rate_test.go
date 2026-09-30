package cellwatch

import (
	"math"
	"testing"
	"time"
)

var epoch = time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

// A window that reproduces the behaviour measured on real hardware in
// September 2026: a laptop moving one percentage point every seventy seconds,
// which is what a naive two-sample estimator reads as zero most of the time and
// as an implausible spike occasionally.
func measuredWindows(interval time.Duration, steps int) []Sample {
	out := make([]Sample, 0, steps)
	step := 70 * time.Second
	for i := 0; i < steps; i++ {
		pct := 68.0 - float64(i)*interval.Seconds()/step.Seconds()
		out = append(out, Sample{At: epoch.Add(time.Duration(i) * interval), Pct: pct, AC: ACOffline})
	}
	return out
}

func rateOf(samples []Sample, p RateParams) Rate {
	var w rateWindow
	for i, s := range samples {
		w.add(s, 10*time.Minute)
		if i == len(samples)-1 {
			return w.rate(p)
		}
	}
	return Rate{}
}

func TestRateRejectsWindowShorterThanQuantum(t *testing.T) {
	// The regression this guards: with a short window the estimator used to
	// report either zero or an implausible spike, because the platform's one
	// percent quantum is larger than the window itself.
	p := DefaultRateParams(2 * time.Second)
	short := measuredWindows(2*time.Second, 5)

	r := rateOf(short, p)
	if r.Stable {
		t.Errorf("a five-sample window must not be reported as stable, got %+v", r)
	}
	if r.Reason == "" {
		t.Error("an unstable rate must explain itself, got an empty reason")
	}
}

func TestRateStabilisesOnceWindowExceedsQuantum(t *testing.T) {
	p := DefaultRateParams(2 * time.Second)
	// Twenty minutes is far longer than the seventy-second quantum, so a real
	// rate should emerge.
	r := rateOf(measuredWindows(2*time.Second, 600), p)

	if !r.Stable {
		t.Fatalf("a long window should stabilise, got %+v", r)
	}
	// 1% per 70s is about 51.4 %/h.
	if r.RawPctPerHour < 45 || r.RawPctPerHour > 58 {
		t.Errorf("raw rate = %.1f %%/h, want roughly 51.4 (1%% per 70s)", r.RawPctPerHour)
	}
}

func TestRateSurvivesACTransition(t *testing.T) {
	p := DefaultRateParams(2 * time.Second)
	samples := measuredWindows(2*time.Second, 600)
	// The pack is unplugged halfway through. Averaging across that point would
	// blend a discharging curve and a charging curve into one meaningless
	// number.
	for i := 300; i < len(samples); i++ {
		samples[i].AC = ACOnline
		samples[i].Charge = true
	}

	r := rateOf(samples, p)
	// The window may report a rate once it has refilled from the samples taken
	// after the change, but the window it reports must not reach back across
	// the change. Demanding no rate at all was the wrong assertion, because the
	// estimator satisfied it by keeping the change in the window and refusing
	// on every subsequent call, which is the permanent failure the package's
	// own regression test condemns. These checks fail against that behaviour,
	// so they pin the invariant the old assertion could only satisfy by
	// accident.
	if r.Samples > len(samples)-300 {
		t.Errorf("the reported figure covers %d readings, more than the %d taken after the transition, so the window spans the power source change",
			r.Samples, len(samples)-300)
	}
	if r.Span > time.Duration(len(samples)-301)*2*time.Second {
		t.Errorf("span %s reaches back across the transition at sample 300", r.Span)
	}
	if !r.Stable {
		t.Fatalf("the window should refill from the post-transition samples and stabilise, got %+v", r)
	}
}

func TestRateDoesNotAverageAcrossSuspendGap(t *testing.T) {
	p := DefaultRateParams(2 * time.Second)

	// Before the sleep the pack drains fast, at roughly 5 points per 70s.
	// After waking it drains slowly, at roughly 1 point per 70s. Averaging the
	// two sides of a suspend would produce a number describing a machine that
	// never existed, so the estimator must discard the earlier history and
	// report only what it observed after the gap.
	pre := make([]Sample, 0, 300)
	for i := 0; i < 300; i++ {
		pre = append(pre, Sample{
			At:  epoch.Add(time.Duration(i) * 2 * time.Second),
			Pct: 80 - float64(i)*2*(time.Second).Seconds()/14,
			AC:  ACOffline,
		})
	}
	post := make([]Sample, 0, 300)
	for i := 0; i < 300; i++ {
		post = append(post, Sample{
			At:  epoch.Add(time.Duration(i)*2*time.Second + time.Hour),
			Pct: 40 - float64(i)*2*(time.Second).Seconds()/70,
			AC:  ACOffline,
		})
	}

	r := rateOf(append(append([]Sample{}, pre...), post...), p)
	if !r.Stable {
		t.Fatalf("post-suspend window should stabilise, got %+v", r)
	}
	// 1 point per 70s is about 51.4 %/h. Anything near the pre-suspend rate of
	// roughly 257 %/h would mean the gap was bridged.
	if r.RawPctPerHour > 80 {
		t.Errorf("rate %.1f %%/h suggests the suspend gap was bridged; want the post-suspend rate near 51", r.RawPctPerHour)
	}
	if r.Span > 30*time.Minute {
		t.Errorf("span %s exceeds the post-suspend window, so pre-suspend samples leaked in", r.Span)
	}
}

func TestRateFlatWindowIsGenuineZero(t *testing.T) {
	p := DefaultRateParams(2 * time.Second)
	// A full pack on mains holds a steady percentage. That is a real
	// observation and must be reported as a stable zero, not as a failure to
	// measure, because the two mean opposite things to a user.
	samples := make([]Sample, 0, 400)
	for i := 0; i < 400; i++ {
		samples = append(samples, Sample{At: epoch.Add(time.Duration(i) * 2 * time.Second), Pct: 100, AC: ACOnline, Charge: true})
	}

	r := rateOf(samples, p)
	if !r.Stable {
		t.Fatalf("a flat window is a measurement, got %+v", r)
	}
	if r.RawPctPerHour != 0 {
		t.Errorf("flat window rate = %.4f, want exactly 0", r.RawPctPerHour)
	}
}

func TestRateIsNegativeWhenCharging(t *testing.T) {
	p := DefaultRateParams(2 * time.Second)
	samples := make([]Sample, 0, 400)
	for i := 0; i < 400; i++ {
		samples = append(samples, Sample{
			At:     epoch.Add(time.Duration(i) * 2 * time.Second),
			Pct:    40 + float64(i)*(2*time.Second).Seconds()/70,
			AC:     ACOnline,
			Charge: true,
		})
	}

	r := rateOf(samples, p)
	if !r.Stable {
		t.Fatalf("charging window should stabilise, got %+v", r)
	}
	if r.RawPctPerHour >= 0 {
		t.Errorf("a charging pack must report a negative drain rate, got %.2f", r.RawPctPerHour)
	}
}

func TestRateEMAStaysWithinObservedRange(t *testing.T) {
	p := DefaultRateParams(2 * time.Second)
	var w rateWindow
	samples := measuredWindows(2*time.Second, 600)

	var minRaw, maxRaw = math.Inf(1), math.Inf(-1)
	for i, s := range samples {
		w.add(s, 10*time.Minute)
		r := w.rate(p)
		if !r.Stable {
			continue
		}
		if r.RawPctPerHour < minRaw {
			minRaw = r.RawPctPerHour
		}
		if r.RawPctPerHour > maxRaw {
			maxRaw = r.RawPctPerHour
		}
		// A moving average cannot leave the range of the observations that fed
		// it. If it does, the alpha is misapplied.
		if r.PctPerHour < minRaw-1e-6 || r.PctPerHour > maxRaw+1e-6 {
			t.Fatalf("sample %d: EMA %.3f outside observed range [%.3f, %.3f]", i, r.PctPerHour, minRaw, maxRaw)
		}
	}
}

func TestRateNeedsAtLeastTwoSamples(t *testing.T) {
	p := DefaultRateParams(2 * time.Second)
	if r := rateOf([]Sample{{At: epoch, Pct: 50}}, p); r.Stable {
		t.Error("a single sample cannot produce a rate")
	}
	if r := rateOf(nil, p); r.Stable {
		t.Error("no samples cannot produce a rate")
	}
}
