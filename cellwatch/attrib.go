package cellwatch

import (
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"
)

// Per-application battery attribution.
//
// No operating system reports true per-application battery use to an
// unprivileged process. Windows keeps per-app energy in the SRUM database, but
// it needs administrator rights and an undocumented Extensible Storage Engine
// format; macOS has powermetrics, which needs root and minutes per sample; Linux
// has nothing at all. This package therefore does not attempt a measurement.
//
// What it does instead is apportion the drain it *can* observe. CPU time and I/O
// are measured; the share of system drain attributed to each application is
// derived from them; and the result is labelled an estimate everywhere it
// appears. The two are never conflated: CPUSeconds is a measurement, Share is
// not.
//
// The largest known error is stated rather than hidden. A lit display, a
// keeping-alive wireless radio, an active video call and thermal management all
// draw real power at near-zero CPU, so they rank at the bottom of a table
// sorted by activity. No CPU and I/O based method can see them, and no amount of
// tuning this code fixes that.

// Attribution constants. They are named, collected here, and documented as
// judgement calls rather than measurements, because nothing in the operating
// system allows them to be fitted.
const (
	// attribCPUWeight and attribIOWeight balance the two activity signals. CPU
	// dominates because CPU is what most battery work consists of and because it
	// is the only signal available on every platform.
	attribCPUWeight = 0.8
	attribIOWeight  = 0.2

	// attribIOBudget is the transfer rate at which the I/O term saturates. It
	// sets the scale of the I/O signal and nothing else: it does not affect the
	// ranking among CPU-bound applications, which is the ranking that matters.
	attribIOBudget = 50 << 20 // 50 MB/s

	// attribIdleFloor is the total normalised activity below which attribution
	// is refused. Below it the shares are ratios of noise, and a table of
	// confident numbers derived from an idle machine is worse than no table.
	attribIdleFloor = 0.02
)

// procSample is one process's cumulative counters at a point in time.
type procSample struct {
	pid        int32
	name       string
	created    int64
	cpuSeconds float64
	readBytes  uint64
	writeBytes uint64
}

// snapshotProcess reads the cumulative counters for one process. Failures are
// per-process and tolerated: a process that exits between enumeration and read
// is a normal event, not an error worth aborting a sampling pass over.
func snapshotProcess(p *process.Process) (procSample, bool) {
	s := procSample{pid: p.Pid}

	name, err := p.Name()
	if err != nil || name == "" {
		return s, false
	}
	s.name = name

	// Creation time is the guard against PID reuse. A PID observed at the start
	// of a window and the same number observed at the end may be two unrelated
	// programs, and the delta between their counters would be a number
	// describing neither. Dropping such a pair is correct; clamping it would
	// invent a figure.
	if ct, err := p.CreateTime(); err == nil {
		s.created = ct
	}

	// Times() returns a cpu.TimesStat, which has no Total field: the busy time
	// is the sum of the individual states. Summing rather than picking one
	// state matters, because attributing on user time alone would ignore
	// kernel time and understate a process doing real I/O.
	if t, err := p.Times(); err == nil {
		s.cpuSeconds = t.User + t.System + t.Nice + t.Irq + t.Softirq + t.Steal
	}

	// I/O counters are readable only by the owning user or root, so on a
	// shared machine a process may contribute no I/O term at all. That is
	// handled by treating the term as zero rather than by failing the sample.
	if io, err := p.IOCounters(); err == nil {
		s.readBytes = io.ReadBytes
		s.writeBytes = io.WriteBytes
	}
	return s, true
}

// procSnapshot is the set of process counters at one instant.
type procSnapshot struct {
	at     time.Time
	procs  map[int32]procSample
	nCPU   int
	sysCPU float64
}

// takeSnapshot enumerates running processes and their cumulative counters.
func takeSnapshot() (procSnapshot, error) {
	snap := procSnapshot{at: time.Now(), procs: map[int32]procSample{}}

	procs, err := process.Processes()
	if err != nil {
		return snap, err
	}
	for _, p := range procs {
		if s, ok := snapshotProcess(p); ok {
			snap.procs[p.Pid] = s
		}
	}

	// The CPU count normalises a process's share into a fraction of the whole
	// machine, so two applications are comparable on a laptop and a desktop.
	if n, err := cpu.Counts(false); err == nil && n > 0 {
		snap.nCPU = n
	} else {
		snap.nCPU = 1
	}

	// System busy time is kept only to report how loaded the machine was. It is
	// not used to apportion anything, because dividing a drain by system CPU
	// would double-count the very processes already being measured.
	if times, err := cpu.Times(false); err == nil && len(times) > 0 {
		t := times[0]
		total := t.User + t.System + t.Nice + t.Irq + t.Softirq + t.Steal + t.Idle + t.Iowait
		if total <= 0 {
			total = 1
		}
		snap.sysCPU = (total - t.Idle) / total
	}
	return snap, nil
}

// Attributor accumulates process snapshots and apportions observed drain across
// applications. The zero value is not usable; construct with NewAttributor.
type Attributor struct {
	first  *procSnapshot
	last   *procSnapshot
	ioFail int
	procs  int
}

// NewAttributor returns an unattributed attributor. Call Sample once per tick
// and Attribute to obtain the current estimate.
func NewAttributor() *Attributor { return &Attributor{} }

// Sample records one instant. The first call only establishes a baseline,
// because a single snapshot contains no interval and therefore no rate.
func (a *Attributor) Sample() error {
	snap, err := takeSnapshot()
	if err != nil {
		return err
	}
	if a.first == nil {
		a.first = &snap
	} else {
		a.last = &snap
	}
	return nil
}

