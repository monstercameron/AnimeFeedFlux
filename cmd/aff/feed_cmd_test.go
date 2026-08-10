package main

import (
	"context"
	"encoding/json"
	"testing"

	"google.golang.org/grpc"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

func TestFeedListJSONOutputParses(t *testing.T) {
	a, stdout, stderr := newTestApp()
	a.JSON = true

	feed := &fakeFeedClient{
		list: func(ctx context.Context, req *affv1.FeedServiceListRequest, opts ...grpc.CallOption) (*affv1.FeedServiceListResponse, error) {
			return &affv1.FeedServiceListResponse{
				Feeds: []*affv1.Feed{
					{Id: 1, Slug: "trivia-daily", Kind: affv1.FeedKind_FEED_KIND_GENERATIVE, Title: "Trivia Daily", Enabled: true, Version: 3},
				},
			}, nil
		},
	}
	a.clients.Feed = feed

	code := a.run([]string{"feed", "list", "--json"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}

	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("--json output does not parse: %v (stdout: %s)", err, stdout.String())
	}
	feeds, ok := got["feeds"].([]any)
	if !ok || len(feeds) != 1 {
		t.Fatalf("feeds = %#v, want a one-element list", got["feeds"])
	}
	first, _ := feeds[0].(map[string]any)
	if first["kind"] != "FEED_KIND_GENERATIVE" {
		t.Fatalf("kind = %v, want the enum's string name, not a raw number", first["kind"])
	}
	if first["slug"] != "trivia-daily" {
		t.Fatalf("slug = %v, want %q", first["slug"], "trivia-daily")
	}
}

func TestFeedListHumanOutput(t *testing.T) {
	a, stdout, stderr := newTestApp()

	feed := &fakeFeedClient{
		list: func(ctx context.Context, req *affv1.FeedServiceListRequest, opts ...grpc.CallOption) (*affv1.FeedServiceListResponse, error) {
			return &affv1.FeedServiceListResponse{
				Feeds: []*affv1.Feed{
					{Id: 7, Slug: "news-daily", Kind: affv1.FeedKind_FEED_KIND_GROUNDED, Title: "News Daily", Enabled: false},
				},
			}, nil
		},
	}
	a.clients.Feed = feed

	code := a.run([]string{"feed", "list"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	if got := stdout.String(); got == "" {
		t.Fatal("expected non-empty table output")
	}
}

func TestSystemVersionJSONOutputParses(t *testing.T) {
	a, stdout, stderr := newTestApp()
	a.JSON = true

	sys := &fakeSystemClient{
		version: func(ctx context.Context, req *affv1.SystemServiceVersionRequest, opts ...grpc.CallOption) (*affv1.SystemServiceVersionResponse, error) {
			return &affv1.SystemServiceVersionResponse{Version: "v1.2.3", Build: "abcdef"}, nil
		},
	}
	a.clients.System = sys

	code := a.run([]string{"system", "version", "--json"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}

	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("--json output does not parse: %v (stdout: %s)", err, stdout.String())
	}
	if got["version"] != "v1.2.3" {
		t.Fatalf("version = %v, want %q", got["version"], "v1.2.3")
	}
}
