package novelty

import (
	"context"
	"math"
	"testing"
)

func TestCosineSelfIsOne(t *testing.T) {
	v := normalize([]float32{1, 2, 3, 4})
	got := Cosine(v, v)
	if math.Abs(got-1) > 1e-6 {
		t.Fatalf("Cosine(v, v) = %v, want 1", got)
	}
}

func TestCosineNegationIsMinusOne(t *testing.T) {
	v := normalize([]float32{1, 2, 3, 4})
	neg := make([]float32, len(v))
	for i, x := range v {
		neg[i] = -x
	}
	got := Cosine(v, neg)
	if math.Abs(got-(-1)) > 1e-6 {
		t.Fatalf("Cosine(v, -v) = %v, want -1", got)
	}
}

func TestCosineOrthogonalIsZero(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	got := Cosine(a, b)
	if math.Abs(got) > 1e-9 {
		t.Fatalf("Cosine(orthogonal) = %v, want 0", got)
	}
}

// A length mismatch must never read as "totally dissimilar" (a silent 0).
// It must be impossible to mistake for a real score, so Cosine panics.
func TestCosineLengthMismatchPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Cosine with mismatched lengths did not panic")
		}
	}()
	Cosine([]float32{1, 2, 3}, []float32{1, 2})
}

func TestGateFlagsNearDuplicateAndPassesDistinct(t *testing.T) {
	fe := NewFakeEmbedder(16)
	g := Gate{Embedder: fe, Threshold: 0.999, Window: 500}
	ctx := context.Background()

	base, err := fe.Embed(ctx, []string{"exact duplicate text"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	corpus := []Vector{
		{ID: "existing-1", Model: fe.Model(), Dim: fe.Dim(), Vec: base[0]},
	}

	dup, nearestID, score, err := g.Check(ctx, "exact duplicate text", corpus)
	if err != nil {
		t.Fatalf("Check (duplicate): %v", err)
	}
	if !dup {
		t.Fatalf("expected duplicate flagged, score=%v", score)
	}
	if nearestID != "existing-1" {
		t.Fatalf("nearestID = %q, want existing-1", nearestID)
	}
	if math.Abs(score-1) > 1e-6 {
		t.Fatalf("score = %v, want ~1 for identical text", score)
	}

	dup2, _, score2, err := g.Check(ctx, "an entirely different sentence about spreadsheets", corpus)
	if err != nil {
		t.Fatalf("Check (distinct): %v", err)
	}
	if dup2 {
		t.Fatalf("expected distinct item to pass, got dup with score=%v", score2)
	}
}

func TestGateRejectsMixedModelInCorpus(t *testing.T) {
	fe := NewFakeEmbedder(16)
	g := Gate{Embedder: fe, Threshold: 0.9, Window: 500}
	corpus := []Vector{
		{ID: "a", Model: "some-other-model", Dim: fe.Dim(), Vec: make([]float32, fe.Dim())},
	}
	_, _, _, err := g.Check(context.Background(), "text", corpus)
	if err == nil {
		t.Fatal("expected error for mismatched model, got nil")
	}
}

func TestGateRejectsMixedDimInCorpus(t *testing.T) {
	fe := NewFakeEmbedder(16)
	g := Gate{Embedder: fe, Threshold: 0.9, Window: 500}
	corpus := []Vector{
		{ID: "a", Model: fe.Model(), Dim: 8, Vec: make([]float32, 8)},
	}
	_, _, _, err := g.Check(context.Background(), "text", corpus)
	if err == nil {
		t.Fatal("expected error for mismatched dimension, got nil")
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := [][]float32{
		{1, -2.5, 3.25, 0},
		{},
		{0.1, 0.2, 0.3},
		normalize([]float32{7, -3, 2, 9, -1}),
	}
	for i, v := range cases {
		enc := Encode(v)
		dec, err := Decode(enc)
		if err != nil {
			t.Fatalf("case %d: Decode: %v", i, err)
		}
		if len(dec) != len(v) {
			t.Fatalf("case %d: length %d, want %d", i, len(dec), len(v))
		}
		for j := range v {
			if dec[j] != v[j] {
				t.Fatalf("case %d: element %d = %v, want %v", i, j, dec[j], v[j])
			}
			if math.IsNaN(float64(dec[j])) {
				t.Fatalf("case %d: element %d is NaN", i, j)
			}
		}
	}
}

func TestDecodeRejectsTruncatedBlob(t *testing.T) {
	_, err := Decode([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for length not a multiple of 4")
	}
}

// realistic anime-trivia paraphrase table. Vectors are constructed directly
// (not derived from FakeEmbedder's hash, which has no semantic locality) so
// this is an honest test of Gate.Check's threshold logic against similarity
// scores that mean what a real embedding model's scores would mean — it does
// not fake the result of Cosine itself, only the inputs to it.
func TestGateParaphraseTable(t *testing.T) {
	const dim = 4
	fe := NewFakeEmbedder(dim)
	g := Gate{Embedder: fe, Threshold: 0.92, Window: 500}
	ctx := context.Background()

	// Two clusters of unit vectors: "Luffy's Devil Fruit" trivia cluster
	// pointing mostly along axis 0, and an unrelated "Attack on Titan wall"
	// cluster pointing mostly along axis 2. Paraphrases sit close to their
	// cluster's centroid; the unrelated item sits in the other cluster.
	luffyCentroid := normalize([]float32{0.98, 0.05, 0.05, 0.05})
	titanCentroid := normalize([]float32{0.05, 0.05, 0.98, 0.05})

	corpus := []Vector{
		{ID: "luffy-original", Model: fe.Model(), Dim: dim, Vec: luffyCentroid},
	}

	tests := []struct {
		name    string
		vec     []float32
		wantDup bool
	}{
		{
			name:    "close paraphrase of the same fact",
			vec:     normalize([]float32{0.97, 0.06, 0.06, 0.04}),
			wantDup: true,
		},
		{
			name:    "near-exact reworded question",
			vec:     normalize([]float32{0.99, 0.04, 0.05, 0.03}),
			wantDup: true,
		},
		{
			name:    "unrelated trivia about a different series",
			vec:     titanCentroid,
			wantDup: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Bypass Embed and inject the constructed vector directly by
			// wrapping the fake embedder with a stub that returns tc.vec.
			stub := stubEmbedder{Model_: fe.Model(), Dim_: dim, Vec: tc.vec}
			gg := Gate{Embedder: stub, Threshold: g.Threshold, Window: g.Window}
			dup, _, score, err := gg.Check(ctx, "candidate", corpus)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if dup != tc.wantDup {
				t.Fatalf("dup = %v, want %v (score=%v)", dup, tc.wantDup, score)
			}
		})
	}
}

// stubEmbedder returns a fixed, pre-constructed vector regardless of input
// text, so TestGateParaphraseTable can drive Gate.Check with hand-built
// similarity relationships instead of relying on hash locality.
type stubEmbedder struct {
	Model_ string
	Dim_   int
	Vec    []float32
}

func (s stubEmbedder) Model() string { return s.Model_ }
func (s stubEmbedder) Dim() int      { return s.Dim_ }
func (s stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = s.Vec
	}
	return out, nil
}
