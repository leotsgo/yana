// Files the owner keeps with the notes under .yana/ (themes, defaults).
// The dotted folder keeps them out of the tree and the index; the git
// history still records them.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/leotsgo/yana/internal/fsutil"
)

// readYana decodes the file at rel into v; found is false when there is
// no file. A file that does not parse (a hand edit gone wrong) is logged
// and reads as found with v left as it was, so settings keep working.
func (s *Server) readYana(r *http.Request, rel string, v any) (found bool, err error) {
	raw, err := os.ReadFile(filepath.Join(s.Root.Dir(), rel))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		loggerFrom(r.Context()).Warn(".yana file unreadable", "path", rel, "err", err)
	}
	return true, nil
}

// writeYana writes v as indented JSON at rel, atomically, and tells git.
func (s *Server) writeYana(rel string, v any) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	abs := filepath.Join(s.Root.Dir(), rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(abs, append(out, '\n'), 0o644); err != nil {
		return err
	}
	if s.Git != nil {
		s.Git.Notify(rel)
	}
	return nil
}
