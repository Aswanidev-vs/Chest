package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Aswanidev-vs/chest/internal/filesystem"
	"github.com/Aswanidev-vs/chest/internal/models"
)

// DefaultMaxEntries is how many history entries `chest history --prune` keeps
// when the caller does not name a different limit.
const DefaultMaxEntries = 100

// maxCheckpoints bounds the checkpoint list even when entries are unlimited, so
// a long-lived history file cannot grow without limit from markers alone. The
// newest markers are the useful ones, so the oldest are dropped first.
const maxCheckpoints = 1000

// Checkpoint is a named marker recording where history stood when
// `chest checkpoint` ran. It is purely additive: recording one moves no file.
// `chest undo --to <label>` reads EntryID to find the last entry the marker
// covers and reverts everything recorded after it as a single batch.
type Checkpoint struct {
	// Name is the user-supplied label, matched by `undo --to` and `checkpoint
	// list`. When a label is used more than once, the newest marker wins.
	Name string `json:"name"`
	// ID is a store-unique identifier for the marker itself, independent of
	// the entry it anchors (EntryID), so a label and a history entry can never
	// be confused for one another.
	ID int `json:"id"`
	// Timestamp is when the marker was taken.
	Timestamp time.Time `json:"timestamp"`
	// EntryID is the most recent history entry ID at the moment the marker was
	// taken. Entries with a higher ID were recorded after it.
	EntryID int `json:"entry_id"`
}

// UndoToResult reports an atomic revert of every operation recorded after a
// checkpoint.
type UndoToResult struct {
	// Checkpoint is the label the revert was requested with.
	Checkpoint string `json:"checkpoint"`
	// EntryID is the entry the checkpoint anchored to; entries above it were
	// the batch.
	EntryID int `json:"entry_id"`
	// UndoneIDs lists the reverted entries oldest first.
	UndoneIDs []int `json:"undone_ids"`
	// AlreadyUndone lists entries after the checkpoint that were recorded as
	// undone already, so they were left untouched instead of failing the batch.
	AlreadyUndone []int `json:"already_undone"`
	// Files counts the restored files across the whole batch.
	Files int `json:"files"`
}

// UndoFilesResult reports a selective revert of one entry.
type UndoFilesResult struct {
	// EntryID is the entry the revert was requested against.
	EntryID int `json:"entry_id"`
	// Requested lists the basenames asked for, in the order given.
	Requested []string `json:"requested"`
	// Restored lists the basenames actually moved back.
	Restored []string `json:"restored"`
	// Unmatched lists requested basenames the entry never moved. They are
	// reported rather than ignored so a typo is never mistaken for success.
	Unmatched []string `json:"unmatched"`
	// Remaining counts the operations still undoable in the entry.
	Remaining int `json:"remaining"`
	// Files counts the restored files.
	Files int `json:"files"`
}

// Store holds history entries
type Store struct {
	LastID  int                   `json:"last_id"`
	Entries []models.HistoryEntry `json:"entries"`
	// Checkpoints holds the named markers. It is optional: a history.json
	// written before checkpoints existed simply has no such key, and loads to
	// a nil slice that every read path handles as "no checkpoints".
	Checkpoints []Checkpoint `json:"checkpoints,omitempty"`
	// MaxEntries is the retention cap Record enforces (0 = unlimited). It is
	// stored with the history it bounds and is optional for the same
	// backward-compatibility reason as Checkpoints.
	MaxEntries int `json:"max_entries,omitempty"`
}

