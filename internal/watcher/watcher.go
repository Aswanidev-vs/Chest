package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/metadata"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/organizer"
	"github.com/Aswanidev-vs/chest/internal/planner"
	"github.com/Aswanidev-vs/chest/internal/presets"
	"github.com/Aswanidev-vs/chest/internal/rules"
	"github.com/Aswanidev-vs/chest/internal/scanner"
	"github.com/fsnotify/fsnotify"
)

// WatchOptions configures watch mode
type WatchOptions struct {
	Directory  string
	Preset     string
	Rules      []string
	Name       string
	Debounce   time.Duration
	Initial    bool
	DryRun     bool
	DateSource string
	// EnrichFunc optionally enriches a scanned file with sample-based
	// metadata (format sniffing, embedded dates, extra fields) before it is
	// planned. When nil, files are planned with the built-in metadata only.
	// The second return value reports whether the file was modified so the
	// planner can skip re-planning untouched files.
	EnrichFunc func(file models.File) (models.File, bool)
}

// EventKind identifies the type of activity reported to the watch output.
type EventKind int

const (
	KindOrganized EventKind = 0 // a file was organized into a compartment
	KindCreated   EventKind = 1 // a new user folder appeared
	KindDeleted   EventKind = 2 // a user folder was deleted or renamed away
	KindError     EventKind = 3 // watcher or processing error
)

// Event is a single piece of activity reported by [Watcher.Start].
type Event struct {
	Kind EventKind // what happened
	Name string    // file or folder name involved
	Dest string    // for KindOrganized: the destination; otherwise empty
	Err  error     // set when Kind is KindError
}

// Watcher monitors a folder and sorts incoming files
type Watcher struct {
	opts    WatchOptions
	rules   []models.Rule
	engine  *rules.Engine
	planner *planner.Planner
	history *history.Manager
	// managed holds the compartment folder names chest itself creates for rules.
	managed map[string]struct{}
	// knownDirs holds external top-level folders seen so far (key: lowercased
	// path), so user folder creation and deletion can be reported accurately.
	knownDirs map[string]struct{}
}

// New creates a Watcher
func New(opts WatchOptions) (*Watcher, error) {
	if opts.Debounce <= 0 {
		opts.Debounce = 500 * time.Millisecond
	}
	if opts.DateSource == "" {
		opts.DateSource = "modified"
	}

	var activeRules []models.Rule

	if opts.Preset != "" {
		presetRules, err := presets.LoadPreset(opts.Preset)
		if err != nil {
			return nil, err
		}
		activeRules = append(activeRules, presetRules...)
	}

	for i, rStr := range opts.Rules {
		r, err := rules.ParseRule(rStr, 100-i)
		if err != nil {
			return nil, err
		}
		activeRules = append(activeRules, r)
	}

	if len(activeRules) == 0 {
		defRules, err := presets.LoadPreset("downloads")
		if err != nil {
			return nil, err
		}
		activeRules = append(activeRules, defRules...)
	}

	engine := rules.NewEngine(activeRules)
	// --name routes every incoming match flat into a single folder inside the
	// watched directory (e.g. --name "Anime" -> Anime/*.mp4), ignoring category
	// subfolders. Without it, matches go into their category compartments.
	var p *planner.Planner
	if opts.Name != "" {
		baseDest := filepath.Join(opts.Directory, opts.Name)
		p = planner.NewFlat(engine, baseDest, models.CollisionRename)
	} else {
		p = planner.New(engine, "", models.CollisionRename)
	}
	histMgr, _ := history.DefaultManager()

	// Collect the compartment folder names chest manages, so in watch mode we
	// don't report chest's own created folders as user-created ones.
	managed := make(map[string]struct{})
	if opts.Name != "" {
		// With --name chest creates a single named folder for every match.
		managed[localName(opts.Name)] = struct{}{}
	} else {
		for _, r := range activeRules {
			if r.Destination != "" {
				managed[localName(r.Destination)] = struct{}{}
			}
		}
	}

	return &Watcher{
		opts:      opts,
		rules:     activeRules,
		engine:    engine,
		planner:   p,
		history:   histMgr,
		managed:   managed,
		knownDirs: make(map[string]struct{}),
	}, nil
}

