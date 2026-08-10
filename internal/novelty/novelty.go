package novelty

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
)

// Vector is one stored embedding row (the item_embeddings table, §10):
// the model and dimension travel with the vector so a model change is
// detectable instead of silently comparing incompatible vectors (§8.1).
type Vector struct {
	ID    string
	Model string
	Dim   int
	Vec   []float32
}

// Cosine returns the cosine similarity of a and b, assuming both are already
// L2-normalized (Embed guarantees this), in which case cosine similarity is
// a plain dot product.
//
// A length mismatch is a programming error, not a legitimate "totally
// dissimilar" input — returning 0 for it would silently read as maximally
// dissimilar and let a duplicate through. So this panics instead of
// returning a value that looks valid.
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		panic(fmt.Sprintf("novelty: Cosine: length mismatch (%d vs %d)", len(a), len(b)))
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

// Gate is the §9 step 5 novelty check: embed a candidate and compare it
// against a window of recent embeddings.
type Gate struct {
	Embedder
	// Threshold is the cosine similarity above which a candidate is
	// considered a duplicate.
	Threshold float64
	// Window bounds how many of the most recent corpus vectors are
	// compared. Callers are expected to pass an already-windowed corpus
	// (e.g. the last 500 rows from storage); Window is kept here as the
	// documented size for that expectation, not enforced by Check itself,
	// since trimming is a storage-layer concern.
	Window int
}

// Check embeds candidate and compares it against corpus (brute force — §8.1
// is explicit that comparing 500 vectors is microseconds and a vector index
// would be premature). It returns whether the candidate duplicates
// something already in the corpus, the ID of the nearest match, and its
// score.
//
// Every vector in corpus must share the gate's embedding model and
// dimension. Comparing vectors from different models or dimensions would
// produce a numerically valid but meaningless similarity score — one that
// could either wave a real duplicate through or reject everything — so this
// refuses and returns an error instead of comparing them.
func (g Gate) Check(ctx context.Context, candidate string, corpus []Vector) (dup bool, nearestID string, score float64, err error) {
	model := g.Embedder.Model()
	dim := g.Embedder.Dim()

	for _, v := range corpus {
		if v.Model != model || v.Dim != dim {
			return false, "", 0, fmt.Errorf(
				"novelty: Check: corpus vector %q has model=%q dim=%d, gate expects model=%q dim=%d",
				v.ID, v.Model, v.Dim, model, dim,
			)
		}
	}

	vecs, err := g.Embedder.Embed(ctx, []string{candidate})
	if err != nil {
		return false, "", 0, fmt.Errorf("novelty: Check: %w", err)
	}
	cand := vecs[0]

	var best float64 = -math.MaxFloat64
	var bestID string
	for _, v := range corpus {
		s := Cosine(cand, v.Vec)
		if s > best {
			best = s
			bestID = v.ID
		}
	}

	if len(corpus) == 0 {
		return false, "", 0, nil
	}
	return best >= g.Threshold, bestID, best, nil
}

// Encode serializes a float32 vector to a little-endian byte slice for the
// item_embeddings BLOB column.
func Encode(v []float32) []byte {
	out := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], math.Float32bits(x))
	}
	return out
}

// Decode reverses Encode. It errors if b's length is not a multiple of 4
// (a truncated or corrupt BLOB), rather than silently dropping the trailing
// bytes.
func Decode(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("novelty: Decode: length %d is not a multiple of 4", len(b))
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		bits := binary.LittleEndian.Uint32(b[i*4 : i*4+4])
		out[i] = math.Float32frombits(bits)
	}
	return out, nil
}
