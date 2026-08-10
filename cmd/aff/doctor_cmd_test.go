package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorReportsAHealthyDatabaseAsHealthy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	seedMigratedDB(t, dbPath)
	t.Setenv("SCHEMAFLUX_API_KEY", "present-for-test")

	a, stdout, stderr := newOpsTestApp(t, dbPath)
	code := a.run([]string{"doctor"})
	if code != exitOK {
		t.Fatalf("aff doctor on a healthy db: exit %d, want %d\nstdout: %s\nstderr: %s",
			code, exitOK, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "FAIL") {
		t.Fatalf("aff doctor reported a failing check on a healthy db: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "database opens") {
		t.Fatalf("stdout = %q, want the database-opens check listed", stdout.String())
	}
}

func TestDoctorReportsACorruptedDatabaseAsUnhealthy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(dbPath, []byte("not a sqlite file"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	a, stdout, _ := newOpsTestApp(t, dbPath)
	code := a.run([]string{"doctor"})
	if code != exitFail {
		t.Fatalf("aff doctor on a corrupted db: exit %d, want %d", code, exitFail)
	}
	if !strings.Contains(stdout.String(), "FAIL") {
		t.Fatalf("stdout = %q, want at least one FAIL line", stdout.String())
	}
}

func TestDoctorFlagsMissingProviderKeyWithoutPrintingAnyValue(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	seedMigratedDB(t, dbPath)
	t.Setenv("SCHEMAFLUX_API_KEY", "")

	a, stdout, _ := newOpsTestApp(t, dbPath)
	code := a.run([]string{"doctor"})
	if code != exitFail {
		t.Fatalf("aff doctor with no provider key: exit %d, want %d", code, exitFail)
	}
	if !strings.Contains(stdout.String(), "provider key present") {
		t.Fatalf("stdout = %q, want the provider-key check listed", stdout.String())
	}
}

func TestDoctorRequiresDBPath(t *testing.T) {
	a, _, stderr := newOpsTestApp(t, "")
	code := a.run([]string{"doctor"})
	if code != exitFail {
		t.Fatalf("aff doctor with no --db: exit %d, want %d", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "--db") {
		t.Fatalf("stderr = %q, want a --db/AFF_DB_PATH complaint", stderr.String())
	}
}
