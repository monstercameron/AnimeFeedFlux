package llm

import (
	"context"
	"sort"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// models.go asks the provider which models this deployment's key can use.
//
// It exists so the Settings model fields can be a CHOSEN value instead of a
// typed one. A hand-typed model id is a per-feed outage waiting to happen:
// PLAN.md §8's error taxonomy classifies "model not found" as a
// recipe-scoped Fatal that disables that feed, and a typo is
// indistinguishable from a deprecation until a scheduled run fails at 4am.
//
// Like internal/novelty's embedder, this calls go-openai directly rather
// than going through SchemaFlux — §8.1 records that SchemaFlux exposes no
// model-listing API, and this is the same narrow, documented exception. The
// call is made server-side and the key never leaves the process (§4).

// Model is one model the provider reports, with the two classifications the
// Settings UI groups by.
type Model struct {
	ID      string
	OwnedBy string
	// Chat and Embedding are INFERRED from the id — the API does not say
	// what a model is for. They exist to group a long menu, never to filter
	// one: a model this heuristic guesses wrong about must still be
	// selectable, or a new model family becomes unusable until someone
	// updates this file.
	Chat      bool
	Embedding bool
}

// ModelLister reports the models available to a provider credential.
type ModelLister interface {
	ListModels(ctx context.Context) ([]Model, error)
}

// OpenAIModelLister lists models from OpenAI.
type OpenAIModelLister struct {
	client *openai.Client
}

// NewOpenAIModelLister builds a lister against apiKey. An empty key is
// allowed here and fails at call time rather than at construction, so the
// server can start without a provider key and simply report the model list
// as unavailable — the same degraded state PLAN.md §12.5 already describes
// for the key-presence indicator.
func NewOpenAIModelLister(apiKey string) *OpenAIModelLister {
	return &OpenAIModelLister{client: openai.NewClient(apiKey)}
}

// listModelsTimeout bounds the call.
//
// Raised from 10s to 30s after a real failure: the settings screen reported
// "couldn't reach the provider" while curl against the same endpoint with
// the same key returned 200 immediately. The server log said
// `context deadline exceeded` — a cold TLS handshake to api.openai.com from
// a residential connection can take most of ten seconds on its own, so the
// timeout was cutting off calls that were merely slow, not broken, and the
// fallback made that look like a bad key.
//
// A long timeout is affordable here precisely BECAUSE the RPC never fails
// the request: the caller renders a text input meanwhile, so nothing is
// blocked on this. The 30s ceiling still exists so a black-holed connection
// does not pin a goroutine indefinitely.
const listModelsTimeout = 30 * time.Second

// ListModels returns the available models, sorted so the ones an operator is
// most likely to want are first: chat models, then embedding models, then
// everything else, alphabetical within each group.
func (l *OpenAIModelLister) ListModels(ctx context.Context) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, listModelsTimeout)
	defer cancel()

	resp, err := l.client.ListModels(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Model, 0, len(resp.Models))
	for _, m := range resp.Models {
		chat, embed := ClassifyModel(m.ID)
		out = append(out, Model{ID: m.ID, OwnedBy: m.OwnedBy, Chat: chat, Embedding: embed})
	}

	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := modelRank(out[i]), modelRank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func modelRank(m Model) int {
	switch {
	case m.Chat:
		return 0
	case m.Embedding:
		return 1
	default:
		return 2
	}
}

// ClassifyModel guesses what a model id is for.
//
// Deliberately a heuristic over the id, and deliberately generous: OpenAI's
// list endpoint returns no capability information at all, so the alternative
// to guessing is presenting one undifferentiated list of every audio, image,
// moderation and transcription model alongside the four an operator wants.
//
// Both return values can be false, and that is a normal outcome, not a
// failure — an unclassified model still appears in the menu, just in the
// "other" group. Nothing here may ever be used to REMOVE a model from the
// list; see Model.Chat's doc comment.
func ClassifyModel(id string) (chat, embedding bool) {
	lower := strings.ToLower(id)

	// Embedding first: "text-embedding-3-small" would otherwise match the
	// generic text-model prefixes below.
	if strings.Contains(lower, "embedding") {
		return false, true
	}

	// Families that are definitively not text generation. Checked before the
	// positive list so e.g. "gpt-4o-transcribe" is not offered as a chat
	// model just because it starts with "gpt".
	for _, no := range []string{
		"whisper", "tts", "dall-e", "moderation", "transcribe", "realtime",
		"audio", "image", "search", "similarity", "edit", "davinci-002", "babbage-002",
	} {
		if strings.Contains(lower, no) {
			return false, false
		}
	}

	for _, yes := range []string{"gpt", "o1", "o3", "o4", "chatgpt"} {
		if strings.HasPrefix(lower, yes) {
			return true, false
		}
	}
	return false, false
}
