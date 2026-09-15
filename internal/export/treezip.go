// The tree zip: markdown and assets exactly as they sit on disk, no
// transformation. Unzipped at the notes root it recreates the space —
// ids, links, and structure intact — which is the round trip invariant
// #6 promises.
package export

import (
	"archive/zip"
	"context"
	"io"
	"os"
)

// TreeStats reports what a tree zip wrote.
type TreeStats struct {
	Files int
	Bytes int64
}

// TreeZip writes a byte-identical zip of one space, or a subtree of it.
// subtree is a path within the space ("" is the whole space). Every
// non-hidden file is included with its contents, mode, and mtime
// unchanged; a space's .space.yml travels with it.
func (d *Deps) TreeZip(ctx context.Context, space, subtree string, w io.Writer) (TreeStats, error) {
	var stats TreeStats
	baseRel, _, err := d.scopeFiles(space, subtree)
	if err != nil {
		return stats, err
	}
	zw := zip.NewWriter(w)
	var copyErr error
	walkErr := walkTree(d.Root, baseRel, func(rel string, info os.FileInfo, hidden bool) {
		if hidden && info.Name() != ".space.yml" {
			return
		}
		abs, _, rerr := d.Root.Resolve(rel)
		if rerr != nil {
			return
		}
		data, rerr := os.ReadFile(abs)
		if rerr != nil {
			return
		}
		stats.Files++
		stats.Bytes += int64(len(data))
		if copyErr == nil {
			copyErr = put(zw, rel, info.ModTime(), data)
		}
	})
	if walkErr != nil {
		_ = zw.Close()
		return stats, walkErr
	}
	if copyErr != nil {
		_ = zw.Close()
		return stats, copyErr
	}
	if err := zw.Close(); err != nil {
		return stats, err
	}
	return stats, nil
}

// scopeFiles validates the space and subtree and returns the base path
// to walk, requiring only that the directory exists.
func (d *Deps) scopeFiles(space, subtree string) (string, []siteNote, error) {
	sub, err := d.Root.Clean(subtree)
	if err != nil {
		return "", nil, err
	}
	baseRel := space
	if sub != "" {
		baseRel = space + "/" + sub
	}
	if abs, _, err := d.Root.Resolve(baseRel); err != nil {
		return "", nil, err
	} else if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", nil, ErrNoNotes
	}
	return baseRel, nil, nil
}
