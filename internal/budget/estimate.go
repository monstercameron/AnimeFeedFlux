package budget

// EstimateTokens approximates a token count from raw text.
//
// This is NOT a tokenizer. SchemaFlux reports no usage (PLAN.md §8.1), so
// there is no ground truth to check against at call time; the only honest
// option is a documented approximation, not a pretend-precise one. The
// heuristic is ~4 characters per token, which holds reasonably for English
// prose under GPT-style BPE tokenizers. It systematically UNDER-counts for
// CJK and other non-Latin scripts, where tokenizers often run closer to
// 1-2 characters per token — a feed whose sources are Japanese titles will
// see this estimate run low. Callers that care about that gap should pad
// their budget headroom rather than trust this function to close it.
func EstimateTokens(s string) int {
	n := len([]rune(s))
	if n == 0 {
		return 0
	}
	// Round up so a non-empty string never estimates to zero tokens, which
	// would let it slip past a >0 budget check for free.
	tok := (n + 3) / 4
	if tok < 1 {
		tok = 1
	}
	return tok
}

// EstimateRequest approximates the input token count for a call made of a
// prompt and a system message. They are summed rather than estimated
// separately-and-discarded because both are sent to the model and both are
// billed as input tokens.
func EstimateRequest(prompt, system string) int {
	return EstimateTokens(prompt) + EstimateTokens(system)
}

// EstimateResponse approximates the output token count from the raw model
// response text.
func EstimateResponse(raw string) int {
	return EstimateTokens(raw)
}
