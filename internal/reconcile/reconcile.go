// Package reconcile keeps three representations of a note coherent: the
// CRDT document (merge state), the .md file (durable text), and the SQLite
// index (derived). Clients mutate the document; editors and scripts mutate
// the file; this package moves changes between the two and reindexes, and
// it must never oscillate.
//
// Write-back (document → file): a mutation marks the note dirty and resets
// an idle timer. When it fires, the body is rendered, the frontmatter block
// prepended, the result hashed and, unless it matches the last hash the
// note was written or read with, written atomically. The hash is recorded.
//
// Read-in (file → document): the watcher reports a changed path. The file
// is read and hashed; a hash equal to the recorded one is the note's own
// write-back echoing through the watcher and is ignored. Anything else is
// an external edit: the file text is diffed against the text the file
// last held and the difference is applied to the document as operations
// with author "filesystem", so characters the editor left alone keep their
// identity and concurrent client typing survives the merge.
//
// The "text the file last held" is a second document, the shadow, which
// trails the main document by exactly the edits that have not reached the
// file yet. It is what makes an external append land next to concurrent
// client typing instead of replacing it.
//
// Every write-back, read-in, and echo suppression is logged with the note
// id and the hashes involved. When this loop misbehaves those lines are the
// only way to see why.
package reconcile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/spaces"
	"github.com/madeofpendletonwool/yana/internal/ydoc"
)

// AuthorFilesystem tags every operation that originated as a file edit.
const AuthorFilesystem = "filesystem"

// Options tune one reconciler. Zero values take the defaults noted.
type Options struct {
	// IdleTime is how long a note must go without a mutation before its
	// document is written to disk (2s).
	IdleTime time.Duration
	// MaxDirty bounds how long a continuously edited note can go unwritten
	// (10 × IdleTime).
	MaxDirty time.Duration
	// Debounce collapses bursts of watcher events on one path (200ms).
	Debounce time.Duration
	// SettleTime is how long a file must be unchanged before an id-less or
	// vanished file is believed (2s). It absorbs truncate+write editors and
	// gives a move time to show both halves.
	SettleTime time.Duration
	// CompactAfter is the log length that triggers a snapshot (500).
	CompactAfter int
	// Retention is how long a deleted note is kept (30 days): the
	// retained sidecar and the .trash copy both sweep after it.
	Retention time.Duration
	// UnloadAfter drops an idle, clean, unpinned document from memory
	// (15m). Negative disables unloading.
	UnloadAfter time.Duration
	// OnTreeChange, when set, is invoked for every path the watcher (or
	// Sync) reports, before any filtering, so the git history layer can
	// track tree activity. It must not block.
	OnTreeChange func(rel string)
	// OnSpaceMembersChanged, when set, is invoked whenever a space's
	// cached membership changes (a .space.yml edit, removal, or a
	// vanished space directory). The realtime relay uses it to sever
	// subscriptions that no longer pass. It must not block.
	OnSpaceMembersChanged func(space string)
	// Now is the clock.
	Now func() time.Time
}

