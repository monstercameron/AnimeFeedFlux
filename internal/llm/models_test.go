package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// ClassifyModel's contract is unusual and worth pinning precisely: it may
// only ever GROUP a menu, never filter one. Both return values false is a
// normal outcome — that model still appears, in the "other" group — so the
// dangerous failure mode is not "guessed wrong", it is a future edit that
// turns a guess into an exclusion.
func TestClassifyModel(t *testing.T) {
	cases := []struct {
		id        string
		chat      bool
		embedding bool
	}{
		{"gpt-4o", true, false},
		{"gpt-4o-mini", true, false},
		{"GPT-4O", true, false}, // classification is case-insensitive
		{"o1-preview", true, false},
		{"o3-mini", true, false},
		{"o4-mini", true, false},
		{"chatgpt-4o-latest", true, false},

		{"text-embedding-3-small", false, true},
		{"text-embedding-ada-002", false, true},

		// Not text generation, and specifically NOT chat just because the id
		// begins with a chat-ish prefix — this is the ordering the negative
		// list exists to enforce.
		{"gpt-4o-transcribe", false, false},
		{"gpt-4o-audio-preview", false, false},
		{"gpt-4o-realtime-preview", false, false},
		{"gpt-image-1", false, false},
		{"whisper-1", false, false},
		{"tts-1-hd", false, false},
		{"dall-e-3", false, false},
		{"omni-moderation-latest", false, false},
		{"davinci-002", false, false},
		{"babbage-002", false, false},

		// Unknown families are unclassified, never dropped.
		{"claude-opus-5", false, false},
		{"llama-3.3-70b", false, false},
		{"", false, false},
	}
	for _, tc := range cases {
		chat, embedding := ClassifyModel(tc.id)
		if chat != tc.chat || embedding != tc.embedding {
			t.Errorf("ClassifyModel(%q) = (chat=%v, embedding=%v), want (chat=%v, embedding=%v)",
				tc.id, chat, embedding, tc.chat, tc.embedding)
		}
		if chat && embedding {
			t.Errorf("ClassifyModel(%q) claimed both chat and embedding", tc.id)
		}
	}
}

func TestModelRankOrdersChatThenEmbeddingThenRest(t *testing.T) {
	chat := modelRank(Model{Chat: true})
	embed := modelRank(Model{Embedding: true})
	other := modelRank(Model{})
	if !(chat < embed && embed < other) {
		t.Errorf("rank order is chat=%d embedding=%d other=%d, want strictly increasing", chat, embed, other)
	}
	// A model somehow flagged both ranks as chat — the menu still has to put
	// it somewhere, and "first" is the harmless answer.
	if got := modelRank(Model{Chat: true, Embedding: true}); got != chat {
		t.Errorf("a both-flagged model ranked %d, want %d", got, chat)
	}
}

// listerAgainst points a lister at a local test server, so the sort order and
// the error path are exercised without a provider key or the network (RULE-1;
// loopback is explicitly allowed by the guard in TestMain).
func listerAgainst(t *testing.T, h http.Handler) *OpenAIModelLister {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := openai.DefaultConfig("test-key-not-a-real-key")
	cfg.BaseURL = srv.URL + "/v1"
	return &OpenAIModelLister{client: openai.NewClientWithConfig(cfg)}
}

func TestListModelsSortsChatFirstThenAlphabetically(t *testing.T) {
	lister := listerAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "whisper-1", "object": "model", "owned_by": "openai"},
				{"id": "text-embedding-3-small", "object": "model", "owned_by": "openai"},
				{"id": "gpt-4o", "object": "model", "owned_by": "openai"},
				{"id": "dall-e-3", "object": "model", "owned_by": "openai"},
				{"id": "gpt-4.1", "object": "model", "owned_by": "openai"},
			},
		})
	}))

	got, err := lister.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	want := []string{"gpt-4.1", "gpt-4o", "text-embedding-3-small", "dall-e-3", "whisper-1"}
	if len(got) != len(want) {
		t.Fatalf("got %d models, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("position %d = %q, want %q (full order %v)", i, got[i].ID, want[i], modelIDs(got))
		}
	}
	// Nothing is dropped: an unclassified model is still in the list, which is
	// the invariant Model.Chat's doc comment turns on.
	if got[0].OwnedBy != "openai" {
		t.Errorf("owned_by was not carried through: %+v", got[0])
	}
	if !got[0].Chat || got[2].Chat || !got[2].Embedding {
		t.Errorf("classification did not survive the sort: %+v", got)
	}
}

func TestListModelsReturnsTheProvidersError(t *testing.T) {
	// The RPC layer degrades to a text input on any error, but only if the
	// error actually reaches it rather than being swallowed into an empty
	// list — an empty menu reads as "this key can use no models".
	lister := listerAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error"}}`))
	}))

	got, err := lister.ListModels(context.Background())
	if err == nil {
		t.Fatalf("want an error from a 401, got %d models", len(got))
	}
	if got != nil {
		t.Errorf("want a nil list alongside the error, got %+v", got)
	}
}

func TestListModelsEmptyListIsNotAnError(t *testing.T) {
	lister := listerAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	got, err := lister.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want an empty list, got %+v", got)
	}
}

func TestListModelsHonoursACanceledContext(t *testing.T) {
	lister := listerAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request should never have been sent for a canceled context")
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lister.ListModels(ctx); err == nil {
		t.Fatal("want an error for a canceled context")
	}
}

func TestNewOpenAIModelListerAcceptsAnEmptyKey(t *testing.T) {
	// Documented behaviour: an empty key fails at call time, not at
	// construction, so the server can boot without a provider key and simply
	// report the model list as unavailable.
	if l := NewOpenAIModelLister("", ""); l == nil || l.client == nil {
		t.Fatal("NewOpenAIModelLister(\"\") did not build a lister")
	}
	if l := NewOpenAIModelLister("not-a-real-key", ""); l == nil || l.client == nil {
		t.Fatal("NewOpenAIModelLister did not build a lister")
	}

}

func modelIDs(ms []Model) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

// A profile's base URL must reach the client rather than being accepted and
// dropped — that dropping is exactly how A4-42 presented: a setting saved,
// displayed, and read by nobody.
func TestOpenAIClientConfigAppliesTheBaseURL(t *testing.T) {
	deflt := openai.DefaultConfig("k")

	if got := openAIClientConfig("k", ""); got.BaseURL != deflt.BaseURL {
		t.Errorf("an empty base URL changed the default to %q", got.BaseURL)
	}
	if got := openAIClientConfig("k", "   "); got.BaseURL != deflt.BaseURL {
		t.Errorf("a whitespace base URL changed the default to %q", got.BaseURL)
	}
	// The trailing slash is stripped: "…/v1//models" is served by some
	// gateways and 404s on others, and the operator should not have to know
	// which kind theirs is.
	if got := openAIClientConfig("k", "http://127.0.0.1:11434/v1/"); got.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Errorf("base URL = %q", got.BaseURL)
	}
	if got := openAIClientConfig("k", "https://openrouter.ai/api/v1"); got.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("base URL = %q", got.BaseURL)
	}
}
