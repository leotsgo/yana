// Package config loads server configuration from the environment (prefix
// YANA_) with an optional YAML file underneath it. Environment wins over the
// file; the file wins over defaults.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the fully resolved server configuration.
type Config struct {
	// NotesRoot is the directory that holds spaces. It is the source of
	// truth; everything under NotesRoot/.sync is derived from it.
	NotesRoot string `yaml:"notes_root"`
	// Listen is the address the HTTP server binds to.
	Listen string `yaml:"listen"`
	// ContentListen is the address the content origin (where HTML notes
	// render) binds to. "off" disables the second listener and the view
	// endpoint.
	ContentListen string `yaml:"content_listen"`
	// ContentOrigin is the public base URL of the content origin when a
	// proxy maps a subdomain onto it (for example
	// https://content.notes.example.com). Empty derives the URL from each
	// request's host plus the content listener's port.
	ContentOrigin string `yaml:"content_origin"`
	// LogLevel is one of debug, info, warn, error.
	LogLevel string `yaml:"log_level"`

	// MaxNoteSize is the largest note (bytes) the scanner will index or the
	// API will accept.
	MaxNoteSize int64 `yaml:"max_note_size"`
	// MaxAssetSize is the largest file under _assets/ that is indexed.
	MaxAssetSize int64 `yaml:"max_asset_size"`
	// MaxNotesPerSpace caps how many notes one space may contain.
	MaxNotesPerSpace int `yaml:"max_notes_per_space"`

	// ScanSettleTime is how old a file's mtime must be before the scanner
	// assigns it an id. Files younger than this are still being written.
	ScanSettleTime time.Duration `yaml:"scan_settle_time"`

	// WritebackIdle is how long a note must go without an edit before its
	// document is written to its file.
	WritebackIdle time.Duration `yaml:"writeback_idle"`
	// WatchDebounce collapses bursts of filesystem events on one path.
	WatchDebounce time.Duration `yaml:"watch_debounce"`
	// CompactAfter is the per-note CRDT log length that triggers a snapshot.
	CompactAfter int `yaml:"compact_after"`
	// CRDTRetention is how long the document of a deleted note is kept
	// under .sync/crdt/retired.
	CRDTRetention time.Duration `yaml:"crdt_retention"`

	// Ripgrep enables the regex search passthrough when an `rg` binary is on
	// PATH. Off means the endpoint reports that regex search is unavailable.
	Ripgrep bool `yaml:"ripgrep"`
	// RipgrepTimeout bounds one regex search.
	RipgrepTimeout time.Duration `yaml:"ripgrep_timeout"`

	// Realtime relay bounds (GET /ws).
	WSMaxConnections   int   `yaml:"ws_max_connections"`
	WSMaxRoomsPerConn  int   `yaml:"ws_max_rooms_per_conn"`
	WSMaxSpacesPerConn int   `yaml:"ws_max_spaces_per_conn"`
	WSMaxMessageBytes  int64 `yaml:"ws_max_message_bytes"`
	// WSUserRate and WSAgentRate bound update and awareness messages per
	// author per minute. A client that batches keystrokes on a 50ms timer
	// peaks at 20 messages a second, so the user default is 1200/min.
	WSUserRate     int           `yaml:"ws_user_rate"`
	WSAgentRate    int           `yaml:"ws_agent_rate"`
	WSPingInterval time.Duration `yaml:"ws_ping_interval"`

	// Git turns the history layer on (true). The layer git-inits the
	// notes root on first run and commits after the tree has been quiet.
	Git bool `yaml:"git"`
	// GitQuiet is how long the tree must go without a change before the
	// open window commits.
	GitQuiet time.Duration `yaml:"git_quiet"`
	// GitInterval bounds how long a continuously edited tree can go
	// uncommitted.
	GitInterval time.Duration `yaml:"git_interval"`
	// GitRemote is an optional URL pushed nightly. Empty disables push.
	GitRemote string `yaml:"git_remote"`
	// GitPushHour is the local hour of the nightly push.
	GitPushHour int `yaml:"git_push_hour"`
	// GitUserName and GitUserEmail identify human edits in git. Agent
	// edits commit under their own label.
	GitUserName  string `yaml:"git_user_name"`
	GitUserEmail string `yaml:"git_user_email"`

	// AccessTTL is how long an access token lives before the client
	// must refresh it.
	AccessTTL time.Duration `yaml:"access_ttl"`
	// RefreshTTL is how long a session may go unused before it expires.
	RefreshTTL time.Duration `yaml:"refresh_ttl"`

	// AgentRate bounds MCP writes per agent label per minute, so a
	// runaway loop cannot fill the tree.
	AgentRate int `yaml:"agent_rate"`

	// DailyPattern is the path of the daily note inside a space. The
	// tokens {YYYY}, {MM}, {DD} and {date} (YYYY-MM-DD) expand from the
	// client's local date.
	DailyPattern string `yaml:"daily_pattern"`
	// DailyTemplate is the path, inside the same space, of a note whose
	// body seeds a new daily note. The same tokens expand in the body.
	// A missing template falls back to a heading with the date.
	DailyTemplate string `yaml:"daily_template"`
}

