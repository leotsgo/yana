// Package git keeps a history of the notes tree in a git repository inside
// the notes root. It owns the whole life of that repository: initialising
// it on first run, committing changes after the tree has been quiet (never
// per write-back), attributing each commit to the author of the edits in
// its window, pushing to an optional remote nightly, and answering the
// history questions the UI asks (log with renames followed, diff between
// revisions, content of a note at a revision).
//
// It drives the git binary rather than linking a library: rename-following
// logs and diffs are the feature, and no pure-Go implementation has them.
// When the binary is missing the layer reports unavailable and every
// caller degrades (the endpoints answer 501, the loop does not run).
//
// Commit cadence: a change resets a quiet timer; when the tree has been
// quiet for Options.Quiet the window commits. An hourly bound (Options.
// Interval) commits a continuously edited tree so an hour of typing
// produces a bounded number of commits, not one. Snapshot commits on
// demand. Attribution: the window's edits are grouped by the author the
// reconciliation loop recorded for them — agent edits commit under the
// agent's label, everything else (humans, editors, scripts) under the
// configured human identity — and a window with more than one author is
// split into one commit per author wherever the changed files allow it.
package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
)

// Options tune one layer. Zero values take the defaults noted.
type Options struct {
	// Quiet is how long the tree must go without a change before the
	// window commits (5m).
	Quiet time.Duration
	// Interval bounds how long a continuously edited tree can go
	// uncommitted (1h).
	Interval time.Duration
	// Remote is an optional git URL pushed nightly. Empty disables push.
	Remote string
	// PushHour is the local hour of the nightly push (2).
	PushHour int
	// HumanName and HumanEmail identify human edits in git. Until Phase 4
	// there is one account-shaped hole where a user goes, so every
	// non-agent edit commits under this identity ("yana user").
	HumanName  string
	HumanEmail string
	// DB supplies the durable author attribution for a window (ops
	// recorded in note_updates). Nil restricts attribution to the live
	// event feed.
	DB *index.DB
	// Now is the clock.
	Now func() time.Time
}

