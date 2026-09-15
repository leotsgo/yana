// The tool surface: list_spaces, list_tree, read_note, write_note,
// append_note, search_notes, move_note. Tool results are plain text or
// JSON strings; a tool that fails answers isError=true with the reason,
// which the agent reads and can act on, rather than a protocol error.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/auth"
	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// toolResult is the MCP tools/call result.
type toolResult struct {
	Content []map[string]string `json:"content"`
	IsError bool                `json:"isError,omitempty"`
}

func textResult(v any) *toolResult {
	var s string
	if t, ok := v.(string); ok {
		s = t
	} else {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			s = fmt.Sprint(v)
		} else {
			s = string(b)
		}
	}
	return &toolResult{Content: []map[string]string{{"type": "text", "text": s}}}
}

func errResult(format string, args ...any) *toolResult {
	return &toolResult{
		Content: []map[string]string{{"type": "text", "text": fmt.Sprintf(format, args...)}},
		IsError: true,
	}
}

// toolList describes the tools. inputSchema is JSON Schema; the shapes
// follow the plan's signatures.
func toolList() []map[string]any {
	str := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	req := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	return []map[string]any{
		{
			"name":        "list_spaces",
			"description": "List the spaces this token may see, with note counts.",
			"inputSchema": req(map[string]any{}),
		},
		{
			"name":        "list_tree",
			"description": "List the notes of one space as a nested tree, optionally under a path prefix.",
			"inputSchema": req(
				map[string]any{"space": str("space name"), "path": str("optional path prefix inside the space")},
				"space"),
		},
		{
			"name":        "read_note",
			"description": "Read one note by id (26-character ULID) or by path (space/folders/note.md). Returns metadata and content.",
			"inputSchema": req(map[string]any{"note": str("note id or path")}, "note"),
		},
		{
			"name":        "write_note",
			"description": "Create or replace a note. The edit is applied to the note's document, so concurrent human typing merges rather than being overwritten. Do not write frontmatter id or created keys; the server assigns them.",
			"inputSchema": req(
				map[string]any{
					"space":       str("space name"),
					"path":        str("note path inside the space, ending in .md or .html"),
					"content":     str("the full note content"),
					"agent_label": str("optional author label; defaults to this token's label"),
				},
				"space", "path", "content"),
		},
		{
			"name":        "append_note",
			"description": "Append content to the end of an existing note, by id or path.",
			"inputSchema": req(
				map[string]any{
					"note":        str("note id or path"),
					"content":     str("content to append"),
					"agent_label": str("optional author label; defaults to this token's label"),
				},
				"note", "content"),
		},
		{
			"name":        "search_notes",
			"description": "Full-text search across the token's spaces, or one named space.",
			"inputSchema": req(
				map[string]any{
					"query": str("search text"),
					"space": str("optional space to restrict to"),
					"limit": map[string]any{"type": "integer", "description": "maximum results, default 50", "minimum": 1, "maximum": 200},
				},
				"query"),
		},
		{
			"name":        "move_note",
			"description": "Move or rename a note by id to a new path. Inbound wikilinks are rewritten.",
			"inputSchema": req(
				map[string]any{
					"note":     str("note id or path"),
					"new_path": str("the note's new full path (space/folders/name.md)"),
				},
				"note", "new_path"),
		},
	}
}

// callTool runs one tool call. The tool name is already known-good;
// callTool only interprets arguments.
func (h *Handler) callTool(ctx context.Context, agent auth.AgentIdentity, name string, args json.RawMessage) *toolResult {
	var a map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return errResult("arguments must be a JSON object")
		}
	}
	arg := func(k string) string {
		if v, ok := a[k].(string); ok {
			return v
		}
		return ""
	}
	author, res := h.author(agent, arg("agent_label"))
	if res != nil {
		return res
	}
	h.Log.Info("tool call", "tool", name, "agent", agent.Label, "author", author)

	switch name {
	case "list_spaces":
		return h.toolListSpaces(ctx, agent)
	case "list_tree":
		return h.toolListTree(ctx, agent, arg("space"), arg("path"))
	case "read_note":
		return h.toolReadNote(ctx, agent, arg("note"))
	case "write_note":
		return h.toolWriteNote(ctx, agent, author, arg("space"), arg("path"), arg("content"))
	case "append_note":
		return h.toolAppendNote(ctx, agent, author, arg("note"), arg("content"))
	case "search_notes":
		return h.toolSearch(ctx, agent, arg("query"), arg("space"), a["limit"])
	case "move_note":
		return h.toolMoveNote(ctx, agent, arg("note"), arg("new_path"))
	}
	return nil // unreachable: dispatch refuses unknown names
}

