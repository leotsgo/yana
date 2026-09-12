package reearth

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/reearth/ygo/crdt"
)

func node(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if _, err := os.Stat(filepath.Join("..", "js", "node_modules", "yjs")); err != nil {
		t.Skip("yjs not installed in spike/crdt/js (npm install)")
	}
	cmd := exec.Command("node", append([]string{"--no-warnings", "fixtures.mjs"}, args...)...)
	cmd.Dir = filepath.Join("..", "js")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node %v: %v", args, err)
	}
	return string(out)
}

func TestCrossLanguage(t *testing.T) {
	dir := t.TempDir()
	abs := func(name string) string { p, _ := filepath.Abs(filepath.Join(dir, name)); return p }

	jsText := node(t, "encode", abs("js.bin"))
	raw, err := os.ReadFile(abs("js.bin"))
	if err != nil {
		t.Fatal(err)
	}
	g := newReplica()
	g.apply(t, raw)
	if g.text.ToString() != jsText {
		t.Fatalf("JS->Go mismatch: go=%q js=%q", g.text.ToString(), jsText)
	}

	var ups [][]byte
	for _, w := range []string{"go-1 ", "go-2 ", "go-3"} {
		n := g.text.Len()
		ups = append(ups, g.edit(func(txn *crdt.Transaction) { g.text.Insert(txn, n, w, nil) }))
	}
	names := []string{abs("js.bin")}
	for i, u := range ups {
		p := abs("u" + string(rune('1'+i)) + ".bin")
		if err := os.WriteFile(p, u, 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, p)
	}
	// Deliver out of order: u1, u3, u2.
	names[2], names[3] = names[3], names[2]
	if got := node(t, append([]string{"apply"}, names...)...); got != g.text.ToString() {
		t.Fatalf("Go->JS mismatch: js=%q go=%q", got, g.text.ToString())
	}

	if err := os.WriteFile(abs("state.bin"), g.doc.EncodeStateAsUpdate(), 0o644); err != nil {
		t.Fatal(err)
	}
	jsAfter := node(t, "roundtrip", abs("state.bin"), abs("delta.bin"))
	delta, err := os.ReadFile(abs("delta.bin"))
	if err != nil {
		t.Fatal(err)
	}
	g.apply(t, delta)
	if g.text.ToString() != jsAfter {
		t.Fatalf("round trip mismatch: go=%q js=%q", g.text.ToString(), jsAfter)
	}
}