func (o *Options) defaults() {
	if o.IdleTime == 0 {
		o.IdleTime = 2 * time.Second
	}
	if o.MaxDirty == 0 {
		o.MaxDirty = 10 * o.IdleTime
	}
	if o.Debounce == 0 {
		o.Debounce = 200 * time.Millisecond
	}
	if o.SettleTime == 0 {
		o.SettleTime = 2 * time.Second
	}
	if o.CompactAfter == 0 {
		o.CompactAfter = 500
	}
	if o.Retention == 0 {
		o.Retention = 30 * 24 * time.Hour
	}
	if o.UnloadAfter == 0 {
		o.UnloadAfter = 15 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// EventKind says what happened to a note.
type EventKind string

const (
	// EventUpdate carries a CRDT update applied to the note.
	EventUpdate EventKind = "update"
	// EventMoved reports a new path for the note.
	EventMoved EventKind = "moved"
	// EventDeleted reports that the note's file is gone.
	EventDeleted EventKind = "deleted"
	// EventChanged reports that a note nobody had open changed on disk
	// and the index caught up: no document, no payload, just the fact.
	// Listing pages (the tasks page) refetch on it.
	EventChanged EventKind = "changed"
)

// Event is delivered to subscribers for every change to a loaded note.
type Event struct {
	NoteID string
	Kind   EventKind
	Path   string
	// Update is the incremental V1 update for EventUpdate.
	Update []byte
	Author string
	// Source is whatever the caller of ApplyUpdate passed so that a relay
	// can skip the connection the update came from. Nil for file edits.
	Source any
}

// Stats is a snapshot of the loop's counters.
type Stats struct {
	Loaded     int   `json:"loaded"`
	Dirty      int   `json:"dirty"`
	Writebacks int64 `json:"writebacks"`
	Readins    int64 `json:"readins"`
	Echoes     int64 `json:"echoes"`
	Events     int64 `json:"watch_events"`
	WatchDirs  int   `json:"watch_dirs"`
	Watching   bool  `json:"watching"`
}

// ErrNotFound is returned when a note id is not in the index.
var ErrNotFound = index.ErrNotFound

// Reconciler is the loop.
type Reconciler struct {
	root *pathsafe.Root
	db   *index.DB
	sc   *scanner.Scanner
	opts Options
	log  *slog.Logger
	base *slog.Logger // component-less, for the watcher's own lines

	mu     sync.Mutex
	notes  map[string]*note  // by id, loaded documents only
	byPath map[string]string // rel path → id for loaded documents
	closed bool
	// loadMu serialises loads so two openers of a fresh note cannot both
	// record its first update.
	loadMu sync.Mutex

	subMu sync.RWMutex
	subs  map[int]func(Event)
	subID int

	watch *watcher

	// testBeforeWrite, when set, runs between the pre-write read and the
	// rename so tests can race a write into that window.
	testBeforeWrite func()

	writebacks, readins, echoes atomic.Int64

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// note is one loaded document.
type note struct {
	id string
	mu sync.Mutex

	rel  string
	head []byte // frontmatter block, prepended on write-back
	mode os.FileMode

	main   *ydoc.Doc // merge state
	shadow *ydoc.Doc // what the file holds, as a document

	// lastHash is the sha256 of the file content the note was last
	// written with or read from. A watcher event whose content hashes to
	// this is the note's own echo.
	lastHash  string
	lastMTime time.Time

	seq     int64
	logRows int

	dirty      bool
	dirtySince time.Time
	timer      *time.Timer
	lastUsed   time.Time
	pins       int
	gone       bool // file deleted; the note is retired
	unloaded   bool // dropped from memory while idle; reload to use
}

// New builds a reconciler. sc is used to reindex files after every change.
func New(root *pathsafe.Root, db *index.DB, sc *scanner.Scanner, opts Options, log *slog.Logger) *Reconciler {
	opts.defaults()
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Reconciler{
		root:   root,
		db:     db,
		sc:     sc,
		opts:   opts,
		log:    log.With("component", "reconcile"),
		base:   log,
		notes:  map[string]*note{},
		byPath: map[string]string{},
		subs:   map[int]func(Event){},
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start creates the sidecar directories, starts the filesystem watcher and
// the maintenance sweeps. A watcher that cannot be created is reported and
// the loop runs write-back only; external edits are then picked up when a
// note is opened or at the next full scan.
func (r *Reconciler) Start() error {
	if err := fsutil.MkdirInherit(r.retiredDir()); err != nil {
		return fmt.Errorf("create %s: %w", r.retiredDir(), err)
	}
	w, err := newWatcher(r)
	if err != nil {
		r.log.Error("filesystem watcher unavailable; external edits are picked up on open and on scan", "err", err)
	} else {
		r.watch = w
		r.wg.Add(2)
		go func() { defer r.wg.Done(); w.collect(r.ctx) }()
		go func() { defer r.wg.Done(); w.process(r.ctx) }()
	}
	r.wg.Add(1)
	go func() { defer r.wg.Done(); r.maintain(r.ctx) }()
	return nil
}

// Close writes every dirty note, stops the watcher, and releases the
// documents. Further calls fail with ErrClosed.
func (r *Reconciler) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errClosed
	}
	r.closed = true
	r.mu.Unlock()
	r.cancel()
	if r.watch != nil {
		r.watch.close()
	}
	r.wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.FlushAll(ctx)
	r.mu.Lock()
	notes := make([]*note, 0, len(r.notes))
	for _, n := range r.notes {
		notes = append(notes, n)
	}
	r.notes = map[string]*note{}
	r.byPath = map[string]string{}
	r.mu.Unlock()
	for _, n := range notes {
		n.mu.Lock()
		n.unloaded = true
		if n.timer != nil {
			n.timer.Stop()
		}
		n.main.Close()
		n.shadow.Close()
		n.mu.Unlock()
	}
	return nil
}

var errClosed = errors.New("reconcile: closed")

// Subscribe registers fn for every event on every loaded note. fn runs on
// the goroutine that produced the event, with that note locked: it must
// not block and must not call back into the Reconciler. Hand the event to
// a channel or a goroutine instead. The returned function removes the
// subscription.
func (r *Reconciler) Subscribe(fn func(Event)) func() {
	r.subMu.Lock()
	r.subID++
	id := r.subID
	r.subs[id] = fn
	r.subMu.Unlock()
	return func() {
		r.subMu.Lock()
		delete(r.subs, id)
		r.subMu.Unlock()
	}
}

func (r *Reconciler) emit(ev Event) {
	r.subMu.RLock()
	defer r.subMu.RUnlock()
	for _, fn := range r.subs {
		fn(ev)
	}
}

// Retention is how long a deleted note stays recoverable.
func (r *Reconciler) Retention() time.Duration { return r.opts.Retention }

// Stats returns the loop's counters.
func (r *Reconciler) Stats() Stats {
	r.mu.Lock()
	notes := make([]*note, 0, len(r.notes))
	for _, n := range r.notes {
		notes = append(notes, n)
	}
	r.mu.Unlock()
	s := Stats{Loaded: len(notes)}
	for _, n := range notes {
		n.mu.Lock()
		if n.dirty {
			s.Dirty++
		}
		n.mu.Unlock()
	}
	s.Writebacks = r.writebacks.Load()
	s.Readins = r.readins.Load()
	s.Echoes = r.echoes.Load()
	if r.watch != nil {
		s.Watching = true
		s.Events = r.watch.events.Load()
		s.WatchDirs = r.watch.dirCount()
	}
	return s
}

// --- public document operations ------------------------------------------

// Pin keeps a note's document loaded until the returned release function
// is called. Relays pin the notes they have subscribers for so file edits
// keep reaching them.
func (r *Reconciler) Pin(ctx context.Context, id string) (func(), error) {
	n, err := r.acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	n.pins++
	n.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			n.mu.Lock()
			n.pins--
			n.mu.Unlock()
		})
	}, nil
}

// State returns the full document as one V1 update.
func (r *Reconciler) State(ctx context.Context, id string) ([]byte, error) {
	n, err := r.acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	defer n.mu.Unlock()
	return n.main.State(), nil
}