// knownTool reports whether name is on the surface.
func knownTool(name string) bool {
	switch name {
	case "list_spaces", "list_tree", "read_note", "write_note", "append_note", "search_notes", "move_note":
		return true
	}
	return false
}

// author resolves the write author for a call: the caller's label, or
// the agent_label argument when the token holder wants its job named.
func (h *Handler) author(agent auth.AgentIdentity, label string) (string, *toolResult) {
	label = strings.TrimSpace(label)
	if label == "" {
		label = agent.Label
	}
	if !auth.ValidAgentLabel(label) {
		return "", errResult("agent_label must be 1-64 characters without colons or angle brackets")
	}
	return "agent:" + label, nil
}

// --- scoping ---------------------------------------------------------------

type scope struct {
	spaces   map[string]bool
	canWrite bool
}

func (s scope) maySee(space string) bool { return s.spaces[space] }

func (s scope) mayWrite(space string) bool { return s.canWrite && s.spaces[space] }

// scopeOf builds the token's space set.
func scopeOf(agent auth.AgentIdentity) scope {
	sp := map[string]bool{}
	for _, s := range agent.Spaces {
		sp[s] = true
	}
	return scope{spaces: sp, canWrite: agent.CanWrite}
}

// spaceScope validates and returns the named space within the token's
// scope. ok=false means the result was already built.
func (h *Handler) spaceScope(agent auth.AgentIdentity, space string) (string, *toolResult) {
	clean, err := h.Root.Clean(space)
	if err != nil || clean == "" || strings.Contains(clean, "/") {
		return "", errResult("space must be a single directory name")
	}
	if !scopeOf(agent).maySee(clean) {
		return "", errResult("this token is not scoped to space %q", clean)
	}
	return clean, nil
}

// resolveNote finds a note by id or path and checks the token may see
// its space.
func (h *Handler) resolveNote(ctx context.Context, agent auth.AgentIdentity, ref string) (index.Note, *toolResult) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return index.Note{}, errResult("note is required")
	}
	var (
		n   index.Note
		err error
	)
	if len(ref) == 26 && isAlnum(ref) {
		n, err = h.DB.GetNote(ctx, ref)
	} else {
		clean, cerr := h.Root.Clean(ref)
		if cerr != nil {
			return index.Note{}, errResult("that path is not allowed")
		}
		n, err = h.DB.GetNoteByPath(ctx, clean)
	}
	if errors.Is(err, index.ErrNotFound) {
		return index.Note{}, errResult("no note matches %q", ref)
	}
	if err != nil {
		return index.Note{}, errResult("the note could not be read")
	}
	if !scopeOf(agent).maySee(n.Space) {
		// Out-of-scope reads look like missing notes; existence is not
		// disclosed.
		return index.Note{}, errResult("no note matches %q", ref)
	}
	return n, nil
}

func isAlnum(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

// withinSpace normalises a note path inside a space: accepts
// "space/folders/note.md" and "folders/note.md" alike. The cleaned
// result is guaranteed to still sit inside the named space — a
// "space/../other/note.md" that escapes is a rejection, not a redirect
// into a space the token never saw.
func (h *Handler) withinSpace(space, p string) (clean string, res *toolResult) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errResult("path is required")
	}
	if prefix := space + "/"; strings.HasPrefix(p, prefix) {
		p = strings.TrimPrefix(p, prefix)
	}
	if p == "" || strings.HasPrefix(p, "/") {
		return "", errResult("path must be inside the space")
	}
	var err error
	clean, err = h.Root.Clean(space + "/" + p)
	if err != nil || spaceOfRel(clean) != space {
		return "", errResult("that path is not allowed")
	}
	return clean, nil
}

