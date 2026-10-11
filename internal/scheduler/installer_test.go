package scheduler

import (
	"errors"
	"strings"
	"testing"
)

// fakeInstaller records calls instead of touching the OS.
type fakeInstaller struct {
	installed   []Schedule
	binPaths    []string
	uninstalled []string
	failInstall bool
}

func (f *fakeInstaller) Install(s Schedule, binPath string) error {
	if f.failInstall {
		return errors.New("install failed")
	}
	f.installed = append(f.installed, s)
	f.binPaths = append(f.binPaths, binPath)
	return nil
}

func (f *fakeInstaller) Uninstall(id string) error {
	f.uninstalled = append(f.uninstalled, id)
	return nil
}

// Compile-time proof that all three implementers satisfy the interface.
var (
	_ OSInstaller = (*fakeInstaller)(nil)
	_ OSInstaller = (*crontabInstaller)(nil)
	_ OSInstaller = schtasksInstaller{}
)

func TestFakeInstallerRecordsCalls(t *testing.T) {
	var f fakeInstaller
	s := Schedule{ID: "s1", Cron: "0 2 * * *", Directory: "/tmp", Preset: "downloads"}

	if err := f.Install(s, "/bin/chest"); err != nil {
		t.Fatalf("Install(): %v", err)
	}
	if err := f.Uninstall("s1"); err != nil {
		t.Fatalf("Uninstall(): %v", err)
	}

	if len(f.installed) != 1 || f.installed[0].ID != "s1" {
		t.Fatalf("installed = %+v", f.installed)
	}
	if len(f.binPaths) != 1 || f.binPaths[0] != "/bin/chest" {
		t.Fatalf("binPaths = %v", f.binPaths)
	}
	if len(f.uninstalled) != 1 || f.uninstalled[0] != "s1" {
		t.Fatalf("uninstalled = %v", f.uninstalled)
	}
}

func TestCrontabInstallerRoundTrip(t *testing.T) {
	var stored string
	inst := &crontabInstaller{
		list:  func() (string, error) { return stored, nil },
		write: func(c string) error { stored = c; return nil },
	}
	// Seed a pre-existing, non-chest crontab that must survive.
	stored = "# my crontab\nMAIL TO=root\n"

	if err := inst.Install(Schedule{ID: "s1", Cron: "0 2 * * *", Directory: "/tmp/a", Preset: "downloads"}, "/bin/chest"); err != nil {
		t.Fatalf("Install s1: %v", err)
	}
	if err := inst.Install(Schedule{ID: "s2", Cron: "30 3 * * *", Directory: "/tmp/b"}, "/bin/chest"); err != nil {
		t.Fatalf("Install s2: %v", err)
	}
	if !strings.Contains(stored, "# my crontab") {
		t.Fatalf("install clobbered existing crontab: %q", stored)
	}
	if !strings.Contains(stored, "chest:s1") || !strings.Contains(stored, "chest:s2") {
		t.Fatalf("both schedules should be present: %q", stored)
	}

	if err := inst.Uninstall("s1"); err != nil {
		t.Fatalf("Uninstall s1: %v", err)
	}
	if strings.Contains(stored, "chest:s1") {
		t.Fatalf("s1 not removed: %q", stored)
	}
	if !strings.Contains(stored, "chest:s2") || !strings.Contains(stored, "# my crontab") {
		t.Fatalf("uninstall disturbed other entries: %q", stored)
	}
}

func TestCrontabInstallerWriteError(t *testing.T) {
	inst := &crontabInstaller{
		list:  func() (string, error) { return "", nil },
		write: func(string) error { return errors.New("crontab unavailable") },
	}
	if err := inst.Install(Schedule{ID: "s1", Cron: "0 2 * * *", Directory: "/tmp"}, "/bin/chest"); err == nil {
		t.Fatal("write error must propagate")
	}
}

func TestSchtasksRejectsNonDailyCron(t *testing.T) {
	inst := schtasksInstaller{}
	err := inst.Install(Schedule{ID: "s1", Cron: "*/5 * * * *", Directory: "/tmp"}, `C:\chest.exe`)
	if err == nil {
		t.Fatal("expected complex cron to be rejected")
	}
	if !strings.Contains(err.Error(), "--print-cron") {
		t.Fatalf("error should point at --print-cron: %v", err)
	}
}

func TestSchtasksCommand(t *testing.T) {
	got := schtasksCommand(`C:\Program Files\chest.exe`, []string{"sort", "/tmp/My Downloads", "--preset", "downloads", "-y"})
	want := `"C:\Program Files\chest.exe" sort "/tmp/My Downloads" --preset downloads -y`
	if got != want {
		t.Errorf("schtasksCommand()\n got: %s\nwant: %s", got, want)
	}
}