// Diff returns what a peer holding sv is missing; nil sv is the full state.
func (r *Reconciler) Diff(ctx context.Context, id string, sv []byte) ([]byte, error) {
	n, err := r.acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	defer n.mu.Unlock()
	return n.main.Diff(sv)
}

// Text returns the current body of the document (not the file).
func (r *Reconciler) Text(ctx context.Context, id string) (string, error) {
	n, err := r.acquire(ctx, id)
	if err != nil {
		return "", err
	}
	defer n.mu.Unlock()
	return n.main.Text(), nil
}

// Path returns the note's current relative path.
func (r *Reconciler) Path(ctx context.Context, id string) (string, error) {
	n, err := r.acquire(ctx, id)
	if err != nil {
		return "", err
	}
	defer n.mu.Unlock()
	return n.rel, nil
}

// ApplyUpdate integrates a client's update, records it in the log under
// author, marks the note dirty, and tells subscribers. source is handed to
// subscribers untouched. An update the document already contained is a
// no-op.
func (r *Reconciler) ApplyUpdate(ctx context.Context, id string, update []byte, author string, source any) error {
	if author == "" {
		return errors.New("reconcile: update author is empty")
	}
	n, err := r.acquire(ctx, id)
	if err != nil {
		return err
	}
	defer n.mu.Unlock()
	re, err := n.main.Apply(update, author)
	if err != nil {
		return fmt.Errorf("apply update to %s: %w", id, err)
	}
	if re == nil {
		return nil
	}
	if err := r.record(ctx, n, re, author); err != nil {
		return err
	}
	r.markDirty(n)
	r.emit(Event{NoteID: id, Kind: EventUpdate, Path: n.rel, Update: re, Author: author, Source: source})
	return nil
}

// SetText replaces the body with text, expressed as the minimal edit, under
// author. It is the path agents and tests use to edit without a Yjs client.
func (r *Reconciler) SetText(ctx context.Context, id, text, author string) error {
	if author == "" {
		return errors.New("reconcile: update author is empty")
	}
	n, err := r.acquire(ctx, id)
	if err != nil {
		return err
	}
	defer n.mu.Unlock()
	u := n.main.SetText(text, author)
	if u == nil {
		return nil
	}
	if err := r.record(ctx, n, u, author); err != nil {
		return err
	}
	r.markDirty(n)
	r.emit(Event{NoteID: id, Kind: EventUpdate, Path: n.rel, Update: u, Author: author})
	return nil
}

