package crdt

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// node runs the Phase 0 spike's reference fixtures and returns what they
// print. The tests skip when node or the fixture's yjs install is
// missing, the same rule the spike's own cross-language tests use.
func node(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if _, err := os.Stat(filepath.Join("..", "..", "spike", "crdt", "js", "node_modules", "yjs")); err != nil {
		t.Skip("yjs not installed in spike/crdt/js (npm ci)")
	}
	cmd := exec.Command("node", append([]string{"--no-warnings", "fixtures.mjs"}, args...)...)
	cmd.Dir = filepath.Join("..", "..", "spike", "crdt", "js")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node %v: %v", args, err)
	}
	return string(out)
}

// TestCrossLanguage runs the same exchanges as the Phase 0 spike's
// cross-language harness, through the bind surface: a JS-authored state
// loads here, Go-authored updates land there out of order, and a full
// state round trip through JS converges.
func TestCrossLanguage(t *testing.T) {
	dir := t.TempDir()
	abs := func(name string) string {
		p, _ := filepath.Abs(filepath.Join(dir, name))
		return p
	}

	jsText := node(t, "encode", abs("js.bin"))
	raw, err := os.ReadFile(abs("js.bin"))
	if err != nil {
		t.Fatal(err)
	}
	d := newDocID(t, 1)
	defer d.Close()
	if _, err := d.ApplyUpdate(raw); err != nil {
		t.Fatal(err)
	}
	if got := d.Text(); got != jsText {
		t.Fatalf("JS->Go mismatch: go=%q js=%q", got, jsText)
	}

	// Go edits, delivered to JS out of order.
	var ups [][]byte
	for _, w := range []string{"go-1 ", "go-2 ", "go-3"} {
		u, err := d.Insert(d.Len(), w)
		if err != nil {
			t.Fatal(err)
		}
		ups = append(ups, u)
	}
	names := []string{abs("js.bin")}
	for i, u := range ups {
		p := abs("u" + string(rune('1'+i)) + ".bin")
		if err := os.WriteFile(p, u, 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, p)
	}
	names[2], names[3] = names[3], names[2] // deliver u1, u3, u2
	if got := node(t, append([]string{"apply"}, names...)...); got != d.Text() {
		t.Fatalf("Go->JS mismatch: js=%q go=%q", got, d.Text())
	}

	// Full state to JS, JS edits, delta back here.
	if err := os.WriteFile(abs("state.bin"), d.State(), 0o644); err != nil {
		t.Fatal(err)
	}
	jsAfter := node(t, "roundtrip", abs("state.bin"), abs("delta.bin"))
	delta, err := os.ReadFile(abs("delta.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ApplyUpdate(delta); err != nil {
		t.Fatal(err)
	}
	if got := d.Text(); got != jsAfter {
		t.Fatalf("round trip mismatch: go=%q js=%q", got, jsAfter)
	}
}

// TestDeltaToJSReference applies a JS-produced update, encodes a delta
// against the state the reference held, and round-trips it back: the
// acceptance exchange for the bind package.
func TestDeltaToJSReference(t *testing.T) {
	dir := t.TempDir()
	abs := func(name string) string {
		p, _ := filepath.Abs(filepath.Join(dir, name))
		return p
	}

	jsText := node(t, "encode", abs("js.bin"))
	raw, err := os.ReadFile(abs("js.bin"))
	if err != nil {
		t.Fatal(err)
	}

	// A twin holding exactly the reference state supplies its state
	// vector; the document then moves on with local edits.
	twin, err := LoadDoc(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer twin.Close()
	d := newDocID(t, 9)
	defer d.Close()
	if _, err := d.ApplyUpdate(raw); err != nil {
		t.Fatal(err)
	}
	if d.Text() != jsText {
		t.Fatalf("JS->Go mismatch: go=%q js=%q", d.Text(), jsText)
	}
	sv := twin.StateVector()
	for _, w := range []string{"alpha ", "βeta ", "🙂 ", "delta"} {
		if u := d.ReplaceText(d.Text() + w); u == nil {
			t.Fatalf("replace with %q produced no update", w)
		}
	}
	// A concurrent edit from a third writer lands in between.
	third := newDocID(t, 10)
	defer third.Close()
	if _, err := third.Insert(0, "[c] "); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ApplyUpdate(third.State()); err != nil {
		t.Fatal(err)
	}

	delta, err := d.Diff(sv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs("js.bin"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs("delta.bin"), delta, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := node(t, "apply", abs("js.bin"), abs("delta.bin")); got != d.Text() {
		t.Fatalf("delta round trip mismatch: js=%q go=%q", got, d.Text())
	}
}
