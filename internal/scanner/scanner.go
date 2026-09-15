// Package scanner walks the notes tree and rebuilds the index from it. It
// is the concrete form of invariant #1: everything the index knows, it
// learned here, and it can learn it again from scratch.
package scanner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/render"
	"github.com/madeofpendletonwool/yana/internal/spaces"
)

// Options tune one scanner.
type Options struct {
	// SettleTime is how old a file's mtime must be before an id is written
	// into it. Younger files are still being written by someone else.
	SettleTime time.Duration
	// MaxNoteSize and MaxAssetSize skip files above these byte counts.
	MaxNoteSize  int64
	MaxAssetSize int64
	// MaxNotesPerSpace stops indexing a space past this many notes.
	MaxNotesPerSpace int
	// Now is the clock (overridable for tests).
	Now func() time.Time
}

// Scanner indexes a tree.
type Scanner struct {
	root *pathsafe.Root
	db   *index.DB
	opts Options
	log  *slog.Logger
	// reassigned collects files rewritten with a fresh id mid-walk so the
	// same scan can index them under it.
	reassigned []string
}

// Result summarises one scan.
type Result struct {
	Notes    int
	Assets   int
	Assigned int // ids written into files
	Retired  int64
	Skipped  int      // over limits, unreadable, unknown types
	Deferred []string // files too young to receive an id
	Duration time.Duration
}

// New builds a scanner over root writing to db.
func New(root *pathsafe.Root, db *index.DB, opts Options, log *slog.Logger) *Scanner {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.SettleTime == 0 {
		opts.SettleTime = 2 * time.Second
	}
	if opts.MaxNoteSize == 0 {
		opts.MaxNoteSize = root.Limits().MaxNoteSize
	}
	if opts.MaxAssetSize == 0 {
		opts.MaxAssetSize = root.Limits().MaxAssetSize
	}
	if opts.MaxNotesPerSpace == 0 {
		opts.MaxNotesPerSpace = root.Limits().MaxNotesPerSpace
	}
	if log == nil {
		log = slog.Default()
	}
	return &Scanner{root: root, db: db, opts: opts, log: log.With("component", "scanner")}
}

type indexed struct {
	note index.Note
	body string // what search reads: markdown text or tag-stripped HTML
	raw  string // untransformed body: what links are extracted from
	tags []string
}