// Flush writes the note to disk now if it is dirty.
func (r *Reconciler) Flush(ctx context.Context, id string) error {
	r.mu.Lock()
	n := r.notes[id]
	r.mu.Unlock()
	if n == nil {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.dirty || n.gone || n.unloaded {
		return nil
	}
	return r.writeBack(ctx, n)
}

// FlushAll writes every dirty note now.
func (r *Reconciler) FlushAll(ctx context.Context) {
	r.mu.Lock()
	ids := make([]string, 0, len(r.notes))
	for id := range r.notes {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	for _, id := range ids {
		if err := r.Flush(ctx, id); err != nil {
			r.log.Error("write-back failed", "id", id, "err", err)
		}
	}
}

// Sync reports the path change for a file the watcher saw; tests and the
// deferred-scan retry use it to drive the loop without fsnotify.
func (r *Reconciler) Sync(ctx context.Context, rel string) {
	r.processPath(ctx, rel)
}

// --- loading --------------------------------------------------------------

// acquire returns the note for id with n.mu held. A note unloaded between
// lookup and lock is reloaded.
func (r *Reconciler) acquire(ctx context.Context, id string) (*note, error) {
	for {
		n, err := r.open(ctx, id)
		if err != nil {
			return nil, err
		}
		n.mu.Lock()
		if n.unloaded {
			n.mu.Unlock()
			continue
		}
		if n.gone {
			n.mu.Unlock()
			return nil, ErrNotFound
		}
		n.lastUsed = r.opts.Now()
		return n, nil
	}
}

// open returns the loaded document for id, loading it if needed.
//
// Lock order everywhere in this package: a note's mu before r.mu, never
// the other way round.
func (r *Reconciler) open(ctx context.Context, id string) (*note, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errClosed
	}
	if n := r.notes[id]; n != nil {
		r.mu.Unlock()
		return n, nil
	}
	r.mu.Unlock()

	r.loadMu.Lock()
	r.mu.Lock()
	if n := r.notes[id]; n != nil {
		r.mu.Unlock()
		r.loadMu.Unlock()
		return n, nil
	}
	r.mu.Unlock()
	n, needsWrite, err := r.load(ctx, id)
	r.loadMu.Unlock()
	if err != nil {
		return nil, err
	}
	discard := func() {
		n.gone = true
		n.main.Close()
		n.shadow.Close()
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		discard()
		return nil, errClosed
	}
	if existing := r.notes[id]; existing != nil {
		// Lost the race to another loader; keep theirs.
		r.mu.Unlock()
		discard()
		return existing, nil
	}
	var displaced *note
	if other, ok := r.byPath[n.rel]; ok && other != id {
		// The index moved this path to a new id under us.
		displaced = r.notes[other]
	}
	r.notes[id] = n
	r.byPath[n.rel] = id
	r.mu.Unlock()
	if displaced != nil {
		r.retire(ctx, displaced, "", "path now belongs to another note")
	}
	if needsWrite {
		n.mu.Lock()
		r.markDirty(n)
		n.mu.Unlock()
	}
	return n, nil
}

// load builds a note from the sidecar, the log, and the file, and brings
// the document in line with the file.
func (r *Reconciler) load(ctx context.Context, id string) (n *note, needsWrite bool, err error) {
	row, err := r.db.GetNote(ctx, id)
	if err != nil {
		return nil, false, err
	}
	abs, rel, err := r.root.Resolve(row.RelPath)
	if err != nil {
		return nil, false, err
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return nil, false, err
	}
	fm := frontmatter.Parse(content)
	if fm.Meta.ID != id {
		return nil, false, fmt.Errorf("%s holds id %q, index says %q; the index is stale", rel, fm.Meta.ID, id)
	}
	mode := os.FileMode(0o644)
	var mtime time.Time
	if info, err := os.Stat(abs); err == nil {
		mode = info.Mode().Perm()
		mtime = info.ModTime()
	}
	now := r.opts.Now()
	n = &note{id: id, rel: rel, head: fm.Head, mode: mode, lastUsed: now}

	base, err := r.readSidecar(id)
	if err != nil {
		return nil, false, err
	}
	if n.shadow, err = ydoc.Load(base); err != nil {
		r.log.Warn("sidecar unreadable; rebuilding the document from the log and the file", "id", id, "err", err)
		base = nil
		n.shadow = ydoc.New()
	}
	if n.main, err = ydoc.Load(base); err != nil {
		n.main = ydoc.New()
	}
	snap, hasSnap, err := r.db.GetSnapshot(ctx, id)
	if err != nil {
		return nil, false, err
	}
	var logged [][]byte
	if hasSnap {
		if _, err := n.main.Apply(snap.Doc, "log"); err != nil {
			r.log.Warn("snapshot unreadable; skipping it", "id", id, "err", err)
		}
		n.seq = snap.Seq
	}
	updates, err := r.db.Updates(ctx, id, n.seq)
	if err != nil {
		return nil, false, err
	}
	for _, u := range updates {
		if _, err := n.main.Apply(u.Payload, "log"); err != nil {
			r.log.Warn("logged update unreadable; skipping it", "id", id, "seq", u.Seq, "err", err)
			continue
		}
		logged = append(logged, u.Payload)
		n.seq = u.Seq
	}
	n.logRows = len(updates)

	body := string(fm.Body)
	hash := hashOf(content)
	fresh := base == nil && !hasSnap && len(updates) == 0
	if base == nil && !fresh {
		// The log survived but the sidecar did not, so there is no record
		// of what the file last held. Diffing from the document itself is
		// the safe fallback: the file wins, expressed as the minimal edit.
		r.syncShadow(n)
	}
	switch {
	case fresh:
		// First sight of this note as a document: the file is its whole
		// history. The creation op is stamped with the file's mtime, not
		// now: it says when that content was written, so a later
		// attribution window does not mistake the load itself for an
		// edit.
		u := n.main.SetText(body, AuthorFilesystem)
		if u != nil {
			if _, err := n.shadow.Apply(u, "load"); err != nil {
				return nil, false, err
			}
			if mtime.IsZero() {
				mtime = r.opts.Now()
			}
			if err := r.recordAt(ctx, n, u, AuthorFilesystem, mtime); err != nil {
				return nil, false, err
			}
		}
		n.lastHash = hash
		if err := r.writeSidecar(n); err != nil {
			return nil, false, err
		}
		r.log.Info("document created from file", "id", id, "path", rel, "file_hash", hash, "bytes", len(content))
	case body == n.shadow.Text():
		// The file is exactly the note's last write-back. Anything in the
		// log beyond that has not reached the file yet.
		n.lastHash = hash
		needsWrite = n.main.Text() != body
		r.log.Info("document loaded", "id", id, "path", rel, "file_hash", hash, "dirty", needsWrite)
	case body == n.main.Text():
		// A write-back reached the file but the sidecar was not rewritten
		// (the process died in between). The document is the file.
		n.lastHash = hash
		r.syncShadow(n)
		r.log.Info("document loaded; file matches the log", "id", id, "path", rel, "file_hash", hash)
	case r.rewindToFile(n, base, logged, body):
		// The file is an intermediate logged state (the process died after
		// writing it and before the sidecar). Later log entries are edits
		// that have not reached the file yet.
		n.lastHash = hash
		needsWrite = true
		r.log.Info("document loaded; file matches an earlier logged state", "id", id, "path", rel, "file_hash", hash)
	default:
		// The file changed while the note was not loaded.
		needsWrite = r.readIn(ctx, n, content, "load")
	}
	return n, needsWrite, nil
}

// rewindToFile replays the log on a copy of the sidecar state one update
// at a time looking for the state whose text equals body. When found, the
// shadow is set to that state and true is returned.
func (r *Reconciler) rewindToFile(n *note, base []byte, logged [][]byte, body string) bool {
	if len(logged) == 0 {
		return false
	}
	scratch, err := ydoc.Load(base)
	if err != nil {
		return false
	}
	defer scratch.Close()
	for _, u := range logged {
		if _, err := scratch.Apply(u, "rewind"); err != nil {
			return false
		}
		if scratch.Text() == body {
			n.shadow.Close()
			n.shadow, err = ydoc.Load(scratch.State())
			if err != nil {
				n.shadow = ydoc.New()
				return false
			}
			return true
		}
	}
	return false
}

// syncShadow brings the shadow up to the main document.
func (r *Reconciler) syncShadow(n *note) {
	d, err := n.main.Diff(n.shadow.StateVector())
	if err != nil {
		return
	}
	if _, err := n.shadow.Apply(d, "sync"); err != nil {
		r.log.Warn("shadow sync failed", "id", n.id, "err", err)
	}
}

// --- write-back ------------------------------------------------------------

// markDirty flags the note and (re)starts its idle timer. n.mu is held.
func (r *Reconciler) markDirty(n *note) {
	now := r.opts.Now()
	if !n.dirty {
		n.dirty = true
		n.dirtySince = now
	}
	if n.timer == nil {
		n.timer = time.AfterFunc(r.opts.IdleTime, func() { r.fire(n) })
		return
	}
	if now.Sub(n.dirtySince) < r.opts.MaxDirty {
		n.timer.Reset(r.opts.IdleTime)
	}
}

func (r *Reconciler) fire(n *note) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.dirty || n.gone || n.unloaded {
		return
	}
	if err := r.writeBack(ctx, n); err != nil {
		r.log.Error("write-back failed; will retry", "id", n.id, "path", n.rel, "err", err)
		n.timer.Reset(r.opts.IdleTime)
	}
}

