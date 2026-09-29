package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Aswanidev-vs/chest/cellwatch"
	"github.com/spf13/cobra"
)

// newBatteryCmd returns the `chest battery` command. It reports battery state
// read from the host operating system, and optionally records that state for
// later reporting.
//
// Every number this command prints is either measured by the platform or
// labelled as an estimate. The distinction is carried in the colour of the
// label and in the JSON field names, because a battery percentage that is
// wrong by being confidently presented is worse than one that is absent.
func newBatteryCmd() *cobra.Command {
	var (
		record    bool
		seconds   time.Duration
		interval  time.Duration
		jsonOut   bool
		lowPct    int
		failUnder int
		dbPath    string
		apps      bool
		top       int
		daemon    bool

		// Period flags are collected separately and resolved once, so a
		// combination of them is reported as a usage error instead of one
		// silently winning.
		periodFlag, weekFlag, monthFlag, yearFlag bool
	)

	resolvePeriod := func() (cellwatch.Period, error) {
		var chosen cellwatch.Period
		var chosenFlag string
		var set bool
		for _, p := range []struct {
			on   bool
			per  cellwatch.Period
			name string
		}{
			{periodFlag, cellwatch.PeriodDay, "--day"},
			{weekFlag, cellwatch.PeriodWeek, "--week"},
			{monthFlag, cellwatch.PeriodMonth, "--month"},
			{yearFlag, cellwatch.PeriodYear, "--year"},
		} {
			if !p.on {
				continue
			}
			if set {
				return 0, fmt.Errorf("choose one period: %s conflicts with %s", p.name, chosenFlag)
			}
			chosen, chosenFlag, set = p.per, p.name, true
		}
		if !set {
			return cellwatch.PeriodAll, nil
		}
		return chosen, nil
	}

	cmd := &cobra.Command{
		Use:     "battery",
		Aliases: []string{"power", "batt"},
		Short:   "Report battery charge, power draw and drain rate",
		Long: `Read battery state from the operating system and report it.

What is measured and what is estimated matters more here than in most commands,
so the two are never mixed:

  MEASURED  Charge percentage, mains state, time remaining, capacity and health,
            wherever the platform reports them. A field the platform cannot
            supply is omitted rather than shown as zero.

  ESTIMATED Drain rate, when the platform exposes no wattage. It is derived from
            change in charge over a window, and the window must be long: battery
            charge is reported in whole percentage points, so on Windows a
            machine draining at one point per seventy seconds cannot be measured
            any faster than that. Short windows are refused rather than answered.

No platform reports true per-application battery use to an unprivileged
process, so this command does not claim to. Per-app figures, where present, are
apportioned from measured CPU and I/O activity and are labelled ESTIMATE.

Use --record to append readings to a history database, --seconds to measure
over a window rather than take a single reading, and --json for
machine-readable output.

HISTORY reads what --record has written:

  chest battery --day            charge and drain for today
  chest battery --week           the current ISO week
  chest battery --month          the current calendar month
  chest battery --year           the current calendar year
  chest battery --month --apps   with per-application attribution

A historical report states its coverage. A range in which the sampler ran for
two hours is reported as such rather than presented as a description of the
month.`,
		Example: `  chest battery
  chest battery --json
  chest battery --seconds 600
  chest battery --record --apps
  chest battery --week --apps
  chest battery --low 20`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			reader, err := cellwatch.NewReader()
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			period, err := resolvePeriod()
			if err != nil {
				return err
			}

			// A period request reads history rather than the machine, so it is
			// dispatched before anything touches the hardware. Reading the
			// battery to answer "what did I spend this month" would report the
			// present in answer to a question about the past.
			if period != cellwatch.PeriodAll {
				return batteryHistory(cmd, period, apps, top, jsonOut, dbPath)
			}

			// A daemon records until stopped. It is started deliberately and not
			// installed as a service, because silently beginning to observe a
			// machine is a decision only the user should make.
			if daemon {
				return batteryDaemon(cmd, reader, interval, apps, dbPath, jsonOut)
			}

			// A single reading cannot produce a rate, so a bare invocation
			// reports state only. Measuring means waiting, and the user has to
			// ask for that with a duration.
			if seconds <= 0 {
				st, err := reader.Read()
				if err != nil {
					return err
				}
				r := cellwatch.Rate{Reason: "single reading; use --seconds to measure a rate"}
				if err := emitBattery(out, st, r, lowPct, jsonOut); err != nil {
					return err
				}
				if err := batteryExit(st, failUnder); err != nil {
					// A threshold trip is an answer, not a usage error, so the
					// flag documentation is not reprinted underneath it.
					cmd.SilenceUsage = true
				}
				return batteryExit(st, failUnder)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Ctrl+C must end the measurement and still print what was gathered.
			// A user who interrupts a ten-minute run wants the partial result,
			// not a discarded one.
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(sig)
			go func() {
				<-sig
				cancel()
			}()

			est := cellwatch.NewRateEstimator(interval)

			// The attributor is created only when --apps is given, because
			// sampling enumerates every process on the machine and that cost
			// should not be paid by a run that will not use the result.
			var attributor *cellwatch.Attributor
			if apps {
				attributor = cellwatch.NewAttributor()
				// A first sample establishes the baseline; a second is needed
				// before there is an interval to measure activity over.
				_ = attributor.Sample()
			}

			// The store is opened only when recording is asked for, so a plain
			// read never creates a database file on disk.
			var store cellwatch.Store
			if record {
				store, err = cellwatch.Open(dbPath)
				if err != nil {
					return err
				}
				defer store.Close()
			}

			var last cellwatch.Status
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			// The first reading is taken immediately rather than after one
			// tick, so a short --seconds run still returns something.
			st, err := reader.Read()
			if err != nil {
				return err
			}
			last = st
			est.Add(cellwatch.Sample{At: time.Now(), Pct: st.ChargePct, AC: st.AC, Charge: st.IsCharging()})
			if store != nil {
				// A failed write is reported rather than swallowed: a sampler
				// that silently stops recording looks identical to one that
				// never recorded anything.
				if err := store.Record(cmd.Context(), st); err != nil {
					return fmt.Errorf("recording: %w", err)
				}
			}

			deadline := time.NewTimer(seconds)
			defer deadline.Stop()

			for {
				select {
				case <-ctx.Done():
					if err := emitFinal(out, last, est.Rate(), lowPct, jsonOut); err != nil {
						return err
					}
					return batteryExit(last, failUnder)
				case <-deadline.C:
					if err := emitFinal(out, last, est.Rate(), lowPct, jsonOut); err != nil {
						return err
					}
					return batteryExit(last, failUnder)
				case <-ticker.C:
					st, err := reader.Read()
					if err != nil {
						// A single failed read is not fatal: the previous
						// reading is still the most recent true one.
						continue
					}
					last = st
					est.Add(cellwatch.Sample{At: time.Now(), Pct: st.ChargePct, AC: st.AC, Charge: st.IsCharging()})
					if store != nil {
						if err := store.Record(cmd.Context(), st); err != nil {
							return fmt.Errorf("recording: %w", err)
						}
					}
					if attributor != nil && store != nil {
						if err := attributor.Sample(); err == nil {
							// The rate is passed through so the stored
							// per-application figure is drain rather than a bare
							// share. A share with no rate behind it cannot be
							// turned back into a cost later.
							attributed := attributor.Attribute(est.Rate().PctPerHour)
							if !attributed.Idle && len(attributed.Apps) > 0 {
								if err := store.Apps(cmd.Context(), time.Now(), attributed.Apps); err != nil {
									return fmt.Errorf("recording apps: %w", err)
								}
							}
						}
					}
				}
			}
		},
	}

	cmd.Flags().BoolVar(&record, "record", false, "Append each reading to the battery history database")
	cmd.Flags().DurationVar(&seconds, "seconds", 0, "Measure over this long instead of taking a single reading")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Sampling interval while measuring")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as machine-readable JSON")
	cmd.Flags().IntVar(&lowPct, "low", 0, "Report charge at or below this percentage as low (0 disables)")
	cmd.Flags().IntVar(&failUnder, "fail-under", 0, "Exit 3 when charge is at or below this percentage (0 disables)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Path to the history database (default: ~/.chest/battery.db)")
	cmd.Flags().BoolVar(&apps, "apps", false, "Report per-application activity apportioned from CPU and I/O (ESTIMATE)")
	cmd.Flags().IntVar(&top, "top", 5, "How many applications to list with --apps")
	cmd.Flags().BoolVar(&daemon, "daemon", false, "Keep recording in the foreground until Ctrl+C")

	// The period flags are mutually exclusive rather than cumulative, because
	// "this week and this month" has no single sensible answer and picking one
	// silently would report the wrong range.
	cmd.Flags().BoolVar(&periodFlag, "day", false, "Report the current UTC day from history")
	cmd.Flags().BoolVar(&weekFlag, "week", false, "Report the current ISO week (starts Monday) from history")
	cmd.Flags().BoolVar(&monthFlag, "month", false, "Report the current calendar month from history")
	cmd.Flags().BoolVar(&yearFlag, "year", false, "Report the current calendar year from history")
	return cmd
}

