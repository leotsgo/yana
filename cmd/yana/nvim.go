// yana at a terminal is neovim: the person's own nvim, config and
// plugins included, with the yana plugin (nvim/, embedded) on the
// runtimepath, browsing and editing the signed-in server. The plugin
// talks to the server through `yana api` (api.go).
//
//	yana                    the buffers left open last time, else the spaces
//	yana open <path|title>  that note too, made current
//	yana new <name>         a new note in the folder for new notes, opened
//	yana find|grep [query]  straight into the note or full-text picker
//	yana recent             the recently opened notes
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

//go:embed nvim/lua nvim/plugin
var pluginFS embed.FS

// extractPlugin writes the embedded plugin under dir, file by file, only
// where a file is missing or differs, so an unchanged plugin is left
// alone (and keeps its mtime).
func extractPlugin(dir string) error {
	return fs.WalkDir(pluginFS, "nvim", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p, "nvim")))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		want, err := pluginFS.ReadFile(p)
		if err != nil {
			return err
		}
		if have, err := os.ReadFile(target); err == nil && bytes.Equal(have, want) {
			return nil
		}
		tmp := target + ".tmp"
		if err := os.WriteFile(tmp, want, 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, target)
	})
}

// launchOpts is what the plugin's launch() is handed.
type launchOpts struct {
	Open   string `json:"open,omitempty"`   // a note path to open and make current
	Picker string `json:"picker,omitempty"` // sf, sg or recent
	Query  string `json:"query,omitempty"`  // the picker's prefilled prompt
	Server string `json:"server"`
	Host   string `json:"host"`
	Init   string `json:"init,omitempty"` // the person's yana init.lua, when there is one
}

// luaString quotes s as a Lua long string, whose contents are never
// interpreted.
func luaString(s string) string {
	eq := ""
	for strings.Contains(s, "]"+eq+"]") {
		eq += "="
	}
	return "[" + eq + "[" + s + "]" + eq + "]"
}

// nvimArgs is the command line that loads the plugin from dir after the
// person's own config and hands launch() opts. The runtimepath is set
// from -c, not --cmd, because plugin managers (lazy.nvim) reset it while
// the config loads.
func nvimArgs(dir string, opts launchOpts) []string {
	js, _ := json.Marshal(opts)
	lua := fmt.Sprintf("lua vim.opt.rtp:prepend(%s) vim.cmd.runtime('plugin/yana.lua') require('yana').launch(vim.json.decode(%s))",
		luaString(dir), luaString(string(js)))
	return []string{"-c", lua}
}

// launch replaces this process with neovim, so quitting neovim is
// quitting yana.
func launch(s *session, opts launchOpts) error {
	data, err := dataDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(data, "nvim")
	if err := extractPlugin(dir); err != nil {
		return fmt.Errorf("writing the neovim plugin to %s: %w", dir, err)
	}
	opts.Server, opts.Host = s.Server, hostOf(s.Server)
	if c, err := configDir(); err == nil {
		if p := filepath.Join(c, "init.lua"); fileExists(p) {
			opts.Init = p
		}
	}
	bin := os.Getenv("YANA_NVIM")
	if bin == "" {
		bin = "nvim"
	}
	nvim, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("%s is not on PATH; yana opens notes in neovim (YANA_NVIM names another binary)", bin)
	}
	env := os.Environ()
	if self, err := os.Executable(); err == nil {
		env = append(env, "YANA_BIN="+self)
	}
	return syscall.Exec(nvim, append([]string{bin}, nvimArgs(dir, opts)...), env)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func sessionForLaunch() (*session, error) {
	s, err := loadSession()
	if err != nil {
		// Bare yana lands here too; someone who meant the server is told how.
		return nil, fmt.Errorf("%w (yana serve runs the server)", err)
	}
	return s, nil
}

// openMain is yana open <x> and yana <x>: the note at that id or path,
// else the one note titled so; several titled so open the picker on it.
func openMain(arg string) error {
	s, err := sessionForLaunch()
	if err != nil {
		return err
	}
	all, err := fetchTree(s)
	if err != nil {
		return err
	}
	switch found := findNotes(all, arg); len(found) {
	case 0:
		return fmt.Errorf("no note at %s (yana find %s searches)", arg, arg)
	case 1:
		return launch(s, launchOpts{Open: found[0].Path})
	default:
		return launch(s, launchOpts{Picker: "sf", Query: arg})
	}
}

// newMain is yana new [--space s] <name>: the note goes where the owner
// set new notes to go (the folder, else the space), else in the first
// space; a name holding a slash is a path from the top.
func newMain(args []string) error {
	fl := flag.NewFlagSet("new", flag.ContinueOnError)
	space := fl.String("space", "", "the space to make the note in")
	if err := fl.Parse(args); err != nil {
		return err
	}
	name := strings.TrimSpace(strings.Join(fl.Args(), " "))
	if name == "" {
		return errors.New("usage: yana new [--space name] <name>")
	}
	s, err := sessionForLaunch()
	if err != nil {
		return err
	}
	p := name
	if !strings.Contains(name, "/") {
		dir, err := newNotesFolder(s, *space)
		if err != nil {
			return err
		}
		p = dir + "/" + name
	}
	e, err := create(s, p)
	if err != nil {
		return err
	}
	return launch(s, launchOpts{Open: e.Path})
}

func newNotesFolder(s *session, space string) (string, error) {
	if space != "" {
		return space, nil
	}
	var d struct {
		Space  string `json:"space"`
		Folder string `json:"folder"`
	}
	if err := s.do(http.MethodGet, "/api/defaults", nil, &d); err != nil {
		return "", err
	}
	if d.Folder != "" {
		return d.Folder, nil
	}
	if d.Space != "" {
		return d.Space, nil
	}
	all, err := fetchTree(s)
	if err != nil {
		return "", err
	}
	for _, e := range all {
		if e.Dir && !strings.Contains(e.Path, "/") {
			return e.Path, nil
		}
	}
	return "", errors.New("this account has no space to write to")
}

func pickerMain(picker string, args []string) error {
	s, err := sessionForLaunch()
	if err != nil {
		return err
	}
	return launch(s, launchOpts{Picker: picker, Query: strings.Join(args, " ")})
}
