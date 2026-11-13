// The tool surface: list_spaces, list_tree, read_note, write_note,
// append_note, search_notes, move_note, upload_asset, draw_flowchart,
// add_drawing_image. Tool results are plain text or
// JSON strings; a tool that fails answers isError=true with the reason,
// which the agent reads and can act on, rather than a protocol error.
package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/leotsgo/yana/internal/auth"
	"github.com/leotsgo/yana/internal/drawing"
	"github.com/leotsgo/yana/internal/drawing/flowchart"
	"github.com/leotsgo/yana/internal/frontmatter"
	"github.com/leotsgo/yana/internal/fsutil"
	"github.com/leotsgo/yana/internal/index"
	"github.com/leotsgo/yana/internal/pathsafe"
	"github.com/leotsgo/yana/internal/reconcile"
	"github.com/leotsgo/yana/internal/scanner"
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
		schema := map[string]any{"type": "object", "properties": props}
		// Omit "required" when nothing is required: a nil variadic marshals as
		// JSON null, which is not a valid JSON Schema "required" and makes
		// strict clients reject the whole tools/list response. Absence already
		// means every property is optional.
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
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
					"path":        str("note path inside the space, ending in .md or .html (drawings, .excalidraw.md, are changed with draw_flowchart and add_drawing_image)"),
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
		{
			"name":        "upload_asset",
			"description": "Upload a file (typically an image) into the _assets directory beside a note. Returns the stored path and a markdown link, relative to the note, to put in its content with write_note or append_note. A name already in use gets a numeric suffix.",
			"inputSchema": req(
				map[string]any{
					"space":       str("space name"),
					"note":        str("path of the note the file belongs to (space/folders/note.md); the note need not exist yet"),
					"filename":    str("file name, e.g. diagram.png"),
					"data":        str("the file content, base64-encoded (a data: URL is accepted too)"),
					"agent_label": str("optional author label; defaults to this token's label"),
				},
				"space", "note", "filename", "data"),
		},
		{
			"name":        "draw_flowchart",
			"description": "Draw a Mermaid flowchart as an Excalidraw drawing (*.excalidraw.md) the user can rearrange by hand. Use it when the user asks for a drawing; a mermaid block in a note is still fine otherwise. New path: laid out automatically. Existing drawing: pass base from read_note; nodes and edges you send are added or updated by id, nothing moves, and nothing is deleted unless listed in remove. Put the returned embed in a note to show the drawing there. Flowcharts only. An edge you send replaces that edge's label and line style; send only the edges you mean to change.",
			"inputSchema": req(
				map[string]any{
					"space":       str("space name"),
					"path":        str("drawing path inside the space, ending in .excalidraw.md"),
					"mermaid":     str("the flowchart, starting with a line like \"flowchart LR\""),
					"base":        str("the drawing's base from read_note; required when the drawing exists"),
					"remove":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "ids from read_note's layout to delete (nodes, edges like a->b, subgraphs, free items)"},
					"agent_label": str("optional author label; defaults to this token's label"),
				},
				"space", "path", "mermaid"),
		},
		{
			"name":        "add_drawing_image",
			"description": "Add a PNG, JPEG or GIF picture to a drawing, beside a node or below everything else, never on top of anything. Pass base from read_note.",
			"inputSchema": req(
				map[string]any{
					"drawing":     str("drawing id or path"),
					"asset":       str("path of an image file already in the drawing's space; give this or data"),
					"data":        str("the image, base64-encoded (a data: URL is accepted too); give this or asset"),
					"near":        str("optional node id to put the picture beside"),
					"width":       map[string]any{"type": "number", "description": "optional width in drawing units; default the image's own, at most 400", "exclusiveMinimum": 0},
					"base":        str("the drawing's base from read_note"),
					"agent_label": str("optional author label; defaults to this token's label"),
				},
				"drawing", "base"),
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
	case "upload_asset":
		return h.toolUploadAsset(ctx, agent, author, arg("space"), arg("note"), arg("filename"), arg("data"))
	case "draw_flowchart":
		var remove []string
		if v := a["remove"]; v != nil {
			list, ok := v.([]any)
			if !ok {
				return errResult("remove must be a list of ids")
			}
			for _, x := range list {
				id, ok := x.(string)
				if !ok {
					return errResult("remove must be a list of ids")
				}
				remove = append(remove, id)
			}
		}
		return h.toolDrawFlowchart(ctx, agent, author, arg("space"), arg("path"), arg("mermaid"), arg("base"), remove)
	case "add_drawing_image":
		width, _ := a["width"].(float64) // 0 or less: the image's own width
		width = min(width, maxImageWidth)
		return h.toolAddDrawingImage(ctx, agent, author, arg("drawing"), arg("asset"), arg("data"), arg("near"), arg("base"), width)
	}
	return nil // unreachable: dispatch refuses unknown names
}

