package drawing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"

	"github.com/leotsgo/yana/internal/fsutil"
)

// rewriteMu makes a drawing's read, edit and write one step, so two
// changes landing together (a browser save and an agent's edit) both keep
// their elements.
// ponytail: one lock for every drawing; per-file locks if writes queue up.
var rewriteMu sync.Mutex

// Rewrite reads the drawing file at abs, hands the whole file (frontmatter
// included) to edit, and writes what edit returns. It returns the file as
// read and the file as written. An edit that returns nil, or the bytes
// already on disk, writes nothing; an edit that fails writes nothing and
// its error comes back unchanged.
func Rewrite(abs string, edit func(file []byte) (next []byte, err error)) (prev, next []byte, err error) {
	rewriteMu.Lock()
	defer rewriteMu.Unlock()
	prev, err = os.ReadFile(abs)
	if err != nil {
		return nil, nil, err
	}
	next, err = edit(prev)
	if err != nil {
		return prev, nil, err
	}
	if next == nil || bytes.Equal(next, prev) {
		return prev, prev, nil
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(abs); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := fsutil.WriteFileAtomic(abs, next, mode); err != nil {
		return prev, nil, err
	}
	return prev, next, nil
}

// Hash is the hex sha256 of a drawing file, the same value the index keeps
// as content_hash.
func Hash(file []byte) string {
	sum := sha256.Sum256(file)
	return hex.EncodeToString(sum[:])
}
