// Where the command line keeps its files.
//
//	yana config   print where init.lua goes, and a commented default
//
// The session and init.lua live in $XDG_CONFIG_HOME/yana (else
// ~/.config/yana), recently opened notes and the buffers left open in
// $XDG_STATE_HOME/yana (else ~/.local/state/yana), the neovim plugin in
// $XDG_DATA_HOME/yana/nvim (else ~/.local/share/yana/nvim), on every OS:
// people who live in a terminal look there, macOS included.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func xdgDir(env, fallback string) (string, error) {
	if d := os.Getenv(env); filepath.IsAbs(d) {
		return filepath.Join(d, "yana"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, fallback, "yana"), nil
}

func configDir() (string, error) { return xdgDir("XDG_CONFIG_HOME", ".config") }

func stateDir() (string, error) { return xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state")) }

func dataDir() (string, error) { return xdgDir("XDG_DATA_HOME", filepath.Join(".local", "share")) }

// statePath names a file in the state dir, making the dir when needed.
func statePath(name string) (string, error) {
	d, err := stateDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}

// migrateSession moves a session kept by an older yana in the OS config
// dir (~/Library/Application Support on macOS) to p.
func migrateSession(p string) {
	old, err := os.UserConfigDir()
	if err != nil {
		return
	}
	old = filepath.Join(old, "yana", "session.json")
	if old == p {
		return
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		return
	}
	raw, err := os.ReadFile(old)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0o700) == nil && os.WriteFile(p, raw, 0o600) == nil {
		os.Remove(old)
	}
}

// defaultInit is what `yana config` prints: every setup option at its
// default, commented out.
const defaultInit = `-- Sourced after the yana plugin when yana launches neovim.
-- Your own neovim config loads first, as always; this file is for yana.
-- require('yana').setup {
--   -- Global maps in a yana session; false leaves a key to your config.
--   keys = {
--     find = '<leader>sf',     -- notes by path or title, today, #tag, space/
--     grep = '<leader>sg',     -- full-text search
--     recent = '<leader><leader>',
--   },
--   picker = 'auto',           -- 'telescope', 'select' (vim.ui.select) or 'auto'
--   live = true,               -- refresh unmodified notes changed elsewhere
-- }
`

func configMain() error {
	d, err := configDir()
	if err != nil {
		return err
	}
	fmt.Printf("-- %s\n%s", filepath.Join(d, "init.lua"), defaultInit)
	return nil
}
