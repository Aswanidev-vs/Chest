package indexer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// TrendPeriod selects the look-back window used when reporting growth. Each
// value is a fixed window rather than a calendar bucket so that "month" means
// "the last 30 days of snapshots" regardless of when the report is run.
type TrendPeriod string

// Supported trend periods.
const (
	TrendPeriodDay   TrendPeriod = "day"
	TrendPeriodWeek  TrendPeriod = "week"
	TrendPeriodMonth TrendPeriod = "month"
)

// DefaultTrendPeriod is used when the caller does not pick one.
const DefaultTrendPeriod = TrendPeriodMonth

// maxTrendSnapshots bounds how many snapshots are retained per scope. Trends
// are a rolling window, not an archive: without a cap the table would grow
// forever on a machine that runs `chest stats` often.
const maxTrendSnapshots = 500

// ParseTrendPeriod validates a period name and returns the matching
// TrendPeriod. An empty name yields DefaultTrendPeriod.
func ParseTrendPeriod(name string) (TrendPeriod, error) {
	switch TrendPeriod(strings.ToLower(strings.TrimSpace(name))) {
	case "":
		return DefaultTrendPeriod, nil
	case TrendPeriodDay:
		return TrendPeriodDay, nil
	case TrendPeriodWeek:
		return TrendPeriodWeek, nil
	case TrendPeriodMonth:
		return TrendPeriodMonth, nil
	default:
		return "", fmt.Errorf("invalid trend period '%s'. Valid: day, week, month", name)
	}
}

// Duration returns the look-back window length for the period.
func (p TrendPeriod) Duration() time.Duration {
	switch p {
	case TrendPeriodDay:
		return 24 * time.Hour
	case TrendPeriodWeek:
		return 7 * 24 * time.Hour
	case TrendPeriodMonth:
		return 30 * 24 * time.Hour
	default:
		return DefaultTrendPeriod.Duration()
	}
}

// StorageSnapshot is one captured point in the storage history of a scope
// ("" = the whole index, otherwise a single indexed folder subtree).
type StorageSnapshot struct {
	ID             int              `json:"id"`
	CapturedAt     time.Time        `json:"captured_at"`
	Scope          string           `json:"scope"`
	TotalFiles     int              `json:"total_files"`
	TotalSize      int64            `json:"total_size"`
	CategoryCounts map[string]int   `json:"category_counts"`
	CategorySizes  map[string]int64 `json:"category_sizes"`
}

// TrendCategoryDelta is the growth of a single category between two snapshots.
type TrendCategoryDelta struct {
	Category   string `json:"category"`
	FilesThen  int    `json:"files_then"`
	FilesNow   int    `json:"files_now"`
	FilesDelta int    `json:"files_delta"`
	SizeThen   int64  `json:"size_then_bytes"`
	SizeNow    int64  `json:"size_now_bytes"`
	SizeDelta  int64  `json:"size_delta_bytes"`
}

// StorageTrend compares the newest recorded snapshot against the one closest
// to (but not after) the start of the requested window.
type StorageTrend struct {
	Scope      string           `json:"scope"`
	Period     TrendPeriod      `json:"period"`
	WindowDays int              `json:"window_days"`
	Since      time.Time        `json:"since"`
	Then       *StorageSnapshot `json:"then"`
	Now        *StorageSnapshot `json:"now"`
	// HasBaseline reports whether two distinct snapshots could be compared.
	// A brand-new database has only the snapshot taken by this very run, so
	// there is nothing to grow from yet.
	HasBaseline     bool                 `json:"has_baseline"`
	Snapshots       int                  `json:"snapshots"`
	TotalFilesThen  int                  `json:"total_files_then"`
	TotalFilesNow   int                  `json:"total_files_now"`
	TotalFilesDelta int                  `json:"total_files_delta"`
	TotalSizeThen   int64                `json:"total_size_then_bytes"`
	TotalSizeNow    int64                `json:"total_size_now_bytes"`
	TotalSizeDelta  int64                `json:"total_size_delta_bytes"`
	TotalGrowthPct  float64              `json:"total_growth_pct"`
	Categories      []TrendCategoryDelta `json:"categories"`
}

