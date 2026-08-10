package llm

import (
	"context"
	"errors"
	"sync"
)

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
