package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
)

// The CLI attaches its stored session token under rpc.SessionTokenHeader —
// the same name internal/rpc.AuthServer.Login/RecoverWithCode set the raw
// token under in the response header (see that constant's doc comment for
// why it exists out-of-band from the message body), and the same name
// internal/rpc's sessionTokenFromContext reads as its metadata fallback for
// exactly this client. A direct gRPC client has no cookie jar, so metadata is
// this transport's equivalent of the browser's Set-Cookie/Cookie pair; using
// the server's own constant instead of a locally-defined one with the same
// string value means there is one name for this on the wire, not two that
// merely happen to agree today.

// sessionData is what session.json holds. It is deliberately not the proto
// Session message: this file additionally carries the raw token (never
// present on the wire in a message body, see rpc.SessionTokenHeader) and the
// server it was issued by, so a stale session file from a different server
// is detectable rather than silently sent to the wrong admin listener.
type sessionData struct {
	Server    string    `json:"server"`
	Token     string    `json:"token"`
	SessionID string    `json:"session_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// loadSession reads the local session file. A missing file is not an error —
// it means "not logged in yet" — but it is reported distinctly via
// errSessionNotFound so callers that require a session (as opposed to `aff
// login` itself) can give a specific message instead of a raw os.ErrNotExist.
var errSessionNotFound = errors.New("aff: no local session; run `aff login`")

func loadSession(path string) (*sessionData, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("aff: reading session file: %w", err)
	}
	var sd sessionData
	if err := json.Unmarshal(b, &sd); err != nil {
		return nil, fmt.Errorf("aff: parsing session file %s: %w", path, err)
	}
	return &sd, nil
}

// saveSession writes the session file at mode 0600 — it holds a bearer
// token functionally equivalent to a live login, so it must never be
// group- or world-readable. The parent directory is created 0700 for the
// same reason: a session file inside a world-readable directory is still
// exposed if the directory's own listing or an intermediate symlink leaks
// it, though the file mode is the load-bearing control.
//
// It writes to a temp file in the same directory and renames over the
// target, so a process interrupted mid-write never leaves a half-written
// (and possibly still-0600-but-truncated) session file that later reads as
// valid JSON with a zero-value token.
func saveSession(path string, sd *sessionData) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("aff: creating session directory: %w", err)
	}
	b, err := json.MarshalIndent(sd, "", "  ")
	if err != nil {
		return fmt.Errorf("aff: encoding session: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("aff: creating temp session file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once the rename below succeeds

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("aff: writing session file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("aff: closing session file: %w", err)
	}
	// os.CreateTemp defaults to 0600, but that default is not part of any
	// documented contract, so it is asserted explicitly rather than assumed.
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("aff: setting session file permissions: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("aff: installing session file: %w", err)
	}
	return nil
}

// clearSession removes the local session file (`aff login` failure cleanup,
// and a future `aff logout`). Removing a file that is already gone is not
// an error — the end state is identical.
func clearSession(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("aff: removing session file: %w", err)
	}
	return nil
}

// sessionCreds implements grpc/credentials.PerRPCCredentials, attaching the
// stored session token to every outgoing RPC under rpc.SessionTokenHeader. An
// empty token attaches nothing rather than an empty header, so an
// unauthenticated call (e.g. `aff login` itself, before any session exists)
// is indistinguishable on the wire from one that simply has no credential to
// send.
type sessionCreds struct {
	token string
}

func (c sessionCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	if c.token == "" {
		return nil, nil
	}
	return map[string]string{rpc.SessionTokenHeader: c.token}, nil
}

// RequireTransportSecurity is false: the admin listener is plaintext gRPC
// bound to 127.0.0.1 behind nginx and an IP allowlist (PLAN.md §4's "Network"
// paragraph) — TLS terminates at nginx, not this connection, so requiring it
// here would make every local/tunnelled invocation fail to dial.
func (c sessionCreds) RequireTransportSecurity() bool { return false }
