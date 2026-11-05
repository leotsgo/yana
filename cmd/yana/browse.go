// The command line's TUI: an oil.nvim-style listing of the server's
// folders and notes, telescope-style pickers, a vim command line, and the
// editor for a note. Keys come from the config file (keymap.go).
//
//	yana | yana browse | yana b   (yana alone only when stdin is a terminal)
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/madeofpendletonwool/yana/internal/scanner"
)

func browseMain() error {
	s, err := loadSession()
	if err != nil {
		// Bare yana lands here too; someone who meant the server is told how.
		return fmt.Errorf("%w (yana serve runs the server)", err)
	}
	m := newModel(s)
	m.restoreView()
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		return err
	}
	m.saveView()
	if m.logout {
		return logout()
	}
	return nil
}

func newModel(s *session) *model {
	m := &model{s: s, cursors: map[string]int{}, texts: map[string]string{}, hist: loadHistory()}
	c, err := loadConfig()
	if err != nil {
		m.fail(err)
	}
	configuredEditor = c.Editor
	m.km, _ = c.keymap() // the defaults always parse; loadConfig fell back to them
	leader, _ := parseKeys(c.Leader, nil)
	m.leader = leader[0]
	return m
}

// --- pure helpers ---------------------------------------------------------------

// fuzzyScore matches query's characters in order inside s, ignoring case,
// and scores the best alignment: runs of consecutive characters and
// starts of words count more, and a plain substring, above all one in the
// file name, beats any scattered match.
func fuzzyScore(query, s string) (int, bool) {
	q := []rune(strings.ToLower(query))
	r := []rune(strings.ToLower(s))
	if len(q) == 0 {
		return 0, true
	}
	best, found := 0, false
	for start := range r {
		if r[start] != q[0] {
			continue
		}
		score, qi, prev := 0, 0, start-2
		for i := start; i < len(r) && qi < len(q); i++ {
			if r[i] != q[qi] {
				continue
			}
			score++
			if i == prev+1 {
				score += 5
			}
			if i == 0 || strings.ContainsRune("/-_ .", r[i-1]) {
				score += 3
			}
			prev, qi = i, qi+1
		}
		if qi == len(q) && (!found || score > best) {
			best, found = score, true
		}
	}
	if !found {
		return 0, false
	}
	ls, lq := strings.ToLower(s), string(q)
	if strings.Contains(ls, lq) {
		best += 20
		if strings.Contains(path.Base(ls), lq) {
			best += 10
		}
	}
	return best, true
}

// rankNotes is the entries matching query by path or title, best first;
// an empty query keeps them all in their order.
func rankNotes(query string, notes []entry) []entry {
	if strings.TrimSpace(query) == "" {
		return notes
	}
	type scored struct {
		e     entry
		score int
	}
	var hits []scored
	for _, e := range notes {
		sp, okp := fuzzyScore(query, e.Path)
		st, okt := fuzzyScore(query, e.Title)
		if !okp && !okt {
			continue
		}
		hits = append(hits, scored{e, max(sp, st)})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return len(hits[i].e.Path) < len(hits[j].e.Path)
	})
	out := make([]entry, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out
}

// pickItem is one picker row: a note, or a daily note that may not exist
// yet (date set), which opening makes.
type pickItem struct {
	e       entry
	snippet string
	date    time.Time
}

func (it pickItem) virtual() bool { return !it.date.IsZero() }

// dateFor reads a word as a day: today, yesterday and tomorrow (from
// their first three letters on) or YYYY-MM-DD.
func dateFor(word string, now time.Time) (time.Time, bool) {
	w := strings.ToLower(strings.TrimSpace(word))
	if len(w) >= 3 {
		for name, days := range map[string]int{"today": 0, "yesterday": -1, "tomorrow": 1} {
			if strings.HasPrefix(name, w) {
				return now.AddDate(0, 0, days), true
			}
		}
	}
	if d, err := time.ParseInLocation("2006-01-02", w, now.Location()); err == nil {
		return d, true
	}
	return time.Time{}, false
}

// pickNotes is the find-notes picker's results for query. A day name or
// date puts that daily note first; #tag keeps notes carrying a tag that
// starts so; a leading space/ keeps that space's notes; the rest of the
// query is fuzzy.
func pickNotes(query string, notes []entry, now time.Time) []pickItem {
	var items []pickItem
	if d, ok := dateFor(query, now); ok {
		day := d.Format("2006-01-02")
		items = append(items, pickItem{e: entry{Path: "daily note " + day, Name: day, Kind: "md"}, date: d})
	}
	var words []string
	keep := notes
	for i, w := range strings.Fields(query) {
		if strings.HasPrefix(w, "#") && len(w) > 1 {
			tag := strings.ToLower(w[1:])
			var next []entry
			for _, e := range keep {
				for _, t := range e.Tags {
					if strings.HasPrefix(strings.ToLower(t), tag) {
						next = append(next, e)
						break
					}
				}
			}
			keep = next
			continue
		}
		if sp, rest, ok := strings.Cut(w, "/"); i == 0 && ok && sp != "" {
			var next []entry
			for _, e := range keep {
				if strings.HasPrefix(e.Path, sp+"/") {
					next = append(next, e)
				}
			}
			if len(next) > 0 {
				keep = next
				if rest != "" {
					words = append(words, rest)
				}
				continue
			}
		}
		words = append(words, w)
	}
	for _, e := range rankNotes(strings.Join(words, " "), keep) {
		items = append(items, pickItem{e: e})
	}
	return items
}

