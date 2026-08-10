// This file closes the other half of the RULE-6 gap TODOS.md names: even
// once a caller CAN invalidate the publish plane's cache
// (internal/publish/invalidate.go), nothing in this package ever calls that
// invalidator, because every write here is a plain *Store method with no
// notion that a cache exists downstream — correctly so, since store.go,
// items.go, runs.go and samples.go must not import internal/publish (the
// dependency would run backwards: the read-only publish plane already
// depends on store-shaped data, store must not depend back on publish).
//
// Hooked is the seam: a thin wrapper around *Store that fires a WriteHook
// after each write this package exposes that changes a feed's published
// representation, and does nothing else. It is a wrapper rather than a change
// to *Store's methods because store.go/items.go/runs.go/samples.go are
// off-limits to edit for this change.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// WriteHook is called with the slug of the feed whose published
// representation just changed. It is called synchronously, AFTER the
// underlying write's transaction has committed — never from inside one.
//
// That ordering is load-bearing, not a style preference (PLAN.md §9's
// closing paragraph, cited in TODOS.md RULE-6): cache invalidation is
// idempotent and self-healing — a missed one repairs itself on the next
// write or restart, and a cache is not a source of truth — so it belongs
// outside the transaction on both counts that matter:
//
//  1. Firing before commit would invalidate the cache for a write that then
//     rolls back (e.g. PromoteSample's UNIQUE(feed_id, published_at) retry
//     path, or CommitRun aborting on a duplicate item), which drops good
//     cached content for a change that never actually happened.
//  2. This is a single-writer SQLite database (store.go: `writer.SetMaxOpenConns(1)`).
//     A hook is arbitrary caller code — it may call an HTTP-adjacent
//     Invalidator, acquire its own lock, or simply be slow — and running it
//     while still inside a transaction would hold that transaction's grip on
//     the one writer connection for as long as the hook takes, serializing
//     every other write in the system behind it.
//
// A hook must not itself begin a write against this *Store — see Hooked's
// doc comment for why that would deadlock on the single writer connection.
type WriteHook func(feedSlug string)

// Hooked wraps a *Store so that every write it exposes which changes a
// feed's published representation fires a WriteHook once, after commit.
// Construct one with NewHooked and have callers that mutate data (generation,
// sampling, the future FeedService/ItemService RPC implementations) hold a
// *Hooked instead of a *Store — everything *Store exposes is still reachable
// through the embedded field, so this is additive, not a narrowing.
//
// Firing after commit means a hook that panics or blocks does not corrupt or
// stall the write it followed — the write already happened — but a hook that
// tries to write back into this same *Store (directly, or indirectly through
// another *Hooked wrapping the same *Store) WILL deadlock or block, because
// SQLite is configured single-writer (store.go) and the write connection is
// not reentrant across a still-open db.ExecContext call stack in the way a
// naive hook implementation might assume. Cache invalidation (the intended
// use) never writes to this database, so this is a constraint on any other
// use of WriteHook, not on the intended one.
type Hooked struct {
	*Store
	hook WriteHook
}

// NewHooked wraps s. A nil hook is accepted and turns every wrapped method
// into a plain passthrough — useful for tests and for any caller (like
// samples_test.go's helpers) that wants *Store's behavior without opting
// into hook semantics.
func NewHooked(s *Store, hook WriteHook) *Hooked {
	return &Hooked{Store: s, hook: hook}
}

// fire calls the hook if both it and slug are present. A blank slug (should
// never happen — every write below resolves one or fails before reaching
// here) is swallowed rather than passed on, since PLAN.md §11's invalidation
// contract is keyed by slug and an empty key would either no-op against a
// real Invalidator or, worse, be misread as "invalidate everything" by some
// future implementation.
func (h *Hooked) fire(slug string) {
	if h.hook != nil && slug != "" {
		h.hook(slug)
	}
}

// slugForFeedID resolves a feed id to its slug for the hook call. It is a
// direct query against the writer connection rather than a new *Store
// method, because items.go (which owns GetFeedBySlug, the closest existing
// analogue) is off-limits to edit for this change — GetFeedBySlug looks up
// by slug, not id, and there is no reverse lookup to reuse.
func (h *Hooked) slugForFeedID(ctx context.Context, feedID int64) (string, error) {
	var slug string
	err := h.Writer().QueryRowContext(ctx, `SELECT slug FROM feeds WHERE id = ?`, feedID).Scan(&slug)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: resolving slug for feed %d: %w", feedID, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("store: resolving slug for feed %d: %w", feedID, err)
	}
	return slug, nil
}

