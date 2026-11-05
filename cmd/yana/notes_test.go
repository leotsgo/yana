package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFlattenAndResolve(t *testing.T) {
	spaces := []spaceTree{
		{Name: "", Children: []treeNode{{Type: "note", Name: "loose.md", Path: "loose.md", ID: "L", Title: "Loose"}}},
		{Name: "home", Children: []treeNode{
			{Type: "dir", Name: "Projects", Path: "home/Projects", Children: []treeNode{
				{Type: "note", Name: "kiln.md", Path: "home/Projects/kiln.md", ID: "K", Kind: "md", Title: "Kiln"},
			}},
			{Type: "note", Name: "b.md", Path: "home/b.md", ID: "B", Title: "Plan", Children: []treeNode{
				{Type: "note", Name: "b.conflict-1.md", Path: "home/b.conflict-1.md", ID: "C"},
			}},
			{Type: "note", Name: "a.md", Path: "home/a.md", ID: "A", Title: "plan", Aliases: []string{"Alpha"}},
		}},
	}
	all := flatten(spaces)
	var paths []string
	for _, e := range all {
		paths = append(paths, e.Path)
	}
	want := []string{"loose.md", "home", "home/Projects", "home/Projects/kiln.md", "home/b.md", "home/b.conflict-1.md", "home/a.md"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("flatten: %v", paths)
	}
	for arg, id := range map[string]string{"K": "K", "home/Projects/kiln.md": "K", "home/Projects/kiln": "K", "/loose.md": "L"} {
		if e, ok := resolveNote(all, arg); !ok || e.ID != id {
			t.Errorf("resolve %q: %v %v", arg, e, ok)
		}
	}
	if _, ok := resolveNote(all, "home"); ok {
		t.Error("a folder resolved as a note")
	}
	// yana <x>: a path wins; else titles, ignoring case.
	if got := findNotes(all, "kiln"); len(got) != 1 || got[0].ID != "K" {
		t.Errorf("unique title: %v", got)
	}
	if got := findNotes(all, "Plan"); len(got) != 2 {
		t.Errorf("two notes titled plan: %v", got)
	}
	if got := findNotes(all, "home/a"); len(got) != 1 || got[0].ID != "A" {
		t.Errorf("path: %v", got)
	}
	if got := findNotes(all, "nothing"); len(got) != 0 {
		t.Errorf("nothing: %v", got)
	}
}

func TestResolveLink(t *testing.T) {
	all := []entry{
		{Path: "home", Dir: true},
		{Path: "home/Projects/kiln.md", ID: "K"},
		{Path: "home/journal.md", ID: "J", Aliases: []string{"Diary"}},
		{Path: "work/kiln.md", ID: "W"},
	}
	cases := map[string]string{
		"kiln":                "K", // a unique file name in the space, not work's
		"Projects/kiln":       "K",
		"journal|the journal": "J",
		"journal#Monday":      "J",
		"Diary":               "J",
		"../Projects/kiln.md": "",
		"missing":             "",
	}
	for target, id := range cases {
		e, ok := resolveLink(all, "home/journal.md", target)
		if (id == "") == ok || (ok && e.ID != id) {
			t.Errorf("%q: %v %v, want %q", target, e.ID, ok, id)
		}
	}
}

func TestPushRecent(t *testing.T) {
	ids := pushRecent([]string{"a", "b", "c"}, "b")
	if !reflect.DeepEqual(ids, []string{"b", "a", "c"}) {
		t.Fatalf("%v", ids)
	}
	var many []string
	for i := 0; i < recentMax+10; i++ {
		many = pushRecent(many, strings.Repeat("x", i+1))
	}
	if len(many) != recentMax {
		t.Fatalf("kept %d", len(many))
	}
}

func TestClassify(t *testing.T) {
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.Local)
	days := map[string]string{"today": "2026-11-04", "yesterday": "2026-11-03", "tomorrow": "2026-11-05", "2026-01-31": "2026-01-31"}
	for arg, day := range days {
		kind, d := classify(arg, now)
		if kind != "day" || d.Format("2006-01-02") != day {
			t.Errorf("%s: %s %s", arg, kind, d.Format("2006-01-02"))
		}
	}
	for _, arg := range []string{"serve", "scan", "login", "open", "new", "find", "grep", "recent", "config", "completion", "api", "__complete", "version", "--help", "drawings"} {
		if kind, _ := classify(arg, now); kind != "command" {
			t.Errorf("%s: %s", arg, kind)
		}
	}
	// Anything else names a note, dates that are not dates included.
	for _, arg := range []string{"Meeting notes", "home/kiln.md", "tod", "2026-13-01", "2026-02-30", "26-01-01"} {
		if kind, _ := classify(arg, now); kind != "note" {
			t.Errorf("%s: %s", arg, kind)
		}
	}
}

