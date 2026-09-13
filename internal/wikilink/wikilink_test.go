package wikilink

import "testing"

// mainNotes is one space named "main"; other/target.md lives in a second
// space and must never be reachable from main.
func mainNotes() []NoteRef {
	return []NoteRef{
		{ID: "home", RelPath: "main/home.md"},
		{ID: "target", RelPath: "main/target.md"},
		{ID: "nested", RelPath: "main/guides/nested.md"},
		{ID: "deep", RelPath: "main/guides/deep/x.md"},
		{ID: "page", RelPath: "main/page.html"},
		{ID: "twinA", RelPath: "main/a/twin.md"},
		{ID: "twinB", RelPath: "main/b/twin.md"},
	}
}

func TestResolveOrder(t *testing.T) {
	r := NewResolver("main", mainNotes())
	cases := []struct {
		name   string
		raw    string
		from   string
		wantID string
		want   Rule
		ok     bool
	}{
		{"sibling", "target", "main/home.md", "target", RuleRelative, true},
		{"sibling with extension", "target.md", "main/home.md", "target", RuleRelative, true},
		{"sibling dir", "guides/nested", "main/home.md", "nested", RuleRelative, true},
		{"up and over", "../../target", "main/guides/deep/x.md", "target", RuleRelative, true},
		{"up one", "../nested", "main/guides/deep/x.md", "nested", RuleRelative, true},
		{"html sibling", "page.html", "main/home.md", "page", RuleRelative, true},
		{"root path", "target", "main/guides/nested.md", "target", RuleRoot, true},
		{"root path with extension", "target.md", "main/guides/nested.md", "target", RuleRoot, true},
		{"root html", "page.html", "main/guides/nested.md", "page", RuleRoot, true},
		{"filename unique", "nested", "main/a/twin.md", "nested", RuleFilename, true},
		{"filename ambiguous", "twin", "main/home.md", "", "", false},
		{"cross space", "other/target", "main/home.md", "", "", false},
		{"escape the space", "../../../etc/keys", "main/guides/deep/x.md", "", "", false},
		{"missing", "nowhere", "main/home.md", "", "", false},
		{"empty", "  ", "main/home.md", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Resolve(tc.raw, tc.from)
			if got.OK != tc.ok || got.ToID != tc.wantID || got.Rule != tc.want {
				t.Fatalf("Resolve(%q, %q) = %+v, want id=%q rule=%q ok=%v",
					tc.raw, tc.from, got, tc.wantID, tc.want, tc.ok)
			}
		})
	}
}

func TestResolveLooseNotes(t *testing.T) {
	r := NewResolver("", []NoteRef{
		{ID: "loose", RelPath: "loose.md"},
		{ID: "journal", RelPath: "journal/today.md"},
	})
	// With no space directory the space root is the notes root, so this is
	// a root-path match, not a filename one.
	if got := r.Resolve("loose", "journal/today.md"); !got.OK || got.Rule != RuleRoot || got.ToID != "loose" {
		t.Fatalf("root match in the notes root: %+v", got)
	}
	if got := r.Resolve("today", "journal/today.md"); !got.OK || got.ToID != "journal" || got.Rule != RuleRelative {
		t.Fatalf("sibling match: %+v", got)
	}
}

func TestRewriteTarget(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
		from string
		to   string
		ext  bool
		want string
	}{
		{"relative same dir", RuleRelative, "main/home.md", "main/renamed.md", false, "renamed"},
		{"relative into subdir", RuleRelative, "main/home.md", "main/guides/renamed.md", false, "guides/renamed"},
		{"relative out of subdir", RuleRelative, "main/guides/home.md", "main/renamed.md", false, "../renamed"},
		{"relative with ext", RuleRelative, "main/home.md", "main/renamed.md", true, "renamed.md"},
		{"root", RuleRoot, "main/home.md", "main/guides/renamed.md", false, "guides/renamed"},
		{"root with ext", RuleRoot, "main/home.md", "main/guides/renamed.md", true, "guides/renamed.md"},
		{"filename", RuleFilename, "main/home.md", "main/guides/renamed.md", false, "renamed"},
		{"filename with ext", RuleFilename, "main/home.md", "main/guides/renamed.md", true, "renamed.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RewriteTarget(tc.rule, tc.from, tc.to, tc.ext); got != tc.want {
				t.Fatalf("RewriteTarget(%q, %q, %q, %v) = %q, want %q",
					tc.rule, tc.from, tc.to, tc.ext, got, tc.want)
			}
		})
	}
}

func TestHasExtension(t *testing.T) {
	for _, s := range []string{"Foo", "docs/Foo", ""} {
		if HasExtension(s) {
			t.Fatalf("HasExtension(%q) = true", s)
		}
	}
	for _, s := range []string{"Foo.md", "docs/Foo.MARKDOWN", "p.html", "p.htm"} {
		if !HasExtension(s) {
			t.Fatalf("HasExtension(%q) = false", s)
		}
	}
}
