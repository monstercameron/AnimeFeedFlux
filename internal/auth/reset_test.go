package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/migrations"
)

func TestNewResetTokenIs256Bits(t *testing.T) {
	raw, hash, expiresAt, err := NewResetToken()
	if err != nil {
		t.Fatalf("NewResetToken: %v", err)
	}
	if raw == "" || hash == "" {
		t.Fatal("NewResetToken: empty raw or hash")
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("raw token is not base64url: %v", err)
	}
	if got := len(decoded) * 8; got != 256 {
		t.Fatalf("raw token has %d bits of entropy, want 256", got)
	}

	if !expiresAt.After(time.Now().UTC()) {
		t.Fatal("NewResetToken: expiresAt is not in the future")
	}
	if until := time.Until(expiresAt); until > resetTokenTTL || until <= 0 {
		t.Fatalf("NewResetToken: expiresAt is %v out from now, want within (0, %v]", until, resetTokenTTL)
	}
}

func TestNewResetTokenHashIsDeterministicFromRaw(t *testing.T) {
	raw, hash, _, err := NewResetToken()
	if err != nil {
		t.Fatalf("NewResetToken: %v", err)
	}
	if got := hashResetToken(raw); got != hash {
		t.Fatalf("hashResetToken(raw) = %q, want %q (the hash NewResetToken returned)", got, hash)
	}
}

func TestVerifyResetTokenAcceptsMatchingRaw(t *testing.T) {
	raw, hash, _, err := NewResetToken()
	if err != nil {
		t.Fatalf("NewResetToken: %v", err)
	}
	if !VerifyResetToken(raw, hash) {
		t.Fatal("VerifyResetToken: correct raw token reported as not matching its own hash")
	}
}

func TestVerifyResetTokenRejectsWrongRaw(t *testing.T) {
	_, hash, _, err := NewResetToken()
	if err != nil {
		t.Fatalf("NewResetToken: %v", err)
	}
	otherRaw, _, _, err := NewResetToken()
	if err != nil {
		t.Fatalf("NewResetToken: %v", err)
	}
	if VerifyResetToken(otherRaw, hash) {
		t.Fatal("VerifyResetToken: unrelated raw token reported as matching")
	}
	if VerifyResetToken("", hash) {
		t.Fatal("VerifyResetToken: empty raw token reported as matching")
	}
}

// TestVerifyResetTokenTimingComparable is a smoke test, mirroring password_test.go's
// TestVerifyTimingComparable: it checks VerifyResetToken does not short-circuit its comparison
// based on how much of the token matches, so a wrong guess and a right guess take comparable
// time. Not a rigorous timing measurement — a generous tolerance catches only a gross regression
// like swapping the constant-time compare for bytes.Equal.
func TestVerifyResetTokenTimingComparable(t *testing.T) {
	if testing.Short() {
		t.Skip("timing smoke test skipped in -short mode")
	}
	raw, hash, _, err := NewResetToken()
	if err != nil {
		t.Fatalf("NewResetToken: %v", err)
	}
	wrongRaw, _, _, err := NewResetToken()
	if err != nil {
		t.Fatalf("NewResetToken: %v", err)
	}

	// Compare MEDIANS, not totals. VerifyResetToken is a SHA-256 plus a
	// constant-time compare — around a microsecond — so a single scheduler
	// preemption anywhere in the loop adds milliseconds to one sample and
	// swamps the sum. This test summed 500 samples and failed intermittently
	// for exactly that reason, and an intermittently-failing security test
	// gets deleted, which costs the check entirely. The median is unmoved by
	// a handful of blown samples while still catching the regression this
	// exists for: swapping the constant-time compare for bytes.Equal, which
	// returns on the first differing byte and shows up as an order-of-
	// magnitude gap, not a 2x one.
	const iterations = 500
	right := make([]time.Duration, iterations)
	wrong := make([]time.Duration, iterations)
	for i := range iterations {
		start := time.Now()
		VerifyResetToken(raw, hash)
		right[i] = time.Since(start)

		start = time.Now()
		VerifyResetToken(wrongRaw, hash)
		wrong[i] = time.Since(start)
	}

	rightMed, wrongMed := medianDuration(right), medianDuration(wrong)
	ratio := float64(rightMed) / float64(wrongMed)
	if ratio < 0.25 || ratio > 4.0 {
		t.Errorf("VerifyResetToken timing diverges beyond tolerance: right median=%v wrong median=%v ratio=%.2f",
			rightMed, wrongMed, ratio)
	}
}