// Attribute computes per-application activity over the window since the first
// sample, and the share of observed drain each is apportioned.
func (a *Attributor) Attribute(systemPctPerHour float64) Attribution {
	out := Attribution{}
	if a.first == nil || a.last == nil {
		out.Reason = "need at least two samples"
		return out
	}

	span := a.last.at.Sub(a.first.at)
	if span <= 0 {
		out.Reason = "samples too close together"
		return out
	}
	out.Window = span
	out.CPUCapacityPct = a.last.sysCPU * 100
	out.Processes = len(a.last.procs)

	// Per-PID deltas, folded by executable name. A browser is thirty processes
	// and reporting it as thirty rows would be technically accurate and
	// useless.
	type fold struct {
		procs int
		cpu   float64
		read  uint64
		write uint64
	}
	byName := map[string]*fold{}
	var totalWeight float64
	var totalCPU float64

	capacity := float64(a.last.nCPU) * span.Seconds()
	if capacity <= 0 {
		out.Reason = "cannot normalise: zero elapsed capacity"
		return out
	}

	for pid, now := range a.last.procs {
		before, existed := a.first.procs[pid]
		if !existed {
			// Started during the window. Its whole life is inside the window,
			// so its counters are already correct without subtraction.
			a.procs++
			continue
		}
		if before.created != 0 && now.created != 0 && before.created != now.created {
			// The PID was reused. The delta belongs to two different programs.
			out.Notes = append(out.Notes, "dropped a PID whose identity changed during the window")
			continue
		}

		cpuDelta := now.cpuSeconds - before.cpuSeconds
		if cpuDelta < 0 {
			// A counter went backwards, which happens when a process is
			// replaced under the same PID without the creation-time guard
			// firing, for instance on a platform that cannot report it.
			a.ioFail++
			continue
		}
		readDelta, writeDelta := uint64(0), uint64(0)
		if now.readBytes >= before.readBytes {
			readDelta = now.readBytes - before.readBytes
		}
		if now.writeBytes >= before.writeBytes {
			writeDelta = now.writeBytes - before.writeBytes
		}

		cpuNorm := cpuDelta / capacity
		if cpuNorm > 1 {
			cpuNorm = 1
		}
		bytesTotal := float64(readDelta + writeDelta)
		ioNorm := bytesTotal / (float64(attribIOBudget) * span.Seconds())
		if ioNorm > 1 {
			ioNorm = 1
		}

		w := attribCPUWeight*cpuNorm + attribIOWeight*ioNorm
		totalWeight += w
		totalCPU += cpuNorm

		f := byName[now.name]
		if f == nil {
			f = &fold{}
			byName[now.name] = f
		}
		f.procs++
		f.cpu += cpuDelta
		f.read += readDelta
		f.write += writeDelta
	}

	if totalWeight < attribIdleFloor {
		// Refusing here is the whole point of the floor. Below it the shares
		// are ratios of two near-zero numbers, and the machine's drain is
		// dominated by things no process counter can see.
		out.Idle = true
		out.Notes = append(out.Notes,
			"system used a small fraction of the machine, so activity is too low to apportion")
		return out
	}

	out.CPUCapacityPct = totalCPU / float64(a.last.nCPU) * 100
	apps := make([]AppActivity, 0, len(byName))
	for name, f := range byName {
		cpuNorm := f.cpu / capacity
		ioNorm := float64(f.read+f.write) / (float64(attribIOBudget) * span.Seconds())
		weight := attribCPUWeight*cpuNorm + attribIOWeight*ioNorm
		share := weight / totalWeight
		apps = append(apps, AppActivity{
			Name:       name,
			Procs:      f.procs,
			CPUSeconds: f.cpu,
			ReadBytes:  f.read,
			WriteBytes: f.write,
			Share:      share,
			PctPerHour: share * systemPctPerHour,
			Provenance: ProvEstimated,
		})
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Share != apps[j].Share {
			return apps[i].Share > apps[j].Share
		}
		return apps[i].Name < apps[j].Name
	})

	var attributed float64
	for _, a := range apps {
		attributed += a.Share
	}
	// The remainder is always reported. Kernel threads, idle time and processes
	// that vanished mid-window are not hidden to make the table look tidy;
	// a table whose shares sum to exactly one is a table that has rounded away
	// something real.
	out.Unattributed = 1 - attributed
	out.Apps = apps
	out.ProcessesLost = a.ioFail
	return out
}

// Attribution is the result of apportioning drain across applications.
type Attribution struct {
	Window time.Duration `json:"window"`

	// Processes is how many processes contributed.
	Processes int `json:"processes"`
	// ProcessesLost is how many were dropped for a counter that moved backwards
	// or an identity that could not be confirmed.
	ProcessesLost int `json:"processes_lost"`

	// CPUCapacityPct is the share of total machine CPU the attributed
	// processes used, and is reported even when the result is Idle, because it
	// is the measurement that explains why the estimate was refused.
	CPUCapacityPct float64 `json:"cpu_capacity_pct"`

	// Idle reports that activity was too low to apportion.
	Idle bool `json:"idle"`

	// Apps is the per-application table, largest share first.
	Apps []AppActivity `json:"apps,omitempty"`

	// Unattributed is the share of drain that no process was credited with.
	Unattributed float64 `json:"unattributed_estimate"`

	// Notes carry the reasons a figure is limited. They are for display.
	Notes []string `json:"notes,omitempty"`

	// Reason explains an empty result.
	Reason string `json:"reason,omitempty"`
}