var markRe = regexp.MustCompile(`<mark>(.*?)</mark>`)

// matchLine is the 1-based line of text a search hit points at: the first
// line holding a term the server marked in its snippet, else one of the
// query's words; 0 when none is found.
func matchLine(text, snippet, query string) int {
	var terms []string
	for _, m := range markRe.FindAllStringSubmatch(snippet, -1) {
		terms = append(terms, m[1])
	}
	terms = append(terms, strings.Fields(query)...)
	lines := strings.Split(strings.ToLower(text), "\n")
	for _, t := range terms {
		t = strings.ToLower(strings.Trim(t, `"*()`))
		if t == "" {
			continue
		}
		for i, l := range lines {
			if strings.Contains(l, t) {
				return i + 1
			}
		}
	}
	return 0
}

// commands are the : commands, in the order help lists them.
var commands = []struct{ name, args, help string }{
	{"e", "<path>", "edit a note; offers to make it when missing"},
	{"today", "", "today's daily note"},
	{"yesterday", "", "yesterday's daily note"},
	{"tomorrow", "", "tomorrow's daily note"},
	{"new", "<name>", "new note in this folder"},
	{"mkdir", "<name>", "new folder in this folder"},
	{"cd", "<folder>", "go to a folder (.. is up, / the top)"},
	{"rename", "<new>", "rename or move the item under the cursor"},
	{"delete", "", "delete the item under the cursor"},
	{"sf", "[query]", "find notes by name"},
	{"sg", "[query]", "full-text search"},
	{"recent", "", "recently opened notes"},
	{"preview", "", "toggle the preview pane"},
	{"help", "", "keys and commands"},
	{"map", "", "the active keymaps"},
	{"whoami", "", "the account and server"},
	{"logout", "", "sign out and quit"},
	{"q", "", "quit"},
	{"qa", "", "quit"},
}

// complete lists what a command line could become: command names while
// the first word is typed, then note paths for :e and folders for :cd.
func complete(line string, all []entry) []string {
	name, arg, hasArg := strings.Cut(line, " ")
	var out []string
	if !hasArg {
		for _, c := range commands {
			if strings.HasPrefix(c.name, name) {
				out = append(out, c.name)
			}
		}
		return out
	}
	wantDir := name == "cd"
	if !wantDir && name != "e" {
		return nil
	}
	var pool []entry
	for _, e := range all {
		if e.Dir == wantDir {
			pool = append(pool, e)
		}
	}
	for _, e := range rankNotes(strings.TrimSpace(arg), pool) {
		if len(out) == 50 {
			break
		}
		out = append(out, name+" "+e.Path)
	}
	return out
}

// clip cuts s to w columns.
// ponytail: counts runes, so wide characters overrun; measure cells if that bites.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(strings.ReplaceAll(s, "\t", "    "))
	if len(r) > w {
		r = r[:w]
	}
	return string(r)
}