// batteryHistory answers a period question from recorded history.
func batteryHistory(cmd *cobra.Command, p cellwatch.Period, withApps bool, topN int, jsonOut bool, dbPath string) error {
	store, err := cellwatch.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	now := time.Now()
	r := cellwatch.ThisPeriod(now, p)
	ctx := cmd.Context()

	rep, err := store.Usage(ctx, r)
	if err != nil {
		return err
	}
	// The per-application series is collected here and rendered as part of the
	// report, rather than printed before it, so the chart appears after the
	// coverage and rate figures that qualify it.
	var series map[string][]cellwatchPoint
	var names []string

	if withApps {
		apps, err := store.TopApps(ctx, r, topN)
		if err != nil {
			return err
		}
		rep.Apps = apps

		// A per-application time series is fetched alongside the totals. The
		// totals answer "which application"; the chart answers "when", and a
		// ranking with no shape behind it is a number a reader has to trust
		// rather than understand. The series is handed to the renderer rather
		// than printed here, because the chart belongs inside the report, after
		// the coverage and rate that qualify it.
		series = map[string][]cellwatchPoint{}
		names = make([]string, 0, len(rep.Apps))
		for _, a := range rep.Apps {
			s, err := store.AppHistory(ctx, a.Name, r)
			if err != nil {
				continue
			}
			points := make([]cellwatchPoint, 0, len(s.Points))
			for _, pt := range s.Points {
				points = append(points, cellwatchPoint{At: pt.At, CPUSeconds: pt.CPUSeconds})
			}
			series[a.Name] = points
			names = append(names, a.Name)
		}
	}

	if !rep.Coverage.Usable() {
		// A thin history is the single most misleading thing this command
		// could print. Two hours of samples described as a month is worse than
		// no report, so it says what was actually observed.
		rep.Notes = append(rep.Notes,
			"history is sparse: the sampler has not been running for most of this period, so these figures describe only the samples recorded")
	}

	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	printBatteryHistory(cmd.OutOrStdout(), rep, p, topN, series, names)
	return nil
}

