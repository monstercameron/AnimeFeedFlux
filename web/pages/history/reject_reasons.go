package history

// Reject reasons arrive from the server as short stable identifiers —
// "novelty_duplicate", "tags_not_lowercase" — because they are grouped and
// counted per run, and a sentence would change wording between commits and
// break the grouping (internal/generate/contract.go says exactly this).
//
// That is the right wire format and the wrong thing to show an operator.
// They were the last machine identifiers left on an operator surface
// (TODOS.md A8-30). This file maps each one to a catalogue key, so the
// expanded run panel can render "Two candidates repeated an item the feed
// already published" and keep the identifier alongside it for diagnosis.
//
// The map is deliberately closed: a token with no entry renders as the raw
// identifier rather than as a blank or a fallback sentence that would claim
// to explain something it does not. TestEveryGenerateReasonHasALabel keeps
// the two in step by walking generate.AllRejectReasons.

// rejectReasonKeys maps a wire token to its catalogue key.
var rejectReasonKeys = map[string]string{
	"invalid_utf8":               "history.runs.reject.invalid_utf8",
	"control_chars":              "history.runs.reject.control_chars",
	"title_required":             "history.runs.reject.title_required",
	"title_too_short":            "history.runs.reject.title_too_short",
	"title_too_long":             "history.runs.reject.title_too_long",
	"title_trailing_punctuation": "history.runs.reject.title_trailing_punctuation",
	"summary_required":           "history.runs.reject.summary_required",
	"summary_exceeds_hard_cap":   "history.runs.reject.summary_exceeds_hard_cap",
	"summary_contains_html":      "history.runs.reject.summary_contains_html",
	"body_html_required":         "history.runs.reject.body_html_required",
	"body_html_relative_link":    "history.runs.reject.body_html_relative_link",
	"answer_leaked_into_summary": "history.runs.reject.answer_leaked_into_summary",
	"tags_too_many":              "history.runs.reject.tags_too_many",
	"tags_not_lowercase":         "history.runs.reject.tags_not_lowercase",
	"link_required_grounded":     "history.runs.reject.link_required_grounded",
	"link_invalid":               "history.runs.reject.link_invalid",
	"link_not_in_candidate_set":  "history.runs.reject.link_not_in_candidate_set",
	"novelty_duplicate":          "history.runs.reject.novelty_duplicate",
	"novelty_check_failed":       "history.runs.reject.novelty_check_failed",
}

// RejectReasonKey returns the catalogue key describing token, and whether
// there is one. An unknown token — a reason added server-side after this
// build — is the caller's cue to print the identifier as-is, which is worse
// than a sentence and much better than nothing.
func RejectReasonKey(token string) (string, bool) {
	key, ok := rejectReasonKeys[token]
	return key, ok
}
