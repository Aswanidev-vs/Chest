# cellwatch -- Design: a standalone, extractable battery history library for Go

> **Status of this document:** design only. No code in the repository has been modified. Every
> API, file path and line reference below was read out of the current tree. Where a behaviour is a
> proposal rather than an existing fact, it is written as a proposal.
>
> **Nothing in `cellwatch/` exists yet.** This document specifies it in full: the package layout,
> the public API, how cellwatch talks to each OS, the per-OS data sources and every trap in them, the
> derived-metric formulas, the capability bitmask, the storage alternatives and the chosen engine's
> schema, the cascading rollup ladder, the per-app attribution algorithm, the historical query
> API, the JSON schema, the tests, the phased delivery plan, and the extraction procedure that will
> turn the directory into its own repository.
>
> **This document replaces an earlier one.** The earlier version designed a `chest battery` CLI
> command backed by `internal/power/`. That architecture is wrong now: the deliverable is a library,
> and CHEST is a consumer of it. Sections carried over unchanged in substance are §3 (platform data
> sources), §4 (derived metrics), §11 (per-app attribution) and the two bug reports in Appendix A.

---

## 0. What is being built

`cellwatch` is a Go package that does three things:

1. **Reads** the machine's power supply state on Linux, macOS and Windows, from the same three
   sources `chest speedtest`'s sibling command would have used, with every platform trap handled
   and every unavailable field reported as unavailable rather than as zero.
2. **Remembers.** A background sampler writes raw observations to a SQLite database of its own and
   cascades them into minute, hour, day, week, month and year aggregates, so that "what did the
   battery do last Tuesday" is a query rather than an omission.
3. **Answers questions** about that history, per application, over day, week, month and year
   windows -- carrying on every single number whether it was measured, derived, imported from a
   platform log, or estimated by apportioning drain across CPU and I/O weight.

It is a library. It has no `main` package, no flags, no colour, no spinner, and no opinion about
where its database file lives. CHEST is one consumer of it; the eventual standalone repository is
the real audience.

```
  caller (CHEST, or anything else)
        |
        |  cellwatch.Usage(ctx, store, cellwatch.Range{...})
        v
  +----------------------------------------------------------+
  |  cellwatch                                             |
  |                                                      |
  |   query layer   Usage  TopApps  AppHistory  Compare      |
  |        |                                             |
  |        v                                             |
  |   store layer    Store (SQLite)  schema  codec  rollup  |
  |        ^                                             |
  |        |                                             |
  |   sampler        Sampler.Run        (opt-in, writes)    |
  |        ^                                             |
  |        |                                             |
  |   platform       Reader  ->  Status  (+ Capability)    |
  |        ^                                             |
  |        |                                             |
  |   native logs    NativeSource  ->  Import (optional)   |
  +----------------------------------------------------------+
        |
        v
   /sys/class/power_supply   GetSystemPowerStatus   ioreg -r -c AppleSmartBattery
   pmset -g log              powercfg /batteryreport /xml      SRUM-DATA.dat (admin)
```

---

## 1. Isolation: same module, extraction-ready

### 1.1 The shape of the decision

| Property | Value |
|---|---|
| Directory | `E:\Chest\cellwatch\` |
| Import path in this repository | `github.com/Aswanidev-vs/chest/cellwatch` |
| Package name | `cellwatch` |
| Own `go.mod` | **No** |
| Own `main.go` | **No** |
| Nested Go module | **No** -- it is a leaf package of `github.com/Aswanidev-vs/chest` (`go.mod:1`) |
| Dependencies | stdlib, `github.com/shirou/gopsutil/v4`, `github.com/ncruces/go-sqlite3` |
| Imports from `github.com/Aswanidev-vs/chest/...` | **Zero** |

The user has explicitly asked for no separate `go.mod` today, and that is the correct call. §2
explains why in the terms that matter to this repository rather than in the abstract.

### 1.2 Why there is no nested `go.mod` today

A nested module is the obvious way to make a directory "extractable", and it is the wrong way for
this repository right now. Three concrete things break:

**1. `go install` ignores `replace`, by design.** The documented install path is

```bash
go install github.com/Aswanidev-vs/chest/cmd/chest@latest
```

(`README.md:17`), and both installer scripts run exactly that
(`install.sh:199-202`, `install.ps1:183-185`). A `replace` directive applies only within the module
that declares it -- this is not a quirk, it is the specified behaviour, because `replace` is how you
point a *local* checkout at a *local* copy of a dependency. So a `cellwatch/go.mod` containing

```
module github.com/Aswanidev-vs/chest/cellwatch
require github.com/Aswanidev-vs/chest v0.0.0
replace github.com/Aswanidev-vs/chest => ../
```

compiles perfectly in a developer checkout and fails for every user who runs `go install`,
because the published root module does not depend on the published `cellwatch` module, and the
release version of `cellwatch` is not resolvable. The failure surfaces as a resolution error or a
missing `go.sum` entry, not as "your install path is broken" -- which is a bad first experience
for the one command in `README.md` that everyone runs.

**2. The release build would silently stop compiling the package.** `.github/workflows/release.yml:57-61`
runs `go build ./cmd/chest` from the repository root. A nested module is excluded from the parent
module's package graph entirely: `./...` does not match into it, `go build ./cmd/chest` does not
pull it in, and `go vet`/`go test ./...` do not touch it. The release pipeline would ship a binary
containing a dependency that CI never compiled and that no test ever ran.

**3. The same blindness applies to `CONTRIBUTING.md:42-45`**, which documents `go test ./...` and
`go test -race ./...` as the project-wide command. With a nested module, that command stops
covering `cellwatch/`.

None of these are hypothetical. They are three separate ways for the package to be outside the
build while looking like it is inside it. So: same module, leaf package, and a set of rules that
make the later extraction a mechanical operation rather than a project.

### 1.3 The five extraction-readiness rules

These are the architecture. The rest of the document is detail.

**Rule 1 -- leaf.** `cellwatch/` imports nothing from `github.com/Aswanidev-vs/chest/...`. Not a
constant, not a helper, not a type. It is a leaf of the dependency graph, which is what makes
`git subtree split` produce a self-contained tree.

**Rule 2 -- stdlib and named third parties only.** The complete import set is the standard library
plus `github.com/shirou/gopsutil/v4` (process sampling) and `github.com/ncruces/go-sqlite3`
(storage). No
transitive dependency of a CHEST package is reachable from here, because Rule 1 forbids reaching it.

**Rule 3 -- no CHEST conventions.** No colour constants from `speedtest.go:21-26`, no glyphs from
`speedui.go:14-17`, no `speedBar`, no `truncate`, no `chestHome`, no `chest.Red`. Any shared value
that both sides genuinely need is written out again on the library side, with its own godoc, because
a shared constant is a dependency.

**Rule 4 -- its own tests, its own fixtures, its own docs.** `cellwatch/*_test.go`,
`cellwatch/testdata/`, and godoc-style comments starting with the symbol's own name on every
exported identifier, written for a reader who has never seen CHEST.

**Rule 5 -- the caller owns the path.** The library exposes `Open(path string, mode Mode, ...)`. It
does not decide that the database lives in `~/.chest`, because when it is extracted that directory
does not exist.

### 1.4 The machine check

Rules 1 and 2 are worth a promise and a test. This test is the difference between a README and a
constraint:

```go
// leaf_test.go
//
// TestNoChestImports is the executable form of extraction rule 1. A README
// that says "do not import from CHEST" is a suggestion; this makes it a
// build failure. It parses the package's own source rather than the build
// graph, so it also catches an import in a file excluded by a build tag on
// the machine running the test.
package cellwatch_test

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

const chestModule = "github.com/Aswanidev-vs/chest"

func TestNoChestImports(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse cellwatch: %v", err)
	}
	for name, pkg := range pkgs {
		for file, astFile := range pkg.Files {
			for _, spec := range astFile.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatalf("%s/%s: bad import %s", name, file, spec.Path.Value)
				}
				if path == chestModule || strings.HasPrefix(path, chestModule+"/") {
					t.Errorf("%s/%s imports %s; cellwatch must stay a leaf so it can be "+
						"split out with `git subtree split --prefix=cellwatch` (rule 1)",
						name, file, path)
				}
			}
		}
	}
}

func TestNoMainPackage(t *testing.T) {
	// A main package inside cellwatch/ would make the directory a command
	// rather than a library, and would survive the subtree split as one.
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.PackageClauseOnly)
	if err != nil {
		t.Fatalf("parse cellwatch: %v", err)
	}
	for name, pkg := range pkgs {
		if name == "main" {
			t.Errorf("cellwatch contains a main package; the directory must be importable only")
		}
	}
}
```

Two more checks belong in CI rather than in a test, because they are about the repository and not
about the package:

```bash
# every symbol carries a doc comment
go vet ./cellwatch
gofmt -l cellwatch
staticcheck -checks=ST1000,ST1020,ST1021,ST1022 ./cellwatch

# the six release targets still build, CGO off
for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  GOOS=${t%/*} GOARCH=${t#*/} CGO_ENABLED=0 go build ./...
done
```

### 1.5 The extraction checklist

When the split happens, and not before, the procedure is:

```bash
# 1. Split the subtree onto its own branch. This rewrites the history of
#    cellwatch/ only, so the branch contains exactly the files that will move.
git subtree split --prefix=cellwatch -b cellwatch-v1

# 2. Give the branch its own bare repository and push it.
git clone --branch cellwatch-v1 --single-branch . ../cellwatch.git
cd ../cellwatch.git
git remote rename origin upstream        # keep a link back to CHEST history
git remote add origin git@github.com:OWNER/cellwatch.git
git push -u origin cellwatch-v1

# 3. The entire go.mod is one line. Everything else survives the split as-is.
go mod edit -module github.com/OWNER/cellwatch
#   module github.com/OWNER/cellwatch
#   go 1.26.0
#   require (
#       github.com/shirou/gopsutil/v4 v4.26.8
#       github.com/ncruces/go-sqlite3 v0.35.4
#   )
#   require golang.org/x/sys v0.47.0 // indirect, via gopsutil
#   (+ indirect: yusufpapurcu/wmi, ebitengine/purego,
#                ncruces/go-sqlite3-wasm/v5, ncruces/julianday)
#
#   ncruces/go-sqlite3 costs nothing *inside CHEST* because CHEST already
#   depends on it. For the extracted module it is three new modules and a
#   4.7 MB source file, and that is the one thing this decision makes
#   worse on extraction. See section 17.1.

# 4. Add the files that cannot come from a subtree split, because they were
#    never in cellwatch/:
#    README.md, LICENSE, CHANGELOG.md, .github/workflows/ci.yml

# 5. In CHEST, point at the local copy during development, then at a tag.
go mod edit -require=github.com/OWNER/cellwatch@v0.1.0
go mod edit -replace=github.com/OWNER/cellwatch=../cellwatch
```

### 1.6 What must be true for §1.5 to work

| # | Condition | How it is checked |
|---|---|---|
| 1 | No file under `cellwatch/` imports the CHEST module | `TestNoChestImports` |
| 2 | No `main` package under `cellwatch/` | `TestNoMainPackage` |
| 3 | No file under `cellwatch/` references a CHEST internal path in a comment, a string, or a doc link | `grep -rn "Aswanidev-vs/chest/internal" cellwatch/` -- expected: no matches, apart from the one line in the CHEST-side consumer section below |
| 4 | Every exported symbol has a godoc comment beginning with its own name | `staticcheck -checks=ST1020,ST1021,ST1022` |
| 5 | `go vet` and `gofmt` clean | `go vet ./cellwatch`, `gofmt -l cellwatch` |
| 6 | All test fixtures are inside `cellwatch/testdata/` | `ls cellwatch/testdata`; no test references `../internal/...` |
| 7 | All six release targets compile with `CGO_ENABLED=0` | the loop in §1.4 |
| 8 | `go.mod` of the new module lists only the SQLite driver and gopsutil | `go list -m all` in the new repo |
| 9 | The `LICENSE` is compatible and present in both trees | `head -3 LICENSE` in both |

Condition 3 is the one that is easy to miss and impossible to automate away: a doc comment that
says "see `internal/cli/speedui.go` for the bar renderer" is a broken link the moment the subtree
splits. In `cellwatch/` every reference is either to a standard library package, to a third-party
package, or to another file in the same directory.

### 1.7 The CHEST side of the boundary

CHEST consumes the library from `internal/cli/`, and the consumption is thin:

```go
// internal/cli/battery.go
import "github.com/Aswanidev-vs/chest/cellwatch"
```

Nothing in `internal/` is changed to accommodate `cellwatch`, and `cellwatch` is not given a
CHEST-shaped helper to make the CLI's job easier. The seam is the public API and nothing else. The
concrete wiring is §17.

---

## 2. Public API surface

### 2.1 Package layout

```
cellwatch/
  doc.go                    package documentation: guarantees, non-goals, honesty rules
  leaf_test.go              the machine check from 1.4

  cellwatch.go              Status, ACState, Reader, NewReader, errors
  capability.go             Capability bitmask, WattageSource, String methods
  capability_{windows,linux,darwin,other}.go    per-platform capability declarations
  reader_{windows,linux,darwin,other}.go       the three platform readers

  rate.go                   pure: window estimator, EMA, drain and remaining maths
  attrib.go                 pure: grouping, deltas, apportionment
  attrib_sample.go          gopsutil process sampling -- I/O only, no logic
  rapl_linux.go             measured CPU-package energy (Intel RAPL)

  native.go                 NativeSource, NativeRecord, Import
  native_darwin.go          pmset -g log / -g rawlog
  native_windows.go         powercfg /batteryreport /xml
  srum_windows.go           SRUM-DATA.dat, admin-gated, best-effort

  sampler.go                Sampler, SamplerConfig, Run
  range.go                  Range, Period, Tier, Weekday, ThisPeriod
  series.go                 Point, Series, Provenance, Coverage
  usage.go                  Usage, TopApps, AppHistory, Compare

  store.go                  the Store interface, Open, Mode, Option, Retention
  schema.go                 the DDL and the PRAGMA setup, pure
  codec.go                  the packed app-slice codec, unexported
  store_sqlite.go           the SQLite implementation
  rollup.go                 the cascading ladder, staleness, pruning

  *_test.go
  testdata/
    linux-energy_full.txt       real /sys/class/power_supply dump, energy_* layout
    linux-charge_full.txt       real dump, charge_* layout, no power_now
    linux-no-battery.txt        real desktop dump
    linux-rapl.txt              real /sys/class/powercap tree
    darwin-ioreg.txt            real ioreg -r -c AppleSmartBattery output
    darwin-ioreg-charging.txt   real output while charging, Amperage positive
    darwin-pmset-log.txt        real pmset -g log output
    windows-powercfg.xml        real powercfg /batteryreport /xml output
```

Twenty-two source files, of which six are build-tagged platform variants of the same three
concerns. The split matches the pattern already in the tree
(`internal/scanner/hidden_windows.go` versus `hidden_other.go`, named at `CONTRIBUTING.md:92`), and
`cellwatch_test.go` parses all files including build-tagged ones, so the layout is checkable.

### 2.2 What the package guarantees

Stated up front, because a library's guarantees are its API.

1. **A field the platform cannot supply is absent, not zero.** Every optional value carries a
   `Known` boolean or a `Capability` bit. A caller can always distinguish "zero watts" from "no
   wattage available" without parsing prose.
2. **Parsers fail closed.** An unreadable or unparseable platform stream returns an error. It never
   returns a zero-valued `Status` that renders as "0 %". This is the rule that turns an `ioreg`
   format change into a visible error instead of a plausible lie.
3. **Every derived figure declares how it was derived.** `Provenance` is on every `Point`, every
   `AppTotal` and every `SystemUsage`. A caller cannot accidentally present an estimate as a
   measurement, because the estimate is labelled in the value itself rather than in a footnote.
4. **Every query returns its coverage.** A `Series` without a `Coverage` is not constructible
   outside this package, so "we only had four minutes of data last Tuesday" is a property of the
   answer rather than a discovery.
5. **Rollups are recomputations, not accumulations.** A stored aggregate is a pure function of the
   records below it. Running the rollup twice produces the same bytes as running it once.
6. **The library never starts a background goroutine you did not ask for.** `NewSampler` does not
   start anything; `Run` does, and only when you call it.
7. **The library never chooses a file path.** `Open` takes one.
8. **The library never writes outside the path it was given**, and never outside the process's own
   temporary directory in tests.

### 2.3 Non-goals

| Not doing | Why |
|---|---|
| Charging, throttling, or battery-policy control | This is a measurement library. A library that writes to the machine's power settings is a different, more dangerous product. |
| A GUI, a TUI, a notification daemon, or a tray icon | Consumer concerns. §16 shows what CHEST builds on top. |
| Choosing a default database path | The library does not know the host application's home directory. |
| User-defined app-folding rules from a config file | A configuration feature belongs to the consumer. The folding table is a package-level table with a documented merge function. |
| Per-package or per-device energy on non-Intel hardware | RAPL is Intel-specific. Pretending otherwise would be a fabricated capability. |
| Real per-application battery drain | No mainstream operating system exposes it. §7 and §11 are the long version of why. |
| Streaming or a live query API | A `Range` query against a local file is already fast enough; a subscription API would be a second write path through the single-writer lock for no benefit. |

### 2.4 Core types

```go
// Package cellwatch reads the machine's power supply, records it over time, and
// answers questions about the record.
//
// # What it measures and what it estimates
//
// Charge percentage, mains state, pack capacity and cycle count are read from
// the platform and are measurements. Drain rate, time remaining, and every
// per-application figure are computations, and no operating system reports
// per-application battery use. Every value cellwatch produces carries a
// Provenance saying which it is. See the package's "Honesty" section in doc.go
// for the full statement.
package cellwatch

// ACState is the mains-line state of the machine.
type ACState int

const (
	// ACUnknown means the platform did not report a mains state.
	ACUnknown ACState = iota
	// ACOn means the machine is running from mains.
	ACOn
	// ACOff means the machine is running from its battery.
	ACOff
)

// String returns "on", "off" or "unknown".
func (a ACState) String() string

// Status is one reading of the machine's power supply.
//
// A field the platform cannot supply is left at its zero value and its Known
// flag is false. It is never filled with a plausible-looking guess: there is
// no path in this package that produces a HealthPct the platform did not
// report, or a Watts figure derived from a nominal pack voltage.
//
// Status describes a single instant. Nothing in it is smoothed, averaged or
// carried forward across reads.
type Status struct {
	// Present is false when the platform reported no battery. That is a
	// normal answer for a desktop, not a failure.
	Present bool

	// Count is the number of batteries the platform reported. Windows
	// GetSystemPowerStatus reports only the first pack, so a two-battery
	// machine reads 1.
	Count int

	// Source names the mechanism that produced this reading, for example
	// "GetSystemPowerStatus", "/sys/class/power_supply" or
	// "ioreg:AppleSmartBattery". It is a diagnostic, not a stable API, and
	// callers should not switch on it.
	Source string

	// AC is the mains-line state.
	AC ACState

	// Status is the platform's own status word, verbatim: "Charging",
	// "Discharging", "Full", "Not charging", "Unknown". Empty when the
	// platform has no such word.
	Status string

	// Charging is true when the pack is taking charge. It is taken from the
	// platform's own charging flag and never inferred from the sign of a
	// current reading.
	Charging bool

	// Full is true when the platform reports the pack fully charged.
	Full bool

	// ChargePct is charge state, 0 to 100. Known is false when the platform
	// reported the value as unknown.
	ChargePct  float64
	ChargeKnown bool

	// Watts is instantaneous power draw. Prefer WattageSource to judge it:
	// WattageSysfs and WattageIOReg are direct platform reads, and
	// WattageChargeDelta is a figure derived from a 1-percentage-point
	// counter and is only as good as the window behind it.
	Watts      float64
	WattsKnown bool
	Wattage    WattageSource

	// CPUWatts is measured CPU-package power, and CPUWattsKnown says whether
	// it was available. It is separate from Watts because it covers the
	// processor packages only: putting it in Watts would understate the
	// machine by the display, the radio and the disk.
	CPUWatts      float64
	CPUWattsKnown bool

	// FullWh is the pack's present full-charge capacity, and DesignWh its
	// factory figure. Both are zero and both Known flags are false when the
	// platform cannot supply them, which on Windows means always unless the
	// caller has separately obtained them from an elevated device IOCTL.
	FullWh      float64
	FullKnown   bool
	DesignWh    float64
	DesignKnown bool

	// HealthPct is FullWh as a percentage of DesignWh.
	HealthPct   float64
	HealthKnown bool

	// CycleCount is the number of completed charge cycles.
	CycleCount  int
	CycleKnown  bool

	// OSRemaining is the operating system's own time-remaining estimate.
	// It is zero and OSRemainingKnown is false when the platform has none, or
	// when the platform's value is one of its documented sentinels.
	OSRemaining      time.Duration
	OSRemainingKnown bool

	// TempC is the pack temperature, not a CPU temperature.
	TempC    float64
	TempKnown bool

	// Notes are human-readable qualifications about this reading, rendered
	// as dim footnotes. They are not errors: a reading with notes is still a
	// valid reading.
	Notes []string
}
```

Errors:

```go
var (
	// ErrNoBattery means the platform reported no battery. This is a normal
	// answer for a desktop, not a failure, and callers should treat it as
	// exit-zero rather than as an error.
	ErrNoBattery = errors.New("cellwatch: no battery detected")

	// ErrUnsupported means this GOOS has no reader compiled in. It is
	// returned by NewReader and by OpenSRUM on platforms that have no
	// equivalent facility.
	ErrUnsupported = errors.New("cellwatch: not implemented for this platform")

	// ErrNeedsElevation means the source exists but this process may not read
	// it. It is returned by OpenSRUM and never by Open, because the
	// battery readers themselves need no elevation anywhere.
	ErrNeedsElevation = errors.New("cellwatch: source requires administrator privileges")

	// ErrNoAppEnergySource means no per-application energy source is
	// available at all on this platform, which is the answer on macOS and
	// Linux and the answer on Windows until SRUM has been opened.
	ErrNoAppEnergySource = errors.New("cellwatch: no per-application energy source on this platform")

	// ErrImportUnsupported means the Store does not implement the write seam
	// that Import needs. It is returned rather than panicking, so a custom
	// Store implementation is not required to support import.
	ErrImportUnsupported = errors.New("cellwatch: this Store does not support native-log import")

	// ErrInsufficientCoverage is returned by Compare when either window's
	// coverage is below Coverage.Usable's threshold. It is returned instead of
	// a delta because the difference between a full week and four minutes of
	// data is arithmetic, not information.
	ErrInsufficientCoverage = errors.New("cellwatch: coverage too low to compare")

	// ErrLocationMismatch is returned by Open when the database records a
	// timezone and a different one is requested. Bucket boundaries are local
	// even though keys are UTC, so a changed zone writes different keys for
	// the same day; making that an error is cheaper than discovering it in a
	// year of history. WithRelocate opts in deliberately.
	ErrLocationMismatch = errors.New("cellwatch: database was written in a different timezone")

	// ErrClosed is returned by every method on a Store whose Close has run.
	ErrClosed = errors.New("cellwatch: store is closed")
)
```

`ErrNoBattery` is deliberately *not* what `Read` returns on Windows or Linux when the mains state
is still knowable. The Windows reader sets `Present: false, AC: ACOn` and returns `nil`, so a
desktop still gets a useful answer. macOS has no such case: if there is no `AppleSmartBattery`
object there is no answer at all, and the reader returns an error naming the fact.

### 2.5 The capability bitmask

The problem this solves: a caller asking for `status.HealthPct` on Windows gets `0`, and cannot
tell whether the pack is brand new or the platform simply does not know. A bitmask lets the caller
ask the question and get "no" as a first-class answer, before the read.

```go
// Capability is a bit set naming the facts a platform can report. Ask before
// you read: a bit that is not set means the value is unavailable and will read
// as its zero value.
//
// Capability describes the PLATFORM. The Known booleans on Status describe
// the SAMPLE. The two are not the same question and conflating them is the
// most common way a caller ends up over-claiming. Linux sets CapWatts,
// because the power_supply subsystem can report watts; an individual read on
// a driver without power_now still returns WattsKnown == false.
type Capability uint32

const (
	// CapChargePct reports charge state as a percentage, 0 to 100.
	CapChargePct Capability = 1 << iota

	// CapACState reports whether the machine is on mains.
	CapACState

	// CapStatusWord reports the platform's own status string verbatim.
	CapStatusWord

	// CapWatts reports instantaneous power draw for the whole machine.
	CapWatts

	// CapFullCapacity reports the pack's present full-charge capacity in Wh.
	CapFullCapacity

	// CapDesignCapacity reports the pack's factory design capacity in Wh.
	CapDesignCapacity

	// CapHealth reports full capacity as a percentage of design capacity.
	CapHealth

	// CapCycleCount reports the number of completed charge cycles.
	CapCycleCount

	// CapOSRemaining reports the operating system's own time-remaining estimate.
	CapOSRemaining

	// CapPackTemp reports the battery pack temperature in degrees Celsius.
	CapPackTemp

	// CapAppEnergy reports per-application energy. NO PLATFORM SETS THIS BY
	// DEFAULT, and no platform sets it for the whole-machine reader. It is
	// declared so that a caller can ask and be told no, which is a better
	// API than a field that is quietly always zero. The single path to
	// setting it is a successfully opened Windows SRUM source; see
	// OpenSRUM for the three reasons that is an enrichment and not a
	// foundation. TestNoCapabilityOverclaim pins the absence.
	CapAppEnergy

	// CapNativeLog reports that the platform keeps its own power-management
	// history that cellwatch can import. Set on macOS, set on Windows for
	// powercfg, and NEVER set on Linux, which has no such history.
	CapNativeLog

	// CapRAPLEnergy reports measured energy counters from the Intel Running
	// Average Power Limit interface. These are per CPU package, not per
	// application, and they are hardware-specific. Linux only, and only when
	// the tree is present and readable.
	CapRAPLEnergy

	// CapNone is the empty set. It is what an unsupported platform returns.
	CapNone Capability = 0

	// CapAll is every bit cellwatch defines. A platform's set is always a
	// subset of CapAll, which is what lets a caller mask safely.
	CapAll = CapChargePct | CapACState | CapStatusWord | CapWatts |
		CapFullCapacity | CapDesignCapacity | CapHealth | CapCycleCount |
		CapOSRemaining | CapPackTemp | CapAppEnergy | CapNativeLog | CapRAPLEnergy
)

// Has reports whether every bit of want is set in c.
func (c Capability) Has(want Capability) bool { return c&want == want }

// HasAny reports whether any bit of want is set in c.
func (c Capability) HasAny(want Capability) bool { return c&want != 0 }

// HasNot reports whether no bit of unwanted is set in c. It is the form a
// caller should use to assert an absence: "this platform does not claim
// per-application energy".
func (c Capability) HasNot(unwanted Capability) bool { return c&unwanted == 0 }

// String returns the lowercase names of the bits set in c, joined with "|",
// in declaration order, or "none".
func (c Capability) String() string

// capabilityName maps a bit to its name. Declared rather than inlined into
// String so that a bit added above without a name here is a compile-time
// gap the reader can see, not a silent omission at runtime.
var capabilityName = map[Capability]string{
	CapChargePct:      "charge_pct",
	CapACState:        "ac_state",
	CapStatusWord:     "status_word",
	CapWatts:          "watts",
	CapFullCapacity:   "full_capacity",
	CapDesignCapacity: "design_capacity",
	CapHealth:         "health",
	CapCycleCount:     "cycle_count",
	CapOSRemaining:    "os_remaining",
	CapPackTemp:       "pack_temp",
	CapAppEnergy:      "app_energy",
	CapNativeLog:      "native_log",
	CapRAPLEnergy:     "rapl_energy",
}
```

The `WattageSource` companion, because "is there a wattage figure" and "how much should I trust it"
are different questions:

```go
// WattageSource names the mechanism that produced a power reading. It is a
// diagnostic, not a capability: WattsKnown says whether a figure exists, and
// this says how much weight it deserves.
type WattageSource uint8

const (
	// WattageNone means no instantaneous power figure was available.
	WattageNone WattageSource = iota

	// WattageSysfs is Linux /sys/class/power_supply/BAT*/power_now, in uW.
	// A direct platform read. Absent on several ARM and DMI-based drivers.
	WattageSysfs

	// WattageIOReg is macOS ioreg AppleSmartBattery Voltage x abs(Amperage).
	// A direct platform read, from smoothed SMC values rather than the raw
	// pack, and zero on Apple Silicon under light load.
	WattageIOReg

	// WattageChargeDelta is derived from the charge percentage across a
	// measurement window. On Windows this is the only path, and it is
	// quantised at one percentage point, so its error is bounded by the
	// window length and not by the machine. See rate.go.
	WattageChargeDelta

	// WattageRAPL is a measured energy delta from the Intel RAPL interface.
	// It covers CPU packages only and populates Status.CPUWatts, never
	// Status.Watts.
	WattageRAPL
)

// String returns the name of the source, for example "power_now" or
// "charge_delta".
func (w WattageSource) String() string
```

### 2.6 The reader

```go
// Reader is the platform seam. Exactly one implementation compiles per target,
// and callers only ever see a Status.
type Reader interface {
	// Read returns one reading of the machine's power supply. It is safe for
	// concurrent use. A Read that cannot produce a reading returns an error;
	// it never returns a zero-valued Status.
	Read() (Status, error)

	// Capabilities returns the bits this reader can set on any machine. It
	// is a property of the compiled binary rather than of the running
	// machine, is stable for the life of the process, and never fails.
	//
	// Capabilities is the wide, pessimistic answer. A narrower, per-read
	// answer is available from the Status's Known booleans.
	Capabilities() Capability
}

// NewReader returns the Reader for the current GOOS. It fails only when the
// platform is not one cellwatch has an implementation for, and in that case
// it returns ErrUnsupported and a nil Reader.
func NewReader() (Reader, error)
```

The three implementations are §3. The fallback is deliberately honest rather than empty:

```go
//go:build !windows && !linux && !darwin

package cellwatch

// newReader returns ErrUnsupported. This file exists so that a freebsd or
// openbsd build of a consumer compiles and says so, rather than failing at
// link time on a missing symbol or, worse, succeeding and reporting no
// battery forever.
func newReader() (Reader, error) { return nil, ErrUnsupported }
```

### 2.7 The sampler

```go
// Sample is one observation: a Status, the instant it was taken, and
// optionally the per-application activity measured over the window that ended
// at that instant.
//
// A Sample is internally consistent. Every field in it describes the same
// observation, so a consumer can store it and never need to reconcile two
// reads that happened to be adjacent.
type Sample struct {
	// At is the instant the reading was taken, in UTC. Keys in the store are
	// derived from this value and nothing else.
	At time.Time

	// Status is the platform reading.
	Status Status

	// Apps is the per-application attribution measured over the window
	// ending at At. It is nil when the sampler was not asked to collect it,
	// which is different from being empty, which means "measured and
	// nothing was running".
	Apps []AppActivity

	// Idle is true when total measured activity fell below the attribution
	// idle floor, in which case every share in Apps is zero and apportioning
	// drain across near-zero activity would be noise presented as data.
	Idle bool

	// Provenance says how this observation was obtained: ProvMeasured for a
	// live read, ProvImported for one that came out of a platform history
	// log.
	Provenance Provenance

	// Source names the mechanism, duplicating Status.Source so that a stored
	// record carries it without the caller having to remember to copy it.
	Source string
}

// Sampler reads the platform on an interval and writes raw samples to a
// Store.
//
// A Sampler is opt-in at every level. NewSampler starts nothing, Run is what
// writes, and a process that only queries history never calls either. That
// is deliberate: a measurement library that writes to disk without being
// asked is a bug, and the consumer's job -- not the library's -- to decide
// that a background daemon is acceptable.
type Sampler struct {
	// unexported
}