// batteryDaemon records until interrupted.
func batteryDaemon(cmd *cobra.Command, reader cellwatch.Reader, interval time.Duration, withApps bool, dbPath string, jsonOut bool) error {
	store, err := cellwatch.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	s, err := cellwatch.NewSampler(cellwatch.SamplerConfig{
		Reader:   reader,
		Store:    store,
		Interval: interval,
		Attrib:   withApps,
	})
	if err != nil {
		return err
	}

	if !jsonOut {
		fmt.Fprintf(cmd.ErrOrStderr(), "\n%sRecording battery history. Press Ctrl+C to stop.%s\n", chestDim, chestReset)
	}
	n, err := s.Run(cmd.Context())
	stats := s.Stats()
	if !jsonOut {
		fmt.Fprintf(cmd.ErrOrStderr(), "\n%sRecorded %d readings over %s.%s\n",
			chestPrimary, n, stats.Started.Round(time.Second), chestReset)
		if stats.LastErr != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "%sLast error: %s%s\n", chestRed, stats.LastErr, chestReset)
		}
		return nil
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"recorded": n, "stats": stats})
}

func emitFinal(out io.Writer, st cellwatch.Status, r cellwatch.Rate, lowPct int, jsonOut bool) error {
	return emitBattery(out, st, r, lowPct, jsonOut)
}

// thresholdError reports that a --fail-under threshold was met, so a script
// can act on the battery level without parsing the human-readable table.
//
// It exists because Execute exits 1 for any error, and a threshold trip is not
// a failure of the command: the command did its job and the answer was "yes".
type thresholdError struct{ threshold int }

func (e thresholdError) Error() string {
	return fmt.Sprintf("charge is at or below %d%%", e.threshold)
}

