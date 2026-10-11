// Package scheduler stores and runs recurring one-shot organizing jobs.
//
// Unlike "chest watch" (a foreground process), a schedule registers a job with
// the operating system's scheduler (crontab on Unix, Task Scheduler on Windows)
// so a directory is organized periodically, e.g. a nightly sort of ~/Downloads.
package scheduler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

// Schedule is a single recurring organizing job.
type Schedule struct {
	ID        string    `toml:"id"`
	Name      string    `toml:"name"`
	Cron      string    `toml:"cron"`
	Directory string    `toml:"directory"`
	Preset    string    `toml:"preset"`
	Rules     []string  `toml:"rules"`
	CreatedAt time.Time `toml:"created_at"`
}

// Args builds the chest invocation for a schedule: a "sort" of the directory
// applying the preset and rules, always with -y so a headless scheduled run
// never blocks on the confirmation prompt.
func (s Schedule) Args() []string {
	args := []string{"sort", s.Directory}
	if s.Preset != "" {
		args = append(args, "--preset", s.Preset)
	}
	for _, r := range s.Rules {
		args = append(args, "--rule", r)
	}
	return append(args, "-y")
}

// storeFile is the on-disk TOML shape: a top-level [[schedules]] array.
type storeFile struct {
	Schedules []Schedule `toml:"schedules"`
}

// Manager is a mutex-guarded view of the schedules.toml store, mirroring
// history.Manager's concurrency discipline.
type Manager struct {
	filePath string
	mu       sync.Mutex
}

// DefaultManager targets ~/.chest/schedules.toml, matching the home-dir state
// convention used by history.
func DefaultManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".chest")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	return &Manager{filePath: filepath.Join(dir, "schedules.toml")}, nil
}

// NewManager targets a custom file path (used by tests to avoid ~/.chest).
func NewManager(path string) *Manager {
	return &Manager{filePath: path}
}

func (m *Manager) load() (storeFile, error) {
	var sf storeFile
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return sf, nil
		}
		return sf, fmt.Errorf("reading schedules file %s: %w", m.filePath, err)
	}
	if len(data) == 0 {
		return sf, nil
	}
	if _, err := toml.Decode(string(data), &sf); err != nil {
		return sf, fmt.Errorf("decoding schedules file %s: %w", m.filePath, err)
	}
	return sf, nil
}

func (m *Manager) save(sf storeFile) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(&sf); err != nil {
		return fmt.Errorf("encoding schedules file %s: %w", m.filePath, err)
	}
	if err := os.WriteFile(m.filePath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing schedules file %s: %w", m.filePath, err)
	}
	return nil
}

// Add persists a new schedule and returns its generated id.
func (m *Manager) Add(s Schedule) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sf, err := m.load()
	if err != nil {
		return "", err
	}
	s.ID = nextID(sf.Schedules)
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	sf.Schedules = append(sf.Schedules, s)
	if err := m.save(sf); err != nil {
		return "", err
	}
	return s.ID, nil
}

// List returns every stored schedule in insertion order.
func (m *Manager) List() ([]Schedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sf, err := m.load()
	if err != nil {
		return nil, err
	}
	return sf.Schedules, nil
}

// Get returns a copy of the schedule with the given id.
func (m *Manager) Get(id string) (*Schedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sf, err := m.load()
	if err != nil {
		return nil, err
	}
	for i := range sf.Schedules {
		if sf.Schedules[i].ID == id {
			s := sf.Schedules[i]
			return &s, nil
		}
	}
	return nil, fmt.Errorf("no schedule with id %q", id)
}

// Remove deletes the schedule with the given id from the store.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sf, err := m.load()
	if err != nil {
		return err
	}
	idx := -1
	for i := range sf.Schedules {
		if sf.Schedules[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("no schedule with id %q", id)
	}
	sf.Schedules = append(sf.Schedules[:idx], sf.Schedules[idx+1:]...)
	return m.save(sf)
}

// nextID produces "s<count+1>", nudging past any id left behind by a removal
// so a fresh schedule can never collide with a surviving one.
func nextID(existing []Schedule) string {
	taken := make(map[string]bool, len(existing))
	for _, e := range existing {
		taken[e.ID] = true
	}
	for n := len(existing) + 1; ; n++ {
		id := fmt.Sprintf("s%d", n)
		if !taken[id] {
			return id
		}
	}
}

// RunNow executes the schedule immediately by re-invoking the current
// executable with the schedule's args, streaming output straight through.
func RunNow(s Schedule) error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}
	cmd := exec.Command(bin, s.Args()...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