// Scan walks the whole tree, upserts every note and asset, and retires rows
// whose files are gone. It is safe to run repeatedly; a second run over an
// unchanged tree changes nothing.
func (s *Scanner) Scan(ctx context.Context) (Result, error) {
	start := s.opts.Now()
	var res Result
	keepNotes := map[string]struct{}{}
	keepAssets := map[string]struct{}{}
	seenIDs := map[string]string{} // id -> rel path
	perSpace := map[string]int{}
	spaceDirs := map[string]struct{}{}
	var batch []indexed
	var assets []index.Asset

	flush := func() error {
		if len(batch) == 0 && len(assets) == 0 {
			return nil
		}
		b, a := batch, assets
		batch, assets = nil, nil
		return s.db.Write(ctx, func(tx *sql.Tx) error {
			for _, it := range b {
				if err := index.UpsertNote(tx, it.note, it.body, it.raw, it.tags); err != nil {
					return err
				}
			}
			for _, as := range a {
				if err := index.UpsertAsset(tx, as); err != nil {
					return err
				}
			}
			return nil
		})
	}

	rootDir := s.root.Dir()
	err := filepath.WalkDir(rootDir, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			if abs == rootDir {
				return err
			}
			s.log.Warn("skipping unreadable entry", "path", abs, "err", err)
			res.Skipped++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := d.Name()
		if abs != rootDir && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			// Top-level directories are the spaces; remember them so the
			// scan can reload every .space.yml it finds.
			if parent := filepath.Dir(abs); parent == rootDir && name != ".sync" {
				spaceDirs[name] = struct{}{}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			// Symlinks are not followed: a link out of the tree is exactly
			// what the path module exists to refuse.
			return nil
		}
		rel, err := filepath.Rel(rootDir, abs)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		cleanRel, err := s.root.Clean(rel)
		if err != nil {
			s.log.Warn("skipping file with unsafe name", "path", rel, "err", err)
			res.Skipped++
			return nil
		}
		info, err := d.Info()
		if err != nil {
			res.Skipped++
			return nil
		}
		space := spaceOf(cleanRel)

		if IsAsset(cleanRel) {
			if info.Size() > s.opts.MaxAssetSize {
				s.log.Warn("asset over size limit", "path", rel, "size", info.Size(), "limit", s.opts.MaxAssetSize)
				res.Skipped++
				return nil
			}
			assets = append(assets, index.Asset{Space: space, RelPath: cleanRel, Size: info.Size()})
			keepAssets[cleanRel] = struct{}{}
			res.Assets++
			return nil
		}
		kind := KindOf(cleanRel)
		if kind == "" {
			return nil
		}
		if info.Size() > s.opts.MaxNoteSize {
			s.log.Warn("note over size limit", "path", rel, "size", info.Size(), "limit", s.opts.MaxNoteSize)
			res.Skipped++
			return nil
		}
		if perSpace[space] >= s.opts.MaxNotesPerSpace {
			s.log.Warn("space over note limit; file not indexed", "space", space, "path", rel, "limit", s.opts.MaxNotesPerSpace)
			res.Skipped++
			return nil
		}

		it, assigned, deferred, err := s.indexFile(ctx, abs, cleanRel, space, kind, info, seenIDs)
		if err != nil {
			s.log.Warn("skipping note", "path", rel, "err", err)
			res.Skipped++
			return nil
		}
		if deferred {
			res.Deferred = append(res.Deferred, cleanRel)
			return nil
		}
		if assigned {
			res.Assigned++
		}
		perSpace[space]++
		keepNotes[cleanRel] = struct{}{}
		batch = append(batch, it)
		res.Notes++
		if len(batch) >= 1000 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	for _, rel := range s.reassigned {
		abs, cleanRel, err := s.root.Resolve(rel)
		if err != nil {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		it, _, _, err := s.indexFile(ctx, abs, cleanRel, spaceOf(cleanRel), KindOf(cleanRel), info, seenIDs)
		if err != nil {
			s.log.Warn("skipping reassigned note", "path", rel, "err", err)
			continue
		}
		batch = append(batch, it)
		keepNotes[cleanRel] = struct{}{}
	}
	s.reassigned = nil
	if err := flush(); err != nil {
		return res, err
	}
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		n, err := index.DeleteNotesExcept(tx, keepNotes)
		if err != nil {
			return err
		}
		res.Retired = n
		if err := index.DeleteAssetsExcept(tx, keepAssets); err != nil {
			return err
		}
		if err := index.RecomputeAllLinks(tx); err != nil {
			return err
		}
		return index.SetScanState(tx, "last_scan", s.opts.Now().UTC().Format(time.RFC3339Nano))
	})
	if err != nil {
		return res, err
	}
	if err := s.syncSpaces(ctx, spaceDirs); err != nil {
		return res, err
	}
	res.Duration = s.opts.Now().Sub(start)
	s.log.Info("scan complete", "notes", res.Notes, "assets", res.Assets, "assigned_ids", res.Assigned,
		"retired", res.Retired, "skipped", res.Skipped, "deferred", len(res.Deferred), "duration", res.Duration)
	return res, nil
}