// --- tools -----------------------------------------------------------------

type spaceInfo struct {
	Name  string `json:"name"`
	Notes int    `json:"notes"`
	Label string `json:"label,omitempty"`
}

func (h *Handler) toolListSpaces(ctx context.Context, agent auth.AgentIdentity) *toolResult {
	rows, err := h.DB.ListSpaces(ctx)
	if err != nil {
		return errResult("the space list could not be read")
	}
	byName := map[string]index.SpaceRow{}
	for _, r := range rows {
		byName[r.Space] = r
	}
	names := make([]string, 0, len(agent.Spaces))
	for _, s := range agent.Spaces {
		names = append(names, s)
	}
	sort.Strings(names)
	out := make([]spaceInfo, 0, len(names))
	for _, name := range names {
		info := spaceInfo{Name: name}
		if r, ok := byName[name]; ok {
			info.Notes = r.Notes
			info.Label = r.Label
		}
		out = append(out, info)
	}
	return textResult(map[string]any{"spaces": out})
}

type treeNode struct {
	Type     string      `json:"type"` // "dir" | "note"
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	ID       string      `json:"id,omitempty"`
	Title    string      `json:"title,omitempty"`
	Kind     string      `json:"kind,omitempty"`
	Children []*treeNode `json:"children,omitempty"`
}

func (h *Handler) toolListTree(ctx context.Context, agent auth.AgentIdentity, space, prefix string) *toolResult {
	sp, res := h.spaceScope(agent, space)
	if res != nil {
		return res
	}
	if prefix != "" {
		clean, err := h.Root.Clean(sp + "/" + strings.Trim(prefix, "/"))
		if err != nil || spaceOfRel(clean) != sp {
			return errResult("that path is not allowed")
		}
		prefix = strings.TrimPrefix(clean, sp+"/")
	}
	notes, err := h.DB.ListNotes(ctx, sp)
	if err != nil {
		return errResult("the tree could not be read")
	}
	root := &treeNode{Type: "dir", Name: sp, Path: sp}
	count := 0
	for _, n := range notes {
		rel := strings.TrimPrefix(n.RelPath, sp+"/")
		if prefix != "" && rel != prefix && !strings.HasPrefix(rel, prefix+"/") {
			continue
		}
		parts := strings.Split(rel, "/")
		cur := root
		for i, part := range parts[:len(parts)-1] {
			dirPath := sp + "/" + strings.Join(parts[:i+1], "/")
			cur = childNode(cur, part, dirPath)
		}
		cur.Children = append(cur.Children, &treeNode{
			Type: "note", Name: parts[len(parts)-1], Path: n.RelPath,
			ID: n.ID, Title: n.Title, Kind: n.Kind,
		})
		count++
	}
	sortNodes(root)
	return textResult(map[string]any{"space": sp, "notes": count, "tree": root.Children})
}

func childNode(parent *treeNode, name, path string) *treeNode {
	for _, c := range parent.Children {
		if c.Type == "dir" && c.Name == name {
			return c
		}
	}
	d := &treeNode{Type: "dir", Name: name, Path: path}
	parent.Children = append(parent.Children, d)
	return d
}

func sortNodes(n *treeNode) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.Type != b.Type {
			return a.Type == "dir"
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	for _, c := range n.Children {
		if c.Type == "dir" {
			sortNodes(c)
		}
	}
}

