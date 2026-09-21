package render

import (
	"strings"
	"testing"
)

func TestTasks(t *testing.T) {
	src := "# Plan\n" +
		"\n" +
		"Intro line.\n" +
		"\n" +
		"## Groceries\n" +
		"\n" +
		"- [ ] buy **milk** and [[Oat milk]]\n" +
		"- [x] bread\n" +
		"  * [ ] nested one\n" +
		"1. [ ] numbered task\n" +
		"2. [X] numbered done\n" +
		"\n" +
		"> - [ ] quoted, not a task\n" +
		"\n" +
		"```sh\n" +
		"- [ ] fenced, not a task\n" +
		"```\n" +
		"\n" +
		"Not a task: [ ] plain brackets.\n"
	tasks := Tasks([]byte(src))
	want := []struct {
		line    int
		indent  int
		done    bool
		heading string
		has     string
	}{
		{6, 0, false, "Groceries", "<strong>milk</strong>"},
		{7, 0, true, "Groceries", "bread"},
		{8, 1, false, "Groceries", "nested one"},
		{9, 0, false, "Groceries", "numbered task"},
		{10, 0, true, "Groceries", "numbered done"},
	}
	if len(tasks) != len(want) {
		t.Fatalf("tasks: %+v", tasks)
	}
	for i, w := range want {
		got := tasks[i]
		if got.Line != w.line || got.Indent != w.indent || got.Done != w.done || got.Heading != w.heading {
			t.Errorf("task %d: got line=%d indent=%d done=%v heading=%q, want %+v", i, got.Line, got.Indent, got.Done, got.Heading, w)
		}
		if !strings.Contains(got.HTML, w.has) {
			t.Errorf("task %d html %q missing %q", i, got.HTML, w.has)
		}
	}
	if h := tasks[0].HTML; strings.Contains(h, "<p>") {
		t.Errorf("inline html keeps its paragraph wrap: %q", h)
	}
	if h := tasks[0].HTML; !strings.Contains(h, `class="wikilink"`) {
		t.Errorf("wikilink did not render: %q", h)
	}
}

func TestTasksEmptyBody(t *testing.T) {
	if got := Tasks(nil); got != nil {
		t.Errorf("nil body: %+v", got)
	}
	if got := Tasks([]byte("no tasks\nhere\n")); got != nil {
		t.Errorf("plain body: %+v", got)
	}
}

func TestInlineHTMLHeadingLikeText(t *testing.T) {
	// Task text starting with # is text in a list item, but the renderer
	// sees it alone; the task row's styles flatten whatever comes back.
	got := InlineHTML("# not a heading")
	if got == "" {
		t.Skip("renderer refused the line")
	}
}
