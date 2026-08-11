package llm

import (
	"context"
	"errors"
	"sync"
)

// A4-12 decision (record kept here, not in PLAN.md/TODOS.md, per that
// ticket): a hand-built FakeProvider — this type — over SchemaFlux's
// cassette mechanism. §8 asked for the choice to be made at A4 and recorded;
// in practice it was already made implicitly by the time this comment was
// written (llm.NewFake is wired into every test downstream of internal/llm),
// so this is that implicit choice made explicit, with the reasoning:
//
//   - Scope mismatch. Cassettes replay HTTP traffic SchemaFlux's OWN client
//     made against a provider. But §9 step 5's novelty gate does not go
//     through SchemaFlux at all — §8.1 records that its embedding API is
//     unexported, so internal/llm calls sashabaranov/go-openai directly. A
//     cassette strategy would need two different recording/replay
//     mechanisms (one for SchemaFlux's Generate calls, one hand-rolled for
//     raw go-openai embedding calls) to cover the same surface this single
//     Provider interface covers today. Fake implements both Generate and
//     Embed uniformly because it fakes OUR interface, not a transport.
//   - What's actually under test here is never "did SchemaFlux parse OpenAI's
//     JSON correctly" — that's SchemaFlux's own test suite's job, and it
//     already uses cassettes for exactly that. What internal/generate and
//     internal/store need to exercise is OUR business logic sitting on top:
//     §9's validation, sanitization, novelty, link-integrity, and the
//     atomic commit. A Fake that returns exactly the Result/error shape we
//     ask it to lets a test express "given this model output, assert this
//     outcome" directly, with no HTTP fixture file to keep in sync with a
//     schema change.
//   - No committed binary/text fixtures to rot. A cassette file silently
//     goes stale the moment a prompt template or the response schema
//     changes shape, and the failure surfaces as an inscrutable replay
//     mismatch rather than a compile error. Fake's scripted values are Go
//     values, so a Result field rename is a compile error at every call
//     site, not a runtime cassette-mismatch discovered later.
//   - RULE-1 is satisfied either way — cassettes make no live call on
//     replay either — so it was never the deciding factor; test ergonomics
//     and the embeddings gap were.
//
// What cassettes would have bought, stated honestly: end-to-end confidence
// that SchemaFlux's real request/response wire format still round-trips
// through our adapter after a SchemaFlux version bump, without spending a
// paid call. That gap is real and open — it is not closed by Fake, which
// only proves our code behaves correctly given SOME shaped response, not
// that a real provider still produces that shape. If that gap ever needs
// closing, it belongs as a small number of cassette-backed tests scoped
// narrowly to SchemaFluxProvider.Generate's mapping in llm.go, gated the
// same way AFF_LIVE_LLM-tagged tests already are — not as a wholesale
// replacement for Fake, which the rest of the suite should keep using.
//
// Fake is a scripted Provider. RULE-1 (PLAN.md §17): the default test run
// makes no network calls, so every test downstream of internal/llm drives
// this instead of SchemaFluxProvider.
type Fake struct {
	mu sync.Mutex

	generateResults []Result
	generateErr     error

	embedResults [][]float32
	embedErr     error

	generateCalls []Request
	embedCalls    [][]string
}

// NewFake returns an empty Fake. Queue results or an error before calling
// Generate/Embed.
func NewFake() *Fake {
	return &Fake{}
}

// Name implements Provider.
func (f *Fake) Name() string { return "fake" }

// QueueResult appends a Result Generate will return, in call order.
func (f *Fake) QueueResult(r Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generateResults = append(f.generateResults, r)
}

// QueueGenerateError makes the next Generate call fail with err instead of
// returning a queued Result. Classify runs on err first, exactly as
// SchemaFluxProvider.Generate would, so a caller testing error handling sees
// the same *Error shape it would see against the real provider.
func (f *Fake) QueueGenerateError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generateErr = err
}

// QueueEmbedResult sets the vectors the next Embed call returns.
func (f *Fake) QueueEmbedResult(vectors [][]float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.embedResults = vectors
}

// QueueEmbedError makes the next Embed call fail with err.
func (f *Fake) QueueEmbedError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.embedErr = err
}

// Generate implements Provider.
func (f *Fake) Generate(ctx context.Context, req Request) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.generateCalls = append(f.generateCalls, req)

	if f.generateErr != nil {
		err := f.generateErr
		f.generateErr = nil
		return Result{}, Classify(err)
	}

	if len(f.generateResults) == 0 {
		return Result{}, Classify(errors.New("fake: no scripted Generate result queued"))
	}

	result := f.generateResults[0]
	f.generateResults = f.generateResults[1:]
	return result, nil
}

// Embed implements Provider.
func (f *Fake) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.embedCalls = append(f.embedCalls, texts)

	if f.embedErr != nil {
		err := f.embedErr
		f.embedErr = nil
		return nil, Classify(err)
	}

	if f.embedResults == nil {
		return nil, Classify(errors.New("fake: no scripted Embed result queued"))
	}

	return f.embedResults, nil
}

// GenerateCalls returns every Request Generate received, in order.
func (f *Fake) GenerateCalls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := make([]Request, len(f.generateCalls))
	copy(calls, f.generateCalls)
	return calls
}

// GenerateCallCount reports how many times Generate was called.
func (f *Fake) GenerateCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.generateCalls)
}

// EmbedCalls returns every []string Embed received, in order.
func (f *Fake) EmbedCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := make([][]string, len(f.embedCalls))
	copy(calls, f.embedCalls)
	return calls
}