// pad fills styled text out to w visible columns.
func pad(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// --- command history ---------------------------------------------------------------

const histMax = 200

func loadHistory() []string {
	p, err := statePath("history")
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

// view is where the browser was left on one server: the folder, the
// item under the cursor and whether the preview was open. The next yana
// opens there.
type view struct {
	Dir      string `json:"dir"`
	Selected string `json:"selected"`
	Preview  bool   `json:"preview"`
}

// loadViews reads the state dir's views, one per server.
func loadViews() map[string]view {
	views := map[string]view{}
	if p, err := statePath("views.json"); err == nil {
		if raw, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(raw, &views)
		}
	}
	return views
}

func (m *model) restoreView() {
	v, ok := loadViews()[m.s.Server]
	if !ok {
		return
	}
	m.dir, m.focus, m.preview = v.Dir, v.Selected, v.Preview
}

func (m *model) saveView() {
	if m.logout {
		return
	}
	views := loadViews()
	v := view{Dir: m.dir, Preview: m.preview}
	if e, ok := m.selected(); ok {
		v.Selected = e.Path
	}
	views[m.s.Server] = v
	p, err := statePath("views.json")
	if err != nil {
		return
	}
	raw, _ := json.MarshalIndent(views, "", "  ")
	_ = os.WriteFile(p, raw, 0o600)
}

func (m *model) remember(line string) {
	if line == "" || (len(m.hist) > 0 && m.hist[len(m.hist)-1] == line) {
		return
	}
	m.hist = append(m.hist, line)
	if len(m.hist) > histMax {
		m.hist = m.hist[len(m.hist)-histMax:]
	}
	if p, err := statePath("history"); err == nil {
		_ = os.WriteFile(p, []byte(strings.Join(m.hist, "\n")+"\n"), 0o600)
	}
}

// --- the model ---------------------------------------------------------------------

type mode int

const (
	modeNormal mode = iota
	modePrompt
	modeConfirm
	modePicker
	modeHelp
)

type model struct {
	s       *session
	km      map[string]string
	leader  string
	all     []entry
	dir     string
	cursors map[string]int // per folder, so coming back lands where it was
	focus   string         // a path to put the cursor on after the next reload
	width   int
	height  int
	pending []string
	mode    mode
	prompt  string // "/", ":", "new" or "move" while mode is modePrompt
	input   string
	wild    []string // the : line's completion menu
	wildIdx int      // -1 while nothing in it is picked
	hist    []string
	histIdx int
	target  entry // what a move or delete acts on
	ask     string
	onYes   func() tea.Cmd
	pattern string
	msg     string
	msgErr  bool
	preview bool
	texts   map[string]string // note id → preview text
	user    string
	pk      *picker
	overlay []string // help or :map
	scroll  int
	logout  bool
}

// picker is one telescope-style finder.
type picker struct {
	kind   string // "sf" notes by name, "sg" full text, "recent"
	query  string
	items  []pickItem
	cursor int
	seq    int // bumped per keystroke; a search answers only its own
	busy   bool
}

type (
	treeMsg struct {
		all []entry
		err error
	}
	userMsg  struct{ name string }
	textMsg  struct{ id, text string }
	waitMsg  struct{ seq int }
	foundMsg struct {
		seq   int
		items []pickItem
		err   error
	}
	editReadyMsg struct {
		es  *editSession
		cmd *exec.Cmd
		err error
	}
	editDoneMsg struct {
		path  string
		saves int
		err   error
	}
	actionMsg struct {
		msg   string
		err   error
		focus string
		open  *entry
	}
)

func (m *model) Init() tea.Cmd { return tea.Batch(m.loadTree(), m.loadUser()) }

func (m *model) loadTree() tea.Cmd {
	return func() tea.Msg {
		all, err := fetchTree(m.s)
		return treeMsg{all, err}
	}
}

func (m *model) loadUser() tea.Cmd {
	return func() tea.Msg {
		var res struct {
			User struct {
				Username string `json:"username"`
			} `json:"user"`
		}
		if m.s.do(http.MethodGet, "/api/auth/state", nil, &res) != nil {
			return userMsg{m.s.Username}
		}
		return userMsg{res.User.Username}
	}
}

func (m *model) listing() []entry { return listDir(m.all, m.dir) }

func (m *model) cur() int { return m.cursors[m.dir] }

func (m *model) setCur(i int) {
	n := len(m.listing())
	if i >= n {
		i = n - 1
	}
	if i < 0 {
		i = 0
	}
	m.cursors[m.dir] = i
}

func (m *model) selected() (entry, bool) {
	l := m.listing()
	if i := m.cur(); i < len(l) {
		return l[i], true
	}
	return entry{}, false
}

func (m *model) focusOn(p string) {
	for i, e := range m.listing() {
		if e.Path == p {
			m.cursors[m.dir] = i
		}
	}
}

func (m *model) say(format string, a ...any) { m.msg, m.msgErr = fmt.Sprintf(format, a...), false }

func (m *model) fail(err error) { m.msg, m.msgErr = err.Error(), true }

func (m *model) confirm(question string, yes func() tea.Cmd) {
	m.mode, m.ask, m.onYes = modeConfirm, question, yes
}

// wantText fetches the preview of e unless it is cached.
func (m *model) wantText(e entry) tea.Cmd {
	if e.Dir || e.ID == "" {
		return nil
	}
	if _, ok := m.texts[e.ID]; ok {
		return nil
	}
	m.texts[e.ID] = "loading…"
	s := m.s
	return func() tea.Msg {
		switch e.Kind {
		case "md":
			t, err := fetchText(s, e.ID)
			if err != nil {
				return textMsg{e.ID, "(" + err.Error() + ")"}
			}
			return textMsg{e.ID, t.Text}
		case "html":
			var n struct {
				Source string `json:"source"`
			}
			if err := s.do(http.MethodGet, "/api/notes/"+url.PathEscape(e.ID), nil, &n); err != nil {
				return textMsg{e.ID, "(" + err.Error() + ")"}
			}
			return textMsg{e.ID, n.Source}
		}
		return textMsg{e.ID, "(a " + e.Kind + "; Enter opens it in the browser)"}
	}
}

func (m *model) previewCmd() tea.Cmd {
	if !m.preview {
		return nil
	}
	if e, ok := m.selected(); ok {
		return m.wantText(e)
	}
	return nil
}

// open edits a note in the editor, or shows a folder.
func (m *model) open(e entry, at func(string) int) tea.Cmd {
	if e.Dir {
		m.dir = e.Path
		m.setCur(m.cur())
		return m.previewCmd()
	}
	if e.Kind == "drawing" {
		u := noteURL(m.s, e.ID)
		m.say("drawings open in the app: %s", u)
		return func() tea.Msg {
			opener := "xdg-open"
			if runtime.GOOS == "darwin" {
				opener = "open"
			}
			_ = exec.Command(opener, u).Start()
			return nil
		}
	}
	m.say("opening %s…", e.Path)
	s := m.s
	return func() tea.Msg {
		es, cmd, err := startEdit(s, e, at)
		return editReadyMsg{es, cmd, err}
	}
}

// openDaily finds or makes the daily note for date and opens it.
func (m *model) openDaily(date time.Time) tea.Cmd {
	s := m.s
	return func() tea.Msg {
		e, err := dailyNote(s, "", date)
		if err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{msg: e.Path, open: &e}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case treeMsg:
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		m.all = msg.all
		if m.dir != "" && !slices.ContainsFunc(m.all, func(e entry) bool { return e.Dir && e.Path == m.dir }) {
			m.dir = "" // the folder left last time is gone
		}
		if m.focus != "" {
			m.focusOn(m.focus)
			m.focus = ""
		}
		m.setCur(m.cur())
		return m, m.previewCmd()
	case userMsg:
		m.user = msg.name
	case textMsg:
		m.texts[msg.id] = msg.text
	case waitMsg:
		if m.pk != nil && msg.seq == m.pk.seq {
			return m, m.search()
		}
	case foundMsg:
		if m.pk != nil && msg.seq == m.pk.seq {
			m.pk.busy = false
			if msg.err != nil {
				m.fail(msg.err)
			}
			m.pk.items, m.pk.cursor = msg.items, 0
			return m, m.pickPreview()
		}
	case editReadyMsg:
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		es := msg.es
		noteOpened(es.note.ID)
		m.msg = ""
		if es.readOnly {
			m.say("%s is read-only for you; nothing typed there is saved", es.note.Path)
		}
		return m, tea.ExecProcess(msg.cmd, func(runErr error) tea.Msg {
			saves, err := es.finish()
			if err == nil && runErr != nil {
				err = fmt.Errorf("the editor: %w", runErr)
			}
			return editDoneMsg{es.note.Path, saves, err}
		})
	case editDoneMsg:
		m.texts = map[string]string{}
		switch {
		case msg.err != nil:
			m.fail(msg.err)
		case msg.saves > 0:
			m.say("saved %s", msg.path)
		}
		return m, m.loadTree()
	case actionMsg:
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		m.say("%s", msg.msg)
		m.focus = msg.focus
		cmds := []tea.Cmd{m.loadTree()}
		if msg.open != nil {
			// The listing follows, so closing the editor lands beside the note.
			m.dir, m.focus = parentOf(msg.open.Path), msg.open.Path
			cmds = append(cmds, m.open(*msg.open, nil))
		}
		return m, tea.Batch(cmds...)
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		// Keys typed faster than the terminal is read arrive as one run
		// (":q", " sf"); each is its own key to a sequence or a mode change.
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 {
			var cmds []tea.Cmd
			for _, r := range msg.Runes {
				_, c := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				cmds = append(cmds, c)
			}
			return m, tea.Batch(cmds...)
		}
		switch m.mode {
		case modePicker:
			return m.pickerKey(msg)
		case modePrompt:
			return m.promptKey(msg)
		case modeConfirm:
			m.mode = modeNormal
			if k := msg.String(); k == "y" || k == "Y" {
				return m, m.onYes()
			}
			m.say("nothing done")
			return m, nil
		case modeHelp:
			switch msg.String() {
			case "j", "down", "ctrl+d":
				m.scroll = min(m.scroll+1, max(0, len(m.overlay)-(m.height-2)))
			case "k", "up", "ctrl+u":
				m.scroll = max(0, m.scroll-1)
			default:
				m.mode = modeNormal
			}
			return m, nil
		}
		return m.normalKey(msg.String())
	}
	return m, nil
}

func (m *model) normalKey(key string) (tea.Model, tea.Cmd) {
	if key == "esc" {
		m.pending, m.msg = nil, ""
		return m, nil
	}
	action, rest := matchSeq(m.km, m.pending, key)
	m.pending = rest
	if action == "" {
		return m, nil
	}
	return m, m.do(action)
}

// do runs one bindable action (see actions in keymap.go).
func (m *model) do(action string) tea.Cmd {
	half := max(1, (m.height-2)/2)
	switch action {
	case "down":
		m.setCur(m.cur() + 1)
	case "up":
		m.setCur(m.cur() - 1)
	case "top":
		m.setCur(0)
	case "bottom":
		m.setCur(len(m.listing()) - 1)
	case "half_down":
		m.setCur(m.cur() + half)
	case "half_up":
		m.setCur(m.cur() - half)
	case "open":
		if e, ok := m.selected(); ok {
			return m.open(e, nil)
		}
	case "parent":
		if m.dir != "" {
			from := m.dir
			m.dir = parentOf(m.dir)
			if _, seen := m.cursors[m.dir]; !seen {
				m.focusOn(from)
			}
		}
	case "root":
		m.dir = ""
	case "search":
		m.startPrompt("/", "")
	case "search_next", "search_prev":
		if m.pattern == "" {
			m.fail(errors.New("no previous search"))
		} else if !m.findNext(m.pattern, action == "search_next") {
			m.fail(fmt.Errorf("pattern not found: %s", m.pattern))
		}
	case "new":
		m.startPrompt("new", "")
	case "rename":
		if e, ok := m.selected(); ok {
			m.target = e
			m.startPrompt("move", e.Path)
		}
	case "delete":
		if e, ok := m.selected(); ok {
			m.confirm("Delete "+e.Path+"? (y/n)", func() tea.Cmd { return m.remove(e) })
		}
	case "preview":
		m.preview = !m.preview
	case "help":
		m.showOverlay(m.helpLines())
	case "quit":
		return tea.Quit
	case "command":
		m.startPrompt(":", "")
	case "find_notes":
		return m.openPicker("sf", "")
	case "grep":
		return m.openPicker("sg", "")
	case "recent":
		return m.openPicker("recent", "")
	}
	return m.previewCmd()
}

func (m *model) startPrompt(kind, input string) {
	m.mode, m.prompt, m.input = modePrompt, kind, input
	m.wild, m.wildIdx, m.histIdx = nil, -1, len(m.hist)
}

func (m *model) showOverlay(lines []string) {
	m.mode, m.overlay, m.scroll = modeHelp, lines, 0
}

func (m *model) helpLines() []string {
	out := []string{" Keys (yana config prints the file to change them)", ""}
	out = append(out, bindings(m.km, m.leader)...)
	out = append(out, "", " Commands (after :, Tab completes)", "")
	for _, c := range commands {
		out = append(out, fmt.Sprintf(" :%-18s %s", strings.TrimSpace(c.name+" "+c.args), c.help))
	}
	return append(out, "",
		" In a picker: type to filter, ↓ ↑ <C-n> <C-p> <C-j> <C-k> move, <CR> opens, <Esc> closes.",
		" Find notes takes today, yesterday, tomorrow or a date, #tag, and a leading space/.",
		" A note opens in your editor; every :w is saved to the server and merged",
		" with whatever changed there meanwhile. j k scroll this; any other key closes it.")
}

// findNext moves to the next (or previous) row whose name holds pattern,
// wrapping round.
func (m *model) findNext(pattern string, forward bool) bool {
	l := m.listing()
	p := strings.ToLower(pattern)
	for step := 1; step <= len(l); step++ {
		i := m.cur() + step
		if !forward {
			i = m.cur() - step
		}
		i = ((i % len(l)) + len(l)) % len(l)
		if strings.Contains(strings.ToLower(l[i].Name), p) {
			m.cursors[m.dir] = i
			return true
		}
	}
	return false
}

// typed applies an editing key to a line of input and reports whether
// the key was one.
func typed(line *string, msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes:
		*line += string(msg.Runes)
	case tea.KeySpace:
		*line += " "
	case tea.KeyBackspace, tea.KeyCtrlH:
		if r := []rune(*line); len(r) > 0 {
			*line = string(r[:len(r)-1])
		}
	case tea.KeyCtrlU:
		*line = ""
	default:
		return false
	}
	return true
}

