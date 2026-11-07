package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leotsgo/yana/internal/git"
	"github.com/leotsgo/yana/internal/index"
)

func TestParseRemoteURL(t *testing.T) {
	ok := map[string]string{
		" git@github.com:me/notes.git ":     "git@github.com:me/notes.git",
		"ssh://git@host:2222/srv/notes.git": "ssh://git@host:2222/srv/notes.git",
		"/mnt/backup/notes.git":             "/mnt/backup/notes.git",
	}
	for in, want := range ok {
		got, err := git.ParseRemoteURL(in)
		if err != nil || got != want {
			t.Errorf("%q: got (%q, %v), want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"https://github.com/me/notes.git", "http://h/x", "git://h/x", "ssh://me:pw@h/x",
		"ext::sh -c x", "-oProxyCommand=x", "relative/path", "",
	} {
		if _, err := git.ParseRemoteURL(in); !errors.Is(err, git.ErrBadRemoteURL) {
			t.Errorf("%q: err = %v, want ErrBadRemoteURL", in, err)
		}
	}
	if got := git.RedactURL("https://me:secret@host/x"); strings.Contains(got, "secret") {
		t.Fatalf("redaction left the password in %q", got)
	}
}

func TestIsSSH(t *testing.T) {
	for in, want := range map[string]bool{
		"ssh://git@host/x": true, "git@github.com:me/n.git": true, "/mnt/backup/n.git": false,
	} {
		if got := git.IsSSH(in); got != want {
			t.Errorf("IsSSH(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSealRoundTrip(t *testing.T) {
	fakeSSH(t)
	dir := t.TempDir()
	opts := testOptions()
	opts.SecretPath = filepath.Join(dir, ".sync", "git_secret")
	l := git.New(dir, opts, nil)
	if err := l.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	sealed, err := l.Seal("ghp_token")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "ghp_token") {
		t.Fatal("sealed credential holds the plaintext")
	}
	// A second layer over the same key file opens it; a layer over a
	// different key does not.
	again := git.New(dir, opts, nil)
	if err := again.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	remote := index.GitRemote{ID: "r", Name: "x", URL: sshURL, Secret: sealed, Enabled: true}
	if _, err := again.Test(context.Background(), remote); err == nil || strings.Contains(err.Error(), "credential") {
		t.Fatalf("test with the right key should fail on the remote, not the credential: %v", err)
	}
	other := testOptions()
	other.SecretPath = filepath.Join(t.TempDir(), "git_secret")
	o := git.New(t.TempDir(), other, nil)
	if err := o.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Test(context.Background(), remote); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("test with the wrong key: %v", err)
	}
}

// bareRemote makes an empty bare repository to push to.
func bareRemote(t testing.TB) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, remote, "init", "--bare", "-b", "main")
	return remote
}

func TestRemotePushOnCommit(t *testing.T) {
	dir := t.TempDir()
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	remote := bareRemote(t)
	ctx := context.Background()
	if err := db.CreateGitRemote(ctx, index.GitRemote{
		ID: "r1", Name: "backup", URL: remote, Schedule: git.ScheduleCommit, Enabled: true, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	// A disabled remote and a nightly one that is not due must be left alone.
	if err := db.CreateGitRemote(ctx, index.GitRemote{
		ID: "r2", Name: "off", URL: bareRemote(t), Schedule: git.ScheduleCommit, Enabled: false, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	opts := testOptions()
	opts.DB = db
	l := git.New(dir, opts, nil)
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	l.Start()
	defer l.Close()
	write(t, l, "home/a.md", "one\n")
	l.Notify("home/a.md")
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := gitOutput(remote, "log", "--format=%s")
		if strings.Contains(out, "file") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote never received the commit; log %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	r, err := db.GetGitRemote(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Pushes != 1 || r.LastPush.IsZero() || r.LastError != "" {
		t.Fatalf("row after push: %+v", r)
	}
	if st := l.Stats(); st.Pushes != 1 || st.Remotes != 1 {
		t.Fatalf("stats: %+v", st)
	}
	// Nothing new: the loop must not push again.
	time.Sleep(4 * opts.Quiet)
	if r2, _ := db.GetGitRemote(ctx, "r1"); r2.Pushes != 1 {
		t.Fatalf("pushed without a new commit: %d", r2.Pushes)
	}
	// A second commit pushes once more.
	write(t, l, "home/a.md", "two\n")
	l.Notify("home/a.md")
	deadline = time.Now().Add(10 * time.Second)
	for {
		r2, _ := db.GetGitRemote(ctx, "r1")
		if r2.Pushes == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("second push never happened: %+v", r2)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if off, _ := db.GetGitRemote(ctx, "r2"); off.Pushes != 0 {
		t.Fatal("disabled remote was pushed")
	}
}

func TestRemotePushFailureRecorded(t *testing.T) {
	dir := t.TempDir()
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	opts := testOptions()
	opts.DB = db
	l := git.New(dir, opts, nil)
	ctx := context.Background()
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	write(t, l, "home/a.md", "one\n")
	l.Snapshot(ctx)
	r := index.GitRemote{ID: "r1", Name: "gone", URL: filepath.Join(t.TempDir(), "missing.git"), Schedule: git.ScheduleCommit, Enabled: true, CreatedAt: time.Now()}
	if err := db.CreateGitRemote(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := l.Push(ctx, r); err == nil {
		t.Fatal("push to a missing repository succeeded")
	}
	got, _ := db.GetGitRemote(ctx, "r1")
	if got.LastError == "" || got.LastErrorAt.IsZero() || got.Pushes != 0 {
		t.Fatalf("failure not recorded: %+v", got)
	}
	if st := l.Stats(); st.Errors == 0 || !strings.Contains(st.LastError, "gone") {
		t.Fatalf("stats after failure: %+v", st)
	}
	// The connection test reports the same failure without touching the row.
	if _, err := l.Test(ctx, r); err == nil {
		t.Fatal("test of a missing repository succeeded")
	}
}