// SamplerConfig configures a Sampler. The zero value is not usable: Store is
// required.
type SamplerConfig struct {
	// Store receives every raw sample. Required.
	Store Store

	// Interval is the sampling period. Default 60s, floor 5s, no upper
	// bound.
	//
	// 60 seconds is not arbitrary. Windows reports charge at one percentage
	// point of granularity and no wattage at all, so the sampling period is
	// the denominator of the measurement. A 10 W draw on a 50 Wh pack is
	// 0.2 percentage points per second, or 12 points per minute: at 60 s the
	// quantisation error on a rate estimate is around 8 %, and at 2 s it is
	// around 250 %. Below about 1 W the counter still stalls, and at that
	// point the machine is asleep.
	//
	// This is deliberately much slower than the live display tick. The live
	// view samples at its own interval and never writes; a 2 s tick
	// persisted for a day would be 43,200 records of a value that changes
	// once a minute.
	Interval time.Duration

	// Reader supplies the platform readings. Nil means NewReader.
	Reader Reader

	// Apps enables per-application attribution. Off by default, because it
	// enumerates every process on the machine once per interval and that is
	// a cost a consumer should opt into deliberately.
	Apps bool

	// MaxApps is the number of applications recorded per sample, ranked by
	// measured activity. Default 8, hard cap 64. The remainder is folded
	// into the unattributed bucket rather than dropped, so the shares still
	// sum to one.
	MaxApps int

	// Location is the zone used to label records and to compute week, month
	// and year boundaries. Nil means time.Local.
	//
	// It affects where bucket boundaries fall. It does not affect the
	// encoding: every key is derived from the UTC instant. See rollup.go.
	Location *time.Location

	// OnSample, if non-nil, is called once per stored sample. It is called
	// from the sampling goroutine, must not block, and must not call back
	// into the Sampler.
	OnSample func(Sample)

	// Logger receives warnings about gaps, unreadable process tables and
	// similar. Nil discards them. A Sampler never logs to the global logger.
	Logger *slog.Logger
}

// NewSampler returns a Sampler for cfg. It performs no I/O: nothing is read
// and nothing is written until Run is called.
func NewSampler(cfg SamplerConfig) (*Sampler, error)

// Run samples until ctx is cancelled, then performs a final Rollup and
// Prune and returns.
//
// Run is the only thing in this package that writes. It returns nil on a
// clean cancellation, and the first error it could not recover from
// otherwise. A single failed read is a gap, not an error: Run tolerates
// them, records them, and keeps going. Ten consecutive failed reads are a
// different thing and do stop it.
func (s *Sampler) Run(ctx context.Context) error

// Rollup advances every tier to cover every raw sample written so far. Run
// calls it on exit and on a timer; a consumer that imports native logs calls
// it directly.
func (s *Sampler) Rollup(ctx context.Context) error
```

### 2.8 Ranges, periods and tiers

```go
// Tier names a rollup granularity. TierAuto is the zero value and asks the
// store for the coarsest tier that fully covers the range.
type Tier uint8

const (
	// TierAuto lets the store choose. A year gets a year record, a fortnight
	// gets days, and a range that starts inside the retained raw window gets
	// seconds.
	TierAuto Tier = iota
	// TierSecond is the raw tier.
	TierSecond
	// TierMinute, TierHour, TierDay, TierWeek, TierMonth and TierYear name
	// the rollup tiers.
	TierMinute
	TierHour
	TierDay
	TierWeek
	TierMonth
	TierYear
)

// String returns "auto", "second", "minute", "hour", "day", "week", "month"
// or "year".
func (t Tier) String() string

// Period selects a calendar window ending at an instant. It is a convenience
// over Range: ThisPeriod builds one. Period is not a Tier -- PeriodAll is a
// span, not a resolution.
type Period uint8

const (
	// PeriodDay is the local calendar day containing the instant.
	PeriodDay Period = iota
	// PeriodWeek is the local ISO week, which begins on Monday.
	PeriodWeek
	// PeriodMonth is the local calendar month.
	PeriodMonth
	// PeriodYear is the local calendar year.
	PeriodYear
	// PeriodLast7Days, PeriodLast30Days and PeriodLast365Days are rolling
	// windows ending at the instant, not calendar periods, and they are not
	// affected by where the month boundary falls.
	PeriodLast7Days
	PeriodLast30Days
	PeriodLast365Days
	// PeriodAll is everything retained.
	PeriodAll
)

// String returns the period's name as it appears in JSON and in
// Provenance-bearing output.
func (p Period) String() string

// Weekday is the day a week begins on.
//
// cellwatch writes ISO-8601 weeks, which begin on Monday, and records the
// choice in a battery_meta row when the store is opened. It is a
// per-database property rather than a per-query option for a concrete
// reason: changing it changes the meaning of every stored week bucket, so it
// is the kind of change that must be made once, deliberately, on an empty
// database -- not once per query.
type Weekday int

const (
	// Monday is the ISO-8601 default.
	Monday Weekday = iota
	// Sunday is the US convention, for consumers whose users expect it.
	Sunday
)

// Range is a half-open window [Start, End) with a location attached.
//
// Start and End are absolute instants. Loc is the location whose calendar
// defines where the window's boundaries fall, and it is retained on the
// result so that a record can say which wall clock it belongs to. That
// matters more than it looks: a local day is 23, 24 or 25 hours long
// depending on daylight saving, and a reader comparing two reports needs to
// know which they got.
type Range struct {
	// Start is inclusive.
	Start time.Time
	// End is exclusive.
	End time.Time
	// Loc is the zone the boundaries were computed in. Nil means UTC.
	Loc *time.Location
	// Tier is the requested resolution. The zero value, TierAuto, asks for
	// the coarsest tier that fully covers the range.
	Tier Tier
}

// Duration returns End minus Start.
func (r Range) Duration() time.Duration

// ThisPeriod returns the Range for the calendar period containing now in
// loc. "This week" is the current ISO week and begins on Monday; see
// weekKey in rollup.go for why, and Weekday for the alternative.
//
// A nil loc means time.Local. A PeriodLast7Days window is half-open on
// [now-7d, now), so it includes the current instant and excludes the one a
// week ago, which is the convention a rolling counter should use.
func ThisPeriod(now time.Time, p Period, loc *time.Location) Range

// Previous returns the Range immediately preceding r, of the same span. It is
// what Compare's second argument usually is, and it exists so that a caller
// cannot accidentally compare a week with a fortnight.
func (r Range) Previous() Range
```

### 2.9 Provenance, series and coverage

This is the type that makes the honesty requirement mechanical rather than aspirational.

```go
// Provenance says how a number was produced.
//
// It is not decoration. A caller that renders ProvEstimated without marking it
// has made a claim the data does not support, and the type is what makes that
// visible at the point where the decision is taken.
type Provenance uint8

const (
	// ProvUnknown is the zero value. A report containing it is a bug, and
	// the JSON serialiser emits "unknown" rather than omitting the field so
	// that the bug is visible.
	ProvUnknown Provenance = iota

	// ProvMeasured was read from the platform, or summed from values that
	// were. Charge percentage, mains state and cumulative charge drained are
	// measured.
	ProvMeasured

	// ProvDerived is arithmetic on measured values. A rate, a duration, a
	// total. The inputs were measured even though the output was computed,
	// and the difference from ProvMeasured is worth showing.
	ProvDerived

	// ProvEstimated was apportioned from measured CPU time and I/O weight.
	// NO OPERATING SYSTEM REPORTS PER-APPLICATION BATTERY USE. Every
	// per-application figure in this package is this, except the two
	// exceptions named below.
	ProvEstimated

	// ProvImported came from a platform power-management history log: pmset
	// on macOS, powercfg on Windows. The platform measured it; cellwatch did
	// not observe it live, and the log may be days old or truncated.
	ProvImported

	// ProvAppEnergyReported came from a per-application energy record the
	// platform itself keeps -- Windows SRUM's EnergyEstimation column. It is
	// a figure the operating system computed about an application, not one
	// cellwatch apportioned, which makes it the closest thing to per-app
	// truth available anywhere. It is still a model output, sampled only
	// while an application has foreground activity, and it is marked
	// separately precisely so that it is never confused with either a
	// measurement or an estimate.
	ProvAppEnergyReported

	// ProvCPUMeasured is measured CPU-package energy from Intel RAPL. It
	// covers the processor packages and nothing else, so the remainder of
	// the machine's draw is the difference between it and the total.
	ProvCPUMeasured
)

// String returns "measured", "derived", "estimated", "imported",
// "app_energy_reported", "cpu_measured" or "unknown".
func (p Provenance) String() string

// Estimated reports whether p is a figure cellwatch computed by apportioning
// drain across activity. It is the predicate a renderer should switch on to
// decide whether to mark a value.
func (p Provenance) Estimated() bool {
	return p == ProvEstimated || p == ProvAppEnergyReported
}

// Point is one aggregate in a series.
type Point struct {
	// At is the start of the bucket, always in UTC. The location a caller
	// asked about is on the Range, not here, because a Point's identity is
	// its instant and not its label.
	At time.Time

	// Duration is the bucket's nominal length. A daylight-saving transition
	// makes the actual observed length differ, and Coverage carries the
	// actual figure.
	Duration time.Duration

	// EnergyWh is energy discharged over the bucket, in watt-hours. It is
	// zero and EnergyKnown is false when the platform never reported the
	// power or the pack capacity needed to derive it -- which is the normal
	// case on Windows, where GetSystemPowerStatus reports neither.
	EnergyWh   float64
	EnergyKnown bool

	// DrainPct is charge consumed over the bucket in percentage points,
	// negative while charging. Unlike EnergyWh this is available everywhere,
	// because the charge counter is available everywhere. It is quantised at
	// one point on Windows and finer elsewhere.
	DrainPct    float64
	DrainKnown  bool

	// Provenance is how DrainPct and EnergyWh were produced.
	Provenance Provenance

	// ImportedFraction is the share of the bucket's observations that came
	// from a native platform log rather than from a live read, 0 to 1. A
	// non-zero value means the bucket mixes two kinds of evidence, and a
	// renderer that collapses the two is hiding that.
	ImportedFraction float64

	// Stale is true when this aggregate predates the newest raw record in
	// its own bucket, because a sample arrived after the rollup ran. A stale
	// point is a correct answer to a question about the past, and is
	// explicitly not the current answer.
	Stale bool
}

// Coverage describes how much of a requested window the store actually saw.
//
// This exists because the alternative is a confident number derived from four
// minutes of data, and because a consumer that renders a percentage will
// render it whether or not anyone told it the history was mostly missing.
type Coverage struct {
	// Observed and Expected are durations. Expected is the length of the
	// requested range, adjusted for daylight saving, so that a 23-hour day
	// does not report as permanently 95.8 percent covered.
	Observed time.Duration
	Expected time.Duration

	// ObservedMinutes and ExpectedMinutes are the same thing in units a
	// progress bar can divide.
	ObservedMinutes int
	ExpectedMinutes int

	// Pct is Observed as a percentage of Expected, 0 to 100.
	Pct float64

	// FirstSample and LastSample bound the data. They are the zero time when
	// nothing was observed at all, which is the case a caller most needs to
	// detect and the one a plain total would hide best.
	FirstSample time.Time
	LastSample  time.Time

	// Gaps is the number of intervals longer than three sampling periods,
	// and LongestGap is the longest of them. A machine that was asleep for
	// four hours contributes one gap, not 480 missing minutes.
	Gaps       int
	LongestGap time.Duration

	// Apps is true when per-application attribution was being collected over
	// the window. A usage report with Apps false and a non-empty Top is a
	// bug; a usage report with Apps false and an empty Top is the truth.
	Apps bool
}

// minUsableCoveragePct is the coverage below which a figure is a rumour. It
// is 25 rather than 50 because a consumer running the sampler continuously
// and a consumer who only opens a laptop twice a day are both legitimate,
// and 25 percent of a day still contains enough samples to rank applications
// coarsely. It is a named constant so that a caller arguing with it does so
// in one place.
const minUsableCoveragePct = 25.0

// Usable reports whether the coverage is good enough to quote a figure. When
// it is false, Compare returns ErrInsufficientCoverage rather than a delta.
func (c Coverage) Usable() bool { return c.Pct >= minUsableCoveragePct }

// String returns a one-line human summary, for example
// "82% of the window, 87 min missing".
func (c Coverage) String() string

// Series is an ordered set of points, the range it answers for, the tier it
// was served at, and how complete it is.
type Series struct {
	// Range is the window, echoed back with the resolved Location set.
	Range Range
	// Tier is the granularity actually served, which may be finer than
	// Range.Tier asked for and is never coarser than a full cover allows.
	Tier Tier
	// Points are ordered by At, ascending, and are never empty unless
	// Coverage.Pct is zero.
	Points []Point
	// Coverage describes how much of the window the series saw.
	Coverage Coverage
}
```

### 2.10 The query API

```go
// UsageReport is the answer to "what did the battery do, and what did my
// applications do, over this window".
type UsageReport struct {
	// Range is the window, echoed back resolved.
	Range Range

	// System is the machine-level total. DrainPct is measured on every
	// platform; EnergyWh is zero with EnergyKnown false wherever the platform
	// reports neither watts nor pack capacity, which on Windows means always.
	System SystemUsage

	// Top is the applications ranked by attributed energy, longest first.
	// Empty when Coverage.Apps is false, and empty-but-Apps-true when the
	// window was measured and nothing ran.
	Top []AppTotal

	// Unattributed is the residual: kernel threads, idle time, the display,
	// the radios, and processes cellwatch could not name or refused to
	// guess at. It is always present, including as a full 100 percent, and
	// it is never zeroed for looking untidy.
	Unattributed AppTotal

	// Notes are caveats a renderer should show. Non-empty in practice; an
	// empty slice means something is wrong with the code.
	Notes []string
}

// SystemUsage is the machine-level total over a Range.
type SystemUsage struct {
	// EnergyWh is cumulative energy discharged. EnergyKnown is false when it
	// cannot be computed at all, which is every unelevated Windows machine.
	EnergyWh   float64
	EnergyKnown bool

	// DrainPct is cumulative charge consumed in percentage points. It is
	// available on every platform, including desktops where it is zero
	// because there is nothing to drain.
	DrainPct   float64
	DrainKnown bool

	// BatterySeconds and ACSeconds are how long the machine spent in each
	// state, summed from observed transitions only. A window with no
	// transitions reports the full span in the state the window began in.
	BatterySeconds time.Duration
	ACSeconds      time.Duration

	// Sessions is the number of observed mains transitions. A laptop opened
	// once has one.
	Sessions int

	// ImportedSeconds is how much of the window was reconstructed from a
	// native platform log rather than observed live.
	ImportedSeconds time.Duration

	// Provenance is how DrainPct and EnergyWh were produced.
	Provenance Provenance
}

// AppTotal is one application's attributed energy over a Range.
type AppTotal struct {
	// App is the folded grouping key: lower-cased, ".exe" stripped, and
	// multi-process applications folded together. See the grouping table in
	// attrib.go.
	App string

	// EnergyWh is the attributed energy. It is zero and EnergyKnown is false
	// wherever the platform reports no power and no pack capacity, whatever
	// Provenance says.
	EnergyWh    float64
	EnergyKnown bool

	// DrainPct is the attributed charge consumption in percentage points,
	// which is the figure available everywhere.
	DrainPct   float64
	DrainKnown bool

	// Share is the fraction of attributed drain this application accounts
	// for, 0 to 1. Shares across Top plus Unattributed sum to 1.
	Share float64

	// Provenance is ProvEstimated for anything cellwatch apportioned, or
	// ProvAppEnergyReported for Windows SRUM figures.
	Provenance Provenance

	// CPUSeconds, ReadBytes and WriteBytes are what was actually measured
	// for this application. They are the evidence behind the estimate, and a
	// consumer that wants to display something defensible can display these
	// instead of the estimate.
	CPUSeconds float64
	ReadBytes  uint64
	WriteBytes uint64

	// Samples is the number of raw samples this application's total was
	// accumulated from, which is a second, independent honesty check: a
	// thousand-app total built from four samples is not a thousand-app total.
	Samples int
}

// Usage returns the aggregate report for r.
//
// Usage is the top-level entry point and it composes the rest: it calls
// Query for the series and TopApps for the application table, and it returns
// ErrInsufficientCoverage only if the caller asked it to compare, which it
// does not. A low-coverage window produces a report whose Coverage says so;
// refusing to produce the report at all would be a worse answer, because the
// caller then has no idea whether the data is missing or the query is
// broken.
func Usage(ctx context.Context, st Store, r Range) (*UsageReport, error)

// TopApps returns the n applications with the most attributed drain in r,
// longest first. n <= 0 returns every application with a non-zero share,
// which on a long window is bounded by the size of the app registry rather
// than by anything the caller chose.
//
// The scan is bounded by the number of buckets in the window, not by the
// length of history, because the rollup ladder resolves the window to the
// coarsest tier that fully covers it: a year-long TopApps reads 365 day
// rows. See §10.1 for the ladder and §9.5 for the packed app slice it
// decodes.
func TopApps(ctx context.Context, st Store, r Range, n int) ([]AppTotal, error)

// AppHistory returns the per-bucket series for one application over r. The
// application is matched against the folded grouping key, so "chrome"
// returns every Chrome process on every platform it was recorded under.
//
// An application that has never been recorded returns an empty Series with
// zero Coverage and no error: "we have never seen that" is an answer.
func AppHistory(ctx context.Context, st Store, app string, r Range) (Series, error)

// Delta is the change between two windows, and it is only ever produced when
// both windows are usable.
type Delta struct {
	// Current and Previous are the windows compared.
	Current  Range
	Previous Range

	// DrainPctDelta is the change in percentage points of charge consumed.
	DrainPctDelta float64

	// EnergyWhDelta is the change in energy, and is zero with EnergyKnown
	// false wherever the underlying figures were not.
	EnergyWhDelta float64
	EnergyKnown   bool

	// Current and Previous are the app tables, so a consumer can show a
	// movement rather than two independent lists.
	CurrentTop  []AppTotal
	PreviousTop []AppTotal

	// Movers are the applications whose share moved most between the two
	// windows, biggest absolute change first, up and down alike. It is the
	// question a person actually asks of a year-on-year comparison.
	Movers []AppMover

	// Current and Previous coverage, echoed so a caller can see how much to
	// trust the comparison it was handed.
	CurrentCoverage  Coverage
	PreviousCoverage Coverage
}

// AppMover is one application's change in share between two windows.
type AppMover struct {
	App           string
	ShareDelta    float64
	CurrentShare  float64
	PreviousShare float64
	// Both is true when the application appeared in only one of the two
	// windows. A move from zero to a large share is a move, and a consumer
	// that renders it as a large percentage change is being honest.
	Both bool
}

// Compare returns the change between two windows.
//
// It returns ErrInsufficientCoverage if either window's coverage is below
// Coverage.Usable's threshold. That is the entire point of the function: a
// week-over-week comparison between a full week and four minutes of data
// produces a large, confident, meaningless number, and returning an error
// is the only way to stop a caller rendering it.
//
// It also returns ErrInsufficientCoverage when the two windows do not
// overlap in kind -- comparing a calendar month against a rolling 30-day
// window is legal but is almost never what was meant, so the ranges are
// checked for equal span and the error says so.
func Compare(ctx context.Context, st Store, current, previous Range) (*Delta, error)
```

### 2.11 The store interface

```go
// Store is the persistence seam. The implementation in store_sqlite.go is
// the one that ships; the interface exists so that a consumer can put a
// different engine, or a test-only in-memory store, behind the query layer
// without touching it.
//
// The interface is deliberately narrow and deliberately not an SQL
// abstraction. It says what battery history means -- put a sample, put
// attribution, query a range, query an application, roll up, prune -- and
// nothing about how. An earlier design made it a key/value seam (Put, Get,
// Delete over opaque bytes) and that was a mistake: every method leaked the
// storage shape into the API, and the rollup ladder had nowhere to live.
//
// A Store is safe for concurrent use by multiple goroutines. A Store is
// also safe for concurrent use by multiple *processes*, which the previous
// engine was not: see §9.4.
type Store interface {
	// PutRaw writes one raw sample. It is idempotent with respect to the
	// sample's own timestamp: writing the same sample twice leaves one
	// row, because the timestamp is the key.
	PutRaw(ctx context.Context, s Sample) error

	// PutApps writes the per-application attribution for the window ending
	// at s.At, updating the existing aggregate for that window rather than
	// appending to it. Writing the same AppSlice twice leaves the same
	// result.
	PutApps(ctx context.Context, s Sample) error

	// Query returns the series for r at the coarsest tier that fully covers
	// it, and coverage for the window as a whole.
	Query(ctx context.Context, r Range) (Series, error)

	// AppHistory returns the series for one application.
	AppHistory(ctx context.Context, app string, r Range) (Series, error)

	// TopApps returns the n applications with the most attributed drain.
	TopApps(ctx context.Context, r Range, n int) ([]AppTotal, error)

	// Rollup advances every tier to cover through. It is idempotent.
	Rollup(ctx context.Context, through time.Time) error

	// Prune deletes every row older than its tier's retention window,
	// counted back from now, and reports how many of each it removed.
	Prune(ctx context.Context, now time.Time) (Pruned, error)

	// Close releases the underlying file handles. It is idempotent, and a
	// Store whose Close has already run returns ErrClosed from every method
	// rather than panicking.
	Close() error
}

// Pruned reports what a Prune removed, per tier. A consumer that shows this
// to a user should show the numbers rather than "cleaned up", because a
// retention policy that quietly ate a year of history is something a user
// has a right to notice.
type Pruned struct {
	Raw    int
	Minute int
	Hour   int
	Day    int
	Week   int
	Month  int
	Year   int
	Apps   int
	Bytes  int64
	At     time.Time
}
```

**The interface has no SQL in it and no `*sql.DB` escapes it.** That is the property worth
enforcing, and §18 Phase 2 turns it into a test: `database/sql` must not appear in any file outside
`store_sqlite.go` and `schema.go`. A `Store` method that took a `*sql.DB`, or returned a
`*sql.Rows`, would be a key/value abstraction wearing a relational costume, and the next person to
add a query to it would be adding a WHERE clause to a public API.

`Import` is deliberately *not* a method on `Store`, because it needs the write seam and requiring
that seam in the exported interface would forbid a consumer's own implementation from compiling.
It is a package function that type-asserts, and says so when the assertion fails:

```go
// writer is the unexported seam Import needs. A Store that supports native-log
// import implements it; the SQLite store does. A Store that does not still
// satisfies Store, and Import returns ErrImportUnsupported rather than
// panicking.
type writer interface {
	write(ctx context.Context, fn func(*sql.Tx) error) error
}

// Open opens the battery store at path, creating it if it does not exist.
//
// mode exists for the read-only case, and for a much smaller reason than it
// did under the previous engine: a write transaction takes a lock on the
// database, so opening read-only is how a consumer guarantees it cannot
// write by accident. It is not a way to avoid a second writer, because WAL
// already handles that. See §9.4.
//
// Open is idempotent with respect to the file, not with respect to the
// process: opening the same path twice in one process returns two Stores
// with two independent connection pools, each of which will serialise
// against the other through SQLite's own locking. A consumer that needs a
// single handle should keep one.
func Open(path string, mode Mode, opts ...Option) (Store, error)
```

### 2.12 Opening a store

```go
// Mode selects whether Open may write.
//
// The distinction is smaller than it was. The previous engine needed Mode
// because a write transaction took an exclusive lock on the database file,
// which made "two processes writing at once" a configuration error the
// store had to detect and refuse. SQLite in WAL mode does not have that
// problem: one writer and many readers, across processes, by design (§9.4).
//
// What Mode still buys is a guarantee rather than a workaround. A reader
// opened read-only cannot write even if a future method is added in a hurry,
// and a consumer that only ever queries can say so in one place.
type Mode uint8

const (
	// ModeWriter opens for reading and writing. This is the default.
	ModeWriter Mode = iota

	// ModeReader opens the database read-only. Every method on a read-only
	// Store other than Close returns ErrReadOnly.
	ModeReader
)

// Option configures Open.
type Option func(*openOptions)

// WithBusyTimeout sets how long a write waits for another process's write
// lock before returning "database is locked". The default is 5s, matching
// internal/indexer/indexer.go:85.
//
// It is not a knob most callers should touch. It exists because a consumer
// running on a network filesystem, where SQLite's own locking is unreliable,
// needs to be able to say so explicitly rather than by changing a constant.
func WithBusyTimeout(d time.Duration) Option

// WithLocation sets the zone used to compute week, month and year bucket
// boundaries for rows written after Open. The default is time.Local, read
// once at Open rather than at each write, so that a process which changes TZ
// mid-run does not write two incompatible layouts into one database.
//
// The setting is recorded in battery_meta. Reopening an existing database
// with a different location is refused with ErrLocationMismatch unless
// WithRelocate is given, because the two would place the same day in
// different buckets.
func WithLocation(loc *time.Location) Option

// WithRelocate permits reopening a database with a different location. The
// existing week, month and year rows keep their old bucket starts and become
// unreachable; raw and minute rows, which are bucketed in UTC, are
// unaffected and the higher tiers rebuild from them on the next Rollup.
func WithRelocate() Option

// WithRetention overrides the default per-tier retention. See Retention.
func WithRetention(r Retention) Option

// WithReadOnly is shorthand for passing ModeReader.
func WithReadOnly() Option

// ErrReadOnly is returned by every mutating method on a Store opened with
// ModeReader.
var ErrReadOnly = errors.New("cellwatch: store is open read-only")

// ErrSchemaVersion is returned when the database was written by a newer
// cellwatch than the one opening it. It is a refusal, not a best-effort
// parse: a misread time series is worse than an unreadable one. §9.5.
var ErrSchemaVersion = errors.New("cellwatch: database written by a newer version")

// Retention is the per-tier retention policy. The zero value of each field
// means "use the default for that tier", so a caller who wants only raw
// samples kept for a day writes Retention{Raw: 24 * time.Hour}.
//
// Retention is applied by Prune and is not applied automatically: a database
// grows until something calls it, which keeps a consumer that never prunes
// from losing data it did not know it had.
type Retention struct {
	// Raw keeps individual samples. Default 14 days.
	Raw time.Duration
	// Minute keeps one-minute aggregates. Default 62 days, which is two
	// calendar months rounded to a round number of days. The number is
	// arbitrary; what it buys is that "show me the last two months at
	// one-minute resolution" is always possible.
	Minute time.Duration
	// Hour keeps hourly aggregates. Default 730 days, two years.
	Hour time.Duration
	// Day, Week and Month keep calendar aggregates. Default 10 years each,
	// which is the point at which a day row is a rounding error in the
	// history's cost. §10.7.
	Day   time.Duration
	Week  time.Duration
	Month time.Duration
	// Year is the only tier with no default expiry. There is one row per
	// year, about 86 bytes; deleting it would be vandalism.
	Year time.Duration
}
```

**`WithTimeout` and `ErrTimeout` are gone**, and their removal is a fair measure of what the
decision bought. They existed only because bbolt's write path took an exclusive file lock and a
second writer had to be told to go away within a bounded time. Under WAL there is no such condition:
a second writer waits in SQLite, a reader never waits at all, and `busy_timeout` is a pragma rather
than an option a consumer has to think about. `§14` loses a row and `§2.12` loses an option, and
both losses are the same loss, which is a constraint that no longer exists.

---

## 3. Platform data sources

Carried over from the previous document. Every trap listed here was researched against a primary
source -- the Win32 documentation, the Linux kernel's `Documentation/ABI/testing/sysfs-class-power`
and a live `/sys/class/power_supply` dump, and `ioreg` output from real hardware -- and every one of
them has cost somebody a wrong number.

### 3.1 Summary

| | Windows | Linux | macOS |
|---|---|---|---|
| Mechanism | `GetSystemPowerStatus` via `x/sys/windows` lazy DLL | read files under `/sys/class/power_supply/` | `exec ioreg -r -c AppleSmartBattery`, parse text |
| Source label | `GetSystemPowerStatus` | `/sys/class/power_supply` | `ioreg:AppleSmartBattery` |
| Charge % | `BatteryLifePercent` (0-100, 255 = unknown) | `capacity` (0-100) | `CurrentCapacity x 100 / AppleRawMaxCapacity` |
| AC state | `ACLineStatus` (0 off, 1 on, 255 unknown) | `type == Mains` with `online == 1` | `ExternalConnected` |
| Instantaneous watts | `IOCTL_BATTERY_QUERY_STATUS` `.Rate` (mW) | `power_now` (uW), often absent | `Voltage` (mV) x `abs(Amperage)` (mA) |
| CPU watts | **not available** | Intel RAPL `energy_uj` delta, when present | **not available** |
| Full / design capacity | `IOCTL_BATTERY_QUERY_INFORMATION` (unprivileged, measured) | `energy_full` / `energy_full_design`, or `charge_full` / `charge_full_design` | `MaxCapacity` / `DesignCapacity` (mAh) |
| Health % | full / design from the same IOCTL | `energy_full / energy_full_design` | `AppleRawMaxCapacity / DesignCapacity` |
| Cycle count | `IOCTL_BATTERY_QUERY_INFORMATION` `.CycleCount` (unprivileged, measured) | `cycle_count`, on very few drivers | `CycleCount`, usually present |
| OS time remaining | `BatteryLifeTime` (s, signed, `0xFFFFFFFF` unknown) | not exposed | `TimeRemaining` (min, `-1` unknown) |
| Present capacity | `IOCTL_BATTERY_QUERY_STATUS` `.Capacity` (mWh) | `energy_now` | `CurrentCapacity` |
| Voltage | `IOCTL_BATTERY_QUERY_STATUS` `.Voltage` (mV) | `voltage_now` | `Voltage` |
| Temperature | not available | not exposed | `Temperature` (centi-degrees C) |
| "No battery" signal | `BatteryFlag & 128` | no `type == Battery`, or `present == 0` | no `AppleSmartBattery` object in output |
| New dependency | **none** | **none** | **none** |

### 3.2 Windows -- `reader_windows.go`

`golang.org/x/sys v0.47.0` is already a direct dependency (`go.mod:18`), and `x/sys/windows` exposes
`NewLazySystemDLL`, `(*LazyDLL).NewProc` and `(*LazyProc).Call`. So `kernel32!GetSystemPowerStatus`
binds with a hand-declared struct and **zero new dependencies**.

```go
//go:build windows

package cellwatch

import "golang.org/x/sys/windows"

var (
	modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemPowerStatus = modkernel32.NewProc("GetSystemPowerStatus")
)