func medianDuration(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[len(s)/2]
}

// TestResetTokenStoreSingleUseAndExpiry exercises the schema migrations/0004_auth_hardening.sql
// adds (password_reset_tokens) end to end: a token round-trips through insert/lookup, single-use
// is representable via used_at, and an expired row is distinguishable from a live one. The
// single-use enforcement itself is the caller's job (see reset.go's doc comment on revocation) —
// this test proves the schema supports doing that correctly, not that some RPC handler does it,
// since no such handler exists in this package.
func TestResetTokenStoreSingleUseAndExpiry(t *testing.T) {
	ctx := context.Background()
	db := openMigratedDB(t)

	raw, hash, expiresAt, err := NewResetToken()
	if err != nil {
		t.Fatalf("NewResetToken: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (token_hash, expires_at) VALUES (?, ?)`,
		hash, expiresAt.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert reset token: %v", err)
	}

	// A second insert with the same hash must fail: UNIQUE(token_hash) is what stops two rows
	// from both claiming to be valid for what should be one single-use secret.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (token_hash, expires_at) VALUES (?, ?)`,
		hash, expiresAt.Format(time.RFC3339Nano),
	); err == nil {
		t.Fatal("password_reset_tokens: duplicate token_hash was accepted, want UNIQUE violation")
	}

	var gotHash string
	var usedAt *string
	if err := db.QueryRowContext(ctx,
		`SELECT token_hash, used_at FROM password_reset_tokens WHERE token_hash = ?`, hash,
	).Scan(&gotHash, &usedAt); err != nil {
		t.Fatalf("lookup reset token: %v", err)
	}
	if !VerifyResetToken(raw, gotHash) {
		t.Fatal("stored token_hash does not verify against the raw token that produced it")
	}
	if usedAt != nil {
		t.Fatalf("used_at = %v, want NULL for a freshly inserted token", *usedAt)
	}

	// Mark it used, the way a caller completing a reset would (see reset.go's doc comment: this
	// must happen alongside the password update and session revocation, all in one transaction —
	// simulated here as a single UPDATE since this package owns no RPC handler to exercise).
	usedTimestamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx,
		`UPDATE password_reset_tokens SET used_at = ? WHERE token_hash = ?`, usedTimestamp, hash,
	); err != nil {
		t.Fatalf("mark reset token used: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT used_at FROM password_reset_tokens WHERE token_hash = ?`, hash,
	).Scan(&usedAt); err != nil {
		t.Fatalf("re-lookup reset token: %v", err)
	}
	if usedAt == nil {
		t.Fatal("used_at is still NULL after marking the token used")
	}

	// Expiry: a token whose expires_at is in the past is distinguishable from a live one by a
	// plain comparison against now — the caller (not this schema) is responsible for rejecting on
	// this, but the query below is exactly what that rejection is built on.
	expiredHash := hashResetToken("expired-token-for-testing-only")
	pastExpiry := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (token_hash, expires_at) VALUES (?, ?)`,
		expiredHash, pastExpiry,
	); err != nil {
		t.Fatalf("insert expired reset token: %v", err)
	}

	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	var expiredCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM password_reset_tokens WHERE token_hash = ? AND expires_at < ?`,
		expiredHash, nowStr,
	).Scan(&expiredCount); err != nil {
		t.Fatalf("query expired token: %v", err)
	}
	if expiredCount != 1 {
		t.Fatalf("expired-token query matched %d rows, want 1", expiredCount)
	}
}

