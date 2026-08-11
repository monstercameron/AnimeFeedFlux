package model

import (
	"testing"
	"time"
)

// This package had no tests at all. The three methods here are small, but two
// of them decide what a published document says, and the third decides
// whether a permalink 410s.

func TestItemIsDeletedIsDrivenByTheTimestamp(t *testing.T) {
	// There is no hard delete and no boolean column: the zero DeletedAt IS
	// "not deleted". A future refactor that introduces a separate flag would
	// have two sources of truth for whether a permalink 410s.
	if (Item{}).IsDeleted() {
		t.Error("a zero-value item reported itself deleted")
	}
	if !(Item{DeletedAt: time.Unix(1, 0)}).IsDeleted() {
		t.Error("an item with a deletion timestamp did not report itself deleted")
	}
}

func TestItemHasAnswer(t *testing.T) {
	if (Item{}).HasAnswer() {
		t.Error("an item with no answer reported one")
	}
	if !(Item{AnswerHTML: "<p>42</p>"}).HasAnswer() {
		t.Error("an item with an answer did not report one")
	}
	// Whitespace is content as far as this is concerned — the renderer is
	// what decides whether to emit it, and pretending an all-space answer is
	// absent here would hide a data bug instead of surfacing it.
	if !(Item{AnswerHTML: " "}).HasAnswer() {
		t.Error("a whitespace answer was treated as absent")
	}
}

func TestChannelNewestPublished(t *testing.T) {
	build := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	older := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC)

	t.Run("picks the latest item regardless of order", func(t *testing.T) {
		c := Channel{BuildTime: build, Items: []Item{
			{PublishedAt: newer}, {PublishedAt: older},
		}}
		if got := c.NewestPublished(); !got.Equal(newer) {
			t.Errorf("NewestPublished() = %s, want %s", got, newer)
		}
	})

	t.Run("falls back to BuildTime for an empty feed", func(t *testing.T) {
		// A feed with no items still has to emit a valid lastBuildDate; the
		// zero time would render as year 1 and is rejected by validators.
		c := Channel{BuildTime: build}
		if got := c.NewestPublished(); !got.Equal(build) {
			t.Errorf("NewestPublished() = %s, want the build time %s", got, build)
		}
	})

	t.Run("falls back when every item is unpublished", func(t *testing.T) {
		c := Channel{BuildTime: build, Items: []Item{{}, {}}}
		if got := c.NewestPublished(); !got.Equal(build) {
			t.Errorf("NewestPublished() = %s, want the build time %s", got, build)
		}
	})

	t.Run("an item published after the build time still wins", func(t *testing.T) {
		// This is not hypothetical: BuildTime is stamped when the document is
		// rendered, and a cached document can be older than an item committed
		// a moment ago.
		after := build.Add(time.Hour)
		c := Channel{BuildTime: build, Items: []Item{{PublishedAt: after}}}
		if got := c.NewestPublished(); !got.Equal(after) {
			t.Errorf("NewestPublished() = %s, want %s", got, after)
		}
	})
}
