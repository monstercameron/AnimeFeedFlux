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

// The mutating commands had no tests at all. Every one of them takes
// --expected-version (PLAN.md §11), and every one of them is a way to lose
// data if the flag handling is wrong: a command that sends 0 instead of
// refusing would overwrite whatever the other tab just saved, and a command
// that reports success on a failed RPC would tell an operator their
// correction went out when it did not.
//
// These drive the real command code against fake clients — the interface seam
// §17.1 allows in place of a network server — and assert three things per
// command: the required flags are enforced, the right request reaches the
// wire, and a server error is a non-zero exit with something on stderr.

// --- fakes ------------------------------------------------------------------

type cmdFeedClient struct {
	affv1.FeedServiceClient
	update     func(context.Context, *affv1.FeedServiceUpdateRequest, ...grpc.CallOption) (*affv1.FeedServiceUpdateResponse, error)
	setEnabled func(context.Context, *affv1.FeedServiceSetEnabledRequest, ...grpc.CallOption) (*affv1.FeedServiceSetEnabledResponse, error)
	del        func(context.Context, *affv1.FeedServiceDeleteRequest, ...grpc.CallOption) (*affv1.FeedServiceDeleteResponse, error)
	exportTOML func(context.Context, *affv1.FeedServiceExportTOMLRequest, ...grpc.CallOption) (*affv1.FeedServiceExportTOMLResponse, error)
	importTOML func(context.Context, *affv1.FeedServiceImportTOMLRequest, ...grpc.CallOption) (*affv1.FeedServiceImportTOMLResponse, error)
}

func (f *cmdFeedClient) Update(ctx context.Context, req *affv1.FeedServiceUpdateRequest, opts ...grpc.CallOption) (*affv1.FeedServiceUpdateResponse, error) {
	return f.update(ctx, req, opts...)
}

func (f *cmdFeedClient) SetEnabled(ctx context.Context, req *affv1.FeedServiceSetEnabledRequest, opts ...grpc.CallOption) (*affv1.FeedServiceSetEnabledResponse, error) {
	return f.setEnabled(ctx, req, opts...)
}

func (f *cmdFeedClient) Delete(ctx context.Context, req *affv1.FeedServiceDeleteRequest, opts ...grpc.CallOption) (*affv1.FeedServiceDeleteResponse, error) {
	return f.del(ctx, req, opts...)
}

func (f *cmdFeedClient) ExportTOML(ctx context.Context, req *affv1.FeedServiceExportTOMLRequest, opts ...grpc.CallOption) (*affv1.FeedServiceExportTOMLResponse, error) {
	return f.exportTOML(ctx, req, opts...)
}

func (f *cmdFeedClient) ImportTOML(ctx context.Context, req *affv1.FeedServiceImportTOMLRequest, opts ...grpc.CallOption) (*affv1.FeedServiceImportTOMLResponse, error) {
	return f.importTOML(ctx, req, opts...)
}

type cmdItemClient struct {
	affv1.ItemServiceClient
	create     func(context.Context, *affv1.ItemServiceCreateRequest, ...grpc.CallOption) (*affv1.ItemServiceCreateResponse, error)
	update     func(context.Context, *affv1.ItemServiceUpdateRequest, ...grpc.CallOption) (*affv1.ItemServiceUpdateResponse, error)
	del        func(context.Context, *affv1.ItemServiceDeleteRequest, ...grpc.CallOption) (*affv1.ItemServiceDeleteResponse, error)
	restore    func(context.Context, *affv1.ItemServiceRestoreRequest, ...grpc.CallOption) (*affv1.ItemServiceRestoreResponse, error)
	correction func(context.Context, *affv1.ItemServicePublishCorrectionRequest, ...grpc.CallOption) (*affv1.ItemServicePublishCorrectionResponse, error)
}

func (f *cmdItemClient) Create(ctx context.Context, req *affv1.ItemServiceCreateRequest, opts ...grpc.CallOption) (*affv1.ItemServiceCreateResponse, error) {
	return f.create(ctx, req, opts...)
}

func (f *cmdItemClient) Update(ctx context.Context, req *affv1.ItemServiceUpdateRequest, opts ...grpc.CallOption) (*affv1.ItemServiceUpdateResponse, error) {
	return f.update(ctx, req, opts...)
}

