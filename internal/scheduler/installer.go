package scheduler

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// OSInstaller registers and removes a schedule with the operating system's
// scheduler. Tests inject a fake so they never touch the OS.
type OSInstaller interface {
	Install(s Schedule, binPath string) error
	Uninstall(id string) error
}

// NewOSInstaller picks the platform implementation: Task Scheduler on Windows,
// crontab everywhere else.
func NewOSInstaller() OSInstaller {
	if runtime.GOOS == "windows" {
		return schtasksInstaller{}
	}
	return newCrontabInstaller()
}

// commandRunner and crontab I/O are fields so tests can inject fakes.
type crontabInstaller struct {
	list  func() (string, error)
	write func(content string) error
}

func newCrontabInstaller() *crontabInstaller {
	return &crontabInstaller{list: crontabList, write: crontabWrite}
}

func (c *crontabInstaller) Install(s Schedule, binPath string) error {
	current, err := c.list()
	if err != nil {
		return err
	}
	line := crontabLine(s, binPath, s.ID)
	return c.write(appendManagedLine(current, line, s.ID))
}

func (c *crontabInstaller) Uninstall(id string) error {
	current, err := c.list()
	if err != nil {
		return err
	}
	return c.write(removeManagedLine(current, id))
}

// crontabList reads the user's crontab. A missing crontab is expected and
// reported as an empty table rather than an error.
func crontabList() (string, error) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		return "", nil
	}
	return string(out), nil
}

func crontabWrite(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("writing crontab: %w", err)
	}
	return nil
}

// schtasksInstaller maps each schedule to a Windows Task Scheduler task named
// CHEST_<id>.
type schtasksInstaller struct{}

func (schtasksInstaller) Install(s Schedule, binPath string) error {
	hour, minute, ok := dailyHourMinute(s.Cron)
	if !ok {
		return fmt.Errorf("cron %q is too complex for a Windows scheduled task; chest only maps a daily time (single hour and minute with day/month/dow fields as '*'). Re-run with --print-cron to get a crontab line you can add via Task Scheduler", s.Cron)
	}
	args := []string{
		"/Create", "/TN", "CHEST_" + s.ID,
		"/SC", "DAILY", "/ST", fmt.Sprintf("%02d:%02d", hour, minute),
		"/TR", schtasksCommand(binPath, s.Args()), "/F",
	}
	return runPassthrough("schtasks", args)
}

func (schtasksInstaller) Uninstall(id string) error {
	return runPassthrough("schtasks", []string{"/Delete", "/F", "/TN", "CHEST_" + id})
}

func runPassthrough(name string, args []string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// schtasksCommand builds the /TR command string, quoting the binary (it often
// lives under "C:\Program Files") and any argument containing spaces.
func schtasksCommand(binPath string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, `"`+binPath+`"`)
	for _, a := range args {
		parts = append(parts, schtasksArg(a))
	}
	return strings.Join(parts, " ")
}

func schtasksArg(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}