// slugForItemKey resolves an item's owning feed's slug by item_key, for the
// two item writes (SoftDeleteItem, RestoreItem) whose *Store signature takes
// only the key, not the feed id.
func (h *Hooked) slugForItemKey(ctx context.Context, itemKey string) (string, error) {
	var slug string
	err := h.Writer().QueryRowContext(ctx,
		`SELECT f.slug FROM items i JOIN feeds f ON f.id = i.feed_id WHERE i.item_key = ?`, itemKey,
	).Scan(&slug)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: resolving slug for item %q: %w", itemKey, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("store: resolving slug for item %q: %w", itemKey, err)
	}
	return slug, nil
}

// slugForRunID resolves a run's owning feed's slug, for CommitRun/FailRun —
// the run row carries feed_id even when the run committed zero items (a
// generative run that skipped every candidate for novelty still needs its
// feed's "why is this feed stale" view invalidated, per §9's "log it" step).
func (h *Hooked) slugForRunID(ctx context.Context, runID int64) (string, error) {
	var slug string
	err := h.Writer().QueryRowContext(ctx,
		`SELECT f.slug FROM runs r JOIN feeds f ON f.id = r.feed_id WHERE r.id = ?`, runID,
	).Scan(&slug)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: resolving slug for run %d: %w", runID, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("store: resolving slug for run %d: %w", runID, err)
	}
	return slug, nil
}

// --- feed-level writes ---------------------------------------------------
//
// PLAN.md §11 is explicit that the rule covers feed-level writes
// (FeedService.Update, SetEnabled, SetMembers) as well as item writes,
// because a feed's title, og:image or TTL is baked into every channel
// element and an aggregate's membership determines what it renders — a
// write that touches zero item rows can still change rendered output.
//
// As of this change, *Store (items.go) implements only CreateFeed and
// GetFeedBySlug; Update/SetEnabled/SetMembers do not exist yet at the store
// layer — they are FeedService RPC surface (PLAN.md §11, TODOS.md B1-03,
// B1-12, B1-13), and TODOS.md shows B1 entirely unchecked, i.e. not yet
// built. There is therefore nothing yet on *Store for Hooked to wrap for
// those three writes; items.go cannot be edited to add them as part of this
// change. CreateFeed is wrapped below on the same "changes published
// representation" reasoning (a newly created, enabled feed's title can reach
// the "/" index immediately), so the pattern this file establishes is ready
// to extend the moment Update/SetEnabled/SetMembers land on *Store — wrap
// them exactly as CreateFeed is wrapped here, firing on f.Slug (Update) or
// the resolved slug (SetEnabled/SetMembers take an id or slug already).

// CreateFeed delegates to *Store.CreateFeed, then fires on the new feed's own
// slug (already known from the input — no lookup needed).
func (h *Hooked) CreateFeed(ctx context.Context, f model.Feed) (int64, error) {
	id, err := h.Store.CreateFeed(ctx, f)
	if err != nil {
		return 0, err
	}
	h.fire(f.Slug)
	return id, nil
}

// --- item writes -----------------------------------------------------------

// InsertItem delegates to *Store.InsertItem, then fires on the item's feed.
func (h *Hooked) InsertItem(ctx context.Context, it model.Item) (int64, error) {
	id, err := h.Store.InsertItem(ctx, it)
	if err != nil {
		return 0, err
	}
	slug, serr := h.slugForFeedID(ctx, it.FeedID)
	if serr != nil {
		// The write already committed; a failure to resolve the slug for the
		// hook must not be reported as if the write itself failed (a caller
		// checking err != nil would wrongly believe nothing was inserted and
		// might retry, producing a duplicate). It is returned as a distinct,
		// wrapped error so it is visible rather than silently swallowed.
		return id, fmt.Errorf("store: item %d committed but cache invalidation could not resolve its feed slug: %w", id, serr)
	}
	h.fire(slug)
	return id, nil
}

