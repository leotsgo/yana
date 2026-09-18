package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/guide"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// The starter note. The owner's first sign-in seeds an empty tree with
// it; Help re-creates it in a space on request. Either way the files
// are written once with O_EXCL and left alone afterwards: they are
// ordinary notes, and deleting them is deleting them.

// seedGuide writes the guide into space (the tree root when empty),
// skipping any file already there, and returns the starter note's path
// and whether anything was written.
func (s *Server) seedGuide(ctx context.Context, space string) (rel string, created bool, err error) {
	for i, f := range guide.Files() {
		rel := f.Rel
		if space != "" {
			rel = space + "/" + rel
		}
		clean, err := s.Root.Clean(rel)
		if err != nil {
			return "", false, err
		}
		abs, _, err := s.Root.Resolve(clean)
		if err != nil {
			return "", false, err
		}
		if i == 0 {
			if err := s.Root.CheckCollision(clean); err != nil {
				return "", false, err
			}
		}
		if err := fsutil.MkdirInherit(filepath.Dir(abs)); err != nil {
			return "", false, err
		}
		fh, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		data := f.Data
		if scanner.KindOf(clean) != "" && !scanner.IsAsset(clean) {
			// An id up front, as the daily note does, so the scan below
			// indexes the note at once instead of waiting for it to settle.
			data, _, _ = frontmatter.EnsureID(data, scanner.NewID(time.Now()), time.Now())
		}
		_, werr := fh.Write(data)
		if cerr := fh.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return "", false, werr
		}
		created = true
		if s.Scanner != nil && scanner.KindOf(clean) != "" {
			if err := s.Scanner.ScanOne(ctx, clean); err != nil {
				s.Log.Warn("guide note is not indexed yet", "path", clean, "err", err)
			}
		}
	}
	rel = guide.NoteName
	if space != "" {
		rel = space + "/" + rel
	}
	return rel, created, nil
}

// treeIsEmpty reports whether the notes root holds no note at all, on
// disk and in the index: the only state the guide is seeded into
// unasked. Dot-prefixed entries (.sync, .git) do not count.
func (s *Server) treeIsEmpty(ctx context.Context) bool {
	if n, _, err := s.DB.Counts(ctx); err != nil || n > 0 {
		return false
	}
	empty := true
	_ = filepath.WalkDir(s.Root.Dir(), func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == s.Root.Dir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && scanner.KindOf(d.Name()) != "" {
			empty = false
			return filepath.SkipAll
		}
		return nil
	})
	return empty
}

// handleGuide (POST /api/guide, {space}) makes sure the guide exists in
// a space the caller can write to and answers with the starter note.
func (s *Server) handleGuide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Space string `json:"space"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	space := ""
	if strings.TrimSpace(body.Space) != "" {
		clean, err := s.Root.Clean(body.Space)
		if err != nil || strings.Contains(clean, "/") || clean == "" {
			writeError(w, http.StatusBadRequest, "space must be a single directory name")
			return
		}
		space = clean
	}
	if !s.mayWrite(w, r, space) {
		return
	}
	rel, created, err := s.seedGuide(r.Context(), space)
	if err != nil {
		if pathsafe.IsRejection(err) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.fail(w, r, err)
		return
	}
	n, err := s.DB.GetNoteByPath(r.Context(), rel)
	if err != nil {
		// Written but not indexed yet (a scanner-less server): the tree
		// picks it up on the next scan.
		writeJSON(w, http.StatusAccepted, map[string]any{"path": rel, "created": created})
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, map[string]any{"id": n.ID, "path": n.RelPath, "note": n, "created": created})
}
