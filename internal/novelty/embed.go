// Package novelty implements the §9 step 5 novelty gate: embed a candidate
// item, compare it against the recent embedding window, and reject
// near-duplicates before they reach the feed.
//
// PLAN.md §8.1 records that SchemaFlux — the LLM layer used everywhere else
// in this codebase — does not expose a public embedding API; EmbeddingProvider
// and friends live in SchemaFlux's internal package. This package is the one
// deliberate, documented exception to "SchemaFlux is the LLM layer": it calls
// the OpenAI embeddings endpoint directly through github.com/sashabaranov/go-openai,
// a dependency SchemaFlux already pulls in transitively. Every other call in
// this codebase goes through SchemaFlux; only embeddings do not, and only
// because there is currently no other way to get them.
package novelty

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// Embedder turns text into vectors. Embed returns one vector per input
// string, in the same order. Model and Dim identify what was produced so
// callers can detect a model change instead of silently comparing
// incompatible vectors (§8.1).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Model() string
	Dim() int
}

// OpenAIEmbedder calls the OpenAI embeddings endpoint directly (see the
// package doc for why this bypasses SchemaFlux).
type OpenAIEmbedder struct {
	client *openai.Client
	model  openai.EmbeddingModel
	dim    int
}

// NewOpenAIEmbedder builds an embedder for the given model. dim is the
// model's known output dimension (e.g. 1536 for text-embedding-3-small);
// it is recorded rather than discovered from the first response so that a
// mismatch between the caller's expectation and the actual API response is
// something Embed can detect and fail loudly on, rather than silently
// storing vectors under the wrong dimension.
// baseURL points the embedder at an OpenAI-COMPATIBLE endpoint instead of
// OpenAI's own; empty means the library default, which is what every caller
// did before provider profiles existed (TODOS.md A4-42). It must match the
// endpoint the STORED vectors came from — cosine similarity between two
// models' vectors is a number with no meaning, so changing this without
// clearing the vector store silently degrades the novelty gate rather than
// failing it.
func NewOpenAIEmbedder(apiKey, baseURL string, model openai.EmbeddingModel, dim int) *OpenAIEmbedder {
	return &OpenAIEmbedder{
		client: openai.NewClientWithConfig(openAIClientConfig(apiKey, baseURL)),
		model:  model,
		dim:    dim,
	}
}

// openAIClientConfig builds the go-openai config for a key and an optional
// base URL. Separate from the constructor because go-openai's Client exposes
// no way to read its own base URL back, so this is the only place a test can
// check the URL was applied rather than accepted and dropped.
func openAIClientConfig(apiKey, baseURL string) openai.ClientConfig {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL = strings.TrimSpace(baseURL); baseURL != "" {
		// See internal/llm's copy: a trailing slash yields "…/v1//embeddings",
		// which some gateways serve and some refuse.
		cfg.BaseURL = strings.TrimRight(baseURL, "/")
	}
	return cfg
}

// Model reports the model id used for every vector this embedder produces.
func (e *OpenAIEmbedder) Model() string { return string(e.model) }

// Dim reports the vector length this embedder produces.
func (e *OpenAIEmbedder) Dim() int { return e.dim }

// Embed calls the OpenAI embeddings API and returns one L2-normalized vector
// per input text, in the same order.
//
// Vectors are normalized here, on the way out, so every downstream consumer
// (the gate, storage, tests) can assume unit length and compute cosine
// similarity as a plain dot product — no per-comparison normalization, and
// no way for a caller to forget it.
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	resp, err := e.client.CreateEmbeddings(ctx, openai.EmbeddingRequestStrings{
		Input: texts,
		Model: e.model,
	})
	if err != nil {
		return nil, fmt.Errorf("novelty: embed: %w", err)
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("novelty: embed: expected %d vectors, got %d", len(texts), len(resp.Data))
	}

	// Placed by each object's own Index, not by its position in the array.
	// The API returns that field precisely because the array order is not
	// guaranteed, and pairing by position would attach one candidate's
	// embedding to another candidate's text. That failure is invisible from
	// the outside: the novelty scores stay entirely plausible, they are just
	// about the wrong pair — so a near-duplicate passes and an original item
	// is rejected, with nothing in any log to say why.
	out := make([][]float32, len(resp.Data))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("novelty: embed: vector index %d out of range for %d texts", d.Index, len(texts))
		}
		if out[d.Index] != nil {
			return nil, fmt.Errorf("novelty: embed: provider returned two vectors for index %d", d.Index)
		}
		if len(d.Embedding) != e.dim {
			return nil, fmt.Errorf("novelty: embed: model %s returned dim %d, expected %d", e.model, len(d.Embedding), e.dim)
		}
		out[d.Index] = normalize(d.Embedding)
	}
	return out, nil
}

// normalize returns a copy of v scaled to unit L2 length. A zero vector is
// returned unchanged (its norm is 0, so scaling would divide by zero) —
// this should not happen for real embeddings, but it keeps Embed from
// producing NaNs if it ever does.
func normalize(v []float32) []float32 {
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	if sumSq == 0 {
		out := make([]float32, len(v))
		copy(out, v)
		return out
	}
	norm := math.Sqrt(sumSq)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	return out
}

// FakeEmbedder produces deterministic vectors from a hash of the input text,
// so tests never touch the network (RULE-1). Two calls with the same text
// always produce the same vector, and the vectors it produces are
// L2-normalized like the real thing.
type FakeEmbedder struct {
	dim       int
	modelName string
}

// NewFakeEmbedder builds a FakeEmbedder that produces vectors of the given
// dimension.
func NewFakeEmbedder(dim int) *FakeEmbedder {
	if dim <= 0 {
		dim = 8
	}
	return &FakeEmbedder{dim: dim, modelName: "fake-embedding-test"}
}

// Model reports a fixed synthetic model name.
func (f *FakeEmbedder) Model() string { return f.modelName }

// Dim reports the configured vector length.
func (f *FakeEmbedder) Dim() int { return f.dim }

// Embed hashes each text with SHA-256 and expands the digest into a
// deterministic float32 vector, then L2-normalizes it. Same text in, same
// vector out, every time, with no network call.
func (f *FakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = normalize(hashVector(t, f.dim))
	}
	return out, nil
}

// hashVector expands text into a dim-length float32 vector by repeatedly
// re-hashing the digest — SHA-256 gives 32 bytes (8 float32 lanes) per
// round, so texts hash across multiple rounds for dim > 8. This is not
// meant to have any semantic properties; it just needs to be a stable
// fingerprint of the exact input string.
func hashVector(text string, dim int) []float32 {
	out := make([]float32, dim)
	sum := sha256.Sum256([]byte(text))
	round := sum
	for i := 0; i < dim; i++ {
		lane := i % 8
		if lane == 0 && i != 0 {
			round = sha256.Sum256(round[:])
		}
		bits := binary.LittleEndian.Uint32(round[lane*4 : lane*4+4])
		// Map to [-1, 1] so components can be positive or negative, like a
		// real embedding, instead of a directionless magnitude.
		out[i] = float32(bits)/float32(math.MaxUint32)*2 - 1
	}
	return out
}