func (m *model) promptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "enter":
		m.mode = modeNormal
		return m, m.submit()
	}
	if m.prompt == ":" {
		switch msg.String() {
		case "tab", "ctrl+n", "shift+tab", "ctrl+p":
			if m.wild == nil {
				m.wild = complete(m.input, m.all)
			}
			if n := len(m.wild); n > 0 {
				step := 1
				if k := msg.String(); k == "shift+tab" || k == "ctrl+p" {
					step = -1
				}
				m.wildIdx = ((m.wildIdx+step)%n + n) % n
				if m.wildIdx < 0 {
					m.wildIdx = n - 1
				}
				m.input = m.wild[m.wildIdx]
			}
			return m, nil
		case "up", "down":
			if msg.String() == "up" && m.histIdx > 0 {
				m.histIdx--
			} else if msg.String() == "down" && m.histIdx < len(m.hist) {
				m.histIdx++
			}
			m.input = ""
			if m.histIdx < len(m.hist) {
				m.input = m.hist[m.histIdx]
			}
			m.wild = nil
			return m, nil
		}
	}
	if msg.Type == tea.KeyBackspace && m.input == "" {
		m.mode = modeNormal // as in vim, backspace on an empty line leaves it
		return m, nil
	}
	if !typed(&m.input, msg) {
		return m, nil
	}
	switch m.prompt {
	case ":":
		m.wild, m.wildIdx = nil, -1
		if m.input != "" {
			m.wild = complete(m.input, m.all)
		}
	case "/":
		if m.input != "" {
			// Incremental, from the top, the way incsearch lands the cursor.
			m.cursors[m.dir] = len(m.listing()) - 1
			m.findNext(m.input, true)
		}
	}
	return m, m.previewCmd()
}

