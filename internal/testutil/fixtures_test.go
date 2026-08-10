package testutil

import (
	"reflect"
	"strings"
	"testing"
)

// TestFixedTimeDeterministic guards against a future edit reintroducing
// time.Now: every caller across the suite depends on this being the same
// instant every run.
func TestFixedTimeDeterministic(t *testing.T) {
	a, b := FixedTime(), FixedTime()
	if !a.Equal(b) {
		t.Fatalf("FixedTime not deterministic: %v != %v", a, b)
	}
	if a.Location() != a.UTC().Location() {
		t.Fatalf("FixedTime not UTC: %v", a)
	}
}

// TestSampleFeedDeterministic asserts two calls produce identical values, so
// a golden file recorded from one call stays valid against every other.
func TestSampleFeedDeterministic(t *testing.T) {
	a, b := SampleFeed(), SampleFeed()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("SampleFeed not deterministic:\n%+v\n%+v", a, b)
	}
}

func TestSampleChannelDeterministic(t *testing.T) {
	a, b := SampleChannel(5), SampleChannel(5)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("SampleChannel not deterministic:\n%+v\n%+v", a, b)
	}
}

// TestSampleItemsDeterministic guards the same property for items
// specifically, since they're built with a loop and an index-derived key
// rather than copied from a literal.
func TestSampleItemsDeterministic(t *testing.T) {
	a, b := SampleItems(10), SampleItems(10)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("SampleItems not deterministic:\n%+v\n%+v", a, b)
	}
}

// TestSampleItemsPublishedAtStrictlyIncreasing is the property renderers and
// the store rely on directly (§5.5: Slack silently drops items sharing a
// timestamp).
func TestSampleItemsPublishedAtStrictlyIncreasing(t *testing.T) {
	items := SampleItems(20)
	for i := 1; i < len(items); i++ {
		prev, cur := items[i-1].PublishedAt, items[i].PublishedAt
		if !cur.After(prev) {
			t.Fatalf("item %d PublishedAt %v not strictly after item %d PublishedAt %v", i, cur, i-1, prev)
		}
	}
}

// TestSampleItemsKeysUnique guards ItemKey uniqueness, since a real ULID
// source would never collide and a hand-built fixture generator could.
func TestSampleItemsKeysUnique(t *testing.T) {
	items := SampleItems(50)
	seen := make(map[string]int, len(items))
	for i, it := range items {
		if len(it.ItemKey) != 26 {
			t.Fatalf("item %d ItemKey %q is %d chars, want 26", i, it.ItemKey, len(it.ItemKey))
		}
		for _, c := range it.ItemKey {
			if !strings.ContainsRune(crockford32, c) {
				t.Fatalf("item %d ItemKey %q contains %q, outside Crockford base32 alphabet", i, it.ItemKey, c)
			}
		}
		if prev, ok := seen[it.ItemKey]; ok {
			t.Fatalf("item %d ItemKey %q duplicates item %d", i, it.ItemKey, prev)
		}
		seen[it.ItemKey] = i
	}
}

// TestTriviaItemAnswerDoesNotLeak is the whole reason TriviaItem exists as a
// fixture: §5.5 requires AnswerHTML never reach SummaryText, BodyHTML sans
// the question itself, or (by extension) any renderer field that becomes an
// og:description. This asserts the fixture itself keeps that promise, so
// every renderer test built on it starts from a clean input.
func TestTriviaItemAnswerDoesNotLeak(t *testing.T) {
	item := TriviaItem()

	const answerToken = "ANSWER-COWBOY-BEBOP"

	if !strings.Contains(item.AnswerHTML, answerToken) {
		t.Fatalf("TriviaItem AnswerHTML %q missing expected token %q", item.AnswerHTML, answerToken)
	}
	if strings.Contains(item.SummaryText, answerToken) {
		t.Fatalf("TriviaItem SummaryText leaks answer token: %q", item.SummaryText)
	}
	if strings.Contains(item.Title, answerToken) {
		t.Fatalf("TriviaItem Title leaks answer token: %q", item.Title)
	}
}

func TestTriviaItemDeterministic(t *testing.T) {
	a, b := TriviaItem(), TriviaItem()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("TriviaItem not deterministic:\n%+v\n%+v", a, b)
	}
}