func (f *cmdItemClient) Delete(ctx context.Context, req *affv1.ItemServiceDeleteRequest, opts ...grpc.CallOption) (*affv1.ItemServiceDeleteResponse, error) {
	return f.del(ctx, req, opts...)
}

func (f *cmdItemClient) Restore(ctx context.Context, req *affv1.ItemServiceRestoreRequest, opts ...grpc.CallOption) (*affv1.ItemServiceRestoreResponse, error) {
	return f.restore(ctx, req, opts...)
}

func (f *cmdItemClient) PublishCorrection(ctx context.Context, req *affv1.ItemServicePublishCorrectionRequest, opts ...grpc.CallOption) (*affv1.ItemServicePublishCorrectionResponse, error) {
	return f.correction(ctx, req, opts...)
}

// --- feed update / enable / disable / delete ---------------------------------

func TestFeedUpdateRequiresIDAndVersion(t *testing.T) {
	cases := map[string][]string{
		"no id":      {"--expected-version", "2"},
		"no version": {"--id", "7"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			a, _, stderr := newTestApp()
			a.clients.Feed = &cmdFeedClient{update: func(context.Context, *affv1.FeedServiceUpdateRequest, ...grpc.CallOption) (*affv1.FeedServiceUpdateResponse, error) {
				t.Error("the RPC was made despite a missing required flag")
				return nil, nil
			}}
			if code := a.cmdFeedUpdate(args); code != exitUsage {
				t.Errorf("exit code = %d, want exitUsage", code)
			}
			if stderr.Len() == 0 {
				t.Error("nothing was written to stderr to say what was missing")
			}
		})
	}
}

func TestFeedUpdateSendsTheVersionItWasGiven(t *testing.T) {
	var got *affv1.FeedServiceUpdateRequest
	a, stdout, _ := newTestApp()
	a.clients.Feed = &cmdFeedClient{
		update: func(_ context.Context, req *affv1.FeedServiceUpdateRequest, _ ...grpc.CallOption) (*affv1.FeedServiceUpdateResponse, error) {
			got = req
			return &affv1.FeedServiceUpdateResponse{Feed: &affv1.Feed{Id: 7, Slug: "trivia", Version: 3}}, nil
		},
	}
	code := a.cmdFeedUpdate([]string{"--id", "7", "--expected-version", "2", "--title", "New Title"})
	if code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got.GetExpectedVersion() != 2 {
		t.Errorf("expected_version sent = %d, want 2", got.GetExpectedVersion())
	}
	if got.GetFeed().GetId() != 7 || got.GetFeed().GetTitle() != "New Title" {
		t.Errorf("request feed = %+v", got.GetFeed())
	}
	if !strings.Contains(stdout.String(), "trivia") {
		t.Errorf("the updated feed was not printed: %s", stdout.String())
	}
}

func TestFeedUpdateReportsAServerError(t *testing.T) {
	a, _, stderr := newTestApp()
	a.clients.Feed = &cmdFeedClient{
		update: func(context.Context, *affv1.FeedServiceUpdateRequest, ...grpc.CallOption) (*affv1.FeedServiceUpdateResponse, error) {
			return nil, errRPCBoom
		},
	}
	if code := a.cmdFeedUpdate([]string{"--id", "7", "--expected-version", "2"}); code != exitFail {
		t.Errorf("exit code = %d, want exitFail", code)
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Errorf("stderr does not carry the failure: %q", stderr.String())
	}
}

func TestFeedEnableAndDisableSendTheRightFlag(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
	}{{"enable", true}, {"disable", false}} {
		t.Run(tc.name, func(t *testing.T) {
			var got *affv1.FeedServiceSetEnabledRequest
			a, _, _ := newTestApp()
			a.clients.Feed = &cmdFeedClient{
				setEnabled: func(_ context.Context, req *affv1.FeedServiceSetEnabledRequest, _ ...grpc.CallOption) (*affv1.FeedServiceSetEnabledResponse, error) {
					got = req
					return &affv1.FeedServiceSetEnabledResponse{Feed: &affv1.Feed{Id: 7}}, nil
				},
			}
			if code := a.cmdFeedSetEnabled([]string{"--expected-version", "4", "7"}, tc.enabled); code != exitOK {
				t.Fatalf("exit code = %d", code)
			}
			if got.GetEnabled() != tc.enabled {
				t.Errorf("enabled sent = %v, want %v", got.GetEnabled(), tc.enabled)
			}
			if got.GetFeedId() != 7 || got.GetExpectedVersion() != 4 {
				t.Errorf("request = %+v", got)
			}
		})
	}
}