// Manager handles recording and undoing operations
type Manager struct {
	filePath string
	mu       sync.Mutex
	// maxEntries is an in-memory retention cap set by SetMaxEntries. It wins
	// over the persisted Store.MaxEntries when both are present.
	maxEntries int
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

// Record saves an operation run. The optional errs carry per-file failures:
// none means every op succeeded (Complete), some failed ops alongside moved
// ops means Partial, and no moved ops means Failed.
func (m *Manager) Record(directory string, ops []models.HistoryOperation, errs ...string) (*models.HistoryEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	store, err := m.load()
	if err != nil {
		return nil, err
	}

	status := "Complete"
	if len(errs) > 0 {
		if len(ops) > 0 {
			status = "Partial"
		} else {
			status = "Failed"
		}
	}

	store.LastID++
	entry := models.HistoryEntry{
		ID:         store.LastID,
		Timestamp:  time.Now(),
		Directory:  directory,
		Operations: ops,
		FilesCount: len(ops),
		Status:     status,
		Errors:     errs,
	}

	store.Entries = append(store.Entries, entry)

	// Retention runs on every record so the file cannot grow without bound.
	// The entry just appended is the newest, and pruning only ever drops from
	// the front, so the newest entry is never the one removed.
	if limit := m.retentionLimit(store.MaxEntries); limit > 0 {
		pruneStore(&store, limit)
	}

	if err := m.save(store); err != nil {
		return nil, err
	}

	return &entry, nil
}

// retentionLimit resolves the cap Record enforces: a cap set on the manager
// wins over the persisted one, and 0 means unlimited. Callers must hold m.mu.
func (m *Manager) retentionLimit(persisted int) int {
	if m.maxEntries > 0 {
		return m.maxEntries
	}
	if persisted > 0 {
		return persisted
	}
	return 0
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
func (m *Manager) Undo(targetID int, allowSystem ...bool) (*models.HistoryEntry, int, error) {
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

	// 0. Safety check system directory
	allowSys := len(allowSystem) > 0 && allowSystem[0]
	if isSys, reason := filesystem.IsSystemPath(entry.Directory); isSys && !allowSys {
		return nil, 0, fmt.Errorf("access denied: operation #%d targeted protected %s (%s).\nCHEST restricts restoring files in OS and system directories.\nUse --allow-system to override if intentional", entry.ID, reason, entry.Directory)
	}

	// 1. Safety check all operations before moving anything
	plan := make([]plannedOp, 0, len(entry.Operations))
	for _, op := range entry.Operations {
		plan = append(plan, plannedOp{entryID: entry.ID, op: op})
	}
	if err := preflight(plan); err != nil {
		return nil, 0, err
	}

	// 2. Perform safe moves back to OriginalSource
	restoredCount, err := execute(plan)
	if err != nil {
		return nil, 0, err
	}

	// 3. Clean up empty destination directories left behind up to the target root directory
	cleanupEmptyDirs(entry.Directory, entry.Operations)

	// 4. Mark status as Undone
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

// plannedOp is one operation of one entry, tagged with the entry it came from
// so a refusal can name the run the user has to look at.
type plannedOp struct {
	entryID int
	op      models.HistoryOperation
}

// preflight validates an entire batch of operations before any of them is
// attempted. It exists so that a revert is all-or-nothing: a batch that would
// restore three of four files is refused outright rather than half applied.
//
// The structural checks come first because they explain a refusal better than
// the filesystem checks do. Two operations restoring to the same location would
// have the second overwrite the first, and a file that was moved again by a
// later operation cannot be put back by the same batch.
func preflight(plan []plannedOp) error {
	restores := make(map[string]int, len(plan)) // restore target -> batch position
	movers := make(map[string]int, len(plan))   // destination -> batch position
	for i, p := range plan {
		src := normalizePath(p.op.OriginalSource)
		dst := normalizePath(p.op.Destination)
		if src != dst {
			movers[dst] = i
		}
	}
	for i, p := range plan {
		src := normalizePath(p.op.OriginalSource)
		if first, dup := restores[src]; dup {
			return fmt.Errorf("cannot undo: operations #%d and #%d would both restore a file to '%s', so one would overwrite the other", plan[first].entryID, p.entryID, src)
		}
		restores[src] = i

		// A file that was moved on again by a later operation of the same
		// batch cannot be put back by it: reverting the later operation would
		// fill this one's destination, and reverting this one first would move
		// the wrong file. Naming the pair beats reporting the collision as a
		// missing or occupied path.
		if blocker, chained := movers[src]; chained && blocker != i {
			return fmt.Errorf("cannot undo: operation #%d and operation #%d exchange '%s', so the two cannot be reverted as one batch.\nUndo operation #%d first, then retry", plan[blocker].entryID, p.entryID, src, plan[blocker].entryID)
		}
	}

	for _, p := range plan {
		// Verify moved destination file still exists
		if !filesystem.Exists(p.op.Destination) {
			return fmt.Errorf("cannot undo: file '%s' no longer exists at destination", p.op.Destination)
		}
		// Verify source path isn't occupied by a new conflicting file
		if filesystem.Exists(p.op.OriginalSource) {
			return fmt.Errorf("cannot undo: original location '%s' is already occupied", p.op.OriginalSource)
		}
	}
	return nil
}

// execute moves a pre-flighted batch back to its original locations.
//
// If a move fails partway through, every file already restored in this pass is
// moved back to where it came from, leaving the batch in its recorded state. A
// later retry therefore starts from a clean slate, and nothing is left
// half-restored.
func execute(plan []plannedOp) (int, error) {
	restoredCount := 0
	for _, p := range plan {
		if err := filesystem.MoveFile(p.op.Destination, p.op.OriginalSource); err != nil {
			for i := 0; i < restoredCount; i++ {
				done := plan[i]
				_ = filesystem.MoveFile(done.op.OriginalSource, done.op.Destination)
			}
			return 0, fmt.Errorf("error restoring %s to %s: %w (rolled back %d restored file(s))", p.op.Destination, p.op.OriginalSource, err, restoredCount)
		}
		restoredCount++
	}
	return restoredCount, nil
}

// cleanupEmptyDirs removes the directories the restored files came out of,
// stopping at root. os.Remove only succeeds on an empty directory, so a folder
// still holding a file is simply left in place.
func cleanupEmptyDirs(root string, ops []models.HistoryOperation) {
	absRoot, errRoot := filepath.Abs(root)
	if errRoot != nil {
		absRoot = filepath.Clean(root)
	}

	dirsToClean := make(map[string]bool)
	for _, op := range ops {
		absDest, errDest := filepath.Abs(op.Destination)
		if errDest != nil {
			absDest = filepath.Clean(op.Destination)
		}
		dir := filepath.Dir(absDest)
		for {
			dirClean := filepath.Clean(dir)
			rel, errRel := filepath.Rel(absRoot, dirClean)
			if errRel != nil || rel == "." || strings.HasPrefix(rel, "..") {
				break
			}
			dirsToClean[dirClean] = true
			parent := filepath.Dir(dirClean)
			if parent == dirClean {
				break
			}
			dir = parent
		}
	}

	// Sort deepest directories first so child empty folders are deleted before parent folders
	var sortedDirs []string
	for d := range dirsToClean {
		sortedDirs = append(sortedDirs, d)
	}
	sort.Slice(sortedDirs, func(i, j int) bool {
		return len(sortedDirs[i]) > len(sortedDirs[j])
	})

	for _, d := range sortedDirs {
		// os.Remove only succeeds if directory is completely empty
		_ = os.Remove(d)
	}
}

// normalizePath makes two spellings of the same path comparable when deciding
// whether two operations of a batch would touch the same file.
func normalizePath(p string) string {
	clean := filepath.Clean(p)
	if abs, err := filepath.Abs(clean); err == nil {
		return abs
	}
	return clean
}

// pruneStore drops the oldest entries until at most keep remain and returns
// how many were removed. It never removes the newest entry: keep is validated
// to be at least 1 by the callers, and only the front of the slice is ever cut.
//
// Checkpoints survive pruning. A marker stays meaningful after the entries it
// was anchored to are gone, because undoing to it is a comparison on entry IDs
// rather than a lookup. The list is only bounded by maxCheckpoints so it cannot
// grow forever.
func pruneStore(store *Store, keep int) int {
	if keep < 1 || len(store.Entries) <= keep {
		return 0
	}
	removed := len(store.Entries) - keep
	store.Entries = append([]models.HistoryEntry(nil), store.Entries[removed:]...)
	if len(store.Checkpoints) > maxCheckpoints {
		store.Checkpoints = append([]Checkpoint(nil), store.Checkpoints[len(store.Checkpoints)-maxCheckpoints:]...)
	}
	return removed
}

// AddCheckpoint records a named marker at the current end of history. It moves
// no files: the marker only records which entry history had reached, so that
// UndoToCheckpoint can later find the operations recorded after it.
func (m *Manager) AddCheckpoint(name string) (*Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("checkpoint name must not be empty")
	}

	store, err := m.load()
	if err != nil {
		return nil, err
	}

	// Marker IDs are store-unique on their own sequence so they can never be
	// mistaken for a history entry ID, and never repeat even after pruning.
	next := 1
	for _, c := range store.Checkpoints {
		if c.ID >= next {
			next = c.ID + 1
		}
	}

	cp := Checkpoint{
		Name:      name,
		ID:        next,
		Timestamp: time.Now(),
		EntryID:   store.LastID,
	}

	store.Checkpoints = append(store.Checkpoints, cp)
	if len(store.Checkpoints) > maxCheckpoints {
		store.Checkpoints = append([]Checkpoint(nil), store.Checkpoints[len(store.Checkpoints)-maxCheckpoints:]...)
	}
	if err := m.save(store); err != nil {
		return nil, err
	}
	return &cp, nil
}

// ListCheckpoints returns every checkpoint, oldest first.
func (m *Manager) ListCheckpoints() ([]Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	store, err := m.load()
	if err != nil {
		return nil, err
	}
	return store.Checkpoints, nil
}

// findCheckpoint resolves a label to a marker, preferring the newest one so a
// reused label always means "the most recent marker with this name".
func findCheckpoint(checkpoints []Checkpoint, name string) (Checkpoint, error) {
	name = strings.TrimSpace(name)
	for i := len(checkpoints) - 1; i >= 0; i-- {
		if checkpoints[i].Name == name {
			return checkpoints[i], nil
		}
	}
	return Checkpoint{}, fmt.Errorf("checkpoint '%s' not found", name)
}

// findEntry resolves an operation ID to its index in the store, falling back to
// the latest entry that can still be undone when targetID is 0 or negative.
func findEntry(store *Store, targetID int) (int, error) {
	if len(store.Entries) == 0 {
		return -1, fmt.Errorf("no history operations found")
	}

	if targetID <= 0 {
		// Find latest entry with Complete status
		for i := len(store.Entries) - 1; i >= 0; i-- {
			if store.Entries[i].Status == "Complete" {
				return i, nil
			}
		}
		return -1, fmt.Errorf("no active operations available to undo")
	}

	for i, e := range store.Entries {
		if e.ID == targetID {
			if e.Status == "Undone" {
				return -1, fmt.Errorf("operation #%d has already been undone", targetID)
			}
			return i, nil
		}
	}
	return -1, fmt.Errorf("history operation #%d not found", targetID)
}

// UndoToCheckpoint reverts every operation recorded after the named checkpoint
// as one atomic batch: either all of the files go back or none of them do.
//
// The whole batch is pre-flighted first. If any destination has since
// disappeared, any original location is occupied, or any two operations would
// collide, the batch is refused with the reason and nothing is moved at all.
// Entries that were already undone are left alone rather than failing the batch,
// because there is nothing left to revert in them.
func (m *Manager) UndoToCheckpoint(name string, allowSystem ...bool) (*UndoToResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	store, err := m.load()
	if err != nil {
		return nil, err
	}

	cp, err := findCheckpoint(store.Checkpoints, name)
	if err != nil {
		return nil, err
	}

	allowSys := len(allowSystem) > 0 && allowSystem[0]
	result := &UndoToResult{Checkpoint: cp.Name, EntryID: cp.EntryID}

	var (
		plan     []plannedOp
		roots    []string
		entries  []models.HistoryEntry
		entryPos []int
	)

	for i := range store.Entries {
		e := store.Entries[i]
		if e.ID <= cp.EntryID {
			continue
		}
		switch e.Status {
		case "Undone":
			result.AlreadyUndone = append(result.AlreadyUndone, e.ID)
			continue
		case "Failed":
			// Nothing moved, so there is nothing to put back.
			continue
		}

		if isSys, reason := filesystem.IsSystemPath(e.Directory); isSys && !allowSys {
			return nil, fmt.Errorf("access denied: operation #%d targeted protected %s (%s).\nCHEST restricts restoring files in OS and system directories.\nUse --allow-system to override if intentional", e.ID, reason, e.Directory)
		}

		result.UndoneIDs = append(result.UndoneIDs, e.ID)
		entries = append(entries, e)
		entryPos = append(entryPos, i)
		roots = append(roots, e.Directory)
		for _, op := range e.Operations {
			plan = append(plan, plannedOp{entryID: e.ID, op: op})
		}
	}

	if len(plan) == 0 {
		if len(result.AlreadyUndone) > 0 {
			return nil, fmt.Errorf("nothing to undo after checkpoint '%s': every operation recorded after it is already undone", cp.Name)
		}
		return nil, fmt.Errorf("no operations recorded after checkpoint '%s'", cp.Name)
	}

	// Everything is checked before anything is touched.
	if err := preflight(plan); err != nil {
		return nil, fmt.Errorf("cannot undo to checkpoint '%s': %w", cp.Name, err)
	}

	restored, err := execute(plan)
	if err != nil {
		return nil, err
	}

	// Clean up per entry, oldest first, so each entry only removes the folders
	// its own restored files came out of.
	for i := range entries {
		var ops []models.HistoryOperation
		for _, p := range plan {
			if p.entryID == entries[i].ID {
				ops = append(ops, p.op)
			}
		}
		cleanupEmptyDirs(roots[i], ops)
	}

	for _, pos := range entryPos {
		store.Entries[pos].Status = "Undone"
	}

	result.Files = restored
	if err := m.save(store); err != nil {
		return result, fmt.Errorf("files restored, but failed updating history status: %w", err)
	}
	return result, nil
}

// UndoFiles reverts only the operations of one entry whose basename matches one
// of names, leaving the rest of the entry in place.
//
// The requested names are matched against the basename of both sides of each
// operation, and a name that matches nothing is reported back in the result
// rather than ignored, so a mistyped filename is never read as a success. If
// nothing matches at all, nothing is moved and the entry's own filenames are
// listed so the user can see what was actually there. The matched operations are
// reverted as one batch: they are all pre-flighted, then moved, then rolled
// back as a unit if a move fails.
func (m *Manager) UndoFiles(targetID int, names []string, allowSystem ...bool) (*UndoFilesResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	store, err := m.load()
	if err != nil {
		return nil, err
	}

	entryIndex, err := findEntry(&store, targetID)
	if err != nil {
		return nil, err
	}
	entry := &store.Entries[entryIndex]

	allowSys := len(allowSystem) > 0 && allowSystem[0]
	if isSys, reason := filesystem.IsSystemPath(entry.Directory); isSys && !allowSys {
		return nil, fmt.Errorf("access denied: operation #%d targeted protected %s (%s).\nCHEST restricts restoring files in OS and system directories.\nUse --allow-system to override if intentional", entry.ID, reason, entry.Directory)
	}

	wanted := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		wanted = append(wanted, n)
	}
	if len(wanted) == 0 {
		return nil, fmt.Errorf("no file name given to undo from operation #%d", entry.ID)
	}

	result := &UndoFilesResult{EntryID: entry.ID, Requested: wanted}

	var plan []plannedOp
	for _, name := range wanted {
		matched := false
		for _, op := range entry.Operations {
			if filepath.Base(op.Destination) != name && filepath.Base(op.OriginalSource) != name {
				continue
			}
			plan = append(plan, plannedOp{entryID: entry.ID, op: op})
			result.Restored = append(result.Restored, name)
			matched = true
		}
		if !matched {
			result.Unmatched = append(result.Unmatched, name)
		}
	}

	if len(plan) == 0 {
		return nil, fmt.Errorf("operation #%d does not move any of: %s\nIt moves: %s", entry.ID, strings.Join(wanted, ", "), describeOperations(entry.Operations))
	}

	if err := preflight(plan); err != nil {
		return nil, fmt.Errorf("cannot undo operation #%d: %w", entry.ID, err)
	}

	restored, err := execute(plan)
	if err != nil {
		return nil, err
	}
	result.Files = restored

	cleanupEmptyDirs(entry.Directory, planOps(plan))

	// Drop the operations that have now been performed: keeping them would
	// make a later `undo <id>` refuse on files that are already back home.
	remaining := make([]models.HistoryOperation, 0, len(entry.Operations))
	done := make(map[string]bool, len(plan))
	for _, p := range plan {
		done[normalizePath(p.op.Destination)+"\x00"+normalizePath(p.op.OriginalSource)] = true
	}
	for _, op := range entry.Operations {
		if done[normalizePath(op.Destination)+"\x00"+normalizePath(op.OriginalSource)] {
			continue
		}
		remaining = append(remaining, op)
	}

	entry.Operations = remaining
	entry.FilesCount = len(remaining)
	if len(remaining) == 0 {
		entry.Status = "Undone"
	}
	result.Remaining = len(remaining)

	if err := m.save(store); err != nil {
		return result, fmt.Errorf("files restored, but failed updating history status: %w", err)
	}
	return result, nil
}

// planOps projects a batch back onto the operations it carries.
func planOps(plan []plannedOp) []models.HistoryOperation {
	ops := make([]models.HistoryOperation, 0, len(plan))
	for _, p := range plan {
		ops = append(ops, p.op)
	}
	return ops
}

// describeOperations renders the filenames an entry moved, for the "no match"
// refusal. The list is capped so a huge entry cannot bury the error.
func describeOperations(ops []models.HistoryOperation) string {
	const maxShown = 10
	names := make([]string, 0, len(ops))
	for _, op := range ops {
		base := filepath.Base(op.Destination)
		if base == "." || base == string(filepath.Separator) {
			base = filepath.Base(op.OriginalSource)
		}
		names = append(names, base)
	}
	sort.Strings(names)
	if len(names) > maxShown {
		return fmt.Sprintf("%s and %d more", strings.Join(names[:maxShown], ", "), len(names)-maxShown)
	}
	if len(names) == 0 {
		return "(no files)"
	}
	return strings.Join(names, ", ")
}

// Prune drops the oldest history entries so that only the newest keep remain,
// and reports how many were removed. The newest entries are never removed, and
// the entry IDs already handed out are never reused: LastID only ever climbs,
// so a pruned ID stays dead rather than silently pointing at a different run.
func (m *Manager) Prune(keep int) (int, error) {
	if keep < 1 {
		return 0, fmt.Errorf("keep must be at least 1")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	store, err := m.load()
	if err != nil {
		return 0, err
	}
	removed := pruneStore(&store, keep)
	if removed == 0 {
		return 0, nil
	}
	if err := m.save(store); err != nil {
		return 0, err
	}
	return removed, nil
}

// SetMaxEntries stores the retention cap that Record enforces from now on and
// prunes the store down to it immediately, reporting how many entries were
// dropped. The cap is written into the history file it bounds, so every later
// run — not just this one — refuses to grow the file past it.
func (m *Manager) SetMaxEntries(n int) (int, error) {
	if n < 1 {
		return 0, fmt.Errorf("max entries must be at least 1")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.maxEntries = n
	store, err := m.load()
	if err != nil {
		return 0, err
	}
	store.MaxEntries = n
	removed := pruneStore(&store, n)
	if err := m.save(store); err != nil {
		return removed, err
	}
	return removed, nil
}

// MaxEntries reports the cap this manager enforces, 0 meaning unlimited.
func (m *Manager) MaxEntries() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxEntries
}