// applyMigrationsTracked is openMigratedDB's schema_migrations-aware sibling: it mirrors
// internal/store.Store.Migrate's bookkeeping (a schema_migrations(version) row per applied file,
// each migration skipped if its version is already recorded) closely enough to prove
// 0004_auth_hardening.sql is idempotent under that exact mechanism, without importing
// internal/store — which would cycle back into this package (see openMigratedDB's doc comment in
// pepper_test.go). It returns how many migrations this call applied, matching store.Migrate's
// `applied int` return.
func applyMigrationsTracked(t *testing.T, db *sql.DB) int {
	t.Helper()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`,
	); err != nil {
		t.Fatalf("creating schema_migrations: %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		t.Fatalf("reading schema_migrations: %v", err)
	}
	have := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scanning schema_migrations: %v", err)
		}
		have[v] = true
	}
	_ = rows.Close()

	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("reading embedded migrations: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	applied := 0
	for _, name := range names {
		version, _, ok := parseMigrationVersion(name)
		if !ok {
			t.Fatalf("migration %q not named NNNN_name.sql", name)
		}
		if have[version] {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("reading migration %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("applying migration %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			version, name, time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			t.Fatalf("recording migration %s: %v", name, err)
		}
		applied++
	}
	return applied
}

// parseMigrationVersion mirrors internal/store's own filename parsing (NNNN_name.sql) closely
// enough for a test to sort/dedupe by version; it is intentionally not shared code with
// internal/store, for the same import-cycle reason as openMigratedDB.
func parseMigrationVersion(filename string) (version int, name string, ok bool) {
	base := filename
	if len(base) < 5 || base[4] != '_' {
		return 0, "", false
	}
	var v int
	for i := 0; i < 4; i++ {
		c := base[i]
		if c < '0' || c > '9' {
			return 0, "", false
		}
		v = v*10 + int(c-'0')
	}
	return v, base[5:], true
}

// TestMigration0004AppliesCleanlyAndIsIdempotent covers the migration-runner requirements for
// 0004_auth_hardening.sql: applied in order after 0001-0003 (numeric filename order, same as
// internal/store's runner), and re-running the same tracked-apply logic against an already
// current database applies nothing and returns no error. schema_migrations bookkeeping — the same
// mechanism internal/store/store.go uses — is what makes 0004 idempotent, not anything special
// about this migration's SQL.
func TestMigration0004AppliesCleanlyAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openTrackedDB(t)

	appliedFirst := applyMigrationsTracked(t, db)
	if appliedFirst == 0 {
		t.Fatal("first apply: applied 0 migrations, want at least 0004 to apply")
	}

	// admin.password_version / admin.pepper_version and password_reset_tokens must exist and be
	// usable after this single apply — proof 0004 applied cleanly alongside 0001-0003 rather than
	// only when run against a pre-existing 0003 database via some other path.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO admin (id, password_hash, kdf_params, created_at, password_version, pepper_version) VALUES (1, 'x', '{}', ?, 1, 0)`,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert admin row using 0004 columns: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (token_hash, expires_at) VALUES ('deadbeef', ?)`,
		time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert into password_reset_tokens: %v", err)
	}

	appliedSecond := applyMigrationsTracked(t, db)
	if appliedSecond != 0 {
		t.Fatalf("second apply applied %d migrations, want 0 (idempotent re-run)", appliedSecond)
	}
}

// openTrackedDB opens a fresh SQLite file for applyMigrationsTracked, without pre-applying any
// migrations (unlike pepper_test.go's openMigratedDB) — this test needs to observe and control
// each apply pass itself.
func openTrackedDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := t.TempDir() + "/auth_reset_migration_test.db"
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