func (h *Handler) toolReadNote(ctx context.Context, agent auth.AgentIdentity, ref string) *toolResult {
	n, res := h.resolveNote(ctx, agent, ref)
	if res != nil {
		return res
	}
	content, err := h.noteText(ctx, n)
	if err != nil {
		return errResult("the note's content could not be read")
	}
	tags, _ := h.DB.Tags(ctx, n.ID)
	if tags == nil {
		tags = []string{}
	}
	return textResult(map[string]any{
		"id": n.ID, "space": n.Space, "path": n.RelPath, "title": n.Title,
		"kind": n.Kind, "created": n.Created, "updated": n.UpdatedAt,
		"tags": tags, "content": content,
	})
}

// noteText returns the note's current text: the document when the
// reconciliation loop is running (it holds edits not yet written back),
// the file otherwise.
func (h *Handler) noteText(ctx context.Context, n index.Note) (string, error) {
	if h.Sync != nil {
		return h.Sync.Text(ctx, n.ID)
	}
	abs, _, err := h.Root.Resolve(n.RelPath)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(frontmatter.Parse(raw).Body), nil
}

func (h *Handler) toolWriteNote(ctx context.Context, agent auth.AgentIdentity, author, space, p, content string) *toolResult {
	sp, res := h.spaceScope(agent, space)
	if res != nil {
		return res
	}
	if !scopeOf(agent).mayWrite(sp) {
		return errResult("this token cannot write to space %q", sp)
	}
	if h.Sync == nil {
		return errResult("%v", errNoSync)
	}
	clean, res := h.withinSpace(sp, p)
	if res != nil {
		return res
	}
	if scanner.KindOf(clean) == "" {
		return errResult("the path must end in .md, .markdown, .html, or .htm")
	}
	if scanner.IsAsset(clean) {
		return errResult("the path sits under _assets; notes live outside it")
	}
	if int64(len(content)) > h.Root.Limits().MaxNoteSize {
		return errResult("content is over the note size limit (%d bytes)", h.Root.Limits().MaxNoteSize)
	}
	if h.Limit != nil && !h.Limit.Allow(author) {
		return errResult("the agent write rate is exceeded; slow down and retry")
	}

	if n, err := h.DB.GetNoteByPath(ctx, clean); err == nil {
		if n.Space != sp {
			return errResult("a note from another space sits at that path")
		}
		if err := h.Sync.SetText(ctx, n.ID, content, author); err != nil {
			return errResult("the write could not be applied to %s", n.RelPath)
		}
		return textResult(map[string]any{"id": n.ID, "path": n.RelPath, "created": false})
	}

	// Creation: the file lands carrying only its frontmatter (so the id
	// and the document exist), then the content is applied as the
	// agent's edit — the body ops carry the agent author and git
	// attributes the note to the agent, which a pre-filled file would
	// not.
	abs, _, err := h.Root.Resolve(clean)
	if err != nil {
		if pathsafe.IsRejection(err) {
			return errResult("that path is not allowed")
		}
		return errResult("the path could not be resolved")
	}
	if err := h.Root.CheckCollision(clean); err != nil {
		return errResult("a file with a name equal after case folding exists at that path")
	}
	id := scanner.NewID(time.Now())
	seed, _, _ := frontmatter.EnsureID(nil, id, time.Now())
	if err := fsutil.MkdirInherit(filepath.Dir(abs)); err != nil {
		return errResult("the folder could not be created")
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return errResult("a file already exists at that path")
		}
		return errResult("the note file could not be created")
	}
	if _, err := f.Write(seed); err != nil {
		f.Close()
		return errResult("the note file could not be written")
	}
	f.Close()
	if h.Scanner != nil {
		if err := h.Scanner.ScanOne(ctx, clean); err != nil && !scanner.IsDeferred(err) {
			return errResult("the note was created but is not indexed yet")
		}
	}
	if err := h.Sync.SetText(ctx, id, content, author); err != nil {
		return errResult("the note was created but the content could not be applied; retry with write_note")
	}
	return textResult(map[string]any{"id": id, "path": clean, "created": true})
}

