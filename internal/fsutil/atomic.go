// Package fsutil holds the small filesystem helpers the rest of the server
// shares. Every write to the notes tree goes through WriteFileAtomic so a
// concurrent reader (an editor, a sync tool, our own watcher) never sees a
// half-written file.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to a dot-prefixed temporary file in the same
// directory, fsyncs it, then renames it over path. On success the directory
// is fsynced too so the rename is durable. mode applies to the new file.
//
// When path already exists and the process is privileged (root in a
// container writing into a bind mount), the new file keeps the old file's
// owner. Otherwise adding an id would hand the user's note to root.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	prev, _ := os.Lstat(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".yana-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if prev != nil {
		if err := keepOwner(tmp, prev); err != nil {
			tmp.Close()
			cleanup()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// MkdirInherit creates dir (and parents) if missing. A directory it creates
// takes the owner of the nearest existing ancestor when the process is
// privileged, so a root process laying down .sync/ inside a user's tree
// leaves something the user can still delete.
func MkdirInherit(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	parent := filepath.Dir(dir)
	if parent != dir {
		if err := MkdirInherit(parent); err != nil {
			return err
		}
	}
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	prev, err := os.Stat(parent)
	if err != nil {
		return nil
	}
	return keepOwnerPath(dir, prev)
}