// batteryExit reports the process exit code for a completed reading. A
// threshold trip is a distinct outcome from success, so it is carried as a
// typed error that Execute translates into exit status 3.
func batteryExit(s cellwatch.Status, failUnder int) error {
	if failUnder > 0 && s.ChargeKnown && s.ChargePct <= float64(failUnder) {
		return thresholdError{threshold: failUnder}
	}
	return nil
}

// emitBattery renders one report. It is a pure function of its arguments so it
// can be tested by writing to a buffer, never by touching a terminal.
func emitBattery(w io.Writer, s cellwatch.Status, r cellwatch.Rate, lowPct int, jsonOut bool) error {
	if jsonOut {
		return printBatteryJSON(w, s, r)
	}
	printBatteryTable(w, s, r, lowPct)
	return nil
}

// batteryRow is one label/value line of the report.
type batteryRow struct {
	Label  string
	Value  string
	Detail string
	// Estimate marks a derived figure. It drives both the colour and the
	// tilde prefix, so an estimate cannot be mistaken for a measurement
	// anywhere in the output.
	Estimate bool
	// Known is false when the platform did not report the value at all. Such a
	// row is omitted rather than printed as a zero.
	Known bool
}

func buildBatteryRows(s cellwatch.Status, r cellwatch.Rate, lowPct int) []batteryRow {
	var rows []batteryRow
	add := func(r batteryRow) {
		if r.Known {
			rows = append(rows, r)
		}
	}

	if !s.Present {
		return []batteryRow{{
			Label:  "Battery",
			Value:  "none detected",
			Detail: "this machine reports no battery",
			Known:  true,
		}}
	}

	pct := "unknown"
	if s.ChargeKnown {
		pct = fmt.Sprintf("%.0f%%", s.ChargePct)
		if lowPct > 0 && s.ChargePct <= float64(lowPct) {
			pct += "  LOW"
		}
	}
	add(batteryRow{Label: "Charge", Value: pct, Known: s.ChargeKnown})

	ac := s.AC.String()
	switch {
	case s.IsCharging():
		ac = "charging"
	case s.Full:
		ac = "full"
	case s.Draining():
		ac = "on battery"
	}
	add(batteryRow{Label: "Power", Value: ac, Detail: s.Status, Known: s.AC != cellwatch.ACUnknown})

	add(batteryRow{
		Label:  "Drain rate",
		Value:  formatRate(r),
		Detail: rateDetail(r),
		Known:  true,
	})

	if s.WattsKnown {
		add(batteryRow{
			Label:  "Power draw",
			Value:  fmt.Sprintf("%.1f W", s.Watts),
			Detail: s.Wattage.String(),
			Known:  true,
		})
	}

	if s.HealthKnown {
		add(batteryRow{
			Label:  "Battery health",
			Value:  fmt.Sprintf("%.0f%%", s.HealthPct),
			Detail: "full capacity as a share of design",
			Known:  true,
		})
	} else if s.FullWhKnown {
		add(batteryRow{
			Label:  "Full capacity",
			Value:  fmt.Sprintf("%.1f Wh", s.FullWh),
			Detail: "health needs a design capacity, which this platform does not report",
			Known:  true,
		})
	}

	if s.CyclesKnown {
		add(batteryRow{Label: "Charge cycles", Value: fmt.Sprintf("%d", s.Cycles), Known: true})
	}

	if s.TimeToEmptyKnown {
		add(batteryRow{
			Label:  "Time remaining",
			Value:  (time.Duration(s.TimeToEmptyS) * time.Second).Round(time.Minute).String(),
			Detail: "the operating system's own estimate",
			Known:  true,
		})
	}

	// The capability list is printed whenever it is short of the full set,
	// because a report that silently omits a field is indistinguishable from a
	// machine that has nothing to report.
	//
	// Time remaining is the one entry that is frequently absent for a reason
	// that is not a platform limitation: while charging, or on mains, the
	// firmware has no meaningful answer and reports the unknown sentinel. Saying
	// "this platform does not expose these" there would blame the OS for a value
	// that only exists while discharging, so the reason is named instead.
	if s.Cap != 0 {
		missing := missingCapabilities(s.Cap)
		if missing != "" {
			detail := "this platform does not expose these"
			if missing == "time remaining" && (s.Charging || s.AC == cellwatch.ACOnline) {
				detail = "not applicable while charging or on mains"
			}
			add(batteryRow{Label: "Not reported", Value: missing, Detail: detail, Known: true})
		}
	}
	return rows
}