// ScanOne re-indexes a single file (used for deferred files and, later, by
// the watcher). A missing file retires its row. Link rows follow: a plain
// content change recomputes the note's own outbound links, a new note or a
// path change recomputes the whole space's, because every raw target in
// the space may resolve differently now.
func (s *Scanner) ScanOne(ctx context.Context, rel string) error {
	abs, cleanRel, err := s.root.Resolve(rel)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return s.db.Write(ctx, func(tx *sql.Tx) error {
			if err := index.DeleteNoteByPath(tx, cleanRel); err != nil {
				return err
			}
			// A removed note can free a basename (uniqueness flips) and
			// breaks inbound links; recompute the space it lived in.
			return index.RecomputeSpaceLinks(tx, spaceOf(cleanRel))
		})
	}
	if err != nil {
		return err
	}
	kind := KindOf(cleanRel)
	if kind == "" || IsAsset(cleanRel) || !info.Mode().IsRegular() {
		return nil
	}
	if info.Size() > s.opts.MaxNoteSize {
		return fmt.Errorf("note over size limit (%d bytes)", info.Size())
	}
	it, _, deferred, err := s.indexFile(ctx, abs, cleanRel, spaceOf(cleanRel), kind, info, map[string]string{})
	if err != nil {
		return err
	}
	if deferred {
		return errDeferred
	}
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		old, err := index.GetNoteTx(tx, it.note.ID)
		if err != nil && !errors.Is(err, index.ErrNotFound) {
			return err
		}
		moved := err == nil && old.RelPath != it.note.RelPath
		fresh := errors.Is(err, index.ErrNotFound)
		if err := index.UpsertNote(tx, it.note, it.body, it.raw, it.tags); err != nil {
			return err
		}
		// A new note can resolve targets that were unresolved; a moved one
		// changes how the whole space resolves. Both redo the space.
		if moved {
			if old.Space != it.note.Space {
				if err := index.RecomputeSpaceLinks(tx, old.Space); err != nil {
					return err
				}
			}
			return index.RecomputeSpaceLinks(tx, it.note.Space)
		}
		if fresh {
			return index.RecomputeSpaceLinks(tx, it.note.Space)
		}
		return index.ReplaceLinksForNote(tx, it.note.ID)
	})
}

