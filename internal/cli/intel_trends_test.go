package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/indexer"
	"github.com/spf13/cobra"
)

// --- flags -------------------------------------------------------------------

func TestTrendFlagsRegistered(t *testing.T) {
	cmd := newStatsCmd()
	for _, name := range []string{"trend", "day", "week", "month"} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("stats is missing --%s flag", name)
		}
		if flag.DefValue != "false" {
			t.Errorf("--%s default = %q, want false", name, flag.DefValue)
		}
	}
}

func TestIndexWatchFlagsRegistered(t *testing.T) {
	cmd := newIndexCmd()

	watch := cmd.Flags().Lookup("watch")
	if watch == nil {
		t.Fatal("index is missing --watch flag")
	}
	if watch.DefValue != "false" {
		t.Errorf("--watch default = %q, want false", watch.DefValue)
	}

	debounce := cmd.Flags().Lookup("debounce")
	if debounce == nil {
		t.Fatal("index is missing --debounce flag")
	}
	if debounce.DefValue != "500ms" {
		t.Errorf("--debounce default = %q, want 500ms", debounce.DefValue)
	}
}

// --- period resolution -------------------------------------------------------

func TestResolveTrendPeriod(t *testing.T) {
	tests := []struct {
		name                   string
		trend, day, week, mont bool
		want                   indexer.TrendPeriod
		wantErr                string
	}{
		{name: "no trend flags", want: indexer.DefaultTrendPeriod},
		{name: "trend only defaults to month", trend: true, want: indexer.TrendPeriodMonth},
		{name: "explicit month", trend: true, mont: true, want: indexer.TrendPeriodMonth},
		{name: "day", trend: true, day: true, want: indexer.TrendPeriodDay},
		{name: "week", trend: true, week: true, want: indexer.TrendPeriodWeek},
		{name: "day without trend", day: true, wantErr: "--day requires --trend"},
		{name: "week without trend", week: true, wantErr: "--week requires --trend"},
		{name: "month without trend", mont: true, wantErr: "--month requires --trend"},
		{name: "day and week", trend: true, day: true, week: true, wantErr: "choose one of --day, --week"},
		{name: "day and month", trend: true, day: true, mont: true, wantErr: "choose one of --day, --month"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTrendPeriod(tt.day, tt.week, tt.mont, tt.trend)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("period = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- report rendering --------------------------------------------------------

func TestCsvRowsForTrends(t *testing.T) {
	report := indexer.StorageTrend{
		TotalFilesThen: 5, TotalFilesNow: 7, TotalFilesDelta: 2,
		TotalSizeThen: 1000, TotalSizeNow: 1500, TotalSizeDelta: 500,
		Categories: []indexer.TrendCategoryDelta{
			{Category: "Document", FilesThen: 2, FilesNow: 3, FilesDelta: 1, SizeThen: 300, SizeNow: 500, SizeDelta: 200},
			{Category: "Image", FilesThen: 3, FilesNow: 4, FilesDelta: 1, SizeThen: 700, SizeNow: 1000, SizeDelta: 300},
		},
	}
	want := [][]string{
		{"category", "files_then", "files_now", "files_delta", "size_then_bytes", "size_now_bytes", "size_delta_bytes"},
		{"Document", "2", "3", "1", "300", "500", "200"},
		{"Image", "3", "4", "1", "700", "1000", "300"},
		{"TOTAL", "5", "7", "2", "1000", "1500", "500"},
	}
	if got := csvRowsForTrends(report); !reflect.DeepEqual(got, want) {
		t.Fatalf("csvRowsForTrends mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestSignedRendering(t *testing.T) {
	if got := signedSize(0); got != "0 B" {
		t.Errorf("signedSize(0) = %q, want %q", got, "0 B")
	}
	if got := signedSize(2048); !strings.HasPrefix(got, "+") {
		t.Errorf("signedSize(2048) = %q, want a leading +", got)
	}
	if got := signedSize(-2048); !strings.HasPrefix(got, "-") {
		t.Errorf("signedSize(-2048) = %q, want a leading -", got)
	}
	if got := signedCount(3); got != "+3" {
		t.Errorf("signedCount(3) = %q, want +3", got)
	}
	if got := signedCount(-3); got != "-3" {
		t.Errorf("signedCount(-3) = %q, want -3", got)
	}
	if got := signedCount(0); got != "0" {
		t.Errorf("signedCount(0) = %q, want 0", got)
	}
}

func TestPrintTrendsGrowth(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.Local)
	report := indexer.StorageTrend{
		Scope:           filepath.Join("C:", "photos"),
		Period:          indexer.TrendPeriodWeek,
		WindowDays:      7,
		Since:           now.AddDate(0, 0, -7),
		Now:             &indexer.StorageSnapshot{CapturedAt: now, TotalSize: 1500, TotalFiles: 7},
		Then:            &indexer.StorageSnapshot{CapturedAt: now.AddDate(0, 0, -7), TotalSize: 1000, TotalFiles: 5},
		HasBaseline:     true,
		Snapshots:       4,
		TotalFilesThen:  5,
		TotalFilesNow:   7,
		TotalFilesDelta: 2,
		TotalSizeThen:   1000,
		TotalSizeNow:    1500,
		TotalSizeDelta:  500,
		TotalGrowthPct:  50,
		Categories: []indexer.TrendCategoryDelta{
			{Category: "Image", FilesNow: 7, FilesThen: 5, FilesDelta: 2, SizeNow: 1500, SizeThen: 1000, SizeDelta: 500},
		},
	}

	var buf bytes.Buffer
	printTrends(&buf, report)
	out := buf.String()

	for _, want := range []string{"CHEST STORAGE TREND", "week", "Snapshots recorded:", "4", "CATEGORY GROWTH", "IMAGE"} {
		if !strings.Contains(out, want) {
			t.Errorf("trend report is missing %q\n%s", want, out)
		}
	}
	if !strings.Contains(out, "+2 files") {
		t.Errorf("trend report should show the signed file delta:\n%s", out)
	}
}

func TestPrintTrendsWithoutBaseline(t *testing.T) {
	report := indexer.StorageTrend{
		Period:    indexer.TrendPeriodDay,
		Now:       &indexer.StorageSnapshot{TotalFiles: 3},
		Snapshots: 1,
	}
	var buf bytes.Buffer
	printTrends(&buf, report)
	if !strings.Contains(buf.String(), "Not enough history yet") {
		t.Fatalf("a first-run trend must say there is no history yet:\n%s", buf.String())
	}
}

// --- regression: nothing changes without the new flags -----------------------

// TestIntelCommandsKeepExistingFlags pins the flag surface of the four
// intelligence commands: every pre-existing flag keeps its name, default and
// shorthand. This is the guard against a "harmless" addition silently
// repurposing an existing switch.
func TestIntelCommandsKeepExistingFlags(t *testing.T) {
	tests := []struct {
		cmd  func() *cobra.Command
		name string
		want map[string]string // flag name -> "shorthand|default"
	}{
		{
			cmd:  newIndexCmd,
			name: "index",
			want: map[string]string{
				"hash":   "|false",
				"clear":  "|false",
				"except": "|[]",
			},
		},
		{
			cmd:  newStatsCmd,
			name: "stats",
			want: map[string]string{
				"except":  "|[]",
				"refresh": "|false",
				"json":    "|false",
				"csv":     "|false",
				"out":     "|",
			},
		},
		{
			cmd:  newDuplicatesCmd,
			name: "duplicates",
			want: map[string]string{
				"except": "|[]",
				"json":   "|false",
				"csv":    "|false",
				"out":    "|",
			},
		},
		{
			cmd:  newAnalyzeCmd,
			name: "analyze",
			want: map[string]string{
				"except":  "|[]",
				"refresh": "|false",
				"json":    "|false",
				"csv":     "|false",
				"out":     "|",
			},
		},
		{
			cmd:  newCleanCmd,
			name: "clean",
			want: map[string]string{
				"all":     "|false",
				"history": "|false",
				"battery": "|false",
				"yes":     "y|false",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.cmd()
			for name, want := range tt.want {
				flag := cmd.Flags().Lookup(name)
				if flag == nil {
					t.Errorf("%s lost its --%s flag", tt.name, name)
					continue
				}
				got := flag.Shorthand + "|" + flag.DefValue
				if got != want {
					t.Errorf("%s --%s = %q, want %q", tt.name, name, got, want)
				}
			}
		})
	}
}

// TestIntelReportPayloadsUnchanged proves the report helpers still produce the
// exact bytes they did before the new flags existed.
func TestIntelReportPayloadsUnchanged(t *testing.T) {
	summary := indexer.StatsSummary{
		TotalFiles:     5,
		TotalSize:      1000,
		CategoryCounts: map[string]int{"images": 3, "code": 2},
		CategorySizes:  map[string]int64{"images": 700, "code": 300},
	}
	wantStats := [][]string{
		{"category", "files", "size_bytes"},
		{"code", "2", "300"},
		{"images", "3", "700"},
		{"TOTAL", "5", "1000"},
	}
	if got := csvRowsForStats(summary); !reflect.DeepEqual(got, wantStats) {
		t.Fatalf("csvRowsForStats changed:\n got %v\nwant %v", got, wantStats)
	}

	dups := []indexer.DuplicateGroup{
		{Hash: "abc", Size: 10, Files: []indexer.IndexedFile{{Path: "/x/a"}, {Path: "/x/b"}}},
	}
	wantDups := [][]string{
		{"hash", "size_bytes", "path"},
		{"abc", "10", "/x/a"},
		{"abc", "10", "/x/b"},
	}
	if got := csvRowsForDuplicates(dups); !reflect.DeepEqual(got, wantDups) {
		t.Fatalf("csvRowsForDuplicates changed:\n got %v\nwant %v", got, wantDups)
	}
}

// TestIndexWatchReindexesContinuously drives the whole watch loop: it indexes
// the folder, picks up a file created afterwards, drops the row for a deleted
// file, and shuts down cleanly when its context is cancelled.
func TestIndexWatchReindexesContinuously(t *testing.T) {
	if runtime.GOOS == "windows" && testing.Short() {
		t.Skip("filesystem notification timing is slow on Windows")
	}

	// The database lives outside t.TempDir on purpose: SQLite leaves -wal and
	// -shm files behind on Windows, and a t.TempDir cleanup that trips over
	// them would report a failure that has nothing to do with this test.
	dbDir, err := os.MkdirTemp("", "chest-watch-test-")
	if err != nil {
		t.Fatalf("create db dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dbDir) })

	store, err := indexer.OpenOrCreate(filepath.Join(dbDir, "index.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	root := t.TempDir()
	initial := filepath.Join(root, "initial.txt")
	if err := os.WriteFile(initial, []byte("one"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runIndexWatch(ctx, &out, store, root, false, nil, 25*time.Millisecond)
	}()

	// Wait for the initial pass to land before changing anything.
	waitForIndexed(t, store, root, 1)

	added := filepath.Join(root, "added.txt")
	if err := os.WriteFile(added, []byte("two"), 0o644); err != nil {
		t.Fatalf("add file: %v", err)
	}
	waitForIndexed(t, store, root, 2)

	// A deletion must purge the row, so the cache never describes a file that
	// is no longer there.
	if err := os.Remove(initial); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	waitForIndexed(t, store, root, 1)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runIndexWatch returned %v, want a clean exit", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runIndexWatch did not return after its context was cancelled")
	}

	if !strings.Contains(out.String(), "CHEST CONTINUOUS INDEXING") {
		t.Errorf("watch banner missing from output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Stopped continuous indexing") {
		t.Errorf("shutdown notice missing from output:\n%s", out.String())
	}
}

// waitForIndexed polls the index until it holds want rows under root.
func waitForIndexed(t *testing.T, store *indexer.Store, root string, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		summary, err := store.GetStats(root)
		if err == nil && summary.TotalFiles == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	summary, err := store.GetStats(root)
	t.Fatalf("index never reached %d file(s) under %s (got %d, err=%v)", want, root, summary.TotalFiles, err)
}

// TestIndexWatchRequiresDirectory: watch mode must refuse a path it cannot
// watch rather than silently doing nothing.
func TestIndexWatchRequiresDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	err := validateWatchTarget(file)
	if err == nil {
		t.Fatal("watching a plain file must fail")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error = %v, want it to explain the path is not a directory", err)
	}

	if err := validateWatchTarget(filepath.Join(dir, "gone")); err == nil {
		t.Fatal("watching a missing directory must fail")
	}

	if err := validateWatchTarget(dir); err != nil {
		t.Fatalf("a real directory must be accepted: %v", err)
	}
}