// systemPowerStatus mirrors the Win32 SYSTEM_POWER_STATUS struct: four bytes
// and two uint32s, in that order, with no padding on any architecture Go
// targets here.
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	Reserved1           byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}
```

The semantics, each of which is a trap:

- `BatteryFlag` is a bit field, and only the low nibble plus bit 7 are dependable: `1` high, `2`
  low, `4` critical, `8` charging, `16` reserved, `128` **no system battery**, `255` unknown. The
  value `255` is also what several fields return at once on a desktop, so `BatteryFlag == 255`
  must be read as "unknown everything" rather than as "critical", since `4 | 8 | 16` overlaps it.
- `BatteryLifePercent` is `0..100`, or `255` for unknown. It is one percentage point granular by
  construction, and that single fact drives most of §4.4.
- `BatteryLifeTime` is nominally a `DWORD` in seconds, but the field is **signed in practice**: many
  firmware stacks return a large unsigned value near `0xFFFFFFFF` to mean "negative" -- the estimate
  is shrinking -- and `0xFFFFFFFF` to mean "unknown". Treating any value `> 2^31` as unknown is the
  only safe reading. `0` is also not meaningful; in the Win32 contract it means "unchanged".
- The function reports the **first** battery only. A two-battery machine shows one, and `Count` is
  1 rather than 2 because the API cannot tell the difference.
- Wattage, present capacity, voltage, design capacity and cycle count are **not** in this API. They
  are read from the battery class driver instead, via `reader_windows_battery.go`.

#### The battery class driver, and the elevation claim

There is a documented route to everything `GetSystemPowerStatus` omits: enumerate the
`GUID_DEVICE_BATTERY` device interfaces with `SetupDiGetClassDevsW` and
`SetupDiEnumDeviceInterfaces`, open the resulting path with `CreateFile`, and issue
`IOCTL_BATTERY_QUERY_INFORMATION` and `IOCTL_BATTERY_QUERY_STATUS` on the handle.

**This design originally asserted that the open requires administrator rights, and that claim was
wrong.** It was written from the documentation rather than measured, and the hardware test
disproved it. An ordinary unprivileged process on the development machine read all of it:

```
QUERY_INFORMATION: tech=1 chem="Li-I" design=45000 full=35740 CYCLES=1541
QUERY_STATUS:      powerState=0x5 capacity=34530 mWh voltage=13005 mV rate=5540 mW
```

No elevation, no flag, no separate opt-in. Two details are load-bearing and neither is obvious:

- `SP_DEVICE_INTERFACE_DATA.cbSize` must be 32. The size the documentation implies is 24, and 24
  fails. The correct value is `unsafe.Sizeof` of the struct as declared.
- The detail buffer is a `DWORD cbSize` followed by the UTF-16 path, so the path begins at element
  2 of a `[]uint16`, and the buffer is sized from the probe call that returns
  `ERROR_INSUFFICIENT_BUFFER`. That error is the documented success for the probe, not a failure.

The IOCTL codes are `0x294040`, `0x294044` and `0x29404C`. They are written in hex in the code
because a hex literal transcribed wrong is a silent failure returning `ERROR_NOT_IMPLEMENTED`
rather than a compile error, which is exactly how this was caught: the first run of the integrated
reader returned nothing at all while the standalone probe had already succeeded.

`github.com/distatus/battery` performs this same sequence and was used as a temporary stand-in
while the enumeration was being debugged. It was removed: it issues the same
`IOCTL_BATTERY_QUERY_INFORMATION` call, reads `bi.CycleCount` into its own struct, and then
discards it, because its exported `Battery` type has no field for it. A dependency that computes an
answer and drops it cannot be extended from outside, and it was last released in September 2023
with a `BREAKING.md`.

The reader, complete:

```go
// Capabilities returns CapChargePct | CapACState | CapStatusWord |
// CapOSRemaining | CapNativeLog. Windows reports the other four nowhere, and
// CapAppEnergy is claimed by nothing until OpenSRUM has succeeded.
func (r *windowsReader) Capabilities() Capability {
	return CapChargePct | CapACState | CapStatusWord | CapOSRemaining | CapNativeLog
}

func (r *windowsReader) Read() (Status, error) {
	var sps systemPowerStatus
	ret, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&sps)))
	if ret == 0 {
		return Status{}, fmt.Errorf("cellwatch: GetSystemPowerStatus: %w", windows.GetLastError())
	}

	st := Status{
		Source: "GetSystemPowerStatus",
		Count:  1,
		AC:     acFromByte(sps.ACLineStatus),
		Notes:  []string{"1% reporting granularity; wattage and capacity come from the battery class driver, not from this call"},
	}

	if sps.BatteryFlag == unknownFlag { // 255: "we know nothing"
		st.Notes = append(st.Notes, "battery flag reported as unknown")
		return st, nil
	}

	st.Present = sps.BatteryFlag&noSystemBattery == 0
	if !st.Present {
		// A desktop is a correct answer, not a failure, and the mains state
		// is still worth reporting.
		return st, nil
	}

	if sps.BatteryLifePercent != unknownByte { // 255
		st.ChargePct = float64(sps.BatteryLifePercent)
		st.ChargeKnown = true
	}
	st.Charging = sps.BatteryFlag&flagCharging != 0
	st.Full = sps.BatteryFlag&flagHigh != 0
	st.Status = statusWord(sps)

	// The signed-in-practice guard from 3.2.
	if lt := sps.BatteryLifeTime; lt > 0 && lt < 1<<31 {
		st.OSRemaining = time.Duration(lt) * time.Second
		st.OSRemainingKnown = true
	} else if lt != 0 {
		st.Notes = append(st.Notes, "OS time-remaining field present but not usable on this firmware")
	}
	return st, nil
}
```

### 3.3 Linux -- `reader_linux.go`

Pure file reads: no syscalls, no cgo. Enumerate `/sys/class/power_supply/`, select the first entry
whose `type` file reads `Battery` (`BAT0`, `BAT1`, ...), and read:

| File | Unit | Notes |
|---|---|---|
| `present` | `0`/`1` | `0` means the pack is absent -- docked out, or a battery that has been disabled in firmware |
| `capacity` | % | occasionally above 100 on some OEM firmware; clamp |
| `status` | string | `Charging`, `Discharging`, `Full`, `Not charging`, `Unknown` |
| `energy_now` / `energy_full` / `energy_full_design` | uWh | modern power-based drivers |
| `charge_now` / `charge_full` / `charge_full_design` | uAh | older current-based drivers |
| `power_now` | uW | **frequently absent**, notably on several ARM and DMI-based drivers |
| `voltage_now` | uV | only useful with the `charge_*` layout |
| `cycle_count` | count | implemented by very few drivers; usually missing |
| `technology` | string | `Li-ion`, `Li-poly`, `NiMH` |
| `modelname`, `manufacturer` | string | cosmetic, shown in the snapshot |

**The `energy_*` and `charge_*` layouts are mutually exclusive and must never be mixed.** uWh is
energy; uAh is charge. Mixing them produces a "health" figure wrong by roughly the nominal cell
voltage -- typically 3.7 to 11.1 V depending on the cell count -- which is exactly the class of
silently wrong number this design refuses to emit. The reader picks one layout, records which in
`Notes`, and leaves the other set of fields at zero with `Known` false.

AC detection uses `type == Mains` with `online == 1`, **not** the `AC`/`ACAD` directory name.
Directory naming is vendor-specific (`AC`, `ACAD`, `ADP1`, `WWAN`); the `type` file is the kernel's
own classification and is uniform. `ACAD` means "AC adapter present" and can read `0` while mains is
live, so `online` is the field to trust and not the existence of a directory.

Known gaps: no `power_now` means no instantaneous watts, and therefore no `TimeToEmpty`, so the rate
comes from the charge-delta window of §4.4 and is correspondingly coarse. `cycle_count` usually
does not exist. `energy_full_design` is frequently `0` on consumer laptops, so `HealthPct` is
unavailable more often here than on macOS.

### 3.4 Linux CPU energy -- `rapl_linux.go`

There is one genuinely *measured* energy source on Linux, and it is not in `power_supply`.

Intel's Running Average Power Limit interface exposes cumulative microjoule counters:

```
/sys/class/powercap/intel-rapl:0/energy_uj          total package energy
/sys/class/powercap/intel-rapl:0/sub0/energy_uj     core domain
/sys/class/powercap/intel-rapl:0/sub1/energy_uj     uncore domain
/sys/class/powercap/intel-rapl:0/sub2/energy_uj     dram domain
/sys/class/powercap/intel-rapl:0/sub3/energy_uj     psys, when present
```

`delta_uj = current - previous`, with unsigned wraparound handled, gives measured joules over the
interval. This is a real counter maintained by the hardware, not a model, and it is the single
upgrade to the honesty of the Linux numbers: `Status.CPUWatts` and `Point.Provenance ==
ProvCPUMeasured` are measurements.

The constraints, all of which have to be in the doc:

- **It is per CPU package, not per application.** It measures the processor and nothing else. It
  populates `Status.CPUWatts` and never `Status.Watts`, because the display, the radio, the disk
  and the charger are all outside it.
- **It is Intel-specific.** AMD exposes the same directory names on some kernels with different
  semantics, and nothing else has it. `CapRAPLEnergy` is set only when the tree is present *and*
  the counters are readable, so a caller can ask rather than assume.
- **The counters wrap.** Typical granularity is about 1 J per counter on older parts and 15-200 uJ
  on newer ones, and a 64-bit microjoule counter still wraps. The reader treats a negative
  unsigned difference as a wrap and adds `1<<64`.
- **Access is not always free.** The `energy_uj` files are world-readable on most kernels, but the
  `intel-rapl:0/intel-rapl-0:0/control` files are root-owned on some, and reading them at all
  requires the `msr` module on pre-5.10 kernels. A permission error is a `CapRAPLEnergy` that is
  not set plus a note, never a failure of the whole read.
- **The sub-domain counters are not a partition.** `sub0` plus `sub1` plus `sub2` can exceed the
  package total, because the domains overlap in places. cellwatch records the package total and the
  individual domains as separate values and does not sum them.

### 3.5 macOS -- `reader_darwin.go`

The cgo-free route is `ioreg`, which ships with macOS and needs no entitlement:

```sh
ioreg -r -c AppleSmartBattery
```

Output is a nested property-list rendering. Abridged, from real hardware:

```
+-o AppleSmartBattery  <class AppleSmartBattery, id 0x1000002f7, registered, matched, active, busy 0 (0 ms), retain 6>
    {
      "AppleRawMaxCapacity" = 4385
      "CurrentCapacity" = 3320
      "CycleCount" = 187
      "DesignCapacity" = 5103
      "ExternalConnected" = No
      "FullyCharged" = No
      "IsCharging" = No
      "MaxCapacity" = 4385
      "NominalChargeCapacity" = 5103
      "TimeRemaining" = 214
      "Amperage" = -1842
      "Voltage" = 11663
      "Temperature" = 2982
    }
```

Keys used, and the traps:

- **`Amperage` is signed and the sign convention is undocumented.** On Apple hardware it is
  reported **negative while discharging** and positive while charging. The reader takes `abs()` for
  the magnitude and uses `IsCharging` / `ExternalConnected` / `FullyCharged` for direction, never
  the sign. A future change of sign convention therefore cannot flip the direction of the report,
  which is the whole reason for taking the absolute value rather than the sign.
- `Voltage` is millivolts and `Amperage` is milliamps, so
  `watts = Voltage/1000 x abs(Amperage)/1000`.
- `MaxCapacity` is the current full-charge capacity, calibrated and reduced once the pack passes
  its 100-cycle threshold. `AppleRawMaxCapacity` is the raw maximum and `DesignCapacity` is the
  factory figure. Health is `AppleRawMaxCapacity x 100 / DesignCapacity`; when
  `AppleRawMaxCapacity` is absent, fall back to `MaxCapacity` and say so in `Notes`, because the
  two differ and the difference is exactly the calibration loss the figure is supposed to show.
- `TimeRemaining` is in **minutes** and uses sentinels: `-1` means "no estimate", `-2` means "no
  battery / AC only". Both are rejected, not printed as a negative duration.
- `Temperature` is centi-degrees Celsius and is a *pack* temperature, not a CPU temperature. It is
  cosmetic and is not used in any formula.
- On Apple Silicon under aggressive power management `Amperage` can read `0` while the machine is
  discharging. `watts == 0 && !IsCharging` is treated as *unknown*, not as *zero*, and the
  charge-delta window takes over. Printing `0.0 W` there would be a measurement of a number the
  hardware did not report.
- `ioreg` output is undocumented and its formatting has changed across releases. The parser is a
  tolerant key/value scanner that handles both `"Key" = "Value"` and `"Key" = "Value"` forms with
  nested braces, and it **fails closed**: an unparseable stream returns an error, never a
  zero-valued `Status` that renders as "0 %". `testdata/darwin-ioreg.txt` pins the real format, so
  a format change becomes a failing test rather than a user's surprise.
- On MDM-managed Macs an installed configuration profile can restrict `ioreg`. The error surfaced is
  then a permission error from `exec`, and it is reported with the OS message verbatim rather than
  replaced with a generic string.

### 3.6 Everything else -- `reader_other.go`

Covered in §2.6. `ErrUnsupported`, never a silent zero.

---

## 4. Derived metrics

### 4.1 Charge percentage

| Platform | Formula | Guard |
|---|---|---|
| Windows | `ChargePct = float64(BatteryLifePercent)` | `255` -> `ChargePct = 0, ChargeKnown = false`; row omitted |
| Linux | `ChargePct = capacity` | clamp to `[0, 100]`; non-numeric -> `ChargeKnown = false` |
| macOS | `ChargePct = 100 x CurrentCapacity / AppleRawMaxCapacity` | `AppleRawMaxCapacity <= 0` -> fall back to `MaxCapacity`; still `<= 0` -> `ChargeKnown = false` |

macOS is the only platform where the percentage is a division rather than a read, and the only one
where it can drift: `CurrentCapacity` and `AppleRawMaxCapacity` are both integers, so the ratio
jitters by roughly +/- 0.02 % per sample. The window in §4.4 absorbs that.

### 4.2 Instantaneous power

| Platform | Formula | `WattsKnown` when |
|---|---|---|
| Windows | -- | **never**. Unconditionally false. |
| Linux | `watts = power_now / 1e6` | `power_now` missing or `0` |
| macOS | `watts = (Voltage/1000) x abs(Amperage)/1000` | `Amperage == 0` while discharging |

`Status.Wattage` records which of `WattageSysfs`, `WattageIOReg` and `WattageChargeDelta` produced
the figure, so a consumer can weight the two paths differently. Linux CPU energy is reported
separately in `CPUWatts` with `WattageRAPL` and is never summed into `Watts` here.

### 4.3 Capacity, health and cycles

```
fullWh    = energy_full / 1e6                        (Linux, energy layout)  [Wh]
designWh  = energy_full_design / 1e6                 (Linux, energy layout)  [Wh]
healthPct = 100 x fullWh / designWh                  (designWh > 0)
healthPct = 100 x AppleRawMaxCapacity / DesignCapacity                    (macOS)
```

Linux's `charge_*` layout stores uAh, not uWh, so `fullWh` is derivable there only if `voltage_now`
is also present: `fullWh = charge_full x voltage_now / 1e6 / 1e6`. That assumes a flat pack voltage
and is wrong by several percent across a discharge cycle, rising under load. **cellwatch does not
do this.** With the `charge_*` layout and no `power_now`, `FullWh` stays zero, `FullKnown` stays
false, `HealthKnown` stays false, and the snapshot omits the health row. A health figure that is
secretly a nominal-voltage guess is worse than no figure at all, because it is wrong in a way the
reader cannot see.

### 4.4 Charge rate: the quantisation problem, and the four-layer fix

This is the single most important correctness issue in the whole package, and it is carried over
from the previous document essentially unchanged because it was the strongest part of it.

**The naive approach does not work.** Windows reports state at one percentage point of granularity
and no wattage at all. Take two samples one second apart on a 50 Wh pack drawing 10 W:

```
drain rate   = 10 W / 50 Wh          = 0.2 h of full pack per hour
              = 720 %/h
over 1 s     = 720 / 3600 = 0.2 %
```

`0.2 %` is below the one-percentage-point reporting quantum, so `BatteryLifePercent` reads `76` and
then `76`. A two-sample delta gives `dPct = 0` and therefore `0 %/h` -- and then, thirty seconds
later, a single tick where the firmware happens to step over gives `dPct = 1` over 2 s, which is a
nonsensical `1800 %/h`. A naive implementation is not merely imprecise: it produces confident
nonsense alternating with confident zeroes, and a user watching it has no way to tell which is
which.

**The fix has four layers**, all in `rate.go`, all pure functions over a sample slice:

1. **Rolling window, endpoints only.** Keep the last N readings and compute the delta across the
   *endpoints*, never between the last two:

   ```
   window = samples[0 .. n]                             n = window size, default 15
   dPct   = samples[0].ChargePct - samples[n].ChargePct (> 0 = charge went down)
   dT     = samples[n].At - samples[0].At
   ```

   At 15 samples x 2 s that is 30 s of history, during which a 720 %/h pack sheds about 6 % -- six
   quantisation steps instead of one. The quantisation error on the rate falls from +/- 100 % to
   roughly +/- 17 %, and the systematic bias largely cancels, because the firmware's internal
   threshold is fixed while our window is not.

2. **Minimum span.** A window is only `Stable` when

   ```
   n >= 3   and   dT >= 3 x interval
   ```

   A rate computed from one or two ticks is a quantiser reading, not a measurement. The renderer
   prints `n/a - need at least 3 samples` instead of the number, and shows the progress counter, so
   the user knows why they are waiting rather than assuming the tool is hung.

3. **Reject windows spanning an AC transition.** If `AC` or the platform status word changed at any
   sample inside the window, the window is discarded and restarted, because the assumption "same
   hardware, drawing from the same pack" was false for part of it. Across a charge-to-discharge
   transition the true rate is genuinely discontinuous and no estimator is entitled to bridge it.

4. **EMA for display, raw window for the record.** The live display should not flicker, so an
   exponential moving average smooths what the user sees, while the record keeps both:

   ```
   ema <- 0.25 x instant + 0.75 x ema        (alpha = 0.25)
   ```

   The JSON reports `drain_pct_per_hour_estimate` (the EMA), plus `drain_pct_per_hour_window` and
   `window_seconds` / `samples_used` / `stable`, so a consumer can recompute or discount the figure.
   Reporting only the EMA would be reporting a smoothed number with no indication that it had been
   smoothed.

**Prefer the direct power path where it exists.** When `WattsKnown` is true the rate comes from
power, not from a charge delta, and the one-percentage-point quantiser is bypassed entirely:

```
pctPerHour = 100 x watts / fullWh x 3600          (requires FullWh > 0)
watts      = (fullWh x dPct/100) / dT_hours       (charge-delta fallback)
source     = "power_now" | "voltage_x_amperage" | "charge_delta"
```

macOS always has a `FullWh`, because `NominalChargeCapacity` is *stated* by the pack rather than
guessed, so macOS takes the good path. Linux with the `energy_*` layout does too. Linux with the
`charge_*` layout falls back to the delta. Windows always falls back.

### 4.5 Time remaining

Prefer the OS value when it is trustworthy; otherwise derive it.

```
// Windows
osRemaining = time.Duration(BatteryLifeTime) * time.Second
usable      = osRemaining > 0 && osRemaining < 2^31 seconds          (see 3.2)

// Derived, from the window rate
remainingHours = (ChargePct - lowWatermark) / pctPerHour   if pctPerHour > 0
remainingHours = +Inf                                      if pctPerHour <= 0
remaining      = 0                                         if ChargePct <= lowWatermark
lowWatermark   = 20                                        default
```

macOS `TimeRemaining` in minutes is used when `>= 0`; `-1` and `-2` are rejected (§3.5) and the
derived figure is used instead.

A derived figure is rendered as `~5 h 31 m` under a label that says it is an estimate, and an
OS-supplied figure as `5 h 31 m` under `OS estimate`. A tilde is a small, cheap honesty device that
costs nothing in a table.

### 4.6 What is never derived

- **Charge percentage.** Never smoothed, never averaged across samples, never carried forward. The
  OS number is shown or the row is omitted.
- **Health percentage.** Never derived from runtime behaviour, and never from a nominal pack
  voltage. Only the capacity ratio, or omitted.
- **Cycle count.** Never estimated. Integer or absent.
- **AC state.** Never inferred from the sign of a current or wattage reading, only from the
  platform's own field.
- **Energy on Windows.** Never invented. `EnergyWh` is zero with `EnergyKnown` false on every
  unelevated Windows machine, and the usage report says so in `SystemUsage` rather than converting
  percentage points into watt-hours with a guessed pack size.

---

## 5. Capability matrix

The point of this section is that a reader which does not know something must say so *before* being
asked for it, in a form a program can act on. `Capabilities()` is that form; the tables below are
what it returns on each platform, and what the underlying reason is for every absence.

### 5.1 The matrix

| Capability | Windows | Linux | macOS | Note |
|---|:--:|:--:|:--:|---|
| `CapChargePct` | yes | yes | yes | The only capability no platform lacks. |
| `CapACState` | yes | yes | yes | |
| `CapStatusWord` | derived | yes | yes | Windows has flags, not a word; `statusWord` synthesises "Discharging" / "Charging" / "Full" / "Not charging" from `BatteryFlag` and from the class driver's `PowerState`, and that synthesis is documented as a derivation. |
| `CapWatts` | yes | conditional | yes | Linux: only where `power_now` exists. Windows: `IOCTL_BATTERY_QUERY_STATUS` `.Rate`, measured. |
| `CapFullCapacity` | yes | conditional | yes | Windows: `IOCTL_BATTERY_QUERY_INFORMATION` `.FullChargedCapacity`, unprivileged. Linux: only with the `energy_*` layout. |
| `CapDesignCapacity` | yes | conditional | yes | Windows: `.DesignedCapacity` from the same call. Linux: `energy_full_design` is frequently `0` on consumer laptops. |
| `CapHealth` | yes | conditional | yes | Follows from the two above: Windows when both are present and non-zero, Linux when both are present and non-zero, macOS always. |
| `CapCycleCount` | yes | rare | yes | Windows: `.CycleCount` from the same call, measured at 1541 on the development machine. Linux: `cycle_count` exists on very few drivers. |
| `CapOSRemaining` | guarded | **no** | yes | Windows: signed-in-practice field, `> 2^31` rejected, and the class driver's present capacity over its rate supplies a derived figure when the firmware declines to estimate one. Linux: not exposed by the kernel at all. |
| `CapPackTemp` | **no** | **no** | yes | |
| `CapAppEnergy` | **no** | **no** | **no** | See 5.3. |
| `CapNativeLog` | yes | **no** | yes | Windows: `powercfg /batteryreport /xml`. macOS: `pmset -g log`, `-g rawlog`. Linux: nothing. |
| `CapRAPLEnergy` | **no** | conditional | **no** | Linux with an Intel RAPL tree that is present and readable. |

### 5.2 The three conditional rows in full

`CapWatts` on Linux is conditional at the *platform* level in a way that is easy to get wrong, so
the design distinguishes the two questions rather than answering the platform one with a guess.

```
                    CapWatts set?    Status.WattsKnown?
  Windows                    no              always false
  Linux, power_now present    yes             true when the file parses and is non-zero
  Linux, power_now absent     yes             always false
  macOS                       yes             false when Amperage == 0 while discharging
```

The middle two rows are the same platform and different machines, and that is the whole reason
`CapWatts` is a platform bit while `WattsKnown` is a per-read bit. A caller who checks only
`Capabilities()` on Linux learns "this platform can report watts"; a caller who checks only
`WattsKnown` learns about this particular read. A caller who wants "will I get watts here" needs
both, and cellwatch provides both rather than making the caller guess which one it was handed.

The same structure applies to `CapFullCapacity` and `CapHealth` on Linux, and to `CapRAPLEnergy`,
which is set at `NewReader` time only if `/sys/class/powercap` was both present and readable, so a
permission error removes the bit rather than producing a reader that returns an error forever.

### 5.3 `CapAppEnergy` is never set by default, on any platform

Stated separately, prominently, and pinned by a test, because it is the claim this library is most
likely to appear to make and least entitled to make.

| | Windows | Linux | macOS |
|---|---|---|---|
| A whole-machine reader that reports per-application energy | **no** | **no** | **no** |
| An *optional source* that reports it, and under what conditions | SRUM, only when `OpenSRUM` has succeeded: elevated, undocumented ESE, ~30-60 min sampling | **nothing exists** | **nothing exists** |
| What the OS itself has | `powermetrics` per-process CPU power, `sudo`, minutes to produce a usable sample | `powertop` estimates, and calls its own per-process attribution a heuristic | `powermetrics`, same as Windows |

The `Capabilities()` returned by `NewReader().Capabilities()` therefore never contains
`CapAppEnergy` on any platform, and a test says so:

```go
func TestNoCapabilityOverclaim(t *testing.T) {
	r, err := NewReader()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if got := r.Capabilities(); got.Has(CapAppEnergy) {
		t.Errorf("reader claims CapAppEnergy (%s); per-application energy is not available "+
			"from any platform reader, and claiming it is the one overclaim this package "+
			"must never make", got)
	}
	if got := r.Capabilities(); got&^CapAll != 0 {
		t.Errorf("reader claims bits outside CapAll: %08b", uint32(got))
	}
}

func TestNativeLogNotClaimedOnLinux(t *testing.T) {
	// A separate test because it is a separate promise: on Linux there is no
	// OS power history at all, and a caller must be able to ask and be told so
	// before planning around one.
	if runtime.GOOS == "linux" {
		if got := Capabilities(); got.Has(CapNativeLog) {
			t.Errorf("linux claims CapNativeLog (%s); /sys/class/power_supply is "+
				"instantaneous only and there is no OS history store", got)
		}
	}
}
```

A caller that wants per-application *measured* energy on Windows, and can accept elevation and an
undocumented format, calls `NewAppEnergySource` and then adds the returned `Capability` to the one
it already had. The widening is explicit and the caller holds both halves, so the mask can never
quietly grow behind their back.

### 5.4 How a consumer should use the matrix

```go
r, err := cellwatch.NewReader()
if err != nil {
	return err
}
caps := r.Capabilities()
st, err := r.Read()
if err != nil {
	return err
}

