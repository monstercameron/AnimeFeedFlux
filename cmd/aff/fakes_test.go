package main

// Test doubles for the six generated client interfaces. Each fake embeds
// the real (nil) interface so it satisfies the full generated contract
// without hand-writing every method — an unwired method panics on a nil
// interface call, which is exactly the failure mode a test that reaches an
// untested method SHOULD get, loudly, rather than silently returning a zero
// value. Only the methods a given test actually drives are overridden.
//
// This is the "interface seam" PLAN.md's testing note allows in place of a
// real bufconn/network gRPC server (§17.1: "no network calls by design").

import (
	"bytes"
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// setHeader writes md into the first grpc.HeaderCallOption found in opts, if
// any — the same mechanism grpc-go's real client stubs use to deliver
// response header metadata to a caller that passed grpc.Header(&md).
func setHeader(opts []grpc.CallOption, md metadata.MD) {
	for _, o := range opts {
		if h, ok := o.(grpc.HeaderCallOption); ok {
			*h.HeaderAddr = md
		}
	}
}

type fakeAuthClient struct {
	affv1.AuthServiceClient
	login func(ctx context.Context, req *affv1.AuthServiceLoginRequest, opts ...grpc.CallOption) (*affv1.AuthServiceLoginResponse, error)
}

func (f *fakeAuthClient) Login(ctx context.Context, req *affv1.AuthServiceLoginRequest, opts ...grpc.CallOption) (*affv1.AuthServiceLoginResponse, error) {
	return f.login(ctx, req, opts...)
}

type fakeFeedClient struct {
	affv1.FeedServiceClient
	list func(ctx context.Context, req *affv1.FeedServiceListRequest, opts ...grpc.CallOption) (*affv1.FeedServiceListResponse, error)
	get  func(ctx context.Context, req *affv1.FeedServiceGetRequest, opts ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error)
}

func (f *fakeFeedClient) List(ctx context.Context, req *affv1.FeedServiceListRequest, opts ...grpc.CallOption) (*affv1.FeedServiceListResponse, error) {
	return f.list(ctx, req, opts...)
}

func (f *fakeFeedClient) Get(ctx context.Context, req *affv1.FeedServiceGetRequest, opts ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error) {
	return f.get(ctx, req, opts...)
}

type fakeSystemClient struct {
	affv1.SystemServiceClient
	version func(ctx context.Context, req *affv1.SystemServiceVersionRequest, opts ...grpc.CallOption) (*affv1.SystemServiceVersionResponse, error)
}

func (f *fakeSystemClient) Version(ctx context.Context, req *affv1.SystemServiceVersionRequest, opts ...grpc.CallOption) (*affv1.SystemServiceVersionResponse, error) {
	return f.version(ctx, req, opts...)
}

// newTestApp builds an *app wired to fakes (no dial, no network) with
// buffered stdio, ready for a subcommand to run against directly.
func newTestApp() (a *app, stdout, stderr *bytes.Buffer) {
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	a = &app{
		Stdout: stdout,
		Stderr: stderr,
		Stdin:  strings.NewReader(""),
		clients: &clientBundle{
			Auth:   &fakeAuthClient{},
			Feed:   &fakeFeedClient{},
			System: &fakeSystemClient{},
		},
	}
	return a, stdout, stderr
}
