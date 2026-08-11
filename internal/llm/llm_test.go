package llm

import (
	"context"
	"errors"
	"testing"
)

func TestFake_ReturnsScriptedResultsInOrder(t *testing.T) {
	fake := NewFake()
	first := Result{Items: []GeneratedItem{{Title: "One"}}, Model: "gpt-test"}
	second := Result{Items: []GeneratedItem{{Title: "Two"}}, Model: "gpt-test"}
	fake.QueueResult(first)
	fake.QueueResult(second)

	got1, err := fake.Generate(context.Background(), Request{Prompt: "a"})
	if err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	if got1.Items[0].Title != "One" {
		t.Errorf("first result = %+v, want Title=One", got1)
	}

	got2, err := fake.Generate(context.Background(), Request{Prompt: "b"})
	if err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if got2.Items[0].Title != "Two" {
		t.Errorf("second result = %+v, want Title=Two", got2)
	}

	if fake.GenerateCallCount() != 2 {
		t.Fatalf("GenerateCallCount() = %d, want 2", fake.GenerateCallCount())
	}
	calls := fake.GenerateCalls()
	if calls[0].Prompt != "a" || calls[1].Prompt != "b" {
		t.Errorf("GenerateCalls() = %+v, want prompts a then b", calls)
	}
}

func TestFake_NoScriptedResultReturnsClassifiedError(t *testing.T) {
	fake := NewFake()
	_, err := fake.Generate(context.Background(), Request{Prompt: "a"})
	if err == nil {
		t.Fatal("Generate with no queued result should fail")
	}
	var classified *Error
	if !errors.As(err, &classified) {
		t.Fatalf("Generate error = %T, want *Error", err)
	}
}

// TestFake_GenerateWrapsProviderFailureInError is the RULE-1 stand-in for
// exercising SchemaFluxProvider.Generate's error path without a network call:
// Fake.Generate runs a scripted plain error through the same Classify path
// the real provider uses, so every Provider implementation's Generate is
// guaranteed to return *Error, never a bare error, on failure.
func TestFake_GenerateWrapsProviderFailureInError(t *testing.T) {
	fake := NewFake()
	plain := errors.New("connection reset by peer")
	fake.QueueGenerateError(plain)

	_, err := fake.Generate(context.Background(), Request{Prompt: "a"})
	if err == nil {
		t.Fatal("Generate should have returned the scripted error")
	}

	var classified *Error
	if !errors.As(err, &classified) {
		t.Fatalf("Generate returned %T (%v), want it wrapped in *Error", err, err)
	}
	if !errors.Is(err, plain) {
		t.Error("the classified error should still unwrap to the original scripted error")
	}
}

func TestFake_EmbedReturnsScriptedVectorsAndRecordsCalls(t *testing.T) {
	fake := NewFake()
	fake.QueueEmbedResult([][]float32{{0.1, 0.2}, {0.3, 0.4}})

	got, err := fake.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Embed returned %d vectors, want 2", len(got))
	}

	calls := fake.EmbedCalls()
	if len(calls) != 1 || len(calls[0]) != 2 {
		t.Fatalf("EmbedCalls() = %+v, want one call with 2 inputs", calls)
	}
}

func TestFake_EmbedWrapsProviderFailureInError(t *testing.T) {
	fake := NewFake()
	plain := errors.New("model does not support embeddings")
	fake.QueueEmbedError(plain)

	_, err := fake.Embed(context.Background(), []string{"a"})
	var classified *Error
	if !errors.As(err, &classified) {
		t.Fatalf("Embed returned %T, want *Error", err)
	}
}

func TestFake_ImplementsProvider(t *testing.T) {
	var _ Provider = NewFake()
}

func TestBuildSteering(t *testing.T) {
	got := buildSteering(Request{System: "Be concise.", MaxItems: 3})
	want := "Be concise. Return at most 3 items in the \"items\" array."
	if got != want {
		t.Errorf("buildSteering() = %q, want %q", got, want)
	}

	if got := buildSteering(Request{}); got != "" {
		t.Errorf("buildSteering(empty) = %q, want empty string", got)
	}
}
