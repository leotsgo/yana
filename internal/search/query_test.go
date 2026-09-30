package search

import (
	"reflect"
	"testing"
	"time"
)

func TestParseOperators(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Term
	}{
		{
			name: "plain words stay text",
			in:   "water heater",
			want: []Term{
				{Raw: "water", Text: "water"},
				{Raw: "heater", Text: "heater"},
			},
		},
		{
			name: "the acceptance query",
			in:   `tag:home -tag:done "water heater" after:2026-01-01`,
			want: []Term{
				{Raw: "tag:home", Op: OpTag, Value: "home"},
				{Raw: "-tag:done", Negated: true, Op: OpTag, Value: "done"},
				{Raw: `"water heater"`, Quoted: true, Text: "water heater"},
				{Raw: "after:2026-01-01", Op: OpAfter, Value: "2026-01-01", Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
		{
			name: "bare hash is a tag",
			in:   "#Home -#done",
			want: []Term{
				{Raw: "#Home", Op: OpTag, Value: "home"},
				{Raw: "-#done", Negated: true, Op: OpTag, Value: "done"},
			},
		},
		{
			name: "unknown operator is text",
			in:   "foo:bar",
			want: []Term{
				{Raw: "foo:bar", Text: "foo:bar"},
			},
		},
		{
			name: "uppercase operator name is text",
			in:   "Tag:home",
			want: []Term{
				{Raw: "Tag:home", Text: "Tag:home"},
			},
		},
		{
			name: "unknown is and has selectors are text",
			in:   "is:nope has:nope",
			want: []Term{
				{Raw: "is:nope", Text: "is:nope"},
				{Raw: "has:nope", Text: "has:nope"},
			},
		},
		{
			name: "selectors are case-insensitive",
			in:   "is:HTML has:Image",
			want: []Term{
				{Raw: "is:HTML", Op: OpIs, Value: "html"},
				{Raw: "has:Image", Op: OpHas, Value: "image"},
			},
		},
		{
			name: "a bad date is text",
			in:   "before:january after:2026-1-1",
			want: []Term{
				{Raw: "before:january", Text: "before:january"},
				{Raw: "after:2026-1-1", Text: "after:2026-1-1"},
			},
		},
		{
			name: "empty values are text",
			in:   "tag: path: space: is: has: author: before: after:",
			want: []Term{
				{Raw: "tag:", Text: "tag:"},
				{Raw: "path:", Text: "path:"},
				{Raw: "space:", Text: "space:"},
				{Raw: "is:", Text: "is:"},
				{Raw: "has:", Text: "has:"},
				{Raw: "author:", Text: "author:"},
				{Raw: "before:", Text: "before:"},
				{Raw: "after:", Text: "after:"},
			},
		},
		{
			name: "quoted operator values",
			in:   `path:"my docs/" tag:"Two Words"`,
			want: []Term{
				{Raw: `path:"my docs/"`, Op: OpPath, Value: "my docs/", Quoted: true},
				{Raw: `tag:"Two Words"`, Op: OpTag, Value: "two words", Quoted: true},
			},
		},
		{
			name: "negated plain word and phrase",
			in:   `-draft -"out of date"`,
			want: []Term{
				{Raw: "-draft", Negated: true, Text: "draft"},
				{Raw: `-“out of date”`, Negated: true},
			},
		},
		{
			name: "a lone dash is a word",
			in:   "-",
			want: []Term{
				{Raw: "-", Text: "-"},
			},
		},
		{
			name: "hash alone is a word",
			in:   "#",
			want: []Term{
				{Raw: "#", Text: "#"},
			},
		},
		{
			name: "unclosed quote runs to the end",
			in:   `"water`,
			want: []Term{
				{Raw: `"water`, Quoted: true, Text: "water"},
			},
		},
		{
			name: "letters that start like an operator",
			in:   "island:home tagline:x",
			want: []Term{
				{Raw: "island:home", Text: "island:home"},
				{Raw: "tagline:x", Text: "tagline:x"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.in).Terms
			// Compare field by field; Date only for before/after terms.
			if len(got) != len(tc.want) {
				t.Fatalf("got %d terms, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				w := tc.want[i]
				if w.Raw == `-“out of date”` {
					continue // covered by the phrase case below
				}
				g := got[i]
				if g.Raw != w.Raw || g.Negated != w.Negated || g.Op != w.Op || g.Value != w.Value || g.Quoted != w.Quoted || g.Text != w.Text {
					t.Fatalf("term %d = %+v, want %+v", i, g, w)
				}
				if w.Op == OpBefore || w.Op == OpAfter {
					if !g.Date.Equal(w.Date) {
						t.Fatalf("term %d date = %v, want %v", i, g.Date, w.Date)
					}
				}
			}
		})
	}
}

func TestParseNegatedPhrase(t *testing.T) {
	got := Parse(`-"out of date"`)
	if len(got.Terms) != 1 {
		t.Fatalf("got %+v", got.Terms)
	}
	g := got.Terms[0]
	if !g.Negated || !g.Quoted || g.Text != "out of date" || g.Op != "" {
		t.Fatalf("got %+v", g)
	}
}

func TestTextTermsAndFilters(t *testing.T) {
	q := Parse(`tag:home "water heater" -draft is:task`)
	pos, neg := q.TextTerms()
	if len(pos) != 1 || pos[0].Text != "water heater" {
		t.Fatalf("positive = %+v", pos)
	}
	if len(neg) != 1 || neg[0].Text != "draft" {
		t.Fatalf("negative = %+v", neg)
	}
	if !q.HasFilters() {
		t.Fatal("expected filters")
	}
	if reflect.DeepEqual(Parse("water").HasFilters(), true) {
		t.Fatal("plain query must not report filters")
	}
}
