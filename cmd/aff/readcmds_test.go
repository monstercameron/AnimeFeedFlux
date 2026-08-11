package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// --- loadProtoJSON ----------------------------------------------------------

func TestLoadProtoJSONSourcesAndFailures(t *testing.T) {
	// protojson, not encoding/json, is what makes `--spec-json` accept enum
	// names instead of raw integers — an operator writing a recipe by hand
	// has no reason to know FEED_KIND_GROUNDED is 2.
	t.Run("inline JSON", func(t *testing.T) {
		spec := &affv1.FeedSpec{}
		if err := loadProtoJSON(`{"cron":"0 9 * * *","itemsPerRun":3}`, "", spec); err != nil {
			t.Fatalf("loadProtoJSON: %v", err)
		}
		if spec.GetCron() != "0 9 * * *" || spec.GetItemsPerRun() != 3 {
			t.Errorf("spec = %+v", spec)
		}
	})

	t.Run("from a file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "spec.json")
		if err := os.WriteFile(path, []byte(`{"model":"gpt-4o"}`), 0o600); err != nil {
			t.Fatalf("writing the fixture: %v", err)
		}
		spec := &affv1.FeedSpec{}
		if err := loadProtoJSON("", path, spec); err != nil {
			t.Fatalf("loadProtoJSON: %v", err)
		}
		if spec.GetModel() != "gpt-4o" {
			t.Errorf("spec = %+v", spec)
		}
	})

	t.Run("neither flag leaves the message alone", func(t *testing.T) {
		spec := &affv1.FeedSpec{Model: "unchanged"}
		if err := loadProtoJSON("", "", spec); err != nil {
			t.Fatalf("loadProtoJSON: %v", err)
		}
		if spec.GetModel() != "unchanged" {
			t.Errorf("an absent flag overwrote the message: %+v", spec)
		}
	})

	t.Run("inline wins over a file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "spec.json")
		if err := os.WriteFile(path, []byte(`{"model":"from-file"}`), 0o600); err != nil {
			t.Fatalf("writing the fixture: %v", err)
		}
		spec := &affv1.FeedSpec{}
		if err := loadProtoJSON(`{"model":"inline"}`, path, spec); err != nil {
			t.Fatalf("loadProtoJSON: %v", err)
		}
		if spec.GetModel() != "inline" {
			t.Errorf("model = %q, want the inline value", spec.GetModel())
		}
	})

	t.Run("failures name what went wrong", func(t *testing.T) {
		if err := loadProtoJSON("{not json", "", &affv1.FeedSpec{}); err == nil {
			t.Error("malformed JSON was accepted")
		}
		// An unknown field is rejected rather than ignored: a typo like
		// "itemPerRun" silently doing nothing is how a recipe ends up
		// running with defaults nobody chose.
		if err := loadProtoJSON(`{"noSuchField":1}`, "", &affv1.FeedSpec{}); err == nil {
			t.Error("an unknown field was accepted")
		}
		if err := loadProtoJSON("", filepath.Join(t.TempDir(), "missing.json"), &affv1.FeedSpec{}); err == nil {
			t.Error("a missing file was accepted")
		}
	})
}

// --- sample / promote --------------------------------------------------------

type cmdSampleClient struct {
	affv1.SampleServiceClient
	sample func(context.Context, *affv1.SampleServiceSampleRequest, ...grpc.CallOption) (*affv1.SampleServiceSampleResponse, error)
}

func (f *cmdSampleClient) Sample(ctx context.Context, req *affv1.SampleServiceSampleRequest, opts ...grpc.CallOption) (*affv1.SampleServiceSampleResponse, error) {
	return f.sample(ctx, req, opts...)
}

// promoteItemClient carries PromoteSample, which lives on ItemService (the
// promotion writes an item; the sample is only its source).
type promoteItemClient struct {
	affv1.ItemServiceClient
	promote func(context.Context, *affv1.ItemServicePromoteSampleRequest, ...grpc.CallOption) (*affv1.ItemServicePromoteSampleResponse, error)
}

func (f *promoteItemClient) PromoteSample(ctx context.Context, req *affv1.ItemServicePromoteSampleRequest, opts ...grpc.CallOption) (*affv1.ItemServicePromoteSampleResponse, error) {
	return f.promote(ctx, req, opts...)
}

type slugFeedClient struct {
	affv1.FeedServiceClient
	get func(context.Context, *affv1.FeedServiceGetRequest, ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error)
}

func (f *slugFeedClient) Get(ctx context.Context, req *affv1.FeedServiceGetRequest, opts ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error) {
	return f.get(ctx, req, opts...)
}