switch {
case caps.Has(cellwatch.CapHealth) && st.HealthKnown:
	showHealth(st.HealthPct)
case caps.Has(cellwatch.CapHealth):
	showHealthUnavailable()      // the platform could, this machine did not
default:
	omitHealthRow()              // no such capability here; do not show a row at all
}
```

Three branches, not two: *available and reported*, *available and absent this time*, and *not
available*. Collapsing the middle case into the first is how a UI ends up printing `0% health`, and
collapsing it into the third is how a UI ends up omitting a row on a laptop whose driver simply
returned nothing on one read.

---

## 6. How cellwatch talks to each OS

This is the first question any reader has, so it is answered here in one place rather than split
between §3, which is the per-platform **data dictionary** -- what each field means and where it
traps -- and §7, which is the **historical** path. This section is the **mechanism**: how the bytes
actually arrive, per platform, and what happens when they do not. Read this section and you know
which of the three rows of §3.1 applies to you; read §3 when you need the semantics of an individual
field.

There are three platforms and three different mechanisms, and the reason they differ is not taste.
Windows exposes the power state through the Win32 API and nothing else, so the honest route is a
direct syscall. Linux exposes it through sysfs and nothing else, so the honest route is to read
files. macOS exposes it through IOKit, which has no Go binding in the standard toolchain, so the
honest route is to shell out to a tool that renders the same registry as text.

**The rule that governs all three** is stated once, here, and it is what the rest of the document is
built on: *every reader returns a `Status` plus the `Capability` bitmask of what it could actually
read, and the union of those masks across the three platforms is what a caller may rely on. Nothing
is synthesised to fill a gap.* §5 gives the mask and the matrix; §4 gives the formulas that only
apply where their inputs are present. A platform that cannot supply a field leaves it absent, and
absent is a value in this package rather than an omission.

### 6.1 The answer in one table

| | Windows | Linux | macOS |
|---|---|---|---|
| **Live mechanism** | **Direct syscall.** `GetSystemPowerStatus` from `kernel32.dll`, bound with `x/sys/windows`. No subprocess, no cgo. | **Plain file reads.** No syscall, no cgo, no subprocess: open and read the text files under `/sys/class/power_supply/<supply>/`. | **Subprocess.** `ioreg -r -c AppleSmartBattery`, parsed as tolerant key/value text. |
| **Historical mechanism** | `powercfg /batteryreport /xml` -- measured, machine-wide, writes a file. SRUM `SRUM-DATA.dat` for per-application energy, opt-in. | **None.** No OS power history exists. `/var/lib/upower/history-*` holds charge *events*, imported only as a transition cross-check. | `pmset -g log` for sleep/wake/AC transitions, `pmset -g rawlog` for battery state read directly from the pack. |
| **Process data** | gopsutil `process` via `yusufpapurcu/wmi` | gopsutil `process` via its `/proc` reader | gopsutil `process` via `ebitengine/purego` |
| **Privilege needed** | **None** for the live path. Elevation only for the two optional extras: the battery IOCTL is `GENERIC_READ\|GENERIC_WRITE` (administrator) and SRUM lives under `C:\ProgramData`. | **None.** The RAPL `control` files are root-owned on some kernels and reading RAPL at all needs the `msr` module pre-5.10, which yields a capability that is not set rather than a failure. | **None** normally. The man page describes `pmset -g log` as being "for admin & debugging purposes", and an MDM profile can restrict `ioreg` -- measured in Phase 0, not assumed. |
| **Failure mode** | `GetSystemPowerStatus` returning 0 is an error wrapping `GetLastError`. `BatteryFlag == 255` is *not* an error: it is a valid answer meaning "unknown everything", and it must render as absent. | **Missing files are the normal case, not an error.** `power_now` and `cycle_count` are usually absent; the reader reports them absent and carries on. A read that finds no `type == Battery` supply is a desktop, which is a correct answer. | An unparseable `ioreg` stream is an **error**, never a zero-valued `Status` that renders as "0 %". A `pmset` permission error is surfaced verbatim from `exec` rather than replaced with a generic string. |
| **New dependency** | **none** (`x/sys` is already at `go.mod:18`) | **none** | **none** (process data only, via gopsutil) |

Two of the six rows are the interesting ones. **Privilege** is `none` for all three live paths, which
is the design constraint that shaped §3.2 and §7.2: a measurement library that demands elevation on a
common path stops being a measurement library, so the administrator-gated Windows sources are behind
an opt-in seam rather than in the default path. **Failure** is not uniform either -- Linux's absent
file is normal, Windows' `255` is a valid-but-empty answer, and macOS' unparseable stream is a hard
error -- and collapsing those three into one "error" type would be wrong in two directions at once.

### 6.2 Windows -- a direct syscall, no subprocess

`GetSystemPowerStatus` is called directly. `golang.org/x/sys v0.47.0` is already a direct dependency
at `go.mod:18`, and its `windows` package provides everything the binding needs:

| Symbol | Location in `x/sys/windows` | Used for |
|---|---|---|
| `NewLazySystemDLL` | `dll_windows.go:249` | load `kernel32.dll` at first call, not at init |
| `(*LazyDLL).NewProc` | `dll_windows.go:234` | resolve `GetSystemPowerStatus` |
| `(*LazyProc).Call` | `dll_windows.go:314` | invoke it |

So this path costs **zero new modules and zero subprocesses**. The lazy loading is deliberate: a
resolved proc is resolved on first use, so a `cellwatch` binary that never asks for battery state
never touches `kernel32.dll`.

**The struct is hand-declared, and that is a finding rather than an omission.** `SYSTEM_POWER_STATUS`
is not in `x/sys` at all: grepping the entire `x/sys` tree returns zero matches for either
`SYSTEM_POWER_STATUS` or `GetSystemPowerStatus`. The six-field, twelve-byte struct at §3.2 -- four
bytes and two `uint32`s, in that order, with no padding on any architecture Go targets -- is therefore
written here rather than imported, and it is mirrored from the Win32 documentation rather than from
Go.

The per-field semantics are a data dictionary and they live in §3.2, which should be read for them.
The mechanism-level facts belong here:

- **No elevation.** The function is callable by any process in the interactive session.
- **The richer data is behind a device handle, not behind this function.** Design and full capacity
  come from `IOCTL_BATTERY_QUERY_INFORMATION` on the battery class device, which requires opening
  that device with `GENERIC_READ | GENERIC_WRITE` -- administrator. This is the optional deeper
  route, it is not in the default path, and it is not behind a flag either: a consumer that wants it
  calls the IOCTL itself and passes the result to the store as a correction, which is what
  `FullWhKnown` and `DesignWhKnown` exist for.
- **The historical routes are both subprocesses.** `powercfg /batteryreport /xml` writes a file and
  is parsed; SRUM opens an undocumented ESE database. Both are §7.2, both are optional, and both are
  the only way this platform produces *measured* watt-hours rather than a derived rate.

### 6.3 Linux -- plain file reads, no syscall, no subprocess

The Linux reader opens and reads text files. It does not import `syscall`, does not use cgo, and
spawns nothing. Enumerate `/sys/class/power_supply/`, select the first entry whose `type` file reads
`Battery`, and read the attribute files. The full list with units is §3.3; the ABI facts that decide
the reader's behaviour were verified against the kernel's own documentation,
`Documentation/ABI/testing/sysfs-class-power` (906 lines), and four of them are load-bearing:

- **`capacity` is 0-100.** Some OEM firmware returns a value above 100, so it is clamped, and the
  clamp is recorded rather than hidden.
- **`status` is one of `Unknown`, `Charging`, `Discharging`, `Not charging`, `Full`.** This -- not
  any numeric field -- is the direction of travel.
- **`current_now` is microamps and is negative while discharging.** This is the same trap as macOS
  `Amperage` and is handled the same way: the reader takes `abs()` for the magnitude and uses
  `status` for direction, never the sign. A future kernel that flips the convention therefore cannot
  flip the direction of a report.
- **`cycle_count` is driver-optional.** Very few drivers implement it. It is reported absent when the
  file is not there, which is the common case.

**AC state is `type == Mains` with `online == 1`, not the directory name.** Directory naming is
vendor-specific (`AC`, `ACAD`, `ADP1`, `WWAN`) and `ACAD` in particular can read `0` while mains is
live, because it means "adapter present" rather than "adapter energised". The `type` file is the
kernel's own classification and is uniform; `online` is the field to trust. The full argument is §3.3.

The deeper Linux source is **Intel RAPL**, at `/sys/class/powercap/intel-rapl/`, which is a *measured*
per-package microjoule energy counter rather than a model. It is worth being precise about what that
buys: `Status.CPUWatts` becomes `ProvCPUMeasured` rather than an estimate, and nothing else changes.
It is per CPU package and not per application, it is Intel-specific, the counters wrap, and the
sub-domain counters are not a partition of the package total. All of that is §3.4.

Process data comes from gopsutil's `/proc` reader, which is the one part of the per-OS surface where
all three platforms share a single implementation and differ only in the `purego`/`wmi` helper.

### 6.4 macOS -- two subprocesses, no cgo

macOS exposes battery state through IOKit, and the standard Go toolchain has no cgo-free binding for
it, so both paths go through a shipped command.

**Live: `ioreg -r -c AppleSmartBattery`.** `ioreg` is a text renderer over the same IORegistry the
framework API reads, and its output for the `AppleSmartBattery` object is a nested property list. The
parser is a tolerant key/value scanner and it **fails closed**: an unparseable stream returns an
error, never a zero-valued `Status` that renders as "0 %". `testdata/darwin-ioreg.txt` pins the real
format so that a format change across a macOS release becomes a failing test rather than a user's
surprise. The per-key traps -- the undocumented sign convention on `Amperage`, the minute-valued
`TimeRemaining` with its `-1` and `-2` sentinels, the centi-degree pack temperature -- are §3.5.

**Historical: `pmset -g log` and `pmset -g rawlog`.** Both are shipped, both are documented in the
`pmset(1)` man page, and `rawlog` is the interesting one: "ongoing log of battery state as read
directly from battery", which is the raw pack telemetry behind the smoothed `SMC` values `ioreg`
reports. It is strictly higher fidelity than the live path. Its limits -- rotated rather than
archived, undocumented text format, admin-oriented, machine-wide with no per-application component --
are §7.1.

**gopsutil does not provide battery on darwin.** This was checked by grepping its darwin source for
`iokit`, `IOPS` and `IORegistry`: on the power path there is nothing. It has `sensors`, not
`battery`, and no `IOPSCopyPowerSourcesInfo` binding anywhere in it. This is a statement of fact
about the dependency, not a preference, and it is why the darwin reader is hand-written where the
Linux one is not.

**The rejected alternative, recorded so it is not re-proposed.** IOKit does have a cgo-free route to
the same data: `IOPSCopyPowerSourcesInfo` bound through `ebitengine/purego`, which is *already* an
indirect dependency of gopsutil for darwin (`go.mod`, via §17.1), so it would add nothing. It was
considered and rejected. `IOPSCopyPowerSourcesInfo` returns an opaque `CFTypeRef` that has to be
walked through CoreFoundation -- `CFArrayGetValueAtIndex`, `CFDictionaryGetValue`, `CFNumberGetValue`,
`CFStringGetCString`, `CFRelease` -- and every one of those is a separately-bound `purego` symbol
with its own calling convention and struct layout assumptions. The failure mode of getting one of them
wrong is not a crash; it is a plausible wrong number, which is precisely the class of defect this
document refuses to produce. `ioreg`, by contrast, is versioned with the OS, is present on every
machine, and a change in its output is caught by a fixture test at the next CI run. The trade is
worth taking, and the reason is that a wrong number that compiles is worse than a missing number that
fails a test.

The one macOS-specific operational hazard is **MDM**: an installed configuration profile can restrict
`ioreg`. When that happens the error surfaced is a permission error from `exec`, reported with the OS
message verbatim rather than replaced with a generic string, because the generic string would be a
diagnosis the library has not earned.

### 6.5 The rule that makes the abstraction honest

The mechanism differs per platform; the contract does not. Every reader -- all three, plus the
fallback in §3.6 -- obeys the same three rules, and they are what make a single cross-platform type
honest rather than lossy:

1. **A reader returns a `Status` and a `Capability` mask together, and the mask says what this build
   could reach.** §5.1 is the matrix and §5.4 is how a consumer is meant to read it; §5.3 is the
   worked case of a capability that is claimed by nothing until it has actually been earned, and it
   is pinned by a test so that it cannot drift.
2. **Absent is a value.** `ChargeKnown`, `WattsKnown`, `CycleCountKnown` and their siblings exist so
   that "the platform did not report this" is representable and therefore renderable, rather than
   collapsing into `0`. This is the distinction §5.4 draws between *available and reported*, *available
   and absent this time*, and *not available* -- three branches, not two.
3. **The capability is a property of the reader; the absence is a property of the sample.** Conflating
   them is how a battery monitor ends up reporting `0.0 W` on a desktop. `CPUWatts` is absent on
   macOS because the platform cannot reach it; `Amperage` reading `0` on Apple Silicon under
   aggressive power management is absent because the hardware did not report it this tick. Both render
   the same way, and neither is `0`.

**What is deliberately not built.** There is no pluggable syscall layer and no runtime driver
registry. One reader per platform, selected by build tag, with a compile-time `ErrUnsupported`
fallback everywhere else. A pluggable transport would mean every platform-specific field becomes
runtime-reflective and compile-time-checkable field access disappears, which costs more than it buys
for three known platforms.

The single exception to rule 1 is worth stating because it is the one place a capability is claimed
by the library rather than reported by a reader: `CapAppEnergy` is set by **nothing** on any platform
until an SRUM reader has actually opened its database (§5.3). Per-application energy is the one
capability no mainstream operating system hands over, and the design says so rather than approximating
it -- which is the subject of §11.

---

## 7. Data collection: is there a native OS monitoring system we are not talking to?

**This section is the historical half only.** How cellwatch talks to each OS for *live* state is
§6, which answers it in one table and should be read first; the per-field semantics of the live path
are §3. What follows is the other half: whether any of the three platforms keeps a power history
that the sampler can backfill from, and what each one is worth.

This is the right question to ask and it has a partial answer, on two of the three platforms, in
proportion to how much any of them actually helps.

### 7.1 macOS: yes, and it is good

`pmset` keeps a power-management history, and the man page documents five ways to read it. Verified
against the `pmset(1)` man page:

| Flag | What the man page says it does |
|---|---|
| `pmset -g log` | "displays a history of sleeps, wakes, and other power management events" |
| `pmset -g pslog` | "ongoing log of power source (battery and UPS) state" |
| `pmset -g rawlog` | "ongoing log of battery state as read directly from battery" |
| `pmset -g thermlog` | thermal log |
| `pmset -g sysloadlog` | system load log |
| `pmset -g assertionslog` | power assertion log |

`-g rawlog` is the interesting one: it is the battery state **as read directly from the battery**,
which is the raw pack telemetry behind the smoothed `SMC` values that `ioreg` reports. It is
strictly higher fidelity than the live path this library already has.

The caveats, all of which have to be in the documentation:

- **The man page describes `-g log` as being "for admin & debugging purposes."** That is a statement
  of intent rather than a documented privilege requirement, and whether it needs elevation varies
  across macOS releases and MDM configurations. Phase 0 must record the answer on real hardware.
  The implementation probes once, records the answer in a fixture, and degrades to the sampler if
  the read is refused -- so getting it wrong costs a feature, not a failure.
- **The log is rotated, not archived.** It is a ring of recent events. It is not an archive of every
  minute since the machine was new, and it must never be presented as one.
- **The format is a fixed-width text log and it has changed across releases.** It is parsed with a
  tolerant scanner and pinned by `testdata/darwin-pmset-log.txt`.
- **It is machine-wide.** There is no per-application energy in any of the five logs.

`native_darwin.go` therefore implements two importers: `pmsetLogSource` for sleep, wake and AC
transitions, and `pmsetRawSource` for charge, voltage and current samples. Both are `NativeSource`
implementations and both go through the same `Import` path as everything else in §7.5.

### 7.2 Windows: partly, and the good part is admin-gated and undocumented

There are two, and they are worth very different amounts.

**`powercfg /batteryreport` -- measured, machine-wide, and usable.**

It writes a report rather than answering a query, so the shape is "run it, parse the file". The
XML form is the one to use:

```
powercfg /batteryreport /output "%TEMP%\battery.xml" /xml
```

The XML contains `BATTREPORT/BATTERY/NL` entries with per-minute `RecentUsage` records carrying a
`Duration` and a `Capacity` in mWh. That is **measured energy over time, produced by the operating
system**, for a window of recent history, and it is the only source on any platform that gives
cumulative watt-hours on Windows -- which matters enormously, because §4.6 says cellwatch will not
invent `EnergyWh` there, and this is the honest way to obtain it.

Its costs: it needs elevation on some Windows builds, it writes a file, the XML schema is stable
only in the sense that nobody has changed it recently rather than that it is documented, and it
covers recent history only.

**SRUM -- per-application, closest to real, and the most fragile thing in this document.**

The System Resource Usage Monitor keeps its database at
`C:\ProgramData\Microsoft\Windows\SRU\SRUM-DATA.dat`. Its `SruDbCheckpoint` table holds one row per
application per time slice, and one of those columns is `EnergyEstimation`: energy attributed to
that application, in mWh. It is the closest thing to per-application battery drain that any
mainstream operating system records, and it is the only thing in this document that could ever set
`CapAppEnergy`.

Three constraints, all of which must be stated wherever the feature is mentioned:

1. **It requires elevation.** `C:\ProgramData\Microsoft\Windows\SRU` is not readable by a standard
   user, and `SRUM-DATA.dat` is frequently held open by the SRUM service itself. A read fails
   routinely and the failure is `ErrNeedsElevation`, not an empty result.
2. **The format is an Extensible Storage Engine database, undocumented by Microsoft, and it changes
   between Windows releases.** Nothing in the Go standard toolchain reads ESE. The third-party
   readers that exist are reverse-engineered and break on new builds. cellwatch does not ship an ESE
   parser: that is a project in itself, and shipping a broken one would be worse than not shipping
   one. The design is the seam, and the implementation is a decision for whoever picks it up:

   ```go
   // srum_windows.go
   //
   // AppEnergySource is a narrow interface precisely so that the parser can
   // live in another module until it is good enough to live here. See 7.3.

   // SRUMSource is the set of third-party readers this package knows how to
   // drive, declared as a variable rather than a type so that an alternative
   // can be registered by a consumer without a fork:

   //     cellwatch.RegisterSRUMReader(myReader)
   var srumReader SRUMReader

   // SRUMReader is what an SRUM implementation has to provide.
   type SRUMReader interface {
       // Open the database read-only, at whatever privilege the process has.
       // It must return ErrNeedsElevation rather than an empty result when it
       // cannot read the file.
       Open(path string) error
       // AppEnergy returns per-application energy in mWh for the half-open
       // window [from, to), keyed by executable name.
       AppEnergy(from, to time.Time) (map[string]float64, error)
       // Close releases the handle.
       Close() error
   }

   // RegisterSRUMReader installs an SRUM implementation. It is not
   // concurrency-safe and is meant to be called from an init function or
   // from main before any sampler starts. Registering nil restores the
   // built-in position, in which OpenSRUM returns ErrUnsupported because no
   // reader is compiled in.
   func RegisterSRUMReader(r SRUMReader)
   ```

   This is the honest structure. The *capability* is real and is claimed; the *parser* is a
   separately-versioned, separately-licensed, best-effort component that a consumer chooses.

3. **`EnergyEstimation` is a model output, not a measurement.** The SRUM subsystem computes it, and
   it is refreshed every 30 to 60 minutes and only while an application has foreground activity. A
   process that ran for ninety seconds and exited leaves no record at all. It gets
   `ProvAppEnergyReported` rather than `ProvMeasured` for exactly this reason, and a report that
   mixes it with apportioned figures must show which rows came from where.

### 7.3 Linux: nothing

There is no operating-system power history on Linux. The situation, stated completely:

| Candidate | What it actually is |
|---|---|
| `/sys/class/power_supply/` | Instantaneous only. No ring buffer, no history, no persistence. Reading it now tells you what is true now and nothing about an hour ago. |
| `powertop` | Computes *estimates* by differencing `power_supply` and attributing to processes. It can save and load its parameters (`powertop --save`, `--load`) but those are model parameters, not a measurement history. It is a tool that watches, not a database that remembers. |
| `systemd-journald` | Can hold any log line a program writes, and a program could write a power sample to it. That is a program doing what cellwatch's sampler does, through a different transport. It is not an OS power feature, and nothing writes power samples to it by default. |
| `upower` history files | `/var/lib/upower/history-charge-*.txt` and `history-*.txt` are real, readable without elevation, and genuinely maintained by a shipped daemon. They record **events** -- AC connected, AC disconnected, charge threshold crossed, reaching 0% -- and not samples. A useful cross-check for transitions, worth importing, and categorically not a substitute for history. |
| Intel RAPL | A real measured energy counter, per CPU package. See §3.4. It is measured, it is not per-application, and it is hardware-specific. |

The conclusion, plainly: **on Linux there is no native historical source, so the completeness of
recorded history depends entirely on whether the sampler was running.** A `cellwatch` report from a
Linux machine that has never run the sampler is an empty report with a coverage of 0 %, and it must
say so rather than rendering zeroes that look like a machine that used no power.

### 7.4 The verdict

> **Partly, and not where it would help most.**
>
> - **macOS: yes.** `pmset -g log` and `pmset -g rawlog` are real, shipped, documented
>   power-management history, and `rawlog` is higher fidelity than anything the live path can
>   produce. Limits: rotated rather than archived, undocumented text format, admin-oriented, and
>   machine-wide with no per-application component.
> - **Windows: partly.** `powercfg /batteryreport /xml` gives measured machine energy per minute
>   over recent history. SRUM gives **per-application** `EnergyEstimation` in mWh, which is the only
>   per-app energy figure any mainstream OS records. Limits: SRUM is an undocumented ESE database
>   that changes between builds, needs elevation, and is often locked; and it is a model output
>   sampled every 30 to 60 minutes.
> - **Linux: no.** There is no OS power history. `power_supply` is instantaneous, `powertop`
>   estimates, `upower` keeps only charge events, and the one measured counter (Intel RAPL) is
>   per-package and Intel-only.
>
> **Therefore a background sampler is still required** for complete, continuous, cross-platform
> history. Native logs are a **cross-check and backfill** source where they exist, which improves
> fidelity and fills the holes a lapsed sampler left -- they are not a substitute. And on Linux in
> particular, history completeness depends entirely on the sampler having been running. A user
> asking this question deserves to be told that before they install anything.

### 7.5 The hybrid architecture

```
   live sampler (opt-in, identical code, all three platforms)
        |
        |  Sample{At, Status, Apps, Provenance: ProvMeasured}
        v
   +----------------------------------------------------+
   |  battery_raw        one row per sample, at = rowid  |
   |      |                                            |
   |      |  recompute, never accumulate               |
   |      v                                            |
   |  battery_rollups    tier + bucket_start, one row  |
   |  + apps blob        packed per-app slice per row   |
   +----------------------------------------------------+
        ^
        |  NativeRecord{At, ..., Provenance: ProvImported}
        |
   native-log import, capability-gated and optional
     darwin    pmset -g log, pmset -g rawlog
     windows   powercfg /batteryreport /xml
     windows   SRUM-DATA.dat          [admin, undocumented, best-effort, opt-in]
     linux     (nothing) + upower events, as a transition cross-check
```

The sampler is the backbone: it works identically on all three platforms, needs no elevation, and is
the only source of continuous data. Native import is a second, optional path into the same buckets.

### 7.6 Import semantics

```go
// NativeSource is a platform power-management history log that cellwatch can
// import. The set of compiled-in sources is platform-specific and small:
// pmset on macOS, powercfg on Windows, nothing on Linux.
//
// A NativeSource is read-only and stateless. Importing the same window twice
// produces the same records, because import writes through the same
// recomputing rollup path as the sampler. See 10.4.
type NativeSource interface {
	// Name identifies the source in logs, in ImportReport, and in the
	// Provenance of anything it wrote. It is "pmset-rawlog" or
	// "powercfg-batteryreport".
	Name() string

	// Earliest reports the oldest instant this source can still answer for,
	// which is however far back the platform happens to have retained its
	// log. The zero time means "no history", and Import uses it to refuse a
	// backfill that would silently produce nothing.
	Earliest(ctx context.Context) (time.Time, error)

	// Read returns native log records covering the half-open window
	// [from, to). It returns an empty slice, not an error, when the window is
	// simply outside the log.
	Read(ctx context.Context, from, to time.Time) ([]NativeRecord, error)

	// Capabilities returns what this source can supply. For a log importer
	// that is CapNativeLog; for the SRUM source it is CapAppEnergy, and only
	// once the source has actually been opened.
	Capabilities() Capability
}

// NativeRecord is one entry from a platform history log. Not every platform
// fills every field, and Absent is the honest statement that the log did not
// record it. cellwatch does not substitute a value for an absent one.
type NativeRecord struct {
	// At is the instant the record describes, in UTC.
	At time.Time

	// Present mirrors Status.Present, and is false for a sleep or wake event
	// rather than for a battery reading.
	Present bool

	// Event names a non-sampling entry such as "sleep", "wake", "ac_on" or
	// "ac_off". It is empty for a battery reading. Import turns events into
	// AC transitions and into coverage boundaries, because a machine that was
	// asleep was not being sampled and pretending otherwise would inflate the
	// observed time.
	Event string

	AC        ACState
	Status    string
	ChargePct float64
	HasCharge bool

	// EnergyWh is measured energy over the interval since the previous
	// record. powercfg supplies this; pmset does not.
	EnergyWh    float64
	HasEnergy   bool

	// App and AppEnergyWh are non-zero only for the SRUM source.
	App          string
	AppEnergyWh  float64
	HasAppEnergy bool
}

// ImportReport is what an import did, in enough detail to tell a user
// whether they now have better data or merely more data.
type ImportReport struct {
	// Source is the NativeSource's Name.
	Source string

	// Records is how many records the source returned, Inserted how many were
	// new, and Superseded how many replaced a sampler record at the same
	// instant.
	Records   int
	Inserted  int
	Superseded int

	// Conflicts is the number of instants where the sampler and the native
	// log disagreed about charge by more than two percentage points -- more
	// than one, because the sampler's own window can put a record on either
	// side of a boundary. A non-zero Conflicts is a signal, not an error,
	// and Import does not resolve it silently.
	Conflicts int

	// Earliest and Latest bound what was imported.
	Earliest time.Time
	Latest   time.Time

	// Notes carries anything a renderer should say, including the
	// "source is rotated, older data does not exist" case.
	Notes []string
}

// Import writes native records for [from, to) into st.
//
// The precedence rule, which is the whole design: **a live sampler record
// always wins; a native record fills a bucket the sampler did not cover; and
// where both exist and disagree, both are kept, the sampler's is used, and
// the disagreement is counted in ImportReport.Conflicts.** The alternative
// -- letting the native log overwrite a live observation -- would mean a
// rotated, lower-resolution log silently degrading data that was better.
//
// Import is safe to call repeatedly, safe to call on a window the sampler
// also covered, and safe to interrupt. It is a recompute, not an append:
// see 10.4.
func Import(ctx context.Context, st Store, src NativeSource, from, to time.Time) (ImportReport, error)
```

`NativeSource` discovery, so a caller does not have to know which platform it is on:

```go
// NativeSources returns the platform log importers compiled into this build.
// On Linux it returns an empty slice, and that is the honest answer: a caller
// that iterates the result and finds nothing should say so rather than fall
// back to a source that does not exist.
func NativeSources() []NativeSource

// AppEnergySource reports per-application energy, where the platform records
// any at all. NewAppEnergySource returns ErrNoAppEnergySource on macOS and
// Linux, and on Windows until a SRUM reader has been registered and the
// database has been opened successfully.
type AppEnergySource interface {
	// Capabilities returns CapAppEnergy once the source is open, and
	// CapNone until then. It is the mechanism by which a caller cannot claim
	// a capability it has not earned.
	Capabilities() Capability

	// Read returns per-application energy for the half-open window
	// [from, to), keyed by executable name as the platform spells it, not by
	// the folded grouping key: folding is a presentation decision and
	// belongs above this layer.
	Read(ctx context.Context, from, to time.Time) (map[string]float64, error)

	// Close releases any handle, including SRUM's read-only file handle.
	Close() error
}

// NewAppEnergySource opens the platform's per-application energy source.
//
// It returns ErrNoAppEnergySource where none exists (macOS, Linux), and
// ErrNeedsElevation on Windows where one exists but this process may not
// read it. It is never called implicitly and nothing in this package opens it
// on its own.
func NewAppEnergySource() (AppEnergySource, error)
```

---

## 8. Storage alternatives considered

### 8.1 The decision, and the measurement behind it

`github.com/ncruces/go-sqlite3` v0.35.4, a direct dependency at `go.mod:11` long before this design
was written, into a database of its own at `~/.chest/battery.db`.

- `internal/indexer/indexer.go:20` already carries `_ "github.com/ncruces/go-sqlite3/driver"`. The
  driver is registered in every binary that imports that package, which is every binary CHEST
  ships.
- The compiled WebAssembly SQLite lives in
  `github.com/ncruces/go-sqlite3-wasm/v5/sqlite3.go`, **4,690,964 bytes** of Go source, and it is
  **already compiled in**: the shipped `chest.exe` (35,919,872 bytes) contains **10,866 `sqlite3`
  string hits** and the `SQLite format 3` file magic.
- The extensions that would have added payload are **not** in the binary. `fts5.go` (836 kB),
  `rtree.go` (237 kB) and `vec1.go` (364 kB) ship in the module and are **not imported by
  `internal/`**, so a plain `database/sql` battery store adds no new payload at all.

So: **zero new dependencies, zero new binary size, no cgo.** The WebAssembly payload is a sunk cost
CHEST is already paying for the file index, and §8.2 is about what the previous version of this
document got wrong by not noticing.

One thing the choice is not, and it is worth being blunt about: SQLite is not being adopted because
it is the best store for time series. It is being adopted because the engine is already on the
machine, and "already there" beats "better" at a price of zero against any alternative that is not
there. §9 then says what that engine is actually good at for this workload, which is more than the
old document gave it credit for: a real query planner, secondary indexes for free, and a
multi-process concurrency model that bbolt does not have.
### 8.2 The correction to the record

The previous version of this section rejected SQLite on the grounds that it drags a **~1.5 MB
embedded WebAssembly blob** into six release binaries. That reasoning was wrong, and the correction
is recorded here rather than made silently, because the way it was wrong is the kind of error worth
not repeating.

**It counted a sunk cost.** The blob is not a cost of *using* SQLite; it is a cost CHEST has already
incurred for the file index, and it is in every shipped binary today whether or not a battery
feature exists. The comparison that mattered was "bbolt's 200 kB against SQLite's 1.5 MB", and both
sides of it had the wrong baseline. The correct comparison is "SQLite's 0 kB against bbolt's
200 kB", because adding a battery store changes nothing about what the linker includes.

That inverts the conclusion, and it inverts it on the axis the old document called decisive. The
old §9.2 recorded a "~1.5 MB embedded WebAssembly SQLite blob in every binary" as a reason to reject
the option, and §9.3 listed "Dependencies" as one of two rows marked **Yes, it decides**. Both were
counting bytes that were already being shipped.

The general form of the mistake: **a design document that recommends or rejects a dependency on the
grounds of what it would cost a user who does not have that dependency yet has not read the
repository it is designing for.** This one had, and the check costs one command:

```bash
strings -a chest.exe | grep -c sqlite3     # 10866
```

The lesson generalises past this decision, which is why it is stated rather than quietly dropped. A
"dependency cost" that is not measured against the current build graph is a guess, and a guess
presented as an argument is worse than no argument, because it is indistinguishable from one.
### 8.3 Considered and rejected, and why the comparison is moot now

The options below were real candidates under the old bbolt-first framing, and two of them were
genuinely competitive. They are **moot as alternatives** for two reasons that did not exist when
they were assessed: every one of them is a key/value store competing for a workload that SQLite now
covers at zero cost, and the question they were being compared on -- "how many modules does this
add" -- no longer distinguishes anything, because the correct answer is zero for all of them.

One line each, so that nobody spends an afternoon on them again. The version numbers were verified
against the module proxy rather than recalled, because a document that recommends a version it has
not looked up is a document that will be wrong within eighteen months.

| Option | Verified | The one-line flaw, now |
|---|---|---|
| **bbolt** v1.5.0 | `go.etcd.io/bbolt` | Ruled out by the user, and its two selling points are answered elsewhere: schema change is `CREATE TABLE IF NOT EXISTS` (§9.5), and multi-process concurrency is WAL (§9.4). |
| **Badger** v4.9.6 | `dgraph-io/badger/v4` | 320 MB of default memtable and block cache, which a **library** cannot override on its consumer's behalf, for a store that fits in RAM. |
| **NutsDB** v1.1.0 | `nutsdb/nutsdb` | Nine modules, one of them last tagged in 2019, for a store that needs none. |
| **BuntDB** v1.3.2 | `tidwall/buntdb` | In-memory with a log replayed in full on open: the wrong durability model for append-heavy, long-lived data. |
| **Pebble** v1.1.5 | `cockroachdb/pebble` | A CockroachDB building block, not an embedded store, and its maintenance cadence is set by a distributed database. |
| **BadgerHold** v1.0.0 | `timshannon/badgerhold` | Unmaintained since 2020. |
| **`boltdb/bolt`** v1.3.1 | `go.etcd.io/bolt` | Pre-fork; the maintained fork is bbolt, so there was never a choice to make. |
| **`natefinch/atomic`** v1.0.1 | `natefinch/atomic` | Not a store: a JSON file, whole-file rewrite, no transactions, no atomic multi-key update. |
| **CloverDB** | -- | No published Go module could be found under any plausible path. |
| **`mattn/go-sqlite3`** v1.14.52 | `mattn/go-sqlite3` | Requires cgo, which `CONTRIBUTING.md:104` and the `CGO_ENABLED=0` matrix forbid. `ncruces/go-sqlite3` is the pure-Go form. |

The two that deserved the long analysis they got -- Badger and NutsDB -- are recorded here in one
line each, and the reasoning above is why that is enough. Both lost on a comparison against an
engine that was never actually a candidate, because the cost of that engine was measured wrong.

One observation is carried forward rather than discarded, because it is now a feature of the chosen
engine rather than an argument for a rejected one: **BuntDB has native secondary indexes, and
Badger does not.** §9.5 builds a primary key that makes the common queries range scans and leans on
the rollup ladder to keep every query bounded. Had the chosen engine needed a hand-built
application-forward index, as the bbolt design did, that would have been a real cost rather than a
footnote.
### 8.4 What would change it

Two things, and neither is a preference. They are stated as conditions so that the next person to
have this argument knows what the argument was about.

1. **A user-configurable key set** -- "let me choose which applications get their own rows". That
   turns the application registry from a derived table into a validation surface, and the fold
   table of §11.3 into untrusted input. Feasible, and a materially different piece of code.
2. **The portability requirement**: a battery history that must be readable with no cellwatch binary
   and no SQLite tooling on the machine at all. At that point the file format is the deliverable
   and the database was never the point -- an append-only text file becomes the correct answer, and
   this is a requirements change rather than an implementation detail. §9.8 records that moving the
   tables to a different file is a one-line change, so this condition is recoverable rather than
   fatal.

Note what is **not** on this list, because each of them was on the old list and each has been
answered:

- *Concurrent multi-process writers.* The previous version of this section named this the one
  condition that "changes the shape of the answer". It is now the chosen engine's headline
  feature. §9.4.
- *User-defined queries and arbitrary group-by reporting.* This is a query-language requirement,
  and SQLite has a query language. The fixed query set of §2.10 means it has not arisen, but the
  answer is no longer a refusal.
- *A store past roughly a gigabyte.* §10.7 puts it at tens of megabytes, and the growth curve
  flattens after year one.
- *Schema migration.* `CREATE TABLE IF NOT EXISTS` in a single `db.Exec` is the migration story,
  and it is the mechanism this repository already uses. §9.5.

---

## 9. Storage: `~/.chest/battery.db`

### 9.1 The decision

**SQLite, in its own file, beside `index.db` rather than inside it.**

`~/.chest/battery.db`, opened with `sql.Open("sqlite3", path)`, `SetMaxOpenConns(1)`,
`PRAGMA journal_mode=WAL`, `PRAGMA busy_timeout=5000` -- the exact pattern at
`internal/indexer/indexer.go:77-85`, and §9.4 explains why each line of it is load-bearing rather
than a default to be improved on.

Three properties are doing the work:

- **It costs nothing.** Same driver, already linked, already paid for. §8.1.
- **Its lifecycle is independent of the file index.** `chest clean` cannot destroy it, because
  `chest clean` does not know it exists. §9.8.
- **It is portable.** One file to copy, and a standard tool to read it. §9.8.

The path is `~/.chest/battery.db` and not something more specific. It sits beside `index.db` and
`history.json` so that every piece of CHEST's local state is in one directory a user can find
without being told, which is the convention already set at `internal/cli/repl.go:485-491` and
`internal/history/history.go:31`. The name is `battery.db` rather than `cellwatch.db` because the
file is CHEST's, not the library's: §1.3 rule 5 puts the path decision on the caller, and a
standalone extracted `cellwatch` would choose something else entirely (§9.8).
### 9.2 Why a separate file rather than tables in `index.db`

Putting the battery tables into `~/.chest/index.db` was the first decision, and it is a bad one.
The argument is recorded because it is short, because it was nearly adopted, and because it is the
kind of thing that gets re-proposed by the next person who notices the tables would fit.

`chest clean` (`internal/cli/intel.go:107`, `newCleanCmd`) deletes `~/.chest/index.db` outright
under `--all`, via `store.DeleteDB()` at `intel.go:139-141`. Battery history is months of
continuously collected measurement that no other tool on the machine can reconstruct -- §7.4 is the
long version of why. Any decision that leaves `chest clean` able to delete it is a decision that
will, eventually, delete it. Not through malice, and not through a bug: through a user typing a
command whose name suggests it is a safe thing to run.

The mitigations that were considered all manage the hazard rather than removing it, and each is
worse than the problem:

- **A `--keep-battery` flag** leaves the default deleting the data. The safe behaviour becomes
  opt-in and the dangerous behaviour is what you get by typing `chest clean`.
- **Documenting it** leaves the same default and adds a note that nobody reads at the moment they
  type the command.
- **Changing the default** so that `clean` preserves battery history alters the meaning of an
  existing command for every existing user, which is a worse surprise than the one it prevents.

A separate file removes the hazard instead of managing it. The cost is a second connection and a
second file on disk, and both are trivial: a few kilobytes of page cache and one line in a
`--help`. **There is no scenario in which sharing a file with the file index is the better answer**,
because the file index has a destructive maintenance command bolted to it and battery history does
not deserve to inherit that.

The argument was also made once in the other direction, and the symmetry is worth recording because
it is the strongest thing in favour of the rejected option. `index.db` already executes its schema
as a single `db.Exec` of `CREATE TABLE IF NOT EXISTS` statements at
`internal/indexer/indexer.go:87-123`, so appending battery tables to that block would have been
non-destructive and would have needed **no migration at all for existing users** -- every statement
is idempotent, and an existing `index.db` would acquire the new tables on its next open. That is a
real advantage and it is the reason the alternative was seriously considered at all.

It does not outweigh a destructive `clean`. Two independent properties are being traded, and they
are not symmetric in value: "no migration for existing users" saves a developer an afternoon on a
greenfield feature, and "months of irreplaceable measurement is one typo from gone" costs a user
their history. The right trade is the one where the reversible error is the one you keep.
### 9.3 The package boundary: its own store, not `internal/indexer`

**Recommendation: the battery store is a file inside `cellwatch/`, with its own `Open`, and
`cellwatch` does not import `github.com/Aswanidev-vs/chest/internal/indexer` at all.**

The alternatives were a `battery` sub-package of `internal/` sharing `indexer.Store`, or
`indexer.Store` itself gaining battery methods. Both are rejected, for three reasons in order of
weight.

**1. It breaks the extraction rule, and the extraction is the point.** §1.3 rule 1 makes
`cellwatch` a leaf: it imports nothing from `github.com/Aswanidev-vs/chest/...`. Importing
`internal/indexer` makes that false, and `TestNoChestImports` (§1.4) fails, and `git subtree split
--prefix=cellwatch` produces a tree that will not build on its own. A store that reaches into
CHEST's indexer is not extractable, which means the deliverable described in §0 item 2 is not
delivered.

**2. It re-couples exactly what §9.2 separated.** The entire reason for a separate file is an
independent lifecycle. Sharing a `*sql.DB` and a `*sql.Tx` across two subsystems means a
long-running battery query can occupy the single connection `chest index` needs, and a schema
change in one is a schema change in the other's file. The two stores are neighbours on the same
filesystem, not components of the same thing. Decoupling them at the package boundary is what makes
the decoupling at the file boundary real rather than nominal.

**3. The only thing they actually share is a blank import.** Sharing a driver is
`import _ "github.com/ncruces/go-sqlite3/driver"` in two files. It does not need a shared `Store`
type, a shared `*sql.DB`, or a shared package. The shared thing is a string.

What CHEST keeps is the **path convention**, not the code. The CHEST-side helper in §16.4 resolves
`~/.chest/battery.db` the same way `internal/cli/repl.go:485-491` and
`internal/history/history.go:31` resolve `~/.chest` -- `os.UserHomeDir()` plus `filepath.Join`,
three lines, living in `internal/cli/`. That is where §1.3 rule 5 says a consumer's path decision
belongs, and keeping it there is what lets the extracted library accept any path at all.

The result is a two-line import graph:

```
  cellwatch  --(blank import)-->  github.com/ncruces/go-sqlite3/driver
  cellwatch  --(nothing)------->  github.com/Aswanidev-vs/chest/...