// Defaults returns the configuration used when nothing is set. The notes
// root defaults to ~/.yana on bare metal; the container image overrides it
// to /notes through the environment.
func Defaults() Config {
	root := "/notes"
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		root = filepath.Join(home, ".yana")
	}
	return Config{
		NotesRoot:          root,
		Listen:             ":8080",
		ContentListen:      ":8081",
		ContentOrigin:      "",
		LogLevel:           "info",
		MaxNoteSize:        10 << 20,
		MaxAssetSize:       50 << 20,
		MaxNotesPerSpace:   100_000,
		ScanSettleTime:     2 * time.Second,
		WritebackIdle:      2 * time.Second,
		WatchDebounce:      200 * time.Millisecond,
		CompactAfter:       500,
		CRDTRetention:      30 * 24 * time.Hour,
		Ripgrep:            true,
		RipgrepTimeout:     5 * time.Second,
		WSMaxConnections:   256,
		WSMaxRoomsPerConn:  16,
		WSMaxSpacesPerConn: 32,
		WSMaxMessageBytes:  1 << 20,
		WSUserRate:         1200,
		WSAgentRate:        300,
		WSPingInterval:     30 * time.Second,
		Git:                true,
		GitQuiet:           5 * time.Minute,
		GitInterval:        time.Hour,
		GitRemote:          "",
		GitPushHour:        2,
		GitUserName:        "yana user",
		GitUserEmail:       "user@yana.local",
		AccessTTL:          15 * time.Minute,
		RefreshTTL:         30 * 24 * time.Hour,
		AgentRate:          30,
		DailyPattern:       "journal/{YYYY}/{MM}/{YYYY}-{MM}-{DD}.md",
		DailyTemplate:      "templates/daily.md",
	}
}

