package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Aswanidev-vs/chest/internal/history"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/Aswanidev-vs/chest/internal/organizer"
	"github.com/Aswanidev-vs/chest/internal/planner"
	"github.com/Aswanidev-vs/chest/internal/presets"
	"github.com/Aswanidev-vs/chest/internal/rules"
	"github.com/fsnotify/fsnotify"
)

// WatchOptions configures watch mode
type WatchOptions struct {
	Directory string
	Preset    string
	Rules     []string
	Debounce  time.Duration
	DryRun    bool
}

// Watcher monitors a folder and sorts incoming files
type Watcher struct {
	opts    WatchOptions
	rules   []models.Rule
	engine  *rules.Engine
	planner *planner.Planner
	history *history.Manager
}

// New creates a Watcher
func New(opts WatchOptions) (*Watcher, error) {
	if opts.Debounce <= 0 {
		opts.Debounce = 500 * time.Millisecond
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
	p := planner.New(engine, "", models.CollisionRename)
	histMgr, _ := history.DefaultManager()

	return &Watcher{
		opts:    opts,
		rules:   activeRules,
		engine:  engine,
		planner: p,
		history: histMgr,
	}, nil
}

// Start begins watching the target folder
func (w *Watcher) Start(ctx context.Context, onEvent func(file string, dest string, err error)) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fsw.Close()

	targetDir := filepath.Clean(w.opts.Directory)
	if err := fsw.Add(targetDir); err != nil {
		return err
	}

	var (
		mu      sync.Mutex
		pending = make(map[string]*time.Timer)
	)

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-fsw.Events:
			if !ok {
				return nil
			}

			// Only process write or create
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
				path := event.Name

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

					w.processFile(path, onEvent)
				})
				mu.Unlock()
			}

		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			if onEvent != nil {
				onEvent("", "", err)
			}
		}
	}
}

func (w *Watcher) processFile(filePath string, onEvent func(file string, dest string, err error)) {
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

	plan, err := w.planner.Plan(w.opts.Directory, []models.File{file})
	if err != nil || len(plan.Operations) == 0 {
		return
	}

	var execErr error
	if !w.opts.DryRun {
		_, execErr = organizer.Execute(plan, w.history, nil)
	}

	if onEvent != nil {
		onEvent(name, plan.Operations[0].Destination, execErr)
	}
}
