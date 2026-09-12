// Package index is the SQLite cache. Everything in it is derived from the
// notes tree and can be rebuilt by a scan; deleting the file loses nothing
// the user considers content.
//
// All writes go through a single goroutine (Write). Reads use the
// connection pool directly; WAL mode lets them proceed while a write is in
// flight.
package index

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrClosed is returned by Write after Close.
var ErrClosed = errors.New("index: database is closed")

// DB wraps the reader pool and the serialized writer.
type DB struct {
	readers *sql.DB
	writer  *sql.DB
	jobs    chan job
	done    chan struct{}
	closeMu sync.RWMutex
	closed  bool
	log     *slog.Logger
	path    string
}

type job struct {
	ctx context.Context
	fn  func(tx *sql.Tx) error
	res chan error
}

// Open creates or opens the index at path, applies migrations, and starts
// the writer goroutine. The parent directory is created if missing.
func Open(path string, log *slog.Logger) (*DB, error) {
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "index")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create index dir: %w", err)
	}
	pragmas := "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	readers, err := sql.Open("sqlite", "file:"+path+"?"+pragmas)
	if err != nil {
		return nil, err
	}
	readers.SetMaxOpenConns(8)
	writer, err := sql.Open("sqlite", "file:"+path+"?"+pragmas+"&_txlock=immediate")
	if err != nil {
		readers.Close()
		return nil, err
	}
	writer.SetMaxOpenConns(1)

	db := &DB{
		readers: readers,
		writer:  writer,
		jobs:    make(chan job),
		done:    make(chan struct{}),
		log:     log,
		path:    path,
	}
	if err := db.migrate(); err != nil {
		readers.Close()
		writer.Close()
		return nil, err
	}
	go db.runWriter()
	return db, nil
}

// Path returns the database file location.
func (db *DB) Path() string { return db.path }

// Close stops the writer and closes both pools. Pending writes complete
// first.
func (db *DB) Close() error {
	db.closeMu.Lock()
	if db.closed {
		db.closeMu.Unlock()
		return ErrClosed
	}
	db.closed = true
	close(db.jobs)
	db.closeMu.Unlock()
	<-db.done
	err1 := db.writer.Close()
	err2 := db.readers.Close()
	return errors.Join(err1, err2)
}

func (db *DB) runWriter() {
	defer close(db.done)
	for j := range db.jobs {
		j.res <- db.runTx(j.ctx, j.fn)
	}
}

func (db *DB) runTx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := db.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Write runs fn inside a transaction on the single writer connection. It
// blocks until the transaction commits or fails.
func (db *DB) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	j := job{ctx: ctx, fn: fn, res: make(chan error, 1)}
	db.closeMu.RLock()
	if db.closed {
		db.closeMu.RUnlock()
		return ErrClosed
	}
	select {
	case db.jobs <- j:
		db.closeMu.RUnlock()
	case <-ctx.Done():
		db.closeMu.RUnlock()
		return ctx.Err()
	}
	select {
	case err := <-j.res:
		return err
	case <-ctx.Done():
		// The job is already running; wait for it so the connection is
		// released before we return.
		return <-j.res
	}
}

// Reader exposes the read pool for query helpers.
func (db *DB) Reader() *sql.DB { return db.readers }

func (db *DB) migrate() error {
	var version int
	if err := db.writer.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		n, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: bad prefix", name)
		}
		if n <= version {
			continue
		}
		raw, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := db.runTx(context.Background(), func(tx *sql.Tx) error {
			if _, err := tx.Exec(string(raw)); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
			_, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", n))
			return err
		}); err != nil {
			return err
		}
		db.log.Info("applied migration", "name", name)
		version = n
	}
	return nil
}
