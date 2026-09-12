package crdt

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Deln0r/ygo"
)

// node runs the JS fixture script; the test is skipped when node or the
// yjs dependency is not installed (run `npm install` in spike/crdt/js).
func node(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if _, err := os.Stat(filepath.Join("js", "node_modules", "yjs")); err != nil {
		t.Skip("yjs not installed in spike/crdt/js (npm install)")
	}
	cmd := exec.Command("node", append([]string{"--no-warnings", "fixtures.mjs"}, args...)...)
	cmd.Dir = "js"
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestCrossLanguage proves wire compatibility in both directions with the
// JavaScript reference implementation, which is what the browser client
// will run.
func TestCrossLanguage(t *testing.T) {
	dir := t.TempDir()
	abs := func(name string) string {
		p, _ := filepath.Abs(filepath.Join(dir, name))
		return p
	}

	// JS -> Go
	jsText := node(t, "encode", abs("js.bin"))
	raw, err := os.ReadFile(abs("js.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ygo.ValidateUpdate(raw); err != nil {
		t.Fatal(err)
	}
	g := newReplica("go")
	g.apply(t, raw)
	if g.String() != jsText {
		t.Fatalf("JS->Go mismatch:\n go=%q\n js=%q", g.String(), jsText)
	}

	// Go -> JS, including a whole-cell (overlapping) incremental update
	// delivered out of order, which JS must de-duplicate.
	u1 := g.edit(t, func(txn *ygo.TransactionMut) { _ = g.text.Insert(txn, g.text.Length(), "go-1 ") })
	u2 := g.edit(t, func(txn *ygo.TransactionMut) { _ = g.text.Insert(txn, g.text.Length(), "go-2 ") })
	u3 := g.edit(t, func(txn *ygo.TransactionMut) {
		if err := ApplyTextDiff(txn, g.text, g.text.String()+"go-3"); err != nil {
			t.Fatal(err)
		}
	})
	for name, b := range map[string][]byte{"u1.bin": u1, "u2.bin": u2, "u3.bin": u3} {
		if err := os.WriteFile(abs(name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := node(t, "apply", abs("js.bin"), abs("u1.bin"), abs("u3.bin"), abs("u2.bin"))
	if got != g.String() {
		t.Fatalf("Go->JS mismatch:\n js=%q\n go=%q", got, g.String())
	}

	// Full state round trip: JS loads Go's whole state, edits, sends back a
	// delta computed against a state vector.
	if err := os.WriteFile(abs("state.bin"), ygo.EncodeStateAsUpdate(g.doc), 0o644); err != nil {
		t.Fatal(err)
	}
	jsAfter := node(t, "roundtrip", abs("state.bin"), abs("delta.bin"))
	delta, err := os.ReadFile(abs("delta.bin"))
	if err != nil {
		t.Fatal(err)
	}
	g.apply(t, delta)
	if g.String() != jsAfter {
		t.Fatalf("round trip mismatch:\n go=%q\n js=%q", g.String(), jsAfter)
	}
}
