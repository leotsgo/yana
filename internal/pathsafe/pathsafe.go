// Package pathsafe is the only way untrusted strings become filesystem
// paths. Every API handler, scanner, exporter and agent tool converts a
// relative note path through a Root before touching the disk.
//
// A Root is a directory the rest of the program may read and write under.
// Resolve turns a caller-supplied relative path into an absolute path that is
// guaranteed, after normalisation and symlink resolution, to sit inside the
// root. Anything else is rejected with an *Error describing why.
package pathsafe

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Limits bounds sizes the rest of the system enforces through this package.
type Limits struct {
	MaxPathLen       int   // whole relative path, bytes after NFC
	MaxNameLen       int   // one path segment, bytes after NFC
	MaxNoteSize      int64 // bytes
	MaxAssetSize     int64 // bytes
	MaxNotesPerSpace int
}

// DefaultLimits are conservative values that fit every mainstream filesystem.
func DefaultLimits() Limits {
	return Limits{
		MaxPathLen:       1024,
		MaxNameLen:       255,
		MaxNoteSize:      10 << 20,
		MaxAssetSize:     50 << 20,
		MaxNotesPerSpace: 100_000,
	}
}

// Error explains why a path was rejected. Reason is stable and suitable for
// an API response; Path is the offending input.
type Error struct {
	Path   string
	Reason string
}

func (e *Error) Error() string { return fmt.Sprintf("path %q rejected: %s", e.Path, e.Reason) }

// ErrOutsideRoot is the reason given when a path escapes the root after
// symlink resolution.
var ErrOutsideRoot = errors.New("path resolves outside the root")

// ErrCollision is the reason given when a case-insensitive sibling exists.
var ErrCollision = errors.New("a file with the same name in different case already exists")

// Root is a directory under which paths may be resolved.
type Root struct {
	abs    string // cleaned absolute path as given
	real   string // after EvalSymlinks
	limits Limits
}

// NewRoot validates dir and returns a Root. dir must exist and be a
// directory; it may itself be a symlink (the resolved target is used for
// the containment check).
func NewRoot(dir string, limits Limits) (*Root, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("root %s: %w", dir, err)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("root %s is not a directory", dir)
	}
	if limits.MaxPathLen == 0 {
		limits = DefaultLimits()
	}
	return &Root{abs: abs, real: real, limits: limits}, nil
}

// Dir returns the root's absolute path.
func (r *Root) Dir() string { return r.abs }

// Limits returns the limits this root enforces.
func (r *Root) Limits() Limits { return r.limits }

// Clean validates and normalises rel without touching the filesystem. It
// returns the canonical relative path (forward slashes, NFC, no leading
// "./"). The empty string and "." both denote the root itself.
func (r *Root) Clean(rel string) (string, error) {
	if !utf8.ValidString(rel) {
		return "", &Error{rel, "not valid UTF-8"}
	}
	for _, c := range rel {
		if c == 0 || unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return "", &Error{rel, "contains a control or format character"}
		}
	}
	if strings.ContainsRune(rel, '\\') {
		return "", &Error{rel, "contains a backslash"}
	}
	rel = norm.NFC.String(rel)
	if len(rel) > r.limits.MaxPathLen {
		return "", &Error{rel, fmt.Sprintf("longer than %d bytes", r.limits.MaxPathLen)}
	}
	if strings.HasPrefix(rel, "/") {
		return "", &Error{rel, "absolute paths are not allowed"}
	}
	if rel == "" || rel == "." {
		return "", nil
	}
	// Segment checks happen on the raw input, before path.Clean can hide a
	// ".." by collapsing it.
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if err := r.checkSegment(rel, seg); err != nil {
			return "", err
		}
	}
	cleaned := path.Clean(rel)
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", &Error{rel, "escapes the root"}
	}
	return cleaned, nil
}

func (r *Root) checkSegment(whole, seg string) error {
	if seg == ".." {
		return &Error{whole, "contains a \"..\" segment"}
	}
	if len(seg) > r.limits.MaxNameLen {
		return &Error{whole, fmt.Sprintf("segment %q is longer than %d bytes", seg, r.limits.MaxNameLen)}
	}
	if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
		return &Error{whole, fmt.Sprintf("segment %q ends with a dot or space", seg)}
	}
	if strings.HasPrefix(seg, " ") {
		return &Error{whole, fmt.Sprintf("segment %q starts with a space", seg)}
	}
	base := seg
	if i := strings.IndexByte(seg, '.'); i >= 0 {
		base = seg[:i]
	}
	if isWindowsReserved(base) {
		return &Error{whole, fmt.Sprintf("segment %q is a reserved device name", seg)}
	}
	for _, c := range seg {
		switch c {
		case '<', '>', ':', '"', '|', '?', '*':
			return &Error{whole, fmt.Sprintf("segment %q contains %q", seg, c)}
		}
	}
	return nil
}

func isWindowsReserved(base string) bool {
	u := strings.ToUpper(base)
	switch u {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(u) == 4 && (strings.HasPrefix(u, "COM") || strings.HasPrefix(u, "LPT")) {
		return u[3] >= '1' && u[3] <= '9'
	}
	return false
}

// Resolve cleans rel and returns the absolute path for it inside the root,
// after verifying that no component of the path (existing or not) resolves
// through a symlink to somewhere outside the root. The second return value
// is the canonical relative path from Clean.
//
// Resolve does not require the target to exist; the longest existing prefix
// is symlink-resolved and the remainder is appended verbatim.
func (r *Root) Resolve(rel string) (string, string, error) {
	cleaned, err := r.Clean(rel)
	if err != nil {
		return "", "", err
	}
	abs := filepath.Join(r.abs, filepath.FromSlash(cleaned))
	if err := r.contains(abs); err != nil {
		return "", "", &Error{rel, err.Error()}
	}
	return abs, cleaned, nil
}

// contains resolves symlinks on the longest existing prefix of abs and
// asserts the result is under the real root.
func (r *Root) contains(abs string) error {
	existing := abs
	var rest []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return ErrOutsideRoot
		}
		rest = append([]string{filepath.Base(existing)}, rest...)
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		// A dangling symlink inside the tree: refuse rather than guess.
		return fmt.Errorf("%w (unresolvable link)", ErrOutsideRoot)
	}
	full := filepath.Join(append([]string{real}, rest...)...)
	if full != r.real && !strings.HasPrefix(full, r.real+string(filepath.Separator)) {
		return ErrOutsideRoot
	}
	return nil
}

// CheckCollision reports ErrCollision when a sibling of rel already exists
// whose name differs only by case (or by Unicode normalisation form). This
// keeps a tree portable between case-sensitive and case-insensitive
// filesystems.
func (r *Root) CheckCollision(rel string) error {
	abs, cleaned, err := r.Resolve(rel)
	if err != nil {
		return err
	}
	if cleaned == "" {
		return nil
	}
	if _, err := os.Lstat(abs); err == nil {
		return nil // exact name exists; that is the file, not a collision
	}
	entries, err := os.ReadDir(filepath.Dir(abs))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	want := foldName(filepath.Base(abs))
	for _, e := range entries {
		if foldName(e.Name()) == want {
			return &Error{rel, fmt.Sprintf("%v with %q", ErrCollision, e.Name())}
		}
	}
	return nil
}

func foldName(s string) string {
	return strings.ToLower(norm.NFC.String(s))
}

// IsRejection reports whether err came from this package's validation (as
// opposed to an I/O failure), so handlers can map it to a 400.
func IsRejection(err error) bool {
	var e *Error
	return errors.As(err, &e)
}
