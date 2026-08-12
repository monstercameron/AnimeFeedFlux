package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// run_cmd_test.go covers `aff run` and `aff runs`, which were the least
// covered commands in the CLI (54.7% and 48.5%) and the only two that consume
// a SERVER STREAM.
//
// The streaming loop is the part worth testing. `aff run` starts a billable
// generation run and then follows it to completion, and every one of its exit
// paths means something different to whoever is watching: a failed run must
// exit non-zero (a cron wrapper needs to know), a stream that ends without
// ever reporting a status must not be mistaken for success, and a terminal
// status must stop the loop rather than block on a stream the server has
// stopped writing to.

// --- stream + client fakes ------------------------------------------------

// fakeWatchStream replays a fixed list of events, then EOF. err, if set, is
// returned instead of the event at errAt.
type fakeWatchStream struct {
	grpc.ClientStream
	events []*affv1.RunServiceWatchResponse
	i      int
	err    error
	errAt  int
}

func (s *fakeWatchStream) Recv() (*affv1.RunServiceWatchResponse, error) {
	if s.err != nil && s.i == s.errAt {
		return nil, s.err
	}
	if s.i >= len(s.events) {
		return nil, io.EOF
	}
	ev := s.events[s.i]
	s.i++
	return ev, nil
}

func (s *fakeWatchStream) Context() context.Context { return context.Background() }

type fakeRunClient struct {
	affv1.RunServiceClient
	watch   func(ctx context.Context, req *affv1.RunServiceWatchRequest, opts ...grpc.CallOption) (affv1.RunService_WatchClient, error)
	history func(ctx context.Context, req *affv1.RunServiceHistoryRequest, opts ...grpc.CallOption) (*affv1.RunServiceHistoryResponse, error)
}

func (f *fakeRunClient) Watch(ctx context.Context, req *affv1.RunServiceWatchRequest, opts ...grpc.CallOption) (affv1.RunService_WatchClient, error) {
	return f.watch(ctx, req, opts...)
}

func (f *fakeRunClient) History(ctx context.Context, req *affv1.RunServiceHistoryRequest, opts ...grpc.CallOption) (*affv1.RunServiceHistoryResponse, error) {
	return f.history(ctx, req, opts...)
}

// runFeedClient satisfies the two FeedService calls `aff run` makes.
type runFeedClient struct {
	affv1.FeedServiceClient
	get    func(ctx context.Context, req *affv1.FeedServiceGetRequest, opts ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error)
	runNow func(ctx context.Context, req *affv1.FeedServiceRunNowRequest, opts ...grpc.CallOption) (*affv1.FeedServiceRunNowResponse, error)
}

func (f *runFeedClient) Get(ctx context.Context, req *affv1.FeedServiceGetRequest, opts ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error) {
	return f.get(ctx, req, opts...)
}

func (f *runFeedClient) RunNow(ctx context.Context, req *affv1.FeedServiceRunNowRequest, opts ...grpc.CallOption) (*affv1.FeedServiceRunNowResponse, error) {
	return f.runNow(ctx, req, opts...)
}

// newRunApp wires an app whose Feed.Get resolves any slug to feed 7 and whose
// RunNow returns run 42, leaving each test to supply only the stream.
func newRunApp(t *testing.T, stream affv1.RunService_WatchClient, watchErr error) (*app, *strings.Builder, *strings.Builder) {
	t.Helper()
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	a := &app{
		Stdout: stdout,
		Stderr: stderr,
		Stdin:  strings.NewReader(""),
		clients: &clientBundle{
			Feed: &runFeedClient{
				get: func(context.Context, *affv1.FeedServiceGetRequest, ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error) {
					return &affv1.FeedServiceGetResponse{Feed: &affv1.Feed{Id: 7, Slug: "daily"}}, nil
				},
				runNow: func(context.Context, *affv1.FeedServiceRunNowRequest, ...grpc.CallOption) (*affv1.FeedServiceRunNowResponse, error) {
					return &affv1.FeedServiceRunNowResponse{RunId: 42}, nil
				},
			},
			Run: &fakeRunClient{
				watch: func(context.Context, *affv1.RunServiceWatchRequest, ...grpc.CallOption) (affv1.RunService_WatchClient, error) {
					return stream, watchErr
				},
			},
		},
	}
	return a, stdout, stderr
}

func watchEvent(status affv1.RunStatus, added, rejected int32, lines ...string) *affv1.RunServiceWatchResponse {
	return &affv1.RunServiceWatchResponse{
		Run: &affv1.Run{
			Id: 42, Status: status,
			ItemsAdded: added, ItemsRejected: rejected,
		},
		LogLines: lines,
	}
}

// --- aff run --------------------------------------------------------------