// Load resolves the configuration from defaults, then the YAML file named
// by YANA_CONFIG (if set), then YANA_* environment variables.
func Load(getenv func(string) string) (Config, error) {
	cfg := Defaults()
	if path := getenv("YANA_CONFIG"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read config file: %w", err)
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("parse config file %s: %w", path, err)
		}
	}
	if err := applyEnv(&cfg, getenv); err != nil {
		return cfg, err
	}
	if cfg.NotesRoot == "" {
		return cfg, fmt.Errorf("notes root is empty")
	}
	abs, err := filepath.Abs(cfg.NotesRoot)
	if err != nil {
		return cfg, fmt.Errorf("resolve notes root: %w", err)
	}
	cfg.NotesRoot = abs
	if _, err := ParseLevel(cfg.LogLevel); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config, getenv func(string) string) error {
	str := func(key string, dst *string) {
		if v := getenv("YANA_" + key); v != "" {
			*dst = v
		}
	}
	i64 := func(key string, dst *int64) error {
		v := getenv("YANA_" + key)
		if v == "" {
			return nil
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("YANA_%s: %w", key, err)
		}
		*dst = n
		return nil
	}
	dur := func(key string, dst *time.Duration) error {
		v := getenv("YANA_" + key)
		if v == "" {
			return nil
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("YANA_%s: %w", key, err)
		}
		*dst = d
		return nil
	}
	boolean := func(key string, dst *bool) error {
		v := getenv("YANA_" + key)
		if v == "" {
			return nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("YANA_%s: %w", key, err)
		}
		*dst = b
		return nil
	}

	str("NOTES_ROOT", &cfg.NotesRoot)
	str("LISTEN", &cfg.Listen)
	str("CONTENT_LISTEN", &cfg.ContentListen)
	str("CONTENT_ORIGIN", &cfg.ContentOrigin)
	str("LOG_LEVEL", &cfg.LogLevel)
	if err := i64("MAX_NOTE_SIZE", &cfg.MaxNoteSize); err != nil {
		return err
	}
	if err := i64("MAX_ASSET_SIZE", &cfg.MaxAssetSize); err != nil {
		return err
	}
	var perSpace int64 = int64(cfg.MaxNotesPerSpace)
	if err := i64("MAX_NOTES_PER_SPACE", &perSpace); err != nil {
		return err
	}
	cfg.MaxNotesPerSpace = int(perSpace)
	if err := dur("SCAN_SETTLE_TIME", &cfg.ScanSettleTime); err != nil {
		return err
	}
	if err := dur("WRITEBACK_IDLE", &cfg.WritebackIdle); err != nil {
		return err
	}
	if err := dur("WATCH_DEBOUNCE", &cfg.WatchDebounce); err != nil {
		return err
	}
	var compact int64 = int64(cfg.CompactAfter)
	if err := i64("COMPACT_AFTER", &compact); err != nil {
		return err
	}
	cfg.CompactAfter = int(compact)
	if err := dur("CRDT_RETENTION", &cfg.CRDTRetention); err != nil {
		return err
	}
	if err := boolean("RIPGREP", &cfg.Ripgrep); err != nil {
		return err
	}
	if err := dur("RIPGREP_TIMEOUT", &cfg.RipgrepTimeout); err != nil {
		return err
	}
	i := func(key string, dst *int) error {
		v := getenv("YANA_" + key)
		if v == "" {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("YANA_%s: %w", key, err)
		}
		*dst = n
		return nil
	}
	if err := i("WS_MAX_CONNECTIONS", &cfg.WSMaxConnections); err != nil {
		return err
	}
	if err := i("WS_MAX_ROOMS_PER_CONN", &cfg.WSMaxRoomsPerConn); err != nil {
		return err
	}
	if err := i("WS_MAX_SPACES_PER_CONN", &cfg.WSMaxSpacesPerConn); err != nil {
		return err
	}
	if err := i64("WS_MAX_MESSAGE_BYTES", &cfg.WSMaxMessageBytes); err != nil {
		return err
	}
	if err := i("WS_USER_RATE", &cfg.WSUserRate); err != nil {
		return err
	}
	if err := i("WS_AGENT_RATE", &cfg.WSAgentRate); err != nil {
		return err
	}
	if err := dur("WS_PING_INTERVAL", &cfg.WSPingInterval); err != nil {
		return err
	}
	if err := boolean("GIT", &cfg.Git); err != nil {
		return err
	}
	if err := dur("GIT_QUIET", &cfg.GitQuiet); err != nil {
		return err
	}
	if err := dur("GIT_INTERVAL", &cfg.GitInterval); err != nil {
		return err
	}
	str("GIT_REMOTE", &cfg.GitRemote)
	if err := i("GIT_PUSH_HOUR", &cfg.GitPushHour); err != nil {
		return err
	}
	str("GIT_USER_NAME", &cfg.GitUserName)
	str("GIT_USER_EMAIL", &cfg.GitUserEmail)
	if err := dur("ACCESS_TTL", &cfg.AccessTTL); err != nil {
		return err
	}
	if err := dur("REFRESH_TTL", &cfg.RefreshTTL); err != nil {
		return err
	}
	if err := i("AGENT_RATE", &cfg.AgentRate); err != nil {
		return err
	}
	str("DAILY_PATTERN", &cfg.DailyPattern)
	str("DAILY_TEMPLATE", &cfg.DailyTemplate)
	return nil
}

// ParseLevel maps a config string to a slog level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("unknown log level %q (use debug, info, warn, or error)", s)
}

// SyncDir returns the directory for derived state under the notes root.
func (c Config) SyncDir() string { return filepath.Join(c.NotesRoot, ".sync") }

// IndexPath returns the SQLite index location.
func (c Config) IndexPath() string { return filepath.Join(c.SyncDir(), "index.db") }

// AuthSecretPath returns the location of the token-signing secret.
func (c Config) AuthSecretPath() string { return filepath.Join(c.SyncDir(), "auth_secret") }

// ContentSecretPath returns the location of the key the content origin
// signs view tokens and derives public-link tokens with.
func (c Config) ContentSecretPath() string { return filepath.Join(c.SyncDir(), "content_secret") }

// GitSecretPath returns the location of the key remote credentials are
// sealed with.
func (c Config) GitSecretPath() string { return filepath.Join(c.SyncDir(), "git_secret") }
