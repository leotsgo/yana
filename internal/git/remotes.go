// Backup remotes: the repositories the history layer pushes to. The owner
// configures them in settings; rows live in the index (real state, like
// agent tokens) and the layer reads them on each tick, so a change takes
// effect without a restart.
//
// Credentials never travel in the URL, on a command line, or in a log
// line. A remote's private key is sealed with AES-GCM under a key in
// .sync/git_secret and written to a 0600 file for the one git subprocess
// that needs it, then removed.
package git

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/leotsgo/yana/internal/fsutil"
	"github.com/leotsgo/yana/internal/index"
)

// Push schedules. Every schedule pushes only when there is a commit the
// remote has not seen.
const (
	// ScheduleCommit pushes after every commit window.
	ScheduleCommit = "commit"
	// ScheduleHourly pushes at most once an hour.
	ScheduleHourly = "hourly"
	// ScheduleNightly pushes once a day at the remote's push hour.
	ScheduleNightly = "nightly"
)

// ValidSchedule reports whether s names a push schedule.
func ValidSchedule(s string) bool {
	return s == ScheduleCommit || s == ScheduleHourly || s == ScheduleNightly
}

// retryBackoff is how long a remote whose push failed waits before the
// loop tries it again; an explicit push ignores it.
const retryBackoff = 10 * time.Minute

// pushTimeout bounds one push or connection test.
const pushTimeout = 10 * time.Minute

// ErrBadRemoteURL is returned for addresses the layer will not hand to git.
var ErrBadRemoteURL = errors.New("remote must be an SSH address (git@host:path or ssh://…) or an absolute path to a bare repository")

// ErrNoSealKey is returned when a credential cannot be stored because the
// key file is unavailable.
var ErrNoSealKey = errors.New("credential storage is unavailable on this server")

