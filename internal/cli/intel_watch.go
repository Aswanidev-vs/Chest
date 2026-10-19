package cli

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Aswanidev-vs/chest/internal/indexer"
	"github.com/fsnotify/fsnotify"
)

// DefaultIndexDebounce mirrors the watch command's default: a burst of
// filesystem events (an editor saving, a copy landing in chunks) is collapsed
// into a single re-index pass.
const DefaultIndexDebounce = 500 * time.Millisecond

// validateWatchTarget rejects a path that cannot be watched. Checking up front
// keeps a mistyped path from reporting itself as "0 files indexed" forever.
func validateWatchTarget(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("failed watching %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cannot watch %s: not a directory", root)
	}
	return nil
}

// runIndexWatch keeps the index in sync with a directory until ctx is
// cancelled (Ctrl+C / SIGTERM).
//
// Every debounced burst re-enters Store.IndexDirectory for the whole root
// rather than patching individual rows by hand. That is what makes the
// incremental size+mtime skip do the real work: a quiet tree costs one cheap
// walk, and a single changed file costs one walk plus one updated row. It also
// means watch mode can never leave the cache describing files that were
// deleted, because the stale-row purge inside that same call handles them.
func runIndexWatch(ctx context.Context, w io.Writer, store *indexer.Store, root string, computeHashes bool, except []string, debounce time.Duration) error {
	if debounce <= 0 {
		debounce = DefaultIndexDebounce
	}
	if err := validateWatchTarget(root); err != nil {
		return err
	}

	// Ctrl+C (and SIGTERM) cancels the context; the loop below then returns
	// after the final close, so the database is always shut down cleanly.
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	start := time.Now()
	initial, err := store.IndexDirectory(root, computeHashes, nil, except)
	if err != nil {
		return err
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fsw.Close()

	if err := addWatchDirs(fsw, store, root, root, except); err != nil {
		return fmt.Errorf("failed watching %s: %w", root, err)
	}

	fmt.Fprintf(w, "\x1b[1;38;5;82m✔ Indexed %d files\x1b[0m in %v\n", initial, time.Since(start).Round(time.Millisecond))
	fmt.Fprintf(w, "\n\x1b[1;38;5;255mCHEST CONTINUOUS INDEXING\x1b[0m\n")
	fmt.Fprint(w, "─────────────────────────────────────────────────────────────\n")
	fmt.Fprintf(w, "  \x1b[38;5;246mWatching:\x1b[0m  \x1b[1;38;5;220m%s\x1b[0m\n", root)
	fmt.Fprintf(w, "  \x1b[38;5;246mDebounce:\x1b[0m  %v\n", debounce)
	fmt.Fprint(w, "  \x1b[38;5;246mPress Ctrl+C to stop.\x1b[0m\n")

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		timer   *time.Timer
		passes  int
		updated int
	)

	// drain cancels any pending pass and waits for an in-flight one. Every
	// exit path runs it (directly for the Ctrl+C summary, via defer for the
	// rest) so the store is never closed out from under a running re-index.
	drain := func() {
		mu.Lock()
		if timer != nil && timer.Stop() {
			wg.Done() // the cancelled callback never runs
		}
		timer = nil
		mu.Unlock()
		wg.Wait()
	}
	defer drain()

	// reindex runs a single incremental pass. Only one timer is ever armed, so
	// passes never overlap and can never interleave their stale-row purges.
	// passes/updated are only read after a drain, which is what makes them safe.
	reindex := func() {
		n, ierr := store.IndexDirectory(root, computeHashes, nil, except)
		if ierr != nil {
			fmt.Fprintf(w, "  \x1b[38;5;196m✖ re-index failed: %v\x1b[0m\n", ierr)
			return
		}
		passes++
		updated += n
		fmt.Fprintf(w, "  \x1b[1;38;5;82m✔\x1b[0m \x1b[38;5;246m%s —\x1b[0m %d file(s) updated\n",
			time.Now().Format("15:04:05"), n)
		// Flush so a piped log shows every update immediately instead of in
		// one block at exit.
		_ = os.Stdout.Sync()
	}

	// schedule (re)arms the single debounce timer, mirroring the watch
	// command's pattern: a burst of writes collapses into one pass.
	schedule := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil && timer.Stop() {
			// The replaced timer never fires, so it never calls Done.
			wg.Done()
		}
		wg.Add(1)
		timer = time.AfterFunc(debounce, func() {
			defer wg.Done()
			reindex()
		})
	}

	for {
		select {
		case <-ctx.Done():
			drain()
			fmt.Fprint(w, "\n")
			fmt.Fprintf(w, "\x1b[1;38;5;82m✔ Stopped continuous indexing\x1b[0m \x1b[38;5;246m(%d pass(es), %d file(s) updated)\x1b[0m\n", passes, updated)
			return nil

		case event, ok := <-fsw.Events:
			if !ok {
				return nil
			}

			// A new directory starts arriving as a single event for itself, so
			// watch the whole subtree it brought with it. Exclusions are matched
			// against the original watch root, exactly as the indexer walk does.
			if event.Has(fsnotify.Create) {
				if st, serr := os.Stat(event.Name); serr == nil && st.IsDir() {
					if err := addWatchDirs(fsw, store, root, event.Name, except); err == nil {
						schedule()
					}
					continue
				}
			}

			// Removals and renames matter too: IndexDirectory purges rows for
			// files that vanished, so the cache never claims a deleted file
			// still exists.
			if !(event.Has(fsnotify.Create) || event.Has(fsnotify.Write) ||
				event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) {
				continue
			}

			schedule()

		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(w, "  \x1b[38;5;196m✖ watch error: %v\x1b[0m\n", err)
		}
	}
}

// addWatchDirs registers dir and every directory beneath it with fsnotify.
// Exclusions are matched against watchRoot (the folder the user asked to watch)
// so a monitored directory is never one the index would skip anyway, and hidden
// directories are skipped exactly as the indexer walk skips them.
func addWatchDirs(fsw *fsnotify.Watcher, store *indexer.Store, watchRoot, dir string, except []string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable or vanished subdirectory must not abort the whole
			// watch; the indexer skips it too.
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != dir {
			name := d.Name()
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if store.IsExcluded(name, watchRoot, path, except) {
				return filepath.SkipDir
			}
		}
		return fsw.Add(path)
	})
}
