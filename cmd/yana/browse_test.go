package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestFuzzyRank(t *testing.T) {
	notes := []entry{
		{Path: "home/r/e/c/i/p/e.md"},
		{Path: "work/specs/recipe-engine.md"},
		{Path: "home/recipes.md", Title: "Recipes"},
		{Path: "home/journal.md", Title: "Jam and bread"},
		{Path: "home/unrelated.md"},
	}
	got := rankNotes("recipe", notes)
	if len(got) != 3 || got[0].Path != "home/recipes.md" || got[2].Path != "home/r/e/c/i/p/e.md" {
		t.Fatalf("recipe: %v", got)
	}
	if got := rankNotes("jam", notes); len(got) != 1 || got[0].Path != "home/journal.md" {
		t.Fatalf("jam matches the title: %v", got)
	}
	if got := rankNotes("rcpeng", notes); len(got) != 1 || got[0].Path != "work/specs/recipe-engine.md" {
		t.Fatalf("rcpeng: %v", got)
	}
	if got := rankNotes("zzz", notes); len(got) != 0 {
		t.Fatalf("zzz: %v", got)
	}
	if got := rankNotes("", notes); len(got) != len(notes) || got[0].Path != notes[0].Path {
		t.Fatal("an empty query keeps every note in order")
	}
	// The best alignment wins, not the first: "spec" right after a slash
	// beats the s of "work" followed by scattered letters.
	a, _ := fuzzyScore("spec", "work/specs")
	b, _ := fuzzyScore("spec", "work/s/p/e/c")
	if a <= b {
		t.Fatalf("spec: %d <= %d", a, b)
	}
}

func TestFlattenAndList(t *testing.T) {
	spaces := []spaceTree{
		{Name: "", Children: []treeNode{{Type: "note", Name: "loose.md", Path: "loose.md", ID: "L"}}},
		{Name: "home", Children: []treeNode{
			{Type: "dir", Name: "Projects", Path: "home/Projects", Children: []treeNode{
				{Type: "note", Name: "kiln.md", Path: "home/Projects/kiln.md", ID: "K", Kind: "md"},
			}},
			{Type: "note", Name: "b.md", Path: "home/b.md", ID: "B", Children: []treeNode{
				{Type: "note", Name: "b.conflict-1.md", Path: "home/b.conflict-1.md", ID: "C"},
			}},
			{Type: "note", Name: "a.md", Path: "home/a.md", ID: "A"},
		}},
	}
	all := flatten(spaces)
	names := func(es []entry) (out []string) {
		for _, e := range es {
			out = append(out, e.Path)
		}
		return
	}
	if got := names(listDir(all, "")); !reflect.DeepEqual(got, []string{"home", "loose.md"}) {
		t.Fatalf("top: %v", got)
	}
	want := []string{"home/Projects", "home/a.md", "home/b.conflict-1.md", "home/b.md"}
	if got := names(listDir(all, "home")); !reflect.DeepEqual(got, want) {
		t.Fatalf("home: %v", got)
	}
	if got := names(listDir(all, "home/Projects")); !reflect.DeepEqual(got, []string{"home/Projects/kiln.md"}) {
		t.Fatalf("projects: %v", got)
	}
	for arg, id := range map[string]string{"K": "K", "home/Projects/kiln.md": "K", "home/Projects/kiln": "K", "/loose.md": "L"} {
		if e, ok := resolveNote(all, arg); !ok || e.ID != id {
			t.Errorf("resolve %q: %v %v", arg, e, ok)
		}
	}
	if _, ok := resolveNote(all, "home"); ok {
		t.Error("a folder resolved as a note")
	}
	if parentOf("home") != "" || parentOf("home/Projects/kiln.md") != "home/Projects" {
		t.Error("parentOf")
	}
}

func TestMatchLine(t *testing.T) {
	text := "# Kiln\n\nfiring schedule\ncone 6 glaze\n"
	if got := matchLine(text, "…cone 6 <mark>glaze</mark>…", "glaze"); got != 4 {
		t.Fatalf("marked term: %d", got)
	}
	if got := matchLine(text, "", "Firing"); got != 3 {
		t.Fatalf("query word: %d", got)
	}
	if got := matchLine(text, "", "absent"); got != 0 {
		t.Fatalf("absent: %d", got)
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
