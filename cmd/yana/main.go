// Command yana runs the notes server, and is its client from a shell:
// at a terminal, yana is neovim on the signed-in server (nvim.go). See
// usage below for every command.
//
// Configuration comes from YANA_* environment variables and an optional
// YAML file named by YANA_CONFIG. See docs/deployment.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/madeofpendletonwool/yana/internal/auth"
	"github.com/madeofpendletonwool/yana/internal/config"
	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/mcp"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/rt"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/search"
	"github.com/madeofpendletonwool/yana/internal/server"
	"github.com/madeofpendletonwool/yana/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// commands are the commands yana takes, in the order usage lists them;
// shell completion offers them with their help.
var commands = []struct{ name, args, help string }{
	{"login", "[server]", "sign in; the session is kept in ~/.config/yana"},
	{"logout", "", "end the session"},
	{"whoami", "", "the server and account signed in"},
	{"today", "[--space s] [text]", "open today's note, or append text to it (- reads stdin)"},
	{"yesterday", "[--space s] [text]", "the same for yesterday"},
	{"tomorrow", "[--space s] [text]", "the same for tomorrow"},
	{"new", "[--space s] <name>", "make a note where new notes go and open it"},
	{"open", "<path|title>", "open a note"},
	{"find", "[query]", "open the note picker"},
	{"grep", "[query]", "open the full-text search"},
	{"recent", "", "open the recently opened notes"},
	{"config", "", "where init.lua goes, and its defaults"},
	{"completion", "bash|zsh", "print the shell completion script"},
	{"serve", "", "run the server (bare yana does, without a terminal)"},
	{"scan", "", "rebuild the index once and exit"},
	{"version", "", "print the version"},
	{"drawings", "convert [--apply]", "convert Obsidian Excalidraw files in place"},
}

func usage() string {
	var b strings.Builder
	b.WriteString("usage: yana [command] [args]\n\n" +
		"  (none)                          at a terminal, neovim on the signed-in server; else serve\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-32s%s\n", strings.TrimSpace(c.name+" "+c.args), c.help)
	}
	b.WriteString("  YYYY-MM-DD [text]               that day's note, like today\n" +
		"  <path|title>                    open that note (yana open, when it is not a command)\n" +
		"\nSee docs/using.md.\n")
	return b.String()
}

// dayArg reads a command-line word as a day: today, yesterday, tomorrow
// or YYYY-MM-DD.
func dayArg(word string, now time.Time) (time.Time, bool) {
	switch word {
	case "today":
		return now, true
	case "yesterday":
		return now.AddDate(0, 0, -1), true
	case "tomorrow":
		return now.AddDate(0, 0, 1), true
	}
	if d, err := time.ParseInLocation("2006-01-02", word, now.Location()); err == nil {
		return d, true
	}
	return time.Time{}, false
}

// classify says what yana's first argument is: a day ("day"), one of
// the commands or a hidden one ("command"), else a note to open ("note").
func classify(arg string, now time.Time) (string, time.Time) {
	if d, ok := dayArg(arg, now); ok {
		return "day", d
	}
	switch arg {
	case "api", "__complete", "browse", "b", "help", "-h", "--help", "-v", "--version":
		return "command", time.Time{}
	}
	for _, c := range commands {
		if c.name == arg {
			return "command", time.Time{}
		}
	}
	return "note", time.Time{}
}

func main() {
	args := os.Args[1:]
	tty := term.IsTerminal(int(os.Stdin.Fd()))
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	} else if tty {
		// Bare yana is neovim for a person at a terminal; a container or a
		// service has no terminal on stdin and gets the server, as it always did.
		cmd = "browse"
	}
	rest := args[min(1, len(args)):]
	kind, day := classify(cmd, time.Now())
	var err error
	switch {
	case kind == "day":
		err = dayMain(day, rest)
	case kind == "note" && tty:
		err = openMain(strings.Join(args, " "))
	case kind == "note":
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage())
		os.Exit(2)
	default:
		switch cmd {
		case "version", "-v", "--version":
			fmt.Println("yana " + version)
			return
		case "help", "-h", "--help":
			fmt.Print(usage())
			return
		case "drawings":
			os.Exit(drawingsMain(rest))
		case "api":
			os.Exit(apiMain(rest, os.Stdin, os.Stdout))
		case "__complete":
			completeMain(rest, os.Stdout)
			return
		case "completion":
			err = completionMain(rest, os.Stdout)
		case "login":
			err = login(rest)
		case "logout":
			err = logout()
		case "whoami":
			err = whoami()
		case "config":
			err = configMain()
		case "open":
			if len(rest) == 0 {
				err = errors.New("usage: yana open <path or title>")
			} else {
				err = openMain(strings.Join(rest, " "))
			}
		case "new":
			err = newMain(rest)
		case "find":
			err = pickerMain("sf", rest)
		case "grep":
			err = pickerMain("sg", rest)
		case "recent":
			err = pickerMain("recent", rest)
		case "browse", "b":
			err = pickerMain("", nil)
		case "serve", "scan":
			serveMain(cmd)
			return
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "yana:", err)
		os.Exit(1)
	}
}