func TestSampleResolvesTheSlugAndReportsRemainingBudget(t *testing.T) {
	// The remaining budget is the number that decides whether to sample
	// again, so it belongs in the first line of output, not behind --json.
	var sampleReq *affv1.SampleServiceSampleRequest
	a, stdout, _ := newTestApp()
	a.clients.Feed = &slugFeedClient{
		get: func(_ context.Context, req *affv1.FeedServiceGetRequest, _ ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error) {
			if req.GetSlug() != "trivia-daily" {
				t.Errorf("looked up %q, want trivia-daily", req.GetSlug())
			}
			return &affv1.FeedServiceGetResponse{Feed: &affv1.Feed{Id: 7, Slug: "trivia-daily"}}, nil
		},
	}
	a.clients.Sample = &cmdSampleClient{
		sample: func(_ context.Context, req *affv1.SampleServiceSampleRequest, _ ...grpc.CallOption) (*affv1.SampleServiceSampleResponse, error) {
			sampleReq = req
			return &affv1.SampleServiceSampleResponse{
				SampleId:                "s-1",
				RemainingDailyBudgetUsd: 4.5,
				Candidates:              []*affv1.SampleCandidate{{CandidateId: "c-1", Title: "One"}},
			}, nil
		},
	}

	code := a.cmdSample([]string{"--size", "3", "--temperature", "0.9", "trivia-daily"})
	if code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if sampleReq.GetFeedId() != 7 || sampleReq.GetSampleSize() != 3 || sampleReq.GetTemperatureOverride() != 0.9 {
		t.Errorf("request = %+v", sampleReq)
	}
	out := stdout.String()
	if !strings.Contains(out, "s-1") || !strings.Contains(out, "4.5000") {
		t.Errorf("output is missing the sample id or the remaining budget: %q", out)
	}
}

func TestSampleFailsWhenTheSlugDoesNotResolve(t *testing.T) {
	a, _, stderr := newTestApp()
	a.clients.Feed = &slugFeedClient{
		get: func(context.Context, *affv1.FeedServiceGetRequest, ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error) {
			return nil, errRPCBoom
		},
	}
	a.clients.Sample = &cmdSampleClient{
		sample: func(context.Context, *affv1.SampleServiceSampleRequest, ...grpc.CallOption) (*affv1.SampleServiceSampleResponse, error) {
			t.Error("a sample was requested for a feed that does not resolve — that would spend money")
			return nil, nil
		},
	}
	if code := a.cmdSample([]string{"nope"}); code != exitFail {
		t.Errorf("exit code = %d, want exitFail", code)
	}
	if !strings.Contains(stderr.String(), "nope") {
		t.Errorf("stderr does not name the slug: %q", stderr.String())
	}
}

func TestSampleRequiresExactlyOneSlug(t *testing.T) {
	for name, args := range map[string][]string{
		"none": {},
		"two":  {"a", "b"},
	} {
		t.Run(name, func(t *testing.T) {
			a, _, _ := newTestApp()
			a.clients.Sample = &cmdSampleClient{}
			if code := a.cmdSample(args); code != exitUsage {
				t.Errorf("exit code = %d, want exitUsage", code)
			}
		})
	}
}

func TestPromoteRequiresACandidate(t *testing.T) {
	// ListSamples returns counts, not candidate ids, so there is nothing to
	// guess from — promoting "the" candidate of a multi-candidate sample
	// would publish whichever one happened to be first.
	a, _, stderr := newTestApp()
	a.clients.Item = &promoteItemClient{
		promote: func(context.Context, *affv1.ItemServicePromoteSampleRequest, ...grpc.CallOption) (*affv1.ItemServicePromoteSampleResponse, error) {
			t.Error("promote ran without a candidate id")
			return nil, nil
		},
	}
	if code := a.cmdPromote([]string{"s-1"}); code != exitUsage {
		t.Errorf("exit code = %d, want exitUsage", code)
	}
	if !strings.Contains(stderr.String(), "--candidate") {
		t.Errorf("stderr does not say which flag is missing: %q", stderr.String())
	}

	var got *affv1.ItemServicePromoteSampleRequest
	a, _, _ = newTestApp()
	a.clients.Item = &promoteItemClient{
		promote: func(_ context.Context, req *affv1.ItemServicePromoteSampleRequest, _ ...grpc.CallOption) (*affv1.ItemServicePromoteSampleResponse, error) {
			got = req
			return &affv1.ItemServicePromoteSampleResponse{Item: &affv1.Item{Id: 11}}, nil
		},
	}
	if code := a.cmdPromote([]string{"--candidate", "c-1", "s-1"}); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got.GetCandidateId() != "c-1" {
		t.Errorf("request = %+v", got)
	}
}

// --- item list / get ---------------------------------------------------------