func (o *Options) defaults() {
	if o.Quiet <= 0 {
		o.Quiet = 5 * time.Minute
	}
	if o.Interval <= 0 {
		o.Interval = time.Hour
	}
	if o.PushHour < 0 || o.PushHour > 23 {
		o.PushHour = 2
	}
	if o.HumanName == "" {
		o.HumanName = "yana user"
	}
	if o.HumanEmail == "" {
		o.HumanEmail = "user@yana.local"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Stats is a snapshot of the layer's counters, reported by /api/status.
type Stats struct {
	Available  bool      `json:"available"`
	Commits    int64     `json:"commits"`
	LastCommit time.Time `json:"last_commit"`
	Pushes     int64     `json:"pushes"`
	LastPush   time.Time `json:"last_push"`
	Errors     int64     `json:"errors"`
}

// LogEntry is one revision of a note's history.
type LogEntry struct {
	Hash    string `json:"hash"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
	// Path is the note's path as of this revision; it differs from the
	// current path when the note has moved since.
	Path string `json:"path"`
}

// Layer is the git history loop.
type Layer struct {
	root string
	opts Options
	log  *slog.Logger

	available bool

	mu           sync.Mutex
	lastCommit   time.Time
	lastActivity time.Time
	lastPush     time.Time
	// windowAuthors maps a relative path to the raw authors (user:*,
	// agent:*, filesystem) whose edits are pending in the open window.
	windowAuthors map[string]map[string]struct{}
	dirty         bool
	commits       int64
	pushes        int64
	errors        int64
	// committing is held while a commit cycle runs so Snapshot and the
	// loop serialise.
	committing sync.Mutex

	flush func(context.Context)
	unsub func()

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// New builds a layer over the notes root. The root is not touched until
// Ensure runs.
func New(root string, opts Options, log *slog.Logger) *Layer {
	opts.defaults()
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Layer{
		root:          root,
		opts:          opts,
		log:           log.With("component", "git"),
		windowAuthors: map[string]map[string]struct{}{},
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Attach wires the reconciliation loop: pending edits are flushed before
// every commit, tree activity resets the quiet timer, and update events
// record the author behind each path. Call before Start.
func (l *Layer) Attach(rec *reconcile.Reconciler) {
	l.flush = rec.FlushAll
	l.unsub = rec.Subscribe(func(ev reconcile.Event) {
		switch ev.Kind {
		case reconcile.EventUpdate:
			l.Author(ev.Path, ev.Author)
		case reconcile.EventMoved, reconcile.EventDeleted:
			l.Notify(ev.Path)
		}
	})
}

// Ensure initialises the repository: git init when the notes root has no
// .git, and a .gitignore covering .sync/ and the loop's transient temp
// files. It reports whether the git binary is usable; a layer over a
// missing binary is unavailable and every operation degrades.
func (l *Layer) Ensure(ctx context.Context) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git binary not found on PATH")
	}
	l.available = true
	if _, err := os.Stat(filepath.Join(l.root, ".git")); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := l.git(ctx, "-c", "init.defaultBranch=main", "init"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
		l.log.Info("initialised repository", "root", l.root)
	}
	ignore := filepath.Join(l.root, ".gitignore")
	want := []string{".sync/", "*.yana-tmp-*"}
	cur, err := os.ReadFile(ignore)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(string(cur), "\n")
	have := map[string]bool{}
	for _, ln := range lines {
		have[strings.TrimSpace(ln)] = true
	}
	var add []string
	for _, w := range want {
		if !have[w] {
			add = append(add, w)
		}
	}
	if len(add) > 0 {
		out := append(bytes.Clone(cur), []byte(strings.Join(add, "\n")+"\n")...)
		if err := fsutil.WriteFileAtomic(ignore, out, 0o644); err != nil {
			return err
		}
	}
	// The window opens at the last commit so attribution survives a
	// restart mid-window: the exact time lives in .sync/git-state.json,
	// falling back to the newest commit's timestamp rounded up a second
	// when that file is gone (.sync is rebuilt from the tree).
	if err := l.loadWindowState(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		l.log.Warn("git state unreadable; the window opens at the last commit", "err", err)
	}
	l.mu.Lock()
	if l.lastCommit.IsZero() {
		l.lastCommit = time.Unix(l.lastCommitUnix(ctx)+1, 0)
	}
	l.mu.Unlock()
	return nil
}

// statePath is the window bookkeeping under .sync, which is derived state
// by contract; losing it only blurs attribution for one window.
func (l *Layer) statePath() string {
	return filepath.Join(l.root, ".sync", "git-state.json")
}

func (l *Layer) saveWindowState(ts time.Time) {
	body := []byte(fmt.Sprintf(`{"window_start":%d}`+"\n", ts.UnixNano()))
	if err := fsutil.MkdirInherit(filepath.Dir(l.statePath())); err != nil {
		return
	}
	if err := fsutil.WriteFileAtomic(l.statePath(), body, 0o644); err != nil {
		l.log.Warn("could not write git state", "err", err)
	}
}

func (l *Layer) loadWindowState() error {
	raw, err := os.ReadFile(l.statePath())
	if err != nil {
		return err
	}
	var st struct {
		WindowStart int64 `json:"window_start"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	ts := time.Unix(0, st.WindowStart)
	if ts.After(l.opts.Now()) {
		return errors.New("window start is in the future")
	}
	l.mu.Lock()
	l.lastCommit = ts
	l.mu.Unlock()
	return nil
}

// Start runs the commit loop. It returns immediately; failures are logged
// and counted, never fatal — the tree is the source of truth, the
// repository is derived.
func (l *Layer) Start() {
	l.wg.Add(1)
	go l.loop()
}

// Close stops the loop and commits what is pending, so a clean shutdown
// leaves the repository holding the tree.
func (l *Layer) Close() {
	l.cancel()
	l.wg.Wait()
	if l.unsub != nil {
		l.unsub()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := l.commitPending(ctx, "shutdown"); err != nil {
		l.log.Error("final commit failed", "err", err)
	}
}

// Notify reports tree activity: every change resets the quiet timer.
func (l *Layer) Notify(rel string) {
	l.mu.Lock()
	l.lastActivity = l.opts.Now()
	l.dirty = true
	l.mu.Unlock()
}

// Author records that author edited path in the open window.
func (l *Layer) Author(path, author string) {
	if path == "" || author == "" {
		return
	}
	l.mu.Lock()
	if l.windowAuthors[path] == nil {
		l.windowAuthors[path] = map[string]struct{}{}
	}
	l.windowAuthors[path][author] = struct{}{}
	l.mu.Unlock()
}

// Snapshot flushes pending edits and commits now. It is the explicit
// "snapshot now" behind POST /api/git/snapshot and the shutdown path.
func (l *Layer) Snapshot(ctx context.Context) (int, error) {
	if !l.available {
		return 0, errors.New("git history is unavailable")
	}
	l.Notify("")
	return l.commitPending(ctx, "snapshot")
}

// Root returns the notes root the layer histories.
func (l *Layer) Root() string { return l.root }

// Stats returns the layer's counters.
func (l *Layer) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Stats{
		Available: l.available, Commits: l.commits, LastCommit: l.lastCommit,
		Pushes: l.pushes, LastPush: l.lastPush, Errors: l.errors,
	}
}

// --- the loop --------------------------------------------------------------

func (l *Layer) loop() {
	defer l.wg.Done()
	check := l.opts.Quiet
	if l.opts.Interval < check {
		check = l.opts.Interval
	}
	check /= 4
	if check < 5*time.Millisecond {
		check = 5 * time.Millisecond
	}
	if check > 30*time.Second {
		check = 30 * time.Second
	}
	tick := time.NewTicker(check)
	defer tick.Stop()
	nextPush := l.nextPushAfter(l.opts.Now())
	for {
		select {
		case <-l.ctx.Done():
			return
		case now := <-tick.C:
			l.mu.Lock()
			quiet := l.dirty && now.Sub(l.lastActivity) >= l.opts.Quiet
			// The interval bound commits even a tree that never goes
			// quiet, and re-checks one that went quiet without the loop
			// noticing.
			interval := l.lastCommit.IsZero() || now.Sub(l.lastCommit) >= l.opts.Interval
			l.mu.Unlock()
			if quiet || interval {
				ctx, cancel := context.WithTimeout(l.ctx, time.Minute)
				if _, err := l.commitPending(ctx, "window"); err != nil {
					l.log.Error("commit failed; will retry", "err", err)
				}
				cancel()
			}
			if !nextPush.After(now) {
				nextPush = l.nextPushAfter(now)
				ctx, cancel := context.WithTimeout(l.ctx, 10*time.Minute)
				if err := l.PushNow(ctx); err != nil {
					l.log.Error("nightly push failed", "remote", l.opts.Remote, "err", err)
				}
				cancel()
			}
		}
	}
}

// nextPushAfter returns the next nightly push time on or after t.
func (l *Layer) nextPushAfter(t time.Time) time.Time {
	if l.opts.Remote == "" {
		return t.AddDate(100, 0, 0) // never
	}
	next := time.Date(t.Year(), t.Month(), t.Day(), l.opts.PushHour, 0, 0, 0, t.Location())
	if !next.After(t) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// PushNow pushes HEAD to the configured remote. It reports nil when no
// remote is configured. The nightly tick and tests call it directly.
func (l *Layer) PushNow(ctx context.Context) error {
	if l.opts.Remote == "" {
		return nil
	}
	l.committing.Lock()
	defer l.committing.Unlock()
	if _, err := l.git(ctx, "push", l.opts.Remote, "HEAD"); err != nil {
		l.countError()
		return err
	}
	l.mu.Lock()
	l.pushes++
	l.lastPush = l.opts.Now()
	l.mu.Unlock()
	l.log.Info("pushed", "remote", l.opts.Remote)
	return nil
}

// --- committing --------------------------------------------------------------

// change is one entry of git status --porcelain.
type change struct {
	path string
	orig string // previous path for renames and copies
}

// commitPending flushes, commits, and reports how many commits it made.
func (l *Layer) commitPending(ctx context.Context, reason string) (int, error) {
	if !l.available {
		return 0, nil
	}
	l.committing.Lock()
	defer l.committing.Unlock()
	if l.flush != nil {
		l.flush(ctx)
	}
	changes, err := l.status(ctx)
	if err != nil {
		l.countError()
		return 0, err
	}
	if len(changes) == 0 {
		now := l.opts.Now()
		l.mu.Lock()
		l.lastCommit = now
		l.dirty = false
		l.mu.Unlock()
		l.saveWindowState(now)
		return 0, nil
	}
	l.mu.Lock()
	windowStart := l.lastCommit
	authors := l.windowAuthors
	l.windowAuthors = map[string]map[string]struct{}{}
	l.mu.Unlock()
	if l.opts.DB != nil {
		if dba, err := l.opts.DB.AuthorPathsSince(ctx, windowStart); err == nil {
			for p, as := range dba {
				if authors[p] == nil {
					authors[p] = map[string]struct{}{}
				}
				for a := range as {
					authors[p][a] = struct{}{}
				}
			}
		} else {
			l.log.Warn("author attribution from the log failed", "err", err)
		}
	}

	// Group changed paths by the author behind them, as far as one
	// author per path is recoverable: agent edits commit under the
	// agent's label. Everything else — humans, editors, scripts, or a
	// path several authors touched — shares the human identity, because
	// the editor at the keyboard is the honest default and a file two
	// authors edited cannot be split by halves.
	groups := map[string][]string{}
	var keys []string
	for _, c := range changes {
		key := ""
		if as := authors[c.path]; len(as) == 1 {
			for a := range as {
				if label, ok := agentLabel(a); ok {
					key = "agent:" + label
				}
			}
		}
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], c.path)
		if c.orig != "" && c.orig != c.path {
			groups[key] = append(groups[key], c.orig)
		}
	}
	// Agents first (alphabetical), the human last, so HEAD after a mixed
	// window is a human commit.
	sort.Slice(keys, func(i, j int) bool {
		ai, aj := isAgent(keys[i]), isAgent(keys[j])
		if ai != aj {
			return ai
		}
		return keys[i] < keys[j]
	})

	made := 0
	for _, key := range keys {
		paths := groups[key]
		args := append([]string{"add", "-A", "--"}, paths...)
		if _, err := l.git(ctx, args...); err != nil {
			l.countError()
			l.log.Error("staging failed", "err", err, "paths", paths)
			continue
		}
		staged, err := l.stagedCount(ctx)
		if err != nil {
			l.countError()
			continue
		}
		if staged == 0 {
			continue
		}
		name, email := l.identity(key)
		subject := fmt.Sprintf("notes: %d file%s changed", len(paths), plural(len(paths)))
		if reason == "snapshot" || reason == "shutdown" {
			subject = fmt.Sprintf("notes: %s (%d file%s)", reason, len(paths), plural(len(paths)))
		}
		if label, ok := agentLabel(key); ok {
			subject += " by " + label
		}
		if _, err := l.git(ctx,
			"-c", "user.name=YANA", "-c", "user.email=yana@local",
			"commit", "--author="+name+" <"+email+">",
			"-m", subject,
		); err != nil {
			l.countError()
			l.log.Error("commit failed", "err", err, "author", name)
			continue
		}
		made++
		l.log.Info("commit", "reason", reason, "author", name, "files", len(paths), "subject", subject)
	}
	now := l.opts.Now()
	l.mu.Lock()
	if made > 0 {
		l.commits += int64(made)
	}
	l.lastCommit = now
	l.dirty = false
	l.mu.Unlock()
	l.saveWindowState(now)
	return made, nil
}

// identity maps a raw author to a git identity. Agent edits commit under
// the agent's label; humans and filesystem edits under the configured
// human identity.
func (l *Layer) identity(author string) (name, email string) {
	if label, ok := agentLabel(author); ok {
		return label, "agent@local"
	}
	return l.opts.HumanName, l.opts.HumanEmail
}

func agentLabel(author string) (string, bool) {
	label, ok := strings.CutPrefix(author, "agent:")
	if !ok || label == "" {
		return "", false
	}
	// A name that would confuse git's author parsing is not welcome.
	if strings.ContainsAny(label, "<>\n\r") {
		return "", false
	}
	return label, true
}

func isAgent(author string) bool {
	_, ok := agentLabel(author)
	return ok
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// status lists the repository's changes, deleted and renamed included.
func (l *Layer) status(ctx context.Context) ([]change, error) {
	// -untracked-files=all: a new directory of notes must arrive as one
	// entry per file, or the whole directory would stage under whichever
	// author happened to own its first file.
	out, err := l.git(ctx, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var changes []change
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		c := change{path: f[3:]}
		// In -z output a rename or copy carries the original path in the
		// field after the entry.
		if strings.ContainsRune(f[0:2], 'R') || strings.ContainsRune(f[0:2], 'C') {
			if i+1 < len(fields) {
				c.orig = fields[i+1]
				i++
			}
		}
		changes = append(changes, c)
	}
	return changes, nil
}

// stagedCount reports how many changes are staged.
func (l *Layer) stagedCount(ctx context.Context) (int, error) {
	out, err := l.git(ctx, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			n++
		}
	}
	return n, nil
}

func (l *Layer) countError() {
	l.mu.Lock()
	l.errors++
	l.mu.Unlock()
}

// lastCommitUnix reads the newest commit's timestamp; 0 with no history.
func (l *Layer) lastCommitUnix(ctx context.Context) int64 {
	out, err := l.git(ctx, "log", "-1", "--format=%ct")
	if err != nil || strings.TrimSpace(out) == "" {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// --- history reads -----------------------------------------------------------

var revPattern = regexp.MustCompile(`^[0-9a-f]{6,64}$`)

// ValidRevision reports whether s is safe to pass to git as a revision.
func ValidRevision(s string) bool { return revPattern.MatchString(s) }

// Log follows a note's path through the repository's history.
func (l *Layer) Log(ctx context.Context, rel string, limit int) ([]LogEntry, error) {
	if !l.available {
		return nil, errors.New("git history is unavailable")
	}
	if _, err := l.git(ctx, "rev-parse", "--verify", "HEAD"); err != nil {
		return []LogEntry{}, nil // no history yet
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	out, err := l.git(ctx, "log", "--follow", "-n", strconv.Itoa(limit),
		"--format=%x00%H%x1f%an%x1f%ae%x1f%aI%x1f%s", "--name-only", "--", rel)
	if err != nil {
		return nil, err
	}
	var entries []LogEntry
	for _, chunk := range strings.Split(out, "\x00") {
		lines := strings.Split(strings.TrimPrefix(chunk, "\n"), "\n")
		if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
			continue
		}
		parts := strings.SplitN(lines[0], "\x1f", 5)
		if len(parts) != 5 {
			continue
		}
		e := LogEntry{Hash: parts[0], Name: parts[1], Email: parts[2], Date: parts[3], Subject: parts[4], Path: rel}
		for _, ln := range lines[1:] {
			if strings.TrimSpace(ln) != "" {
				// --name-only lists the path as of this commit, which is
				// how a restore finds a note that has since moved.
				e.Path = ln
				break
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// Diff returns the change to rel between two revisions.
func (l *Layer) Diff(ctx context.Context, rel, from, to string) (string, error) {
	if !l.available {
		return "", errors.New("git history is unavailable")
	}
	if !ValidRevision(from) || !ValidRevision(to) {
		return "", errors.New("revisions must be commit hashes")
	}
	out, err := l.git(ctx, "diff", "--no-color", from, to, "--", rel)
	if err != nil {
		return "", err
	}
	return out, nil
}

// Show returns a file's content at a revision. The path is the one the
// file held at that revision, as reported by Log.
func (l *Layer) Show(ctx context.Context, rev, rel string) ([]byte, error) {
	if !l.available {
		return nil, errors.New("git history is unavailable")
	}
	if !ValidRevision(rev) {
		return nil, errors.New("revision must be a commit hash")
	}
	out, err := l.git(ctx, "show", rev+":"+filepath.ToSlash(rel))
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// --- plumbing ------------------------------------------------------------------

// git runs one git command in the notes root and returns its stdout.
func (l *Layer) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = l.root
	// Never stop for a credential prompt; a push that needs one fails.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(msg)
	}
	return out.String(), nil
}
