// Where the command line keeps its files, and the TUI's keymap.
//
//	yana config   print the config file's path and the defaults as YAML
//
// The config and the session live in $XDG_CONFIG_HOME/yana (else
// ~/.config/yana), recently opened notes and command history in
// $XDG_STATE_HOME/yana (else ~/.local/state/yana), on every OS: people
// who live in a terminal look there, macOS included.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
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

// --- config and keymap -----------------------------------------------------------

type cliConfig struct {
	Leader string              `yaml:"leader"`
	Editor string              `yaml:"editor"` // empty: $VISUAL, $EDITOR, nvim
	Keys   map[string][]string `yaml:"keys"`
}

// actions are the bindable actions, in the order help lists them, with
// their default keys.
var actions = []struct {
	name, help string
	keys       []string
}{
	{"down", "move down", []string{"j", "<Down>"}},
	{"up", "move up", []string{"k", "<Up>"}},
	{"top", "first row", []string{"gg", "<Home>"}},
	{"bottom", "last row", []string{"G", "<End>"}},
	{"half_down", "half a page down", []string{"<C-d>", "<PageDown>"}},
	{"half_up", "half a page up", []string{"<C-u>", "<PageUp>"}},
	{"open", "open the folder or note", []string{"<CR>", "l", "<Right>"}},
	{"parent", "up a folder", []string{"-", "h", "<BS>", "<Left>"}},
	{"root", "the top", []string{"_"}},
	{"search", "search the listing", []string{"/"}},
	{"search_next", "next match", []string{"n"}},
	{"search_prev", "previous match", []string{"N"}},
	{"new", "new note (name/ makes a folder)", []string{"o", "a"}},
	{"rename", "rename or move", []string{"r", "cw"}},
	{"delete", "delete to the trash", []string{"dd"}},
	{"preview", "toggle the preview pane", []string{"<C-p>"}},
	{"help", "this help", []string{"g?"}},
	{"quit", "quit", []string{"q"}},
	{"command", "command line", []string{":"}},
	{"find_notes", "find notes by name", []string{"<leader>sf"}},
	{"grep", "full-text search", []string{"<leader>sg"}},
	{"recent", "recently opened notes", []string{"<leader><leader>"}},
}

func defaultConfig() cliConfig {
	c := cliConfig{Leader: " ", Keys: map[string][]string{}}
	for _, a := range actions {
		c.Keys[a.name] = a.keys
	}
	return c
}

func configPath() (string, error) {
	d, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.yaml"), nil
}

// loadConfig reads the config file over the defaults: a key the file
// leaves out keeps its default. A file that does not parse, or names an
// action or key that does not exist, gives the defaults and the error.
func loadConfig() (cliConfig, error) {
	def := defaultConfig()
	p, err := configPath()
	if err != nil {
		return def, nil
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	c, err := mergeConfig(def, raw)
	if err != nil {
		// The reason first: a long path would push it off the status line.
		return def, fmt.Errorf("config: %v; using the defaults (%s)", err, p)
	}
	return c, nil
}

func mergeConfig(def cliConfig, raw []byte) (cliConfig, error) {
	var file cliConfig
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return def, err
	}
	c := cliConfig{Leader: def.Leader, Editor: def.Editor, Keys: map[string][]string{}}
	for k, v := range def.Keys {
		c.Keys[k] = v
	}
	if file.Leader != "" {
		c.Leader = file.Leader
	}
	if file.Editor != "" {
		c.Editor = file.Editor
	}
	for name, keys := range file.Keys {
		if _, ok := def.Keys[name]; !ok {
			return def, fmt.Errorf("no action named %q", name)
		}
		c.Keys[name] = keys
	}
	if _, err := c.keymap(); err != nil {
		return def, err
	}
	return c, nil
}

// keymap is each bound key sequence, joined by seqSep, to its action.
func (c cliConfig) keymap() (map[string]string, error) {
	leader, err := parseKeys(c.Leader, nil)
	if err != nil || len(leader) != 1 {
		return nil, fmt.Errorf("leader %q must be one key", c.Leader)
	}
	km := map[string]string{}
	for name, list := range c.Keys {
		for _, k := range list {
			seq, err := parseKeys(k, leader)
			if err != nil {
				return nil, fmt.Errorf("%s: %v", name, err)
			}
			if len(seq) == 0 {
				continue
			}
			km[strings.Join(seq, seqSep)] = name
		}
	}
	return km, nil
}

