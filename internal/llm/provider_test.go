package llm

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewSchemaFluxProviderRejectsAnEmptyKeyAsAccountFatal(t *testing.T) {
	// The scope matters as much as the failure: an account-scoped Fatal trips
	// the GLOBAL kill switch (§8), which is correct for a missing credential —
	// every feed shares it — and would be wrong for anything recipe-scoped.
	for _, key := range []string{"", "   ", "\t\n"} {
		p, err := NewSchemaFluxProvider(Config{APIKey: key})
		if err == nil {
			t.Fatalf("NewSchemaFluxProvider(%q) returned a provider %v, want an error", key, p)
		}
		var le *Error
		if !errors.As(err, &le) {
			t.Fatalf("want a classified *Error, got %T: %v", err, err)
		}
		if le.Kind != Fatal || le.Scope != ScopeAccount {
			t.Errorf("key %q classified %v/%v, want fatal/account", key, le.Kind, le.Scope)
		}
	}
}

func TestNewSchemaFluxProviderDefaultsToOpenAI(t *testing.T) {
	p, err := NewSchemaFluxProvider(Config{APIKey: "not-a-real-key"})
	if err != nil {
		t.Fatalf("NewSchemaFluxProvider: %v", err)
	}
	if got := p.Name(); got != "openai" {
		t.Errorf("Name() = %q, want %q", got, "openai")
	}
	if p.logger == nil {
		t.Error("a nil Config.Logger must fall back to slog.Default(), not to silence")
	}
}

func TestNewSchemaFluxProviderKeepsAnExplicitProviderName(t *testing.T) {
	p, err := NewSchemaFluxProvider(Config{APIKey: "not-a-real-key", ProviderName: "openai"})
	if err != nil {
		t.Fatalf("NewSchemaFluxProvider: %v", err)
	}
	if got := p.Name(); got != "openai" {
		t.Errorf("Name() = %q, want %q", got, "openai")
	}
}

func TestNewSchemaFluxProviderUsesTheSuppliedLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	p, err := NewSchemaFluxProvider(Config{APIKey: "not-a-real-key", Logger: logger})
	if err != nil {
		t.Fatalf("NewSchemaFluxProvider: %v", err)
	}
	if p.logger != logger {
		t.Error("Config.Logger was not used")
	}
}

func TestEmbedIsUnavailableAndSaysWhy(t *testing.T) {
	// Not a stub that returns an empty slice: a silent empty embedding would
	// make the §9 step-5 novelty check pass everything.
	p, err := NewSchemaFluxProvider(Config{APIKey: "not-a-real-key"})
	if err != nil {
		t.Fatalf("NewSchemaFluxProvider: %v", err)
	}
	vecs, err := p.Embed(context.Background(), []string{"anything"})
	if vecs != nil {
		t.Errorf("want no vectors, got %d", len(vecs))
	}
	var le *Error
	if !errors.As(err, &le) {
		t.Fatalf("want a classified *Error, got %T: %v", err, err)
	}
	if le.Kind != Fatal || le.Scope != ScopeRecipe {
		t.Errorf("classified %v/%v, want fatal/recipe", le.Kind, le.Scope)
	}
}

func TestFakeIsIdentifiable(t *testing.T) {
	// A run whose logs say "fake" is a run nobody mistakes for a billed one.
	if got := NewFake().Name(); got != "fake" {
		t.Errorf("Fake.Name() = %q, want %q", got, "fake")
	}
}

