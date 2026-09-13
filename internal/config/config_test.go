package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8080" || cfg.MaxNoteSize != 10<<20 || cfg.LogLevel != "info" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.WritebackIdle != 2*time.Second || cfg.WatchDebounce != 200*time.Millisecond ||
		cfg.CompactAfter != 500 || cfg.CRDTRetention != 30*24*time.Hour {
		t.Fatalf("unexpected sync defaults: %+v", cfg)
	}
	if !filepath.IsAbs(cfg.NotesRoot) {
		t.Fatalf("notes root should be absolute: %q", cfg.NotesRoot)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "yana.yml")
	if err := os.WriteFile(file, []byte("listen: \":9000\"\nlog_level: debug\nmax_note_size: 42\nscan_settle_time: 7s\ncompact_after: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(env(map[string]string{
		"YANA_CONFIG":         file,
		"YANA_LISTEN":         ":1234",
		"YANA_NOTES_ROOT":     dir,
		"YANA_RIPGREP":        "false",
		"YANA_WRITEBACK_IDLE": "500ms",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":1234" {
		t.Errorf("env should win over file: %q", cfg.Listen)
	}
	if cfg.LogLevel != "debug" || cfg.MaxNoteSize != 42 || cfg.ScanSettleTime != 7*time.Second {
		t.Errorf("file values not applied: %+v", cfg)
	}
	if cfg.Ripgrep {
		t.Error("YANA_RIPGREP=false not applied")
	}
	if cfg.WritebackIdle != 500*time.Millisecond || cfg.CompactAfter != 9 {
		t.Errorf("sync settings not applied: %+v", cfg)
	}
	if cfg.NotesRoot != dir {
		t.Errorf("notes root: %q", cfg.NotesRoot)
	}
	if cfg.IndexPath() != filepath.Join(dir, ".sync", "index.db") {
		t.Errorf("index path: %q", cfg.IndexPath())
	}
}

func TestBadValues(t *testing.T) {
	if _, err := Load(env(map[string]string{"YANA_LOG_LEVEL": "loud"})); err == nil {
		t.Error("bad log level accepted")
	}
	if _, err := Load(env(map[string]string{"YANA_MAX_NOTE_SIZE": "ten"})); err == nil {
		t.Error("bad integer accepted")
	}
	if _, err := Load(env(map[string]string{"YANA_CONFIG": "/nonexistent/yana.yml"})); err == nil {
		t.Error("missing config file accepted")
	}
}