// Start begins watching the target folder
func (w *Watcher) Start(ctx context.Context, cb func(ev Event)) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fsw.Close()

	targetDir := filepath.Clean(w.opts.Directory)
	if err := fsw.Add(targetDir); err != nil {
		return err
	}

	// Track the external folders that already exist so we can report when the
	// user creates a new one and stop reporting ones once they're deleted.
	w.seedKnownDirs(targetDir)

	var (
		mu      sync.Mutex
		pending = make(map[string]*time.Timer)
	)

	// Optional initial sweep: organize files already present in the folder
	// before entering watch mode. This makes `watch --initial` behave like a
	// one-time sort followed by continuous monitoring of new arrivals.
	if w.opts.Initial {
		sc := scanner.New(scanner.ScanOptions{
			Recursive:      false, // fsnotify only monitors the top-level directory
			IncludeHidden:  false,
			FollowSymlinks: false,
			ExtractDates:   w.opts.DateSource != "modified",
		})
		existing, serr := sc.Scan(targetDir)
		if serr == nil {
			for _, ex := range existing {
				w.processFile(ex.Path, cb)
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			path := event.Name

			// Report top-level folder changes (create/delete/rename).
			//
			// On Windows a folder creation arrives as a Create event; a deletion
			// as a Remove; a rename as a Remove of the old name plus a Create of
			// the new one (fsnotify's Rename/Create correlation is not part of the
			// public API, so the old name is reported as removed here).
			if event.Has(fsnotify.Create) {
				if st, serr := os.Stat(path); serr == nil && st.IsDir() {
					w.handleDirCreated(path, cb)
					continue
				}
			}
			if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				if _, known := w.knownDirs[strings.ToLower(path)]; known {
					w.handleDirRemoved(path, cb)
				}
				continue // (file) removals and renames are not reported
			}

			// Only organize new or modified files.
			if !(event.Has(fsnotify.Create) || event.Has(fsnotify.Write)) {
				continue
			}

			// Ignore temp/download files (.tmp, .crdownload, .part)
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".tmp" || ext == ".crdownload" || ext == ".part" {
				continue
			}

			mu.Lock()
			if timer, exists := pending[path]; exists {
				timer.Stop()
			}

			pending[path] = time.AfterFunc(w.opts.Debounce, func() {
				mu.Lock()
				delete(pending, path)
				mu.Unlock()

				w.processFile(path, cb)
			})
			mu.Unlock()

		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			if cb != nil {
				cb(Event{Kind: KindError, Err: err})
			}
		}
	}
}

func (w *Watcher) processFile(filePath string, cb func(ev Event)) {
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		return
	}

	name := info.Name()
	if strings.HasPrefix(name, ".") {
		return
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	relPath, _ := filepath.Rel(w.opts.Directory, filePath)

	file := models.File{
		Path:      filePath,
		RelPath:   relPath,
		Name:      name,
		Extension: ext,
		Size:      info.Size(),
		ModTime:   info.ModTime(),
		IsDir:     false,
	}
	if meta, err := metadata.Extract(filePath, ext); err == nil {
		if meta.Format != "" {
			file.Format = meta.Format
		}
		if file.MIMEType == "" {
			file.MIMEType = meta.MIMEType
		}
		if len(meta.Fields) > 0 {
			file.Metadata = meta.Fields
		}
		if w.opts.DateSource != "modified" && !meta.Date.Date.IsZero() {
			file.TakenDate = &meta.Date.Date
			file.TakenDateSource = meta.Date.Source
		}
	}

	// External enrichment (e.g. metadata plugins) runs last so it can fill
	// gaps or override values the built-in extractors produced.
	if w.opts.EnrichFunc != nil {
		if enriched, ok := w.opts.EnrichFunc(file); ok {
			file = enriched
		}
	}

	plan, err := w.planner.Plan(w.opts.Directory, []models.File{file})
	if err != nil || len(plan.Operations) == 0 {
		return
	}

	var execErr error
	if !w.opts.DryRun {
		_, execErr = organizer.Execute(plan, w.history, nil)
	}

	if cb != nil {
		cb(Event{Kind: KindOrganized, Name: name, Dest: plan.Operations[0].Destination, Err: execErr})
	}
}

// seedKnownDirs records the external (non-managed) folders already present so
// that creation and deletion of user folders can be reported accurately.
func (w *Watcher) seedKnownDirs(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, ierr := entry.Info()
		if ierr != nil {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if !info.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if _, managed := w.managed[localName(path)]; managed {
			continue
		}
		w.knownDirs[strings.ToLower(path)] = struct{}{}
	}
}

// handleDirCreated records a new top-level folder and reports it, unless it's
// one of chest's own compartment folders (or a dot-directory).
func (w *Watcher) handleDirCreated(path string, cb func(ev Event)) {
	if strings.HasPrefix(baseName(path), ".") {
		return
	}
	if _, managed := w.managed[localName(path)]; managed {
		return
	}
	lower := strings.ToLower(path)
	if _, known := w.knownDirs[lower]; known {
		return
	}
	w.knownDirs[lower] = struct{}{}
	if cb != nil {
		cb(Event{Kind: KindCreated, Name: baseName(path)})
	}
}

// handleDirRemoved stops tracking a folder that was deleted or renamed away and
// reports it.
func (w *Watcher) handleDirRemoved(path string, cb func(ev Event)) {
	delete(w.knownDirs, strings.ToLower(path))
	if cb != nil {
		cb(Event{Kind: KindDeleted, Name: baseName(path)})
	}
}

// baseName returns the final component of a path, preserving original case.
func baseName(p string) string {
	parts := strings.Split(filepath.ToSlash(p), "/")
	return strings.TrimSpace(parts[len(parts)-1])
}

// localName returns the lower-cased final component of a path, used for matching
// paths against managed destination folders.
func localName(p string) string {
	return strings.ToLower(baseName(p))
}
