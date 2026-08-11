package novelty

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// The embedder is the one place in this codebase that calls a provider
// without going through SchemaFlux, and until now nothing exercised it: every
// novelty test used a fake embedder, so the response handling — dimension
// check, ordering, normalization — had never run against a response body of
// any kind.
//
// A local test server stands in for the API (RULE-1's guard allows loopback;
// see main_test.go), so none of this needs a key or the network.

func embedderAgainst(t *testing.T, dim int, h http.Handler) *OpenAIEmbedder {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := openai.DefaultConfig("test-key-not-a-real-key")
	cfg.BaseURL = srv.URL + "/v1"
	return &OpenAIEmbedder{
		client: openai.NewClientWithConfig(cfg),
		model:  openai.SmallEmbedding3,
		dim:    dim,
	}
}

// embeddingsResponse builds a well-formed response body. indexes lets a test
// return the data objects in an order other than the request order.
func embeddingsResponse(vectors [][]float32, indexes []int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := make([]map[string]any, 0, len(vectors))
		for i, v := range vectors {
			idx := i
			if i < len(indexes) {
				idx = indexes[i]
			}
			data = append(data, map[string]any{
				"object":    "embedding",
				"index":     idx,
				"embedding": v,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"model":  "text-embedding-3-small",
			"data":   data,
		})
	}
}

func TestEmbedNormalizesToUnitLength(t *testing.T) {
	// Every downstream consumer treats cosine similarity as a plain dot
	// product. If a vector arrives un-normalized, similarity scores are
	// scaled by an arbitrary factor and the novelty threshold silently means
	// something different for every item.
	e := embedderAgainst(t, 3, embeddingsResponse([][]float32{{3, 0, 4}}, nil))

	got, err := e.Embed(context.Background(), []string{"one"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d vectors, want 1", len(got))
	}
	if l := l2(got[0]); math.Abs(l-1) > 1e-6 {
		t.Errorf("vector length = %v, want 1 (got %v)", l, got[0])
	}
	// 3-4-5 triangle: the direction must survive the scaling.
	if math.Abs(float64(got[0][0])-0.6) > 1e-6 || math.Abs(float64(got[0][2])-0.8) > 1e-6 {
		t.Errorf("normalization changed the direction: %v", got[0])
	}
}

func TestEmbedReturnsVectorsInRequestOrder(t *testing.T) {
	// The API returns objects carrying their own `index` precisely because
	// the array order is not guaranteed. Pairing vector[i] with text[i] by
	// position alone would attach one item's embedding to another item's
	// text — and the failure is invisible: novelty scores stay plausible,
	// they are just about the wrong pair.
	first := []float32{1, 0, 0}
	second := []float32{0, 1, 0}
	e := embedderAgainst(t, 3, embeddingsResponse(
		[][]float32{second, first}, // returned out of order...
		[]int{1, 0},                // ...and saying so
	))

	got, err := e.Embed(context.Background(), []string{"text-zero", "text-one"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d vectors, want 2", len(got))
	}
	if got[0][0] != 1 {
		t.Errorf("vector 0 = %v, want the embedding whose index is 0 (%v)", got[0], first)
	}
	if got[1][1] != 1 {
		t.Errorf("vector 1 = %v, want the embedding whose index is 1 (%v)", got[1], second)
	}
}

func TestEmbedRejectsAWrongDimension(t *testing.T) {
	// dim is recorded rather than discovered so a model swap fails loudly
	// here instead of silently storing incomparable vectors.
	e := embedderAgainst(t, 1536, embeddingsResponse([][]float32{{1, 0, 0}}, nil))

	_, err := e.Embed(context.Background(), []string{"one"})
	if err == nil {
		t.Fatal("want an error when the model returns the wrong dimension")
	}
	if !strings.Contains(err.Error(), "1536") || !strings.Contains(err.Error(), "dim 3") {
		t.Errorf("the error does not say what was expected vs. received: %v", err)
	}
}

func TestEmbedRejectsAShortResponse(t *testing.T) {
	// Fewer vectors than texts means the pairing is unknowable, so this must
	// fail rather than return a shorter slice the caller may zip by index.
	e := embedderAgainst(t, 3, embeddingsResponse([][]float32{{1, 0, 0}}, nil))

	_, err := e.Embed(context.Background(), []string{"one", "two"})
	if err == nil {
		t.Fatal("want an error when the provider returns fewer vectors than texts")
	}
	if !strings.Contains(err.Error(), "expected 2") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestEmbedNoTextsMakesNoCall(t *testing.T) {
	e := embedderAgainst(t, 3, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Embed called the provider for an empty batch")
	}))
	got, err := e.Embed(context.Background(), nil)
	if err != nil || got != nil {
		t.Errorf("Embed(nil) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestEmbedWrapsAProviderFailure(t *testing.T) {
	e := embedderAgainst(t, 3, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
	}))
	_, err := e.Embed(context.Background(), []string{"one"})
	if err == nil {
		t.Fatal("want an error from a 429")
	}
	if !strings.Contains(err.Error(), "novelty: embed") {
		t.Errorf("the error lost its context: %v", err)
	}
}

func TestNewOpenAIEmbedderRecordsItsModelAndDim(t *testing.T) {
	e := NewOpenAIEmbedder("not-a-real-key", openai.SmallEmbedding3, 1536)
	if got := e.Model(); got != string(openai.SmallEmbedding3) {
		t.Errorf("Model() = %q, want %q", got, openai.SmallEmbedding3)
	}
	if got := e.Dim(); got != 1536 {
		t.Errorf("Dim() = %d, want 1536", got)
	}
}

func TestNormalizeLeavesAZeroVectorAlone(t *testing.T) {
	// Not a real embedding, but scaling it would divide by zero and poison
	// every later comparison with NaN.
	in := []float32{0, 0, 0}
	got := normalize(in)
	for i, x := range got {
		if x != 0 {
			t.Errorf("normalize(zero)[%d] = %v, want 0", i, x)
		}
	}
	// And it must be a copy, not the caller's slice.
	got[0] = 9
	if in[0] != 0 {
		t.Error("normalize returned the caller's own slice")
	}
}

func l2(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

func TestEmbedRejectsAnUnusableIndex(t *testing.T) {
	// Both of these would otherwise leave a nil vector in the returned slice,
	// which the gate would read as a zero embedding — similarity 0 against
	// everything, i.e. "definitely novel", for an item nobody embedded.
	t.Run("out of range", func(t *testing.T) {
		e := embedderAgainst(t, 3, embeddingsResponse([][]float32{{1, 0, 0}}, []int{7}))
		if _, err := e.Embed(context.Background(), []string{"one"}); err == nil {
			t.Fatal("want an error for an out-of-range index")
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		e := embedderAgainst(t, 3, embeddingsResponse([][]float32{{1, 0, 0}, {0, 1, 0}}, []int{0, 0}))
		if _, err := e.Embed(context.Background(), []string{"one", "two"}); err == nil {
			t.Fatal("want an error when two vectors claim the same index")
		}
	})
}