// submit runs what the prompt line asked for.
func (m *model) submit() tea.Cmd {
	in := strings.TrimSpace(m.input)
	switch m.prompt {
	case "/":
		if in != "" {
			m.pattern = in
		}
		return m.previewCmd()
	case ":":
		m.remember(in)
		return m.runCommand(in)
	case "new":
		if in == "" {
			return nil
		}
		return m.create(path.Join(m.dir, in) + trailingSlash(in))
	case "move":
		if in == "" || in == m.target.Path {
			return nil
		}
		return m.move(m.target, strings.Trim(in, "/"))
	}
	return nil
}

func trailingSlash(s string) string {
	if strings.HasSuffix(s, "/") {
		return "/"
	}
	return ""
}

// here resolves a name typed on the command line: a path from the top
// when it has a slash, else a name in the folder shown.
func (m *model) here(arg string) string {
	if strings.Contains(strings.Trim(arg, "/"), "/") {
		return strings.Trim(arg, "/")
	}
	return path.Join(m.dir, strings.Trim(arg, "/"))
}

func (m *model) runCommand(line string) tea.Cmd {
	name, arg, _ := strings.Cut(line, " ")
	arg = strings.TrimSpace(arg)
	needArg := func() bool {
		if arg == "" {
			m.fail(fmt.Errorf(":%s needs an argument", name))
			return false
		}
		return true
	}
	switch name {
	case "":
		return nil
	case "q", "qa", "q!", "qa!", "quit", "wq", "x":
		return tea.Quit
	case "e", "edit":
		if !needArg() {
			return nil
		}
		for _, p := range []string{arg, path.Join(m.dir, arg)} {
			if e, ok := resolveNote(m.all, p); ok {
				m.dir = parentOf(e.Path)
				m.focusOn(e.Path)
				return m.open(e, nil)
			}
		}
		p := m.here(arg)
		if scanner.KindOf(p) == "" {
			p += ".md"
		}
		m.confirm(p+" does not exist; make it? (y/n)", func() tea.Cmd { return m.create(p) })
	case "today", "yesterday", "tomorrow":
		d, _ := dateFor(name, time.Now())
		return m.openDaily(d)
	case "new":
		if needArg() {
			return m.create(path.Join(m.dir, arg) + trailingSlash(arg))
		}
	case "mkdir":
		if needArg() {
			return m.create(path.Join(m.dir, arg) + "/")
		}
	case "cd":
		switch arg {
		case "", "/":
			m.dir = ""
			return nil
		case "..":
			m.dir = parentOf(m.dir)
			return nil
		}
		for _, p := range []string{strings.Trim(arg, "/"), path.Join(m.dir, arg)} {
			for _, e := range m.all {
				if e.Dir && e.Path == p {
					m.dir = p
					return m.previewCmd()
				}
			}
		}
		m.fail(fmt.Errorf("no folder %s", arg))
	case "rename":
		if e, ok := m.selected(); ok && needArg() {
			to := arg
			if !strings.Contains(arg, "/") {
				to = path.Join(parentOf(e.Path), arg)
			}
			return m.move(e, to)
		}
	case "delete":
		return m.do("delete")
	case "sf", "sg":
		return m.openPicker(name, arg)
	case "recent":
		return m.openPicker("recent", arg)
	case "preview":
		return m.do("preview")
	case "help":
		m.showOverlay(m.helpLines())
	case "map":
		m.showOverlay(append([]string{" Keymaps", ""}, bindings(m.km, m.leader)...))
	case "whoami":
		m.say("%s on %s", m.user, m.s.Server)
	case "logout":
		m.logout = true
		return tea.Quit
	default:
		m.fail(fmt.Errorf("not a command: %s", name))
	}
	return nil
}