// seqSep joins the keys of a sequence; no key's name holds it.
const seqSep = "\x1f"

// special maps vim's <Name> notation to bubbletea's key names.
var special = map[string]string{
	"cr": "enter", "enter": "enter", "return": "enter", "esc": "esc",
	"space": " ", "bs": "backspace", "tab": "tab", "s-tab": "shift+tab",
	"up": "up", "down": "down", "left": "left", "right": "right",
	"home": "home", "end": "end", "pageup": "pgup", "pagedown": "pgdown",
	"del": "delete", "lt": "<",
}

// parseKeys turns vim notation ("gg", "<C-p>", "<leader>sf") into the
// key names bubbletea reports, one per key. leader is what <leader>
// stands for.
func parseKeys(s string, leader []string) ([]string, error) {
	var out []string
	for len(s) > 0 {
		if s[0] == '<' {
			if end := strings.IndexByte(s, '>'); end > 1 {
				name := strings.ToLower(s[1:end])
				s = s[end+1:]
				switch {
				case name == "leader":
					if leader == nil {
						return nil, errors.New("<leader> inside the leader")
					}
					out = append(out, leader...)
				case special[name] != "":
					out = append(out, special[name])
				case len(name) == 3 && name[1] == '-' && (name[0] == 'c' || name[0] == 'm' || name[0] == 'a'):
					mod := "ctrl+"
					if name[0] != 'c' {
						mod = "alt+"
					}
					out = append(out, mod+name[2:])
				default:
					return nil, fmt.Errorf("unknown key <%s>", name)
				}
				continue
			}
		}
		r := []rune(s)[0]
		out = append(out, string(r))
		s = s[len(string(r)):]
	}
	return out, nil
}

// keyNames is special the other way round, for showing keys.
var keyNames = map[string]string{
	"enter": "CR", "esc": "Esc", " ": "Space", "backspace": "BS", "tab": "Tab",
	"shift+tab": "S-Tab", "up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"home": "Home", "end": "End", "pgup": "PageUp", "pgdown": "PageDown", "delete": "Del", "<": "lt",
}

// showKeys is a sequence back in vim notation, for :map and help.
func showKeys(seq string, leader string) string {
	var b strings.Builder
	for _, k := range strings.Split(seq, seqSep) {
		switch {
		case k == leader:
			b.WriteString("<leader>")
		case keyNames[k] != "":
			b.WriteString("<" + keyNames[k] + ">")
		case strings.HasPrefix(k, "ctrl+"):
			b.WriteString("<C-" + k[5:] + ">")
		case strings.HasPrefix(k, "alt+"):
			b.WriteString("<M-" + k[4:] + ">")
		default:
			b.WriteString(k)
		}
	}
	return b.String()
}

// bindings lists each action with its keys in vim notation.
func bindings(km map[string]string, leader string) []string {
	byAction := map[string][]string{}
	for seq, a := range km {
		byAction[a] = append(byAction[a], showKeys(seq, leader))
	}
	var out []string
	for _, a := range actions {
		ks := byAction[a.name]
		sort.Strings(ks)
		out = append(out, fmt.Sprintf(" %-12s %-28s %s", a.name, strings.Join(ks, " "), a.help))
	}
	return out
}

// matchSeq feeds one key to the pending sequence. It returns the action
// to run ("" while a sequence is still being typed, or when the keys are
// bound to nothing) and what stays pending. A key that breaks a sequence
// is tried again on its own.
func matchSeq(km map[string]string, pending []string, key string) (action string, rest []string) {
	buf := append(append([]string{}, pending...), key)
	joined := strings.Join(buf, seqSep)
	if a, ok := km[joined]; ok {
		return a, nil
	}
	for seq := range km {
		if strings.HasPrefix(seq, joined+seqSep) {
			return "", buf
		}
	}
	if len(pending) > 0 {
		return matchSeq(km, nil, key)
	}
	return "", nil
}

func configMain() error {
	p, err := configPath()
	if err != nil {
		return err
	}
	raw, _ := yaml.Marshal(defaultConfig())
	fmt.Printf("# %s\n# The defaults; copy what you change. editor empty means $VISUAL, $EDITOR, nvim.\n%s", p, raw)
	return nil
}