type listItemClient struct {
	affv1.ItemServiceClient
	list func(context.Context, *affv1.ItemServiceListRequest, ...grpc.CallOption) (*affv1.ItemServiceListResponse, error)
	get  func(context.Context, *affv1.ItemServiceGetRequest, ...grpc.CallOption) (*affv1.ItemServiceGetResponse, error)
}

func (f *listItemClient) List(ctx context.Context, req *affv1.ItemServiceListRequest, opts ...grpc.CallOption) (*affv1.ItemServiceListResponse, error) {
	return f.list(ctx, req, opts...)
}

func (f *listItemClient) Get(ctx context.Context, req *affv1.ItemServiceGetRequest, opts ...grpc.CallOption) (*affv1.ItemServiceGetResponse, error) {
	return f.get(ctx, req, opts...)
}

func TestItemListPassesEveryFilterThrough(t *testing.T) {
	// A filter that is parsed and then not sent is the worst kind: the
	// operator sees a plausible list that answers a different question.
	var got *affv1.ItemServiceListRequest
	a, stdout, _ := newTestApp()
	a.clients.Item = &listItemClient{
		list: func(_ context.Context, req *affv1.ItemServiceListRequest, _ ...grpc.CallOption) (*affv1.ItemServiceListResponse, error) {
			got = req
			return &affv1.ItemServiceListResponse{
				Items:         []*affv1.Item{{Id: 11, ItemKey: "k-1", Title: "One"}},
				NextPageToken: "next-token",
			}, nil
		},
	}
	code := a.cmdItemList([]string{
		"--query", "dragon", "--feed", "7", "--origin", "manual",
		"--deleted", "only", "--page-size", "25", "--page-token", "tok",
	})
	if code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got.GetQuery() != "dragon" || got.GetFeedId() != 7 ||
		got.GetOrigin() != affv1.Origin_ORIGIN_MANUAL ||
		got.GetDeletedFilter() != affv1.DeletedFilter_DELETED_FILTER_ONLY_DELETED ||
		got.GetPageSize() != 25 || got.GetPageToken() != "tok" {
		t.Errorf("request = %+v", got)
	}
	// The next page token has to be visible, or paging by hand is impossible.
	if !strings.Contains(stdout.String(), "next-token") {
		t.Errorf("output does not carry the next page token: %q", stdout.String())
	}
}

func TestItemListRejectsBadFilterValues(t *testing.T) {
	for name, args := range map[string][]string{
		"bad origin":  {"--origin", "invented"},
		"bad deleted": {"--deleted", "sometimes"},
	} {
		t.Run(name, func(t *testing.T) {
			a, _, stderr := newTestApp()
			a.clients.Item = &listItemClient{
				list: func(context.Context, *affv1.ItemServiceListRequest, ...grpc.CallOption) (*affv1.ItemServiceListResponse, error) {
					t.Error("the list RPC ran with an unparseable filter")
					return nil, nil
				},
			}
			if code := a.cmdItemList(args); code != exitUsage {
				t.Errorf("exit code = %d, want exitUsage", code)
			}
			if stderr.Len() == 0 {
				t.Error("nothing on stderr")
			}
		})
	}
}

func TestItemGetTakesANumericID(t *testing.T) {
	var got *affv1.ItemServiceGetRequest
	client := &listItemClient{
		get: func(_ context.Context, req *affv1.ItemServiceGetRequest, _ ...grpc.CallOption) (*affv1.ItemServiceGetResponse, error) {
			got = req
			return &affv1.ItemServiceGetResponse{Item: &affv1.Item{Id: 11, ItemKey: "2026-08-11-one"}}, nil
		},
	}

	a, stdout, _ := newTestApp()
	a.clients.Item = client
	if code := a.cmdItemGet([]string{"11"}); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got.GetId() != 11 {
		t.Errorf("request = %+v, want id 11", got)
	}
	if !strings.Contains(stdout.String(), "2026-08-11-one") {
		t.Errorf("the item was not printed: %q", stdout.String())
	}

	// The argument is an id, not an item key: a key silently parsed as 0
	// would fetch whatever the server returns for id 0 rather than saying
	// "that is not an id".
	for name, args := range map[string][]string{
		"none":        nil,
		"two":         {"11", "12"},
		"non-numeric": {"2026-08-11-one"},
	} {
		a, _, stderr := newTestApp()
		a.clients.Item = &listItemClient{
			get: func(context.Context, *affv1.ItemServiceGetRequest, ...grpc.CallOption) (*affv1.ItemServiceGetResponse, error) {
				t.Errorf("%s: the RPC ran for an unusable argument", name)
				return nil, nil
			},
		}
		if code := a.cmdItemGet(args); code != exitUsage {
			t.Errorf("%s: exit code = %d, want exitUsage", name, code)
		}
		if stderr.Len() == 0 {
			t.Errorf("%s: nothing on stderr", name)
		}
	}
}
