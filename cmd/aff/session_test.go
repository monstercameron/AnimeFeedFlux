package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
)

func TestSaveSessionIsMode0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "session.json")

	sd := &sessionData{Server: "127.0.0.1:9091", Token: "abc123", SessionID: "sess-1", ExpiresAt: time.Now()}
	if err := saveSession(path, sd); err != nil {
		t.Fatalf("saveSession: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if runtime.GOOS != "windows" {
		// Windows' os.FileMode from a NTFS ACL does not reproduce Unix
		// 0600 bit-for-bit, so the permission assertion only holds where
		// os.Chmod's argument is authoritative (PLAN.md task: "the session
		// file is written 0600").
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("session file mode = %o, want 0600", perm)
		}
	}

	got, err := loadSession(path)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if got.Token != sd.Token || got.SessionID != sd.SessionID || got.Server != sd.Server {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, sd)
	}
}

func TestLoadSessionMissingIsErrSessionNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := loadSession(filepath.Join(dir, "does-not-exist.json"))
	if !errors.Is(err, errSessionNotFound) {
		t.Fatalf("err = %v, want errSessionNotFound", err)
	}
}

func TestClearSessionRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := saveSession(path, &sessionData{Token: "x"}); err != nil {
		t.Fatalf("saveSession: %v", err)
	}
	if err := clearSession(path); err != nil {
		t.Fatalf("clearSession: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file to be gone, stat err = %v", err)
	}
	// Clearing an already-absent session file is not an error.
	if err := clearSession(path); err != nil {
		t.Fatalf("clearSession on missing file: %v", err)
	}
}

func TestSessionCredsAttachesToken(t *testing.T) {
	c := sessionCreds{token: "tok-1"}
	md, err := c.GetRequestMetadata(t.Context())
	if err != nil {
		t.Fatalf("GetRequestMetadata: %v", err)
	}
	if md[rpc.SessionTokenHeader] != "tok-1" {
		t.Fatalf("metadata[%s] = %q, want %q", rpc.SessionTokenHeader, md[rpc.SessionTokenHeader], "tok-1")
	}

	empty := sessionCreds{}
	md, err = empty.GetRequestMetadata(t.Context())
	if err != nil {
		t.Fatalf("GetRequestMetadata: %v", err)
	}
	if md != nil {
		t.Fatalf("expected no metadata for an empty token, got %v", md)
	}
}