// UpdateItem delegates to *Store.UpdateItem, then fires on it.FeedID's slug
// — trusted the same way UpdateItem itself trusts the caller-supplied row
// (it.ID identifies which row; item_key is deliberately never taken from
// the caller, per items.go's own comment). it.FeedID does not change on an
// edit (no feed_id in UpdateItem's SET list), so the caller's copy of it is
// as reliable a source for the slug lookup as a fresh SELECT would be.
func (h *Hooked) UpdateItem(ctx context.Context, it model.Item, expectedVersion int64) error {
	if err := h.Store.UpdateItem(ctx, it, expectedVersion); err != nil {
		return err
	}
	slug, serr := h.slugForFeedID(ctx, it.FeedID)
	if serr != nil {
		return fmt.Errorf("store: item %d updated but cache invalidation could not resolve its feed slug: %w", it.ID, serr)
	}
	h.fire(slug)
	return nil
}

// SoftDeleteItem delegates to *Store.SoftDeleteItem, then fires on the
// deleted item's feed. The slug is resolved from itemKey before delegating
// — cheap, avoids doing it twice, and item_key -> feed_id never changes so
// there is no staleness risk in reading it either before or after the
// delete.
func (h *Hooked) SoftDeleteItem(ctx context.Context, itemKey string) error {
	slug, serr := h.slugForItemKey(ctx, itemKey)
	if serr != nil {
		return serr
	}
	if err := h.Store.SoftDeleteItem(ctx, itemKey); err != nil {
		return err
	}
	h.fire(slug)
	return nil
}

// RestoreItem delegates to *Store.RestoreItem, then fires on the restored
// item's feed.
func (h *Hooked) RestoreItem(ctx context.Context, itemKey string) error {
	slug, serr := h.slugForItemKey(ctx, itemKey)
	if serr != nil {
		return serr
	}
	if err := h.Store.RestoreItem(ctx, itemKey); err != nil {
		return err
	}
	h.fire(slug)
	return nil
}

// --- run / generation writes -----------------------------------------------

// CommitRun delegates to *Store.CommitRun, then fires on the run's feed.
// This is generation's own write (PLAN.md §9 step 7/8): the items and the
// closed run row commit together inside CommitRun's own transaction, and
// this hook fires strictly after that call returns successfully — i.e.
// strictly after commit, satisfying RULE-6's "outside the transaction"
// requirement without this file ever opening or touching a transaction
// itself.
func (h *Hooked) CommitRun(ctx context.Context, runID int64, items []model.Item, summary RunSummary) error {
	if err := h.Store.CommitRun(ctx, runID, items, summary); err != nil {
		return err
	}
	slug, serr := h.slugForRunID(ctx, runID)
	if serr != nil {
		return fmt.Errorf("store: run %d committed but cache invalidation could not resolve its feed slug: %w", runID, serr)
	}
	h.fire(slug)
	return nil
}

// PromoteSample delegates to *Store.PromoteSample, then fires on the
// promoted item's feed.
//
// The slug MUST be resolved before delegating, not after: promoteOnce
// deletes the samples row in the same transaction that inserts the item, so
// by the time PromoteSample returns successfully the `id` sample row is
// gone and "look up sample id's feed_id" is no longer possible. The feed a
// sample belongs to never changes between creation and promotion/discard —
// PutSample fixes feed_id at creation and nothing in samples.go ever
// updates it — so reading it before the call is exactly as accurate as
// reading it after would have been, had that still been possible.
func (h *Hooked) PromoteSample(ctx context.Context, id int64, item model.Item) (int64, error) {
	var feedID int64
	if err := h.Writer().QueryRowContext(ctx,
		`SELECT feed_id FROM samples WHERE id = ?`, id,
	).Scan(&feedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("store: promoting sample %d: %w", id, ErrNotFound)
		}
		return 0, fmt.Errorf("store: reading sample %d: %w", id, err)
	}

	itemID, err := h.Store.PromoteSample(ctx, id, item)
	if err != nil {
		return 0, err
	}

	slug, serr := h.slugForFeedID(ctx, feedID)
	if serr != nil {
		return itemID, fmt.Errorf("store: sample %d promoted but cache invalidation could not resolve its feed slug: %w", id, serr)
	}
	h.fire(slug)
	return itemID, nil
}
