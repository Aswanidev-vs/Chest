package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/indexer"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/spf13/cobra"
)

type sampleReport struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

func TestRenderReportJSON(t *testing.T) {
	got, err := renderReport(sampleReport{Name: "hi", N: 3}, nil, true)
	if err != nil {
		t.Fatalf("renderReport json: %v", err)
	}
	want := "{\n  \"name\": \"hi\",\n  \"n\": 3\n}\n"
	if string(got) != want {
		t.Fatalf("json mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestRenderReportCSV(t *testing.T) {
	rows := [][]string{{"a", "b"}, {"1", "2"}}
	got, err := renderReport(nil, rows, false)
	if err != nil {
		t.Fatalf("renderReport csv: %v", err)
	}
	if string(got) != "a,b\n1,2\n" {
		t.Fatalf("csv mismatch: %q", got)
	}
}

func TestEmitReportStdout(t *testing.T) {
	var buf bytes.Buffer
	if err := emitReport(&buf, sampleReport{Name: "hi", N: 3}, nil, true, false, ""); err != nil {
		t.Fatalf("emitReport: %v", err)
	}
	if buf.String() != "{\n  \"name\": \"hi\",\n  \"n\": 3\n}\n" {
		t.Fatalf("stdout json mismatch: %q", buf.String())
	}
}

func TestEmitReportCSVStdout(t *testing.T) {
	var buf bytes.Buffer
	rows := [][]string{{"a", "b"}, {"1", "2"}}
	if err := emitReport(&buf, nil, rows, false, true, ""); err != nil {
		t.Fatalf("emitReport: %v", err)
	}
	if buf.String() != "a,b\n1,2\n" {
		t.Fatalf("stdout csv mismatch: %q", buf.String())
	}
}

func TestEmitReportToFile(t *testing.T) {
	csvRows := [][]string{{"hash", "size_bytes", "path"}, {"abc", "10", "/x"}}

	tests := []struct {
		name        string
		asJSON      bool
		asCSV       bool
		wantInFile  string
		notInResult string
	}{
		{"json-to-file", true, false, "{\n  \"name\": \"hi\"", ""},
		{"csv-to-file", false, true, "hash,size_bytes,path\nabc,10,/x\n", "{\n"},
		// --out alone (neither --json nor --csv) must default to JSON.
		{"out-defaults-json", false, false, "{\n  \"name\": \"hi\"", "hash,size_bytes,path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "report.out")
			var buf bytes.Buffer
			if err := emitReport(&buf, sampleReport{Name: "hi", N: 3}, csvRows, tt.asJSON, tt.asCSV, out); err != nil {
				t.Fatalf("emitReport: %v", err)
			}

			// The file holds only the format content; the confirmation is printed
			// to w instead and must not leak the content.
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("read back file: %v", err)
			}
			if !strings.HasPrefix(string(data), tt.wantInFile) {
				t.Fatalf("file content = %q, want prefix %q", data, tt.wantInFile)
			}
			if tt.notInResult != "" && strings.Contains(string(data), tt.notInResult) {
				t.Fatalf("file unexpectedly contains %q", tt.notInResult)
			}

			confirmation := "wrote " + itoa(len(data)) + " bytes to " + out + "\n"
			if buf.String() != confirmation {
				t.Fatalf("confirmation mismatch:\n got %q\nwant %q", buf.String(), confirmation)
			}
		})
	}
}

func TestCsvRowsForStats(t *testing.T) {
	summary := indexer.StatsSummary{
		TotalFiles:     5,
		TotalSize:      1000,
		CategoryCounts: map[string]int{"images": 3, "code": 2},
		CategorySizes:  map[string]int64{"images": 700, "code": 300},
	}
	// Categories are emitted in sorted order regardless of Go's random map order.
	want := [][]string{
		{"category", "files", "size_bytes"},
		{"code", "2", "300"},
		{"images", "3", "700"},
		{"TOTAL", "5", "1000"},
	}
	if got := csvRowsForStats(summary); !reflect.DeepEqual(got, want) {
		t.Fatalf("csvRowsForStats mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestCsvRowsForDuplicates(t *testing.T) {
	groups := []indexer.DuplicateGroup{
		{Hash: "abc", Size: 10, Files: []indexer.IndexedFile{{Path: "/x/a"}, {Path: "/x/b"}}},
		{Hash: "def", Size: 20, Files: []indexer.IndexedFile{{Path: "/y/c"}, {Path: "/y/d"}}},
	}
	want := [][]string{
		{"hash", "size_bytes", "path"},
		{"abc", "10", "/x/a"},
		{"abc", "10", "/x/b"},
		{"def", "20", "/y/c"},
		{"def", "20", "/y/d"},
	}
	if got := csvRowsForDuplicates(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("csvRowsForDuplicates mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestCsvRowsForHistory(t *testing.T) {
	entries := []models.HistoryEntry{
		{ID: 1, Timestamp: time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC), FilesCount: 3, Status: "Complete", Directory: "dir1"},
		{ID: 2, Timestamp: time.Date(2026, 2, 3, 9, 30, 0, 0, time.UTC), FilesCount: 5, Status: "Undone", Directory: "dir2"},
	}
	// Newest first (reverse of the stored ascending order), matching the human table.
	want := [][]string{
		{"id", "timestamp", "files_count", "status", "directory"},
		{"2", "2026-02-03 09:30", "5", "Undone", "dir2"},
		{"1", "2026-01-02 15:04", "3", "Complete", "dir1"},
	}
	if got := csvRowsForHistory(entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("csvRowsForHistory mismatch:\n got %v\nwant %v", got, want)
	}
}

// TestExportFlagsRejectJSONAndCSV covers the --json/--csv combination error.
// The validation runs before any index is opened, so this is hermetic.
func TestExportFlagsRejectJSONAndCSV(t *testing.T) {
	tests := []struct {
		name string
		cmd  *cobra.Command
	}{
		{"stats", newStatsCmd()},
		{"duplicates", newDuplicatesCmd()},
		{"analyze", newAnalyzeCmd()},
		{"history", newHistoryCmd()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.cmd.SetOut(&buf)
			tt.cmd.SetErr(&buf)
			tt.cmd.SetArgs([]string{"--json", "--csv"})
			err := tt.cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "choose one of --json or --csv") {
				t.Fatalf("Execute() error = %v, want error containing %q", err, "choose one of --json or --csv")
			}
		})
	}
}

// TestExportFlagsRegistered verifies each report command exposes the three
// export flags without running any report logic.
func TestExportFlagsRegistered(t *testing.T) {
	for _, mk := range []func() *cobra.Command{newStatsCmd, newDuplicatesCmd, newAnalyzeCmd, newHistoryCmd} {
		cmd := mk()
		for _, name := range []string{"json", "csv", "out"} {
			if cmd.Flags().Lookup(name) == nil {
				t.Errorf("%s is missing --%s flag", cmd.Name(), name)
			}
		}
	}
}

// itoa is a tiny local helper so the test doesn't import strconv for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