// writeBack renders the document and writes the file if it changed. n.mu
// is held.
func (r *Reconciler) writeBack(ctx context.Context, n *note) error {
	if n.gone {
		n.dirty = false
		return nil
	}
	abs, _, err := r.root.Resolve(n.rel)
	if err != nil {
		return err
	}
	old, seen, wait := r.absorbUnread(ctx, n, abs)
	if wait {
		// The file is mid-change; try again after another idle window.
		if n.timer != nil {
			n.timer.Reset(r.opts.IdleTime)
		}
		return nil
	}
	if old != nil {
		defer old.Close()
	}
	body := n.main.Text()
	content := make([]byte, 0, len(n.head)+len(body))
	content = append(content, n.head...)
	content = append(content, body...)
	hash := hashOf(content)
	n.dirty = false
	if hash == n.lastHash {
		r.log.Debug("write-back skipped; file already holds this text", "id", n.id, "path", n.rel, "hash", hash)
		return r.compact(ctx, n)
	}
	if r.testBeforeWrite != nil {
		r.testBeforeWrite()
	}
	if err := fsutil.WriteFileAtomic(abs, content, n.mode); err != nil {
		n.dirty = true
		return err
	}
	prev := n.lastHash
	n.lastHash = hash
	if info, err := os.Stat(abs); err == nil {
		n.lastMTime = info.ModTime()
	}
	var preSync []byte
	if old != nil {
		// What the file held before this write, as a document; the base
		// for merging a write that raced the rename.
		preSync = n.shadow.State()
	}
	r.syncShadow(n)
	if err := r.writeSidecar(n); err != nil {
		r.log.Error("sidecar write failed", "id", n.id, "err", err)
	}
	r.writebacks.Add(1)
	r.log.Info("write-back", "id", n.id, "path", n.rel, "hash", hash, "prev_hash", prev, "bytes", len(content))
	r.reindex(ctx, n.rel)
	if r.recoverLateWrite(ctx, n, old, seen, preSync) {
		r.markDirty(n)
	}
	return r.compact(ctx, n)
}

// absorbUnread looks at the file right before a write-back and reads in
// any external change the watcher has not delivered yet, so the write
// never overwrites an edit it has not seen. It reports wait=true when the
// write-back should hold off: the file is missing (a move or a delete the
// watcher will sort out), belongs to another note, or is in the transient
// empty state a truncate+write editor leaves.
//
// On success it returns the open file (the inode the rename is about to
// replace) and the bytes it read, for recoverLateWrite. n.mu is held.
func (r *Reconciler) absorbUnread(ctx context.Context, n *note, abs string) (old *os.File, seen []byte, wait bool) {
	f, err := os.Open(abs)
	if errors.Is(err, fs.ErrNotExist) {
		if r.watch == nil {
			// Nobody will report the deletion; recreate the file.
			return nil, nil, false
		}
		r.log.Debug("write-back deferred; file is missing", "id", n.id, "path", n.rel)
		return nil, nil, true
	}
	if err != nil {
		r.log.Warn("write-back deferred; file unreadable", "id", n.id, "path", n.rel, "err", err)
		return nil, nil, true
	}
	content, err := io.ReadAll(f)
	if err != nil {
		f.Close()
		r.log.Warn("write-back deferred; file unreadable", "id", n.id, "path", n.rel, "err", err)
		return nil, nil, true
	}
	if runtime.GOOS == "windows" {
		// An open handle blocks the rename there; give up the late-write
		// check rather than the write.
		f.Close()
		f = nil
	}
	if hashOf(content) == n.lastHash {
		return f, content, false
	}
	fm := frontmatter.Parse(content)
	if fm.Meta.ID != n.id {
		if f != nil {
			f.Close()
		}
		r.log.Debug("write-back deferred; file holds another id", "id", n.id, "path", n.rel, "found", fm.Meta.ID)
		return nil, nil, true
	}
	if len(fm.Body) == 0 && len(n.main.Text()) > 0 {
		if info, err := os.Stat(abs); err == nil && r.opts.Now().Sub(info.ModTime()) < r.opts.SettleTime {
			if f != nil {
				f.Close()
			}
			r.log.Debug("write-back deferred; file momentarily empty", "id", n.id, "path", n.rel)
			return nil, nil, true
		}
	}
	r.readIn(ctx, n, content, "writeback")
	return f, content, false
}