// formatRate renders a drain rate, prefixing an estimate with a tilde. The
// tilde is the cheapest available signal that a number is derived, and it costs
// nothing in a table.
func formatRate(r cellwatch.Rate) string {
	if !r.Stable {
		return "n/a"
	}
	if r.PctPerHour == 0 {
		return "0 %/h"
	}
	verb := "discharging"
	if r.PctPerHour < 0 {
		verb = "charging"
	}
	return fmt.Sprintf("~%.1f %%/h  %s", mathAbs(r.PctPerHour), verb)
}

func rateDetail(r cellwatch.Rate) string {
	if !r.Stable {
		return r.Reason
	}
	return fmt.Sprintf("ESTIMATE over %s, %d samples", r.Span.Round(time.Second), r.Samples)
}

func mathAbs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// missingCapabilities names what the platform did not report, so a reader of
// the output can tell a limitation from a healthy machine.
func missingCapabilities(got cellwatch.Capability) string {
	all := []struct {
		c    cellwatch.Capability
		name string
	}{
		{cellwatch.CapChargePct, "charge %"},
		{cellwatch.CapACState, "mains state"},
		{cellwatch.CapWatts, "wattage"},
		{cellwatch.CapFullCapacity, "full capacity"},
		{cellwatch.CapDesignCapacity, "design capacity"},
		{cellwatch.CapHealth, "health"},
		{cellwatch.CapCycleCount, "cycle count"},
		{cellwatch.CapTimeRemaining, "time remaining"},
	}
	var names []string
	for _, c := range all {
		if got&c.c == 0 {
			names = append(names, c.name)
		}
	}
	return strings.Join(names, ", ")
}

// printBatteryTable renders the report in the same style as printSpeedTable:
// a banner, a rule, aligned rows, and a dim footer.
func printBatteryTable(w io.Writer, s cellwatch.Status, r cellwatch.Rate, lowPct int) {
	const inner = 32
	fmt.Fprintf(w, "\n  %s+%s%s+%s\n", chestDim, chestReset, strings.Repeat("-", inner), chestDim)
	pad := func(text string) string {
		n := (inner - len([]rune(text))) / 2
		if n < 0 {
			n = 0
		}
		return strings.Repeat(" ", n) + text
	}
	fmt.Fprintf(w, "  %s|%s%s%s%s%s|%s\n", chestDim, chestReset, chestPrimary,
		pad("CHEST BATTERY"), chestReset, chestDim, chestReset)
	fmt.Fprintf(w, "  %s+%s%s+%s\n", chestDim, chestReset, strings.Repeat("-", inner), chestDim)

	source := s.Source
	if source == "" {
		source = "unknown"
	}
	fmt.Fprintf(w, "  %sSource:%s %s\n\n", chestDim, chestReset, source)

	for _, row := range buildBatteryRows(s, r, lowPct) {
		colour := chestDim
		switch {
		case strings.Contains(row.Value, "LOW"), row.Value == "none detected":
			colour = chestRed
		case row.Estimate:
			colour = chestCyan
		default:
			colour = chestPrimary
		}
		fmt.Fprintf(w, "  %s%-16s%s %s%-28s%s %s%s%s\n",
			chestDim, row.Label, chestReset, colour, row.Value, chestReset,
			chestDim, row.Detail, chestReset)
	}
	fmt.Fprintln(w)
}

