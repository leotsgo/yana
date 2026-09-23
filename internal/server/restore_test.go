package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
)

// gitOut runs git in dir and returns its combined output.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// writeFile writes a note under the git env's root with an old mtime, so
// the scanner and the settle logic treat it as settled.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(abs, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestGitRestoreFromRemote(t *testing.T) {
	e := newGitEnv(t)
	ctx := context.Background()

	// A second space, and a note the restore must bring back.
	writeFile(t, e.dir, "proj/plan.md", "---\nid: plan-1\n---\n# Plan\n\nthe plan body\n")
	e.rec.Sync(ctx, "proj/plan.md")
	plan, err := e.db.GetNoteByPath(ctx, "proj/plan.md")
	if err != nil {
		t.Fatalf("plan not indexed: %v", err)
	}
	_ = plan
	hist, err := e.db.GetNoteByPath(ctx, "home/hist.md")
	if err != nil {
		t.Fatalf("hist not indexed: %v", err)
	}
	v1, err := os.ReadFile(filepath.Join(e.dir, "home", "hist.md"))
	if err != nil {
		t.Fatal(err)
	}

	// A backup remote holding the tree as it stands.
	bare := filepath.Join(t.TempDir(), "backup.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitOut(t, bare, "init", "--bare", "-b", "main")
	var remote index.GitRemote
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "backup", "url": bare, "schedule": "commit"}, &remote); code != 201 {
		t.Fatalf("create remote: %d", code)
	}
	var pushed struct {
		OK     bool
		Remote index.GitRemote
	}
	if code := e.post(t, "/api/git/remotes/"+remote.ID+"/push", map[string]any{}, &pushed); code != 200 || !pushed.OK {
		t.Fatalf("push: %d %+v", code, pushed)
	}

	// An editor holds the note open; the tree then moves on.
	text, err := e.rec.Text(ctx, hist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "first version") {
		t.Fatalf("unexpected document text: %q", text)
	}
	fm := frontmatter.Parse(v1)
	v2 := append([]byte{}, fm.Head...)
	v2 = append(v2, []byte(strings.Replace(string(fm.Body), "first version", "second version", 1))...)
	writeFile(t, e.dir, "home/hist.md", string(v2))
	e.rec.Sync(ctx, "home/hist.md")
	if text, _ = e.rec.Text(ctx, hist.ID); !strings.Contains(text, "second version") {
		t.Fatalf("document did not follow the edit: %q", text)
	}
	// The space is lost on disk.
	if err := os.RemoveAll(filepath.Join(e.dir, "proj")); err != nil {
		t.Fatal(err)
	}
	e.rec.Sync(ctx, "proj/plan.md")

	// The preview describes the backup without touching anything.
	var pv struct {
		Preview struct {
			Commit   string
			Commits  int64
			Notes    int
			Relation string
			Newest   struct{ Subject string }
		}
	}
	if code := e.post(t, "/api/git/remotes/"+remote.ID+"/restore/preview", map[string]any{}, &pv); code != 200 {
		t.Fatalf("preview: %d", code)
	}
	if pv.Preview.Relation != "identical" || pv.Preview.Commits < 1 || pv.Preview.Notes < 2 || pv.Preview.Newest.Subject == "" {
		t.Fatalf("preview: %+v", pv.Preview)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "proj")); !os.IsNotExist(err) {
		t.Fatal("the preview touched the tree")
	}

	// The restore needs the remote's name, exactly.
	var errOut struct{ Error string }
	if code := e.post(t, "/api/git/remotes/"+remote.ID+"/restore", map[string]any{"confirm": "nope"}, &errOut); code != 400 {
		t.Fatalf("wrong confirm accepted: %d %s", code, errOut.Error)
	}

	var res struct {
		OK      bool
		Commit  string
		Tag     string
		Added   int
		Changed int
		Deleted int
	}
	if code := e.post(t, "/api/git/remotes/"+remote.ID+"/restore", map[string]any{"confirm": "backup"}, &res); code != 200 || !res.OK {
		t.Fatalf("restore: %d %+v", code, res)
	}
	if res.Commit != pv.Preview.Commit || !strings.HasPrefix(res.Tag, "pre-restore/") || res.Added < 1 || res.Changed < 1 {
		t.Fatalf("restore report: %+v", res)
	}

	// The lost space is back, with its id.
	got, err := os.ReadFile(filepath.Join(e.dir, "proj", "plan.md"))
	if err != nil || !strings.Contains(string(got), "id: plan-1") {
		t.Fatalf("plan.md after restore: %q %v", got, err)
	}
	if _, err := e.db.GetNote(ctx, "plan-1"); err != nil {
		t.Fatalf("the restored note is not indexed: %v", err)
	}
	// The open editor converged to the restored text.
	if text, _ := e.rec.Text(ctx, hist.ID); !strings.Contains(text, "first version") {
		t.Fatalf("the open editor did not converge: %q", text)
	}
	if b, err := os.ReadFile(filepath.Join(e.dir, "home", "hist.md")); err != nil || strings.Contains(string(b), "second version") {
		t.Fatalf("hist.md after restore: %q %v", b, err)
	}
	// The pre-restore state is reachable without the reflog.
	if out := gitOut(t, e.dir, "tag", "-l", "pre-restore/*"); strings.TrimSpace(out) == "" {
		t.Fatal("no pre-restore tag")
	}
	if out := gitOut(t, e.dir, "rev-parse", "HEAD"); strings.TrimSpace(out) != res.Commit {
		t.Fatalf("HEAD %s, want %s", out, res.Commit)
	}
	// .sync belongs to this server.
	if _, err := os.Stat(filepath.Join(e.dir, ".sync", "index.db")); err != nil {
		t.Fatalf("the index did not survive the restore: %v", err)
	}
}