// create makes the note at p, or the folder when p ends in a slash; a
// folder at the top is a new space. A new note opens in the editor.
func (m *model) create(p string) tea.Cmd {
	s := m.s
	return func() tea.Msg {
		if strings.HasSuffix(p, "/") {
			p = strings.Trim(p, "/")
			var err error
			if !strings.Contains(p, "/") {
				err = s.do(http.MethodPost, "/api/spaces", map[string]string{"name": p}, nil)
			} else {
				err = s.do(http.MethodPost, "/api/dirs", map[string]string{"path": p}, nil)
			}
			return actionMsg{msg: "made " + p + "/", err: err, focus: p}
		}
		if scanner.KindOf(p) == "" {
			p += ".md"
		}
		var res struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		}
		if err := s.do(http.MethodPost, "/api/notes", map[string]string{"path": p}, &res); err != nil {
			return actionMsg{err: err}
		}
		e := entry{Path: res.Path, Name: path.Base(res.Path), ID: res.ID, Kind: scanner.KindOf(res.Path)}
		return actionMsg{msg: "made " + res.Path, open: &e}
	}
}

func (m *model) move(e entry, to string) tea.Cmd {
	s := m.s
	return func() tea.Msg {
		var err error
		switch {
		case e.Dir && !strings.Contains(e.Path, "/"):
			err = errors.New("spaces are renamed in the app's settings")
		case e.Dir:
			err = s.do(http.MethodPost, "/api/dirs/move", map[string]string{"path": e.Path, "to": to}, nil)
		default:
			if scanner.KindOf(to) == "" {
				to += path.Ext(e.Path)
			}
			err = s.do(http.MethodPost, "/api/notes/"+url.PathEscape(e.ID)+"/move", map[string]string{"path": to}, nil)
		}
		return actionMsg{msg: "moved to " + to, err: err, focus: to}
	}
}

func (m *model) remove(e entry) tea.Cmd {
	s := m.s
	return func() tea.Msg {
		var err error
		switch {
		case e.Dir && !strings.Contains(e.Path, "/"):
			err = errors.New("spaces are deleted in the app's settings")
		case e.Dir:
			err = s.do(http.MethodDelete, "/api/dirs?path="+url.QueryEscape(e.Path), nil, nil)
		default:
			err = s.do(http.MethodDelete, "/api/notes/"+url.PathEscape(e.ID), nil, nil)
		}
		return actionMsg{msg: e.Path + " is in the trash", err: err}
	}
}

// --- pickers ---------------------------------------------------------------------------

func (m *model) notes() []entry {
	var out []entry
	for _, e := range m.all {
		if !e.Dir {
			out = append(out, e)
		}
	}
	return out
}

func (m *model) openPicker(kind, query string) tea.Cmd {
	m.pk, m.mode = &picker{kind: kind, query: query}, modePicker
	return m.refilter()
}