func TestCandidates(t *testing.T) {
	d := &compData{
		Server: "s",
		Dirs:   []string{"home", "home/Projects", "work"},
		Notes:  []string{"home/Projects/kiln.md", "home/a b.md", "home/plan.md", "loose.md", "work/x.md"},
	}
	data := func() *compData { return d }
	none := func() *compData { return nil }
	values := func(lines []string) (out []string) {
		for _, l := range lines {
			v, _, _ := strings.Cut(l, "\t")
			out = append(out, v)
		}
		return
	}
	// The first word: commands with their help, then paths.
	got := candidates([]string{"to"}, data)
	if !reflect.DeepEqual(values(got), []string{"today", "tomorrow"}) || !strings.Contains(got[0], "\t") {
		t.Errorf("to: %q", got)
	}
	if got := values(candidates([]string{"h"}, data)); !reflect.DeepEqual(got, []string{"home/"}) {
		t.Errorf("h: %v", got)
	}
	if got := values(candidates([]string{"l"}, data)); !reflect.DeepEqual(got, []string{"login", "logout", "loose.md"}) {
		t.Errorf("l: %v", got)
	}
	// Paths go one segment at a time.
	if got := candidates([]string{"open", "home/"}, data); !reflect.DeepEqual(got, []string{"home/Projects/", "home/a b.md", "home/plan.md"}) {
		t.Errorf("open home/: %v", got)
	}
	if got := candidates([]string{"open", "home/P"}, data); !reflect.DeepEqual(got, []string{"home/Projects/"}) {
		t.Errorf("open home/P: %v", got)
	}
	if got := candidates([]string{"open", "home/Projects/"}, data); !reflect.DeepEqual(got, []string{"home/Projects/kiln.md"}) {
		t.Errorf("open home/Projects/: %v", got)
	}
	if got := candidates([]string{"open", `home/a\ `}, data); !reflect.DeepEqual(got, []string{"home/a b.md"}) {
		t.Errorf("escaped space: %v", got)
	}
	if got := candidates([]string{"open", ""}, data); !reflect.DeepEqual(got, []string{"home/", "loose.md", "work/"}) {
		t.Errorf("open: %v", got)
	}
	// Spaces after --space, the flag on the commands that take it.
	if got := candidates([]string{"today", "--space", ""}, data); !reflect.DeepEqual(got, []string{"home", "work"}) {
		t.Errorf("--space: %v", got)
	}
	if got := candidates([]string{"new", "--space", "w"}, data); !reflect.DeepEqual(got, []string{"work"}) {
		t.Errorf("--space w: %v", got)
	}
	if got := values(candidates([]string{"yesterday", "--"}, data)); !reflect.DeepEqual(got, []string{"--space"}) {
		t.Errorf("flags: %v", got)
	}
	if got := candidates([]string{"login", "--"}, data); len(got) != 0 {
		t.Errorf("login takes no --space: %v", got)
	}
	if got := candidates([]string{"completion", ""}, data); !reflect.DeepEqual(got, []string{"bash", "zsh"}) {
		t.Errorf("completion: %v", got)
	}
	// No session: nothing from the server.
	if got := candidates([]string{"open", ""}, none); len(got) != 0 {
		t.Errorf("no session, open: %v", got)
	}
	if got := candidates([]string{"today", "--space", ""}, none); len(got) != 0 {
		t.Errorf("no session, --space: %v", got)
	}
	if got := values(candidates([]string{"op"}, none)); !reflect.DeepEqual(got, []string{"open"}) {
		t.Errorf("no session, commands still complete: %v", got)
	}
}

func TestExtractPlugin(t *testing.T) {
	dir := t.TempDir()
	if err := extractPlugin(dir); err != nil {
		t.Fatal(err)
	}
	init := filepath.Join(dir, "lua", "yana", "init.lua")
	plugin := filepath.Join(dir, "plugin", "yana.lua")
	want, _ := pluginFS.ReadFile("nvim/lua/yana/init.lua")
	if got, err := os.ReadFile(init); err != nil || string(got) != string(want) {
		t.Fatalf("missing files are written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "test")); err == nil {
		t.Fatal("the tests are not part of the plugin")
	}
	// Unchanged files are left alone; a changed one is put back.
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, p := range []string{init, plugin} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(plugin, []byte("-- edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(plugin, old, old)
	if err := extractPlugin(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(init); !fi.ModTime().Equal(old) {
		t.Error("an unchanged file was rewritten")
	}
	got, _ := os.ReadFile(plugin)
	want, _ = pluginFS.ReadFile("nvim/plugin/yana.lua")
	if string(got) != string(want) {
		t.Error("a changed file was not put back")
	}
	if fi, _ := os.Stat(plugin); fi.ModTime().Equal(old) {
		t.Error("the changed file kept its old mtime")
	}
}

func TestNvimArgs(t *testing.T) {
	if got := luaString("a]]b]=]c"); got != "[==[a]]b]=]c]==]" {
		t.Errorf("luaString: %s", got)
	}
	args := nvimArgs("/d/yana nvim", launchOpts{Open: "home/a.md", Server: "http://h", Host: "h"})
	if len(args) != 2 || args[0] != "-c" || !strings.Contains(args[1], `[[/d/yana nvim]]`) || !strings.Contains(args[1], `"open":"home/a.md"`) {
		t.Errorf("%q", args)
	}
}
