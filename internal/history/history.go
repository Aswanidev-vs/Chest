package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/models"
)

// Store holds history entries
type Store struct {
	LastID  int                   `json:"last_id"`
	Entries []models.HistoryEntry `json:"entries"`
}

// Manager handles recording and undoing operations
type Manager struct {
	filePath string
	mu       sync.Mutex
}

// DefaultManager creates manager targeting ~/.chest/history.json and ~/.chest/index.db
func DefaultManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".chest")
	if err := filesystem.EnsureDir(dir); err != nil {
		return nil, err
	}
	return &Manager{filePath: filepath.Join(dir, "history.json")}, nil
}

// NewManager creates manager targeting custom path
func NewManager(path string) *Manager {
	return &Manager{filePath: path}
}

func (m *Manager) load() (Store, error) {
	var store Store
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return store, err
	}
	if len(data) == 0 {
		return store, nil
	}
	err = json.Unmarshal(data, &store)
	return store, err
}

func (m *Manager) save(store Store) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.filePath, data, 0644)
}

// Record saves a completed operation run
func (m *Manager) Record(directory string, ops []models.HistoryOperation) (*models.HistoryEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	store, err := m.load()
	if err != nil {
		return nil, err
	}

	store.LastID++
	entry := models.HistoryEntry{
		ID:         store.LastID,
		Timestamp:  time.Now(),
		Directory:  directory,
		Operations: ops,
		FilesCount: len(ops),
		Status:     "Complete",
	}

	store.Entries = append(store.Entries, entry)
	if err := m.save(store); err != nil {
		return nil, err
	}

	return &entry, nil
}

// List returns all history entries
func (m *Manager) List() ([]models.HistoryEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	store, err := m.load()
	if err != nil {
		return nil, err
	}
	return store.Entries, nil
}

// Undo reverses an operation by ID (or latest if id == 0)
func (m *Manager) Undo(targetID int) (*models.HistoryEntry, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	store, err := m.load()
	if err != nil {
		return nil, 0, err
	}

	if len(store.Entries) == 0 {
		return nil, 0, fmt.Errorf("no history operations found")
	}

	var entryIndex = -1
	if targetID <= 0 {
		// Find latest entry with Complete status
		for i := len(store.Entries) - 1; i >= 0; i-- {
			if store.Entries[i].Status == "Complete" {
				entryIndex = i
				break
			}
		}
		if entryIndex == -1 {
			return nil, 0, fmt.Errorf("no active operations available to undo")
		}
	} else {
		for i, e := range store.Entries {
			if e.ID == targetID {
				entryIndex = i
				break
			}
		}
		if entryIndex == -1 {
			return nil, 0, fmt.Errorf("history operation #%d not found", targetID)
		}
		if store.Entries[entryIndex].Status == "Undone" {
			return nil, 0, fmt.Errorf("operation #%d has already been undone", targetID)
		}
	}

	entry := &store.Entries[entryIndex]

	// 1. Safety check all operations before moving anything
	for _, op := range entry.Operations {
		// Verify moved destination file still exists
		if !filesystem.Exists(op.Destination) {
			return nil, 0, fmt.Errorf("cannot undo: file '%s' no longer exists at destination", op.Destination)
		}
		// Verify source path isn't occupied by a new conflicting file
		if filesystem.Exists(op.OriginalSource) {
			return nil, 0, fmt.Errorf("cannot undo: original location '%s' is already occupied", op.OriginalSource)
		}
	}

	// 2. Perform safe moves back to OriginalSource
	restoredCount := 0
	for _, op := range entry.Operations {
		if err := filesystem.MoveFile(op.Destination, op.OriginalSource); err != nil {
			return nil, restoredCount, fmt.Errorf("error restoring %s to %s: %w", op.Destination, op.OriginalSource, err)
		}
		restoredCount++
	}

	// 3. Mark status as Undone
	entry.Status = "Undone"
	if err := m.save(store); err != nil {
		return entry, restoredCount, fmt.Errorf("files restored, but failed updating history status: %w", err)
	}

	return entry, restoredCount, nil
}

// ClearHistory removes all undo history by deleting the history.json file
func (m *Manager) ClearHistory() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	err := os.Remove(m.filePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clear history: %w", err)
	}
	return nil
}
