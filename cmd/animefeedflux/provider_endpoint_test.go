package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
)

// stubProvider stands in for the boot-time provider so these tests can assert
// WHICH provider a call reached without any network at all (RULE-1).
type stubProvider struct {
	name  string
	calls int
}

func (p *stubProvider) Generate(context.Context, llm.Request) (llm.Result, error) {
	p.calls++
	return llm.Result{}, nil
}

func (p *stubProvider) Embed(context.Context, []string) ([][]float32, error) {
	p.calls++
	return nil, errors.New("no embeddings here")
}

func (p *stubProvider) Name() string { return p.name }

// With no resolver — the shape every test and any deployment that has not
// touched profiles ends up in — every call goes to the boot-time provider.
func TestResolvingProviderFallsBackWithNoResolver(t *testing.T) {
	fallback := &stubProvider{name: "openai"}
	p := newResolvingProvider(nil, fallback, slog.New(slog.DiscardHandler))

	if _, err := p.Generate(t.Context(), llm.Request{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := p.Embed(t.Context(), []string{"x"}); err == nil {
		t.Fatal("embed through the wrapper did not reach the fallback's error")
	}
	if fallback.calls != 2 {
		t.Fatalf("the fallback saw %d calls, want 2", fallback.calls)
	}
	// Name must not vary with the endpoint: it identifies the backend
	// protocol, and it is recorded against runs.
	if p.Name() != "openai" {
		t.Fatalf("name = %q", p.Name())
	}
}

// Model and Dim describe the vectors in the store, not the endpoint that
// produced them. A profile switch must not change either, because two
// models' vectors are not comparable and the novelty gate compares them.
func TestResolvingEmbedderReportsAStableModelAndDim(t *testing.T) {
	e := newResolvingEmbedder(nil, nil, "text-embedding-3-small", 1536, slog.New(slog.DiscardHandler))
	if e.Model() != "text-embedding-3-small" {
		t.Errorf("model = %q", e.Model())
	}
	if e.Dim() != 1536 {
		t.Errorf("dim = %d", e.Dim())
	}
}
