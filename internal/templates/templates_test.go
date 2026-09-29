package templates

import (
	"reflect"
	"testing"
	"time"
)

func when() time.Time {
	return time.Date(2026, 9, 29, 14, 5, 0, 0, time.UTC)
}

func TestApply(t *testing.T) {
	day := when()
	tests := []struct {
		name    string
		body    string
		values  Values
		body_   string
		cursor  int
		prompts []string
	}{
		{
			name:   "plain body passes through",
			body:   "# Hello\n\nSome words.\n",
			body_:  "# Hello\n\nSome words.\n",
			cursor: -1,
		},
		{
			name:   "every variable",
			body:   "{{title}} {{date}} {{time}} {{user}} {{space}} {{folder}}\n",
			values: Values{Title: "Kickoff", When: day, User: "sam", Space: "work", Folder: "meetings"},
			body_:  "Kickoff 2026-09-29 14:05 sam work meetings\n",
			cursor: -1,
		},
		{
			name:   "date formats",
			body:   "{{date:YYYY-MM-DD}} {{date:DD/MM/YYYY}} {{date:YY.MM.DD}} {{date:HH:mm}}\n",
			values: Values{When: day},
			body_:  "2026-09-29 29/09/2026 26.09.29 14:05\n",
			cursor: -1,
		},
		{
			name:   "date pattern passes unknown letters through",
			body:   "{{date:MMMM YYYY}}\n",
			values: Values{When: day},
			body_:  "MMMM 2026\n",
			cursor: -1,
		},
		{
			name:   "unknown variables stay",
			body:   "{{title}} and {{nope}} and {{ title }}\n",
			values: Values{Title: ""},
			body_:  "{{title}} and {{nope}} and {{ title }}\n",
			cursor: -1,
		},
		{
			name:   "empty title stays as written",
			body:   "# {{title}}\n",
			values: Values{Title: ""},
			body_:  "# {{title}}\n",
			cursor: -1,
		},
		{
			name:   "cursor removed at offset",
			body:   "# {{title}}\n\n- {{cursor}}first\n",
			values: Values{Title: "Pack"},
			body_:  "# Pack\n\n- first\n",
			cursor: len("# Pack\n\n- "),
		},
		{
			name:   "first cursor wins and later ones drop",
			body:   "a{{cursor}}b{{cursor}}c\n",
			body_:  "abc\n",
			cursor: 1,
		},
		{
			name:   "cursor counts runes not bytes",
			body:   "áé{{cursor}}\n",
			body_:  "áé\n",
			cursor: 2,
		},
		{
			name:   "fenced code blocks are left alone",
			body:   "{{title}}\n\n```\n{{title}} {{cursor}} {{nope}}\n```\n\n{{user}}\n",
			values: Values{Title: "T", User: "sam"},
			body_:  "T\n\n```\n{{title}} {{cursor}} {{nope}}\n```\n\nsam\n",
			cursor: -1,
		},
		{
			name:   "tilde fences too",
			body:   "~~~\n{{title}}\n~~~\n{{title}}\n",
			values: Values{Title: "T"},
			body_:  "~~~\n{{title}}\n~~~\nT\n",
			cursor: -1,
		},
		{
			name:   "indented fence",
			body:   "   ```\n{{title}}\n   ```\n{{title}}\n",
			values: Values{Title: "T"},
			body_:  "   ```\n{{title}}\n   ```\nT\n",
			cursor: -1,
		},
		{
			name:   "inline code is substituted",
			body:   "`{{title}}`\n",
			values: Values{Title: "T"},
			body_:  "`T`\n",
			cursor: -1,
		},
		{
			name:    "prompts collect distinct labels in order",
			body:    "{{prompt:Who}} met {{prompt:Where}} and {{prompt:Who}}\n",
			values:  Values{},
			body_:   "{{prompt:Who}} met {{prompt:Where}} and {{prompt:Who}}\n",
			cursor:  -1,
			prompts: []string{"Who", "Where"},
		},
		{
			name:    "prompt answers fill in",
			body:    "{{prompt:Who}} met at {{prompt:Time}}\n",
			values:  Values{Answers: map[string]string{"Who": "sam", "Time": "noon"}},
			body_:   "sam met at noon\n",
			cursor:  -1,
			prompts: []string{"Who", "Time"},
		},
		{
			name:    "unanswered prompt stays",
			body:    "{{prompt:Who}} met\n",
			values:  Values{Answers: map[string]string{"Other": "x"}},
			body_:   "{{prompt:Who}} met\n",
			cursor:  -1,
			prompts: []string{"Who"},
		},
		{
			name:   "prompts inside fences are not asked",
			body:   "```\n{{prompt:Who}}\n```\n",
			values: Values{},
			body_:  "```\n{{prompt:Who}}\n```\n",
			cursor: -1,
		},
		{
			name:    "multi-line variable answer",
			body:    "a\n{{prompt:Notes}}\nb\n",
			values:  Values{Answers: map[string]string{"Notes": "l1\nl2"}},
			body_:   "a\nl1\nl2\nb\n",
			cursor:  -1,
			prompts: []string{"Notes"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Apply(tt.body, tt.values)
			if got.Body != tt.body_ {
				t.Fatalf("body = %q, want %q", got.Body, tt.body_)
			}
			if got.Cursor != tt.cursor {
				t.Fatalf("cursor = %d, want %d", got.Cursor, tt.cursor)
			}
			if !reflect.DeepEqual(got.Prompts, tt.prompts) {
				t.Fatalf("prompts = %v, want %v", got.Prompts, tt.prompts)
			}
		})
	}
}

// A template documenting its own variables: everything inside the fence
// survives, everything outside is substituted, and the file the note
// gets has no braces left except the fenced example.
func TestApplyNoBracesExceptFenced(t *testing.T) {
	body := "# {{title}}\n\n{{date}} — {{user}}\n\n{{cursor}}Variables:\n\n```\n{{title}} {{date}} {{cursor}}\n```\n"
	got := Apply(body, Values{Title: "Meeting", When: when(), User: "sam"})
	want := "# Meeting\n\n2026-09-29 — sam\n\nVariables:\n\n```\n{{title}} {{date}} {{cursor}}\n```\n"
	if got.Body != want {
		t.Fatalf("body = %q, want %q", got.Body, want)
	}
	if got.Cursor != 29 { // runes: past the heading, the date line and the blank line
		t.Fatalf("cursor = %d", got.Cursor)
	}
}

// Body without a trailing newline keeps its shape.
func TestApplyWithoutTrailingNewline(t *testing.T) {
	got := Apply("{{title}} {{cursor}}end", Values{Title: "T"})
	if got.Body != "T end" || got.Cursor != 2 {
		t.Fatalf("apply = %q cursor %d", got.Body, got.Cursor)
	}
}