// refilter recomputes the picker's results for its query. Full-text
// search goes to the server after a pause in the typing instead.
func (m *model) refilter() tea.Cmd {
	pk := m.pk
	pk.cursor = 0
	switch pk.kind {
	case "sf":
		pk.items = pickNotes(pk.query, m.notes(), time.Now())
	case "recent":
		byID := map[string]entry{}
		for _, e := range m.notes() {
			byID[e.ID] = e
		}
		var recent []entry
		for _, id := range loadRecent() {
			if e, ok := byID[id]; ok {
				recent = append(recent, e)
			}
		}
		pk.items = nil
		for _, e := range rankNotes(pk.query, recent) {
			pk.items = append(pk.items, pickItem{e: e})
		}
	case "sg":
		pk.seq++
		if len(strings.TrimSpace(pk.query)) < 2 {
			pk.items, pk.busy = nil, false
			return nil
		}
		pk.busy = true
		seq := pk.seq
		return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return waitMsg{seq} })
	}
	return m.pickPreview()
}

func (m *model) search() tea.Cmd {
	s, q, seq := m.s, m.pk.query, m.pk.seq
	return func() tea.Msg {
		var res struct {
			Hits []struct {
				Note struct {
					ID    string `json:"id"`
					Path  string `json:"path"`
					Title string `json:"title"`
					Kind  string `json:"kind"`
				} `json:"note"`
				Snippet string `json:"snippet"`
			} `json:"hits"`
		}
		err := s.do(http.MethodGet, "/api/search?limit=50&q="+url.QueryEscape(q), nil, &res)
		items := make([]pickItem, 0, len(res.Hits))
		for _, h := range res.Hits {
			n := h.Note
			items = append(items, pickItem{e: entry{Path: n.Path, Name: path.Base(n.Path), ID: n.ID, Title: n.Title, Kind: n.Kind}, snippet: h.Snippet})
		}
		return foundMsg{seq, items, err}
	}
}

func (m *model) pickPreview() tea.Cmd {
	if it, ok := m.pk.selected(); ok && !it.virtual() {
		return m.wantText(it.e)
	}
	return nil
}

func (pk *picker) selected() (pickItem, bool) {
	if pk.cursor < len(pk.items) {
		return pk.items[pk.cursor], true
	}
	return pickItem{}, false
}

func (m *model) pickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pk := m.pk
	switch msg.String() {
	case "esc":
		m.mode, m.pk = modeNormal, nil
		return m, nil
	case "enter":
		it, ok := pk.selected()
		if !ok {
			return m, nil
		}
		m.mode, m.pk = modeNormal, nil
		if it.virtual() {
			return m, m.openDaily(it.date)
		}
		var at func(string) int
		if pk.kind == "sg" {
			at = func(text string) int { return matchLine(text, it.snippet, pk.query) }
		}
		m.dir = parentOf(it.e.Path)
		m.focusOn(it.e.Path)
		return m, m.open(it.e, at)
	case "up", "ctrl+p", "ctrl+k":
		pk.cursor = max(0, pk.cursor-1)
		return m, m.pickPreview()
	case "down", "ctrl+n", "ctrl+j":
		pk.cursor = max(0, min(len(pk.items)-1, pk.cursor+1))
		return m, m.pickPreview()
	}
	if typed(&pk.query, msg) {
		return m, m.refilter()
	}
	return m, nil
}

// --- view --------------------------------------------------------------------------------

var (
	dirStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "75"})
	selBg     = lipgloss.AdaptiveColor{Light: "254", Dark: "238"}
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "243"})
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"})
	hitStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "166", Dark: "214"})
	barStyle  = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "252", Dark: "236"}).Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "252"})
	modeStyle = lipgloss.NewStyle().Bold(true).Background(lipgloss.AdaptiveColor{Light: "25", Dark: "75"}).Foreground(lipgloss.AdaptiveColor{Light: "255", Dark: "234"})
)

// box draws lines inside a rounded frame w wide and h tall, title on top.
func box(title string, lines []string, w, h int) []string {
	if w < 6 || h < 2 {
		return nil
	}
	top := "╭─ " + clip(title, w-6) + " "
	top += strings.Repeat("─", max(0, w-1-lipgloss.Width(top))) + "╮"
	out := []string{dimStyle.Render(top)}
	side := dimStyle.Render("│")
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, side+pad(l, w-2)+side)
	}
	return append(out, dimStyle.Render("╰"+strings.Repeat("─", w-2)+"╯"))
}

// row renders one listing row w wide.
func row(e entry, w int, sel bool) string {
	st := lipgloss.NewStyle()
	name := e.Name
	if e.Dir {
		st, name = dirStyle, name+"/"
	} else if e.Kind != "md" {
		name += "  [" + e.Kind + "]"
	}
	if sel {
		st = st.Background(selBg)
	}
	return st.Render(pad(clip(" "+name, w), w))
}

// textLines is a note's text cut to a pane, with line mark (1-based)
// picked out and scrolled into view.
func textLines(text string, w, h, mark int) []string {
	lines := strings.Split(text, "\n")
	top := 0
	if mark > h/3 {
		top = mark - 1 - h/3
	}
	var out []string
	for i := top; i < len(lines) && len(out) < h; i++ {
		l := clip(lines[i], w)
		if i+1 == mark {
			l = hitStyle.Render(l)
		}
		out = append(out, l)
	}
	return out
}

func (m *model) previewOf(e entry, w, h, mark int) []string {
	if e.Dir {
		var out []string
		for _, c := range listDir(m.all, e.Path) {
			out = append(out, row(c, w, false))
		}
		return out
	}
	return textLines(m.texts[e.ID], w, h, mark)
}

// beside joins two columns line by line, the left padded to lw.
func beside(left, right []string, lw, h int) []string {
	out := make([]string, h)
	for i := range out {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = pad(l, lw) + r
	}
	return out
}

