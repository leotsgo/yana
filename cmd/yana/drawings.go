package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/leotsgo/yana/internal/config"
	"github.com/leotsgo/yana/internal/drawing"
	"github.com/leotsgo/yana/internal/frontmatter"
	"github.com/leotsgo/yana/internal/fsutil"
)

// drawingsMain runs `yana drawings convert [--apply]` and returns the exit
// status.
func drawingsMain(args []string) int {
	if len(args) < 1 || args[0] != "convert" || len(args) > 2 || (len(args) == 2 && args[1] != "--apply") {
		fmt.Fprintln(os.Stderr, "usage: yana drawings convert [--apply]")
		return 2
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 2
	}
	apply := len(args) == 2
	converted, failed, err := convertDrawings(cfg.NotesRoot, apply, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "drawings:", err)
		return 1
	}
	verb := "would convert"
	if apply {
		verb = "converted"
	}
	fmt.Printf("%s %d, failed %d\n", verb, converted, failed)
	if failed > 0 {
		return 1
	}
	return 0
}

// convertDrawings rewrites Obsidian Excalidraw files under root as yana
// drawings, in place; without apply it only reports. Files whose name lacks
// .excalidraw.md are not renamed here: a rename goes through the move API so
// links follow it.
func convertDrawings(root string, apply bool, out io.Writer) (converted, failed int, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && (d.Name() == "_assets" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(out, "FAIL %s: %v\n", rel, err)
			failed++
			return nil
		}
		if !drawing.IsObsidian(src) {
			return nil
		}
		dst, err := drawing.FromObsidian(src)
		var s drawing.Scene
		if err == nil {
			s, _, err = drawing.Parse(frontmatter.Parse(dst).Body)
		}
		if err == nil && apply {
			info, serr := d.Info()
			if serr != nil {
				err = serr
			} else {
				err = fsutil.WriteFileAtomic(p, dst, info.Mode().Perm())
			}
		}
		if err != nil {
			fmt.Fprintf(out, "FAIL %s: %v\n", rel, err)
			failed++
			return nil
		}
		converted++
		fmt.Fprintf(out, "ok   %s (%d elements)\n", rel, len(s.Elements))
		if !drawing.Is(rel) {
			stem, _ := drawing.SplitExt(rel)
			fmt.Fprintf(out, "     rename %s → %s%s\n", rel, stem, drawing.Ext)
		}
		return nil
	})
	return converted, failed, err
}