// recoverLateWrite closes the last gap in write-back: a writer that
// appended to the old file between absorbUnread's read and the rename
// wrote into an inode nothing else points at any more. The handle from
// absorbUnread still does. Re-reading it and merging whatever appeared
// keeps that write. The change is diffed from preSync, the shadow as it
// was before the write (its text is what the writer saw), so only the
// writer's edit is applied. It reports whether anything was recovered.
// n.mu is held.
func (r *Reconciler) recoverLateWrite(ctx context.Context, n *note, old *os.File, seen, preSync []byte) bool {
	if old == nil {
		return false
	}
	if _, err := old.Seek(0, io.SeekStart); err != nil {
		return false
	}
	now, err := io.ReadAll(old)
	if err != nil || bytes.Equal(now, seen) {
		return false
	}
	fm := frontmatter.Parse(now)
	if fm.Meta.ID != n.id {
		return false
	}
	stale, err := ydoc.Load(preSync)
	if err != nil {
		return false
	}
	defer stale.Close()
	u := stale.SetText(string(fm.Body), AuthorFilesystem)
	if u == nil {
		return false
	}
	re, err := n.main.Apply(u, AuthorFilesystem)
	if err != nil {
		r.log.Error("late write: applying diff failed", "id", n.id, "err", err)
		return false
	}
	if re != nil {
		if err := r.record(ctx, n, re, AuthorFilesystem); err != nil {
			r.log.Error("late write: recording update failed", "id", n.id, "err", err)
		}
		r.emit(Event{NoteID: n.id, Kind: EventUpdate, Path: n.rel, Update: re, Author: AuthorFilesystem})
	}
	// The shadow stays as the file is; the next write-back brings the file
	// up to the merged text and syncs it then.
	r.readins.Add(1)
	r.log.Info("recovered a write that raced the write-back", "id", n.id, "path", n.rel, "bytes", len(now)-len(seen))
	return true
}

// --- read-in ---------------------------------------------------------------

// readIn absorbs external file content into the document and reports
// whether the document now differs from the file (client edits that had
// not reached the file merged with the external change). n.mu is held.
func (r *Reconciler) readIn(ctx context.Context, n *note, content []byte, via string) bool {
	fm := frontmatter.Parse(content)
	hash := hashOf(content)
	prev := n.lastHash
	n.head = fm.Head
	body := string(fm.Body)
	n.lastHash = hash
	u := n.shadow.SetText(body, AuthorFilesystem)
	if u == nil {
		r.log.Info("read-in: frontmatter only", "id", n.id, "path", n.rel, "file_hash", hash, "prev_hash", prev, "via", via)
		return n.main.Text() != body
	}
	re, err := n.main.Apply(u, AuthorFilesystem)
	if err != nil {
		r.log.Error("read-in: applying file diff failed", "id", n.id, "path", n.rel, "err", err)
		return false
	}
	if re != nil {
		if err := r.record(ctx, n, re, AuthorFilesystem); err != nil {
			r.log.Error("read-in: recording update failed", "id", n.id, "err", err)
		}
		r.emit(Event{NoteID: n.id, Kind: EventUpdate, Path: n.rel, Update: re, Author: AuthorFilesystem})
	}
	r.readins.Add(1)
	r.log.Info("read-in", "id", n.id, "path", n.rel, "file_hash", hash, "prev_hash", prev, "update_bytes", len(u), "via", via)
	return n.main.Text() != body
}

// --- log ---------------------------------------------------------------------

// record appends an update to the note's log. n.mu is held.
func (r *Reconciler) record(ctx context.Context, n *note, update []byte, author string) error {
	return r.recordAt(ctx, n, update, author, r.opts.Now())
}

// recordAt appends an update stamped with an explicit time. n.mu is held.
func (r *Reconciler) recordAt(ctx context.Context, n *note, update []byte, author string, ts time.Time) error {
	n.seq++
	seq := n.seq
	if err := r.db.Write(ctx, func(tx *sql.Tx) error {
		return index.AppendUpdate(tx, n.id, seq, update, author, ts)
	}); err != nil {
		n.seq--
		return fmt.Errorf("record update for %s: %w", n.id, err)
	}
	n.logRows++
	return nil
}

// compact snapshots the log once it is long. It runs right after a
// write-back, when the file, the sidecar, and the document agree, so the
// snapshot never gets ahead of the sidecar and crash recovery (see load)
// can replay the remaining rows from it. n.mu is held.
func (r *Reconciler) compact(ctx context.Context, n *note) error {
	if n.logRows < r.opts.CompactAfter {
		return nil
	}
	state := n.main.State()
	seq := n.seq
	ts := r.opts.Now()
	if err := r.db.Write(ctx, func(tx *sql.Tx) error {
		return index.WriteSnapshot(tx, n.id, seq, state, ts)
	}); err != nil {
		return fmt.Errorf("compact %s: %w", n.id, err)
	}
	n.logRows = 0
	r.log.Info("compacted log", "id", n.id, "seq", seq, "snapshot_bytes", len(state))
	return nil
}

// --- paths -------------------------------------------------------------------

