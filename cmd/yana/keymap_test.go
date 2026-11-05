package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseKeys(t *testing.T) {
	leader := []string{" "}
	cases := map[string][]string{
		"gg":               {"g", "g"},
		"<C-p>":            {"ctrl+p"},
		"<CR>":             {"enter"},
		"<Esc>":            {"esc"},
		"<Space>":          {" "},
		"<BS>":             {"backspace"},
		"<leader>sf":       {" ", "s", "f"},
		"<leader><leader>": {" ", " "},
		"<S-Tab>":          {"shift+tab"},
		"<M-x>":            {"alt+x"},
		"a<lt>b":           {"a", "<", "b"},
		"<":                {"<"},
	}
	for in, want := range cases {
		got, err := parseKeys(in, leader)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := parseKeys("<Nope>", leader); err == nil {
		t.Error("an unknown key parsed")
	}
	if _, err := parseKeys("<leader>", nil); err == nil {
		t.Error("<leader> inside the leader parsed")
	}
}

func TestMatchSeq(t *testing.T) {
	km, err := defaultConfig().keymap()
	if err != nil {
		t.Fatal(err)
	}
	// Each case feeds keys one by one and lists the actions that fire.
	cases := []struct {
		keys []string
		want []string
	}{
		{[]string{"g", "g"}, []string{"top"}},
		{[]string{"g", "?"}, []string{"help"}},
		{[]string{"d", "d"}, []string{"delete"}},
		{[]string{"c", "w"}, []string{"rename"}},
		{[]string{" ", "s", "f"}, []string{"find_notes"}},
		{[]string{" ", "s", "g"}, []string{"grep"}},
		{[]string{" ", " "}, []string{"recent"}},
		{[]string{"j", "k", "G", "enter", "ctrl+d"}, []string{"down", "up", "bottom", "open", "half_down"}},
		// A key that breaks a sequence runs on its own.
		{[]string{"g", "j"}, []string{"down"}},
		{[]string{"d", "x", "d", "d"}, []string{"delete"}},
		{[]string{" ", "s", "x", "q"}, []string{"quit"}},
	}
	for _, c := range cases {
		var got, pending []string
		for _, k := range c.keys {
			var a string
			a, pending = matchSeq(km, pending, k)
			if a != "" {
				got = append(got, a)
			}
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.keys, got, c.want)
		}
	}
}

func TestMergeConfig(t *testing.T) {
	def := defaultConfig()
	c, err := mergeConfig(def, []byte("leader: \",\"\neditor: vim\nkeys:\n  parent: [\"<BS>\"]\n  find_notes: [\"<leader>f\", \"<C-f>\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Leader != "," || c.Editor != "vim" {
		t.Fatalf("%+v", c)
	}
	km, _ := c.keymap()
	if km[","+seqSep+"f"] != "find_notes" || km["ctrl+f"] != "find_notes" || km["backspace"] != "parent" {
		t.Fatal("the file's keys are not bound")
	}
	if km["-"] != "" || km[" "+seqSep+"s"+seqSep+"f"] != "" {
		t.Fatal("a replaced binding is still there")
	}
	if km["j"] != "down" || km[","+seqSep+","] != "recent" {
		t.Fatal("an action the file left out lost its default")
	}
	if !reflect.DeepEqual(def.Keys["parent"], []string{"-", "h", "<BS>", "<Left>"}) {
		t.Fatal("merging changed the defaults")
	}
	for _, bad := range []string{"keys: [", "keys:\n  fly: [x]\n", "keys:\n  up: [\"<Bogus>\"]\n", "leader: ab\n"} {
		got, err := mergeConfig(def, []byte(bad))
		if err == nil || !reflect.DeepEqual(got, def) {
			t.Errorf("%q: %v, want the defaults and an error", bad, err)
		}
	}
	lines := strings.Join(bindings(km, ","), "\n")
	if !strings.Contains(lines, "<leader>f") || !strings.Contains(lines, "<C-f>") {
		t.Fatalf("bindings:\n%s", lines)
	}
}

func TestPickNotesShortcuts(t *testing.T) {
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	notes := []entry{
		{Path: "home/kiln.md", Tags: []string{"pottery"}},
		{Path: "home/today-list.md"},
		{Path: "work/kiln-budget.md", Tags: []string{"money"}},
		{Path: "work/plan.md", Tags: []string{"pottery"}},
	}
	paths := func(items []pickItem) (out []string) {
		for _, it := range items {
			out = append(out, it.e.Path)
		}
		return
	}
	got := pickNotes("today", notes, now)
	if len(got) < 2 || !got[0].virtual() || got[0].date.Format("2006-01-02") != "2026-11-04" || got[1].e.Path != "home/today-list.md" {
		t.Fatalf("today: %v", paths(got))
	}
	for q, day := range map[string]string{"yesterday": "2026-11-03", "tom": "2026-11-05", "2026-01-31": "2026-01-31"} {
		if got := pickNotes(q, notes, now); len(got) == 0 || !got[0].virtual() || got[0].date.Format("2006-01-02") != day {
			t.Errorf("%s: %v", q, paths(got))
		}
	}
	if got := pickNotes("to", notes, now); len(got) > 0 && got[0].virtual() {
		t.Error("two letters made a date")
	}
	if got := paths(pickNotes("#pot", notes, now)); !reflect.DeepEqual(got, []string{"home/kiln.md", "work/plan.md"}) {
		t.Errorf("#pot: %v", got)
	}
	if got := paths(pickNotes("work/kiln", notes, now)); !reflect.DeepEqual(got, []string{"work/kiln-budget.md"}) {
		t.Errorf("work/kiln: %v", got)
	}
	if got := paths(pickNotes("work/ #pottery", notes, now)); !reflect.DeepEqual(got, []string{"work/plan.md"}) {
		t.Errorf("work/ #pottery: %v", got)
	}
}

func TestComplete(t *testing.T) {
	all := []entry{
		{Path: "home", Dir: true}, {Path: "home/Projects", Dir: true},
		{Path: "home/kiln.md"}, {Path: "home/Projects/plan.md"},
	}
	if got := complete("re", all); !reflect.DeepEqual(got, []string{"rename", "recent"}) {
		t.Errorf("re: %v", got)
	}
	if got := complete("e kil", all); !reflect.DeepEqual(got, []string{"e home/kiln.md"}) {
		t.Errorf("e kil: %v", got)
	}
	if got := complete("cd proj", all); !reflect.DeepEqual(got, []string{"cd home/Projects"}) {
		t.Errorf("cd proj: %v", got)
	}
	if got := complete("sg x", all); got != nil {
		t.Errorf("sg takes no completion: %v", got)
	}
}