```
### 9.4 Concurrency: one connection, WAL, and what it actually solves

The connection setup is deliberately a copy of `internal/indexer/indexer.go:77-85`. Each line is
load-bearing and the reason is given, because a reader who does not know why `SetMaxOpenConns(1)`
is set will eventually "fix" it.

```go
// store_sqlite.go

// Open opens the battery store at path. It creates the parent directory if
// it does not exist, exactly as OpenOrCreate does for the file index
// (internal/indexer/indexer.go:69-75).
//
// The three lines after sql.Open are not defaults to be improved on. Each is
// load-bearing and §9.4 says which.
func Open(path string, mode Mode, opts ...Option) (Store, error) {
	o := openOptions{loc: time.Local, retention: DefaultRetention()}
	for _, opt := range opts {
		opt(&o)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("cellwatch: %s: %w", path, err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("cellwatch: open %s: %w", path, err)
	}

	// One connection. Not a performance setting: it is what makes a
	// multi-statement write a single transaction rather than several
	// independent ones, and it is why PutRaw can rely on SQLite's own
	// single-writer guarantee instead of taking a mutex of its own.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {   // §9.5
		db.Close()
		return nil, fmt.Errorf("cellwatch: schema: %w", err)
	}
	// §9.8: runOnce for the one-time data fixes.
	// ...
}
```

**`SetMaxOpenConns(1)`, and why it is a fix rather than a limitation.** `database/sql` opens
connections lazily and up to a limit, and SQLite's concurrency control is a lock held on the
*connection*, not on the `*sql.DB` handle. With more than one connection in the pool, two goroutines
calling `db.Exec` can land on two different connections, SQLite will serialise them, and the loser
gets `SQLITE_BUSY` back from the pool rather than blocking. That failure is intermittent, appears
only under concurrency, and is universally diagnosed as "SQLite is flaky" by everyone who has not
read this paragraph. With one connection, statements serialise in the pool instead, and a
multi-statement write is a genuine transaction.

It also removes a class of bug rather than merely a class of error. `PutRaw` writes a raw row, an
application attribution and a staleness marker. With one connection and an explicit `BEGIN`, those
are one commit: there is no window in which a rollup and the record it is derived from are two
separate commits, and therefore no window in which a concurrent reader sees one without the other.

**WAL, and the constraint it removes.** `PRAGMA journal_mode=WAL` gives **one writer and many
readers, across processes**. This is the answer to the question bbolt could not answer, and it is
worth being concrete about what was given up to get it.

Under bbolt, `chest battery --watch` in one terminal, a cron job, and a service pointed at one
database file were three writers, which is a configuration error the store has to detect and reject
with a timeout. Here they are two different files with two different connection pools, and if they
were the same file the indexer would be reading while the sampler wrote, with no blocking and no
error. **A foreground `chest battery --history` query while the background sampler writes is the
normal case, not the edge case**, and WAL is what makes it unremarkable.

**`busy_timeout=5000`.** Five seconds is long enough that a checkpoint or a competing writer does
not surface as a `database is locked` error, and short enough that a genuinely stuck writer fails
visibly. The same value the indexer uses, at `internal/indexer/indexer.go:85`, for the same reason.

**The shared-pool question, answered directly.** A background sampler and `chest index` do **not**
share a connection pool, because they do not share a file. Within the battery store, a foreground
query and the sampler share one pool of one connection, and they are safe because of WAL: a reader
does not block the writer and a writer does not block a reader. Across processes, `busy_timeout`
absorbs the writer-against-writer case.

There is no configuration in this design under which a reader and a writer deadlock, and **no mutex
anywhere in this package**. That is the single largest improvement over the bbolt design, which
needed a one-writer-per-file rule, a `Mode` type to enforce it, a `WithTimeout` option to bound the
wait, and an `ErrTimeout` in the error table to report it. All four of those disappear, and §14
loses a row.
### 9.5 The schema

One `db.Exec` of `CREATE TABLE IF NOT EXISTS` statements, in the style of
`internal/indexer/indexer.go:87-123`, so that opening a new database and opening an old one are the
same code path and **there is nothing to migrate**. This is the same argument that made the
rejected `index.db` option attractive in §9.2 and it holds equally well in a database of its own.

```sql
CREATE TABLE IF NOT EXISTS battery_raw (
  at             INTEGER PRIMARY KEY,   -- unix nanoseconds, UTC
  flags          INTEGER NOT NULL,     -- bit 0..7, the Known/AC/wattage flags
  ac             INTEGER NOT NULL,     -- 0 unknown, 1 on, 2 off
  wattage_src    INTEGER NOT NULL,     -- WattageSource
  charge_milli   INTEGER NOT NULL,     -- percent x 1000
  watts_milli    INTEGER NOT NULL,     -- watts x 1000
  full_milliwh   INTEGER NOT NULL,     -- Wh x 1000
  design_milliwh INTEGER NOT NULL,     -- Wh x 1000
  drain_milli    INTEGER NOT NULL,     -- percentage points x 1000, signed
  health_deci    INTEGER,              -- percent x 10, NULL when unknown
  cycles         INTEGER               -- cycle count, NULL when unknown
);

CREATE TABLE IF NOT EXISTS battery_rollups (
  tier           INTEGER NOT NULL,     -- Tier: minute..year
  bucket_start   INTEGER NOT NULL,     -- unix nanoseconds, UTC, the period start
  observed_ms    INTEGER NOT NULL,     -- summed from the records, never nominal
  ac_ms          INTEGER NOT NULL,
  battery_ms     INTEGER NOT NULL,
  sessions       INTEGER NOT NULL,
  drain_milli    INTEGER NOT NULL,
  energy_milliwh INTEGER NOT NULL,
  charge_min     INTEGER NOT NULL,
  charge_max     INTEGER NOT NULL,
  first_at       INTEGER NOT NULL,
  last_at        INTEGER NOT NULL,
  imported_n     INTEGER NOT NULL,
  total_n        INTEGER NOT NULL,
  prov_cpu       INTEGER NOT NULL,
  app_count      INTEGER NOT NULL,
  apps           BLOB,                 -- packed per-app slice, NULL when attribution is off
  PRIMARY KEY (tier, bucket_start)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS battery_apps (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL UNIQUE,     -- the folded grouping key, §11.3
  first_seen INTEGER NOT NULL          -- unix nanoseconds, UTC
);

CREATE TABLE IF NOT EXISTS battery_meta (
  key   TEXT PRIMARY KEY,
  value TEXT
);
```

`battery_meta` is named with the prefix rather than simply `meta` even though the file is separate
from the index. It costs four characters and it makes `sqlite3 battery.db .tables` self-describing,
and more practically it means that if anyone ever merges this file into `index.db` -- which §9.2
forbids but does not make impossible -- the two key spaces do not collide silently.

**Raw: the timestamp is the rowid.** `at INTEGER PRIMARY KEY` makes the column both the primary key
and SQLite's internal rowid, so the table has **no secondary index and one B-tree**. A nanosecond
rowid is monotonic for an append-ordered sampler, so new records extend the file rather than
splitting pages, which is the cheapest write pattern a B+tree has. And it is the same property the
bbolt key design had -- writing the same sample twice replaces the row rather than appending a
second one -- arrived at for free.

**Rollups: one table with a `tier` discriminator, not seven tables.** A tier is a value, not a
schema. Adding an eighth granularity is an enum constant and a bucket function rather than a
`CREATE TABLE`; one upsert statement serves every tier (§9.7); the retention sweep is one
`DELETE ... WHERE tier = ?` (§10.6); and the whole ladder is testable by parameterising over a tier
rather than by writing the same test seven times.

The cost is a discriminator column on every row -- one byte -- against six copies of a seventeen-byte
record header, six separate DDL statements to keep in sync, and six opportunities to add a tier to
five of the seven places. The old design's `// ...` inside `Prune` and `rollup`, where the tier set
had to be restated in more than one function, is the shape of bug this prevents.

**`WITHOUT ROWID`, and why.** The primary key *is* the identity of a rollup, so SQLite stores the
row directly in the key B-tree with no rowid column and no separate index. The alternative costs a
rowid plus a complete second B-tree over `(tier, bucket_start)` for every row in the table --
roughly 60 % on top of the data. SQLite's guidance is that `WITHOUT ROWID` suits rows that are not
too wide, and a seventeen-column row of small integers at about 86 bytes is comfortably narrow:
about forty-five rows to a 4096-byte page, so the fixed per-row overhead is amortised across a
real page rather than dominating it.

**The application dimension is a packed column, not a second table.** This is the one genuinely
contested decision, and the reasoning is kept because the instinct is to normalise and the instinct
is wrong here.

A `battery_app_rollups` table is the idiomatic SQL answer, and it is a real loss. Every app row
repeats the seventeen-byte record header to carry a handful of payload bytes, and repeats the
eight-byte `bucket_start` that its parent row already holds. That is about 44 bytes on disk for
roughly 20 bytes of data. With nine app rows per minute over 62 days it is 803,520 rows and
**35 MB**, against **16 MB** for the same data packed into the parent row. §10.7 has the full
arithmetic.

It is affordable **because the tier ladder already bounds the row count a query touches**, and that
is the whole argument. A `TopApps` over a year reads 365 day rows, not 89,280 minute rows and not
803,520 app rows. The dimension that needs indexing is *time*, and time is exactly what the ladder
collapses. `AppHistory` for one application over a year is those same 365 rows, decoded in Go and
filtered. The performance case for a second table never arises once the ladder is in place, and the
storage case is not close.

The cost that is accepted, stated rather than hidden: `AppHistory` at minute resolution across the
full 62-day minute window decodes 89,280 blobs, which is tens of milliseconds. That is a number
worth publishing in the method comment, not a problem worth a second table and 19 MB.

**What is indexed, and what is not.** `battery_raw` needs none -- the rowid is the timestamp.
`battery_rollups` needs none -- the primary key is the range every query uses. `battery_apps` has
one, on `name`, for the lookup in `AppHistory`. `battery_meta` has one, on `key`, because that is
what `PRIMARY KEY` means for a text column. The entire schema is four tables, one implicit index
and one explicit one.

Every query in §2.10 is therefore either a primary-key range scan or a full scan of a table that is
at most 365 rows at the tier a whole-year query resolves to. That is a property of the schema and
the ladder together, and it is what `TestQueryChoosesCoarsestFullCover` in §15.1 asserts by
counting rows rather than by timing.

**Scaling factors, and why they are integers.** Every stored measurement is a scaled integer.

| Column | Scale | Why this scale and not the next one |
|---|---|---|
| `charge_milli`, `drain_milli` | x 1000 | A hundredth of a percent is far finer than any platform reports, and costs three bytes. |
| `watts_milli` | x 1000 | A milliwatt is finer than every source, including the RAPL counter at §3.4. |
| `full_milliwh`, `design_milliwh`, `energy_milliwh` | x 1000 | Watt-hours to a tenth, which is finer than the 1 % resolution of `batteryreport` or SRUM. |
| `health_deci` | x 10 | Health is a slowly-moving integer percentage. A tenth is generous. |
| `cycles` | -- | Plain `INTEGER`. The bbolt codec used `int16` deliberately, as a guard so a corrupt value could not poison an aggregate. In SQL that guard belongs in a `CHECK` constraint, not in a width, and at 32,767 cycles the pack has failed. |
| `observed_ms`, `ac_ms`, `battery_ms` | -- | Milliseconds, because a minute bucket on a DST day is 23 or 25 hours and the sum has to be exact (§10.3). |
| `flags`, `ac`, `wattage_src`, `tier` | -- | Small enumerations, stored as the Go constant. |

A `REAL` column was rejected for all of it. SQLite stores a `REAL` as an eight-byte IEEE double,
which makes "the last observed charge" a floating-point comparison, and a charge reading that is
`0.1 + 0.2` rather than `0.3` is a support ticket about a battery.

**A record written by a newer cellwatch.** There is no codec version byte here, and that is a real
departure from the bbolt design, which had `codecVersion = 1` as the first byte of every value. The
engine's own mechanism replaces it: the store sets `PRAGMA user_version` on open and checks it
against the version it was built to read, returning `ErrSchemaVersion` rather than reading a schema
it does not recognise. A misparsed time series is worse than an unreadable one, so the refusal is
the point, and this is the one place where the move from a byte format to a named schema is a
genuine loss: adding a column no longer needs a version bump, and that is worth more than the guard
it replaces.
### 9.6 The write path: one transaction, and idempotence as a property of the key

The whole write is one transaction, and idempotence comes from the primary key rather than from a
check:

```go
// store_sqlite.go

// PutRaw writes one raw sample.
//
// Idempotence is structural. at is the rowid, so writing the same sample
// twice replaces the row instead of appending a second one. There is no
// "have I already stored this" test, and therefore no window in which such a
// test and the write disagree -- which is the failure mode the bbolt design
// avoided by the same property arriving a different way.
func (s *sqlStore) PutRaw(ctx context.Context, sample Sample) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO battery_raw
				(at, flags, ac, wattage_src, charge_milli, watts_milli,
				 full_milliwh, design_milliwh, drain_milli, health_deci, cycles)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			sample.At.UTC().UnixNano(),
			sampleFlags(sample.Status),
			uint8(sample.Status.AC),
			uint8(sample.Status.Wattage),
			milli(sample.Status.ChargePct),
			milli(sample.Status.Watts),
			milli(sample.Status.FullWh),
			milli(sample.Status.DesignWh),
			milli(sample.drainPct),
			nullableDeci(sample.Status.HealthPct, sample.Status.HealthKnown),
			nullableCycles(sample.Status.CycleCount, sample.Status.HealthKnown))
		if err != nil {
			return err
		}
		return markRollupStale(ctx, tx, sample.At)
	})
}

// write runs fn inside one transaction, with the rollback every caller would
// otherwise forget. The single connection of §9.4 is what makes BEGIN and
// COMMIT a real transaction boundary rather than two independent statements
// that happen to be adjacent.
func (s *sqlStore) write(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
```

`INSERT OR REPLACE` rather than `INSERT` and rather than `UPDATE`, for one sentence: `REPLACE` is a
delete and an insert in a single statement, so it is the only one of the three that is correct
whether or not the row exists, and the only one that cannot leave a stale row in place. A
`samples` table that silently accumulated duplicates under a re-run import is a store that reports
confident nonsense, which is the failure mode this whole design is arranged to avoid.

`MarkRollupStale` is the same idea the bbolt `meta` key carried, as one row in `battery_meta`, and
§10.5 covers the semantics. §10.4 covers the rollup upsert, which is the same shape against the
composite primary key.
### 9.7 `chest clean`, and the `--battery` flag

**The primary win, stated first because it is the reason for the separate file: `chest clean`
cannot destroy battery history.** `newCleanCmd` (`internal/cli/intel.go:107`) knows about
`index.db` and `history.json`. It does not know about `battery.db`, so neither `chest clean` nor
`chest clean --all` touches it. The regression that sharing `index.db` would have introduced does
not exist, and nothing has to be done to prevent it.

**The decision.** `chest clean` gains an opt-in `--battery` flag. The default is that battery
history is **never** deleted. Destroying months of recorded history has to be something the user
asks for by name, never a side effect of tidying a rebuildable file cache.

```go
// internal/cli/intel.go, inside newCleanCmd

var (
	purgeAll     bool
	clearHistory bool
	purgeBattery bool
	yes          bool
)

// ...in the confirmation branch, alongside the existing `what` construction
// at intel.go:126-129:

what := "This will permanently delete ~/.chest/index.db"
if clearHistory {
	what += " and ~/.chest/history.json"
}
if purgeBattery {
	// A different kind of risk from the other two, and the words should
	// say so. index.db is a cache the next `chest index` rebuilds from
	// the filesystem. battery.db is the only copy of measurements that
	// exist nowhere else, and deleting it is not a cache eviction.
	what += " and ~/.chest/battery.db (recorded battery history; " +
		"permanent and not recoverable)"
}

// ...after the index branch, alongside the clearHistory block at
// intel.go:167-176:

if purgeBattery {
	// os.IsNotExist is success, not an error: a user who has never used
	// the battery command must still be able to run `chest clean --battery`
	// and have it work. Requiring the feature to have been used before it
	// can be cleaned up is a small stupid gate.
	if err := os.Remove(batteryDBPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed removing battery db: %w", err)
	}
	fmt.Println("... Removed ~/.chest/battery.db battery history.")
}

cmd.Flags().BoolVar(&purgeBattery, "battery", false,
	"Also delete the battery history database (~/.chest/battery.db). Permanent; "+
		"this data is recorded nowhere else")
```

Five points, each of which is a decision rather than an implementation detail.

**1. The flag, in the style of the flags already in that function.** `BoolVar`, no shorthand. The
existing flags in `newCleanCmd` are `--all`, `--history` and `-y/--yes`; only `-y` has a letter, and
it is a letter the user would already guess. Inventing a new letter for a destructive flag is
exactly the sort of thing that gets typed by muscle memory without being read. `--battery` reads as
a sibling of `--history` in `--help` and no letter is warranted.

It also satisfies §16.3's no-prefix-pair rule: `battery`, `all` and `history` share no prefix, so
`detectFlagTypos` cannot produce a wrong suggestion for any of them.

**2. The confirmation prompt must name what is about to be destroyed, in different words for each
kind.** The existing prompt at `intel.go:126-131` builds a `what` string naming the paths. With
`--battery` set it must additionally name `~/.chest/battery.db` **and say that recorded battery
history is permanent and not recoverable**. Deleting a rebuildable file cache and deleting months of
measurements are different risks, and a prompt that lists them in the same register is a prompt
that has stopped informing anyone. The two clauses read:

```
  This will permanently delete ~/.chest/index.db and ~/.chest/battery.db
  (recorded battery history; permanent and not recoverable).
```

**3. `-y/--yes` bypasses the prompt, and that is a real footgun which must be stated rather than
silently inherited.** `chest clean --battery -y` deletes battery history with no prompt at all. That
is the existing contract for `--all` and `--history` and it is not changed here, because changing it
for one flag and not the others would be a worse inconsistency than the one it fixes. But it belongs
in the flag's help text, in the man page, and in this document: **`-y` with `--battery` deletes
recorded history without any confirmation.** A user who has made `--all` muscle memory has not made
`--battery` muscle memory, because `--battery` is new, and the first time they type it is likely to
be with `-y` already on the line. This is the single most likely way the safety property of this
decision is lost in practice, and it is a documentation problem rather than a code one.

**4. The file need not exist.** If `~/.chest/battery.db` was never created, `chest clean --battery`
succeeds quietly. `os.IsNotExist` is treated as success, exactly as `intel.go:140` already does for
`index.db` (`if err := store.DeleteDB(); err != nil && !os.IsNotExist(err)`). A user should not have
to have used the feature in order to be able to clear it. Note that this is also what makes the flag
safe to put in a cleanup script: it is idempotent.

**5. The combination matrix.** `--all` currently means "the whole `index.db` **file**", not "the
whole file index" and not "everything CHEST stores" -- it calls `store.DeleteDB()`
(`intel.go:139-141`), which removes the database file, and it has never touched `history.json`,
which is what `--history` is for.

| Flags | `index.db` | `history.json` | `battery.db` |
|---|---|---|---|
| `chest clean` | cache truncated (`DELETE FROM files; VACUUM;`) | kept | **kept** |
| `chest clean --all` | file deleted | kept | **kept** |
| `chest clean --history` | cache truncated | deleted | **kept** |
| `chest clean --battery` | cache truncated | kept | **deleted** |
| `chest clean --all --history` | file deleted | deleted | **kept** |
| `chest clean --all --battery` | file deleted | kept | **deleted** |
| `chest clean --history --battery` | cache truncated | deleted | **deleted** |
| `chest clean --all --history --battery` | file deleted | deleted | **deleted** |
| any of the above with `-y` | as above | as above | as above, **with no prompt** |

**`--all` does not imply `--battery`, and this is deliberate.** The honest reason: `--all` means
"all of the index database", and a user who reads it as "all of my CHEST state" is reading a word
the flag has never meant, because it has never covered `history.json` either. Extending it to cover
`battery.db` would change the meaning of an existing flag, and -- more importantly -- it would
destroy battery history through a flag the user did not read as destructive. **The rule is that a
flag which can destroy non-derivable data names that data.** `--battery` names it; `--all` does not
and must not.

The ambiguity is real and it is addressed in the help text rather than in the behaviour, because
changing the behaviour is the more dangerous of the two. Two changes to `intel.go` close it:

- `--all`'s help text becomes **"Completely delete the index database file (~/.chest/index.db)"**,
  which is what it already says in the prompt at `intel.go:126` and is more precise than the current
  `"Completely delete the ~/.chest/index.db database file"`. It already says "index.db"; what is
  added is that it does not say "all".
- The success message for `--all` is unchanged, and the `--battery` message is separate, so running
  both produces two lines naming two different files rather than one line claiming everything.

**`chest clean --battery` is not part of `chest index --clear`.** `chest index --clear`
(`intel.go:101`) calls `store.ClearIndex()`, which is `DELETE FROM files; VACUUM;` -- it empties
the file index and touches nothing else in the repository. Battery history is unrelated to it and is
never cleared by that command, and saying so in the `--help` for `--clear` costs one clause.

**6. Backup and inspection, which is the other genuine win of a separate file.** A separate file is
one file to copy:

```bash
cp ~/.chest/battery.db ~/battery-2026.db      # a year of history, one command
sqlite3 ~/.chest/battery.db \
  "SELECT datetime(bucket_start/1000000000,'unixepoch'), drain_milli/1000.0
     FROM battery_rollups WHERE tier=4 ORDER BY bucket_start DESC LIMIT 5"
```

Both work with no cellwatch binary and no library present. The first is a backup strategy anyone can
write; the second is an answer to "what does the database actually say" obtainable with the standard
tool that ships with most systems, which is how a number in a report gets checked rather than
trusted. With the tables inside `index.db` both are true only by accident, and the second requires
the reader to know which tables are which.
### 9.8 Migration markers, and why this decision is reversible

**A one-time data fix, when one is needed, uses the `meta` key pattern already in this repository.**
`migrateRelativePaths` at `internal/indexer/indexer.go:180-182` is the precedent, and it is used
rather than a versioned migration runner because there is one migration and it is a data fix rather
than a schema change:

```go
// One-time upgrade, guarded by a marker row so it runs at most once per
// database. The pattern is migrateRelativePaths
// (internal/indexer/indexer.go:180-182) and it is the right size for what
// this package needs: a versioned migration runner is a thing to get
// wrong, and there is nothing here worth that machinery.
func (s *sqlStore) runOnce(ctx context.Context, key string, fn func() error) error {
	var done string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM battery_meta WHERE key = ?`, key).Scan(&done)
	if err == nil && done == "1" {
		return nil
	}
	if err := fn(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO battery_meta (key, value) VALUES (?, '1')`, key)
	return err
}
```

The keys it is for: `battery_timezone` and `battery_week_start` (§10.2, §10.3), and any future repair
of rows written before a bug was found. **The schema itself needs no marker**, because every
statement is `CREATE TABLE IF NOT EXISTS` and the engine's `PRAGMA user_version` carries the
version. A new `cellwatch` opening a database written by an older one creates whatever tables are
missing, writes its `user_version`, and carries on.

**This decision is reversible, and the reversal is cheap.** Because the driver is a blank import and
the store owns its own `*sql.DB`, moving `~/.chest/battery.db` to `~/.chest/index.db`, to
`~/.local/share/cellwatch/history.db` for an extracted standalone library, or to a path a caller
supplies, is a change to one string and one `Exec` of the same DDL. There is no engine change, no
data conversion, and no dependency change, because the file format is SQLite's and SQLite was
already on the machine for an unrelated reason.

That reversibility is a genuine argument for the decision, and it is worth more than the one-time
cost of a second connection. It is also the practical answer to §8.4's portability condition: when
that condition arrives, the response is not "migrate off SQLite" but "open the same file from a
different path, or dump it with `.dump` and import the text".

---

## 10. Cascading rollups

### 10.1 The ladder

```
  raw  ---60 s--->  minute  ---60---->  hour  --24-->  day  --7-->  week  -->  month  -->  year
   |                 |                  |             |          |         |          |
   |                 |                  |             |          |         |          |
  14 days          62 days           730 days     10 years   10 years  10 years   forever
```

| Tier | Table | Bucket | Retained | Rows after 1 year |
|---|---|---|---|---|
| raw | `battery_raw` | 60 s | 14 days | 20,160 |
| minute | `battery_rollups`, `tier = 1` | 1 min | 62 days | 89,280 |
| hour | `battery_rollups`, `tier = 2` | 1 h | 730 days | 8,760 |
| day | `battery_rollups`, `tier = 3` | 1 day | 10 years | 365 |
| week | `battery_rollups`, `tier = 4` | ISO week | 10 years | 52 |
| month | `battery_rollups`, `tier = 5` | 1 calendar month | 10 years | 12 |
| year | `battery_rollups`, `tier = 6` | 1 calendar year | never pruned | 1 |

The ladder is unchanged by the move to SQL. What changed is the representation: the bbolt design
gave each tier its own bucket of time-prefixed keys, and this gives each tier a value of a
discriminator column in one table (§9.5). The shape of the question a tier answers is identical, and
so is the property that makes the ladder worth having -- **the cost of a query is bounded by the
resolution requested, not by the length of history.** "Energy over the last year" reads 365 day
rows. "Energy over March" reads 31. "Energy over the last two hours" reads 120 minute rows. In SQL
that property is now also backed by the query planner rather than by a cursor, and a whole-year
query is an ordinary primary-key range scan: `WHERE tier = 3 AND bucket_start >= ? AND bucket_start
< ?`, which is the primary key in order and therefore a contiguous B-tree walk.

Two retention numbers are arbitrary and are documented as such. **62 days** is two calendar months
rounded to a round number of days; the number that matters is that it exceeds two months, so that
"the last two months at one-minute resolution" is always answerable. **730 days** of hours is two
years, chosen because two years of hourly records is 17,520 records -- small enough that the
question "what did this week look like an hour at a time, last year" never needs raw data. Everything
from the day tier up is cheap enough to keep for a decade, which is why 10 years appears three times
and the year tier never expires.

### 10.2 Bucket boundaries

**The raw tier has no boundary question.** `battery_raw.at` is the sample's own UTC instant, and it
is the rowid. This is the foundation of everything below.

**Minutes, hours and days are UTC.** A minute is 60 s, an hour is 3600 s, and a *UTC* day is
86400 s. Their `bucket_start` values are derived from the UTC instant and are exact. The reason is
§10.3.

**Weeks are ISO-8601 and begin on Monday.** The choice is stated once here and the justification is
three sentences long:

> A week boundary has to be computed by something, and the only week definition in the Go standard
> library is ISO-8601 -- `time.Time.ISOWeek()`, which returns a year and a week number and is
> defined to start on Monday. Writing bespoke week arithmetic for a library that will be read by
> people who did not write it is how a year acquires two different week 1s. So: Monday, ISO
> numbering, `time.ISOWeek()`, and the row records the **ISO week-year**, not the calendar year.

That last clause is a real trap, and it is the one that bites:

```
  2026-12-28  is a Monday.  ISO week 53 of ISO year 2026.
  2026-12-31  is a Thursday. ISO week 53 of ISO year 2026.
  2027-01-01  is a Friday.  ISO week  1 of ISO year 2027.   <- calendar year 2027, but
                                                               ISO week-year 2027, while
  2026-01-01  is a Thursday. ISO week  1 of ISO year 2026.   the calendar year it belongs
                                                               to as a "2026" query would
                                                               suggest

  The week bucket starting 2027-01-01 therefore contains days from calendar
  year 2026. Using the calendar year gives an ordering that does not match
  the ISO year, and "weeks in 2026" silently gains a week and loses another.
```

In the bbolt design this was the week **key**, and the trap was a byte-ordering bug that would
reorder buckets. In SQL it is a slightly different and slightly more dangerous bug, because the
primary key `(tier, bucket_start)` is ordered by `bucket_start` and therefore by the **UTC instant**,
which is always correct -- while the *query* "all weeks in ISO year 2027" has to be expressed as a
range on `bucket_start`, and a naive writer will filter on `strftime('%Y', bucket_start)`, which is
the calendar year and is wrong for the first and last days of every year.

The rule that prevents it is that the ISO week-year is computed once, at write time, and the bucket
start is the Monday instant. A week query is then a range scan on that instant, not a string
operation on it:

```go
// weekBucket returns the tier=4 bucket that t falls in: the start of the
// ISO-8601 week containing t, in UTC.
//
// Weeks begin on Monday and are numbered 1..53 by ISO-8601, so the ISO
// week-year is what identifies a week -- not the calendar year. 2027-01-01
// belongs to the week starting 2026-12-28, and a query for "weeks in 2027"
// must be a range on the bucket start rather than a calendar-year test,
// because the calendar year of the start and of the ISO year disagree on
// the first and last days of every year.
//
// The boundary is computed in the location passed in, because "this week"
// is a local-calendar question. The returned instant is UTC, because every
// bucket_start in the table is UTC and the primary key is ordered by it.
func weekBucket(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	// time.Weekday is Sunday=0. ISO weeks start on Monday, so subtract one
	// before taking the modulo -- using time.Weekday directly is the classic
	// way to get a week that starts on Sunday while the comment says Monday.
	offset := (int(local.Weekday()) + 6) % 7
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	start := midnight.AddDate(0, 0, -offset)
	return start.UTC()
}
```

The `ISOWeek()` return value is no longer needed at write time, because the bucket is identified by
its start instant. It is still what `Period.String()` and the `--period` rendering use, and
`Timezone is a property of the database, recorded once` (§10.3, rule 3) still means the same thing
now that the record is a `battery_meta` row rather than a meta key.

**Months and years are local-calendar, UTC-stamped.** The month bucket names the March 2026 that a
user in their own timezone means, not the UTC one. Its `bucket_start` is derived from local calendar
arithmetic and is then a UTC instant whose extent depends on the zone and the year -- March 2026 is
31 days in every zone, because no zone has a DST transition on the 1st of March, but October
contains one in most of the world and the *length* of that month in hours is not 744 for everybody.
This is why the bucket stores the start instant and not a month number: a start instant is exact, and
the extent is computed from it when it is needed (§10.3, rule 2).

**The start of a period is `ThisPeriod`, and it is a half-open window.** `ThisPeriod` for a week
returns `[Monday 00:00 local, next Monday 00:00 local)`. A day returns `[midnight local, next
midnight local)`. This matters at the boundary: an event at exactly 00:00 on Monday belongs to *this*
week, and `Previous` of this week ends at exactly that instant and therefore does not. In SQL this is
a half-open range on `bucket_start` -- `>= from AND < to` -- and it is why every query in §2.10
uses `<` on the upper bound rather than `<=`.

### 10.3 Daylight saving: the trap, and the four rules

A time-series store that keys on local wall-clock time corrupts itself twice a year. The rules
below are what prevent that, and each of them exists because of a specific failure.

**Rule 1 -- every bucket is derived from the UTC instant. Always. Never from local fields.**

The failure this prevents is the one that would be most damaging and hardest to notice. In autumn,
clocks go back and the wall-clock hour `01:00` **occurs twice**: once at 07:00 UTC and once at
08:00 UTC in Europe/London. A `bucket_start` built from local `H:M:S` collides -- two distinct
instants, one value -- and the second write silently overwrites the first. A day then has 1,439
rows instead of 1,500, coverage reports 95.9 % on a day that was fully observed, and nothing anywhere
says why. Deriving bucket starts from UTC makes the repeated hour two distinct values and the
problem does not exist.

In SQLite this is a `UNIQUE` primary key doing the work rather than a key-formatting convention:
the two instants produce two different `bucket_start` values, so the second write is a different
primary key and there is nothing to collide with. The rule survives the move unchanged in substance
and gains a guarantee -- the database itself now refuses the bad data rather than the design merely
avoiding it.

**Rule 2 -- but the *boundaries* are local, so the record count per bucket is not constant.**

This is the consequence of Rule 1 that people get wrong in the other direction. A local day is:

| Transition | Local day length | Minutes in the day | Raw records at 60 s |
|---|---|---|---|
| Normal | 24 h | 1,440 | 1,440 |
| Spring forward (clocks skip an hour) | 23 h | 1,380 | 1,380 |
| Fall back (clocks repeat an hour) | 25 h | 1,500 | 1,500 |

So **nothing in this package may assume 1,440 minutes per day, 168 per week, or 730 per hour-of-year
when judging completeness.** `Coverage.Expected` is computed from the actual UTC extent of the local
period, and `observed_ms` in `battery_rollups` accumulates the extent the records themselves claim
rather than the nominal one:

```go
// expectedExtent returns the true length of the local period containing t.
// This is the number Coverage.Expected divides by, and computing it any
// other way -- 1440 minutes for a day, 168 for a week -- is a bug that shows
// up twice a year as a day that reports 95.8 or 104.2 percent coverage.
func expectedExtent(t time.Time, p Period, loc *time.Location) time.Duration {
	local := t.In(loc)
	var end time.Time
	switch p {
	case PeriodDay:
		y, m, d := local.Date()
		end = time.Date(y, m, d, 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	case PeriodWeek:
		// time.Weekday is Sunday=0. ISO weeks start on Monday, so subtract
		// one before taking the modulo -- using time.Weekday directly is the
		// classic way to get a week that starts on Sunday while the key
		// format says Monday.
		offset := (int(local.Weekday()) + 6) % 7
		midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		end = midnight.AddDate(0, 0, -offset+7)
	case PeriodMonth:
		end = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
	case PeriodYear:
		end = time.Date(local.Year(), 1, 1, 0, 0, 0, 0, loc).AddDate(1, 0, 0)
	default:
		return 0
	}
	return end.Sub(t)   // only called with t == period start
}
```

**Rule 3 -- the location is a property of the database, recorded once, and echoed on every result.**

`Open` writes the location to a `battery_meta` row (`battery_timezone`) and refuses to reopen with a
different one unless `WithRelocate` is given, using the `runOnce` marker of §9.8. Without that rule,
a laptop that crosses a timezone writes a Monday bucket under one zone and a Tuesday bucket for the
same wall clock under another, and the history becomes a puzzle. With it, the choice is made once,
deliberately, and every returned `Range` carries its `Loc` so a consumer can label a day as "15
March, Europe/London" rather than as a bare timestamp.

**Rule 4 -- `time.Weekday()` is not an ISO weekday, and `AddDate` is not `Add(24*time.Hour)`.**

Both are one-character bugs with a season of latency:

```go
local.AddDate(0, 0, 1)   // correct: next local midnight, 23 h or 25 h away
local.Add(24 * time.Hour) // wrong: 00:00 becomes 01:00 on a spring-forward day,
                          // and the day key is then not even a midnight
```

Every boundary in `rollup.go` uses `AddDate`, and a test asserts that a spring-forward and an
autumn day produce the right extents:

```go
func TestPeriodExtentAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// 2026-03-29 is the UK spring forward; 2026-10-25 is the autumn one.
	for _, tc := range []struct {
		day  string
		want time.Duration
	}{
		{"2026-03-28", 24 * time.Hour},
		{"2026-03-29", 23 * time.Hour},   // clocks skip 01:00
		{"2026-10-24", 24 * time.Hour},
		{"2026-10-25", 25 * time.Hour},   // clocks repeat 01:00
	} {
		d, _ := time.ParseInLocation("2006-01-02", tc.day, loc)
		if got := expectedExtent(d, PeriodDay, loc); got != tc.want {
			t.Errorf("%s: extent %v, want %v", tc.day, got, tc.want)
		}
	}
}

func TestAutumnRepeatedHourDoesNotCollide(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/London")
	// 01:30 occurs twice on 2026-10-25 in London: at 00:30 UTC and 01:30 UTC.
	first := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)
	second := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	if first.Equal(second) {
		t.Fatal("test setup is wrong: the two instants are equal")
	}
	if !sameLocalClock(first, second, loc) {
		t.Fatal("test setup is wrong: the two instants should share a local wall clock")
	}
	// Assert against a real store rather than against a formatting
	// function. The primary key is the guarantee now: if both instants
	// collapse to one bucket_start, the second INSERT OR REPLACE would
	// overwrite the first and the day would report 1,439 rows and 95.9 %
	// coverage with nothing anywhere saying why.
	st := openTempStore(t)
	st.PutRaw(context.Background(), sampleAt(first))
	st.PutRaw(context.Background(), sampleAt(second))
	var n int
	st.db.QueryRow(`SELECT count(*) FROM battery_raw WHERE at >= ? AND at < ?`,
		first.UnixNano(), first.Add(25*time.Hour).UnixNano()).Scan(&n)
	if n != 2 {
		t.Errorf("got %d raw rows for the repeated local hour, want 2; "+
			"bucket boundaries must be derived from UTC", n)
	}
}
```

The `sameLocalClock` check stays in the test even though SQLite would reject the collision anyway,
because a test that only asserts the database refused to do the wrong thing is a test that stops
detecting the day boundary being computed in local time somewhere else. The first assertion says the
inputs really are the hard case; the second says the implementation handles them.

### 10.4 How a raw sample becomes an aggregate: recompute, never accumulate

This is the property that makes the whole storage layer safe, and it is worth stating as an
invariant:

> **A rollup record is a pure function of the records below it. The stored aggregate is replaced by
> a recomputation, never incremented in place.**

```go
// rollup advances one bucket of one tier by reading the tier below it and
// writing the result. It replaces; it does not add to what is already there.
//
// That single decision is what makes all of the following safe, and each of
// them is a case an accumulating design gets wrong:
//
//   - re-importing a native platform log over days the sampler already
//     covered, where double counting would be the default outcome;
//   - repairing rows corrupted by a power cut mid-transaction, by re-running
//     the day;
//   - a consumer that runs Rollup twice out of caution;
//   - two samplers covering overlapping windows after a clock change.
//
// A recompute is also cheap, because the tier below is already coarse: a
// year recomputed from 365 day rows is 365 reads, and a month recomputed
// from 31 of them is 31. Only a full rebuild from raw is O(rows), and even
// that is 525,600 reads for a year, which is about 300 ms.
func (s *sqlStore) rollup(ctx context.Context, tier Tier, start time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT drain_milli, energy_milliwh, charge_min, charge_max,
			       observed_ms, ac_ms, battery_ms, sessions,
			       first_at, last_at, imported_n, total_n, prov_cpu, apps
			  FROM battery_rollups
			 WHERE tier = ? AND bucket_start >= ? AND bucket_start < ?`,
			tier.below().code, start.UnixNano(), nextPeriod(start, tier, s.opts.loc).UnixNano())
		if err != nil {
			return err
		}
		defer rows.Close()

		acc := newAccumulator()
		for rows.Next() {
			rec, err := scanRollup(rows)
			if err != nil {
				return err
			}
			acc.addRollup(rec)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if acc.empty() {
			// Nothing below means nothing to write. Writing a zero row
			// here would make "we observed nothing" indistinguishable from
			// "we observed nothing happening", and the first is a gap
			// worth showing.
			return nil
		}
		return upsertRollup(ctx, tx, tier, start, acc)
	})
}