// processPath handles one changed path from the watcher (or a retry).
func (r *Reconciler) processPath(ctx context.Context, rel string) {
	if r.opts.OnTreeChange != nil {
		r.opts.OnTreeChange(rel)
	}
	if spaces.IsFile(rel) {
		r.syncSpaceFile(ctx, rel)
		return
	}
	abs, rel, err := r.root.Resolve(rel)
	if err != nil {
		r.log.Debug("ignoring path", "path", rel, "err", err)
		return
	}
	if scanner.KindOf(rel) == "" || scanner.IsAsset(rel) {
		return
	}
	info, err := os.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		r.pathGone(ctx, rel)
		return
	}
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	if info.Size() > r.root.Limits().MaxNoteSize {
		r.log.Warn("note over size limit; not reconciled", "path", rel, "size", info.Size())
		return
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		r.log.Warn("cannot read changed file", "path", rel, "err", err)
		return
	}
	fm := frontmatter.Parse(content)
	if fm.Meta.ID == "" {
		r.idlessFile(ctx, rel, content, info)
		return
	}
	id := fm.Meta.ID

	r.mu.Lock()
	var overwritten *note
	if prevID, ok := r.byPath[rel]; ok && prevID != id {
		// The file at this path now carries a different id: the note that
		// lived here was overwritten.
		overwritten = r.notes[prevID]
	}
	n := r.notes[id]
	r.mu.Unlock()
	if overwritten != nil {
		r.retire(ctx, overwritten, rel, "file replaced by another note")
	}

	if n == nil {
		r.unloadedChanged(ctx, id, rel)
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if n.gone || n.unloaded {
		return
	}
	moved := false
	if n.rel != rel {
		if _, err := os.Lstat(filepath.Join(r.root.Dir(), filepath.FromSlash(n.rel))); err == nil {
			// Both paths exist: this is a copy, and the copy needs its own
			// id. The rewrite comes back through the watcher.
			r.log.Warn("duplicate note id; reassigning the copy", "id", id, "kept", n.rel, "copy", rel)
			if err := r.sc.ReassignID(rel); err != nil {
				r.log.Warn("could not reassign duplicate id", "path", rel, "err", err)
			}
			return
		}
		r.mu.Lock()
		delete(r.byPath, n.rel)
		r.byPath[rel] = id
		r.mu.Unlock()
		r.log.Info("note moved", "id", id, "from", n.rel, "to", rel)
		n.rel = rel
		moved = true
		r.emit(Event{NoteID: id, Kind: EventMoved, Path: rel})
	}
	n.lastUsed = r.opts.Now()

	// The file was read before this lock was taken, and a write-back may
	// have replaced it while the lock was waited for. Applying that
	// earlier copy would treat the loop's own newer write as an external
	// edit and undo fresh local edits, so re-read under the lock and use
	// what the file holds now.
	if fresh, rerr := os.ReadFile(abs); rerr != nil {
		r.log.Debug("cannot re-read at lock time; skipped", "id", id, "path", rel, "err", rerr)
		return
	} else if hashOf(fresh) != hashOf(content) {
		r.log.Debug("file changed again before the lock; using the newer bytes", "id", id, "path", rel)
		content = fresh
		fm = frontmatter.Parse(content)
		if info, err = os.Lstat(abs); err != nil {
			return
		}
		if fm.Meta.ID != id {
			// The path now holds a different note; the next watcher event
			// handles it rather than merging the wrong file in here.
			r.log.Warn("file replaced while waiting for the lock; leaving it for the next event", "id", id, "path", rel)
			return
		}
	}
	hash := hashOf(content)
	if hash == n.lastHash {
		r.echoes.Add(1)
		n.lastMTime = info.ModTime()
		r.log.Debug("echo suppressed", "id", id, "path", rel, "hash", hash)
		if moved {
			r.reindex(ctx, rel)
		}
		return
	}
	if len(fm.Body) == 0 && len(n.main.Text()) > 0 && r.opts.Now().Sub(info.ModTime()) < r.opts.SettleTime {
		// A truncate+write editor shows an empty (or frontmatter-only)
		// file for a moment. Look again once it has settled.
		r.log.Debug("empty file at a note that had text; re-checking after settle", "id", id, "path", rel)
		r.retryLater(rel)
		return
	}
	if r.readIn(ctx, n, content, "watch") {
		r.markDirty(n)
	}
	r.reindex(ctx, rel)
}

// idlessFile handles a file with no id: a new note, or a known note that a
// truncate+write editor has momentarily emptied.
func (r *Reconciler) idlessFile(ctx context.Context, rel string, content []byte, info fs.FileInfo) {
	r.mu.Lock()
	prevID, known := r.byPath[rel]
	r.mu.Unlock()
	if known && r.opts.Now().Sub(info.ModTime()) < r.opts.SettleTime {
		r.log.Debug("id-less file at a known note; re-checking after settle", "id", prevID, "path", rel, "bytes", len(content))
		r.retryLater(rel)
		return
	}
	// The scanner assigns the id (respecting the settle time) and indexes
	// the file; its rewrite comes back through the watcher as a normal
	// change.
	err := r.sc.ScanOne(ctx, rel)
	switch {
	case err == nil:
	case scanner.IsDeferred(err):
		r.retryLater(rel)
		return
	default:
		r.log.Warn("new file could not be indexed", "path", rel, "err", err)
		return
	}
	if known {
		// The settled file has no id and a fresh one was assigned; the
		// note that lived here is gone.
		r.mu.Lock()
		o := r.notes[prevID]
		r.mu.Unlock()
		if o != nil {
			r.retire(ctx, o, rel, "file replaced by an id-less file")
		}
	}
}

// unloadedChanged reindexes a changed file whose document is not loaded.
// The document catches up with the file when it is next opened.
func (r *Reconciler) unloadedChanged(ctx context.Context, id, rel string) {
	known, err := r.db.GetNote(ctx, id)
	if err == nil && known.RelPath != rel {
		if _, err := os.Lstat(filepath.Join(r.root.Dir(), filepath.FromSlash(known.RelPath))); err == nil {
			r.log.Warn("duplicate note id; reassigning the copy", "id", id, "kept", known.RelPath, "copy", rel)
			if err := r.sc.ReassignID(rel); err != nil {
				r.log.Warn("could not reassign duplicate id", "path", rel, "err", err)
			}
			return
		}
		r.log.Info("note moved", "id", id, "from", known.RelPath, "to", rel)
	}
	if r.restoreRetired(id) {
		r.log.Info("note returned; sidecar restored from retention", "id", id, "path", rel)
	}
	r.reindex(ctx, rel)
	r.emit(Event{NoteID: id, Kind: EventChanged, Path: rel})
}

