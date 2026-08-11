// System-plane persistence: cursor-paginated audit-trail reads and the
// database VACUUM operation backing the Settings "Data" section (PLAN.md
// §12.5, §10, §15). Everything here is a method on *Store — control-plane
// only, same as items.go/runs.go/auth.go.
//
// This file deliberately does NOT redeclare AuthEvent (internal/store/auth.go
// already owns that type and RecordAuthEvent/ListAuthEvents/RecentFailures)
// — it adds the one thing auth.go's ListAuthEvents does not have: an
// id-cursor so a caller can page through `auth_events` instead of only ever
// asking for "the newest N".
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// --- audit events (cursor pagination) ---------------------------------

// ListAuditEventsPage returns up to limit+1 rows from `auth_events`, newest
// first, starting strictly before beforeID (beforeID <= 0 means "start from
// the newest row"). The +1 lookahead lets a caller (internal/rpc/system.go)
// detect whether a next page exists without a separate COUNT(*) — the same
// convention internal/rpc/run.go's History uses against `runs`, and for the
// same reason: `id` is a monotonic INTEGER PRIMARY KEY assigned in insertion
// order (migrations/0001_auth.sql), so "id < cursor" is an exact, tie-free
// "older than the last row I returned" boundary that a same-second `at`
// could not give.
//
// limit must be > 0; callers are responsible for bounding it (page-size caps
// belong at the RPC layer, same split as ListItems/ListRuns).
func (s *Store) ListAuditEventsPage(ctx context.Context, beforeID int64, limit int) ([]AuthEvent, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("store: listing audit events: limit must be > 0")
	}

	query := `SELECT id, at, kind, ip, ok, detail FROM auth_events`
	args := []any{}
	if beforeID > 0 {
		query += ` WHERE id < ?`
		args = append(args, beforeID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing audit events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AuthEvent
	for rows.Next() {
		var (
			e       AuthEvent
			at      string
			ip, det sql.NullString
			ok      int64
		)
		if err := rows.Scan(&e.ID, &at, &e.Kind, &ip, &ok, &det); err != nil {
			return nil, fmt.Errorf("store: scanning audit event: %w", err)
		}
		if e.At, err = parseTime(at); err != nil {
			return nil, fmt.Errorf("store: parsing auth_events.at: %w", err)
		}
		e.IP = ip.String
		e.Detail = det.String
		e.OK = ok != 0
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating audit events: %w", err)
	}
	return out, nil
}

// --- vacuum -------------------------------------------------------------

// ErrRunInFlight is returned by Vacuum's caller-side guard (see
// internal/rpc/system.go) when a generation run is currently 'running' —
// Vacuum itself does not check this; RunInFlight is the check, kept
// separate so the RPC layer decides the policy (refuse) while this file
// only ever answers "is one running right now".
var ErrRunInFlight = errors.New("store: generation run in flight")

// RunInFlight reports whether any run row is currently in the 'running'
// state. VACUUM rewrites the entire database file and holds SQLite's single
// writer connection (Store.writer has MaxOpenConns(1)) for the duration; a
// live generation run committing items or renewing its heartbeat during
// that window would either stall behind the VACUUM or, worse, have its
// heartbeat renewal itself stall long enough to look crashed to
// ReclaimStaleRuns. Refusing up front is simpler and cheaper than letting
// the two contend for the same connection.
func (s *Store) RunInFlight(ctx context.Context) (bool, error) {
	var n int
	if err := s.writer.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE status = 'running'`,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("store: checking for an in-flight run: %w", err)
	}
	return n > 0, nil
}

// dbFileSizeBytes sums the main database file with its WAL and
// shared-memory sidecars (§3: journal_mode=WAL) — under WAL the main file
// alone understates on-disk size, since recent commits live in `-wal` until
// the next checkpoint. Mirrors internal/rpc/system.go's sysDBSizeBytes
// exactly; duplicated rather than shared because that function is
// unexported in a different package and this package may not import rpc.
func dbFileSizeBytes(path string) (int64, error) {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		fi, err := os.Stat(path + suffix)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		total += fi.Size()
	}
	return total, nil
}

// VacuumStats reports what Vacuum accomplished: size before, size after,
// and how long the exclusive lock was held. "Did that accomplish anything"
// is the only reason to ever run this, per PLAN.md §12.5's Data section.
type VacuumStats struct {
	SizeBeforeBytes int64
	SizeAfterBytes  int64
	Duration        time.Duration
}

// Vacuum runs SQLite's `VACUUM` against the writer connection, rewriting
// the whole database file. This BLOCKS: `VACUUM` takes an exclusive lock
// for its entire duration, and on a 2 GB box with a live publish plane
// serving feeds, that duration is not negligible — SQLite's VACUUM is
// broadly disk-bandwidth-bound (it rewrites every page), so a rough budget
// is "a few seconds per 100 MB of database" on this project's target
// hardware; a multi-GB database can mean a stall measured in tens of
// seconds to low minutes. There is no cheap way to estimate it more
// precisely than that without doing the work, which is why this function
// returns the ACTUAL elapsed duration rather than a prediction — see
// VacuumStats.
//
// Vacuum itself does not consult RunInFlight — see ErrRunInFlight's doc
// comment for why that check belongs to the caller (internal/rpc/system.go)
// rather than being silently baked in here, where a future non-RPC caller
// (a CLI command, a test) could not opt out of it.
func (s *Store) Vacuum(ctx context.Context) (VacuumStats, error) {
	before, err := dbFileSizeBytes(s.path)
	if err != nil {
		return VacuumStats{}, fmt.Errorf("store: statting database before vacuum: %w", err)
	}

	start := time.Now()
	if _, err := s.writer.ExecContext(ctx, `VACUUM`); err != nil {
		return VacuumStats{}, fmt.Errorf("store: vacuuming database: %w", err)
	}
	elapsed := time.Since(start)

	after, err := dbFileSizeBytes(s.path)
	if err != nil {
		return VacuumStats{}, fmt.Errorf("store: statting database after vacuum: %w", err)
	}

	return VacuumStats{SizeBeforeBytes: before, SizeAfterBytes: after, Duration: elapsed}, nil
}