func TestFeedSetEnabledRejectsBadArguments(t *testing.T) {
	cases := map[string][]string{
		"no id":           {"--expected-version", "4"},
		"two ids":         {"--expected-version", "4", "7", "8"},
		"non-numeric id":  {"--expected-version", "4", "seven"},
		"missing version": {"7"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			a, _, stderr := newTestApp()
			a.clients.Feed = &cmdFeedClient{}
			if code := a.cmdFeedSetEnabled(args, true); code != exitUsage {
				t.Errorf("exit code = %d, want exitUsage", code)
			}
			if stderr.Len() == 0 {
				t.Error("nothing on stderr")
			}
		})
	}
}

func TestFeedDeleteRequiresAVersionAndOneID(t *testing.T) {
	a, _, _ := newTestApp()
	a.clients.Feed = &cmdFeedClient{}
	if code := a.cmdFeedDelete([]string{"7"}); code != exitUsage {
		t.Errorf("deleting without --expected-version exited %d, want exitUsage", code)
	}

	var got *affv1.FeedServiceDeleteRequest
	a, _, _ = newTestApp()
	a.clients.Feed = &cmdFeedClient{
		del: func(_ context.Context, req *affv1.FeedServiceDeleteRequest, _ ...grpc.CallOption) (*affv1.FeedServiceDeleteResponse, error) {
			got = req
			return &affv1.FeedServiceDeleteResponse{}, nil
		},
	}
	if code := a.cmdFeedDelete([]string{"--expected-version", "5", "7"}); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got.GetFeedId() != 7 || got.GetExpectedVersion() != 5 {
		t.Errorf("request = %+v", got)
	}
}

// --- item create / update / delete / restore / correct -----------------------

func TestItemCreateRequiresFeedAndTitle(t *testing.T) {
	for name, args := range map[string][]string{
		"no feed":  {"--title", "x"},
		"no title": {"--feed", "7"},
	} {
		t.Run(name, func(t *testing.T) {
			a, _, stderr := newTestApp()
			a.clients.Item = &cmdItemClient{}
			if code := a.cmdItemCreate(args); code != exitUsage {
				t.Errorf("exit code = %d, want exitUsage", code)
			}
			if stderr.Len() == 0 {
				t.Error("nothing on stderr")
			}
		})
	}
}

func TestItemCreateSendsEveryField(t *testing.T) {
	var got *affv1.ItemServiceCreateRequest
	a, _, _ := newTestApp()
	a.clients.Item = &cmdItemClient{
		create: func(_ context.Context, req *affv1.ItemServiceCreateRequest, _ ...grpc.CallOption) (*affv1.ItemServiceCreateResponse, error) {
			got = req
			return &affv1.ItemServiceCreateResponse{Item: &affv1.Item{Id: 11}}, nil
		},
	}
	code := a.cmdItemCreate([]string{
		"--feed", "7", "--title", "An item", "--summary", "sum",
		"--body", "<p>b</p>", "--link", "https://example.com/x",
	})
	if code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	it := got.GetItem()
	if it.GetFeedId() != 7 || it.GetTitle() != "An item" || it.GetSummaryText() != "sum" ||
		it.GetBodyHtml() != "<p>b</p>" || it.GetLink() != "https://example.com/x" {
		t.Errorf("a field was dropped on the way to the wire: %+v", it)
	}
}

func TestItemUpdateRequiresIDAndVersion(t *testing.T) {
	for name, args := range map[string][]string{
		"no id":      {"--expected-version", "2"},
		"no version": {"--id", "11"},
	} {
		t.Run(name, func(t *testing.T) {
			a, _, _ := newTestApp()
			a.clients.Item = &cmdItemClient{}
			if code := a.cmdItemUpdate(args); code != exitUsage {
				t.Errorf("exit code = %d, want exitUsage", code)
			}
		})
	}

	var got *affv1.ItemServiceUpdateRequest
	a, _, _ := newTestApp()
	a.clients.Item = &cmdItemClient{
		update: func(_ context.Context, req *affv1.ItemServiceUpdateRequest, _ ...grpc.CallOption) (*affv1.ItemServiceUpdateResponse, error) {
			got = req
			return &affv1.ItemServiceUpdateResponse{Item: &affv1.Item{Id: 11}}, nil
		},
	}
	if code := a.cmdItemUpdate([]string{"--id", "11", "--expected-version", "2", "--title", "T"}); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got.GetExpectedVersion() != 2 || got.GetItem().GetId() != 11 {
		t.Errorf("request = %+v", got)
	}
}