func serveMain(cmd string) {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	level, _ := config.ParseLevel(cfg.LogLevel)
	base := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(base)
	log := base.With("component", "main")

	if err := run(cmd, cfg, base, log); err != nil {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}
}

// run wires everything; base is the component-less logger subsystems tag
// themselves with, log is main's own.
func run(cmd string, cfg config.Config, base, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.NotesRoot, 0o755); err != nil {
		return fmt.Errorf("notes root %s: %w", cfg.NotesRoot, err)
	}
	limits := pathsafe.DefaultLimits()
	limits.MaxNoteSize = cfg.MaxNoteSize
	limits.MaxAssetSize = cfg.MaxAssetSize
	limits.MaxNotesPerSpace = cfg.MaxNotesPerSpace
	root, err := pathsafe.NewRoot(cfg.NotesRoot, limits)
	if err != nil {
		return err
	}
	db, err := index.Open(cfg.IndexPath(), base)
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer db.Close()
	log.Info("starting", "version", version, "notes_root", cfg.NotesRoot, "index", cfg.IndexPath(), "listen", cfg.Listen)

	sc := scanner.New(root, db, scanner.Options{
		SettleTime:       cfg.ScanSettleTime,
		MaxNoteSize:      cfg.MaxNoteSize,
		MaxAssetSize:     cfg.MaxAssetSize,
		MaxExtractSize:   cfg.MaxExtractSize,
		MaxNotesPerSpace: cfg.MaxNotesPerSpace,
	}, base)

	if cmd == "scan" {
		res, err := sc.Scan(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("indexed %d notes and %d assets in %s (%d ids assigned, %d retired, %d skipped, %d deferred)\n",
			res.Notes, res.Assets, res.Duration.Round(time.Millisecond), res.Assigned, res.Retired, res.Skipped, len(res.Deferred))
		return nil
	}

	rg := search.NewRipgrep(cfg.NotesRoot, cfg.Ripgrep, cfg.RipgrepTimeout)
	if cfg.Ripgrep && !rg.Available() {
		log.Warn("ripgrep (rg) not found on PATH; regex search is unavailable")
	}
	// The history layer exists before the reconciler so the reconciler
	// can hand it tree activity as it happens.
	var gl *git.Layer
	if cfg.Git {
		gl = git.New(cfg.NotesRoot, git.Options{
			Quiet:      cfg.GitQuiet,
			Interval:   cfg.GitInterval,
			Remote:     cfg.GitRemote,
			PushHour:   cfg.GitPushHour,
			HumanName:  cfg.GitUserName,
			HumanEmail: cfg.GitUserEmail,
			SecretPath: cfg.GitSecretPath(),
			DB:         db,
		}, base)
		if err := gl.Ensure(ctx); err != nil {
			log.Warn("git history is unavailable", "err", err)
			gl = nil
		}
	}
	onTreeChange := func(string) {}
	if gl != nil {
		onTreeChange = gl.Notify
	}
	// The watcher starts before the initial scan so nothing that changes
	// during the scan is missed.
	rec := reconcile.New(root, db, sc, reconcile.Options{
		IdleTime:     cfg.WritebackIdle,
		Debounce:     cfg.WatchDebounce,
		SettleTime:   cfg.ScanSettleTime,
		CompactAfter: cfg.CompactAfter,
		Retention:    cfg.CRDTRetention,
		OnTreeChange: onTreeChange,
	}, base)
	if err := rec.Start(); err != nil {
		return fmt.Errorf("start reconciler: %w", err)
	}
	if gl != nil {
		gl.Attach(rec)
		gl.Start()
	}

	// Accounts: the owner is created through the first-run flow on the
	// web client or the API; there are no default credentials.
	as, err := auth.Open(db, cfg.AuthSecretPath(), auth.Options{
		AccessTTL:  cfg.AccessTTL,
		RefreshTTL: cfg.RefreshTTL,
	}, base)
	if err != nil {
		return fmt.Errorf("open auth: %w", err)
	}

	hub := rt.New(rec, as, rt.Options{
		MaxConnections:   cfg.WSMaxConnections,
		MaxRoomsPerConn:  cfg.WSMaxRoomsPerConn,
		MaxSpacesPerConn: cfg.WSMaxSpacesPerConn,
		MaxMessageBytes:  cfg.WSMaxMessageBytes,
		PingInterval:     cfg.WSPingInterval,
		Verify: func(token string) (rt.Identity, error) {
			id, err := as.VerifyAccess(token)
			if err != nil {
				return rt.Identity{}, err
			}
			return rt.Identity{UserID: id.UserID, Username: id.Username, Owner: id.Owner, SessionID: id.SessionID}, nil
		},
		Limiter: pathsafe.NewRateLimiter(
			pathsafe.Rate{N: cfg.WSUserRate, Window: time.Minute},
			pathsafe.Rate{N: cfg.WSAgentRate, Window: time.Minute},
		),
	}, base)
	// A .space.yml edit or a vanished space severs open subscriptions
	// within one watcher cycle; a revoked session closes its sockets.
	rec.SetOnSpaceMembersChanged(func(space string) {
		hub.RecheckSpace(context.Background(), space)
	})
	as.OnSessionRevoked(hub.KickSession)

	// The agent surface: MCP over the same loop, the same path safety,
	// and its own write rate.
	agentRate := pathsafe.Rate{N: cfg.AgentRate, Window: time.Minute}
	agents := mcp.New(mcp.Deps{
		DB: db, Root: root, Sync: rec, Scanner: sc, Auth: as,
		Limit:   pathsafe.NewRateLimiter(agentRate, agentRate),
		Log:     base,
		Version: version,
	})

	srv := server.New(server.Deps{
		DB: db, Root: root, Ripgrep: rg, Web: web.Dist(), Log: base, Version: version, Sync: rec, RT: hub, Scanner: sc, Git: gl, Auth: as, MCP: agents,
		Daily:         server.DailyConfig{Pattern: cfg.DailyPattern, Template: cfg.DailyTemplate},
		ContentAddr:   cfg.ContentListen,
		ContentOrigin: cfg.ContentOrigin,
		SearchJS:      web.ExportSearchJS(),
		Favicon:       web.Favicon(),
		Rich:          web.ExportRich(),
	})
	if cfg.ContentListen != "" && cfg.ContentListen != "off" {
		// The content secret signs view tokens and derives public-link
		// tokens; it persists so a shared link outlives a restart.
		secret, err := fsutil.LoadOrCreateSecret(cfg.ContentSecretPath())
		if err != nil {
			return fmt.Errorf("content secret: %w", err)
		}
		srv.Deps.Content = server.NewContent(db, root, secret, base)
		srv.Deps.Content.Rich = web.ExportRich()
	}

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 2)
	go func() {
		err := httpSrv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	// The content origin: a second listener, its own port, none of the
	// app's routes. That separation is what makes the sandboxed frame
	// holding note markup harmless.
	var contentSrv *http.Server
	if srv.Deps.Content != nil {
		contentSrv = &http.Server{
			Addr:              cfg.ContentListen,
			Handler:           srv.Deps.Content,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		go func() {
			err := contentSrv.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			errCh <- err
		}()
		log.Info("content origin listening", "addr", cfg.ContentListen, "public_origin", cfg.ContentOrigin)
	}

	// The initial scan runs while the listener is already up so /healthz
	// answers immediately; /readyz turns 200 when the index is complete.
	go func() {
		res, err := sc.Scan(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("initial scan failed", "err", err)
			}
			return
		}
		srv.SetReady(true)
		rec.SweepOrphans(ctx)
		retryDeferred(ctx, sc, res.Deferred, cfg.ScanSettleTime, log)
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http: %w", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = httpSrv.Shutdown(shutdownCtx)
	if contentSrv != nil {
		if cerr := contentSrv.Shutdown(shutdownCtx); cerr != nil {
			log.Warn("content origin shutdown", "err", cerr)
		}
	}
	// Live editing sessions end before the reconciliation loop flushes and
	// closes.
	hub.Close()
	// Dirty notes are written before the index closes.
	if cerr := rec.Close(); cerr != nil {
		log.Warn("reconciler close", "err", cerr)
	}
	// With the tree settled, the repository commits what it holds.
	if gl != nil {
		gl.Close()
	}
	return err
}

// retryDeferred re-indexes files the scan skipped because they were still
// being written. Each pass waits a settle time; files that stay busy are
// retried a few times and then left for the next full scan.
func retryDeferred(ctx context.Context, sc *scanner.Scanner, paths []string, settle time.Duration, log *slog.Logger) {
	for attempt := 0; attempt < 5 && len(paths) > 0; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(settle + 500*time.Millisecond):
		}
		var again []string
		for _, p := range paths {
			err := sc.ScanOne(ctx, p)
			switch {
			case err == nil:
			case scanner.IsDeferred(err):
				again = append(again, p)
			default:
				log.Warn("deferred file could not be indexed", "path", p, "err", err)
			}
		}
		paths = again
	}
	if len(paths) > 0 {
		log.Warn("files still changing after several retries; they will be indexed on the next scan", "count", len(paths))
	}
}