// syncSpaces reloads every space's .space.yml into the membership
// cache and retires rows for directories that are gone.
func (s *Scanner) syncSpaces(ctx context.Context, dirs map[string]struct{}) error {
	for space := range dirs {
		abs := filepath.Join(s.root.Dir(), space, spaces.FileName)
		data, err := os.ReadFile(abs)
		var spec spaces.Spec
		if err == nil {
			if spec, err = spaces.Parse(data); err != nil {
				s.log.Warn("space membership file is invalid; keeping the previous members", "space", space, "err", err)
				continue
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("cannot read space membership", "space", space, "err", err)
			continue
		}
		if _, err := s.db.SyncSpaceSpec(ctx, space, spec, s.log); err != nil {
			s.log.Error("cannot cache space membership", "space", space, "err", err)
		}
	}
	return s.db.RetireSpacesExcept(ctx, dirs, s.log)
}

var errDeferred = errors.New("file is still being written; try again later")

// IsDeferred reports whether err means the file was too young to index.
func IsDeferred(err error) bool { return errors.Is(err, errDeferred) }

func (s *Scanner) indexFile(ctx context.Context, abs, rel, space, kind string, info fs.FileInfo, seenIDs map[string]string) (indexed, bool, bool, error) {
	content, err := os.ReadFile(abs)
	if err != nil {
		return indexed{}, false, false, err
	}
	fm := frontmatter.Parse(content)
	assigned := false
	now := s.opts.Now()

	needsID := fm.Meta.ID == ""
	replaceExisting := false
	if !needsID {
		if other, dup := seenIDs[fm.Meta.ID]; dup && other != rel {
			// Two files carry the same id (a `cp`). The one the index already
			// knows at its path keeps it; otherwise the first one walked does.
			known, err := s.db.GetNote(ctx, fm.Meta.ID)
			if err == nil && known.RelPath == rel {
				s.log.Warn("duplicate note id; reassigning the copy", "id", fm.Meta.ID, "kept", rel, "reassigned", other)
				if err := s.reassign(other); err != nil {
					s.log.Warn("could not reassign duplicate id", "path", other, "err", err)
				} else {
					s.reassigned = append(s.reassigned, other)
				}
			} else {
				s.log.Warn("duplicate note id; reassigning", "id", fm.Meta.ID, "kept", other, "reassigned", rel)
				needsID = true
				replaceExisting = true
			}
		}
	}
	if needsID {
		if now.Sub(info.ModTime()) < s.opts.SettleTime {
			s.log.Debug("file too young to assign an id; deferring", "path", rel, "age", now.Sub(info.ModTime()))
			return indexed{}, false, true, nil
		}
		id := NewID(now)
		var meta frontmatter.Meta
		var out []byte
		if replaceExisting {
			out, _ = frontmatter.ReplaceID(content, id)
			meta = fm.Meta
			meta.ID = id
		} else {
			out, meta, _ = frontmatter.EnsureID(content, id, now)
		}
		if err := fsutil.WriteFileAtomic(abs, out, info.Mode().Perm()); err != nil {
			return indexed{}, false, false, fmt.Errorf("write id into %s: %w", rel, err)
		}
		s.log.Info("assigned id", "path", rel, "id", id)
		content = out
		fm = frontmatter.Parse(content)
		fm.Meta = meta
		assigned = true
		if fi, err := os.Stat(abs); err == nil {
			info = fi
		}
	}
	seenIDs[fm.Meta.ID] = rel

	sum := sha256.Sum256(content)
	body := fm.Body
	var title, bodyText, raw string
	switch kind {
	case "md":
		title = render.Title(body)
		bodyText = string(body)
		raw = bodyText
	case "html":
		bodyText = render.StripHTML(body)
		title = htmlTitle(body)
		raw = string(body)
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	}
	created := fm.Meta.Created
	if created.IsZero() {
		created = info.ModTime().UTC()
	}
	note := index.Note{
		ID:          fm.Meta.ID,
		Space:       space,
		RelPath:     rel,
		Title:       title,
		Preview:     render.Preview([]byte(bodyText), 200),
		Kind:        kind,
		ContentHash: hex.EncodeToString(sum[:]),
		Size:        info.Size(),
		MTime:       info.ModTime().UTC(),
		Created:     created.UTC(),
		UpdatedAt:   now.UTC(),
		Order:       fm.Meta.Order,
		Trusted:     fm.Meta.Trusted,
	}
	var tags []string
	if kind == "md" {
		tags = render.Tags(body)
	}
	return indexed{note: note, body: bodyText, raw: raw, tags: tags}, assigned, false, nil
}

// ReassignID gives the file at rel a fresh id on disk. The watcher uses it
// when a copy of a known note appears at a second path.
func (s *Scanner) ReassignID(rel string) error { return s.reassign(rel) }

// reassign gives the file at rel a fresh id on disk.
func (s *Scanner) reassign(rel string) error {
	abs, _, err := s.root.Resolve(rel)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	out, ok := frontmatter.ReplaceID(content, NewID(s.opts.Now()))
	if !ok {
		return errors.New("no id line to replace")
	}
	return fsutil.WriteFileAtomic(abs, out, info.Mode().Perm())
}

// NewID returns a fresh ULID for the given time.
func NewID(now time.Time) string {
	return ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
}

// spaceOf is the first path segment when the file sits inside a top-level
// directory, or "" for files loose in the root.
func spaceOf(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

// IsAsset reports whether rel sits under an _assets directory.
func IsAsset(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/") {
		if seg == "_assets" {
			return true
		}
	}
	return false
}

// KindOf returns "md" or "html" for note files and "" for anything else.
func KindOf(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".md", ".markdown":
		return "md"
	case ".html", ".htm":
		return "html"
	}
	return ""
}

func htmlTitle(src []byte) string {
	lower := strings.ToLower(string(src))
	i := strings.Index(lower, "<title>")
	if i < 0 {
		i = strings.Index(lower, "<h1")
		if i < 0 {
			return ""
		}
		j := strings.Index(lower[i:], ">")
		if j < 0 {
			return ""
		}
		i += j + 1
		end := strings.Index(lower[i:], "</h1>")
		if end < 0 {
			return ""
		}
		return strings.TrimSpace(render.StripHTML(src[i : i+end]))
	}
	i += len("<title>")
	end := strings.Index(lower[i:], "</title>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(string(src[i : i+end]))
}