func TestRunStreamsLogLinesAndReportsSuccess(t *testing.T) {
	stream := &fakeWatchStream{events: []*affv1.RunServiceWatchResponse{
		watchEvent(affv1.RunStatus_RUN_STATUS_RUNNING, 0, 0, "generating 2 candidates"),
		watchEvent(affv1.RunStatus_RUN_STATUS_SUCCEEDED, 2, 1, "committed"),
	}}
	a, stdout, stderr := newRunApp(t, stream, nil)

	if code := a.run([]string{"run", "daily"}); code != exitOK {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"generating 2 candidates", "committed", "run 42 finished", "added 2", "rejected 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
}

// TestRunExitsNonZeroOnAFailedRun is the property a cron wrapper depends on:
// the command SUCCEEDED at watching, but the run itself failed, and those two
// must not be reported the same way.
func TestRunExitsNonZeroOnAFailedRun(t *testing.T) {
	stream := &fakeWatchStream{events: []*affv1.RunServiceWatchResponse{
		watchEvent(affv1.RunStatus_RUN_STATUS_FAILED, 0, 0, "provider returned 500"),
	}}
	a, stdout, _ := newRunApp(t, stream, nil)

	if code := a.run([]string{"run", "daily"}); code != exitFail {
		t.Errorf("exit = %d for a failed run, want %d", code, exitFail)
	}
	if !strings.Contains(stdout.String(), "FAILED") && !strings.Contains(stdout.String(), "failed") {
		t.Errorf("stdout does not report the failure:\n%s", stdout.String())
	}
}

// A skipped run is not a failure: a budget cap stopping a run before the
// provider was called is the system working as designed.
func TestRunTreatsASkippedRunAsSuccess(t *testing.T) {
	stream := &fakeWatchStream{events: []*affv1.RunServiceWatchResponse{
		watchEvent(affv1.RunStatus_RUN_STATUS_SKIPPED, 0, 0),
	}}
	a, _, stderr := newRunApp(t, stream, nil)
	if code := a.run([]string{"run", "daily"}); code != exitOK {
		t.Errorf("exit = %d for a skipped run, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
}

func TestRunStopsAtTheFirstTerminalStatus(t *testing.T) {
	// A third event after the terminal one must never be read: the server
	// stops writing after a terminal status, so a loop that keeps reading
	// would block forever against a real stream.
	stream := &fakeWatchStream{events: []*affv1.RunServiceWatchResponse{
		watchEvent(affv1.RunStatus_RUN_STATUS_RUNNING, 0, 0),
		watchEvent(affv1.RunStatus_RUN_STATUS_SUCCEEDED, 1, 0),
		watchEvent(affv1.RunStatus_RUN_STATUS_RUNNING, 0, 0, "SHOULD NEVER BE READ"),
	}}
	a, stdout, _ := newRunApp(t, stream, nil)

	if code := a.run([]string{"run", "daily"}); code != exitOK {
		t.Fatalf("exit = %d, want %d", code, exitOK)
	}
	if strings.Contains(stdout.String(), "SHOULD NEVER BE READ") {
		t.Error("the loop kept reading after a terminal status")
	}
	if stream.i != 2 {
		t.Errorf("read %d events, want 2 — it must stop at the terminal one", stream.i)
	}
}

// TestRunFailsWhenTheStreamEndsWithoutAStatus covers the honest-reporting
// branch: an EOF before any run was seen must not be reported as success.
func TestRunFailsWhenTheStreamEndsWithoutAStatus(t *testing.T) {
	a, _, stderr := newRunApp(t, &fakeWatchStream{}, nil)
	if code := a.run([]string{"run", "daily"}); code != exitFail {
		t.Errorf("exit = %d for an empty stream, want %d", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "no run status") {
		t.Errorf("stderr = %q, want it to say the stream carried no status", stderr.String())
	}
}

func TestRunReportsAStreamError(t *testing.T) {
	stream := &fakeWatchStream{
		events: []*affv1.RunServiceWatchResponse{watchEvent(affv1.RunStatus_RUN_STATUS_RUNNING, 0, 0)},
		err:    errors.New("connection reset"),
		errAt:  1,
	}
	a, _, stderr := newRunApp(t, stream, nil)
	if code := a.run([]string{"run", "daily"}); code != exitFail {
		t.Errorf("exit = %d, want %d", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "connection reset") {
		t.Errorf("stderr = %q, want the underlying stream error", stderr.String())
	}
}

func TestRunReportsAWatchSetupError(t *testing.T) {
	a, _, stderr := newRunApp(t, nil, errors.New("watch refused"))
	if code := a.run([]string{"run", "daily"}); code != exitFail {
		t.Errorf("exit = %d, want %d", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "watch refused") {
		t.Errorf("stderr = %q, want the watch error", stderr.String())
	}
}

func TestRunRequiresExactlyOneSlug(t *testing.T) {
	for _, args := range [][]string{{"run"}, {"run", "a", "b"}} {
		a, _, stderr := newRunApp(t, &fakeWatchStream{}, nil)
		if code := a.run(args); code != exitUsage {
			t.Errorf("aff %v exit = %d, want %d", args, code, exitUsage)
		}
		if !strings.Contains(stderr.String(), "one feed slug") {
			t.Errorf("stderr = %q, want it to state the argument requirement", stderr.String())
		}
	}
}

func TestRunReportsAnUnresolvableSlug(t *testing.T) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	a := &app{
		Stdout: stdout, Stderr: stderr, Stdin: strings.NewReader(""),
		clients: &clientBundle{Feed: &runFeedClient{
			get: func(context.Context, *affv1.FeedServiceGetRequest, ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error) {
				return nil, errors.New("no such feed")
			},
		}},
	}
	if code := a.run([]string{"run", "nope"}); code != exitFail {
		t.Errorf("exit = %d, want %d", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "nope") {
		t.Errorf("stderr = %q, want it to name the slug it could not resolve", stderr.String())
	}
}

func TestRunJSONEmitsOneObjectPerEvent(t *testing.T) {
	stream := &fakeWatchStream{events: []*affv1.RunServiceWatchResponse{
		watchEvent(affv1.RunStatus_RUN_STATUS_RUNNING, 0, 0, "line"),
		watchEvent(affv1.RunStatus_RUN_STATUS_SUCCEEDED, 1, 0),
	}}
	a, stdout, stderr := newRunApp(t, stream, nil)
	a.JSON = true

	if code := a.run([]string{"run", "--json", "daily"}); code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if strings.Count(out, "\"run\"") < 2 {
		t.Errorf("expected one JSON object per event:\n%s", out)
	}
	// The human summary line must NOT appear in --json mode; something is
	// parsing this.
	if strings.Contains(out, "finished:") {
		t.Errorf("--json output contains the human summary line:\n%s", out)
	}
}

func TestRunIsTerminalClassifiesEveryStatus(t *testing.T) {
	terminal := []affv1.RunStatus{
		affv1.RunStatus_RUN_STATUS_SUCCEEDED,
		affv1.RunStatus_RUN_STATUS_FAILED,
		affv1.RunStatus_RUN_STATUS_SKIPPED,
	}
	nonTerminal := []affv1.RunStatus{
		affv1.RunStatus_RUN_STATUS_UNSPECIFIED,
		affv1.RunStatus_RUN_STATUS_RUNNING,
	}
	for _, s := range terminal {
		if !runIsTerminal(s) {
			t.Errorf("runIsTerminal(%v) = false, want true", s)
		}
	}
	for _, s := range nonTerminal {
		if runIsTerminal(s) {
			t.Errorf("runIsTerminal(%v) = true, want false — the watch loop would stop early", s)
		}
	}
}

// --- aff runs -------------------------------------------------------------

func newRunsApp(t *testing.T, resp *affv1.RunServiceHistoryResponse, err error) (*app, *strings.Builder, *strings.Builder) {
	t.Helper()
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	a := &app{
		Stdout: stdout, Stderr: stderr, Stdin: strings.NewReader(""),
		clients: &clientBundle{Run: &fakeRunClient{
			history: func(context.Context, *affv1.RunServiceHistoryRequest, ...grpc.CallOption) (*affv1.RunServiceHistoryResponse, error) {
				return resp, err
			},
		}},
	}
	return a, stdout, stderr
}

func TestRunsListsHistory(t *testing.T) {
	a, stdout, stderr := newRunsApp(t, &affv1.RunServiceHistoryResponse{
		Runs: []*affv1.Run{
			{Id: 1, Status: affv1.RunStatus_RUN_STATUS_SUCCEEDED, ItemsAdded: 3},
			{Id: 2, Status: affv1.RunStatus_RUN_STATUS_FAILED},
		},
	}, nil)

	if code := a.run([]string{"runs"}); code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	for _, want := range []string{"1", "2"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output missing run %s:\n%s", want, stdout.String())
		}
	}
}

func TestRunsReportsAnEmptyHistoryWithoutFailing(t *testing.T) {
	a, _, stderr := newRunsApp(t, &affv1.RunServiceHistoryResponse{}, nil)
	if code := a.run([]string{"runs"}); code != exitOK {
		t.Errorf("exit = %d for an empty history, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
}

func TestRunsReportsARequestError(t *testing.T) {
	a, _, stderr := newRunsApp(t, nil, errors.New("history unavailable"))
	if code := a.run([]string{"runs"}); code != exitFail {
		t.Errorf("exit = %d, want %d", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "history unavailable") {
		t.Errorf("stderr = %q, want the underlying error", stderr.String())
	}
}

func TestRunsRejectsPositionalArguments(t *testing.T) {
	a, _, _ := newRunsApp(t, &affv1.RunServiceHistoryResponse{}, nil)
	if code := a.run([]string{"runs", "stray"}); code != exitUsage {
		t.Errorf("exit = %d for a stray positional argument, want %d", code, exitUsage)
	}
}

var _ = metadata.MD{} // keep the metadata import if fakes_test.go changes shape