func TestItemDeleteSaysWhatDeleteMeansHere(t *testing.T) {
	// There is no hard delete: the permalink 410s forever and the guid is
	// never reused. Printing a bare "deleted" invites the wrong mental model
	// and the wrong next action (trying to recreate the item at the same
	// key).
	var got *affv1.ItemServiceDeleteRequest
	a, stdout, _ := newTestApp()
	a.clients.Item = &cmdItemClient{
		del: func(_ context.Context, req *affv1.ItemServiceDeleteRequest, _ ...grpc.CallOption) (*affv1.ItemServiceDeleteResponse, error) {
			got = req
			return &affv1.ItemServiceDeleteResponse{}, nil
		},
	}
	if code := a.cmdItemDelete([]string{"--expected-version", "3", "11"}); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got.GetItemId() != 11 || got.GetExpectedVersion() != 3 {
		t.Errorf("request = %+v", got)
	}
	out := stdout.String()
	if !strings.Contains(out, "410") || !strings.Contains(strings.ToLower(out), "guid") {
		t.Errorf("output does not explain what a delete does here: %q", out)
	}

	// --json is what a script reads; it must be machine-shaped, not prose.
	a, stdout, _ = newTestApp()
	a.JSON = true
	a.clients.Item = &cmdItemClient{
		del: func(context.Context, *affv1.ItemServiceDeleteRequest, ...grpc.CallOption) (*affv1.ItemServiceDeleteResponse, error) {
			return &affv1.ItemServiceDeleteResponse{}, nil
		},
	}
	if code := a.cmdItemDelete([]string{"--expected-version", "3", "11"}); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), `"deleted"`) {
		t.Errorf("--json output = %q", stdout.String())
	}
}

func TestItemRestoreAndCorrect(t *testing.T) {
	t.Run("restore", func(t *testing.T) {
		var got *affv1.ItemServiceRestoreRequest
		a, _, _ := newTestApp()
		a.clients.Item = &cmdItemClient{
			restore: func(_ context.Context, req *affv1.ItemServiceRestoreRequest, _ ...grpc.CallOption) (*affv1.ItemServiceRestoreResponse, error) {
				got = req
				return &affv1.ItemServiceRestoreResponse{Item: &affv1.Item{Id: 11}}, nil
			},
		}
		if code := a.cmdItemRestore([]string{"--expected-version", "3", "11"}); code != exitOK {
			t.Fatalf("exit code = %d", code)
		}
		if got.GetItemId() != 11 || got.GetExpectedVersion() != 3 {
			t.Errorf("request = %+v", got)
		}

		a, _, _ = newTestApp()
		a.clients.Item = &cmdItemClient{}
		if code := a.cmdItemRestore([]string{"11"}); code != exitUsage {
			t.Errorf("restore without --expected-version exited %d, want exitUsage", code)
		}
	})

	t.Run("correct", func(t *testing.T) {
		// A correction is the only thing that reaches subscribers after the
		// fact (§12.4: RSS has no retraction), so the id it corrects and the
		// text both have to arrive intact.
		var got *affv1.ItemServicePublishCorrectionRequest
		a, _, _ := newTestApp()
		a.clients.Item = &cmdItemClient{
			correction: func(_ context.Context, req *affv1.ItemServicePublishCorrectionRequest, _ ...grpc.CallOption) (*affv1.ItemServicePublishCorrectionResponse, error) {
				got = req
				return &affv1.ItemServicePublishCorrectionResponse{Item: &affv1.Item{Id: 12}}, nil
			},
		}
		code := a.cmdItemCorrect([]string{"--title", "Correction", "--summary", "we got it wrong", "11"})
		if code != exitOK {
			t.Fatalf("exit code = %d", code)
		}
		if got.GetCorrectsItemId() != 11 || got.GetTitle() != "Correction" || got.GetSummaryText() != "we got it wrong" {
			t.Errorf("request = %+v", got)
		}

		for name, args := range map[string][]string{
			"no title":   {"--summary", "s", "11"},
			"no summary": {"--title", "t", "11"},
			"no id":      {"--title", "t", "--summary", "s"},
		} {
			a, _, _ := newTestApp()
			a.clients.Item = &cmdItemClient{}
			if code := a.cmdItemCorrect(args); code != exitUsage {
				t.Errorf("%s: exit code = %d, want exitUsage", name, code)
			}
		}
	})
}

