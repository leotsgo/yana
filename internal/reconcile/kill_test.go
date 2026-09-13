package reconcile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
)

// Acceptance 5: kill the process mid-write-back; on restart no note is
// corrupt or truncated. The child (TestKillChild, re-executed from this
// test binary) appends numbered lines and flushes as fast as it can; the
// parent SIGKILLs it at a random moment, inspects the tree, restarts the
// loop over it, and checks that everything still agrees.
func TestKillMidWriteBack(t *testing.T) {
	if os.Getenv("YANA_KILL_CHILD") != "" {
		t.Skip("child process")
	}
	rounds := 6
	if testing.Short() {
		rounds = 2
	}
	dir := t.TempDir()
	h := newHarness(t, dir, testOptions())
	id := h.newNote("kill/note.md", "")
	h.close()

	for round := 0; round < rounds; round++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestKillChild$", "-test.v")
		cmd.Env = append(os.Environ(), "YANA_KILL_CHILD=1", "YANA_KILL_DIR="+dir, "YANA_KILL_ID="+id)
		var out strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(300+round*137%400) * time.Millisecond)
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = cmd.Wait()
		if strings.Contains(out.String(), "--- FAIL") || strings.Contains(out.String(), "panic:") {
			t.Fatalf("child failed:\n%s", out.String())
		}

		// The file is whole: a frontmatter block with the id, then
		// consecutively numbered complete lines.
		raw, err := os.ReadFile(filepath.Join(dir, "kill", "note.md"))
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		fm := frontmatter.Parse(raw)
		if fm.Meta.ID != id {
			t.Fatalf("round %d: frontmatter damaged: %q", round, raw[:min(len(raw), 80)])
		}
		fileLines := checkLines(t, round, "file", string(fm.Body))

		// The sidecar loads and is a consistent state too.
		if state, err := os.ReadFile(filepath.Join(dir, ".sync", "crdt", id+".bin")); err == nil {
			d, err := loadForTest(state)
			if err != nil {
				t.Fatalf("round %d: sidecar corrupt: %v", round, err)
			}
			checkLines(t, round, "sidecar", d)
		}

		// Restart: the document, file and index agree, and nothing the
		// file held is gone.
		h = newHarness(t, dir, testOptions())
		body := h.converged(id)
		docLines := checkLines(t, round, "restart", body)
		if docLines < fileLines {
			t.Fatalf("round %d: restart lost lines: file had %d, document has %d", round, fileLines, docLines)
		}
		h.close()
		t.Logf("round %d: file %d lines, after restart %d lines", round, fileLines, docLines)
	}
}

// checkLines asserts body is "line 1\nline 2\n..." and returns the count.
func checkLines(t *testing.T, round int, what, body string) int {
	t.Helper()
	if body == "" {
		return 0
	}
	if !strings.HasSuffix(body, "\n") {
		t.Fatalf("round %d: %s truncated: %q", round, what, tail(body))
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	for i, l := range lines {
		if l != "line "+strconv.Itoa(i+1) {
			t.Fatalf("round %d: %s line %d is %q", round, what, i+1, l)
		}
	}
	return len(lines)
}

func tail(s string) string {
	if len(s) > 60 {
		return "…" + s[len(s)-60:]
	}
	return s
}

// TestKillChild is the process the kill test runs and kills.
func TestKillChild(t *testing.T) {
	if os.Getenv("YANA_KILL_CHILD") == "" {
		t.Skip("only runs as the kill test's child")
	}
	dir, id := os.Getenv("YANA_KILL_DIR"), os.Getenv("YANA_KILL_ID")
	opts := testOptions()
	opts.IdleTime = 10 * time.Millisecond
	h := newHarness(t, dir, opts)
	c := h.newClient("k", id)
	n := len(strings.Split(strings.TrimSuffix(c.text(), "\n"), "\n"))
	if c.text() == "" {
		n = 0
	}
	for i := n + 1; ; i++ {
		c.appendText(fmt.Sprintf("line %d\n", i))
		if err := h.rec.Flush(h.ctx, id); err != nil {
			t.Fatal(err)
		}
	}
}