// RecordStorageSnapshot appends the current aggregate totals for scope to the
// storage history. It is additive only — no existing row or table is touched —
// so a failed or concurrent call can never damage the file index.
func (s *Store) RecordStorageSnapshot(scope string, summary StatsSummary) (StorageSnapshot, error) {
	counts := summary.CategoryCounts
	if counts == nil {
		counts = map[string]int{}
	}
	sizes := summary.CategorySizes
	if sizes == nil {
		sizes = map[string]int64{}
	}

	countsJSON, err := json.Marshal(counts)
	if err != nil {
		return StorageSnapshot{}, fmt.Errorf("failed encoding category counts: %w", err)
	}
	sizesJSON, err := json.Marshal(sizes)
	if err != nil {
		return StorageSnapshot{}, fmt.Errorf("failed encoding category sizes: %w", err)
	}

	now := time.Now()
	res, err := s.db.Exec(
		`INSERT INTO storage_snapshots (captured_at, scope, total_files, total_size, category_counts, category_sizes)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		now.Unix(), scope, summary.TotalFiles, summary.TotalSize, string(countsJSON), string(sizesJSON))
	if err != nil {
		return StorageSnapshot{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return StorageSnapshot{}, err
	}

	// Trim the oldest rows of this scope so the history stays bounded. This
	// only ever touches rows written by this same feature.
	if _, err := s.db.Exec(
		`DELETE FROM storage_snapshots
		 WHERE scope = ? AND id NOT IN (
		     SELECT id FROM storage_snapshots WHERE scope = ? ORDER BY id DESC LIMIT ?
		 )`, scope, scope, maxTrendSnapshots); err != nil {
		return StorageSnapshot{}, err
	}

	return StorageSnapshot{
		ID:             int(id),
		CapturedAt:     now,
		Scope:          scope,
		TotalFiles:     summary.TotalFiles,
		TotalSize:      summary.TotalSize,
		CategoryCounts: counts,
		CategorySizes:  sizes,
	}, nil
}

// listStorageSnapshots returns every retained snapshot for a scope in
// chronological order.
func (s *Store) listStorageSnapshots(scope string) ([]StorageSnapshot, error) {
	rows, err := s.db.Query(
		`SELECT id, captured_at, scope, total_files, total_size, category_counts, category_sizes
		 FROM storage_snapshots WHERE scope = ? ORDER BY captured_at ASC, id ASC`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []StorageSnapshot
	for rows.Next() {
		var (
			snap       StorageSnapshot
			capturedAt int64
			countsJSON string
			sizesJSON  string
		)
		if err := rows.Scan(&snap.ID, &capturedAt, &snap.Scope, &snap.TotalFiles, &snap.TotalSize, &countsJSON, &sizesJSON); err != nil {
			return nil, err
		}
		snap.CapturedAt = time.Unix(capturedAt, 0)
		snap.CategoryCounts = map[string]int{}
		snap.CategorySizes = map[string]int64{}
		if countsJSON != "" {
			_ = json.Unmarshal([]byte(countsJSON), &snap.CategoryCounts)
		}
		if sizesJSON != "" {
			_ = json.Unmarshal([]byte(sizesJSON), &snap.CategorySizes)
		}
		list = append(list, snap)
	}
	return list, rows.Err()
}

// StorageTrend reports how a scope grew over the given period. now is normally
// the capture time of the snapshot just recorded, so the newest snapshot acts
// as the "now" side of the comparison.
//
// The baseline is the newest snapshot taken at or before the window start. When
// no snapshot is old enough (a young database), the earliest retained snapshot
// is used instead so the first reports still show a real delta; when only one
// snapshot exists at all, HasBaseline is false and the deltas are zero.
func (s *Store) StorageTrend(scope string, period TrendPeriod, now time.Time) (StorageTrend, error) {
	if period == "" {
		period = DefaultTrendPeriod
	}
	if now.IsZero() {
		now = time.Now()
	}

	snaps, err := s.listStorageSnapshots(scope)
	if err != nil {
		return StorageTrend{}, err
	}

	trend := StorageTrend{
		Scope:      scope,
		Period:     period,
		WindowDays: int(period.Duration().Hours() / 24),
		Since:      now.Add(-period.Duration()),
		Snapshots:  len(snaps),
		Categories: []TrendCategoryDelta{},
	}
	if len(snaps) == 0 {
		return trend, nil
	}

	latest := snaps[len(snaps)-1]
	trend.Now = &latest
	trend.TotalFilesNow = latest.TotalFiles
	trend.TotalSizeNow = latest.TotalSize

	baseline := -1
	for i, snap := range snaps {
		if !snap.CapturedAt.After(trend.Since) {
			baseline = i
		}
	}
	if baseline < 0 && len(snaps) > 1 {
		baseline = 0
	}
	if baseline < 0 || baseline == len(snaps)-1 {
		// Nothing older than "now" to compare against yet.
		return trend, nil
	}

	then := snaps[baseline]
	trend.Then = &then
	trend.HasBaseline = true
	trend.TotalFilesThen = then.TotalFiles
	trend.TotalSizeThen = then.TotalSize
	trend.TotalFilesDelta = latest.TotalFiles - then.TotalFiles
	trend.TotalSizeDelta = latest.TotalSize - then.TotalSize
	if then.TotalSize > 0 {
		trend.TotalGrowthPct = float64(trend.TotalSizeDelta) / float64(then.TotalSize) * 100
	}

	seen := make(map[string]struct{}, len(then.CategorySizes)+len(latest.CategorySizes))
	for cat := range then.CategorySizes {
		seen[cat] = struct{}{}
	}
	for cat := range latest.CategorySizes {
		seen[cat] = struct{}{}
	}
	cats := make([]string, 0, len(seen))
	for cat := range seen {
		if cat == "" {
			continue
		}
		cats = append(cats, cat)
	}
	sort.Strings(cats)

	for _, cat := range cats {
		trend.Categories = append(trend.Categories, TrendCategoryDelta{
			Category:   cat,
			FilesThen:  then.CategoryCounts[cat],
			FilesNow:   latest.CategoryCounts[cat],
			FilesDelta: latest.CategoryCounts[cat] - then.CategoryCounts[cat],
			SizeThen:   then.CategorySizes[cat],
			SizeNow:    latest.CategorySizes[cat],
			SizeDelta:  latest.CategorySizes[cat] - then.CategorySizes[cat],
		})
	}
	return trend, nil
}
