package auth

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"testing"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver; same one internal/store uses (PLAN.md §3)

	"github.com/monstercameron/AnimeFeedFlux/migrations"
)

func TestPepperChangesStoredValue(t *testing.T) {
	argonOutput := []byte("pretend-this-is-32-bytes-of-key")
	pepper := []byte("some-256-bit-secret-from-the-env")

	peppered := Pepper(argonOutput, pepper)
	if string(peppered) == string(argonOutput) {
		t.Fatal("Pepper: peppered output equals the unpeppered input")
	}
}

func TestPepperDeterministic(t *testing.T) {
	argonOutput := []byte("pretend-this-is-32-bytes-of-key")
	pepper := []byte("some-256-bit-secret-from-the-env")

	a := Pepper(argonOutput, pepper)
	b := Pepper(argonOutput, pepper)
	if string(a) != string(b) {
		t.Fatal("Pepper: same input+pepper produced different output")
	}
}

func TestVerifyPepperedWrongPepperFails(t *testing.T) {
	argonOutput := []byte("pretend-this-is-32-bytes-of-key")
	pepper := []byte("correct-pepper-from-the-env")
	wrongPepper := []byte("a-different-pepper-entirely")

	stored := Pepper(argonOutput, pepper)

	if !VerifyPeppered(argonOutput, stored, pepper) {
		t.Fatal("VerifyPeppered: correct pepper reported as not matching")
	}
	if VerifyPeppered(argonOutput, stored, wrongPepper) {
		t.Fatal("VerifyPeppered: wrong pepper reported as matching")
	}
}

// TestNoPepperVerifiesExactlyAsBefore is the compatibility case that matters most (PLAN.md §4):
// an existing deployment with no AFF_SECRET_KEY configured must keep working unmodified. The zero
// value for pepper — nil, or an empty slice — must be a true no-op: Pepper returns argonOutput
// unchanged, and VerifyPeppered against a value that was never peppered must still succeed.
func TestNoPepperVerifiesExactlyAsBefore(t *testing.T) {
	argonOutput := []byte("pretend-this-is-32-bytes-of-key")

	if got := Pepper(argonOutput, nil); string(got) != string(argonOutput) {
		t.Fatalf("Pepper(x, nil) = %q, want unchanged %q", got, argonOutput)
	}
	if got := Pepper(argonOutput, []byte{}); string(got) != string(argonOutput) {
		t.Fatalf("Pepper(x, []byte{}) = %q, want unchanged %q", got, argonOutput)
	}

	// stored is what an unconfigured deployment persisted: the raw argon2id output, never
	// peppered, because Hash-time also had no pepper configured.
	stored := argonOutput
	if !VerifyPeppered(argonOutput, stored, nil) {
		t.Fatal("VerifyPeppered: no pepper configured should verify exactly as before peppering existed")
	}
}

func TestVerifyPepperedWrongCandidateFailsWithNoPepper(t *testing.T) {
	stored := []byte("pretend-this-is-32-bytes-of-key")
	wrong := []byte("a-completely-different-key-here")
	if VerifyPeppered(wrong, stored, nil) {
		t.Fatal("VerifyPeppered: mismatched candidate reported as matching with no pepper")
	}
}

// openMigratedDB applies every embedded migration, in ascending version order, to a fresh SQLite
// file and returns the writer connection. It is a deliberately minimal stand-in for
// internal/store.Store.Migrate: this package cannot import internal/store, because store already
// imports internal/auth (for admin credential handling), and internal/auth's white-box tests
// (package auth, not auth_test) importing store back would be an import cycle. Using the same
// driver and the same embedded migrations.FS that store.Migrate consumes keeps this a faithful
// exercise of the real migration SQL rather than a hand-rolled schema that could drift from it.
func openMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "auth_pepper_test.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

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
	sort.Strings(names) // NNNN_ prefix makes lexical order equal numeric order

	for _, name := range names {
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("reading migration %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("applying migration %s: %v", name, err)
		}
	}
	return db
}

// TestPepperVersionRoundTrips exercises the pepper_version column added by
// migrations/0004_auth_hardening.sql against a real database: applying every migration, inserting
// an admin row without specifying pepper_version, and reading it back. It must default to 0 (no
// pepper) so a row written before peppering existed round-trips as "unpeppered" rather than
// erroring or defaulting to some other value, and it must accept and return an explicit version
// once one is set, since that is what makes rotation possible at all (PLAN.md §4).
func TestPepperVersionRoundTrips(t *testing.T) {
	ctx := context.Background()
	db := openMigratedDB(t)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO admin (id, password_hash, kdf_params, created_at) VALUES (1, 'x', '{}', ?)`,
		now,
	); err != nil {
		t.Fatalf("insert admin row: %v", err)
	}

	var defaultVersion int
	if err := db.QueryRowContext(ctx, `SELECT pepper_version FROM admin WHERE id = 1`).Scan(&defaultVersion); err != nil {
		t.Fatalf("select default pepper_version: %v", err)
	}
	if defaultVersion != 0 {
		t.Fatalf("default pepper_version = %d, want 0 (no pepper)", defaultVersion)
	}

	if _, err := db.ExecContext(ctx, `UPDATE admin SET pepper_version = 3 WHERE id = 1`); err != nil {
		t.Fatalf("update pepper_version: %v", err)
	}
	var gotVersion int
	if err := db.QueryRowContext(ctx, `SELECT pepper_version FROM admin WHERE id = 1`).Scan(&gotVersion); err != nil {
		t.Fatalf("select updated pepper_version: %v", err)
	}
	if gotVersion != 3 {
		t.Fatalf("pepper_version after update = %d, want 3", gotVersion)
	}
}