func TestGitRestoreRefusals(t *testing.T) {
	e := newGitEnv(t)

	var off index.GitRemote
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "off", "url": filepath.Join(t.TempDir(), "x.git"), "enabled": false}, &off); code != 201 {
		t.Fatalf("create disabled: %d", code)
	}
	if code := e.post(t, "/api/git/remotes/"+off.ID+"/restore/preview", map[string]any{}, nil); code != 400 {
		t.Fatalf("disabled preview: %d", code)
	}
	if code := e.post(t, "/api/git/remotes/"+off.ID+"/restore", map[string]any{"confirm": "off"}, nil); code != 400 {
		t.Fatalf("disabled restore: %d", code)
	}

	var dead index.GitRemote
	if code := e.post(t, "/api/git/remotes", map[string]any{"name": "dead", "url": filepath.Join(t.TempDir(), "missing.git")}, &dead); code != 201 {
		t.Fatalf("create dead: %d", code)
	}
	var errOut struct{ Error string }
	if code := e.post(t, "/api/git/remotes/"+dead.ID+"/restore/preview", map[string]any{}, &errOut); code != 502 || errOut.Error == "" {
		t.Fatalf("unreachable preview: %d %q", code, errOut.Error)
	}
	if code := e.post(t, "/api/git/remotes/"+dead.ID+"/restore", map[string]any{"confirm": "dead"}, &errOut); code != 502 {
		t.Fatalf("unreachable restore: %d", code)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "home", "hist.md")); err != nil {
		t.Fatalf("an unreachable restore touched the tree: %v", err)
	}
}

func TestGitRestoreNeedsOwner(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)
	gl := git.New(f.dir, git.Options{Quiet: time.Hour, Interval: time.Hour, DB: f.db}, nil)
	if err := gl.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.srv.Git = gl
	ctx := context.Background()
	remote := index.GitRemote{ID: "r1", Name: "backup", URL: filepath.Join(t.TempDir(), "x.git"), Schedule: git.ScheduleNightly, Enabled: true, CreatedAt: time.Now()}
	if err := f.db.CreateGitRemote(ctx, remote); err != nil {
		t.Fatal(err)
	}
	if code, _ := doPost(t, f.ts, http.MethodPost, "/api/git/remotes/r1/restore/preview", w.samHdr, map[string]any{}); code != 403 {
		t.Fatalf("editor previewing a restore: %d", code)
	}
	if code, _ := doPost(t, f.ts, http.MethodPost, "/api/git/remotes/r1/restore", w.samHdr, map[string]any{"confirm": "backup"}); code != 403 {
		t.Fatalf("editor restoring: %d", code)
	}
	// The owner passes the gate and reaches the remote's own failure.
	if code, _ := doPost(t, f.ts, http.MethodPost, "/api/git/remotes/r1/restore/preview", w.ownerHdr, map[string]any{}); code != 502 {
		t.Fatalf("owner previewing a restore: %d", code)
	}
}
