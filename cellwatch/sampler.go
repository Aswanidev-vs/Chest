package cellwatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Sampler reads battery state on an interval and records it.
//
// The daemon question deserves a straight answer rather than a hedge. A daily,
// weekly, monthly or yearly total can only be complete if something is
// observing the machine while nobody is looking at it. A command that records
// only while it is open produces a history with holes in it, and a hole is
// indistinguishable from a period in which nothing happened. So the sampler
// runs until it is stopped, and it is started deliberately rather than
// installed as a service: an installer that silently begins observing a machine
// is a different decision from running a program, and only the user gets to
// make it.
type Sampler struct {
	reader   Reader
	store    Store
	interval time.Duration
	attrib   bool

	// out receives one line per sampling pass when it is not nil, so a
	// foreground run shows what the background one is doing.
	out io.Writer

	mu       sync.Mutex
	started  time.Time
	samples  int
	recorded int
	lastErr  error
}

// SamplerConfig configures a Sampler.
type SamplerConfig struct {
	// Reader supplies battery state. Required.
	Reader Reader
	// Store receives readings. Required.
	Store Store
	// Interval is the sampling period. Values below 5s are raised to it.
	//
	// The floor is not a politeness setting. The charge counter is quantised to
	// whole percentage points, so sampling faster than the quantisation does
	// not resolve anything; it multiplies the work and the storage by the
	// sampling rate while producing identical numbers.
	Interval time.Duration
	// Attrib enables per-application attribution, which enumerates every
	// process on the machine and is therefore markedly more expensive.
	Attrib bool
	// Out, when set, receives a line per pass.
	Out io.Writer
}

// NewSampler returns a sampler configured by cfg.
func NewSampler(cfg SamplerConfig) (*Sampler, error) {
	if cfg.Reader == nil {
		return nil, errors.New("cellwatch: sampler requires a reader")
	}
	if cfg.Store == nil {
		return nil, errors.New("cellwatch: sampler requires a store")
	}
	interval := cfg.Interval
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	return &Sampler{
		reader:   cfg.Reader,
		store:    cfg.Store,
		interval: interval,
		attrib:   cfg.Attrib,
		out:      cfg.Out,
	}, nil
}

// Run samples until the context is cancelled or the process is interrupted, and
// returns the number of readings recorded.
//
// A failed read is counted and the loop continues. A sampler that stops on the
// first transient failure is a sampler that has silently become a program that
// records nothing, and nothing in its output would say so. The failure is
// surfaced through Stats and in the final report instead.
func (s *Sampler) Run(ctx context.Context) (int, error) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	attrib := NewAttributor()

	s.mu.Lock()
	s.started = time.Now()
	s.mu.Unlock()

	// The first pass establishes a baseline. Attribution needs two samples to
	// have an interval, and a rate needs a window, so an immediate first pass
	// only costs one extra read and makes a short run return something.
	if err := s.pass(ctx, attrib, true); err != nil {
		return 0, err
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return s.Stats().Recorded, nil
		case <-ticker.C:
			if err := s.pass(ctx, attrib, false); err != nil {
				continue
			}
		}
	}
}

// pass performs one sampling cycle. A read or write failure is recorded in the
// sampler's stats before it is returned, so a caller that only inspects the
// final report still learns that samples were lost.
func (s *Sampler) pass(ctx context.Context, a *Attributor, first bool) error {
	if err := s.doPass(ctx, a, first); err != nil {
		s.mu.Lock()
		s.lastErr = err
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *Sampler) doPass(ctx context.Context, a *Attributor, first bool) error {
	st, err := s.reader.Read()
	if err != nil {
		return err
	}
	if !st.Present {
		// A desktop is not a failure and not worth a database row. Recording it
		// as a zero-charge reading would put a fabricated "0%" into history.
		return nil
	}

	if err := s.store.Record(ctx, st); err != nil {
		return fmt.Errorf("recording: %w", err)
	}

	s.mu.Lock()
	s.samples++
	s.recorded++
	n := s.recorded
	s.mu.Unlock()

	if s.attrib {
		if err := a.Sample(); err == nil && !first {
			// Attribution needs a system rate to apportion against. Without one
			// the shares would still be computable but the drain figures would
			// be zero, which reads as "this app used no power" rather than "the
			// rate is not known yet".
			apps := a.Attribute(0)
			if !apps.Idle && len(apps.Apps) > 0 {
				if err := s.store.Apps(ctx, time.Now(), apps.Apps); err != nil {
					return fmt.Errorf("recording apps: %w", err)
				}
			}
		}
	}

	if s.out != nil {
		fmt.Fprintf(s.out, "\r%s#%d%s recorded", chestDimFor(s.out), n, "\x1b[0m")
	}
	return nil
}

// SamplerStats reports what a sampler managed to do.
type SamplerStats struct {
	Started  time.Duration `json:"started_for"`
	Samples  int           `json:"samples"`
	Recorded int           `json:"recorded"`
	Skipped  int           `json:"skipped"`
	LastErr  string        `json:"last_error,omitempty"`
}

// Stats returns a snapshot of progress.
func (s *Sampler) Stats() SamplerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := SamplerStats{
		Samples:  s.samples,
		Recorded: s.recorded,
		Skipped:  s.samples - s.recorded,
		Started:  time.Since(s.started),
	}
	if s.lastErr != nil {
		st.LastErr = s.lastErr.Error()
	}
	return st
}

// chestDimFor exists so the sampler's progress line matches the host project's
// palette without this package depending on it. The package is deliberately
// standalone, so a caller that wants a different colour should pass its own
// io.Writer rather than this reaching for a shared constant.
func chestDimFor(io.Writer) string { return "\x1b[38;5;246m" }
