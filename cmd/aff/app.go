// Command aff is the control-plane CLI: a gRPC client against the exact same
// AuthService/FeedService/ItemService/RunService/SampleService/SystemService
// the admin UI will call (PLAN.md §11, §18 B3). There is no privileged back
// door — every workflow below round-trips through the RPC layer, so by the
// time the UI exists every RPC it needs has already been exercised here.
//
// The only two exceptions are `aff admin init` and `aff admin reset`
// (admin_cmd.go), which are deliberately local-only: they write to the
// SQLite file directly because they are the recovery path for when the
// server itself cannot be reached (PLAN.md §12.2).
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// Exit codes. 0/1 are the conventional success/failure pair; 2 marks a usage
// error (bad flags, unknown subcommand) so a caller can tell "you invoked me
// wrong" apart from "the request you made correctly failed".
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

// clientBundle groups the six generated service clients. It is exactly the
// generated interfaces, not a narrower hand-written subset, so a fake used
// in tests is a drop-in implementation of the real gRPC-generated contract
// rather than a second, driftable copy of it.
type clientBundle struct {
	Auth   affv1.AuthServiceClient
	Feed   affv1.FeedServiceClient
	Item   affv1.ItemServiceClient
	Run    affv1.RunServiceClient
	Sample affv1.SampleServiceClient
	System affv1.SystemServiceClient
}

// app carries everything a subcommand needs, injected rather than read from
// globals, so tests can run the real command code against fake clients and a
// buffer instead of a terminal and a socket (PLAN.md §17.1's "no network
// calls by design").
type app struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	Now    func() time.Time

	Server      string // admin gRPC address, e.g. 127.0.0.1:9091
	SessionFile string // path to the local session file (session.go)
	DBPath      string // local SQLite path, for `admin init`/`admin reset` only
	JSON        bool   // --json: machine-readable output where a human would pipe it

	// clients is pre-populated by tests (fakes) or lazily filled by dial()
	// in production. Exactly one of the two is ever used per app value.
	clients *clientBundle
	closer  func() error
	dial    func(ctx context.Context) (*clientBundle, func() error, error)

	stdinBuf *bufio.Reader
}

func newApp() *app {
	a := &app{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
		Now:    time.Now,
	}
	a.dial = a.realDial
	return a
}

// client returns the connected client bundle, dialing lazily on first use so
// commands that never touch the network (e.g. `aff admin init`, bad-flag
// errors) never open a socket.
func (a *app) client(ctx context.Context) (*clientBundle, error) {
	if a.clients != nil {
		return a.clients, nil
	}
	if a.dial == nil {
		return nil, fmt.Errorf("aff: no server configured (set --server or AFF_ADMIN_ADDR)")
	}
	cb, closer, err := a.dial(ctx)
	if err != nil {
		return nil, err
	}
	a.clients, a.closer = cb, closer
	return cb, nil
}

// Close releases the dialed connection, if any. Safe to call on an app that
// never dialed.
func (a *app) Close() error {
	if a.closer != nil {
		return a.closer()
	}
	return nil
}

func (a *app) reader() *bufio.Reader {
	if a.stdinBuf == nil {
		a.stdinBuf = bufio.NewReader(a.Stdin)
	}
	return a.stdinBuf
}

// readLine reads one line from stdin, echoed, for prompts that are not
// secrets (TOTP codes, confirmations).
func (a *app) readLine(prompt string) (string, error) {
	fmt.Fprint(a.Stderr, prompt)
	line, err := a.reader().ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readPassword reads one line from stdin with terminal echo suppressed when
// stdin is a real console (term_*.go), and never writes what was typed back
// out. The prompt goes to Stderr, not Stdout, so `--json` output piped to a
// file or another process never has the prompt text mixed into it. When
// stdin is not a terminal — piped input, as every test and a scripted
// `admin init` use — there is no echo to suppress, so it degrades to a plain
// read and says so on Stderr, since a password read from a pipe may land in
// shell history or a script's own logs and the caller should know that.
func (a *app) readPassword(prompt string) (string, error) {
	fmt.Fprint(a.Stderr, prompt)
	if f, ok := a.Stdin.(*os.File); ok && isTerminal(f) {
		restore, err := setEcho(f, false)
		defer restore()
		if err != nil {
			return "", fmt.Errorf("aff: disabling terminal echo: %w", err)
		}
		line, err := a.reader().ReadString('\n')
		fmt.Fprintln(a.Stderr)
		if err != nil && err != io.EOF {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprintln(a.Stderr, "\naff: stdin is not a terminal, echo cannot be suppressed")
	line, err := a.reader().ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func main() {
	a := newApp()
	os.Exit(a.run(os.Args[1:]))
}
