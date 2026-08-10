package budget

import "testing"

func TestEstimateTokens_Monotonic(t *testing.T) {
	short := "hello world"
	longer := short + " this text just keeps going and going and going"
	longest := longer + longer + longer

	et := EstimateTokens(short)
	el := EstimateTokens(longer)
	elongest := EstimateTokens(longest)

	if !(et < el && el < elongest) {
		t.Fatalf("expected strictly increasing estimates, got %d, %d, %d", et, el, elongest)
	}
}

func TestEstimateTokens_EmptyIsZero(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Fatalf("expected 0 for empty string, got %d", got)
	}
}

func TestEstimateTokens_NonEmptyNeverZero(t *testing.T) {
	if got := EstimateTokens("a"); got < 1 {
		t.Fatalf("expected a non-empty string to estimate to at least 1 token, got %d", got)
	}
}

func TestEstimateRequest_SumsPromptAndSystem(t *testing.T) {
	prompt := "generate a summary of this anime episode"
	system := "you are a helpful assistant"

	got := EstimateRequest(prompt, system)
	want := EstimateTokens(prompt) + EstimateTokens(system)
	if got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}

func TestEstimateResponse_MatchesEstimateTokens(t *testing.T) {
	raw := `{"title":"Example","summary_text":"a short summary"}`
	if got, want := EstimateResponse(raw), EstimateTokens(raw); got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}