func (h *Handler) toolAppendNote(ctx context.Context, agent auth.AgentIdentity, author, ref, content string) *toolResult {
	if h.Sync == nil {
		return errResult("%v", errNoSync)
	}
	n, res := h.resolveNote(ctx, agent, ref)
	if res != nil {
		return res
	}
	if !scopeOf(agent).mayWrite(n.Space) {
		return errResult("this token cannot write to space %q", n.Space)
	}
	if int64(len(content)) > h.Root.Limits().MaxNoteSize {
		return errResult("content is over the note size limit (%d bytes)", h.Root.Limits().MaxNoteSize)
	}
	if h.Limit != nil && !h.Limit.Allow(author) {
		return errResult("the agent write rate is exceeded; slow down and retry")
	}
	cur, err := h.Sync.Text(ctx, n.ID)
	if err != nil {
		return errResult("the note could not be read")
	}
	next := content
	if cur != "" {
		next = cur
		if !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		next += content
	}
	if int64(len(next)) > h.Root.Limits().MaxNoteSize {
		return errResult("the append would put the note over its size limit")
	}
	if err := h.Sync.SetText(ctx, n.ID, next, author); err != nil {
		return errResult("the append could not be applied")
	}
	return textResult(map[string]any{"id": n.ID, "path": n.RelPath, "appended": len(content)})
}

func (h *Handler) toolSearch(ctx context.Context, agent auth.AgentIdentity, query, space string, limitArg any) *toolResult {
	query = strings.TrimSpace(query)
	if query == "" {
		return errResult("query is required")
	}
	if len(query) > 512 {
		return errResult("query is longer than 512 characters")
	}
	allowed := append([]string(nil), agent.Spaces...)
	sort.Strings(allowed)
	if space != "" {
		sp, res := h.spaceScope(agent, space)
		if res != nil {
			return res
		}
		allowed = []string{sp}
	}
	limit := 50
	if v, ok := limitArg.(float64); ok {
		limit = int(v)
	}
	hits, err := h.DB.Search(ctx, query, "", allowed, limit)
	if err != nil {
		return errResult("the search could not run")
	}
	out := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		out = append(out, map[string]any{
			"id": hit.Note.ID, "path": hit.Note.RelPath, "title": hit.Note.Title,
			"space": hit.Note.Space, "snippet": hit.Snippet,
		})
	}
	return textResult(map[string]any{"hits": out})
}

func (h *Handler) toolMoveNote(ctx context.Context, agent auth.AgentIdentity, ref, newPath string) *toolResult {
	if h.Sync == nil {
		return errResult("%v", errNoSync)
	}
	n, res := h.resolveNote(ctx, agent, ref)
	if res != nil {
		return res
	}
	sc := scopeOf(agent)
	if !sc.mayWrite(n.Space) {
		return errResult("this token cannot write to space %q", n.Space)
	}
	clean, err := h.Root.Clean(strings.TrimSpace(newPath))
	if err != nil {
		return errResult("that path is not allowed")
	}
	target := spaceOfRel(clean)
	if target == "" {
		return errResult("the new path must name a space (space/folders/note.md)")
	}
	if !sc.mayWrite(target) {
		return errResult("this token cannot write to space %q", target)
	}
	may := func(space string) error {
		if !sc.mayWrite(space) {
			return &reconcile.SpaceDeniedError{Space: space, Err: errDenied}
		}
		return nil
	}
	mr, err := h.Sync.Move(ctx, n.ID, clean, may)
	switch {
	case err == nil:
	case errors.Is(err, reconcile.ErrMoveSamePath):
		return errResult("the note is already at that path")
	case errors.Is(err, reconcile.ErrMoveTargetTaken):
		return errResult("a file already exists at that path")
	default:
		var denied *reconcile.SpaceDeniedError
		if errors.As(err, &denied) {
			return errResult("the move was refused: %v", denied.Unwrap())
		}
		if pathsafe.IsRejection(err) {
			return errResult("that path is not allowed")
		}
		return errResult("the move could not be applied")
	}
	return textResult(map[string]any{
		"id": n.ID, "path": mr.NewPath, "rewritten": mr.Rewritten, "broken": mr.Broken,
	})
}

var errDenied = errors.New("no write access to this space")

func spaceOfRel(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}