// knownTool reports whether name is on the surface.
func knownTool(name string) bool {
	switch name {
	case "list_spaces", "list_tree", "read_note", "write_note", "append_note", "search_notes", "move_note", "upload_asset",
		"draw_flowchart", "add_drawing_image":
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
// its space. param names the argument ref came from, for the error.
func (h *Handler) resolveNote(ctx context.Context, agent auth.AgentIdentity, param, ref string) (index.Note, *toolResult) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return index.Note{}, errResult("%s is required", param)
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
	n, res := h.resolveNote(ctx, agent, "note", ref)
	if res != nil {
		return res
	}
	tags, _ := h.DB.Tags(ctx, n.ID)
	if tags == nil {
		tags = []string{}
	}
	out := map[string]any{
		"id": n.ID, "space": n.Space, "path": n.RelPath, "title": n.Title,
		"kind": n.Kind, "created": n.Created, "updated": n.UpdatedAt, "tags": tags,
	}
	if !isDrawing(n) {
		content, err := h.noteText(ctx, n)
		if err != nil {
			return errResult("the note's content could not be read")
		}
		out["content"] = content
		return textResult(out)
	}
	abs, _, err := h.Root.Resolve(n.RelPath)
	if err != nil {
		return errResult("the note's content could not be read")
	}
	file, err := os.ReadFile(abs)
	if err != nil {
		return errResult("the note's content could not be read")
	}
	_, scene, err := drawing.Parse(frontmatter.Parse(file).Body)
	var v flowchart.View
	if err == nil {
		v, err = flowchart.Read(scene)
	}
	if err != nil {
		out["content"] = "Excalidraw drawing (could not be read)."
		return textResult(out)
	}
	out["base"] = drawing.Hash(file)
	content, images := drawingContent(v, scene)
	out["content"] = content
	r := textResult(out)
	r.Content = append(r.Content, images...)
	return r
}

// maxImageWidth caps add_drawing_image's width, in drawing units.
const maxImageWidth = 4000

// Caps on the pictures read_note hands back with a drawing.
const (
	maxDrawingImages     = 5
	maxDrawingImageBytes = 4 << 20
)

// drawingContent is what an agent reads of a drawing: the flowchart as
// Mermaid plus the layout of every item, never the scene JSON, and its
// pictures as image blocks. A picture past the caps is marked
// omitted, which its layout line shows as "(not included)".
func drawingContent(v flowchart.View, scene []byte) (string, []map[string]string) {
	files, _ := flowchart.Files(scene) // a bad file reads as not included
	var images []map[string]string
	included := map[string]bool{}
	total := 0
	for _, f := range files {
		if len(images) == maxDrawingImages || total+len(f.Data) > maxDrawingImageBytes {
			continue
		}
		total += len(f.Data)
		included[f.ID] = true
		images = append(images, map[string]string{
			"type": "image", "data": base64.StdEncoding.EncodeToString(f.Data), "mimeType": f.MimeType,
		})
	}
	for i, it := range v.Items {
		v.Items[i].Omitted = it.Type == "image" && !included[it.FileID]
	}
	return v.Text(), images
}

func isDrawing(n index.Note) bool { return n.Kind == drawing.Kind || drawing.Is(n.RelPath) }

const errDrawingTool = "drawings are changed with draw_flowchart and add_drawing_image, not this tool"

// noteText returns the note's current text: the document when the
// reconciliation loop is running (it holds edits not yet written back),
// the file otherwise. A drawing is always the file: it is saved whole and
// never gets a CRDT document.
func (h *Handler) noteText(ctx context.Context, n index.Note) (string, error) {
	if h.Sync != nil && !isDrawing(n) {
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
	if drawing.Is(clean) {
		return errResult(errDrawingTool)
	}
	if scanner.KindOf(clean) == "" {
		return errResult("the path must end in .md, .markdown, .html, or .htm (not .excalidraw.md)")
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
	n, res := h.resolveNote(ctx, agent, "note", ref)
	if res != nil {
		return res
	}
	if !scopeOf(agent).mayWrite(n.Space) {
		return errResult("this token cannot write to space %q", n.Space)
	}
	if isDrawing(n) {
		return errResult(errDrawingTool)
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
	n, res := h.resolveNote(ctx, agent, "note", ref)
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

func (h *Handler) toolUploadAsset(ctx context.Context, agent auth.AgentIdentity, author, space, note, filename, data string) *toolResult {
	sp, res := h.spaceScope(agent, space)
	if res != nil {
		return res
	}
	if !scopeOf(agent).mayWrite(sp) {
		return errResult("this token cannot write to space %q", sp)
	}
	notePath, res := h.withinSpace(sp, note)
	if res != nil {
		return res
	}
	if scanner.IsAsset(notePath) {
		return errResult("note must be a note path, not one under _assets")
	}
	name := strings.Join(strings.Fields(strings.TrimSpace(filename)), "-")
	if name == "" || strings.ContainsAny(name, `/\`) {
		return errResult("filename must be a plain file name")
	}
	if scanner.KindOf(name) != "" {
		return errResult("notes are written with write_note, not uploaded")
	}
	raw, err := decodeData(data)
	if err != nil {
		return errResult("data is not valid base64")
	}
	if len(raw) == 0 {
		return errResult("the file is empty")
	}
	if limit := h.Root.Limits().MaxAssetSize; int64(len(raw)) > limit {
		return errResult("file is over the asset size limit (%d bytes)", limit)
	}
	if h.Limit != nil && !h.Limit.Allow(author) {
		return errResult("the agent write rate is exceeded; slow down and retry")
	}
	clean, err := h.Root.Clean(path.Dir(notePath) + "/_assets/" + name)
	if err != nil {
		return errResult("that file name is not allowed")
	}
	final, ok := h.Root.FreeName(clean)
	if !ok {
		return errResult("could not find a free name for the file")
	}
	abs, _, err := h.Root.Resolve(final)
	if err != nil {
		return errResult("that path is not allowed")
	}
	if err := fsutil.MkdirInherit(filepath.Dir(abs)); err != nil {
		return errResult("the _assets folder could not be created")
	}
	if err := fsutil.WriteFileAtomic(abs, raw, 0o644); err != nil {
		return errResult("the file could not be written")
	}
	if h.Scanner != nil {
		if err := h.Scanner.ScanAsset(ctx, final); err != nil {
			h.Log.Warn("uploaded asset is not indexed yet", "path", final, "err", err)
		}
	}
	base := path.Base(final)
	link := fmt.Sprintf("[%s](_assets/%s)", base, base)
	switch strings.ToLower(path.Ext(base)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".avif":
		alt := strings.NewReplacer("-", " ", "_", " ").Replace(strings.TrimSuffix(base, path.Ext(base)))
		link = fmt.Sprintf("![%s](_assets/%s)", alt, base)
	}
	h.Log.Info("asset uploaded", "path", final, "bytes", len(raw), "author", author)
	return textResult(map[string]any{"path": final, "name": base, "size": len(raw), "markdown": link})
}

var errDenied = errors.New("no write access to this space")

func spaceOfRel(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

// decodeData decodes a base64 argument, accepting a data: URL too.
func decodeData(data string) ([]byte, error) {
	if strings.HasPrefix(data, "data:") {
		data = data[strings.IndexByte(data, ',')+1:]
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(data))
}

// --- drawings --------------------------------------------------------------

// toolError is a refusal whose text the agent reads as it is.
type toolError string

func (e toolError) Error() string { return string(e) }

const (
	errStale      toolError = "the drawing changed since you read it; read it again"
	errUnreadable toolError = "the drawing on disk could not be read; open it in yana first"
	errNoBase     toolError = "base is required to change an existing drawing; read_note returns it"
)

// drawingError words a flowchart error for the agent.
func drawingError(err error) toolError {
	var pe *flowchart.ParseError
	switch {
	case errors.Is(err, flowchart.ErrNotFlowchart):
		return "only flowcharts can be drawn for now; write a mermaid block in the note instead"
	case errors.As(err, &pe):
		return toolError("mermaid: " + pe.Error())
	case errors.Is(err, flowchart.ErrImageType):
		return "only PNG, JPEG and GIF images can be added"
	}
	// UnknownIDsError, a node id naming a subgraph, and the like: their
	// text is already meant for the agent.
	return toolError(err.Error())
}

// editDrawing changes the drawing at abs under the lock browser saves
// take. base must be the hash of the file as it is now; change gets the
// scene JSON. Any error writes nothing. It returns the file as it is
// afterwards and whether it changed.
func (h *Handler) editDrawing(abs, base string, change func(scene []byte) ([]byte, error)) ([]byte, bool, error) {
	if base == "" {
		return nil, false, errNoBase
	}
	prev, next, err := drawing.Rewrite(abs, func(file []byte) ([]byte, error) {
		if drawing.Hash(file) != base {
			return nil, errStale
		}
		fm := frontmatter.Parse(file)
		_, scene, err := drawing.Parse(fm.Body)
		if err != nil {
			return nil, errUnreadable
		}
		out, err := change(scene)
		if err != nil {
			return nil, drawingError(err)
		}
		if bytes.Equal(out, scene) {
			return nil, nil
		}
		file = append(append([]byte{}, fm.Head...), drawing.Encode(out)...)
		if int64(len(file)) > h.Root.Limits().MaxNoteSize {
			return nil, toolError(fmt.Sprintf("the drawing would be over the note size limit (%d bytes)", h.Root.Limits().MaxNoteSize))
		}
		return file, nil
	})
	return next, err == nil && !bytes.Equal(prev, next), err
}

// drawingWritten indexes a drawing an agent wrote and records the author
// for git. The file watcher tells open editors.
func (h *Handler) drawingWritten(ctx context.Context, rel, author string) {
	if h.Scanner != nil {
		if err := h.Scanner.ScanOne(ctx, rel); err != nil && !scanner.IsDeferred(err) {
			h.Log.Warn("drawing is not indexed yet", "path", rel, "err", err)
		}
	}
	if h.Git != nil {
		h.Git.Author(rel, author)
	}
}

// writeFailed answers an editDrawing error: the agent's own refusals as
// they are, anything else (disk trouble) without its details.
func writeFailed(err error) *toolResult {
	var te toolError
	if errors.As(err, &te) {
		return errResult("%s", te)
	}
	return errResult("the drawing could not be written")
}

// embedText is what a note writes to show the drawing at rel: its name
// when that resolves to it from the space root, else its path.
func (h *Handler) embedText(ctx context.Context, space, rel string) string {
	name, _ := drawing.SplitExt(path.Base(rel))
	if r, err := h.DB.SpaceResolver(ctx, space); err == nil {
		target, err := h.DB.GetNoteByPath(ctx, rel)
		if res := r.Resolve(name, space+"/note.md"); err == nil && res.OK && res.ToID == target.ID {
			return "![[" + name + "]]"
		}
	}
	return "![[" + strings.TrimSuffix(strings.TrimPrefix(rel, space+"/"), ".md") + "]]"
}

func (h *Handler) toolDrawFlowchart(ctx context.Context, agent auth.AgentIdentity, author, space, p, mermaid, base string, remove []string) *toolResult {
	sp, res := h.spaceScope(agent, space)
	if res != nil {
		return res
	}
	if !scopeOf(agent).mayWrite(sp) {
		return errResult("this token cannot write to space %q", sp)
	}
	clean, res := h.withinSpace(sp, p)
	if res != nil {
		return res
	}
	if !drawing.Is(clean) {
		return errResult("the path must end in .excalidraw.md")
	}
	if scanner.IsAsset(clean) {
		return errResult("the path sits under _assets; drawings live outside it")
	}
	g, err := flowchart.Parse(mermaid)
	if err != nil {
		return errResult("%s", drawingError(err))
	}
	// Layout is about cubic and runs under the drawing lock.
	if len(g.Nodes) > 300 || len(g.Edges) > 1000 {
		return errResult("too large for a drawing (at most 300 nodes and 1000 edges); write a mermaid block instead")
	}
	if h.Limit != nil && !h.Limit.Allow(author) {
		return errResult("the agent write rate is exceeded; slow down and retry")
	}
	abs, _, err := h.Root.Resolve(clean)
	if err != nil {
		return errResult("that path is not allowed")
	}
	now := time.Now()
	stats := map[string]int{}
	var file []byte
	created, changed := false, true
	if _, err := os.Lstat(abs); errors.Is(err, fs.ErrNotExist) {
		if len(remove) > 0 {
			return errResult("remove is for an existing drawing; this path has none")
		}
		if file, res = h.createDrawing(abs, clean, g, now); res != nil {
			return res
		}
		created = true
		stats["nodes_added"], stats["edges_added"] = len(g.Nodes), len(g.Edges)
	} else {
		var st flowchart.Stats
		file, changed, err = h.editDrawing(abs, base, func(scene []byte) ([]byte, error) {
			out, s, err := flowchart.Apply(scene, g, remove, now)
			st = s
			return out, err
		})
		if err != nil {
			return writeFailed(err)
		}
		stats = map[string]int{
			"nodes_added": st.NodesAdded, "nodes_updated": st.NodesUpdated, "nodes_removed": st.NodesRemoved,
			"edges_added": st.EdgesAdded, "edges_updated": st.EdgesUpdated, "edges_removed": st.EdgesRemoved,
			"other_removed": st.OtherRemoved,
		}
	}
	if changed {
		h.drawingWritten(ctx, clean, author)
	}
	id := frontmatter.Parse(file).Meta.ID
	if n, err := h.DB.GetNoteByPath(ctx, clean); id == "" && err == nil {
		id = n.ID // a file without a frontmatter id yet
	}
	return textResult(map[string]any{
		"id": id, "path": clean, "created": created,
		"embed": h.embedText(ctx, sp, clean), "stats": stats, "base": drawing.Hash(file),
	})
}

// createDrawing writes a new drawing of g at abs, the way write_note
// creates a note: frontmatter id, exclusive create.
func (h *Handler) createDrawing(abs, rel string, g flowchart.Graph, now time.Time) ([]byte, *toolResult) {
	if err := h.Root.CheckCollision(rel); err != nil {
		return nil, errResult("a file with a name equal after case folding exists at that path")
	}
	scene, err := flowchart.New(g, now)
	if err != nil {
		return nil, errResult("the drawing could not be made")
	}
	file, _, _ := frontmatter.EnsureID(drawing.Encode(scene), scanner.NewID(now), now)
	if int64(len(file)) > h.Root.Limits().MaxNoteSize {
		return nil, errResult("the drawing would be over the note size limit (%d bytes)", h.Root.Limits().MaxNoteSize)
	}
	if err := fsutil.MkdirInherit(filepath.Dir(abs)); err != nil {
		return nil, errResult("the folder could not be created")
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, errResult("a file already exists at that path; read it and pass base")
		}
		return nil, errResult("the drawing file could not be created")
	}
	_, err = f.Write(file)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(abs)
		return nil, errResult("the drawing file could not be written")
	}
	return file, nil
}

func (h *Handler) toolAddDrawingImage(ctx context.Context, agent auth.AgentIdentity, author, ref, asset, data, near, base string, width float64) *toolResult {
	n, res := h.resolveNote(ctx, agent, "drawing", ref)
	if res != nil {
		return res
	}
	if !scopeOf(agent).mayWrite(n.Space) {
		return errResult("this token cannot write to space %q", n.Space)
	}
	if !isDrawing(n) {
		return errResult("that note is not a drawing")
	}
	if (asset == "") == (data == "") {
		return errResult("give one of asset or data")
	}
	limit := h.Root.Limits().MaxAssetSize
	var img []byte
	if asset != "" {
		clean, res := h.withinSpace(n.Space, asset)
		if res != nil {
			return res
		}
		abs, _, err := h.Root.Resolve(clean)
		if err != nil {
			return errResult("that path is not allowed")
		}
		fi, err := os.Stat(abs)
		if err != nil || !fi.Mode().IsRegular() {
			return errResult("no file at %s", clean)
		}
		if fi.Size() > limit {
			return errResult("file is over the asset size limit (%d bytes)", limit)
		}
		if img, err = os.ReadFile(abs); err != nil {
			return errResult("the file could not be read")
		}
	} else {
		var err error
		if img, err = decodeData(data); err != nil {
			return errResult("data is not valid base64")
		}
		if int64(len(img)) > limit {
			return errResult("file is over the asset size limit (%d bytes)", limit)
		}
	}
	if h.Limit != nil && !h.Limit.Allow(author) {
		return errResult("the agent write rate is exceeded; slow down and retry")
	}
	abs, _, err := h.Root.Resolve(n.RelPath)
	if err != nil {
		return errResult("that path is not allowed")
	}
	file, changed, err := h.editDrawing(abs, base, func(scene []byte) ([]byte, error) {
		return flowchart.AddImage(scene, img, near, width, time.Now())
	})
	if err != nil {
		return writeFailed(err)
	}
	if changed {
		h.drawingWritten(ctx, n.RelPath, author)
	}
	return textResult(map[string]any{"path": n.RelPath, "base": drawing.Hash(file)})
}