// --- recipe export / import --------------------------------------------------

func TestRecipeExportToStdoutAndFile(t *testing.T) {
	payload := `{"slug":"trivia-daily","title":"Daily Trivia"}`
	newClient := func() *cmdFeedClient {
		return &cmdFeedClient{
			exportTOML: func(_ context.Context, req *affv1.FeedServiceExportTOMLRequest, _ ...grpc.CallOption) (*affv1.FeedServiceExportTOMLResponse, error) {
				if req.GetFeedId() != 7 {
					t.Errorf("feed id sent = %d, want 7", req.GetFeedId())
				}
				return &affv1.FeedServiceExportTOMLResponse{Toml: payload}, nil
			},
		}
	}

	a, stdout, _ := newTestApp()
	a.clients.Feed = newClient()
	if code := a.cmdRecipeExport([]string{"7"}); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if stdout.String() != payload {
		t.Errorf("stdout = %q, want the recipe verbatim", stdout.String())
	}

	// --out writes the same bytes to a file: this is the disaster-recovery
	// copy, so anything the terminal would have mangled must not be applied.
	out := filepath.Join(t.TempDir(), "recipe.json")
	a, _, _ = newTestApp()
	a.clients.Feed = newClient()
	if code := a.cmdRecipeExport([]string{"--out", out, "7"}); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the exported file: %v", err)
	}
	if string(written) != payload {
		t.Errorf("file contents = %q, want the recipe verbatim", written)
	}
}

func TestRecipeExportRejectsBadArguments(t *testing.T) {
	for name, args := range map[string][]string{
		"no id":          {},
		"two ids":        {"7", "8"},
		"non-numeric id": {"seven"},
	} {
		t.Run(name, func(t *testing.T) {
			a, _, _ := newTestApp()
			a.clients.Feed = &cmdFeedClient{}
			if code := a.cmdRecipeExport(args); code != exitUsage {
				t.Errorf("exit code = %d, want exitUsage", code)
			}
		})
	}
}

func TestRecipeImportRequiresAVersionWhenReplacing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recipe.json")
	if err := os.WriteFile(path, []byte(`{"slug":"trivia-daily"}`), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	// Replacing an existing feed without a version would clobber a
	// concurrent edit, so it must be refused before the RPC.
	a, _, stderr := newTestApp()
	a.clients.Feed = &cmdFeedClient{importTOML: func(context.Context, *affv1.FeedServiceImportTOMLRequest, ...grpc.CallOption) (*affv1.FeedServiceImportTOMLResponse, error) {
		t.Error("the import RPC ran without an expected version")
		return nil, nil
	}}
	if code := a.cmdRecipeImport([]string{"--feed-id", "7", path}); code != exitUsage {
		t.Errorf("exit code = %d, want exitUsage", code)
	}
	if stderr.Len() == 0 {
		t.Error("nothing on stderr")
	}

	// Creating a new feed needs no version.
	var got *affv1.FeedServiceImportTOMLRequest
	a, _, _ = newTestApp()
	a.clients.Feed = &cmdFeedClient{
		importTOML: func(_ context.Context, req *affv1.FeedServiceImportTOMLRequest, _ ...grpc.CallOption) (*affv1.FeedServiceImportTOMLResponse, error) {
			got = req
			return &affv1.FeedServiceImportTOMLResponse{Feed: &affv1.Feed{Id: 9, Slug: "trivia-daily"}}, nil
		},
	}
	if code := a.cmdRecipeImport([]string{path}); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(got.GetToml(), "trivia-daily") {
		t.Errorf("the file contents did not reach the wire: %q", got.GetToml())
	}

	// A missing file is a failure, not an empty import that would replace a
	// recipe with nothing.
	a, _, _ = newTestApp()
	a.clients.Feed = &cmdFeedClient{importTOML: func(context.Context, *affv1.FeedServiceImportTOMLRequest, ...grpc.CallOption) (*affv1.FeedServiceImportTOMLResponse, error) {
		t.Error("an unreadable file still reached the RPC")
		return nil, nil
	}}
	if code := a.cmdRecipeImport([]string{filepath.Join(dir, "does-not-exist.json")}); code != exitFail {
		t.Errorf("exit code = %d, want exitFail", code)
	}
}
