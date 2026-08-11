package rpc

import (
	"strings"
	"testing"
	"time"
)

// A page token is opaque to the client by design (§11), which means nothing
// outside this package can notice when it stops round-tripping — a caller
// just silently gets the wrong page, or page 1 forever.

func TestItemPageTokenRoundTrips(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		id   int64
	}{
		{"ordinary", time.Date(2026, 8, 11, 14, 32, 5, 0, time.UTC), 42},
		{"sub-second precision survives", time.Date(2026, 8, 11, 14, 32, 5, 123456789, time.UTC), 1},
		{"zero id", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 0},
		{"large id", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 1 << 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok := itemEncodePageToken(tc.at, tc.id)
			if tok == "" {
				t.Fatal("encoded an empty token")
			}
			// Opaque means opaque: the id must not be legible in the token,
			// or callers will start parsing it and the format becomes an API.
			if strings.Contains(tok, "|") {
				t.Errorf("token %q leaks its internal separator", tok)
			}

			gotAt, gotID, err := itemDecodePageToken(tok)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if gotID != tc.id {
				t.Errorf("id round-tripped to %d, want %d", gotID, tc.id)
			}
			if !gotAt.Equal(tc.at) {
				t.Errorf("timestamp round-tripped to %s, want %s", gotAt, tc.at)
			}
		})
	}
}

func TestItemPageTokenRejectsGarbage(t *testing.T) {
	// A cursor is attacker-reachable input: it arrives on the wire from a
	// browser. Every one of these must be an error, never a panic and never
	// a silently-zero cursor that restarts pagination at the newest row.
	cases := map[string]string{
		"empty":              "",
		"not base64":         "!!!!",
		"base64 but no pipe": "aGVsbG8",          // "hello"
		"bad timestamp":      "bm90LWEtdGltZXwx", // "not-a-time|1"
		"bad id":             "MjAyNi0wMS0wMVQwMDowMDowMFp8bm90LWFuLWlk",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			at, id, err := itemDecodePageToken(tok)
			if err == nil {
				t.Fatalf("decoded %q into (%s, %d), want an error", tok, at, id)
			}
			if !at.IsZero() || id != 0 {
				t.Errorf("a failed decode returned (%s, %d), want zero values", at, id)
			}
		})
	}
}

func TestFeedEscapeLikeNeutralizesWildcards(t *testing.T) {
	// Without this a search for "50%" matches everything, and one for "_"
	// matches every single-character title — a search box that quietly
	// ignores what was typed.
	cases := map[string]string{
		"plain":      "plain",
		"50%":        `50\%`,
		"a_b":        `a\_b`,
		`back\slash`: `back\\slash`,
		`%_\`:        `\%\_\\`,
		"":           "",
	}
	for in, want := range cases {
		if got := feedEscapeLike(in); got != want {
			t.Errorf("feedEscapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}
