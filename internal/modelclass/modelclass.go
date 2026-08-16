// Package modelclass is the chat/embedding classification heuristic over a
// model id, and nothing else — a LEAF package (stdlib only), extracted from
// internal/llm on 2026-08-15 because internal/feedspec needs the heuristic
// and feedspec is compiled into the BROWSER (the /generate editor validates
// recipes client-side). Importing it from internal/llm dragged llm →
// SchemaFlux → go-openai into the WASM bundle, where check-wasm-secrets
// correctly failed the build on go-openai's `authToken` field name — the
// admin bundle must never carry the server's provider-client code.
//
// internal/llm.ClassifyModel remains the server-side name (delegating
// here), so its callers and its doc-comment contract are unchanged.
package modelclass

import "strings"

// ClassifyModel reports whether id looks like a text-generation model
// (chat) or an embedding model. A pure heuristic over the id — the
// provider's list endpoint returns no capability information at all.
//
// Both return values can be false, and that is a normal outcome, not a
// failure — an unclassified model still appears in a menu, just in the
// "other" group. Nothing here may ever be used to REMOVE a model from a
// list (see internal/llm's Model.Chat doc comment).
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
