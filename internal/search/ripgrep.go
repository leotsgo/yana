// Package search holds the regex search passthrough. Full-text search lives
// in the index package; this file shells out to ripgrep when it is
// installed, because a regex over a tree of markdown files is a job rg
// already does better than anything worth writing here.
package search

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrUnavailable is returned when ripgrep is disabled or not installed.
var ErrUnavailable = errors.New("regex search is unavailable: ripgrep (rg) is not installed on the server")

// ErrBadPattern is returned when rg rejects the regex.
var ErrBadPattern = errors.New("regex could not be parsed")

// RegexMatch is one matching line.
type RegexMatch struct {
	Path string `json:"path"` // relative to the notes root, forward slashes
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Ripgrep runs regex searches over a root directory.
type Ripgrep struct {
	bin     string
	root    string
	timeout time.Duration
}

// NewRipgrep locates rg on PATH. enabled=false yields a searcher that
// always reports ErrUnavailable, so callers need not special-case config.
func NewRipgrep(root string, enabled bool, timeout time.Duration) *Ripgrep {
	r := &Ripgrep{root: root, timeout: timeout}
	if !enabled {
		return r
	}
	if p, err := exec.LookPath("rg"); err == nil {
		r.bin = p
	}
	return r
}

// Available reports whether regex search can run.
func (r *Ripgrep) Available() bool { return r.bin != "" }

// Search runs pattern over the root (or one space beneath it) and returns
// up to limit matching lines. Dot-prefixed files and directories are never
// searched, matching the scanner's view of the tree.
func (r *Ripgrep) Search(ctx context.Context, pattern, space string, limit int) ([]RegexMatch, error) {
	if !r.Available() {
		return nil, ErrUnavailable
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	dir := r.root
	if space != "" {
		dir = filepath.Join(r.root, filepath.FromSlash(space))
	}
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	args := []string{
		"--json", "--no-messages", "--max-columns", "400",
		"--max-count", "50", // per file
		"--glob", "*.md", "--glob", "*.markdown", "--glob", "*.html", "--glob", "*.htm",
		"--glob", "!.*",
		"-e", pattern, "--", ".",
	}
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var matches []RegexMatch
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var ev rgEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil || ev.Type != "match" {
			continue
		}
		rel := filepath.ToSlash(filepath.Clean(ev.Data.Path.Text))
		if space != "" {
			rel = space + "/" + rel
		}
		matches = append(matches, RegexMatch{
			Path: rel,
			Line: ev.Data.LineNumber,
			Text: strings.TrimRight(ev.Data.Lines.Text, "\r\n"),
		})
		if len(matches) >= limit {
			_ = cmd.Process.Kill()
			break
		}
	}
	waitErr := cmd.Wait()
	if len(matches) >= limit {
		return matches, nil
	}
	if ctx.Err() != nil {
		return matches, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) {
		switch exit.ExitCode() {
		case 1: // no matches
			return nil, nil
		case 2:
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = "rg exited with status 2"
			}
			return nil, errors.Join(ErrBadPattern, errors.New(msg))
		}
		return nil, waitErr
	}
	return matches, waitErr
}

type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}
