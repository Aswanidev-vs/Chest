package cli

import (
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

// runScheduleAdd runs "schedule add" with a captured stdout. --print-cron is
// always passed so the command only renders the crontab line, and the home-dir
// variables are redirected to a temp dir as defense in depth: without both,
// a regression here would register real scheduled tasks and write the
// developer's actual ~/.chest/schedules.toml.
func runScheduleAdd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	isolateHome(t)

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	cmd := newScheduleCmd()
	cmd.SetArgs(append([]string{"add", "--print-cron"}, args...))
	execErr := cmd.Execute()

	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), execErr
}

// isolateHome redirects os.UserHomeDir to a throwaway directory. USERPROFILE is
// what it consults on Windows, HOME everywhere else.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestScheduleAddPrintCron(t *testing.T) {
	dir := t.TempDir()

	out, err := runScheduleAdd(t, "--cron", "0 2 * * *", "--preset", "downloads", dir)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.HasPrefix(out, "0 2 * * * ") {
		t.Fatalf("output is not a crontab line: %q", out)
	}
	for _, want := range []string{"sort", dir, "--preset", "downloads", "-y"} {
		if !strings.Contains(out, want) {
			t.Errorf("crontab line missing %q: %q", want, out)
		}
	}
}

func TestScheduleAddPrintCronQuotesRic(t *testing.T) {
	dir := t.TempDir()

	// A rule with spaces and glob characters must arrive shell-quoted.
	out, err := runScheduleAdd(t, "--cron", "*/15 * * * *", "--rule", "*.tmp -> Temp", dir)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(out, "'*.tmp -> Temp'") {
		t.Errorf("rule not single-quoted: %q", out)
	}
}

func TestScheduleAddRejectsBadCron(t *testing.T) {
	dir := t.TempDir()

	_, err := runScheduleAdd(t, "--cron", "99 * * *", dir)
	if err == nil || !strings.Contains(err.Error(), "invalid cron") {
		t.Fatalf("Execute() error = %v, want invalid cron error", err)
	}

	_, err = runScheduleAdd(t, dir)
	if err == nil {
		t.Fatal("missing required --cron must error")
	}
}

func TestScheduleAddRejectsSystemDirectory(t *testing.T) {
	target := "/etc"
	if runtime.GOOS == "windows" {
		target = `C:\Windows`
	}
	_, err := runScheduleAdd(t, "--cron", "0 2 * * *", target)
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("Execute() error = %v, want access denied", err)
	}
}

func TestScheduleRemoveUnknownID(t *testing.T) {
	// A throwaway home dir means this can never read or mutate the developer's
	// real ~/.chest schedules.
	isolateHome(t)

	cmd := newScheduleCmd()
	cmd.SetArgs([]string{"remove", "does-not-exist"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "no schedule with id") {
		t.Fatalf("Execute() error = %v, want unknown id error", err)
	}
}
