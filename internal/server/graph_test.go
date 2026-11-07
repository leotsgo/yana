package server

import (
	"reflect"
	"testing"

	"github.com/leotsgo/yana/internal/index"
)

func TestGraphEndpoint(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	var g index.Graph
	if code := e.get(t, "/api/graph", &g); code != 200 {
		t.Fatalf("graph %d", code)
	}
	var paths []string
	pos := map[string]int{}
	for i, n := range g.Notes {
		paths = append(paths, n.Path)
		pos[n.Path] = i
	}
	want := []string{"main/docs/guide.md", "main/hello.md", "main/second.md", "other/note.md"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("notes = %v", paths)
	}
	// hello-second (both directions, once) and guide-hello.
	wantLinks := [][2]int{{pos["main/docs/guide.md"], pos["main/hello.md"]}, {pos["main/hello.md"], pos["main/second.md"]}}
	if !reflect.DeepEqual(g.Links, wantLinks) {
		t.Fatalf("links = %v want %v", g.Links, wantLinks)
	}
	if len(g.Ghosts) != 1 || g.Ghosts[0].Target != "missing" || !reflect.DeepEqual(g.Ghosts[0].From, []int{pos["main/hello.md"]}) {
		t.Fatalf("ghosts = %+v", g.Ghosts)
	}
}

func TestGraphRespectsSpaces(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)
	notePaths := func(hdr string) []string {
		code, body := doGet(t, f.ts, "/api/graph", hdr)
		if code != 200 {
			t.Fatalf("graph %d", code)
		}
		var out []string
		for _, n := range body["notes"].([]any) {
			out = append(out, n.(map[string]any)["path"].(string))
		}
		return out
	}
	if got := notePaths(w.samHdr); !reflect.DeepEqual(got, []string{"home/home-secret.md"}) {
		t.Fatalf("sam sees %v", got)
	}
	found := false
	for _, p := range notePaths(w.ownerHdr) {
		found = found || p == "work/work-secret.md"
	}
	if !found {
		t.Fatal("owner does not see work/work-secret.md")
	}
	if code, _ := doGet(t, f.ts, "/api/graph", ""); code != 401 {
		t.Fatalf("anonymous got %d", code)
	}
}

// A user in no space must not see the vault root: root notes have space
// "" and belong to the owner alone.
func TestGraphAndTasksForUserWithoutSpaces(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)
	f.writeNote(t, "rootTask.md", "- [ ] root-only task\n")
	_, lonelyHdr := f.account(t, "lonely", "lonely-password1")

	code, body := doGet(t, f.ts, "/api/graph", lonelyHdr)
	if code != 200 {
		t.Fatalf("graph %d", code)
	}
	if notes := body["notes"].([]any); len(notes) != 0 {
		t.Fatalf("lonely sees %v", notes)
	}
	code, body = doGet(t, f.ts, "/api/tasks?count=1", lonelyHdr)
	if code != 200 || body["count"] != float64(0) {
		t.Fatalf("tasks count %d %v", code, body)
	}
	// Control: the owner does see the root task.
	if _, body = doGet(t, f.ts, "/api/tasks?count=1", w.ownerHdr); body["count"] != float64(1) {
		t.Fatalf("owner tasks %v", body)
	}
}
