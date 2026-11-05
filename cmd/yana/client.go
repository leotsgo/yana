// The command line's client side: sign in to a yana server once, then
// write to it from the shell.
//
//	yana login [server]   sign in; the session is kept in ~/.config/yana
//	yana logout           end the session
//	yana whoami           the server and account signed in
//	yana today [--space name] text...
//	                      append a line to today's note ("-" reads stdin)
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// session is what login keeps: the server and the account's tokens. The
// file is the account's key, so it is written readable by the user only.
type session struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Access   string `json:"access_token"`
	Refresh  string `json:"refresh_token"`
}

type tokens struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
}

func sessionPath() (string, error) {
	if p := os.Getenv("YANA_CLI_SESSION"); p != "" {
		return p, nil
	}
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "session.json")
	migrateSession(p)
	return p, nil
}

func loadSession() (*session, error) {
	p, err := sessionPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("not signed in; run yana login")
	}
	if err != nil {
		return nil, err
	}
	var s session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &s, nil
}

func (s *session) save() error {
	p, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(s, "", "  ")
	// Through a rename: the plugin runs several yana api at once, and one
	// refreshing the token must never leave another a half-written file.
	tmp := p + ".tmp" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// apiError is the server's {"error": "..."} answer.
type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("the server answered %d", e.Status)
	}
	return e.Msg
}

func call(server, method, path, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, strings.TrimRight(server, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &apiError{Status: resp.StatusCode, Msg: e.Error}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// do is call with the session's token, refreshing it once when it has
// expired and keeping the new pair.
func (s *session) do(method, path string, body, out any) error {
	err := call(s.Server, method, path, s.Access, body, out)
	var ae *apiError
	if !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized {
		return err
	}
	if err := s.refresh(); err != nil {
		return err
	}
	return call(s.Server, method, path, s.Access, body, out)
}

// refresh trades the refresh token for a new access token and keeps it.
func (s *session) refresh() error {
	var res struct {
		Tokens tokens `json:"tokens"`
	}
	if call(s.Server, http.MethodPost, "/api/auth/refresh", "", map[string]string{"refresh_token": s.Refresh}, &res) != nil {
		return errors.New("the session has ended; run yana login")
	}
	s.Access = res.Tokens.Access
	if res.Tokens.Refresh != "" {
		s.Refresh = res.Tokens.Refresh
	}
	return s.save()
}

func login(args []string) error {
	in := bufio.NewReader(os.Stdin)
	ask := func(prompt, def string) string {
		if def != "" {
			prompt += " [" + def + "]"
		}
		fmt.Fprint(os.Stderr, prompt+": ")
		line, _ := in.ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
		return def
	}
	var server string
	if len(args) > 0 {
		server = args[0]
	} else {
		def := ""
		if s, err := loadSession(); err == nil {
			def = s.Server
		}
		server = ask("Server", def)
	}
	if server == "" {
		return errors.New("a server address is needed, like https://notes.example.com")
	}
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	server = strings.TrimRight(server, "/")
	user := ask("Username", os.Getenv("USER"))
	fmt.Fprint(os.Stderr, "Password: ")
	var pw []byte
	var err error
	if term.IsTerminal(int(os.Stdin.Fd())) {
		pw, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
	} else {
		var line string
		line, err = in.ReadString('\n')
		pw = []byte(strings.TrimRight(line, "\r\n"))
		if errors.Is(err, io.EOF) && len(pw) > 0 {
			err = nil
		}
	}
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	var res struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		Tokens tokens `json:"tokens"`
	}
	body := map[string]string{"username": user, "password": string(pw), "label": "yana cli on " + host}
	if err := call(server, http.MethodPost, "/api/auth/login", "", body, &res); err != nil {
		return err
	}
	s := session{Server: server, Username: res.User.Username, Access: res.Tokens.Access, Refresh: res.Tokens.Refresh}
	if err := s.save(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Signed in to %s as %s.\n", server, s.Username)
	return nil
}

func logout() error {
	s, err := loadSession()
	if err != nil {
		return err
	}
	// The server forgets the session; the file goes either way.
	_ = call(s.Server, http.MethodPost, "/api/auth/logout", "", map[string]string{"refresh_token": s.Refresh}, nil)
	p, _ := sessionPath()
	if err := os.Remove(p); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Signed out.")
	return nil
}

func whoami() error {
	s, err := loadSession()
	if err != nil {
		return err
	}
	var res struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := s.do(http.MethodGet, "/api/auth/state", nil, &res); err != nil {
		return err
	}
	fmt.Printf("%s on %s\n", res.User.Username, s.Server)
	return nil
}

// dayMain is yana today, yesterday, tomorrow and yana YYYY-MM-DD. With
// text, or text on stdin, it appends to that day's note; with neither at
// a terminal it opens the note in neovim.
func dayMain(date time.Time, args []string) error {
	fs := flag.NewFlagSet("today", flag.ContinueOnError)
	space := fs.String("space", "", "the space whose daily note to use (default: the one set in Settings)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	text := strings.Join(fs.Args(), " ")
	if text == "" && term.IsTerminal(int(os.Stdin.Fd())) {
		s, err := loadSession()
		if err != nil {
			return err
		}
		note, err := dailyNote(s, *space, date)
		if err != nil {
			return err
		}
		return launch(s, launchOpts{Open: note.Path})
	}
	if text == "-" || text == "" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		text = string(raw)
	}
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return errors.New(`nothing to write: yana today "text"`)
	}
	s, err := loadSession()
	if err != nil {
		return err
	}
	// Each line a list item, the way Capture in the app writes it.
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") || strings.HasPrefix(l, "+ ") {
			lines[i] = "  " + l
		} else {
			lines[i] = "- " + l
		}
	}
	text = strings.Join(lines, "\n")

	note, err := dailyNote(s, *space, date)
	if err != nil {
		return err
	}
	if err := s.do(http.MethodPost, "/api/notes/"+note.ID+"/append", map[string]string{"content": text}, nil); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Added to %s.\n", note.Path)
	return nil
}

// dailyNote finds or makes the daily note for date, the way Today in the
// app does. It goes in space when given, else where the owner set daily
// notes to go (the daily folder, then the daily space, then the space for
// new notes), else in the account's first space.
func dailyNote(s *session, space string, date time.Time) (entry, error) {
	target := map[string]any{"space": space, "date": date.Format("2006-01-02")}
	if space == "" {
		var d struct {
			Space       string `json:"space"`
			DailySpace  string `json:"daily_space"`
			DailyFolder string `json:"daily_folder"`
		}
		if err := s.do(http.MethodGet, "/api/defaults", nil, &d); err != nil {
			return entry{}, err
		}
		switch sp, folder, _ := strings.Cut(d.DailyFolder, "/"); {
		case d.DailyFolder != "":
			target["space"], target["folder"] = sp, folder
		case d.DailySpace != "":
			target["space"] = d.DailySpace
		case d.Space != "":
			target["space"] = d.Space
		default:
			var list struct {
				Spaces []struct {
					Name string `json:"name"`
				} `json:"spaces"`
			}
			if err := s.do(http.MethodGet, "/api/spaces", nil, &list); err != nil {
				return entry{}, err
			}
			if len(list.Spaces) == 0 {
				return entry{}, errors.New("this account has no space to write to")
			}
			target["space"] = list.Spaces[0].Name
		}
	}
	var note entry
	if err := s.do(http.MethodPost, "/api/notes/daily", target, &note); err != nil {
		return entry{}, err
	}
	note.Name, note.Kind = filepath.Base(note.Path), "md"
	return note, nil
}