// upsertRollup writes one aggregate. INSERT OR REPLACE against
// (tier, bucket_start) is the whole idempotence story: re-running a day
// overwrites that day's row with a recomputation of the same day, and the
// result is the same row rather than the old total plus the delta.
//
// The previous design achieved this by writing a key into a B+tree. The
// mechanism is identical and the code is shorter, which is the usual
// relationship between a keyed store and a relational one.
func upsertRollup(ctx context.Context, tx *sql.Tx, tier Tier, start time.Time, acc *accumulator) error {
	_, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO battery_rollups
			(tier, bucket_start, observed_ms, ac_ms, battery_ms, sessions,
			 drain_milli, energy_milliwh, charge_min, charge_max,
			 first_at, last_at, imported_n, total_n, prov_cpu, app_count, apps)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		tier.code, start.UTC().UnixNano(),
		acc.observedSeconds, acc.acSeconds, acc.batterySeconds, acc.sessions,
		milli(acc.drainPct), milli(acc.energyWh),
		milli(acc.chargeMin), milli(acc.chargeMax),
		acc.firstAt.UnixNano(), acc.lastAt.UnixNano(),
		acc.importedN, acc.totalN, boolToInt(acc.provcpu),
		acc.appCount, acc.encodeApps())
	return err
}
```

The accumulator is where the aggregation rules live, and they are simple because everything is a
sum and a max:

```go
// accumulator folds a set of rows from the tier below into one. It carries
// more state than a handful of sums, because the useful aggregate of a time
// series is not only "how much" but also "how much of the window did we
// actually see".
type accumulator struct {
	// Summed: derived from measurements.
	drainPct   float64 // percentage points, signed
	energyWh   float64
	energyKn   bool // OR of the per-record EnergyKnown flags
	chargeMin  float64
	chargeMax  float64
	firstAt    time.Time
	lastAt     time.Time

	// Observed, not summed: these are facts about the window.
	observedSeconds float64
	acSeconds       float64
	batterySeconds  float64
	sessions        int
	apps            map[uint32]*appAcc

	// Provenance is mixed, and the fraction is carried rather than collapsed.
	observed   Provenance
	importedN  int
	totalN     int
	provcpu    bool
}
```

`addRollup` sums the measured values, extends the observed extent by the *row's* interval rather
than by a nominal one (which is another way DST stays honest -- a 25-hour day accumulates 25 hours
because the rows say so), and ORs the `Known` flags rather than averaging them, so a bucket is
`EnergyKnown` if any row in it was, and the `ImportedFraction` is `importedN / totalN`.

The accumulator's shape is unchanged by the move to SQL, and that is the point worth making: the
aggregation rules are pure arithmetic over plain structs, they were unit-testable before any storage
existed, and they still are. What changed is only the function that gets a row into the accumulator
and the function that writes the result out.

`Provenance` for the bucket is decided by that fraction:

```
  importedN == 0                 -> ProvMeasured   (or ProvCPUMeasured if RAPL contributed)
  importedN == totalN            -> ProvImported
  otherwise                      -> ProvDerived    (mixed: measured and imported evidence)
  DrainPct came from a window    -> ProvDerived
  DrainPct is apportioned        -> ProvEstimated
```

The mixed case being `ProvDerived` rather than a fourth value is deliberate. It is not a fourth
thing, it is a mixture of the first two, and `Point.ImportedFraction` carries exactly how much of a
mixture, so a consumer that wants to split it can.

### 10.5 Staleness: a late sample invalidates an aggregate, and says so

The recompute rule makes rollups *correct*, but not *current*. A raw sample that arrives after its
minute has been rolled up leaves the minute record behind, and a query served from the minute tier
would silently return the older number.

The fix costs one comparison in a transaction that is happening anyway, and one extra field in the
JSON:

```go
// markRollupStale records the earliest instant at which a raw row exists
// that the rollups have not yet seen. PutRaw calls it on every write and
// Rollup clears it.
//
// It is done at write time rather than at read time on purpose. Doing it at
// read time would mean querying the raw tier on every question to find the
// newest row, and the entire purpose of the tier ladder is that a query does
// not touch the raw tier.
func markRollupStale(ctx context.Context, tx *sql.Tx, at time.Time) error {
	// One row, compared and written rather than read-modify-written by the
	// caller. The condition is in the statement so the monotonicity is
	// enforced by the database, and so is TestMarkRollupStaleIsMonotonic's
	// subject: the marker can only ever move earlier.
	_, err := tx.ExecContext(ctx, `
		INSERT INTO battery_meta (key, value) VALUES ('rollup_stale_from', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
		 WHERE CAST(battery_meta.value AS INTEGER) > excluded.value`,
		strconv.FormatInt(at.UnixNano(), 10))
	return err
}
```

`Query` reads that one row and sets `Point.Stale` on any point whose bucket ends after it. The
result is a `Series` that is *correct as of a known point in the past* and says which points are
behind. It is a much better failure mode than a quietly stale number, and it is one primary-key
lookup per query rather than a scan.

`Rollup` is what clears it, and it is called on a timer by the sampler (every hour by default, which
covers the previous hour completely) and on exit.

### 10.6 Retention and pruning

`Prune` is not automatic. A store grows until something calls `Prune`, and the sampler's `Run` calls
it daily. The reason is that a retention policy that fires on a schedule is a retention policy that
eats a user's history on a day they did not expect it, and a caller who never prunes has a large
file rather than a wrong one.

```go
// Prune deletes every row older than its tier's retention window and
// reports what it removed, per tier.
//
// A tier's cut is a timestamp, and the delete is a range on the primary
// key: for tier 4 that is WHERE tier = 4 AND bucket_start < ?, which the
// WITHOUT ROWID primary key (tier, bucket_start) satisfies as a contiguous
// B-tree range rather than a scan. There is no key to format and no cursor
// to drive, and the cost is proportional to what is deleted rather than to
// the size of the table.
func (s *sqlStore) Prune(ctx context.Context, now time.Time) (Pruned, error) {
	var out Pruned
	out.At = now
	for _, t := range []Tier{TierMinute, TierHour, TierDay, TierWeek, TierMonth, TierYear} {
		keep := s.opts.retention.forTier(t)
		if keep <= 0 {
			continue // year rows: no default expiry
		}
		cut := startOfPeriod(now.Add(-keep), t, s.opts.loc).UnixNano()
		n, err := s.deleteFrom(ctx, "battery_rollups", t, cut)
		if err != nil {
			return out, err
		}
		t.setCount(&out, n)
	}
	// battery_raw is pruned on its own rowid range, which is the same
	// shape and needs no tier at all.
	//
	// The application registry is pruned too: an application whose every
	// row is gone is removed from battery_apps, because otherwise the
	// registry becomes a permanent record of every program the user has
	// ever run and TopApps pays for applications that stopped in 2019.
	// ...
	return out, nil
}

