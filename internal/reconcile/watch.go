package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// watcher turns fsnotify events into debounced batches of relative paths.
// inotify is not recursive, so every directory in the tree gets its own
// watch and new directories are added as they appear.
type watcher struct {
	r   *Reconciler
	fsw *fsnotify.Watcher
	log *slog.Logger

	mu      sync.Mutex
	pending map[string]struct{}
	timer   *time.Timer
	dirs    map[string]struct{}
	batches chan []string
	limited bool

	events atomic.Int64
}

func newWatcher(r *Reconciler) (*watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, describeWatchErr(err)
	}
	w := &watcher{
		r:       r,
		fsw:     fsw,
		log:     r.base.With("component", "watch"),
		pending: map[string]struct{}{},
		dirs:    map[string]struct{}{},
		batches: make(chan []string, 64),
	}
	if err := w.addTree(r.root.Dir()); err != nil {
		fsw.Close()
		return nil, err
	}
	w.log.Info("watching", "root", r.root.Dir(), "dirs", len(w.dirs))
	return w, nil
}

func (w *watcher) close() { _ = w.fsw.Close() }

func (w *watcher) dirCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.dirs)
}

// addTree watches dir and every non-dot directory below it.
func (w *watcher) addTree(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != w.r.root.Dir() && strings.HasPrefix(d.Name(), ".") {
			return fs.SkipDir
		}
		return w.addDir(p)
	})
}

func (w *watcher) addDir(p string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.dirs[p]; ok {
		return nil
	}
	if err := w.fsw.Add(p); err != nil {
		if isWatchLimit(err) {
			if !w.limited {
				w.limited = true
				w.log.Error("inotify watch limit reached; directories beyond it are not watched. Raise fs.inotify.max_user_watches on the host (docs/deployment.md, inotify limits)",
					"dir", p, "watched", len(w.dirs), "err", err)
			}
			return nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	w.dirs[p] = struct{}{}
	return nil
}

func (w *watcher) forgetDir(p string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for d := range w.dirs {
		if d == p || strings.HasPrefix(d, p+string(filepath.Separator)) {
			delete(w.dirs, d)
		}
	}
}

// collect drains fsnotify and debounces paths into batches. It must never
// block on processing or the kernel queue overflows.
func (w *watcher) collect(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.events.Add(1)
			w.handle(ev)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				w.log.Warn("inotify queue overflowed; some changes were missed until the next scan")
				continue
			}
			w.log.Warn("watcher error", "err", err)
		}
	}
}

func (w *watcher) handle(ev fsnotify.Event) {
	rel, err := filepath.Rel(w.r.root.Dir(), ev.Name)
	if err != nil {
		return
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || hasDotSegment(rel) {
		return
	}
	// A rename or removal of a directory takes every note under it away
	// (or, for a rename, brings them back elsewhere with a Create).
	if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		if w.isDir(ev.Name) {
			w.forgetDir(ev.Name)
			w.enqueueUnder(rel)
			return
		}
	}
	if ev.Has(fsnotify.Create) {
		if info, err := os.Lstat(ev.Name); err == nil && info.IsDir() {
			if err := w.addTree(ev.Name); err != nil {
				w.log.Warn("cannot watch new directory", "dir", rel, "err", err)
			}
			w.enqueueTree(ev.Name)
			return
		}
	}
	if scanner.KindOf(rel) == "" || scanner.IsAsset(rel) {
		return
	}
	w.enqueue(rel)
}

func (w *watcher) isDir(p string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.dirs[p]
	return ok
}

// enqueue adds a path to the pending batch and (re)starts the debounce.
func (w *watcher) enqueue(rel string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending[rel] = struct{}{}
	if w.timer == nil {
		w.timer = time.AfterFunc(w.r.opts.Debounce, w.flush)
		return
	}
	w.timer.Reset(w.r.opts.Debounce)
}

// enqueueTree adds every note file under a new directory: files created
// before the watch existed produced no events.
func (w *watcher) enqueueTree(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(w.r.root.Dir(), p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if scanner.KindOf(rel) != "" && !scanner.IsAsset(rel) {
			w.enqueue(rel)
		}
		return nil
	})
}

// enqueueUnder queues every note the index or the loaded set knows under
// a directory that vanished.
func (w *watcher) enqueueUnder(dirRel string) {
	prefix := dirRel + "/"
	w.r.mu.Lock()
	for rel := range w.r.byPath {
		if strings.HasPrefix(rel, prefix) {
			w.enqueue(rel)
		}
	}
	w.r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	notes, err := w.r.db.ListNotes(ctx, "")
	if err != nil {
		return
	}
	for _, n := range notes {
		if strings.HasPrefix(n.RelPath, prefix) {
			w.enqueue(n.RelPath)
		}
	}
}

func (w *watcher) flush() {
	w.mu.Lock()
	batch := make([]string, 0, len(w.pending))
	for rel := range w.pending {
		batch = append(batch, rel)
	}
	w.pending = map[string]struct{}{}
	w.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	select {
	case w.batches <- batch:
	default:
		// Processing is behind; fold the batch back in and try again
		// after another debounce interval.
		w.mu.Lock()
		for _, rel := range batch {
			w.pending[rel] = struct{}{}
		}
		w.timer.Reset(w.r.opts.Debounce)
		w.mu.Unlock()
	}
}

// process handles batches. Paths whose files exist are handled before
// paths whose files are gone, so a move's destination is known before its
// source is judged.
func (w *watcher) process(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case batch := <-w.batches:
			var gone []string
			for _, rel := range batch {
				if _, err := os.Lstat(filepath.Join(w.r.root.Dir(), filepath.FromSlash(rel))); err != nil {
					gone = append(gone, rel)
					continue
				}
				w.r.processPath(ctx, rel)
			}
			for _, rel := range gone {
				w.r.processPath(ctx, rel)
			}
		}
	}
}

func hasDotSegment(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// isWatchLimit reports whether err is the kernel refusing another watch
// (max_user_watches) or another inotify instance (max_user_instances).
func isWatchLimit(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE)
}

func describeWatchErr(err error) error {
	if isWatchLimit(err) {
		return errors.New("inotify instance limit reached; raise fs.inotify.max_user_instances on the host (docs/deployment.md, inotify limits): " + err.Error())
	}
	return err
}

// dropLog deletes a note's log rows and snapshot.
func (r *Reconciler) dropLog(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.db.Write(ctx, func(tx *sql.Tx) error { return index.DeleteLog(tx, id) }); err != nil {
		r.log.Warn("could not delete log", "id", id, "err", err)
	}
}
