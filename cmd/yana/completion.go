// Shell completion.
//
//	yana completion bash|zsh   print the script to source
//	yana __complete <words>    the candidates for the last word, one per
//	                           line, "value<TAB>description" for commands
//	                           and flags
//
// Note paths and space names come from a cache in the state dir, so TAB
// never waits on the network: a cache older than a minute is refreshed in
// the background, and with no cache the server gets 300ms.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"
)

func completionMain(args []string, w io.Writer) error {
	if len(args) == 1 && args[0] == "bash" {
		_, err := io.WriteString(w, bashCompletion)
		return err
	}
	if len(args) == 1 && args[0] == "zsh" {
		_, err := io.WriteString(w, zshCompletion)
		return err
	}
	return errors.New("usage: yana completion bash|zsh")
}

// compData is what completion knows of the server.
type compData struct {
	Server string   `json:"server"`
	Notes  []string `json:"notes"`
	Dirs   []string `json:"dirs"` // spaces included
}

func (d *compData) spaces() []string {
	var out []string
	for _, p := range d.Dirs {
		if !strings.Contains(p, "/") {
			out = append(out, p)
		}
	}
	return out
}

func completeMain(words []string, w io.Writer) {
	if len(words) == 1 && words[0] == "--refresh" {
		if s, err := loadSession(); err == nil {
			refreshCompletion(s)
		}
		return
	}
	for _, c := range candidates(words, loadCompletion) {
		fmt.Fprintln(w, c)
	}
}

// candidates completes the last of words (the command line after yana).
// data is asked only when the answer needs the server, and may answer
// nil: no session, or nothing in time.
func candidates(words []string, data func() *compData) []string {
	if len(words) == 0 {
		words = []string{""}
	}
	cur := unescapeWord(words[len(words)-1])
	prev := ""
	if len(words) >= 2 {
		prev = words[len(words)-2]
	}
	var out []string
	prefixed := func(list []string) {
		for _, v := range list {
			if strings.HasPrefix(v, cur) {
				out = append(out, v)
			}
		}
	}
	if prev == "--space" {
		if d := data(); d != nil {
			prefixed(d.spaces())
		}
		return out
	}
	if len(words) == 1 {
		for _, c := range commands {
			if strings.HasPrefix(c.name, cur) {
				out = append(out, c.name+"\t"+c.help)
			}
		}
		if d := data(); d != nil {
			out = append(out, pathCandidates(d, cur)...)
		}
		return out
	}
	first := words[0]
	if strings.HasPrefix(cur, "-") {
		_, isDay := dayArg(first, time.Now())
		if isDay || first == "new" {
			if strings.HasPrefix("--space", cur) {
				out = append(out, "--space\tthe space to use")
			}
		}
		return out
	}
	switch first {
	case "open":
		if len(words) == 2 {
			if d := data(); d != nil {
				out = pathCandidates(d, cur)
			}
		}
	case "completion":
		if len(words) == 2 {
			prefixed([]string{"bash", "zsh"})
		}
	}
	return out
}

// pathCandidates completes a note path one segment at a time, like
// files: the folders and notes directly under what cur has so far.
func pathCandidates(d *compData, cur string) []string {
	dir := cur[:strings.LastIndex(cur, "/")+1]
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !strings.HasPrefix(p, cur) {
			return
		}
		c := p
		if i := strings.IndexByte(p[len(dir):], '/'); i >= 0 {
			c = p[:len(dir)+i+1]
		}
		if !seen[c] && c != cur { // a folder already typed out is not offered again
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, p := range d.Dirs {
		add(p + "/")
	}
	for _, p := range d.Notes {
		add(p)
	}
	sort.Strings(out)
	return out
}

// unescapeWord undoes the shell's backslashes in a word still being typed.
func unescapeWord(w string) string {
	var b strings.Builder
	for i := 0; i < len(w); i++ {
		if w[i] == '\\' && i+1 < len(w) {
			i++
		}
		b.WriteByte(w[i])
	}
	return b.String()
}

const compMaxAge = time.Minute

func compCachePath() (string, error) { return statePath("completion.json") }

// loadCompletion is the cached tree for this session's server. A stale
// cache is answered as it is and refreshed in the background; with none,
// the server gets 300ms.
func loadCompletion() *compData {
	s, err := loadSession()
	if err != nil {
		return nil
	}
	p, err := compCachePath()
	if err != nil {
		return nil
	}
	if fi, err := os.Stat(p); err == nil {
		var d compData
		if raw, err := os.ReadFile(p); err == nil && json.Unmarshal(raw, &d) == nil && d.Server == s.Server {
			if time.Since(fi.ModTime()) > compMaxAge {
				// Marked fresh now, so the next TAB does not start another.
				now := time.Now()
				_ = os.Chtimes(p, now, now)
				refreshInBackground()
			}
			return &d
		}
	}
	ch := make(chan *compData, 1)
	go func() { ch <- refreshCompletion(s) }()
	select {
	case d := <-ch:
		return d
	case <-time.After(300 * time.Millisecond):
		refreshInBackground()
		return nil
	}
}

// refreshCompletion fetches the tree and keeps it for completion.
func refreshCompletion(s *session) *compData {
	all, err := fetchTree(s)
	if err != nil {
		return nil
	}
	d := &compData{Server: s.Server}
	for _, e := range all {
		if e.Dir {
			d.Dirs = append(d.Dirs, e.Path)
		} else {
			d.Notes = append(d.Notes, e.Path)
		}
	}
	if p, err := compCachePath(); err == nil {
		raw, _ := json.Marshal(d)
		tmp := p + ".tmp"
		if os.WriteFile(tmp, raw, 0o600) == nil {
			_ = os.Rename(tmp, p)
		}
	}
	return d
}

// refreshInBackground starts a detached yana __complete --refresh. Its
// output goes nowhere, so the shell reading this one's does not wait.
func refreshInBackground() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "__complete", "--refresh")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

const bashCompletion = `# yana completion for bash: source <(yana completion bash)
_yana() {
  local IFS=$'\n'
  COMPREPLY=($(yana __complete "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null | cut -f1))
  compopt -o filenames 2>/dev/null
  if [[ ${#COMPREPLY[@]} -eq 1 && ${COMPREPLY[0]} == */ ]]; then
    compopt -o nospace 2>/dev/null
  fi
}
complete -F _yana yana
`

const zshCompletion = `#compdef yana
# yana completion for zsh: source <(yana completion zsh) after compinit,
# or save as _yana in a directory on $fpath.
_yana() {
  local -a lines described dirs notes
  local line
  lines=("${(@f)$(yana __complete "${(@Q)words[2,CURRENT]}" 2>/dev/null)}")
  for line in $lines; do
    [[ -z $line ]] && continue
    if [[ $line == *$'\t'* ]]; then
      described+=("${${line%%$'\t'*}//:/\\:}:${line#*$'\t'}")
    elif [[ $line == */ ]]; then
      dirs+=("$line")
    else
      notes+=("$line")
    fi
  done
  (( $#described )) && _describe -t commands 'yana command' described
  (( $#dirs )) && compadd -S '' -- $dirs
  (( $#notes )) && compadd -- $notes
  return 0
}
if [[ $zsh_eval_context[-1] == loadautofunc ]]; then
  _yana "$@"
else
  compdef _yana yana
fi
`