// deleteFrom removes every row of one tier whose bucket starts at or before
// cut, and returns the count.
//
// changes() is requested rather than inferred. A delete that frees whole
// pages is a different event from one that fragments them, and knowing
// which happened is what lets the caller decide whether to suggest a
// VACUUM. It costs nothing when it is not wanted.
func (s *sqlStore) deleteFrom(ctx context.Context, table string, tier Tier, cut int64) (int, error) {
	var total int
	err := s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE tier = ? AND bucket_start < ?`, tier.code, cut)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		total = int(n)
		return nil
	})
	return total, err
}
```

Three details that are easy to get wrong and are in the code above:

- **The cut is a timestamp on the key column, not a comparison against a decoded value.** The
  bbolt version had to format a cut *key* and seek to it; the SQL version passes an integer and lets
  the primary key do the rest. The property that mattered -- the cost is set by what is deleted --
  is now a property of the index rather than of a cursor.
- **An application with no remaining rows is removed from `battery_apps`.** Otherwise the registry
  is a permanent record of every program the user has ever run, and `TopApps` iterates all of them.
- **`Prune` is never automatic.** A store grows until something calls it, and the sampler's `Run`
  calls it daily. A retention policy that fires on a schedule is a retention policy that eats a
  user's history on a day they did not expect it, and a caller who never prunes has a large file
  rather than a wrong one.

### 10.7 What one year costs

Re-derived for SQLite, because the row overhead is not the same arithmetic as a B+tree's and
quoting the old number would be quoting a cost model for a store this design no longer uses. Every
assumption is stated so it can be checked rather than trusted.

**The SQLite row cost model**, with `page_size` at its 4096-byte default:

- **Record header: `1 + N` bytes** for a table of `N` columns, where the first byte is the varint
  header length and each column carries a one-byte serial type. Every column in §9.5 has a serial
  type below 128, so none of them needs a multi-byte type.
- **Cell pointer: 2 bytes** per row, in the page's pointer array.
- **Page fill: about 95 %.** Append-ordered and sequentially keyed rows fill pages nearly to the
  point; the rightmost leaf of each B-tree level is partial, and interior pages cost a few percent
  more. The divisor 1/0.95 is applied to the payload-plus-pointer figure.
- **The `at` column of `battery_raw` is the rowid**, so it occupies one header byte and no body
  bytes at all. That is the single largest reason the raw tier is cheap, and it is a consequence of
  `INTEGER PRIMARY KEY` rather than of a design decision anyone made.

**Row sizes, derived rather than assumed:**

| Table | Columns | Header | Body | Packed apps | Row |
|---|---:|---:|---:|---:|---:|
| `battery_raw` | 11 | 12 B | 25 B | -- | **41 B** |
| `battery_rollups`, no attribution | 17 | 18 B | 60 B | 0 | **86 B** |
| `battery_rollups`, 9 app entries | 17 | 18 B | 60 B | 90 B | **181 B** |

The packed app slice is ten bytes per entry -- a uvarint app id, a varint share in parts per
million, a varint CPU-seconds-in-milliseconds, and a process count -- so nine entries plus the
unattributed bucket is 90 bytes, which is most of the difference between 86 and 181.

**The year, with the ladder's retention windows from §10.1:**

| Tier | Rows | B/row | Attribution on | Attribution off |
|---|---:|---:|---:|---:|
| `battery_raw`, 14 days | 20,160 | 41 | **0.83 MB** | 0.83 MB |
| minute, 62 days | 89,280 | 181 / 86 | **16.2 MB** | 7.7 MB |
| hour, 730 days | 17,520 | 86 | **1.51 MB** | 1.51 MB |
| day, 10 years | 3,650 | 86 | 0.31 MB | 0.31 MB |
| week, month, year | 65 | 86 | 0.006 MB | 0.006 MB |
| `battery_apps`, 30 entries | 30 | ~60 | 0.002 MB | 0.002 MB |
| **total** | | | **≈ 18.9 MB** | **≈ 10.4 MB** |

Plus, alongside the database file: a `-wal` sidecar that grows to at most `wal_autocheckpoint` times
`page_size` -- 1000 pages, about **4 MB** -- while the database is open and is truncated on a clean
close, and a 32 kB `-shm` file. Neither is steady state and neither is a backup target; a `cp` of a
live database should copy the `-wal` file too, which is what §9.7's backup example should say more
precisely than it does.

**So: about 19 MB for a year with per-application attribution on, or about 10 MB with it off, which
is the default.** Ten years adds 3,650 day rows' worth of growth and nothing else, so the curve
flattens hard after year one -- the year-over-year increment is dominated by the minute tier, which
stops growing at 62 days.

**The comparison with the bbolt figures, and the two surprises in it.** The old estimate was 23 MB
with attribution at a 0.9 fill factor, 42 MB at bbolt's default, 4 MB without attribution. Two of
those comparisons do not go the way the model would predict, and both are worth stating:

- **SQLite is smaller where the record is wide.** 19 MB against 23 MB with attribution, and the
  reason is specific: the bbolt design spent **17 bytes of ASCII key per record** on a date and time
  in `YYYYMMDD/HHMMSS` form, while the SQL design spends **8 bytes of integer primary key** that
  serves as both identity and range key. A 17-byte formatted string is not free, and across 89,280
  minute rows it is 1.5 MB that the relational layout does not spend.
- **SQLite is larger where the record is narrow.** 10 MB against 4 MB without attribution, and this
  is the honest cost of a seventeen-column table: every row carries an eighteen-byte header that a
  forty-byte packed binary record did not, and a minute row with no application data is exactly the
  case where that overhead is not amortised by anything. The default configuration -- attribution
  off -- is the one where the relational layout is the worse of the two, and it is still under a
  quarter of what the old default-fill estimate predicted.

**The normalised alternative, for the record.** A `battery_app_rollups` table instead of the packed
`apps` column would be 803,520 rows for nine app entries per minute over 62 days, at about 44 bytes
each -- **35 MB** against 16 MB, and 2.2x for the same data. §9.5 gives the reasoning for not doing
it; the number is here so that the decision can be re-examined without re-deriving it.

**These are calculations, not measurements.** Phase 2 measures the real number by generating a year
of synthetic rollups and reading the file size, and the first honest statement about this section in
a post-implementation document should be the measurement rather than this table. What the table does
establish is the order of magnitude, and that it is not tens of gigabytes -- which is the only
figure the design actually depends on.

---

## 11. Per-app attribution

Carried over from the previous document. The premise, the formula, the folding table, the PID-reuse
guard and the failure-mode table are unchanged, because they were correct and because they are the
part of that document most likely to be forgotten by anyone who reads only the new one.

### 11.1 The honest premise

No mainstream operating system exposes per-application battery consumption.

- **Windows** has no such API. It has SRUM, which records a figure the OS itself computes
  (`ProvAppEnergyReported`, elevation-gated, undocumented, sampled every 30-60 minutes) and
  `powermetrics`, which reports per-process *CPU* power under `sudo` and takes minutes to produce a
  usable sample. Neither is a whole-machine per-app figure.
- **macOS** has `powermetrics`, with the same shape: per-process CPU power, `sudo`, minutes.
- **Linux** has nothing. `powertop` estimates from `power_supply` differences plus per-process CPU
  time, and its own documentation calls the per-process attribution a heuristic.

What cellwatch can do, honestly, is: **measure how much CPU time and I/O each application consumed,
and apportion the drain the battery actually reported in proportion to that activity.** The result
answers *"given that the machine lost 9.4 %/h, roughly how much of that correlates with what Chrome
was doing"*. It does not answer "how much power Chrome consumed". Every figure carries
`Provenance: ProvEstimated` and the field name in JSON ends `_estimate`, so the distinction is
structural rather than a matter of a footnote somebody might skip.

### 11.2 Sampling

Per tick, inside the sampler:

```go
procs, err := process.Processes()
for _, p := range procs {
	name, nErr := p.Name()
	if nErr != nil {
		continue // exited between the list and the query
	}
	t, tErr := p.Times()
	if tErr != nil {
		lost++
		continue // do NOT carry a negative delta into the aggregation
	}
	ioStat, ioErr := p.IOCounters()  // tolerated failure, see 11.6
	created, cErr := p.CreateTime()  // PID-reuse guard, 9.5
	// ...
}
```

Two design constraints on the loop:

- **The process sampler is decoupled from the store.** It runs in its own goroutine and publishes on
  a channel, so a slow enumeration never delays a store write. If a sample takes longer than the
  interval the tick is **skipped, never queued**: a queued backlog would make the reported window
  longer than the window the numbers claim to cover, which is the class of dishonesty this design
  forbids everywhere else.
- **Cost.** On Linux this is `/proc` reads, single-digit milliseconds for 300 processes; on Windows
  gopsutil uses `NtQuerySystemInformation`; on Darwin, `proc_listpids` via `purego`. At the 60 s
  sampling interval it is not a measurable tax. Measured in Phase 2 before the dependency is kept.

### 11.3 Grouping

The key is the executable, folded so that multi-process applications land on one row.

1. `key = strings.ToLower(name)`, then strip a trailing `.exe`.
2. Apply the folding table, first match wins on prefix, then on suffix:

   | Key | Matches |
   |---|---|
   | `chrome` | `google chrome*`, `chromium*`, `*helper (renderer)`, `*helper (gpu)` |
   | `edge` | `msedge*`, `microsoftedge*` |
   | `firefox` | `firefox*`, `web content` |
   | `code` | `code*`, `code helper (renderer)`, `extension host` |
   | `slack` | `slack*`, `slack helper (renderer)` |
   | `terminal` | `iterm2*`, `terminal`, `alacritty`, `kitty`, `wezterm*` |
   | `spotify` | `spotify*` |
   | `python` | `python3*` |

3. **Anything matching `*helper*` that is not in the table is not guessed at.** It goes into
   `unattributed` with a note. A renderer we cannot name is a guess, and a guess in a per-app table
   is a lie with a percentage attached.
4. System processes are not hidden. `systemd`, `kworker*`, `ksoftirqd`, `WindowServer` and
   `csrss.exe` appear as their own rows when they are heavy enough to place. On Linux they are
   among the most informative rows there are, for exactly the reason in 11.6.

The table is versioned with the code, unit-tested, and intentionally small. A user-supplied mapping
file is **out of scope** -- it is a configuration feature, and the consumer's config layer does not
exist yet. The library exposes one hook, which is a function and not a file:

```go
// RegisterFolder, if non-nil, is applied to an executable name after the
// built-in table has declined to match it. It is a hook rather than a
// configuration file on purpose: a library that reads a config file has an
// opinion about where files live, and this one does not (see 2.2, rule 5).
//
// It is not concurrency-safe and is meant to be set from init or from main
// before any sampler starts.
var RegisterFolder func(executable string) (key string, ok bool)
```

### 11.4 The formula

Per PID `p`, over the sampler's window `[0, n]`:

```
cpuSeconds(p) = p.Times[n].Total - p.Times[0].Total        (seconds)
readBytes(p)  = p.IO[n].ReadBytes   - p.IO[0].ReadBytes
writeBytes(p) = p.IO[n].WriteBytes  - p.IO[0].WriteBytes

cpuNorm(p) = min(1, cpuSeconds(p) / (nCPU x dT))           (fraction of machine capacity)
ioNorm(p)  = min(1, (readBytes(p) + writeBytes(p)) / ioBudgetPerSec / dT)
weight(p)  = 0.8 x cpuNorm(p) + 0.2 x ioNorm(p)

share(app)      = SUM weight(p) for p in app  /  SUM weight(p) for all p
unattributed    = 1 - SUM share(app)         (kernel threads, idle, vanished, unnamed helpers)
drainPctHr(app) = share(app) x systemDrainPctPerHour
energyWh(app)   = share(app) x systemEnergyWh        (zero wherever EnergyKnown is false)
```

Constants, all named, all in one place, all documented as guesses:

| Constant | Value | What it actually controls |
|---|---|---|
| `attribCPUWeight` | `0.8` | balance between the CPU term and the I/O term |
| `attribIOWeight` | `0.2` | as above |
| `attribIOBudget` | `50 MB/s` | the I/O term's saturation point |
| `attribIdleFloor` | `0.02` | total activity below which apportionment is noise |

`attribIOBudget` only sets the scale at which the I/O term saturates. It does not affect the ranking
among CPU-bound applications, which is the ranking people actually care about. Saying out loud that
this constant is arbitrary is more useful than pretending it was fitted, because nothing in the
operating system lets you fit it.

### 11.5 PID reuse

A PID captured at sample 0 and a PID with the same number at sample `n` may be different processes.
`CreateTime()` is recorded at both ends; a mismatch means the delta belongs to two different
programs and is **dropped entirely**, not clamped. Clamping would invent a number.

### 11.6 Failure modes, surfaced not hidden

| Failure mode | What actually happens | How the design surfaces it |
|---|---|---|
| **Idle drain** | The machine loses 8 %/h with everything at 0.5 % CPU. Share ratios over near-zero activity are pure noise. | Below `attribIdleFloor` total activity, `Idle = true`, all shares are zeroed, and the table is replaced by `system used 0.8% of the machine - too little activity to apportion`. Absolute `cpu_capacity_pct` is still reported, because it is measured. |
| **Kernel and background** | `[kworker/u16:2]`, `systemd-journald`, `ksoftirqd` do real work with no user-facing name. | They are not hidden. They are either their own rows or part of the explicit `unattributed` row, which is always rendered and never zeroed for looking untidy. |
| **Idle app, awake radio** | Wi-Fi keepalive, a lit display, an active video call, Bluetooth audio all cost real watts and near-zero CPU. They will rank at the bottom. | Stated verbatim in the report notes and in any consumer's documentation. **This is the single largest known error in the model and it is not correctable with CPU and I/O data.** It is also the reason the unattributed bucket exists and is never removed. |
| **Suspend/resume** | `energy_now` and `CurrentCapacity` jump backwards across a suspend; the window is meaningless. | A tick gap greater than `3 x interval` discards the window and restarts it, the same rule as §4.4 layer 3. It also creates a `Coverage.Gaps` entry, so a week of data with six hours of sleep reports 6 gaps rather than 360 missing minutes. |
| **Process exits mid-window** | `Times()` errors, or the PID is simply gone at the next tick. | Skipped, never a negative delta. Counted in `ProcessesLost` and folded into `unattributed`. |
| **I/O counters unreadable** | On Linux `/proc/<pid>/io` needs the same uid or root. | Per-process failure is tolerated: that process's I/O term is zero. If more than half the processes failed, `IOIncomplete = true` and a note recommends running as a privileged user. The library never escalates itself. |
| **Zero processes** | `process.Processes()` returns empty (hardened container, `hidepid=2` mount). | The tick is skipped. After three consecutive empty ticks, `Run` returns an error and stops. No division by zero, ever. |
| **The drain is not the apps'** | The battery number is net of the display, the radio, thermal management and the charger. | `Provenance: ProvEstimated` on the value, `_estimate` on the JSON field, and a note on every report. |

### 11.7 Structs

```go
// AppActivity is one grouped application's measured activity over a window
// and the share of the observed system drain apportioned to it.
//
// Everything from Share onwards is an ESTIMATE derived from CPU time and I/O
// weight. No operating system reports per-application battery use, and the
// one figure that comes close -- Windows SRUM's EnergyEstimation -- reaches
// cellwatch through a different type, AppEnergy, for exactly that reason.
type AppActivity struct {
	// ID is the sequential registry id from §9.5, the id column of
	// battery_apps. It is assigned once and is stable for the life of the
	// database, and it is what a packed app entry carries instead of a
	// name.
	ID uint32

	// App is the folded grouping key.
	App string

	// Procs is how many processes matched this key in this window.
	Procs int

	// CPUSeconds, ReadBytes and WriteBytes are measured. They are the
	// evidence, and a consumer that wants to show something defensible can
	// show these instead of the estimate.
	CPUSeconds float64
	ReadBytes  uint64
	WriteBytes uint64

	// CPUCapacity is this application's share of total machine capacity,
	// 0 to 1, and is also measured.
	CPUCapacity float64

	// Weight is the 0-to-1 activity score, Share is the normalised weight
	// across all applications, and both are intermediate quantities of §11.4.
	// Neither is energy and neither is presented as energy.
	Weight float64
	Share  float64
}

// Attribution is one window's worth of measured application activity and the
// apportionment of the observed drain across it.
type Attribution struct {
	// Window is how long the activity was measured over.
	Window time.Duration

	// Processes and ProcessesLost are the sampled and the vanished counts.
	Processes     int
	ProcessesLost int

	// CPUCapacityPct is the whole machine's capacity used, and is reported
	// even when Idle, because it is measured and Idle is a decision.
	CPUCapacityPct float64

	// IOIncomplete is true when more than half the processes failed their
	// I/O counter read.
	IOIncomplete bool

	// Idle is true when total activity was below attribIdleFloor, in which
	// case every share is zero and apportioning would be noise.
	Idle bool

	// Apps is the per-application table, ordered by weight descending,
	// truncated to SamplerConfig.MaxApps with the remainder folded into
	// Unattributed.
	Apps []AppActivity

	// Unattributed is the residual share, 0 to 1, always present and
	// including a full 1 when nothing could be named.
	Unattributed float64

	// Notes carry anything a consumer should show.
	Notes []string
}

// Attribute folds a window of raw process measurements into an Attribution.
// It is a pure function over the two endpoint snapshots, which is what makes
// it testable without a process table and without a clock.
func Attribute(first, last []ProcSample, nCPU int) Attribution

// ProcSample is one process's counters at one instant.
type ProcSample struct {
	PID        int32
	Name       string
	CreateTime int64  // milliseconds since the epoch, for the reuse guard
	CPUSeconds float64 // cumulative user+system
	ReadBytes  uint64
	WriteBytes uint64
}
```

---

## 12. Historical per-app usage

The new user-facing feature: day, week, month and **year** totals, per application, over time.

### 12.1 The API surface

The public entry points were specified in §2.10. What is worth restating here is how a caller asks
for "this week", "this month" and "this year", and what happens to a sparse history.

```go
// The three common questions, and the exact calls that answer them.

loc := time.Local

// "How much did my apps cost this week?"
wk := cellwatch.ThisPeriod(time.Now(), cellwatch.PeriodWeek, loc)
rep, err := cellwatch.Usage(ctx, store, wk)

// "Which five apps cost the most today?"
today := cellwatch.ThisPeriod(time.Now(), cellwatch.PeriodDay, loc)
top, err := cellwatch.TopApps(ctx, store, today, 5)

// "And how does this month compare with last month?"
thisMonth := cellwatch.ThisPeriod(time.Now(), cellwatch.PeriodMonth, loc)
lastMonth := thisMonth.Previous()
delta, err := cellwatch.Compare(ctx, store, thisMonth, lastMonth)

// "Show me Chrome, hour by hour, for the last fortnight."
r := cellwatch.Range{
	Start: time.Now().Add(-14 * 24 * time.Hour),
	End:   time.Now(),
	Loc:   loc,
	Tier:  cellwatch.TierAuto,
}
hist, err := cellwatch.AppHistory(ctx, store, "chrome", r)
```

Note what is **not** in that list. There is no `PeriodYTD`. A year-to-date query is
`Range{Start: time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc), End: now}` and it is a two-line
composition rather than a `Period` constant, because every additional period is a semantics question
about what a partial period means -- a year-to-date total is not a year, a rolling 365 days is not a
calendar year, and conflating them in one enum is how a report ends up comparing a partial week with
a full one.

`Tier` is optional and defaults to `TierAuto`, which asks the store for the coarsest tier that fully
covers the range. A caller who wants to draw 700 points gets them; a caller who wants the answer
fast gets a month record and does not notice.

### 12.2 What is being totalled, and its honesty status

This is the table that the whole feature rests on, and it is the table a consumer's documentation
should reproduce.

| Quantity | Status | Where it comes from |
|---|---|---|
| **Cumulative charge drained, percentage points** | **MEASURED** | Sum of per-interval charge deltas from the platform counter. Available on all three platforms. The primary unit of historical drain, precisely because it is the only one that is everywhere. |
| **Cumulative energy, watt-hours** | **MEASURED**, on macOS and on Linux with the `energy_*` layout | `power_now x dt` (Linux) or `Voltage x abs(Amperage) x dt` (macOS). |
| **Cumulative energy, watt-hours, on Windows** | available, with a caveat | `GetSystemPowerStatus` reports neither watts nor pack capacity, but the class driver does: `IOCTL_BATTERY_QUERY_STATUS` `.Capacity` in mWh against `.Rate` in mW. A run integrating that rate is measured, not a percentage-point estimate. The caveat is that the figure covers the whole machine rather than the pack alone, since the rate includes AC-side draw while on mains. |
| **Per-application energy, watt-hours** | **ESTIMATE** | `share x systemEnergyWh`, where `share` is the CPU-and-I/O weight of §11.4. Same apportionment as the live view, accumulated. |
| **Per-application energy, Windows with SRUM** | **NEAR-MEASURED, `ProvAppEnergyReported`** | The figure the OS computed, sampled every 30-60 minutes, only for foreground activity. The closest thing to per-app truth available anywhere; still a model output, and absent for short-lived processes. |
| **CPU-package energy, Linux with Intel RAPL** | **MEASURED**, `ProvCPUMeasured` | `energy_uj` delta. Covers the processor packages only. |
| **Applications ranked by CPU time or I/O bytes** | **MEASURED** | `AppTotal.CPUSeconds`, `ReadBytes`, `WriteBytes`. Always available, on every platform, with no estimation anywhere. |

The last row is the escape hatch, and it is worth calling out in any consumer's documentation: if a
user does not trust the estimate, cellwatch can still tell them that Chrome burned 24,811 CPU-seconds
and read 1.9 GB, and that is a measurement. The estimate is offered alongside it, not instead of it.

The consequence for the JSON: every per-application field that is apportioned is named
`*_estimate`, and the `provenance` field says so in words. There is no way to read a cellwatch
payload and mistake an apportionment for a measurement, which is the point.

### 12.3 Comparison across periods

```go
// Compare returns the change between two windows, and refuses to answer when
// either window is too sparse to mean anything.
//
// The refusal is the important half. A week-over-week comparison between a
// full week and four minutes of data produces a large, confident, meaningless
// number, and a UI with a "vs last week" column will render it. Returning an
// error is the only thing that stops that, and the caller is expected to
// render the error -- which is easy, because it is a sentence: "not enough
// history to compare".
//
// Two checks, both ErrInsufficientCoverage with a distinguishing note:
//   1. either window's Coverage.Pct < 25 (Coverage.Usable)
//   2. the two windows have different spans, which is almost always a
//      calendar period compared against a rolling window
func Compare(ctx context.Context, st Store, current, previous Range) (*Delta, error)
```

`Delta.Movers` is the part a person actually reads. Comparing a list of totals for this month
against a list of totals for last month requires the reader to do the subtraction; `Movers` has done
it, biggest absolute change first, in both directions, and marks `Both: false` on an application
that appears in only one window. A move from zero to a large share is a move, and a consumer that
renders it as "no previous data" is being less informative than the data allows.

### 12.4 What one data point in a sparse history means

Consider a Linux machine where the sampler ran for four minutes on 3 March and nothing since. The
question "how much did my apps cost this month?" returns a `UsageReport` whose `Top` has four
entries, all derived from four minutes, and whose `Coverage` says:

```json
"coverage": {
  "observed_seconds": 244,
  "expected_seconds": 2678400,
  "observed_minutes": 4,
  "expected_minutes": 44640,
  "pct": 0.009,
  "first_sample": "2026-03-03T09:12:04Z",
  "last_sample": "2026-03-03T09:16:11Z",
  "gaps": 0,
  "longest_gap": "0s",
  "apps": true,
  "usable": false
}
```

The report is still returned. Refusing entirely would leave the caller unable to distinguish "the
data is missing" from "the query is broken", which is a worse failure. What it must not do is let
four minutes of data render as a month. Four mechanisms enforce that, and all four are in the type
rather than in advice:

1. `Coverage.Usable()` is false, so `Compare` returns `ErrInsufficientCoverage` rather than a
   month-over-month delta.
2. `Coverage.Pct` is 0.009, and the renderer has no choice but to show it if it shows the total at
   all.
3. `AppTotal.Samples` is 4 on every row, so a consumer can see the sample count per application
   without computing it.
4. `UsageReport.Notes` is never empty, and the sparse case produces the specific note
   `"this window holds 4 minutes of samples; totals are not comparable"`.

The honest summary for the consumer's documentation, in one sentence: **a single data point in a
sparse history means almost nothing, and cellwatch's job is to make that visible rather than to
round it off.**

---

## 13. Sample output

### 13.1 A consumer's rendering

The library renders nothing. What a consumer draws from a `UsageReport` is its own business, but the
shape below is what the data supports, and it is written the way `printSpeedTable`
(`speedui.go:36-106`) would write it, with the honesty conventions of §7.6 of the previous design
carried over: every derived row is marked, and a row the platform cannot supply is omitted rather
than printed as zero.

```text
  -- Battery use, this week (2026-03-09 -> 2026-03-15, Europe/London) --
  sampled     82% of the window      87 min missing, longest gap 31 min
  drain       63.0 pts               measured  (charge counter)
  on mains    2 h 12 m
  on battery  5 h 17 m
  per-app     apportioned from measured CPU time and I/O - ESTIMATES

    chrome     [*****      ]  36.1%   22.7 pts est
    Code       [**        ]  14.2%    8.9 pts est
    firefox    [***       ]   9.4%    5.9 pts est
    Slack      [*         ]   4.1%    2.6 pts est
    terminal   [*         ]   2.8%    1.8 pts est
    unattr.    [**        ]  21.4%   13.5 pts est

  Estimates apportion the observed system drain by measured CPU time and
  I/O. No operating system reports true per-app battery use. Wi-Fi, the
  display and audio keep drawing power at near-zero CPU and will always
  rank too low. Unattributed covers kernel threads, idle time, the display,
  the radios, and processes CHEST could not name.
```

Four things in that block are load-bearing:

- **`drain 63.0 pts measured`** -- the primary unit, because the charge counter exists everywhere.
- **`per-app ... ESTIMATES`** in the section header, so the caveat is above the numbers rather than
  below them, where it is read last and forgotten.
- **the `unattr.` row at 21.4 %** -- a fifth of the total, visible, never zeroed.
- **no watt-hours anywhere**, because the machine in this example is Windows and §4.6 forbids
  inventing them. The consumer prints percentage points and says `pts`, not `Wh`, and a
  consumer that can obtain pack capacity prints watt-hours on a second line with a different label.

The same report on a macOS machine with RAPL present on Linux looks like this at the top:

```text
  drain       63.0 pts               measured  (charge counter)
  energy      41.8 Wh                measured  (power_now x dt)
  cpu         19.4 Wh                measured  (intel-rapl energy_uj delta)
  remainder   22.4 Wh                measured  (display, radios, disk, losses)
```

Splitting out the CPU-package figure changes what the per-app table means, and a consumer that has
it should say so: the apportionment is now over a *smaller* denominator, because a measured 19.4 Wh
is known to belong to the processor and the estimated 41.8 Wh total is not. §3.4 explains why the
remainder is worth showing separately.

### 13.2 The JSON payload

`UsageReport` as serialised by a consumer. Pointers with `omitempty` mean *unknown* is absent rather
than a fabricated zero, and every estimate-derived field name ends in `_estimate`:

```json
{
  "cellwatch_version": "0.1.0",
  "range": {
    "start": "2026-03-09T00:00:00Z",
    "end": "2026-03-15T00:00:00Z",
    "loc": "Europe/London",
    "period": "week",
    "requested_tier": "auto",
    "served_tier": "day",
    "note": "Europe/London is UTC+00:00 for this window; the 29 March transition is outside it"
  },
  "system": {
    "drain_pct": 63.0,
    "drain_known": true,
    "energy_wh": 0,
    "energy_known": false,
    "battery_seconds": 190800,
    "ac_seconds": 331200,
    "sessions": 1,
    "imported_seconds": 0,
    "provenance": "measured",
    "note": "this platform reports neither power nor pack capacity, so cumulative energy is unavailable and drain is expressed in percentage points of charge"
  },
  "coverage": {
    "observed_seconds": 497322,
    "expected_seconds": 604800,
    "observed_minutes": 8288,
    "expected_minutes": 10080,
    "pct": 82.2,
    "first_sample": "2026-03-09T00:00:12Z",
    "last_sample": "2026-03-14T23:59:41Z",
    "gaps": 3,
    "longest_gap_seconds": 1874,
    "apps": true,
    "usable": true
  },
  "top_apps": [
    {
      "app": "chrome",
      "procs": 41,
      "cpu_seconds": 24811.4,
      "cpu_capacity_pct": 34.7,
      "read_bytes": 1934200000,
      "write_bytes": 411300000,
      "share_estimate": 0.361,
      "drain_pct_estimate": 22.7,
      "energy_wh_estimate": 0,
      "energy_known": false,
      "samples": 8184,
      "provenance": "estimated"
    },
    {
      "app": "code",
      "procs": 22,
      "cpu_seconds": 9102.7,
      "cpu_capacity_pct": 13.4,
      "read_bytes": 411000000,
      "write_bytes": 88000000,
      "share_estimate": 0.142,
      "drain_pct_estimate": 8.9,
      "energy_wh_estimate": 0,
      "energy_known": false,
      "samples": 8151,
      "provenance": "estimated"
    },
    {
      "app": "firefox",
      "procs": 14,
      "cpu_seconds": 5120.9,
      "cpu_capacity_pct": 5.7,
      "share_estimate": 0.094,
      "drain_pct_estimate": 5.9,
      "energy_wh_estimate": 0,
      "energy_known": false,
      "samples": 7990,
      "provenance": "estimated"
    }
  ],
  "unattributed": {
    "app": "unattributed",
    "share_estimate": 0.214,
    "drain_pct_estimate": 13.5,
    "energy_wh_estimate": 0,
    "energy_known": false,
    "provenance": "estimated",
    "note": "kernel threads, idle time, the display, the Wi-Fi and Bluetooth radios, and processes cellwatch could not name"
  },
  "capabilities": "charge_pct|ac_state|status_word|os_remaining|native_log",
  "caveats": [
    "per-application figures apportion the observed system drain by measured CPU time and I/O weight. No operating system reports true per-application battery use.",
    "applications that keep a radio, the display or a codec busy draw power at near-zero CPU and are systematically under-counted.",
    "87 minutes of the requested week are missing. Comparisons are suppressed below 25% coverage."
  ]
}
```

Field-by-field, the parts that carry weight:

| Field | Why it is there |
|---|---|
| `served_tier: "day"` | A caller can see that a 7-point answer came from 7 day records, not from 8,000 raw samples. It is the cheapest possible explanation of why the query was fast. |
| `energy_known: false` **with** `energy_wh: 0` | Present rather than omitted, so a consumer that wants to print "0 Wh" has to opt into it and can see that the zero is a zero because the figure is unavailable. |
| `provenance: "estimated"` on every app | The word, in the payload, per row. |
| `*_estimate` on every apportioned field | The name, in the field, per row. Two independent signals, because a UI is a different program from the one that produced this. |
| `capabilities` | The string form of the platform's bitmask, so a consumer debugging "why is this empty" does not have to guess. |
| `caveats` | Never empty. A payload with an empty `caveats` array is a bug. |
| `loc` on the range, and the note about the DST transition | A day total is ambiguous without the zone, and the transition note is what stops a consumer from re-deriving the expected extent wrongly. |

The macOS shape differs in exactly two places, and the differences are the interesting part:

```json
"system": {
  "drain_pct": 63.0,
  "drain_known": true,
  "energy_wh": 41.83,
  "energy_known": true,
  "battery_seconds": 190800,
  "ac_seconds": 331200,
  "sessions": 1,
  "provenance": "derived",
  "note": "energy from Voltage x abs(Amperage) x dt; the charge counter is the cross-check"
}
```

`provenance` is `derived` rather than `measured` because the watt-hours are an integration of a
measured instantaneous value over a window, while the percentage points are a straight subtraction of
two measured readings. The distinction is small and it is worth the field.

And on Windows with SRUM open, one row changes and the top of the table changes too:

```json
"capabilities": "charge_pct|ac_state|status_word|os_remaining|native_log|app_energy",
"top_apps": [
  {
    "app": "chrome",
    "share_estimate": 0,
    "share_estimate_known": false,
    "energy_wh_estimate": 0,
    "energy_wh": 9.42,
    "energy_known": true,
    "provenance": "app_energy_reported",
    "note": "SRUM EnergyEstimation, sampled every 30-60 min and only while chrome had foreground activity; short-lived processes leave no record"
  }
]
```

A row with `provenance: app_energy_reported` and no `share_estimate` is a figure the operating
system computed about an application, and it must not be sorted into the same column as a row that
is an apportionment without saying which is which. `Provenance.Estimated()` returns true for both,
deliberately, because both need marking; the value of the field is telling them apart.

---

## 14. Errors and edge cases

| Case | Detection | Behaviour |
|---|---|---|
| **No battery (desktop)** | Windows `BatteryFlag & 128`; Linux no `type == Battery` or `present == 0`; macOS no `AppleSmartBattery` object | A desktop is a correct answer, not an error. `Read` returns `Present: false` with `AC: ACOn` on Windows and Linux, and a plain status on macOS. The consumer prints "running on mains" and exits 0. |
| **Battery full on AC** | `status == "Full"`, `FullyCharged`, or `BatteryFlag & 8` with `pct == 100` | No rate, and specifically **no** "draining at 0.0 %/h" line, which would be a fake measurement of a state that is not happening. Time remaining prints as `-`, not `0:00` and not an infinity. |
| **AC-only, or a UPS** | `ACOn` with `Present == false`, or `Not charging` at 100 % | AC state, no rate, no health. |
| **Read fails, transient** | any `Read` error | One retry after 250 ms, which covers a sysfs read that races a suspend. A second failure marks the sample a `gap`; a window with more than half its samples missing is discarded and restarted. The sampler keeps running. |
| **Read fails, persistent** | the same error on 10 consecutive ticks | `cannot read battery state: <err>`, stop the sampler, return the error. The OS message is shown verbatim, not replaced with a generic string. |
| **Permission denied** | sysfs EACCES, `ioreg` blocked by an MDM profile, SRU directory unreadable | The underlying error, verbatim. For SRUM specifically, `ErrNeedsElevation`, which is distinguishable from a genuine parse failure and which a consumer should render as "run elevated", not as "no data". |
| **Process exits mid-sample** | `Times()` error, or absent at the next tick | Skipped, never a negative delta. Counted in `ProcessesLost`, folded into `unattributed`. |
| **PID reuse** | `CreateTime()` differs between window endpoints | That PID's delta is dropped entirely. Not clamped, not guessed. |
| **Zero processes** | `len(processes) == 0` | Skip the tick. Three consecutive empty ticks and `Run` returns an error naming the likely cause (`/proc` mounted with `hidepid`, or a container). |
| **I/O counters unreadable** | `IOCounters()` error per process | I/O term zero for that process. More than half failing sets `IOIncomplete` and adds a note suggesting elevated privileges. The library never escalates itself. |
| **Suspend/resume** | tick gap `> 3 x interval` | Discard the window and restart it; record a `Coverage.Gap`. |
| **Unsupported platform** | `reader_other.go` returns `ErrUnsupported` | `battery readings are not implemented for <GOOS>` -- the same honest move as `errNoDirectIO` at `directio_other.go`. |
| **Unknown percent** | `BatteryLifePercent == 255`, `capacity` non-numeric, both macOS capacities zero | `ChargeKnown = false`, the charge row is omitted, and a `--fail-under`-style threshold check does not fire because there is no measured charge to compare. |
| **Another process holds the write lock** | `database is locked` after `busy_timeout` | `cellwatch: <path> is locked by another process (another sampler already running?)`. Reported after five seconds by the `busy_timeout` in §9.4, not blocked silently. A reader never hits this: WAL readers do not take the write lock. |
| **Read-only handle mutated** | any write method on a `ModeReader` store | `ErrReadOnly`, from every mutating method. |
| **Record from a newer cellwatch** | codec version byte mismatch | `ErrCodecVersion`. A refusal, not a best-effort parse: a misparsed time series is worse than an unreadable one. |
| **Truncated record** | record shorter than its header or app section | An error. Reading half a record is how a store starts reporting confident nonsense. |
| **Timezone changed on disk** | `Open` finds a `meta.timezone` that differs from the option | `ErrLocationMismatch`, unless `WithRelocate` was given. Changing the zone silently would make the same wall clock produce two different keys. |
| **Native log covers nothing** | `Earliest` is the zero time, or `Read` returns an empty slice | An empty `ImportReport` with a note, not an error and not an empty import. |
| **Native log needs elevation** | `OpenSRUM` cannot open the file | `ErrNeedsElevation`, and `Capabilities()` stays `CapNone` so nothing can claim a capability that was not earned. |
| **Native log disagrees with the sampler** | charge differs by more than 2 points at the same instant | The sampler's record is kept, both are stored, `ImportReport.Conflicts` is incremented, and the disagreement is visible. Not resolved silently in either direction. |
| **`Compare` with a sparse window** | either `Coverage.Pct < 25` | `ErrInsufficientCoverage`, with a note saying which window and how much it had. |
| **Context cancelled mid-query** | any `ctx.Done()` in a cursor walk | The walk returns `ctx.Err()`. A cancelled query returns nothing, rather than a partial series that looks complete. |

---

## 15. Testing strategy

### 15.1 What is unit-testable with no hardware

Everything in §4, §9.5, §9.6, §10 and §11 is a pure function over plain structs or byte slices. That
is the point of the layout in §2.1, and it is why the tests below need no battery.

**Buckets and the schema** -- `bucket_test.go`, `schema_test.go`:

| Test | Assertion |
|---|---|
| `TestBucketStartsAreChronological` | 1,000 pseudo-random instants, sorted by `bucket_start`, come back in the order they were generated |
| `TestRawRowIsUTCDerived` | the autumn repeated local hour produces two distinct rows, verified against a real store (§10.3) |
| `TestWeekBucketIsMonday` | the `bucket_start` of every week row is a Monday, in several zones |
| `TestWeekBucketUsesISOYear` | a query for ISO year 2027 is a range on `bucket_start`, and 2026 has 52 or 53 week rows and not 54; asserting the range excludes a calendar-year filter (§10.2) |
| `TestMonthAndYearBucketsAreLocalCalendar` | the month bucket for 2026-03 contains exactly the UTC instants of the user's March, in a named zone |
| `TestAppIdIsSequentialAndStable` | `battery_apps.id` comes from `AUTOINCREMENT`, never from a hash of the name, and is unchanged for the life of the database |
| `TestSchemaIsIdempotent` | running the `schemaSQL` of §9.5 twice against an open database succeeds and changes nothing; this is the test that stands in for a migration runner |
| `TestSchemaVersionIsChecked` | a database whose `PRAGMA user_version` is newer returns `ErrSchemaVersion` rather than being read |
| `TestNoTableOutsideTheSchema` | the tables present in a fresh database are exactly the four of §9.5, so a stray `CREATE TABLE` in an `Open` path is a test failure |

**Rollups and periods** -- `rollup_test.go`:

| Test | Assertion |
|---|---|
| `TestPeriodExtentAcrossDST` | 23 h and 25 h local days, in a named zone (§10.3) |
| `TestAutumnRepeatedHourDoesNotCollide` | two instants sharing a local wall clock get different keys |
| `TestAddDateNotAddHours` | every boundary uses `AddDate`; a spring-forward day's start is still a midnight |
| `TestRollupIsIdempotent` | rolling a day twice produces byte-identical records |
| `TestRollupRecomputesNotAccumulates` | rolling a day after adding a late raw record gives the corrected total, not the old one plus the delta |
| `TestRollupOfEmptyBucketWritesNothing` | a bucket with no records below it gets no record, so "observed nothing" stays distinguishable from "observed nothing happening" |
| `TestMarkRollupStaleIsMonotonic` | the stale marker only ever moves earlier |
| `TestStaleFlagSurfacesLateSample` | a `Point` after the stale marker comes back with `Stale == true` |
| `TestPruneUsesARangeNotAScan` | `EXPLAIN QUERY PLAN` on the prune delete names the primary key, and a pruned tier has no row before the cut, verified with a direct `count(*)` |
| `TestPruneRemovesEmptyAppsFromRegistry` | an application with no remaining rows is gone from `battery_apps` |

**Rates** -- `rate_test.go`, the §4.4 quantisation regressions:

| Test | Assertion |
|---|---|
| `TestRateWindowRejectsShortSpan` | a 2-sample window is not stable |
| `TestRateSmoothsQuantisation` | a synthetic 1 %-granular series at a known true rate is recovered to within 15 % over 15 samples, and demonstrably **fails** over 2 samples -- the regression this design exists to prevent, asserted in both directions |
| `TestRateRejectsACTransition` | a window containing an AC change is discarded, not bridged |
| `TestRateRestartsAfterGap` | a window spanning a 4x tick gap is discarded |
| `TestRateSignOnCharge` | charging yields a negative rate and no remaining time, not a positive drain |
| `TestEMAConverges` | the EMA reaches the window value within ~8 samples and never exceeds it by more than one quantum |
| `TestRatePowerPathPreferred` | with `WattsKnown` and `FullWh > 0`, the source is the power path and no window is needed |
| `TestRateNoWattsNoFullWh` | missing inputs produce a stable rate with a reason, never `NaN` |

**Attribution** -- `attrib_test.go`:

| Test | Assertion |
|---|---|
| `TestGroupingFoldsHelpers` | `Google Chrome Helper (Renderer)` and `chrome.exe` land in one `chrome` row; `Foo Helper (Renderer)` goes to `unattributed` |
| `TestGroupingStripsExeAndCase` | `MSEDGE.EXE` and `msedge` are one key |
| `TestApportionSumsToOne` | `SUM share + unattributed == 1` within 1e-9 |
| `TestApportionProportional` | doubling one application's CPU doubles its share |
| `TestPIDReuseDropsDelta` | a differing `CreateTime` between window endpoints yields no delta for that PID |
| `TestVanishedProcessNoNegativeDelta` | a PID missing at the second sample contributes 0, not a negative |
| `TestIdleFloorSuppressesShares` | total activity under 0.02 sets `Idle`, zeroes all shares, and still reports `CPUCapacityPct` |
| `TestUnattributedAlwaysPresent` | with an empty PID set, `unattributed == 1` |
| `TestIOIncompleteFlag` | more than 50 % `IOCounters` failures sets `IOIncomplete` |

**App-slice codec** -- `codec_test.go`. This is now a much smaller surface than the old binary
record codec, because SQL holds the scalars and only the packed app slice needs encoding:

| Test | Assertion |
|---|---|
| `TestAppSliceRoundTrip` | 10,000 randomised app slices, including empty, single, and full-8-plus-unattributed, encode and decode identically |
| `TestAppSliceRejectsTruncation` | every prefix of a valid blob returns an error, never a partial `AppActivity` |
| `TestAppSliceIgnoresStructReorder` | the encoded bytes are a function of the field list, not of the Go struct, pinned by a golden byte string |
| `TestScaledIntegersRoundTrip` | every scaling factor of §9.5 survives a round trip at its extremes, including negative `drain_milli` for charging |
| `TestScaledIntegersRejectNaN` | a `NaN` or infinite derived value is refused at the boundary rather than stored, because SQLite would store it and every later `SUM` would poison |

**Capabilities and the leaf rule** -- `capability_test.go`, `leaf_test.go`: the tests in §5.3 and
§1.4, plus `TestCapabilityStringCoversEveryBit`, which fails if a bit is added to the mask without a
name, since that is the failure mode the map in §2.5 was written to prevent.

**The engine boundary** -- `boundary_test.go`. This replaces the old bbolt grep and is the one test
in the file that enforces an architectural rule rather than a behaviour:

```go
// The Store interface is a seam only if the engine stays behind it. If
// database/sql appears anywhere else, every future change to the schema
// becomes a change to the public API, and the interface stops being a
// decision and becomes a description of the implementation.
func TestSQLStaysInTheStoreFiles(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse cellwatch: %v", err)
	}
	for name, pkg := range pkgs {
		for file, astFile := range pkg.Files {
			switch file {
			case "store_sqlite.go", "schema.go", "codec.go":
				continue
			}
			for _, spec := range astFile.Imports {
				if path, _ := strconv.Unquote(spec.Path.Value); path == "database/sql" {
					t.Errorf("%s/%s imports database/sql; the engine belongs "+
						"behind the Store interface of §2.11 and nowhere else",
						name, file)
				}
			}
		}
	}
}
```

`codec.go` is in the allow-list because the packed app slice is stored as a `[]byte` and the
conversion is part of the encoding, not of the query layer. It is in the list for that reason and
not because it is convenient -- a wider allow-list is a rule that stops being enforced.

**Store behaviour** -- `store_test.go`, against a real SQLite file in `t.TempDir()`:

| Test | Assertion |
|---|---|
| `TestStoreRoundTrip` | 10,000 samples in, one year-long query out, totals matching within float tolerance |
| `TestPutRawIsIdempotent` | the same sample written 3 times leaves one row |
| `TestQueryChoosesCoarsestFullCover` | a year query touches 1 row, a month query touches <= 31, and both are asserted with `EXPLAIN QUERY PLAN` and a row count, not by timing |
| `TestQueryPlanUsesThePrimaryKey` | every query in §2.10 plans as `SEARCH ... USING PRIMARY KEY`, not `SCAN`; this is the regression test for the ladder (§10.1) |
| `TestCoverageRefusesShortHistory` | four minutes of samples in a month returns `Usable() == false` |
| `TestCompareRefusesSparseWindow` | `Compare` on a sparse window returns `ErrInsufficientCoverage` |
| `TestReadOnlyStoreRefusesWrites` | every mutating method returns `ErrReadOnly` on a `ModeReader` handle |
| `TestSecondWriterWaitsRatherThanFailing` | a second process writing the same file blocks on the write lock and succeeds after the first commits, within `busy_timeout`; and a reader in that same second process is **not** blocked at all, which is the WAL claim of §9.4 asserted rather than asserted-in-prose |
| `TestConcurrentReadersOneWriter` | 8 goroutines querying while one writes, under `-race`, no errors, and `SetMaxOpenConns(1)` is not a source of deadlocks under `go test -race` with a 30 s timeout |
| `TestStoreSatisfiesInterface` | the compile-time assertion `var _ Store = (*sqlStore)(nil)`, and a second, test-only in-memory implementation asserted to satisfy `Store` too -- which is what makes the interface honest rather than decorative |

### 15.2 Fixtures

```
cellwatch/testdata/
  linux-energy_full.txt       real /sys/class/power_supply dump, energy_* layout
  linux-charge_full.txt       real dump, charge_* layout, no power_now
  linux-no-battery.txt        real desktop dump
  linux-rapl.txt              real /sys/class/powercap tree
  darwin-ioreg.txt            real ioreg -r -c AppleSmartBattery output
  darwin-ioreg-charging.txt   real output while charging, Amperage positive
  darwin-ioreg-silicon.txt    real Apple Silicon output, Amperage 0 while discharging
  darwin-pmset-log.txt        real pmset -g log output
  windows-powercfg.xml        real powercfg /batteryreport /xml output
  windows-srum-columns.txt    the SRUM column list and types, with its provenance noted
```

The fixtures are the contract. Captured once from real hardware and committed, they turn a platform
format change into a failing test rather than into a user's surprise. The Windows fixtures are the
awkward ones: a test machine is needed, and `windows-srum-columns.txt` records the column list of
the ESE table **with the date it was observed and the Windows build it was observed on**, because a
list without those two things is not a fact.

Parsers are split from the syscalls and the `exec` calls specifically so that they can be tested
against fixtures without either.

### 15.3 Manual verification -- real hardware, per OS

Not automatable, and not to be pretended otherwise.

**Windows** (laptop and desktop)

1. `chest battery` on a laptop on battery; compare `ChargePct` with Settings, within 1 %.
2. Same on AC: `AC` flips to `ACOn`, the rate becomes unavailable, and there is no fake zero.
3. On the desktop: no battery, exit 0, no crash.
4. A 60 s windowed run: the rate is within roughly 20 % of Task Manager's battery-use figure for the
   same period.
5. Record whether `BatteryLifeTime` ever comes back near `0xFFFFFFFF`. If it never does, the
   `> 2^31` guard is still correct but the `OS estimate` path is untested in practice, and that is
   worth knowing before claiming it.
6. `powercfg /batteryreport /xml` -- does it need elevation on this build? Does the XML contain
   per-minute `Capacity` in mWh? Record both answers in the fixture header.
7. `OpenSRUM` elevated -- does it open, and what does `EnergyEstimation` look like for a known
   process? If the ESE reader has not been chosen, this is the test that chooses it.

**Linux** (a laptop with `energy_*`, a laptop with `charge_*`, a desktop, and an Intel machine with
RAPL)

1. `ls /sys/class/power_supply` and confirm the committed fixture matches the layout under test.
2. On the `charge_*` machine, confirm the health row is **absent** rather than a nominal-voltage
   guess. This is the single most likely thing to be got wrong.
3. On a non-privileged user, confirm the `IOIncomplete` note appears and that it goes away when
   elevated. The library must not escalate itself.
4. With `hidepid=2` mounted, confirm the three-empty-tick path returns the section 12 error.
5. Disconnect and reconnect AC mid-run; confirm the window restarts rather than showing a negative
   drain.
6. RAPL: confirm `energy_uj` is readable, record the granularity, and confirm the wraparound
   arithmetic against a known long idle.
7. Check whether `/var/lib/upower/history-*` exists and what it contains. If it does, the Linux
   `NativeSource` is a real event importer rather than a non-existent one; if it does not, §7.3's
   statement that Linux has nothing stands unchallenged.

**macOS** (Intel **and** Apple Silicon)

1. `ioreg -r -c AppleSmartBattery`, diffed against the committed fixture.
2. Discharging: `Amperage` should read negative. Confirm the report says discharging and shows a
   positive watt magnitude.
3. Charging: `Amperage` positive, `IsCharging` true, rate negative, remaining unavailable.
4. `pmset -g log` and `pmset -g rawlog` **without** `sudo`, on a machine with no MDM profile.
   **Record whether elevation is required** -- §7.1 says this must be measured and not assumed.
5. `pmset -g log` after a week of uptime: how far back does it actually reach? The number goes in
   the fixture header, because it is the bound on the backfill.
6. On Apple Silicon under low load, watch for `Amperage == 0` while discharging, and confirm the
   fallback to the charge-delta window rather than a `0.0 W` reading.
7. A Mac with an MDM profile restricting `ioreg`: confirm the underlying error is shown verbatim.

**All platforms**

1. `go build` for all six release targets with `CGO_ENABLED=0` (`.github/workflows/release.yml:19-32`),
   and record the binary size delta against the gopsutil addition. The storage delta is expected
   to be zero and that is the point: measure it, do not assume it.
2. `go test ./...` and `go test -race ./...` (`CONTRIBUTING.md:42-45`).
3. `go test ./cellwatch/` alone, with the rest of the repository renamed, as a dry run of the
   extraction. If that passes, §1.6's conditions one and two are holding.
4. `grep -rn "Aswanidev-vs/chest/internal" cellwatch/` returns nothing.
5. A 24-hour sampler run followed by a year-sized query, to confirm the storage estimate in §10.7 is
   in the right order of magnitude and that nothing grows without bound.

---

## 16. How CHEST consumes cellwatch

CHEST is a consumer. Everything in this section is a thin wiring layer over the library, and the
reason it is thin is that §1.3's rules make it have to be.

### 16.1 The shape

```
  internal/cli/battery.go        the cobra command: flags, modes, signals
  internal/cli/batteryhistory.go the history modes, wiring Usage/TopApps/Compare
  internal/cli/batteryui.go      pure renderers: table, frame, JSON
  internal/cli/batteryui_test.go table-driven, bytes.Buffer, strings.Contains
        |
        |  import "github.com/Aswanidev-vs/chest/cellwatch"
        v
  cellwatch/                     everything else
```

The rule the previous design enforced and this one keeps: **`batteryui.go` never reads a process
list, a sysfs file, a clock, or the store.** It takes a fully-populated report struct and writes
bytes. That is what makes the `speedui_test.go` test style (`speedui_test.go:39-60`, a real
`bytes.Buffer` and `strings.Contains`) applicable unchanged.

### 16.2 Commands and flags

```go
cmd := &cobra.Command{
	Use:     "battery",
	Aliases: []string{"power", "batt"},
	Short:   "Report battery charge, drain rate, per-app drain ESTIMATE, and history",
	Args:    cobra.NoArgs,
	...
}
```

| Short | Long | Type | Default | Meaning |
|---|---|---|---|---|
| `-w` | `--watch` | bool | `false` | Live mode: refresh every `--interval` until Ctrl+C. |
| `-i` | `--interval` | duration | `2s` | Tick period in `--watch`. Clamped to `[200ms, 60s]`. Does not affect stored history. |
| `-s` | `--seconds` | duration | `0` | Measure for this long, then print one report and exit. |
| `-n` | `--samples` | int | `15` | Length of the rate-estimation window, in samples. |
| `-t` | `--top` | int | `5` | How many applications to list. |
| `-a` | `--apps` | bool | `false` | Include the per-app attribution table. |
| `-j` | `--json` | bool | `false` | Machine-readable output. |
| `-l` | `--low` | int | `0` | Low-charge warning threshold, percent. `0` disables. |
| `-f` | `--fail-under` | int | `0` | Exit `3` when measured charge is at or below this percent. `0` disables. |
| `-d` | `--days` | int | `7` | History window for the history modes. |
| `-P` | `--period` | string | `day` | `day`, `week`, `month` or `year`; maps to `cellwatch.Period`. |
| `-R` | `--record` | duration | `0` | Run the sampler in the foreground for this long, writing history. |
| `-c` | `--compare` | bool | `false` | Compare the window against the one before it. |
| `-h` | `--help` | bool | - | Cobra built-in. |

Two flags are new relative to the previous design, and both exist because the library now has
history: `--period` and `--compare`. The prefix set has **no pair where one long flag is a prefix of
another**, which §16.3 explains is load-bearing. `--record` is foreground-only on purpose: CHEST does
not install a daemon, and a CLI that quietly spawns a background process on a user's machine is a
different product from one that does not. A scheduled sampler is `cron`, an OS service, or a
`cellwatch` consumer's own decision.

**One flag is added to a different command, and it is recorded here so the two sections cannot
disagree.** `chest clean` gains `--battery`, which deletes `~/.chest/battery.db`. The default is
that battery history is never deleted. The full specification, the eight-way combination matrix and
the `-y` footgun are in §9.7; the CHEST-side change is one `BoolVar` and one `os.Remove` in
`internal/cli/intel.go`, listed in §16.6.

### 16.3 `detectFlagTypos` and what it constrains

`detectFlagTypos` runs on **every** invocation through `PersistentPreRunE` (`root.go:16-18`) and
rejects single-dash tokens longer than one character that prefix-match a long flag:

```go
	// Only consider single-dash tokens with more than one character.
	if len(arg) < 3 || arg[0] != '-' || arg[1] == '-' {
		continue
	}
	token := arg[1:]
	// If the first character is a shorthand that expects a value
	// (e.g. -pdownloads), the rest of the token is its value, not flags.
	if fl := flags.ShorthandLookup(token[:1]); fl != nil && fl.NoOptDefVal == "" {
		continue
	}
	var matches []string
	flags.VisitAll(func(fl *pflag.Flag) {
		if strings.HasPrefix(fl.Name, token) {
			matches = append(matches, "--"+fl.Name)
		}
	})
	if len(matches) > 0 {
		return fmt.Errorf("unknown flag %q. Did you mean %q?", arg, matches[0])
	}
```

`internal/cli/root.go:33-60`, body at 41-57.

**This is a constraint on the CHEST CLI wrapper and on nothing else.** It is a CLI-surface
invariant: it has no bearing on `cellwatch`'s API, its flag naming, or its package. Stated here
because the flag set above is affected by it, and only for that reason.

Consequences for the flag set in §16.2:

1. **`chest battery -json` is rejected** with `unknown flag "-json". Did you mean "--json"?`. That
   is correct behaviour, not a bug -- it is the same protection that stops `chest sort -dry` from
   silently enabling `--date --recursive --yes` (`root.go:24-32`).
2. **No long flag may be a prefix of another long flag.** `matches[0]` is the first hit from
   `flags.VisitAll`, which pflag orders lexicographically, so a prefix pair can produce a suggestion
   naming the wrong flag. The set `apps, compare, days, fail-under, help, interval, json, low,
   period, record, samples, seconds, top, watch` has no prefix pairs. If a future edit adds
   `--low-percent` beside `--low`, the rule forbids it rather than relying on alphabetical luck.
3. **Value-taking shorthands swallow the remainder of the token** (`root.go:47-49`), so `-i2s` and
   `-s45s` are legal and mean exactly what they look like. `-i`, `-s`, `-d` and `-R` are the
   value-taking shorthands here; `-w`, `-a`, `-j`, `-c` are bools with `NoOptDefVal = "true"`, so
   `-wj` is parsed by pflag as a cluster and the typo detector does not intercept it.
4. `-n` is `--samples` on this command and means nothing like `-n` = `--dry-run` on `sort`/`watch`
   (`watch.go:115`). Shorthands are per-command so there is no conflict, but the manual page states
   explicitly that `battery` is read-only and that `-n` is not a dry run.

### 16.4 The path, the colour and the store handle

**Path.** The library does not choose one (§2.2, rule 5). CHEST does, using the pattern already in
the tree (`os.UserHomeDir()` plus `.chest`, as at `internal/cli/repl.go:485-491` and
`internal/history/history.go:31`):

```go
// cellwatchPath returns the battery history database path. The library is
// given a path rather than choosing one, because when cellwatch is its own
// module there is no ~/.chest to choose it from -- which is §1.3 rule 5.
//
// battery.db sits beside index.db and history.json rather than inside
// index.db, and §9.2 is the reason. The short version: `chest clean
// --all` deletes index.db, and battery history is the only copy of
// measurements that exist nowhere else.
func cellwatchPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home directory: %w", err)
	}
	return filepath.Join(home, ".chest", "battery.db"), nil
}
```

A query opens with `ModeReader`; `--record` opens with `ModeWriter`. **A second `chest battery
--record` in another terminal is not an error**, and this is a real change in behaviour worth
stating plainly: under the previous engine it returned a "locked by another process" error within a
second, because a write transaction held an exclusive file lock. Under WAL it is simply the second
writer, and it waits its turn (§9.4). Both samplers write the same `battery_raw` rows keyed by
timestamp, so they converge rather than conflict -- and if a consumer genuinely wants the
single-sampler guarantee, it is a documented option rather than an accident of the storage engine
and should be spelled out in the man page.

**Colour.** Reused verbatim from `speedtest.go:21-26` and `speedui.go:14-17`; cellwatch introduces
zero new escape sequences and zero new glyphs. Semantic assignment as before: `chestPrimary` green
for normal charge, `chestGold` for on AC or charging, `chestCyan` for every `ESTIMATE` label and
derived number, `chestRed` for below `--low` or an error, `chestDim` for units, footnotes and rules.
The one addition is a convention rather than a constant: `chestCyan` on the word `measured` too, so
that a consumer can switch on `Provenance` and get a consistent colour for both classes of figure
without the reader having to know which is which.

**Redraw.** In-place, not full-screen. The alternate screen buffer is rejected for the same reason as
before: scrolling back after quitting would show nothing, and the report pasted into a bug report
would be gone. `frame N+1 write = "\x1b[<prevLines>F" + "\x1b[0J" + <frame body>`, with `prevLines`
tracked rather than guessed, is the multi-line generalisation of the `\r\x1b[2K` form already used at
`progress.go:83` and `progress.go:147`. The live frame goes to **stderr** so that
`chest battery --watch --json > ticks.jsonl` produces a clean file, matching the convention at
`speedtest.go:92-97`.

### 16.5 Files to create

| Path | Contents |
|---|---|
| `cellwatch/*.go` | §2.1 |
| `cellwatch/*_test.go`, `cellwatch/testdata/` | §15 |
| `internal/cli/battery.go` | `newBatteryCmd`, flags, mode dispatch, signals |
| `internal/cli/batteryhistory.go` | the history modes over `Usage` / `TopApps` / `Compare`, and `--record` over `Sampler.Run` |
| `internal/cli/batteryui.go` | `printBatteryTable`, `batteryBar`, `buildFrame`, `printBatteryJSON` |
| `internal/cli/batteryui_test.go` | §15-style renderer tests |

### 16.6 Files to touch

| Path | Change | Anchor |
|---|---|---|
| `internal/cli/root.go` | add `rootCmd.AddCommand(newBatteryCmd())` | after `root.go:90` |
| `internal/cli/man.go` | a `"battery"` entry in `manTopics` | after the `speedtest` block, `man.go:475-502` |
| `internal/cli/man.go` | add `"battery"` to the root topic's `SeeAlso` | `man.go:48` |
| `README.md` | one row in the Command Reference table | after the `chest speedtest` row, `README.md:89` |
| `README.md` | one Core Features bullet | after the speedtest bullet, `README.md:70` |
| `docs/documentation.html` | sidebar nav link | after `href="#cmd-speedtest"`, line 68 |
| `docs/documentation.html` | a `<h3 id="cmd-battery">` section | after the speedtest section, line 763 |
| `CONTRIBUTING.md` | add `cellwatch/` to the codebase tree | `CONTRIBUTING.md:52-70` |
| `CONTRIBUTING.md` | add the leaf rule: no imports from `chest/...` | §1.3 |
| `plan.md` | add `battery` to the Meta group in the CLI Surface table | `plan.md:31` |
| `go.mod` / `go.sum` | `github.com/shirou/gopsutil/v4 v4.26.8` as a direct dep. **No storage dependency is added**: `github.com/ncruces/go-sqlite3` is already a direct dep at `go.mod:11` | `go.mod:5-20` |
| `internal/cli/intel.go` | a `--battery` case in `newCleanCmd`, alongside `--all` and `--history` | `intel.go:107`, flags at `intel.go:179-181` |

`CONTRIBUTING.md:70` ends the tree at `watcher/`, and `internal/metadata/` is already missing from
it (see Appendix A), so `cellwatch/` goes in at the top level -- it is a top-level directory, not an
`internal/` one, and that is the point.

### 16.7 What is deliberately not touched

- `internal/models`, `internal/rules`, `internal/planner`, `internal/organizer`,
  `internal/filesystem`, `internal/history` -- the command has no place in the sort pipeline.
- `internal/indexer` -- **not** touched, and `cellwatch` does not import it. The file index's
  `history_entries` / `history_operations` tables stay as they are: unused, and `plan.md:80`
  already records that as a known finding rather than a resource. The battery store is a separate
  file with a separate connection for the reasons in §9.3, and the only thing shared with the indexer
  is a blank driver import and a convention about where files live in `~/.chest`.
- `internal/cli/progress.go` -- `spinner` and `progressBar` are reused, not modified. A
  battery-specific variant would fork a working TTY gate.
- `Makefile`, `install.sh`, `install.ps1`, `.github/workflows/release.yml` -- the build matrix and
  install paths are platform-agnostic already, and §1.2 is the reason they must stay that way.

---

## 17. Dependencies and build constraints

### 17.1 What `go.mod` gains

```
require (
	github.com/shirou/gopsutil/v4 v4.26.8        // NEW - process sampling
)

require (
	github.com/ncruces/go-sqlite3 v0.35.4       // ALREADY a direct dep (go.mod:11) - storage
	golang.org/x/sys v0.47.0                    // existing direct dep (go.mod:18)
	github.com/yusufpapurcu/wmi vX              // NEW indirect, via gopsutil (Windows)
	github.com/ebitengine/purego vX             // NEW indirect, via gopsutil (Darwin)
)
```

**One new direct dependency, and it is only gopsutil.** The storage engine adds nothing at all: not
a module, not a version, not a line in `go.sum`. `github.com/ncruces/go-sqlite3` is already a
direct dependency at `go.mod:11`, already registered by the blank import at
`internal/indexer/indexer.go:20`, and its WebAssembly payload is already linked into every shipped
binary (§8.1). Its two indirects, `ncruces/go-sqlite3-wasm/v5` v5.0.35304 (`go.mod:31`) and
`ncruces/julianday` v1.0.0 (`go.mod:32`), are likewise already present.

This is now the strongest selling point `cellwatch` has, and it is worth stating in the form a
reader can check: **the whole feature adds one module, and that module is for per-application
attribution, which is optional and which the design says can be dropped.** Drop gopsutil and the
library is pure standard library plus a driver CHEST already had. Pure Go, no cgo, no new modules,
and the storage decision contributes zero to any of those.

**The one honest exception, which is about extraction and not about CHEST.** The argument above is
true *inside this repository* and only inside it. When `cellwatch/` is split into its own module
(§1.5), `ncruces/go-sqlite3` stops being a dependency CHEST already had and becomes three new
modules in the new module's `go.mod`, carrying a 4,690,964-byte source file. §1.5's checklist
records it at step 3, and it is the single item on that checklist that this decision makes worse
rather than better.

It is worth putting that next to the decision rather than burying it, because the honest summary is:
**the storage choice is free for CHEST and not free for a hypothetical standalone cellwatch.** For
CHEST, which is the deliverable today, that is a strong argument. For a library that others will
adopt, a 4.7 MB dependency for an embedded history database is a real cost, and a future maintainer
of the standalone repository may reasonably revisit it. §9.8 is the answer if they do -- moving the
file is one string, and the DDL is portable to any SQLite.

**gopsutil adds two indirect modules** and nothing else. v4.26.8 is the latest published version
(verified against the Go module proxy); v4's package set is `cpu`, `disk`, `host`, `load`, `mem`,
`net`, `process`, `sensors`, `winservices`.

**gopsutil has no battery package.** This was verified exhaustively before the previous design was
written, and the finding is unchanged and load-bearing: the module proxy index, the full contents of
the release zips for v3.24.5 and v4.26.8, the complete git tree at those tags, and all 130 published
tags were checked. There is no `battery` package. Battery state is read natively per platform in §3,
and that is not a preference -- the dependency cannot be asked for battery data because it does not
have it.

APIs relied on, all in `process` and `cpu`:

| Call | Returns | Used for |
|---|---|---|
| `process.Processes()` | `[]*process.Process` | the PID set each tick |
| `(*Process).Times()` | `TimesStat{User, System, Total float64}` (seconds) | CPU-second deltas |
| `(*Process).IOCounters()` | `IOCountersStat{ReadBytes, WriteBytes uint64}` | I/O deltas |
| `(*Process).CreateTime()` | `(int64, error)` (ms since epoch) | the PID-reuse guard |
| `(*Process).Name()` | `(string, error)` | the grouping key |
| `cpu.Counts(false)` | `(int, error)` | logical CPU count, for the capacity normaliser |
| `cpu.Times(false)` | `[]TimesStat` | system-wide busy time, for context fields |

### 17.2 CGO and the release matrix

`CONTRIBUTING.md:104` requires pure-Go dependencies to keep cross-compilation effortless. Nothing
that `cellwatch` adds violates it, and that is a property of the decision rather than a fact about
the libraries:

- `ncruces/go-sqlite3` is pure Go. It is a real SQLite compiled to WebAssembly and run on
  `wazero`-style WASM, which is precisely why it needs no cgo where `mattn/go-sqlite3` does. It
  already builds under `CGO_ENABLED=0` for all six release targets, because `chest` already depends
  on it.
- gopsutil v4 is pure Go. Its two dependencies are `yusufpapurcu/wmi` (Windows service and process
  metadata, pure Go) and `ebitengine/purego` (Darwin `IOKit` and `proc_pidinfo` calls through
  `libSystem` without cgo).

The six release targets at `.github/workflows/release.yml:19-32` are unaffected, and the build at
line 57-61 keeps working because `cellwatch/` is a package in the same module rather than a nested
one -- which is §1.2's second point restated as a build fact.

**Binary size, and what Phase 2 should actually measure.** The storage delta is expected to be
**zero**, and measuring it is still worth doing: "expected zero" is a prediction, and §8.2 is a
paragraph about a prediction that was wrong. The number to record is the size of `chest.exe` before
and after `cellwatch/` exists, and the assertion is that it does not move.

The gopsutil delta is the real measurement, together with first-tick latency on a machine with 400+
processes. If gopsutil's cost is not worth per-app attribution, the honest fallback is to drop it
and ship everything else: the library reads state, stores history and answers system-level
questions, with `Coverage.Apps == false` and an empty `Top` -- which is a truthful answer, not a
degraded one. That fallback got materially cheaper with this storage decision, because the storage
half of the feature now costs nothing regardless of which way the attribution question goes.

---

## 18. Phased delivery

Effort scale copied from `plan.md:95`: **S** = days, **M** = ~1-2 weeks, **L** = multi-week. Risk is
the chance of regressing existing behaviour. In a library with no consumers outside CHEST, "regress
existing behaviour" means CHEST's build, CHEST's binary size, and the six-target matrix.

### Phase 0 -- Spike (Effort **S**, Risk None)

- Read `GetSystemPowerStatus`, `/sys/class/power_supply/` and `ioreg -r -c AppleSmartBattery` by hand
  on one machine per OS. Capture the raw output as fixtures.
- Confirm the `SYSTEM_POWER_STATUS` layout and the `> 2^31` guard against a real laptop, and record
  whether `BatteryLifeTime` is ever sane.
- **Record whether `pmset -g log` and `pmset -g rawlog` need elevation, and how far back the log
  reaches.** §7.1 says this must be measured, not assumed, and the backfill feature's usefulness
  depends entirely on the second answer.
- Run `powercfg /batteryreport /xml` on Windows; record whether it needs elevation and whether the
  XML has per-minute mWh. On Linux, record whether `/var/lib/upower/history-*` exists.
- On an Intel machine, read `/sys/class/powercap` and record the counter granularity and wraparound
  period.
- **Exit criteria:** eight committed fixtures with headers recording the machine, the OS build and
  the date, and written answers to the four questions above. No CLI, no dependency.

### Phase 1 -- The library skeleton and the readers (Effort **S**, Risk None) - shippable

- `cellwatch/` with `Status`, `Reader`, the three platform readers, the fallback, and the capability
  mask. Parsers split from the syscalls and the `exec` calls so they are fixture-testable.
- `cellwatch/leaf_test.go` and the capability tests from §5.3. This phase is where the extraction
  rules become executable.
- Touch: nothing outside `cellwatch/`.
- **Why shippable:** it is correct, fast and has no estimation in it at all, and every other phase
  depends on it.

### Phase 2 -- The SQLite store, the schema, the write path (Effort **S**, Risk None)

**This phase used to be the largest one. It is now the smallest, and that is the clearest
consequence of the storage decision** (§8.1). There is no key layout to specify to the byte, no
binary codec to write and test, no lock-timeout error to define, and no dependency to add. What
remains is four `CREATE TABLE IF NOT EXISTS` statements and an upsert.

- `store.go`, `schema.go`, `store_sqlite.go`, `codec.go` (the packed app slice only), and the
  bucket functions from §10.2.
- `TestSchemaIsIdempotent` and `TestNoTableOutsideTheSchema` land here, before any query code
  exists to depend on a wrong schema -- and they are the whole of the "migration" story, which is
  the item the previous design carried a codec version byte for.
- The DST test from §10.3 lands here too, against a real store rather than against a formatting
  function, so the primary-key guarantee is asserted rather than assumed.
- **No `go.mod` change at all.** `ncruces/go-sqlite3` is already there (§17.1).
- **Enforce the engine boundary mechanically, not by convention.** `TestSQLStaysInTheStoreFiles`
  (§15.1) is ten lines and it is the difference between a seam and a wish: `database/sql` outside
  `store_sqlite.go` and `schema.go` is a test failure. This is the direct replacement for the old
  "grep for `*bolt.DB` outside `store_bbolt.go`" check, and it protects the same property -- that a
  different store is a new implementation of §2.11 rather than a rewrite of the query layer.
- **Measure the binary size delta and record it as zero.** §17.2 says why a prediction of zero is
  still worth measuring, and §8.2 is the reason.
- **Measure the real storage figure** by generating a year of synthetic rollups and reading the
  file size, and correct §10.7's table with the measurement.

**Risk note:** lower than any previous Phase 2, but not zero. The two things that can go wrong are
`SetMaxOpenConns(1)` being "fixed" by someone who finds the queueing surprising, and the app slice
being normalised into a second table by someone who finds normalisation surprising. Both are
defended by the tests named above and by the reasoning in §9.5, which is why that reasoning is
written down rather than left implicit.

### Phase 3 -- Rollups, coverage and queries (Effort **M**, Risk None)

- `rollup.go`, `range.go`, `series.go`, `usage.go`: the ladder, the boundaries, the coverage
  arithmetic, `Usage` / `TopApps` / `AppHistory` / `Compare`.
- The recompute-not-accumulate invariant and its idempotence test, over the
  `INSERT OR REPLACE` path of §10.4.
- `prune` and the retention table.
- `TestQueryPlanUsesThePrimaryKey`: every query in §2.10 must plan as a `SEARCH` on the primary
  key. This is the test that keeps the ladder honest, and it is new -- the bbolt design had the
  equivalent property by construction (a cursor over an ordered key space) rather than by
  assertion, and in SQL it has to be checked.
- **This is the phase that makes history trustworthy**, and it is now the largest remaining one
  along with Phase 4. The coverage rules and the `ErrInsufficientCoverage` refusal belong here, not
  in a later polish pass, because a query layer built without them is one where adding them later
  means changing the API.

### Phase 4 -- Sampler, rate estimation and attribution (Effort **M**, Risk Low)

**The largest phase in the plan, and it is largest because the storage phase shrank.** The effort
that was spent specifying a byte-exact key layout and a binary record codec has nowhere to go, and
this is where it should go.

- `sampler.go`, `rate.go`, `attrib.go`, `attrib_sample.go`; `go.mod` gains gopsutil.
- **The §4.4 four-layer estimator and its quantisation regression tests.** With the storage phase
  down to **S**, this is the single hardest piece of engineering left in the design, and the one
  where being wrong produces numbers that look right. The 1 %-granularity problem is analysed in
  §4.4 and it does not go away because the database is easier: a two-sample window on a 1 %-stepped
  reading is 0 %, 50 % or 100 % of the truth, and only the window estimator fixes it.
- **The rollup upserts and their idempotence**, exercised against a real store rather than against
  a mock. Phase 3 writes them; this phase is where they meet late raw samples, native-log import
  and a power cut mid-transaction, which is where "recompute, never accumulate" either holds or
  quietly does not.
- **The background sampler lifecycle**, which the previous design under-specified and this one should
  not: what happens on suspend and resume (§14's tick-gap rule), what happens when `battery.db` is
  deleted underneath a running sampler, what the exit path does (checkpoint the WAL, `Close`, so
  the `-wal` file is truncated and a plain file copy of the database is complete -- §10.7), and
  what a second `--record` process does under WAL rather than under an exclusive file lock (§16.4).
- The grouping table, the weights, the idle floor, the unattributed row, the PID-reuse guard.
- Measure sampling cost and binary size delta. Re-verify all six targets with `CGO_ENABLED=0`.
- **Risk note:** this is where a wrong number is most likely to look right. It stays behind an
  opt-in flag in the consumer, and if the measurements do not hold up, drop the phase and ship
  everything else.

### Phase 5 -- Native-log import (Effort **M**, Risk None, platform-split)

- `native.go` and the conflict/reconciliation rules of §7.6, which are platform-independent and land
  first with a test-only source.
- `native_darwin.go`: `pmset -g log` and `-g rawlog`, at whatever elevation Phase 0 established.
- `native_windows.go`: `powercfg /batteryreport /xml`.
- `srum_windows.go`: the `SRUMReader` seam and `RegisterSRUMReader`. **The ESE parser itself is not
  in this phase and may never be** -- it is a separately-versioned component and §7.2 says so. If a
  suitable parser is found, the seam already exists for it.
- `rapl_linux.go`: measured CPU-package energy, in a later sub-phase because it is Linux-only and
  does not block anything.

### Phase 6 -- CHEST consumer (Effort **M**, Risk Low) - shippable

- `internal/cli/battery.go`, `batteryhistory.go`, `batteryui.go`, `batteryui_test.go`.
- Flags from §16.2. `root.go`, `man.go`, `README.md`, `docs/documentation.html`, `CONTRIBUTING.md`,
  `plan.md`, and the `chest clean --battery` flag of §9.7 in `internal/cli/intel.go`.
- The `chest clean` work is small in code and load-bearing in behaviour, and it deserves its own
  test rather than a manual check: a table-driven test over the eight combinations of `--all`,
  `--history` and `--battery` from §9.7, asserting which files survive each one, plus a test that
  `--battery` against a non-existent `battery.db` exits 0. The failure mode being prevented is a
  future edit that makes `--all` imply `--battery`, and a test is the only thing that will catch it
  in a year when nobody remembers this paragraph.
- **Why separate:** the library is the deliverable, and a consumer merged into the same phase is a
  consumer whose failures get attributed to the library. It also means the library can be extracted
  at any point after Phase 5 without untangling it from CLI work.

### Sequencing

**Phases 1, 2 and 3 are the library's spine** -- read, store, query -- and Phase 2 is now **S**
rather than **M**, which is the storage decision's most useful effect on this plan. Phase 4 makes
the numbers trustworthy and is now the largest single piece of work in it. Phase 5 is the
cross-check. Phase 6 is CHEST. If the schedule is tight, cut 5, then 4, and keep 1, 2, 3, 6: the
result is a library that reads state, keeps history and answers system-level questions correctly on
three platforms, with no CGO, no elevation, no estimation anywhere in its system-level numbers, and
**not one new dependency for the storage** -- plus a CLI that says, in plain words, that per-app
figures are apportioned because no operating system says otherwise.

---

## Appendix A: pre-existing CHEST defects

Both were found while writing the previous version of this document and both are still true. Neither
is introduced or worsened by `cellwatch`, and neither is a blocker for it. They are recorded here
because they are cheap to fix and they will be re-discovered otherwise.

**A1 -- `man.go` documents a shorthand that does not exist.** `internal/cli/man.go:490` renders

```
"-j, --json           Output results as machine-readable JSON",
```

while `internal/cli/speedtest.go:106` registers

```go
cmd.Flags().BoolVar(&jsonOut, "json", false, "Output results as JSON")
```

with no shorthand. So the manual page promises `chest speedtest -j` and pflag does not provide it.
`chest speedtest -j` fails with an "unknown shorthand" error. Both lines were re-read against the
current tree while rewriting this document and both are unchanged.

Fix, if wanted: either `cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "Output results as JSON")`
or drop the `j` from the man page. The first is right, because the man page is the thing users read
and `-j, --json` is the convention every other command with a JSON flag already follows.

**A2 -- the documented codebase tree is missing a package.** `CONTRIBUTING.md:52-70` lists
`internal/` alphabetically from `classifier/` to `watcher/`, and `internal/metadata/` is not in it,
even though `internal/metadata/metadata.go` exists, defines the `Extractor` registry that
`internal/plugin` serves over RPC, and is the largest single package by file count in the tree.
`plan.md:21` describes it as one of the five enrich steps in the pipeline, so the omission is in the
tree diagram rather than in the architecture.

Fix: insert `│   ├── metadata/          # File metadata extraction & plugin RPC registry` between
`models/` and `organizer/`. While doing so, add `cellwatch/` at the top level -- it is not under
`internal/`, and a tree that shows everything else under `internal/` is the natural place a reader
would look for the exception and not find it.

<!--END-OF-DOC-->








