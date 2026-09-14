// Package spaces defines the sharing model: a space is a top-level
// directory whose .space.yml file lists its members. The file is part of
// the honest tree — hand-edit it, and the server follows within one
// watcher cycle.
package spaces

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the membership file every space may carry.
const FileName = ".space.yml"

// Roles, ordered. The global owner account (users.is_owner) implicitly
// holds RoleOwner in every space; rows in .space.yml grant access to
// everyone else.
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// ValidRole reports whether s is one of the three roles.
func ValidRole(s string) bool {
	return s == RoleOwner || s == RoleEditor || s == RoleViewer
}

// RoleAtLeast reports whether have satisfies want. An empty have is no
// membership at all.
func RoleAtLeast(have, want string) bool {
	rank := map[string]int{RoleViewer: 1, RoleEditor: 2, RoleOwner: 3}
	h, ok := rank[have]
	if !ok {
		return false
	}
	w, ok := rank[want]
	if !ok {
		return false
	}
	return h >= w
}

// Member is one line of a .space.yml member list. User is a user id
// (ULID) or a username; both resolve against the users table when the
// file is cached into space_members.
type Member struct {
	User string `yaml:"user" json:"user"`
	Role string `yaml:"role" json:"role"`
}

// Spec is the parsed content of one .space.yml.
type Spec struct {
	Name    string   `yaml:"name" json:"name"`
	Members []Member `yaml:"members" json:"members"`
}

// FileRel is the path of a space's membership file relative to the root.
func FileRel(space string) string { return space + "/" + FileName }

// IsFile reports whether rel is a space membership file
// ("<space>/.space.yml").
func IsFile(rel string) bool {
	space, ok := strings.CutSuffix(rel, "/"+FileName)
	return ok && ValidName(space)
}

// ValidName reports whether space is a usable space directory name.
func ValidName(space string) bool {
	if space == "" || strings.Contains(space, "/") || strings.HasPrefix(space, ".") {
		return false
	}
	if strings.TrimSpace(space) != space {
		return false
	}
	return true
}

// Parse decodes and validates a .space.yml. Unknown fields are an error:
// a typo in "members" should be loud, not silently ignored.
func Parse(data []byte) (Spec, error) {
	var spec Spec
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return Spec{}, fmt.Errorf("spaces: parse %s: %w", FileName, err)
	}
	return spec, validate(spec)
}

func validate(spec Spec) error {
	if len(spec.Name) > 100 {
		return fmt.Errorf("spaces: name is longer than 100 characters")
	}
	seen := map[string]string{}
	for i, m := range spec.Members {
		if m.User == "" {
			return fmt.Errorf("spaces: member %d has no user", i+1)
		}
		if len(m.User) > 64 {
			return fmt.Errorf("spaces: member %q is not a user id or username", m.User)
		}
		if !ValidRole(m.Role) {
			return fmt.Errorf("spaces: member %q has role %q (use owner, editor, or viewer)", m.User, m.Role)
		}
		if prev, dup := seen[strings.ToLower(m.User)]; dup {
			return fmt.Errorf("spaces: member %q is listed twice (role %q and %q)", m.User, prev, m.Role)
		}
		seen[strings.ToLower(m.User)] = m.Role
	}
	return nil
}

// Render writes a spec back as YAML with a comment header, so files the
// API writes stay pleasant to edit by hand.
func Render(spec Spec) []byte {
	var b strings.Builder
	b.WriteString("# YANA/ space membership. Edit by hand if you like; the server reloads it on save.\n")
	b.WriteString("# user is a username or id; role is owner, editor, or viewer.\n")
	if spec.Name != "" {
		fmt.Fprintf(&b, "name: %s\n", yamlScalar(spec.Name))
	}
	if len(spec.Members) == 0 {
		b.WriteString("members: []\n")
		return []byte(b.String())
	}
	b.WriteString("members:\n")
	for _, m := range spec.Members {
		fmt.Fprintf(&b, "  - user: %s\n    role: %s\n", yamlScalar(m.User), m.Role)
	}
	return []byte(b.String())
}

// yamlScalar quotes a string only when YAML would otherwise reshape it.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	needQuote := false
	for _, r := range s {
		if r < 0x20 || r == ':' || r == '#' || r == '{' || r == '}' || r == '[' || r == ']' || r == ',' ||
			r == '&' || r == '*' || r == '!' || r == '|' || r == '>' || r == '\'' || r == '"' ||
			r == '%' || r == '@' || r == '`' || r == '\u00a0' {
			needQuote = true
			break
		}
	}
	if needQuote || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}
