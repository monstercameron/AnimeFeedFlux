package novelty

import (
	"context"
	"math"
	"testing"
)

func TestFakeEmbedderDeterministic(t *testing.T) {
	fe := NewFakeEmbedder(12)
	ctx := context.Background()

	a, err := fe.Embed(ctx, []string{"One Piece trivia: what is Luffy's dream?"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	b, err := fe.Embed(ctx, []string{"One Piece trivia: what is Luffy's dream?"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(a[0]) != len(b[0]) {
		t.Fatalf("length mismatch: %d vs %d", len(a[0]), len(b[0]))
	}
	for i := range a[0] {
		if a[0][i] != b[0][i] {
			t.Fatalf("component %d differs across calls: %v vs %v", i, a[0][i], b[0][i])
		}
	}
}

func TestFakeEmbedderDifferentTextDifferentVector(t *testing.T) {
	fe := NewFakeEmbedder(12)
	ctx := context.Background()

	a, err := fe.Embed(ctx, []string{"text one"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	b, err := fe.Embed(ctx, []string{"text two"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	same := true
	for i := range a[0] {
		if a[0][i] != b[0][i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("distinct inputs produced identical vectors")
	}
}

func TestFakeEmbedderBatchMatchesSingle(t *testing.T) {
	fe := NewFakeEmbedder(8)
	ctx := context.Background()

	batch, err := fe.Embed(ctx, []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Embed batch: %v", err)
	}
	single, err := fe.Embed(ctx, []string{"beta"})
	if err != nil {
		t.Fatalf("Embed single: %v", err)
	}
	for i := range batch[1] {
		if batch[1][i] != single[0][i] {
			t.Fatalf("batch and single-call embeddings diverge at %d", i)
		}
	}
}

func TestFakeEmbedderOutputIsNormalized(t *testing.T) {
	fe := NewFakeEmbedder(16)
	vecs, err := fe.Embed(context.Background(), []string{"normalize me"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	var sumSq float64
	for _, x := range vecs[0] {
		sumSq += float64(x) * float64(x)
	}
	norm := math.Sqrt(sumSq)
	if math.Abs(norm-1) > 1e-6 {
		t.Fatalf("norm = %v, want 1 (L2-normalized)", norm)
	}
}

func TestFakeEmbedderModelAndDim(t *testing.T) {
	fe := NewFakeEmbedder(24)
	if fe.Dim() != 24 {
		t.Fatalf("Dim() = %d, want 24", fe.Dim())
	}
	if fe.Model() == "" {
		t.Fatal("Model() is empty")
	}
}

func TestFakeEmbedderEmptyInput(t *testing.T) {
	fe := NewFakeEmbedder(8)
	out, err := fe.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed(nil): %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected 0 vectors, got %d", len(out))
	}
}