func TestKindStringCoversEveryKind(t *testing.T) {
	cases := map[Kind]string{
		KindUnclassified: "unclassified",
		Transient:        "transient",
		Invalid:          "invalid",
		Fatal:            "fatal",
		Kind(99):         "unclassified",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

func TestScopeStringCoversEveryScope(t *testing.T) {
	cases := map[Scope]string{
		ScopeNone:    "none",
		ScopeAccount: "account",
		ScopeRecipe:  "recipe",
		Scope(99):    "none",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Scope(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestErrorUnwrapReachesThroughForErrorsIs(t *testing.T) {
	sentinel := errors.New("underlying")
	e := &Error{Kind: Transient, Err: sentinel}
	if !errors.Is(e, sentinel) {
		t.Error("errors.Is could not reach the wrapped error")
	}
	if got := e.Unwrap(); got != sentinel {
		t.Errorf("Unwrap() = %v, want %v", got, sentinel)
	}
	// Both nil cases: a nil *Error and an Error with nothing wrapped. Neither
	// may panic — errors.Is walks these on any failed match.
	var nilErr *Error
	if got := nilErr.Unwrap(); got != nil {
		t.Errorf("(*Error)(nil).Unwrap() = %v, want nil", got)
	}
	if got := (&Error{Kind: Fatal}).Unwrap(); got != nil {
		t.Errorf("Unwrap() with no wrapped error = %v, want nil", got)
	}
	if got := nilErr.Error(); got != "<nil>" {
		t.Errorf("(*Error)(nil).Error() = %q, want %q", got, "<nil>")
	}
	if nilErr.Retryable() {
		t.Error("(*Error)(nil).Retryable() must be false")
	}
}

func TestErrorMessageShape(t *testing.T) {
	cases := []struct {
		name string
		err  *Error
		want []string
		not  []string
	}{
		{
			name: "fatal names its scope",
			err:  &Error{Kind: Fatal, Scope: ScopeAccount, Err: errors.New("bad key")},
			want: []string{"fatal", "account", "bad key"},
		},
		{
			name: "a scopeless fatal says nothing about scope",
			err:  &Error{Kind: Fatal, Err: errors.New("canceled")},
			want: []string{"fatal", "canceled"},
			not:  []string{"none"},
		},
		{
			name: "transient reports the wait the provider asked for",
			err:  &Error{Kind: Transient, RetryAfter: 3 * time.Second},
			want: []string{"transient", "retry after 3s"},
		},
		{
			name: "scope is not reported for a non-fatal kind",
			err:  &Error{Kind: Invalid, Scope: ScopeRecipe},
			want: []string{"invalid"},
			not:  []string{"recipe"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.err.Error()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("Error() = %q, want it to contain %q", got, w)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("Error() = %q, want it NOT to contain %q", got, n)
				}
			}
		})
	}
}

func TestLogGenerateFailureLevelsAndRule3(t *testing.T) {
	// The level distinction is the whole reason Config.Logger exists: WARN
	// means it is expected to self-heal, ERROR means a human must look.
	// RULE-3 is the other half — a schema-violation error can carry the
	// model's raw output, so the wrapped message must never reach the log.
	cases := []struct {
		name      string
		err       *Error
		wantLevel string
	}{
		{"transient self-heals", &Error{Kind: Transient, Err: errors.New("SECRET MODEL OUTPUT")}, "WARN"},
		{"invalid needs a human", &Error{Kind: Invalid, Err: errors.New("SECRET MODEL OUTPUT")}, "ERROR"},
		{"fatal needs a human", &Error{Kind: Fatal, Scope: ScopeAccount, Err: errors.New("SECRET MODEL OUTPUT")}, "ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			logGenerateFailure(context.Background(), logger, "gpt-4o", tc.err)

			line := buf.String()
			if !strings.Contains(line, "level="+tc.wantLevel) {
				t.Errorf("logged at the wrong level: %q, want %s", line, tc.wantLevel)
			}
			if !strings.Contains(line, "llm_"+tc.err.Kind.String()) {
				t.Errorf("log line does not carry the kind: %q", line)
			}
			if !strings.Contains(line, "gpt-4o") {
				t.Errorf("log line does not carry the model: %q", line)
			}
			if strings.Contains(line, "SECRET MODEL OUTPUT") {
				t.Errorf("RULE-3 violation: the wrapped error message reached the log line: %q", line)
			}
		})
	}
}

func TestLogGenerateFailureIsSafeWithNothingToLog(t *testing.T) {
	// Both guards exist because this is called on an error path, which is
	// exactly where a nil dereference would be worst.
	logGenerateFailure(context.Background(), nil, "m", &Error{Kind: Fatal})
	var buf bytes.Buffer
	logGenerateFailure(context.Background(), slog.New(slog.NewTextHandler(&buf, nil)), "m", nil)
	if buf.Len() != 0 {
		t.Errorf("a nil error logged something: %q", buf.String())
	}
}

func TestBuildSteeringNamesTheItemsField(t *testing.T) {
	// The steering text and the schema have to ask for the same shape: the
	// schema nests the slice under an object, so steering that says "return
	// N items" without naming the field is how the model ends up filling the
	// wrapper and leaving the array empty.
	got := buildSteering(Request{System: "be terse", MaxItems: 5})
	if !strings.Contains(got, "be terse") {
		t.Errorf("system prompt was dropped: %q", got)
	}
	if !strings.Contains(got, `"items"`) {
		t.Errorf("steering does not name the items field: %q", got)
	}
	if !strings.Contains(got, "5") {
		t.Errorf("steering does not carry MaxItems: %q", got)
	}

	if got := buildSteering(Request{}); got != "" {
		t.Errorf("an empty request produced steering %q, want none", got)
	}
	if got := buildSteering(Request{MaxItems: -1}); got != "" {
		t.Errorf("a negative MaxItems produced steering %q, want none", got)
	}
	if got := buildSteering(Request{System: "only system"}); got != "only system" {
		t.Errorf("steering = %q, want just the system prompt", got)
	}
}
