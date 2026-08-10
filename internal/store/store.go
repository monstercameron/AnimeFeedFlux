// Package store owns the SQLite database: connections, migrations, and the
// split between the writer and the read-only handle the publish plane gets.
//
// The split is the point (PLAN.md §2). The publish plane is the only thing
// exposed to the internet, and it holds a handle opened `mode=ro` behind an
// interface with no write methods. A bug there cannot corrupt the database
// because that code path has no writer — not because anyone was careful.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/migrations"

	_ "modernc.org/sqlite" // pure-Go driver; see PLAN.md §3 (A0-16)
)

// driverName is the name modernc.org/sqlite registers itself under.
const driverName = "sqlite"

// Store holds both connections. Callers that write take *Store; callers that
// must not write take Reader.
type Store struct {
	writer *sql.DB
	reader *sql.DB
	path   string
	log    *slog.Logger
}

// Reader is the read-only surface. It deliberately exposes no Exec, no Begin,
// and no access to the underlying writer, so a handler cannot reach a write
// path by accident or by refactor.
type Reader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	PingContext(ctx context.Context) error
}

// Options configure Open.
type Options struct {
	Path string
	Log  *slog.Logger
}

// Open prepares the database and returns a Store with both connections live.
//
// Boot order is load-bearing and is asserted rather than assumed. A read-only
// connection to a WAL database still needs write access to the `-shm`
// wal-index, which cannot be avoided with `immutable=1` here because the reader
// must see new writes. So the writer is opened FIRST and held for the process
// lifetime, which creates `-wal` and `-shm` before the reader attaches.
// Otherwise the first cold boot after a fresh deploy — or after a crash that
// removed `-shm` — fails with "unable to open database file", intermittently
// and with an error that names the wrong problem.
func Open(ctx context.Context, opts Options) (*Store, error) {
	if opts.Path == "" {
		return nil, errors.New("store: path is required")
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	abs, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("store: resolving %q: %w", opts.Path, err)
	}

	// 1. Writer first. Always.
	writer, err := sql.Open(driverName, writerDSN(abs))
	if err != nil {
		return nil, fmt.Errorf("store: opening writer: %w", err)
	}
	// SQLite has a single writer. More than one connection buys nothing and
	// turns a serialised wait into SQLITE_BUSY.
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0)

	if err := writer.PingContext(ctx); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("store: connecting writer: %w", err)
	}

	// Force the WAL and its sidecar files into existence before the reader
	// attaches. Ping alone may not have written anything.
	if _, err := writer.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS _boot (id INTEGER PRIMARY KEY)`); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("store: initialising database: %w", err)
	}

	if err := verifyPragmas(ctx, writer); err != nil {
		_ = writer.Close()
		return nil, err
	}

	// 2. Reader second, read-only.
	reader, err := sql.Open(driverName, readerDSN(abs))
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("store: opening reader: %w", err)
	}
	reader.SetMaxOpenConns(4) // readers do not block each other under WAL
	reader.SetMaxIdleConns(4)

	if err := reader.PingContext(ctx); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("store: connecting reader (writer must be opened first): %w", err)
	}

	log.Info("store open", slog.String("path", abs))

	return &Store{writer: writer, reader: reader, path: abs, log: log}, nil
}

func writerDSN(path string) string {
	// modernc.org/sqlite takes pragmas as repeated _pragma query parameters,
	// applied to every connection in the pool.
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + filepath.ToSlash(path) + "?" + q.Encode()
}

func readerDSN(path string) string {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	return "file:" + filepath.ToSlash(path) + "?" + q.Encode()
}

// verifyPragmas confirms the settings actually took. A DSN typo silently leaves
// the default in place — journal_mode stays `delete`, foreign keys stay off —
// and neither failure is visible until something depends on it much later.
func verifyPragmas(ctx context.Context, db *sql.DB) error {
	var journal string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil {
		return fmt.Errorf("store: reading journal_mode: %w", err)
	}
	if !strings.EqualFold(journal, "wal") {
		return fmt.Errorf("store: journal_mode is %q, want wal", journal)
	}

	var fk int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		return fmt.Errorf("store: reading foreign_keys: %w", err)
	}
	if fk != 1 {
		return errors.New("store: foreign_keys is off")
	}
	return nil
}

// Writer returns the read-write handle. Only the control plane should hold it.
func (s *Store) Writer() *sql.DB { return s.writer }

// Reader returns the read-only handle for the publish plane.
func (s *Store) Reader() Reader { return s.reader }

// Path is the absolute database path.
func (s *Store) Path() string { return s.path }

// Close shuts both connections down, reader first.
func (s *Store) Close() error {
	var errs []error
	if s.reader != nil {
		if err := s.reader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing reader: %w", err))
		}
	}
	if s.writer != nil {
		// Checkpoint so the WAL is folded back in and a copy of the .db file is
		// not missing recent transactions.
		if _, err := s.writer.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			errs = append(errs, fmt.Errorf("checkpointing: %w", err))
		}
		if err := s.writer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing writer: %w", err))
		}
	}
	return errors.Join(errs...)
}

// migration is one numbered file.
type migration struct {
	version int
	name    string
	sql     string
}

// Migrate applies every migration not yet recorded, each in its own
// transaction, forward only.
//
// There are no down-migrations, deliberately (§10): the recovery path is a
// backup restore, which is honest about what would actually happen. A down
// migration that has never been run is a script that does not work.
func (s *Store) Migrate(ctx context.Context) (applied int, err error) {
	if _, err := s.writer.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		) STRICT`); err != nil {
		return 0, fmt.Errorf("store: creating schema_migrations: %w", err)
	}

	have, err := s.appliedVersions(ctx)
	if err != nil {
		return 0, err
	}

	all, err := loadMigrations()
	if err != nil {
		return 0, err
	}

	for _, m := range all {
		if have[m.version] {
			continue
		}
		if err := s.applyOne(ctx, m); err != nil {
			return applied, err
		}
		applied++
		s.log.Info("migration applied",
			slog.Int("version", m.version),
			slog.String("name", m.name))
	}
	return applied, nil
}

func (s *Store) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := s.writer.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("store: reading schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	have := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: scanning schema_migrations: %w", err)
		}
		have[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating schema_migrations: %w", err)
	}
	return have, nil
}

func (s *Store) applyOne(ctx context.Context, m migration) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration %d: %w", m.version, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("store: migration %d (%s): %w", m.version, m.name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("store: recording migration %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration %d: %w", m.version, err)
	}
	return nil
}

// loadMigrations reads the embedded files and orders them by version.
//
// Embedding matters: the binary carries its own schema, so a container cannot
// start against a migration directory that was not shipped with it.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("store: reading migrations: %w", err)
	}

	var out []migration
	seen := map[int]string{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationName(e.Name())
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[version]; dup {
			// Two files claiming one version means the order they apply in is
			// whatever the filesystem says, which is not a thing to leave to
			// chance in a schema.
			return nil, fmt.Errorf("store: duplicate migration version %d (%s and %s)", version, prev, e.Name())
		}
		seen[version] = e.Name()

		body, err := migrations.FS.ReadFile(e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: reading %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: version, name: name, sql: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func parseMigrationName(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	num, rest, found := strings.Cut(base, "_")
	if !found {
		return 0, "", fmt.Errorf("store: migration %q must be named NNNN_name.sql", filename)
	}
	v, err := strconv.Atoi(num)
	if err != nil {
		return 0, "", fmt.Errorf("store: migration %q has a non-numeric version: %w", filename, err)
	}
	return v, rest, nil
}
