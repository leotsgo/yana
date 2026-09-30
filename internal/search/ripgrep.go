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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

	versionOnce sync.Once
	version     string
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

// Version reports the installed rg's version ("14.1.0"), read once, or
// "" when rg is not available or will not say.
func (r *Ripgrep) Version() string {
	if r.bin == "" {
		return ""
	}
	r.versionOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, r.bin, "--version").Output()
		if err != nil {
			return
		}
		// The first line is "ripgrep 14.1.0" followed by build details.
		first, _, _ := strings.Cut(string(out), "\n")
		r.version = strings.TrimSpace(strings.TrimPrefix(first, "ripgrep"))
	})
	return r.version
}

// Search runs pattern over the root (or one space beneath it) and returns
// up to limit matching lines. Dot-prefixed files and directories are never
// searched, matching the scanner's view of the tree.
func (r *Ripgrep) Search(ctx context.Context, pattern, space string, limit int) ([]RegexMatch, error) {
	return r.SearchSpaces(ctx, pattern, space, nil, limit)
}

// SearchSpaces is Search with a membership filter: allowed nil searches
// the whole root (or the one requested space), otherwise only the
// listed spaces. Results never leave the allowed set.
func (r *Ripgrep) SearchSpaces(ctx context.Context, pattern, space string, allowed []string, limit int) ([]RegexMatch, error) {
	return r.SearchSpacesPaths(ctx, pattern, space, nil, allowed, limit)
}

// SearchSpacesPaths is SearchSpaces with explicit path targets (folders
// from path: terms, root-relative). When paths are set they are what rg
// walks — already membership-checked by the caller; allowed still bounds
// the space and paths targets when they are used instead.
func (r *Ripgrep) SearchSpacesPaths(ctx context.Context, pattern, space string, paths []string, allowed []string, limit int) ([]RegexMatch, error) {
	if !r.Available() {
		return nil, ErrUnavailable
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// The search targets: "." when unrestricted, otherwise the allowed
	// space directories. An empty allowed list matches nothing.
	targets := []string{"."}
	restricted := false
	if len(paths) > 0 {
		targets = make([]string, 0, len(paths))
		for _, p := range paths {
			t := filepath.FromSlash(p)
			if t == "" || t == "." || strings.HasPrefix(t, "..") || strings.Contains(t, "/../") ||
				strings.Contains(t, "\\") || filepath.IsAbs(t) {
				return nil, nil
			}
			// A folder that is not there answers with no matches, the
			// way an FTS path: term does.
			if _, err := os.Stat(filepath.Join(r.root, t)); err != nil {
				continue
			}
			targets = append(targets, t)
		}
		if len(targets) == 0 {
			return nil, nil
		}
		restricted = true
	} else if space != "" {
		targets = []string{filepath.FromSlash(space)}
		restricted = true
	} else if allowed != nil {
		if len(allowed) == 0 {
			return nil, nil
		}
		targets = make([]string, 0, len(allowed))
		for _, sp := range allowed {
			targets = append(targets, filepath.FromSlash(sp))
		}
		restricted = true
	}
	if restricted {
		// Path arguments are trusted directory names from the config or
		// the membership cache; refuse anything that could climb out.
		for _, t := range targets {
			if t == "." || strings.HasPrefix(t, "..") || strings.Contains(t, "/../") || strings.Contains(t, "\\") {
				return nil, nil
			}
		}
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
		"-e", pattern, "--",
	}
	args = append(args, targets...)
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Dir = r.root
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
		// rg runs with the notes root as its working directory, so
		// printed paths are already root-relative whatever the targets.
		rel := filepath.ToSlash(filepath.Clean(ev.Data.Path.Text))
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
