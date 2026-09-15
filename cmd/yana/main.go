// Command yana runs the notes server.
//
//	yana            serve (default)
//	yana scan       rebuild the index once and exit
//	yana version    print the version
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
	"syscall"
	"time"

	"github.com/madeofpendletonwool/yana/internal/auth"
	"github.com/madeofpendletonwool/yana/internal/config"
	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
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

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "version", "-v", "--version":
		fmt.Println("yana " + version)
		return
	case "serve", "scan":
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (use serve, scan, or version)\n", cmd)
		os.Exit(2)
	}

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
		MaxConnections:  cfg.WSMaxConnections,
		MaxRoomsPerConn: cfg.WSMaxRoomsPerConn,
		MaxMessageBytes: cfg.WSMaxMessageBytes,
		PingInterval:    cfg.WSPingInterval,
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

	srv := server.New(server.Deps{
		DB: db, Root: root, Ripgrep: rg, Web: web.Dist(), Log: base, Version: version, Sync: rec, RT: hub, Scanner: sc, Git: gl, Auth: as,
		Daily: server.DailyConfig{Pattern: cfg.DailyPattern, Template: cfg.DailyTemplate},
	})

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		err := httpSrv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

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