func (m *model) View() string {
	if m.width == 0 {
		return ""
	}
	h := m.height - 2
	var body []string
	switch m.mode {
	case modePicker:
		body = m.pickerView(h)
	case modeHelp:
		body = m.overlay[min(m.scroll, len(m.overlay)):]
	default:
		body = m.listView(h)
	}
	for len(body) < h {
		body = append(body, "")
	}
	body = body[:h]
	if m.mode == modePrompt && m.prompt == ":" && len(m.wild) > 0 {
		m.wildMenu(body)
	}
	return strings.Join(append(body, m.statusLine(), m.cmdLine()), "\n")
}

// wildMenu draws the command line's completions over the bottom of body,
// like nvim's popup menu.
func (m *model) wildMenu(body []string) {
	rows := min(len(m.wild), 10, len(body))
	first := 0
	if m.wildIdx >= rows {
		first = m.wildIdx - rows + 1
	}
	w := 0
	for _, c := range m.wild[first : first+rows] {
		w = max(w, len([]rune(c)))
	}
	w = min(w+2, m.width)
	for i := 0; i < rows; i++ {
		st := barStyle
		if first+i == m.wildIdx {
			st = modeStyle
		}
		body[len(body)-rows+i] = st.Render(pad(clip(" "+m.wild[first+i], w), w))
	}
}

func (m *model) listView(h int) []string {
	l := m.listing()
	lw := m.width
	split := m.preview && m.width >= 50
	if split {
		lw = max(24, m.width*2/5)
	}
	off := max(0, m.cur()-h+1)
	var left []string
	for i := off; i < len(l) && len(left) < h; i++ {
		left = append(left, row(l[i], lw, i == m.cur()))
	}
	if len(l) == 0 {
		left = append(left, dimStyle.Render(" (empty; o makes a note)"))
	}
	if !split {
		return left
	}
	var right []string
	if e, ok := m.selected(); ok {
		right = box(e.Path, m.previewOf(e, m.width-lw-3, h-2, 0), m.width-lw-1, h)
	}
	return beside(left, right, lw+1, h)
}

func (m *model) pickerView(h int) []string {
	pk := m.pk
	title := map[string]string{"sf": "Find notes", "sg": "Live grep", "recent": "Recent notes"}[pk.kind]
	lw := m.width
	wide := m.width >= 80
	if wide {
		lw = m.width / 2
	}
	count := fmt.Sprintf("%d", len(pk.items))
	if pk.busy {
		count = "…"
	}
	prompt := box(title, []string{pad("> "+clip(pk.query, lw-8-len(count))+"█", lw-3-len(count)) + dimStyle.Render(count)}, lw, 3)
	rh := h - 3
	off := max(0, pk.cursor-(rh-2)+1)
	var rows []string
	for i := off; i < len(pk.items) && len(rows) < rh-2; i++ {
		it := pk.items[i]
		text := " " + it.e.Path
		if it.virtual() {
			text = " ◆ " + it.e.Path + " (made when opened if missing)"
		}
		if it.snippet != "" {
			text += "  " + strings.Join(strings.Fields(markRe.ReplaceAllString(it.snippet, "$1")), " ")
		}
		st := lipgloss.NewStyle()
		if it.virtual() {
			st = hitStyle
		}
		if i == pk.cursor {
			st = st.Background(selBg).Bold(true)
		}
		rows = append(rows, st.Render(pad(clip(text, lw-2), lw-2)))
	}
	left := append(prompt, box("Results", rows, lw, rh)...)
	if !wide {
		return left
	}
	right := box("Preview", nil, m.width-lw, h)
	if it, ok := pk.selected(); ok && !it.virtual() {
		mark := 0
		if pk.kind == "sg" {
			mark = matchLine(m.texts[it.e.ID], it.snippet, pk.query)
		}
		right = box(it.e.Path, m.previewOf(it.e, m.width-lw-2, h-2, mark), m.width-lw, h)
	}
	return beside(left, right, lw, h)
}

func (m *model) statusLine() string {
	modeName := map[mode]string{modeNormal: " NORMAL ", modePrompt: " COMMAND ", modeConfirm: " CONFIRM ", modePicker: " PICKER ", modeHelp: " HELP "}[m.mode]
	where := strings.TrimPrefix(strings.TrimPrefix(m.s.Server, "https://"), "http://") + "  /" + m.dir
	if m.dir != "" {
		where += "/"
	}
	pending := ""
	if len(m.pending) > 0 {
		pending = showKeys(strings.Join(m.pending, seqSep), m.leader) + "  "
	}
	left := modeStyle.Render(modeName) + barStyle.Render(" "+where+" ")
	right := barStyle.Render(" " + pending + m.user + " ")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		return clip(where, m.width)
	}
	return left + barStyle.Render(strings.Repeat(" ", gap)) + right
}

func (m *model) cmdLine() string {
	switch m.mode {
	case modePrompt:
		label := map[string]string{"/": "/", ":": ":", "new": "New (end with / for a folder): ", "move": "Move to: "}[m.prompt]
		return clip(label+m.input, m.width-1) + "█"
	case modeConfirm:
		return clip(m.ask, m.width)
	}
	if m.msgErr {
		return errStyle.Render(clip(m.msg, m.width))
	}
	return clip(m.msg, m.width)
}