// pathGone handles a path the watcher reported that no longer exists. A
// move shows as a removal at one path and a creation at another; the
// settle delay gives the second half time to arrive before the note is
// treated as deleted.
func (r *Reconciler) pathGone(ctx context.Context, rel string) {
	r.mu.Lock()
	id, loaded := r.byPath[rel]
	r.mu.Unlock()
	if !loaded {
		// Not loaded: the index row is retired unless the file moved and
		// its new path was indexed first (UpsertNote follows the id).
		r.reindex(ctx, rel)
		r.emit(Event{Kind: EventChanged, Path: rel})
		return
	}
	r.log.Debug("note file missing; confirming after settle", "id", id, "path", rel)
	time.AfterFunc(r.opts.SettleTime, func() {
		if r.ctx.Err() != nil {
			return
		}
		r.confirmGone(context.Background(), id, rel)
	})
}

func (r *Reconciler) confirmGone(ctx context.Context, id, rel string) {
	if _, err := os.Lstat(filepath.Join(r.root.Dir(), filepath.FromSlash(rel))); err == nil {
		r.processPath(ctx, rel)
		return
	}
	r.mu.Lock()
	n := r.notes[id]
	r.mu.Unlock()
	if n == nil {
		return
	}
	if r.retire(ctx, n, rel, "file removed") {
		r.reindex(ctx, rel)
	}
}

// retire drops a loaded note whose file is gone. Its document is saved to
// the retention area so an accidental rm is recoverable. When atPath is
// set the note is only retired if that is still its path. It reports
// whether the note was retired.
func (r *Reconciler) retire(ctx context.Context, n *note, atPath, why string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.gone || n.unloaded || (atPath != "" && n.rel != atPath) {
		return false
	}
	n.gone = true
	n.dirty = false
	if n.timer != nil {
		n.timer.Stop()
	}
	if err := r.retireSidecar(n); err != nil {
		r.log.Error("could not retire sidecar", "id", n.id, "err", err)
	}
	r.mu.Lock()
	if r.notes[n.id] == n {
		delete(r.notes, n.id)
	}
	if r.byPath[n.rel] == n.id {
		delete(r.byPath, n.rel)
	}
	r.mu.Unlock()
	r.log.Info("note deleted", "id", n.id, "path", n.rel, "reason", why)
	r.emit(Event{NoteID: n.id, Kind: EventDeleted, Path: n.rel})
	n.main.Close()
	n.shadow.Close()
	return true
}

// reindex refreshes the index row for a path from the file on disk (or
// retires it when the file is gone).
func (r *Reconciler) reindex(ctx context.Context, rel string) {
	err := r.sc.ScanOne(ctx, rel)
	switch {
	case err == nil:
	case scanner.IsDeferred(err):
		r.retryLater(rel)
	default:
		r.log.Warn("reindex failed", "path", rel, "err", err)
	}
}

// retryLater looks at a path again once the settle time has passed.
func (r *Reconciler) retryLater(rel string) {
	time.AfterFunc(r.opts.SettleTime+50*time.Millisecond, func() {
		if r.ctx.Err() != nil {
			return
		}
		if r.watch != nil {
			r.watch.enqueue(rel)
			return
		}
		r.processPath(context.Background(), rel)
	})
}

// --- maintenance -------------------------------------------------------------

// maintain unloads idle documents and sweeps expired trash.
func (r *Reconciler) maintain(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	r.sweepRetired()
	r.sweepTrash()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.unloadIdle()
			r.sweepRetired()
			r.sweepTrash()
		}
	}
}

func (r *Reconciler) unloadIdle() {
	if r.opts.UnloadAfter < 0 {
		return
	}
	now := r.opts.Now()
	r.mu.Lock()
	notes := make([]*note, 0, len(r.notes))
	for _, n := range r.notes {
		notes = append(notes, n)
	}
	r.mu.Unlock()
	for _, n := range notes {
		n.mu.Lock()
		if n.gone || n.unloaded || n.dirty || n.pins > 0 || now.Sub(n.lastUsed) <= r.opts.UnloadAfter {
			n.mu.Unlock()
			continue
		}
		n.unloaded = true
		if n.timer != nil {
			n.timer.Stop()
		}
		r.mu.Lock()
		if r.notes[n.id] == n {
			delete(r.notes, n.id)
		}
		if r.byPath[n.rel] == n.id {
			delete(r.byPath, n.rel)
		}
		r.mu.Unlock()
		n.main.Close()
		n.shadow.Close()
		r.log.Debug("unloaded idle document", "id", n.id)
		n.mu.Unlock()
	}
}

// SweepOrphans retires sidecars whose id the index no longer has. Call it
// after a full scan, when the index is trustworthy.
func (r *Reconciler) SweepOrphans(ctx context.Context) {
	entries, err := os.ReadDir(r.crdtDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".bin")
		if !ok || e.IsDir() {
			continue
		}
		r.mu.Lock()
		_, loaded := r.notes[id]
		r.mu.Unlock()
		if loaded {
			continue
		}
		if _, err := r.db.GetNote(ctx, id); errors.Is(err, index.ErrNotFound) {
			if err := os.Rename(r.sidecarPath(id), r.retiredPath(id)); err == nil {
				// Retention counts from now, not from the last write-back.
				now := r.opts.Now()
				_ = os.Chtimes(r.retiredPath(id), now, now)
				r.log.Info("note deleted while the server was down; sidecar retired", "id", id)
			}
		}
	}
}

// --- helpers ------------------------------------------------------------------

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
