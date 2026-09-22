// Backup remotes: the repositories the history layer pushes to. The owner
// configures them in settings; rows live in the index (real state, like
// agent tokens) and the layer reads them on each tick, so a change takes
// effect without a restart.
//
// Credentials never travel in the URL, on a command line, or in a log
// line. A token is sealed with AES-GCM under a key in .sync/git_secret and
// handed to git per push through an inline credential helper that reads
// two environment variables set for that one subprocess.
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

	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/index"
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

// ErrBadRemoteURL is returned for URLs the layer will not hand to git.
var ErrBadRemoteURL = errors.New("remote URL must be https://, ssh://, git@host:path, or an absolute path")

// ErrNoSealKey is returned when a credential cannot be stored because the
// key file is unavailable.
var ErrNoSealKey = errors.New("credential storage is unavailable on this server")

var scpLike = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9._-]+:[^\s]+$`)

// ParseRemoteURL validates a remote URL and separates any credential the
// user pasted into it, so https://me:token@host/x is stored as the URL
// https://host/x with the username and token beside it.
func ParseRemoteURL(raw string) (clean, username, password string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "-") || strings.ContainsAny(raw, " \t\r\n\x00") || len(raw) > 2048 {
		return "", "", "", ErrBadRemoteURL
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", "", ErrBadRemoteURL
		}
		switch u.Scheme {
		case "https", "http", "ssh", "git":
		default:
			return "", "", "", ErrBadRemoteURL
		}
		if u.Host == "" {
			return "", "", "", ErrBadRemoteURL
		}
		if u.User != nil {
			username = u.User.Username()
			password, _ = u.User.Password()
			if u.Scheme == "ssh" || u.Scheme == "git" {
				// ssh://git@host/path: the user is part of the address.
				u.User = url.User(username)
				username = ""
				if password != "" {
					return "", "", "", ErrBadRemoteURL
				}
			} else {
				u.User = nil
			}
		}
		return u.String(), username, password, nil
	}
	if scpLike.MatchString(raw) || filepath.IsAbs(raw) {
		return raw, "", "", nil
	}
	return "", "", "", ErrBadRemoteURL
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
	clean, username, password, err := ParseRemoteURL(l.opts.Remote)
	if err != nil {
		return err
	}
	r := index.GitRemote{
		ID: NewRemoteID(), Name: "remote", URL: clean, Schedule: ScheduleNightly, PushHour: l.opts.PushHour,
		Username: username, Enabled: true, CreatedAt: l.opts.Now(),
	}
	if password != "" {
		if r.Secret, err = l.Seal(password); err != nil {
			return err
		}
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

// credentialEnv returns the configuration and environment that let git
// authenticate to r without a prompt. The helper is an inline shell
// function reading two variables, so the secret is in the environment of
// this one subprocess and nowhere else; an empty first helper entry
// clears any helper from the host's git configuration.
func (l *Layer) credentialEnv(r index.GitRemote) (config, env []string, err error) {
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new")
	}
	// Only network and file transports: a URL cannot pick ext:: or another
	// transport that runs a command. ParseRemoteURL refuses those already;
	// this holds even if a row was written another way.
	config = append(config, "protocol.allow=never",
		"protocol.file.allow=always", "protocol.https.allow=always", "protocol.http.allow=always",
		"protocol.ssh.allow=always", "protocol.git.allow=always")
	if !r.HasSecret {
		return config, env, nil
	}
	token, err := l.open(r.Secret)
	if err != nil {
		return nil, nil, err
	}
	username := r.Username
	if username == "" {
		// GitHub, Gitea, and GitLab accept a token with any username.
		username = "yana"
	}
	config = append(config,
		"credential.helper=",
		`credential.helper=!f() { printf 'username=%s\npassword=%s\n' "$YANA_GIT_USERNAME" "$YANA_GIT_PASSWORD"; }; f`,
	)
	env = append(env, "YANA_GIT_USERNAME="+username, "YANA_GIT_PASSWORD="+token)
	return config, env, nil
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
	config, env, err := l.credentialEnv(r)
	if err != nil {
		return err
	}
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
	config, env, err := l.credentialEnv(r)
	if err != nil {
		return 0, err
	}
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
