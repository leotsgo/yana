package git_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leotsgo/yana/internal/git"
	"github.com/leotsgo/yana/internal/index"
	"golang.org/x/crypto/ssh"
)

const sshURL = "git@example.invalid:me/notes.git"

// fakeSSH puts an ssh first on PATH that copies the key it was given and
// its mode into the returned directory, then refuses the login.
func fakeSSH(t *testing.T) (out string) {
	t.Helper()
	out = t.TempDir()
	bin := t.TempDir()
	script := `#!/bin/sh
while [ $# -gt 0 ]; do
  if [ "$1" = "-i" ]; then key="$2"; fi
  shift
done
cp "$key" "$OUT/key"
stat -c %a "$key" > "$OUT/mode"
echo "Permission denied (publickey)." >&2
exit 255
`
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OUT", out)
	return out
}

func keyedRemote(t *testing.T, l *git.Layer) index.GitRemote {
	t.Helper()
	_, sealedKey, err := l.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	sealed, _, err := l.OpenKey(sealedKey)
	if err != nil {
		t.Fatal(err)
	}
	return index.GitRemote{ID: "r1", Name: "origin", URL: sshURL, Secret: sealed, Enabled: true}
}

func TestSSHCommandUsesKeyFile(t *testing.T) {
	out := fakeSSH(t)
	l := sealingLayer(t)
	if _, err := l.Test(context.Background(), keyedRemote(t, l)); err == nil {
		t.Fatal("Test against a refusing ssh succeeded")
	}
	key, err := os.ReadFile(filepath.Join(out, "key"))
	if err != nil || !strings.Contains(string(key), "OPENSSH PRIVATE KEY") {
		t.Fatalf("ssh was not given the private key: %q, %v", key, err)
	}
	mode, _ := os.ReadFile(filepath.Join(out, "mode"))
	if strings.TrimSpace(string(mode)) != "600" {
		t.Fatalf("key file mode = %q, want 600", mode)
	}
}

func TestSSHRemoteWithoutKey(t *testing.T) {
	l := sealingLayer(t)
	_, err := l.Test(context.Background(), index.GitRemote{URL: sshURL})
	if err == nil || !strings.Contains(err.Error(), "no key yet") {
		t.Fatalf("err = %v, want the no-key message", err)
	}
}

func TestSSHKeyFileRemoved(t *testing.T) {
	fakeSSH(t)
	l := sealingLayer(t)
	r := keyedRemote(t, l)
	if _, err := l.Test(context.Background(), r); err == nil {
		t.Fatal("Test succeeded")
	}
	write(t, l, "a.md", "x\n")
	runGit(t, l.Root(), "add", "-A")
	runGit(t, l.Root(), "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "x")
	if err := l.Push(context.Background(), r); err == nil {
		t.Fatal("Push succeeded")
	}
	if left, _ := filepath.Glob(filepath.Join(l.Root(), ".sync", "ssh-key-*")); len(left) > 0 {
		t.Fatalf("key files left behind: %v", left)
	}
}

func TestStaleKeyFilesSwept(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ".sync", "ssh-key-old")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := git.New(dir, testOptions(), nil).Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale key file survived Ensure: %v", err)
	}
}

func TestExplainGitError(t *testing.T) {
	cases := []struct{ url, msg, want string }{
		{"git@github.com:me/n.git", "git@github.com: Permission denied (publickey).",
			"github.com refused the key. Check that it is added to this repository with write access."},
		{"git@github.com:me/n.git", "Host key verification failed.",
			"github.com presented a different host key than the one this server remembers. If that change is expected, forget the host key in Settings › Backup."},
		{"git@nope:me/n.git", "ssh: Could not resolve hostname nope",
			"Could not find nope. Check the address."},
		{"/x", "fatal: '/x' does not appear to be a git repository",
			"There is no repository at that address, or this key cannot see it."},
	}
	for _, c := range cases {
		got := git.ExplainGitError(c.url, errString(c.msg+"\nfatal: Could not read from remote repository."))
		if !strings.HasPrefix(got, c.want) || !strings.HasSuffix(got, "\nfatal: Could not read from remote repository.") {
			t.Errorf("%q: got %q", c.msg, got)
		}
	}
	if got := git.ExplainGitError(sshURL, errString("fatal: something else")); got != "fatal: something else" {
		t.Errorf("unrecognised error changed: %q", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestHostKeyAndForget(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	l := sealingLayer(t)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	line := "example.invalid " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + "\n"
	if err := os.WriteFile(filepath.Join(l.Root(), ".sync", "known_hosts"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := l.HostKey("git@example.invalid:x"); !strings.HasPrefix(got, "example.invalid SHA256:") {
		t.Fatalf("HostKey = %q", got)
	}
	if got := l.HostKey("/some/path.git"); got != "" {
		t.Fatalf("HostKey of a path = %q", got)
	}
	if err := l.ForgetHost("git@example.invalid:x"); err != nil {
		t.Fatal(err)
	}
	if got := l.HostKey("git@example.invalid:x"); got != "" {
		t.Fatalf("HostKey after forget = %q", got)
	}
	if err := (git.New(t.TempDir(), testOptions(), nil)).ForgetHost("git@example.invalid:x"); err != nil {
		t.Fatalf("ForgetHost with no known_hosts: %v", err)
	}
}
