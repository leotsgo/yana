// Host keys and error wording for SSH remotes. The server remembers a
// host's key the first time it connects (accept-new) in .sync/known_hosts;
// the owner can read the fingerprint it holds and forget it.
package git

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// sshHost returns the host a validated address names, as known_hosts spells
// it: [host]:port when the address gives a port. ok is false for a path.
func sshHost(raw string) (host, known string, ok bool) {
	if !IsSSH(raw) {
		return "", "", false
	}
	if strings.HasPrefix(raw, "ssh://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", "", false
		}
		if p := u.Port(); p != "" {
			return u.Hostname(), "[" + u.Hostname() + "]:" + p, true
		}
		return u.Hostname(), u.Hostname(), true
	}
	_, rest, _ := strings.Cut(raw, "@")
	host, _, _ = strings.Cut(rest, ":")
	return host, host, true
}

// HostKey returns "<host> SHA256:…" for the key this server remembers for
// the address's host, or "" when it has none or the address is a path.
func (l *Layer) HostKey(raw string) string {
	host, known, ok := sshHost(raw)
	if !ok {
		return ""
	}
	out, err := exec.Command("ssh-keygen", "-l", "-F", known, "-f", l.knownHosts()).Output()
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(out), "\n") {
		for _, f := range strings.Fields(ln) {
			if strings.HasPrefix(f, "SHA256:") {
				return host + " " + f
			}
		}
	}
	return ""
}

// ForgetHost drops the remembered key for the address's host, so the next
// connection learns whatever key the host presents.
func (l *Layer) ForgetHost(raw string) error {
	_, known, ok := sshHost(raw)
	if !ok {
		return nil
	}
	if _, err := os.Stat(l.knownHosts()); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if out, err := exec.Command("ssh-keygen", "-R", known, "-f", l.knownHosts()).CombinedOutput(); err != nil {
		return fmt.Errorf("forget host key: %s", strings.TrimSpace(string(out)))
	}
	_ = os.Remove(l.knownHosts() + ".old")
	return nil
}

// ExplainGitError puts a plain sentence ahead of git's own last line for
// the failures an owner can act on; any other error is returned unchanged.
func ExplainGitError(raw string, err error) string {
	msg := err.Error()
	host, _, _ := sshHost(raw)
	var say string
	switch {
	case strings.Contains(msg, "Permission denied (publickey)"):
		say = host + " refused the key. Check that it is added to this repository with write access."
	case strings.Contains(msg, "Host key verification failed"), strings.Contains(msg, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
		say = host + " presented a different host key than the one this server remembers. If that change is expected, forget the host key in Settings › Backup."
	case strings.Contains(msg, "Could not resolve hostname"):
		_, after, _ := strings.Cut(msg, "Could not resolve hostname ")
		after, _, _ = strings.Cut(after, "\n")
		name, _, _ := strings.Cut(after, ":")
		say = "Could not find " + strings.TrimSpace(name) + ". Check the address."
	case strings.Contains(msg, "does not appear to be a git repository"):
		say = "There is no repository at that address, or this key cannot see it."
	default:
		return msg
	}
	lines := strings.Split(strings.TrimSpace(msg), "\n")
	return say + "\n" + strings.TrimSpace(lines[len(lines)-1])
}