var scpLike = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9._-]+:[^\s]+$`)

// ParseRemoteURL validates a remote address: an SSH address or an
// absolute path. Credentials never ride in an address, so one with a
// password is refused.
func ParseRemoteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "-") || strings.ContainsAny(raw, " \t\r\n\x00") || len(raw) > 2048 {
		return "", ErrBadRemoteURL
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "ssh" || u.Host == "" {
			return "", ErrBadRemoteURL
		}
		if u.User != nil {
			if _, has := u.User.Password(); has {
				return "", ErrBadRemoteURL
			}
		}
		return raw, nil
	}
	if scpLike.MatchString(raw) || filepath.IsAbs(raw) {
		return raw, nil
	}
	return "", ErrBadRemoteURL
}

// IsSSH reports whether a validated address is reached over SSH rather
// than being a path.
func IsSSH(url string) bool {
	return strings.HasPrefix(url, "ssh://") || scpLike.MatchString(url)
}

// RedactURL hides any userinfo in a URL for logs.
func RedactURL(raw string) string {
	if !strings.Contains(raw, "://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, has := u.User.Password(); has {
		u.User = url.UserPassword(u.User.Username(), "redacted")
	}
	return u.String()
}

// --- sealing -----------------------------------------------------------------

func (l *Layer) loadSealKey() error {
	if l.opts.SecretPath == "" {
		return nil
	}
	if b, err := os.ReadFile(l.opts.SecretPath); err == nil && len(b) >= 32 {
		l.sealKey = b[:32]
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := fsutil.MkdirInherit(filepath.Dir(l.opts.SecretPath)); err != nil {
		return err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	if err := os.WriteFile(l.opts.SecretPath, b, 0o600); err != nil {
		return err
	}
	l.sealKey = b
	return nil
}

// Seal encrypts a credential for storage.
func (l *Layer) Seal(plain string) ([]byte, error) {
	if len(l.sealKey) == 0 {
		return nil, ErrNoSealKey
	}
	block, err := aes.NewCipher(l.sealKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return append(nonce, gcm.Seal(nil, nonce, []byte(plain), nil)...), nil
}

func (l *Layer) open(sealed []byte) (string, error) {
	if len(l.sealKey) == 0 {
		return "", ErrNoSealKey
	}
	block, err := aes.NewCipher(l.sealKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("sealed credential is truncated")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("stored credential does not open with this server's key")
	}
	return string(plain), nil
}

// --- remotes -----------------------------------------------------------------

// NewRemoteID returns a fresh remote id.
func NewRemoteID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "r_" + hex.EncodeToString(b)
}

// seedRemote carries YANA_GIT_REMOTE into the table on first run.
func (l *Layer) seedRemote(ctx context.Context) error {
	if l.opts.DB == nil {
		return nil
	}
	existing, err := l.opts.DB.ListGitRemotes(ctx)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.noteRemotes(existing)
	l.mu.Unlock()
	if len(existing) > 0 || l.opts.Remote == "" {
		return nil
	}
	clean, err := ParseRemoteURL(l.opts.Remote)
	if err != nil {
		return err
	}
	r := index.GitRemote{
		ID: NewRemoteID(), Name: "remote", URL: clean, Schedule: ScheduleNightly, PushHour: l.opts.PushHour,
		Enabled: true, CreatedAt: l.opts.Now(),
	}
	if err := l.opts.DB.CreateGitRemote(ctx, r); err != nil {
		return err
	}
	l.mu.Lock()
	l.remotes = 1
	l.mu.Unlock()
	l.log.Info("seeded remote from YANA_GIT_REMOTE", "url", RedactURL(clean))
	return nil
}

func countEnabled(rs []index.GitRemote) int {
	n := 0
	for _, r := range rs {
		if r.Enabled {
			n++
		}
	}
	return n
}

// remoteEnv returns the configuration and environment that let git reach r
// without a prompt. For an SSH remote the opened private key is written to
// a 0600 file under .sync for the one subprocess and git is pointed at it
// through GIT_SSH_COMMAND, which names the file and never holds the key;
// cleanup removes the file. A path remote needs no key. GIT_SSH_COMMAND is
// always ours, whatever the server's environment says.
func (l *Layer) remoteEnv(r index.GitRemote) (config, env []string, cleanup func(), err error) {
	// Only network and file transports: a URL cannot pick ext:: or another
	// transport that runs a command. ParseRemoteURL refuses those already;
	// this holds even if a row was written another way.
	config = []string{"protocol.allow=never", "protocol.file.allow=always", "protocol.ssh.allow=always"}
	cleanup = func() {}
	if !IsSSH(r.URL) {
		return config, nil, cleanup, nil
	}
	if len(r.Secret) == 0 {
		return nil, nil, nil, errors.New("this remote has no key yet; make one in Settings › Backup")
	}
	pemText, err := l.open(r.Secret)
	if err != nil {
		return nil, nil, nil, err
	}
	sync := filepath.Join(l.root, ".sync")
	if err := fsutil.MkdirInherit(sync); err != nil {
		return nil, nil, nil, err
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	keyFile := filepath.Join(sync, keyFilePrefix+hex.EncodeToString(b))
	f, err := os.OpenFile(keyFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, nil, err
	}
	_, werr := f.WriteString(pemText)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(keyFile)
		return nil, nil, nil, werr
	}
	env = []string{"GIT_SSH_COMMAND=ssh -i " + shellQuote(keyFile) +
		" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new" +
		" -o UserKnownHostsFile=" + shellQuote(l.knownHosts())}
	return config, env, func() { _ = os.Remove(keyFile) }, nil
}

// keyFilePrefix names the per-process key files under .sync.
const keyFilePrefix = "ssh-key-"

// knownHosts is where this server remembers the hosts it has pushed to.
func (l *Layer) knownHosts() string {
	return filepath.Join(l.root, ".sync", "known_hosts")
}

// sweepKeyFiles removes key files a crash left behind.
func (l *Layer) sweepKeyFiles() {
	old, _ := filepath.Glob(filepath.Join(l.root, ".sync", keyFilePrefix+"*"))
	for _, f := range old {
		_ = os.Remove(f)
	}
}

// shellQuote quotes s for the shell git runs GIT_SSH_COMMAND through.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Push pushes HEAD to one remote now and records the outcome on its row.
// It ignores the schedule and the retry backoff.
func (l *Layer) Push(ctx context.Context, r index.GitRemote) error {
	if !l.available {
		return errors.New("git history is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	l.committing.Lock()
	defer l.committing.Unlock()
	err := l.push(ctx, r)
	now := l.opts.Now()
	if l.opts.DB != nil {
		if rerr := l.opts.DB.RecordGitPush(context.WithoutCancel(ctx), r.ID, now, err); rerr != nil {
			l.log.Warn("could not record the push", "remote", r.Name, "err", rerr)
		}
	}
	l.mu.Lock()
	if err != nil {
		l.retryAfter[r.ID] = now.Add(retryBackoff)
	} else {
		delete(l.retryAfter, r.ID)
		l.pushes++
		l.lastPush = now
	}
	l.mu.Unlock()
	if err != nil {
		l.fail(fmt.Errorf("push to %s: %w", r.Name, err))
		l.log.Error("push failed", "remote", r.Name, "url", RedactURL(r.URL), "err", err)
		return err
	}
	l.log.Info("pushed", "remote", r.Name, "url", RedactURL(r.URL))
	return nil
}

func (l *Layer) push(ctx context.Context, r index.GitRemote) error {
	config, env, cleanup, err := l.remoteEnv(r)
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = l.gitEnv(ctx, config, env, "push", "--", r.URL, "HEAD")
	return err
}

// Test checks that r is reachable with its credentials (git ls-remote)
// without pushing anything. It reports the refs the remote holds.
func (l *Layer) Test(ctx context.Context, r index.GitRemote) (refs int, err error) {
	if !l.available {
		return 0, errors.New("git history is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	config, env, cleanup, err := l.remoteEnv(r)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	out, err := l.gitEnv(ctx, config, env, "ls-remote", "--heads", "--", r.URL)
	if err != nil {
		return 0, err
	}
	for _, ln := range strings.Split(out, "\n") {
		if strings.TrimSpace(ln) != "" {
			refs++
		}
	}
	return refs, nil
}

// PushNow pushes to every enabled remote (or, without a DB, to the
// environment's remote) regardless of schedule. It returns the first
// failure after trying them all.
func (l *Layer) PushNow(ctx context.Context) error {
	if l.opts.DB == nil {
		if l.opts.Remote == "" {
			return nil
		}
		return l.Push(ctx, index.GitRemote{ID: "env", Name: "remote", URL: l.opts.Remote, Enabled: true})
	}
	remotes, err := l.opts.DB.ListGitRemotes(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, r := range remotes {
		if !r.Enabled {
			continue
		}
		if err := l.Push(ctx, r); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Reload notes that the remotes table changed; the next tick reads it
// anyway, this just keeps the status count current.
func (l *Layer) Reload(ctx context.Context) {
	if l.opts.DB == nil {
		return
	}
	remotes, err := l.opts.DB.ListGitRemotes(ctx)
	if err != nil {
		return
	}
	l.mu.Lock()
	l.noteRemotes(remotes)
	l.mu.Unlock()
}

// noteRemotes records what the remotes table says: how many are enabled
// and the newest push any of them has seen, so the stats report a push
// made before this process started. The caller holds l.mu.
func (l *Layer) noteRemotes(remotes []index.GitRemote) {
	l.remotes = countEnabled(remotes)
	for _, r := range remotes {
		if r.LastPush.After(l.lastPush) {
			l.lastPush = r.LastPush
		}
	}
}

// pushDue runs from the loop: it pushes every enabled remote whose
// schedule has come round and that has a commit it has not seen.
func (l *Layer) pushDue(now time.Time) {
	if !l.available || l.opts.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(l.ctx, time.Minute)
	remotes, err := l.opts.DB.ListGitRemotes(ctx)
	cancel()
	if err != nil {
		l.log.Warn("could not list remotes", "err", err)
		return
	}
	l.mu.Lock()
	l.noteRemotes(remotes)
	lastMade := l.lastMade
	l.mu.Unlock()
	for _, r := range remotes {
		if !r.Enabled || lastMade.IsZero() || !lastMade.After(r.LastPush) {
			continue
		}
		l.mu.Lock()
		retry := l.retryAfter[r.ID]
		l.mu.Unlock()
		if retry.After(now) {
			continue
		}
		if !due(r, now) {
			continue
		}
		if err := l.Push(l.ctx, r); err != nil {
			l.log.Error("scheduled push failed; will retry", "remote", r.Name, "err", err)
		}
	}
}

// due reports whether r's schedule calls for a push at now, given that
// there is something to push.
func due(r index.GitRemote, now time.Time) bool {
	switch r.Schedule {
	case ScheduleCommit:
		return true
	case ScheduleHourly:
		return r.LastPush.IsZero() || now.Sub(r.LastPush) >= time.Hour
	case ScheduleNightly:
		hour := r.PushHour
		if hour < 0 || hour > 23 {
			hour = 2
		}
		boundary := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
		if boundary.After(now) {
			boundary = boundary.AddDate(0, 0, -1)
		}
		return r.LastPush.Before(boundary)
	}
	return false
}