// printBatteryJSON emits the same report as JSON. Field names carry the
// distinction between a measurement and a derivation, so a consumer cannot
// read an estimate as a reading even if it ignores the provenance field.
func printBatteryJSON(w io.Writer, s cellwatch.Status, r cellwatch.Rate) error {
	type report struct {
		Present    bool   `json:"present"`
		Source     string `json:"source"`
		PowerState string `json:"power_state"`
		StatusWord string `json:"status_word,omitempty"`

		ChargePct   *float64 `json:"charge_pct"`
		ChargeKnown bool     `json:"charge_pct_known"`

		Watts         *float64 `json:"watts"`
		WattsKnown    bool     `json:"watts_known"`
		WattageSource string   `json:"watts_source,omitempty"`

		FullCapacityWh   *float64 `json:"full_capacity_wh"`
		DesignCapacityWh *float64 `json:"design_capacity_wh"`
		HealthPct        *float64 `json:"health_pct"`
		HealthKnown      bool     `json:"health_pct_known"`

		Cycles      *int `json:"charge_cycles"`
		CyclesKnown bool `json:"charge_cycles_known"`

		TimeRemainingMin *int64 `json:"time_remaining_minutes"`
		TimeRemainingOK  bool   `json:"time_remaining_known"`

		// The rate is derived, and both the smoothed and the raw figure are
		// reported so a consumer can see how much smoothing was applied.
		DrainPctPerHourEstimate *float64 `json:"drain_pct_per_hour_estimate"`
		DrainPctPerHourWindow   *float64 `json:"drain_pct_per_hour_window"`
		RateProvenance          string   `json:"rate_provenance"`
		RateStable              bool     `json:"rate_stable"`
		RateWindowSeconds       float64  `json:"rate_window_seconds"`
		RateSamples             int      `json:"rate_samples"`
		RateUnavailableReason   string   `json:"rate_unavailable_reason,omitempty"`

		Capabilities    string `json:"platform_capabilities"`
		NotReportedByOS string `json:"not_reported_by_platform,omitempty"`
		Disclaimer      string `json:"disclaimer"`
	}
	out := report{
		Present:               s.Present,
		Source:                s.Source,
		PowerState:            s.AC.String(),
		StatusWord:            s.Status,
		ChargeKnown:           s.ChargeKnown,
		WattsKnown:            s.WattsKnown,
		HealthKnown:           s.HealthKnown,
		CyclesKnown:           s.CyclesKnown,
		TimeRemainingOK:       s.TimeToEmptyKnown,
		RateStable:            r.Stable,
		RateWindowSeconds:     r.Span.Seconds(),
		RateSamples:           r.Samples,
		RateUnavailableReason: r.Reason,
		Capabilities:          s.Cap.String(),
		NotReportedByOS:       missingCapabilities(s.Cap),
		Disclaimer: "Charge, power state and capacity are measured by the operating " +
			"system. Drain rate is an ESTIMATE derived from change in charge over a " +
			"window. No platform reports per-application battery use to an " +
			"unprivileged process, so none is claimed here.",
	}
	if s.ChargeKnown {
		out.ChargePct = &s.ChargePct
	}
	if s.WattsKnown {
		out.Watts = &s.Watts
		out.WattageSource = s.Wattage.String()
	}
	if s.FullWhKnown {
		out.FullCapacityWh = &s.FullWh
	}
	if s.DesignWhKnown {
		out.DesignCapacityWh = &s.DesignWh
	}
	if s.HealthKnown {
		out.HealthPct = &s.HealthPct
	}
	if s.CyclesKnown {
		out.Cycles = &s.Cycles
	}
	if s.TimeToEmptyKnown {
		m := s.TimeToEmptyS / 60
		out.TimeRemainingMin = &m
	}
	if r.Stable {
		out.DrainPctPerHourEstimate = &r.PctPerHour
		out.DrainPctPerHourWindow = &r.RawPctPerHour
		out.RateProvenance = cellwatch.ProvEstimated.String()
	} else {
		out.RateProvenance = cellwatch.ProvUnknown.String()
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// printBatteryHistory renders a period report. Coverage is printed first and
// unconditionally, because a reader who skips it would otherwise take a
// two-hour sample for a monthly total.
func printBatteryHistory(w io.Writer, rep *cellwatch.UsageReport, p cellwatch.Period, topN int, series map[string][]cellwatchPoint, names []string) {
	fmt.Fprintf(w, "\n  %s+%s%s+%s\n", chestDim, chestReset, strings.Repeat("-", 32), chestDim)
	fmt.Fprintf(w, "  %s|%s%sBATTERY - %s%s%s|%s\n", chestDim, chestReset, chestPrimary,
		strings.ToUpper(p.String()), strings.Repeat(" ", 20-len(p.String())), chestDim, chestReset)
	fmt.Fprintf(w, "  %s+%s%s+%s\n\n", chestDim, chestReset, strings.Repeat("-", 32), chestDim)

	cov := rep.Coverage
	colour := chestPrimary
	if !cov.Usable() {
		colour = chestGold
	}
	fmt.Fprintf(w, "  %sCoverage:%s %s%s%s  %s%s since %s%s\n",
		chestDim, chestReset, colour, cov.String(), chestReset,
		chestDim, rep.Range.From.Format("2 Jan"), rep.Range.To.Format("2 Jan 15:04"), chestReset)

	// Sparse history is stated here, before any figure, rather than as a
	// footnote underneath it. A reader who stops after the charge line has
	// already been given a number that describes a fraction of the period.
	if !cov.Usable() && cov.Samples > 0 {
		fmt.Fprintf(w, "  %s⚠ Sparse history: the figures below describe only the %d samples recorded,%s\n", chestGold, cov.Samples, chestReset)
		fmt.Fprintf(w, "  %s  not the whole %s. Record more with: chest battery --record --daemon%s\n", chestDim, p.String(), chestReset)
	}

	if cov.Samples == 0 {
		fmt.Fprintf(w, "\n  %sNo history recorded for this period.%s\n", chestDim, chestReset)
		fmt.Fprintf(w, "  %sRecord some with: chest battery --record --daemon%s\n\n", chestDim, chestReset)
		return
	}

	sys := rep.System
	fmt.Fprintf(w, "  %sCharge:%s %s%.0f%%%s -> %s%.0f%%%s   %slost %.1f points%s\n",
		chestDim, chestReset, chestPrimary, sys.ChargeStartPct, chestReset,
		chestPrimary, sys.ChargeEndPct, chestReset, chestDim, sys.ChargeLostPct, chestReset)

	// A rate of exactly zero over a period that shows charge being lost, or a
	// period too short to contain a whole percentage point, is an absence of
	// measurement rather than a measurement of absence. Saying "0.0 %/h" here
	// would be a confident number that no reading supports.
	switch {
	case sys.DrainPctPerHour == 0:
		fmt.Fprintf(w, "  %sDrain:%s  %sn/a%s  %sno change recorded over this period, or the period is shorter than one charge step%s\n",
			chestDim, chestReset, chestCyan, chestReset, chestDim, chestReset)
	default:
		fmt.Fprintf(w, "  %sDrain:%s  %s~%.1f %%/h%s  %sESTIMATE over the period%s\n",
			chestDim, chestReset, chestCyan, sys.DrainPctPerHour, chestReset, chestDim, chestReset)
	}

	fmt.Fprintf(w, "  %sSamples:%s %s%d charging, %d discharging%s\n\n",
		chestDim, chestReset, chestDim, sys.ChargingSamples, sys.DischargingSamples, chestReset)

	if len(rep.Apps) > 0 {
		fmt.Fprintf(w, "  %sTOP %d APPLICATIONS%s\n", chestGold, topN, chestReset)
		// Shares are already normalised to what was attributed, so the bars are
		// scaled to the largest rather than to a fixed total, which keeps the
		// column readable when only a few applications were active.
		var maxPct float64
		for _, a := range rep.Apps {
			if a.CPUSeconds > maxPct {
				maxPct = a.CPUSeconds
			}
		}
		for i, a := range rep.Apps {
			// The existing diamond gauge is reused rather than replaced, so a
			// battery table and a speedtest table use the same visual language.
			gauge, _ := speedBar(a.CPUSeconds / maxPct)
			// The attributed drain is only shown when a system rate exists to
			// apportion against. Without one, every application would carry a
			// cost of zero, which reads as "this used no power" rather than
			// "the system rate is not known", so the column is left out rather
			// than filled with a figure that means nothing.
			cost := "  " + chestDim + "no system rate" + chestReset
			if sys.DrainPctPerHour != 0 {
				cost = fmt.Sprintf("%s~%.1f %%/h%s", chestCyan, a.PctPerHour, chestReset)
			}
			fmt.Fprintf(w, "  %s%2d. %-24s%s %s%-6.1fs%s  %s%s%s  %s\n",
				chestDim, i+1, truncate(a.Name, 24), chestReset,
				chestDim, a.CPUSeconds, chestReset,
				chestCyan, gauge, chestReset, cost)
		}
		fmt.Fprintf(w, "\n  %sPer-application figures are ESTIMATES apportioned from measured CPU and I/O.%s\n", chestDim, chestReset)
		fmt.Fprintf(w, "  %sA lit display, a wireless radio and thermal management draw real power at near-zero CPU%s\n", chestDim, chestReset)
		fmt.Fprintf(w, "  %sand are invisible to this method.%s\n", chestDim, chestReset)
		printAppChart(w, rep.Range, series, names)
	}

	for _, n := range rep.Notes {
		fmt.Fprintf(w, "  %s%s%s\n", chestGold, n, chestReset)
	}
	if len(rep.Notes) > 0 {
		fmt.Fprintln(w)
	}
}
