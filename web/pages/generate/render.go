//go:build js && wasm

// render.go owns Render, the /generate page's single top-level component.
// Every cross-pane hook (the RPC resources, the editor draft, the sampler
// stream state) is called here, unconditionally and in a fixed order
// every render — never behind a Go-level `if`/`switch` that could skip
// one on some renders, since GWC's On*/Use* hooks are positional per
// fiber (confirmed by reading internal/runtime/hooks.go's GoUseFunc
// directly: every call does `hooks.index++` unconditionally). The three
// panes are mounted as their OWN child components via ui.CreateElement
// rather than called as plain functions, so a real structural difference
// inside any one of them (e.g. the editor's "no feed selected" vs "form"
// states, which have very different hook counts) never has to line up
// with the others' or with Render's own sequence — see render_editor.go's
// package doc comment for the full pattern this file and its siblings
// follow.
package generatepage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/fetch"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/interop"
	"github.com/monstercameron/GoWebComponents/v5/state"
	"github.com/monstercameron/GoWebComponents/v5/ui"
	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
	wui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// tapeWindowDays is the width of the tape's fixed time window (the
// signature element render_rail.go draws per feed — see this task's brief
// and docs/design-direction.md's "Signature: the tape"). Fixed rather than
// derived from the data span so an empty/near-empty tape still reads as "no
// activity in two weeks", not as a degenerate zero-width scale.
const tapeWindowDays = 14

// persistedSampleStorageKey is the single localStorage key the sampler's
// candidates/sampleID snapshot lives under. FeedSlug (inside the envelope,
// not the key) disambiguates which feed the snapshot belongs to, so
// switching feeds without a matching snapshot just reads a non-match
// instead of needing a second storage round-trip per slug.
const persistedSampleStorageKey = "generate.sampler.samples.v1"

// loadPersistedSampleState reads and decodes the D2-29 sample snapshot from
// localStorage. Any failure (storage unavailable, nothing stored, corrupt
// JSON) is reported as "not found" rather than an error the page has to
// surface — a missing/broken snapshot degrades to the pre-D2-29 behavior
// (candidates just start empty), it never blocks the page.
func loadPersistedSampleState() (PersistedSampleState, bool) {
	store, err := interop.LocalStorage()
	if err != nil {
		return PersistedSampleState{}, false
	}
	raw, found, err := store.GetItem(persistedSampleStorageKey)
	if err != nil || !found || raw == "" {
		return PersistedSampleState{}, false
	}
	var ps PersistedSampleState
	if err := json.Unmarshal([]byte(raw), &ps); err != nil {
		return PersistedSampleState{}, false
	}
	return ps, true
}

// savePersistedSampleState writes (or, with a zero-value ps, effectively
// clears — SampleID/Candidates empty fails PersistedSampleUsable) the
// D2-29 snapshot. Storage errors (quota, unavailable) are swallowed: this
// is a nice-to-have surviving a refresh, never a blocker for sampling
// itself.
func savePersistedSampleState(ps PersistedSampleState) {
	store, err := interop.LocalStorage()
	if err != nil {
		return
	}
	data, err := json.Marshal(ps)
	if err != nil {
		return
	}
	_ = store.SetItem(persistedSampleStorageKey, string(data))
}

// clearPersistedSampleState removes the D2-29 snapshot outright (used when
// a sample is discarded, or a stale/mismatched entry is found on load).
func clearPersistedSampleState() {
	store, err := interop.LocalStorage()
	if err != nil {
		return
	}
	_ = store.RemoveItem(persistedSampleStorageKey)
}

// Feed-window and TTL fallbacks for a brand-new draft, used when the admin
// has not configured a default. The feed window matches the schema's own
// default; the TTL matches internal/publish's hardcoded 15-minute
// Cache-Control, so a feed's advertised TTL and the header it is served with
// agree out of the box.
// These mirror internal/rpc's DefaultFeed* constants, which seed the same
// values into Settings. They cannot be imported (this is browser code and
// that is server code), so the duplication is deliberate and only matters
// when Settings has no value to supply — see newFeedDraft, which prefers the
// configured default whenever there is one.
const (
	newFeedItemsPerRun     = 1
	newFeedWindowFallback  = 50
	newFeedTTLMinutes      = 15
	newFeedTokenBudget     = 100_000
	newFeedRunBudgetPerDay = 24

	// A new feed needs a VALID schedule, not an empty one.
	//
	// internal/feedspec rejects an empty cron with `cron_invalid` and an
	// empty timezone with `timezone_invalid`, so a draft that left both blank
	// could never be saved — the same class of trap as the zero budgets, and
	// the one that actually made "create a feed" impossible in practice.
	// 09:00 daily is a real, deliberate default: once a day matches what this
	// app is for, and a morning slot means a failed overnight run is visible
	// when someone is awake to see it. UTC because the browser's zone is not
	// available to this code and a wrong-but-confident local zone is worse
	// than an obvious one the operator will change.
	newFeedCron     = "0 9 * * *"
	newFeedTimezone = "UTC"
)

// newFeedDraft builds the blank feed the "+ New" control starts from.
//
// It used to set ItemsPerRun and FeedWindow and nothing else, which made a
// new feed UNSAVEABLE: internal/feedspec's validateBudgets rejects a zero
// daily token budget and a zero daily run budget (ReasonBudgetDailyTokensZero
// / ReasonBudgetDailyRunsZero), so the first save of every new feed failed
// validation on two fields the operator had never been shown and had no
// reason to think were required.
//
// The admin's configured defaults win where they are set. Those settings
// (§12.5's default_daily_token_budget / default_daily_run_budget /
// default_feed_window) previously had no reader anywhere in the app — they
// were editable, persisted, and inert. This is what they are for.
func newFeedDraft(g *affv1.Settings_Generation) *affv1.Feed {
	spec := &affv1.FeedSpec{
		Cron:             newFeedCron,
		Timezone:         newFeedTimezone,
		ItemsPerRun:      newFeedItemsPerRun,
		FeedWindow:       newFeedWindowFallback,
		DailyTokenBudget: newFeedTokenBudget,
		DailyRunBudget:   newFeedRunBudgetPerDay,
	}
	if v := g.GetDefaultFeedWindow(); v > 0 {
		spec.FeedWindow = v
	}
	if v := g.GetDefaultDailyTokenBudget(); v > 0 {
		spec.DailyTokenBudget = v
	}
	if v := g.GetDefaultDailyRunBudget(); v > 0 {
		spec.DailyRunBudget = v
	}
	// A zero TTL renders as <ttl>0</ttl>, which tells every aggregator to
	// never cache; the column's own schema default is 15 but the RPC always
	// sends an explicit value, so the default never applied to a feed created
	// from this page.
	// Kind must be explicit. Left at the proto zero value
	// (FEED_KIND_UNSPECIFIED) the select matches none of its three options, so
	// the browser displays the FIRST one — "Generative" — while the value
	// underneath is unspecified. Validate then rejects a draft nobody touched,
	// complaining about a field that visibly shows a legal value. Generative
	// is also the right default: it is the kind that needs no sources.
	return &affv1.Feed{
		Spec:       spec,
		Kind:       affv1.FeedKind_FEED_KIND_GENERATIVE,
		TtlMinutes: newFeedTTLMinutes,
	}
}

// Render is registered against "/generate" (see deps.go's init). It is a
// router.Component body (via web/shell/pages.go's pageComponent), so GWC
// mounts/unmounts it on navigation like any other page.
func Render() ui.Node {
	sess := state.UseAtomKey(shell.SessionAtom)
	connected := sess.Get() != appstate.Disconnected
	killed := sess.Get() == appstate.Killed

	// --- Rail data -----------------------------------------------------
	feedsRes := fetch.UseResource(func(ctx context.Context) ([]*affv1.Feed, error) {
		if !wired() {
			return nil, errNotWired
		}
		resp, err := deps.Feed.List(ctx, &affv1.FeedServiceListRequest{PageSize: 200})
		if err != nil {
			return nil, err
		}
		return resp.GetFeeds(), nil
	})
	// The public base URL, for the subscribe-URL panel (render_urls.go).
	// It lives in Settings and this page has no other way to know it — a feed
	// URL is base + slug + extension, and two of those three are here.
	settingsRes := fetch.UseResource(func(ctx context.Context) (*affv1.SystemServiceGetSettingsResponse, error) {
		if !wired() || deps.System == nil {
			return nil, errNotWired
		}
		return deps.System.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
	})
	// The provider's own model list, so the strip's model control is a CHOSEN
	// value rather than a typed one — same reasoning, and the same RPC, as
	// the Settings provider screen (internal/rpc/models.go). ListModels never
	// fails the request; it reports Unavailable, and the strip falls back to
	// the text input in that case.
	modelsRes := fetch.UseResource(func(ctx context.Context) (*affv1.SystemServiceListModelsResponse, error) {
		if !wired() || deps.System == nil {
			return nil, errNotWired
		}
		return deps.System.ListModels(ctx, &affv1.SystemServiceListModelsRequest{})
	})

	// UseResource fetches once at mount, and on a hard load of /generate that
	// mount happens while the session is still ANON: the WebSocket has not
	// finished its handshake, so every loader above ran against a socket that
	// could not carry the call, and nothing re-ran them afterwards. The feed
	// picker was permanently empty on a page navigated straight to — which
	// reads as "no feeds exist", the one thing it did not mean.
	//
	// Keyed on the session state ITSELF, not on a `connected` boolean:
	// appstate.Anon is the zero value and `state != Disconnected` is already
	// true there, so a bool never changes when the session actually becomes
	// usable and the effect would never re-fire.
	ui.UseEffect(func() func() {
		if sess.Get() == appstate.Auth || sess.Get() == appstate.Killed {
			feedsRes.Reload()
			settingsRes.Reload()
			modelsRes.Reload()
		}
		return nil
	}, sess.Get())

	// Feed deletion state: which feed the confirmation is open for, what has
	// been typed into it, and whether the call is in flight.
	// pump keeps renders flowing while a mutation is in flight — see
	// web/ui/pump.go for the GWC defect it exists for.
	pump := wui.UsePump()
	pendingDelete := ui.UseState((*affv1.Feed)(nil))
	deleteTyped := ui.UseState("")
	deleting := ui.UseState(false)
	// railKebab is the slug whose row menu is open, "" for none — held here
	// rather than per row so opening one closes the last.
	railKebab := ui.UseState("")
	// stripMenu is whether the strip's per-feed ⋯ menu is open.
	stripMenu := ui.UseState(false)

	// Effort is a per-preview knob on the strip; it is not a recipe field
	// today, so it lives here rather than on the draft feed.
	effort := ui.UseState("smart")
	statsRes := fetch.UseResource(func(ctx context.Context) (*affv1.SystemServiceStatsResponse, error) {
		if !wired() || deps.System == nil {
			return nil, errNotWired
		}
		return deps.System.Stats(ctx, &affv1.SystemServiceStatsRequest{})
	})
	// itemsRes feeds render_rail.go's tape (the signature element,
	// docs/design-direction.md: "one tick per item, positioned by
	// published_at") — a flat recent-items page across every feed, grouped
	// per-row by FeedId in renderRailRow rather than one request per feed.
	itemsRes := fetch.UseResource(func(ctx context.Context) ([]*affv1.Item, error) {
		if !wired() || deps.Item == nil {
			return nil, errNotWired
		}
		resp, err := deps.Item.List(ctx, &affv1.ItemServiceListRequest{PageSize: 300})
		if err != nil {
			return nil, err
		}
		return resp.GetItems(), nil
	})
	// runsRes drives the rail's 7-day spend column (Run.EstCostUsd is the
	// only cost figure this page has any RPC for — Feed/SystemService carry
	// none — so it is aggregated client-side per feed_id over the trailing
	// week, same window as the tape).
	runsRes := fetch.UseResource(func(ctx context.Context) ([]*affv1.Run, error) {
		if !wired() || deps.Run == nil {
			return nil, errNotWired
		}
		resp, err := deps.Run.History(ctx, &affv1.RunServiceHistoryRequest{
			PageSize:     500,
			StartedAfter: timestamppb.New(time.Now().Add(-tapeWindowDays * 24 * time.Hour)),
		})
		if err != nil {
			return nil, err
		}
		return resp.GetRuns(), nil
	})

	// --- Editor state ----------------------------------------------------
	selectedSlug := ui.UseState("")
	draft := ui.UseState((*affv1.Feed)(nil))
	loadedSnapshot := ui.UseState((*affv1.Feed)(nil))
	fieldErrs := ui.UseState(FieldErrors(nil))
	saving := ui.UseState(false)
	saveErr := ui.UseState(error(nil))
	validateErr := ui.UseState(error(nil))
	conflictTheirs := ui.UseState((*affv1.Feed)(nil))
	conflictChoice := ui.UseState(ResolveTakeTheirs)
	perFieldKeepMine := ui.UseState(map[string]bool(nil))
	creatingNew := ui.UseState(false)

	// --- Sampler state ---------------------------------------------------
	sampleSize := ui.UseState(int32(1))
	tempOverride := ui.UseState(0.0)
	candidates := ui.UseState([]*affv1.SampleCandidate(nil))
	sampleID := ui.UseState("")
	sampling := ui.UseState(false)
	sampleErr := ui.UseState(error(nil))
	selectedCandidate := ui.UseState(0)
	selectedView := ui.UseState(ViewRendered)
	remainingBudget := ui.UseState(0.0)
	cancelGen := ui.UseRef(0)
	railErr := ui.UseState(error(nil))
	actionErr := ui.UseState(error(nil))

	// The unsaved-changes guard (D2-15) and the session-expiry hold
	// (web/shell D0-08) share one predicate: is the draft different from
	// what was loaded/saved. Registered every render so the closure always
	// sees the latest draft/loadedSnapshot state handles, matching
	// web/shell/session.go's own doc note that RegisterDirtyCheck may be
	// called any time a page owns editable state.
	shell.RegisterDirtyCheck(func() bool {
		return DraftDirty(loadedSnapshot.Get(), draft.Get())
	})

	// Loading a feed into the editor when the rail selection changes.
	// Effect-scoped (not read during render) per the "no browser/network
	// reads in the render body" rule; keyed on the slug string so it only
	// re-fires when the selection actually changes, not on every render.
	ui.UseEffect(func() func() {
		slug := selectedSlug.Get()
		if slug == "" || !wired() {
			return nil
		}
		ctxLoad, cancel := context.WithCancel(context.Background())
		go func() {
			resp, err := deps.Feed.Get(ctxLoad, &affv1.FeedServiceGetRequest{Slug: slug})
			if err != nil {
				// Surfaced, not swallowed. This used to be a bare `return`,
				// so a feed that failed to load left the editor showing the
				// PREVIOUS feed's fields with the picker showing the new
				// one's name — the worst of both, and indistinguishable from
				// a dead click. saveErr is the editor's own error slot; a
				// load failure and a save failure both mean "this form is not
				// what you think it is", which is the message either way.
				if ctxLoad.Err() == nil {
					saveErr.Set(err)
				}
				return
			}
			draft.Set(cloneFeed(resp.GetFeed()))
			loadedSnapshot.Set(cloneFeed(resp.GetFeed()))
			fieldErrs.Set(nil)
			saveErr.Set(nil)
			conflictTheirs.Set(nil)

			// D2-29: before wiping the sampler pane for the newly-selected
			// feed, check whether a still-fresh (<24h) snapshot for THIS
			// slug was left in localStorage by an earlier page load — a
			// stale/mismatched/expired entry is discarded rather than
			// resurrected, so switching feeds never leaks one feed's
			// candidates into another's pane.
			if ps, ok := loadPersistedSampleState(); ok && PersistedSampleUsable(ps, slug, time.Now()) {
				candidates.Set(ps.Candidates)
				sampleID.Set(ps.SampleID)
				sampleSize.Set(ps.SampleSize)
				tempOverride.Set(ps.TempOverride)
				selectedCandidate.Set(ps.SelectedCandidate)
				selectedView.Set(ps.SelectedView)
			} else {
				if ok {
					clearPersistedSampleState()
				}
				candidates.Set(nil)
				sampleID.Set("")
				// The sample-shaping knobs belong to the sample, not to the
				// session: leaving them set carried the previous feed's
				// candidate count and temperature override onto the next
				// feed, where the operator had not chosen them.
				sampleSize.Set(1)
				tempOverride.Set(0)
				selectedCandidate.Set(0)
			}
		}()
		return func() { cancel() }
	}, selectedSlug.Get())

	killedReason := ""
	if killed {
		killedReason = SampleDisabledReason(deps.I18n, true, false, 0, 0)
	} else if d := draft.Get(); d != nil {
		killedReason = SampleDisabledReason(deps.I18n, false, !d.GetEnabled() && d.GetId() != 0, d.GetConsecutiveFailures(), autoDisableThreshold)
	}

	if !wired() {
		return renderNotWired()
	}

	railProps := railProps{
		Connected: connected,
		Resource:  feedsRes,
		Stats:     statsRes,
		Items:     itemsRes,
		Runs:      runsRes,
		Selected:  selectedSlug.Get(),
		Err:       railErr.Get(),
		OnSelect: func(slug string) {
			selectedSlug.Set(slug)
			creatingNew.Set(false)
		},
		OnNew: func() {
			selectedSlug.Set("")
			creatingNew.Set(true)
			draft.Set(newFeedDraft(settingsRes.Get().Value.GetSettings().GetGeneration()))
			loadedSnapshot.Set(nil)
			fieldErrs.Set(nil)
		},
		// OnToggleEnabled/OnRunNow deliberately no longer gate on the
		// render-time `connected` snapshot before attempting the call
		// (TODOS.md D0-10): the guarded client refuses instantly with
		// wsconn.ErrDisconnected if the socket is not Ready regardless, so
		// gating here only risked a stale closure silently no-opping a
		// click with no feedback at all — which is exactly the bug this
		// task closes (previously neither handler surfaced any error, not
		// even a rejection). Attempting always and classifying the result
		// is what actually produces the required visible refusal.
		OnToggleEnabled: func(f *affv1.Feed) {
			if f == nil {
				return
			}
			railErr.Set(nil)
			pump.Run(func(done func()) {
				defer done()
				_, err := deps.Feed.SetEnabled(context.Background(), &affv1.FeedServiceSetEnabledRequest{
					FeedId: f.GetId(), Enabled: !f.GetEnabled(), ExpectedVersion: f.GetVersion(),
				})
				if err != nil {
					railErr.Set(err)
					return
				}
				feedsRes.Reload()
			})
		},
		OnRunNow: func(f *affv1.Feed) {
			if f == nil {
				return
			}
			railErr.Set(nil)
			pump.Run(func(done func()) {
				defer done()
				_, err := deps.Feed.RunNow(context.Background(), &affv1.FeedServiceRunNowRequest{FeedId: f.GetId()})
				if err != nil {
					railErr.Set(err)
					return
				}
				feedsRes.Reload()
			})
		},
		KebabOpen: railKebab.Get(),
		OnKebabOpen: func(slug string, open bool) {
			if open {
				railKebab.Set(slug)
				return
			}
			if railKebab.Get() == slug {
				railKebab.Set("")
			}
		},
		// Opening the confirmation is all this does. The delete itself is
		// behind a typed confirmation because the server's delete is soft but
		// there is no Restore RPC for feeds, so from here it is a one-way
		// door — and a deleted feed's URL stops resolving for everyone
		// already subscribed to it.
		OnDelete: func(f *affv1.Feed) {
			if f == nil {
				return
			}
			railKebab.Set("")
			railErr.Set(nil)
			deleteTyped.Set("")
			pendingDelete.Set(f)
		},
	}

	editorProps := editorProps{
		Connected:      connected,
		Draft:          draft.Get(),
		Loaded:         loadedSnapshot.Get(),
		IsNew:          creatingNew.Get(),
		FieldErrs:      fieldErrs.Get(),
		Saving:         saving.Get(),
		SaveErr:        saveErr.Get(),
		ValidateErr:    validateErr.Get(),
		ConflictTheirs: conflictTheirs.Get(),
		Resolution:     conflictChoice.Get(),
		OnFieldChange: func(mutate func(*affv1.Feed)) {
			d := draft.Get()
			if d == nil {
				return
			}
			next := cloneFeed(d)
			mutate(next)
			draft.Set(next)
		},
		OnValidate: func() {
			d := draft.Get()
			if d == nil {
				return
			}
			validateErr.Set(nil)
			pump.Run(func(done func()) {
				defer done()
				resp, err := deps.Feed.ValidateSpec(context.Background(), &affv1.FeedServiceValidateSpecRequest{
					Kind: d.GetKind(), Slug: d.GetSlug(), Spec: d.GetSpec(),
				})
				if err != nil {
					// D0-10: a transport-level failure here (disconnected or
					// otherwise) must not be swallowed just because
					// ValidateSpec's own contract is "return field errors,
					// not a top-level error" for validation failures proper.
					validateErr.Set(err)
					return
				}
				fieldErrs.Set(MapFieldErrors(resp.GetErrors()))
			})
		},
		OnSave: func() {
			d := draft.Get()
			if d == nil {
				return
			}
			saving.Set(true)
			saveErr.Set(nil)
			pump.Run(func(done func()) {
				defer done()
				defer saving.Set(false)
				var err error
				var saved *affv1.Feed
				if d.GetId() == 0 {
					resp, e := deps.Feed.Create(context.Background(), &affv1.FeedServiceCreateRequest{Feed: d})
					err = e
					if resp != nil {
						saved = resp.GetFeed()
					}
				} else {
					resp, e := deps.Feed.Update(context.Background(), &affv1.FeedServiceUpdateRequest{
						Feed: d, ExpectedVersion: loadedSnapshot.Get().GetVersion(),
					})
					err = e
					if resp != nil {
						saved = resp.GetFeed()
					}
				}
				if err != nil {
					if IsVersionConflict(err) {
						getResp, gErr := deps.Feed.Get(context.Background(), &affv1.FeedServiceGetRequest{Slug: d.GetSlug()})
						if gErr == nil {
							conflictTheirs.Set(getResp.GetFeed())
							return
						}
					}
					saveErr.Set(err)
					return
				}
				draft.Set(cloneFeed(saved))
				loadedSnapshot.Set(cloneFeed(saved))
				creatingNew.Set(false)
				feedsRes.Reload()
			})
		},
		OnResolveConflict: func(resolution ConflictResolution, keepMine map[string]bool) {
			theirs := conflictTheirs.Get()
			mine := draft.Get()
			if theirs == nil {
				return
			}
			merged := ApplyResolution(mine, theirs, resolution, keepMine)
			draft.Set(merged)
			loadedSnapshot.Set(cloneFeed(theirs))
			conflictTheirs.Set(nil)
		},
		SetResolution:     conflictChoice.Set,
		PerFieldKeepMine:  perFieldKeepMine.Get(),
		SetPerFieldChoice: perFieldKeepMine.Set,
	}

	samplerProps := samplerProps{
		Connected:       connected,
		Feed:            draft.Get(),
		DisabledReason:  killedReason,
		SampleSize:      sampleSize.Get(),
		SetSampleSize:   sampleSize.Set,
		TempOverride:    tempOverride.Get(),
		SetTempOverride: tempOverride.Set,
		Candidates:      candidates.Get(),
		Sampling:        sampling.Get(),
		SampleErr:       sampleErr.Get(),
		SelectedIndex:   selectedCandidate.Get(),
		SetSelected:     selectedCandidate.Set,
		View:            selectedView.Get(),
		SetView:         selectedView.Set,
		RemainingBudget: remainingBudget.Get(),
		// The admin's configured rates (§13: estimates are "an editable price
		// table multiplied by recorded token counts"). This was hardcoded nil,
		// so EstimateSampleCostUSD could never match a model and the strip's
		// estimate read "Estimate unavailable" permanently — §12.3 requires
		// the cost to be visible at the moment of spending, and a value that
		// is never available is not that. The settings resource is already
		// fetched on this page for the subscribe-URL panel; this is the same
		// response's provider section.
		Prices:    settingsRes.Get().Value.GetSettings().GetProvider().GetPriceTable(),
		ActionErr: actionErr.Get(),
		OnSample: func() {
			d := draft.Get()
			// D0-10: `connected` is intentionally not checked here anymore —
			// the Sample button is already DisabledIf(!p.Connected) in
			// render_sampler.go, so this is only reachable via a stale
			// click racing a connection drop, and letting the guarded
			// SampleStream call itself fail with wsconn.ErrDisconnected
			// (classified below into sampleErr) is what actually produces
			// a visible refusal instead of a silent no-op.
			if d == nil || d.GetId() == 0 || killedReason != "" || !ValidSampleSize(sampleSize.Get()) {
				return
			}
			gen := cancelGen.Get() + 1
			cancelGen.Set(gen)
			sampling.Set(true)
			sampleErr.Set(nil)
			candidates.Set(nil)
			clearPersistedSampleState() // D2-29: a new sample invalidates any prior snapshot
			slug := d.GetSlug()
			ctxStream, cancel := context.WithCancel(context.Background())
			activeCancel.Set(cancel)
			pump.Run(func(done func()) {
				defer done()
				defer sampling.Set(false)
				// The UNSAVED editor state rides along, so a preview shows
				// what the operator is currently typing rather than what is
				// on file. Without this, trying a prompt meant saving it
				// over the working recipe first (§22 J3), which is not an
				// iteration loop — it is a commitment with a preview
				// attached. See SampleDraft in proto/aff/v1/sample.proto.
				stream, err := deps.Sample.SampleStream(ctxStream, &affv1.SampleServiceSampleStreamRequest{
					FeedId: d.GetId(), SampleSize: sampleSize.Get(), TemperatureOverride: tempOverride.Get(),
					Draft: &affv1.SampleDraft{
						SystemPrompt: d.GetSpec().GetSystemPromptTemplate(),
						UserPrompt:   d.GetSpec().GetUserPromptTemplate(),
						Model:        d.GetSpec().GetModel(),
						Effort:       effort.Get(),
					},
				})
				if err != nil {
					if cancelGen.Get() == gen {
						sampleErr.Set(err)
					}
					return
				}
				var acc []*affv1.SampleCandidate
				sid := ""
				for {
					msg, rerr := stream.Recv()
					if cancelGen.Get() != gen {
						return // superseded by a newer sample or a cancel
					}
					if rerr != nil {
						break
					}
					if msg.GetSampleId() != "" {
						sid = msg.GetSampleId()
						sampleID.Set(sid)
					}
					if c := msg.GetCandidate(); c != nil {
						acc = append(acc, c)
						candidates.Set(append([]*affv1.SampleCandidate(nil), acc...))
					}
					if msg.GetDone() {
						if msg.GetErrorKind() != affv1.ErrorKind_ERROR_KIND_UNSPECIFIED {
							sampleErr.Set(errSample{kind: msg.GetErrorKind(), message: msg.GetErrorMessage()})
						}
						break
					}
				}
				// D2-29: only a completed sample (a real sample ID plus at
				// least one candidate) is worth surviving a refresh — a
				// cancelled/errored/empty stream leaves nothing behind, so
				// a refresh after a failed attempt doesn't resurrect it.
				if sid != "" && len(acc) > 0 {
					savePersistedSampleState(PersistedSampleState{
						SavedAtUnix:       time.Now().Unix(),
						FeedSlug:          slug,
						SampleID:          sid,
						Candidates:        acc,
						SampleSize:        sampleSize.Get(),
						TempOverride:      tempOverride.Get(),
						SelectedCandidate: selectedCandidate.Get(),
						SelectedView:      selectedView.Get(),
					})
				}
			})
		},
		OnCancel: func() {
			cancelGen.Set(cancelGen.Get() + 1)
			if fn := activeCancel.Get(); fn != nil {
				fn()
			}
			sampling.Set(false)
		},
		OnPromote: func(candidateID string) {
			// No optimistic local update happens here (Promote never wrote
			// local state before this task and still does not), so there is
			// nothing to roll back on failure — only a visible error to
			// surface where D0-10 previously found none at all.
			if deps.Item == nil || sampleID.Get() == "" {
				return
			}
			id := sampleID.Get()
			actionErr.Set(nil)
			pump.Run(func(done func()) {
				defer done()
				_, err := deps.Item.PromoteSample(context.Background(), &affv1.ItemServicePromoteSampleRequest{
					SampleId: id, CandidateId: candidateID,
				})
				if err != nil {
					actionErr.Set(err)
					return
				}
				// Success has to CHANGE something. Promote used to leave the
				// pane exactly as it found it: the candidate stayed listed
				// and still promotable, so the only way to tell a promote had
				// worked was to go and look at /history — and clicking twice
				// created the item twice. Discard already clears this state
				// on success; promotion is the more consequential of the two
				// and cleared nothing.
				candidates.Set(nil)
				sampleID.Set("")
				selectedCandidate.Set(0)
				clearPersistedSampleState()
			})
		},
		OnDiscard: func() {
			if sampleID.Get() == "" {
				return
			}
			id := sampleID.Get()
			// D0-10's rollback rule: this clears candidates/sampleID
			// optimistically for a snappy UI, but that view update implies
			// the discard reached the server. If the guarded call comes
			// back non-OK (disconnected refused it outright, or the server
			// rejected it, or anything else), ShouldRollbackOptimisticState
			// says to undo it — restoring the prior candidates/sampleID so
			// the operator never sees "discarded" for a discard that never
			// left the browser.
			prevCandidates := candidates.Get()
			prevSampleID := id
			prevSlug := draft.Get().GetSlug()
			actionErr.Set(nil)
			candidates.Set(nil)
			sampleID.Set("")
			clearPersistedSampleState() // D2-29: discarded samples don't come back on refresh
			pump.Run(func(done func()) {
				defer done()
				_, err := deps.Sample.DiscardSample(context.Background(), &affv1.SampleServiceDiscardSampleRequest{SampleId: id})
				outcome := ClassifyMutationError(err, shell.ErrDisconnected)
				if ShouldRollbackOptimisticState(outcome) {
					candidates.Set(prevCandidates)
					sampleID.Set(prevSampleID)
					actionErr.Set(err)
					// The discard never actually reached the server, so the
					// snapshot D2-29 relies on for "survives a refresh" has
					// to come back too, not just the in-memory view.
					savePersistedSampleState(PersistedSampleState{
						SavedAtUnix:       time.Now().Unix(),
						FeedSlug:          prevSlug,
						SampleID:          prevSampleID,
						Candidates:        prevCandidates,
						SampleSize:        sampleSize.Get(),
						TempOverride:      tempOverride.Get(),
						SelectedCandidate: selectedCandidate.Get(),
						SelectedView:      selectedView.Get(),
					})
				}
			})
		},
	}

	dspec := draft.Get().GetSpec()
	// The same mutation path the recipe form uses, so a field edited on the
	// strip and one edited in the collapsed form land in the same draft.
	onFieldChange := editorProps.OnFieldChange
	strip := renderStrip(stripProps{
		Feeds:             feedsRes.Get().Value,
		Selected:          selectedSlug.Get(),
		OnSelect:          railProps.OnSelect,
		OnNew:             railProps.OnNew,
		Model:             dspec.GetModel(),
		Models:            modelsRes.Get().Value.GetModels(),
		ModelsUnavailable: modelsRes.Get().Value.GetUnavailable(),
		ModelsReason:      modelsRes.Get().Value.GetUnavailableReason(),
		OnModel: func(v string) {
			onFieldChange(func(f *affv1.Feed) { ensureSpec(f).Model = v })
		},
		Effort:   effort.Get(),
		OnEffort: func(v string) { effort.Set(v) },
		Size:     sampleSize.Get(),
		OnSize:   func(n int32) { sampleSize.Set(n) },
		Temp:     tempOverride.Get(),
		OnTemp:   func(f float64) { tempOverride.Set(f) },
		// Feed CRUD, on the strip where it can be seen.
		Dirty:        DraftDirty(loadedSnapshot.Get(), draft.Get()),
		Saving:       saving.Get(),
		OnSave:       editorProps.OnSave,
		OnDeleteFeed: func() { railProps.OnDelete(editorProps.Loaded) },
		OnRunNow:     func() { railProps.OnRunNow(editorProps.Loaded) },
		FeedEnabled:  editorProps.Loaded.GetEnabled(),
		OnToggleEnabled: func() {
			railProps.OnToggleEnabled(editorProps.Loaded)
		},
		MenuOpen:   stripMenu.Get(),
		OnMenuOpen: stripMenu.Set,
		Estimate:   previewEstimate(samplerProps),
		Disabled:   !samplerProps.Connected || draft.Get().GetId() == 0 || samplerProps.DisabledReason != "",
		Reason:     samplerProps.DisabledReason,
		Sampling:   sampling.Get(),
		OnPreview: func() {
			if samplerProps.OnSample != nil {
				samplerProps.OnSample()
			}
		},
	})

	prompts := h.Fragment(
		// There is no rail to select from any more — the feed picker is the
		// first control on the strip — so this says where to look now.
		h.Show(draft.Get() == nil,
			h.P(h.ClassStr("af-gen__empty"), h.Text(deps.I18n.T("generate.workbench.noFeed")))),
		renderPromptField(promptFieldProps{
			ID: "gen-system-prompt", LabelKey: "generate.editor.systemPrompt",
			HintKey: "generate.workbench.systemHint",
			Value:   dspec.GetSystemPromptTemplate(),
			OnChange: func(v string) {
				onFieldChange(func(f *affv1.Feed) { ensureSpec(f).SystemPromptTemplate = v })
			},
		}),
		renderPromptField(promptFieldProps{
			ID: "gen-user-prompt", LabelKey: "generate.editor.userPrompt",
			HintKey: "generate.workbench.userHint",
			Value:   dspec.GetUserPromptTemplate(),
			OnChange: func(v string) {
				onFieldChange(func(f *affv1.Feed) { ensureSpec(f).UserPromptTemplate = v })
			},
			Chips: true,
		}),
	)

	// The typed-confirmation modal for feed deletion. Built unconditionally
	// (web/ui.Confirm renders nothing when Open is false) so its hooks keep a
	// fixed slot in this fiber.
	deleteConfirm := wui.Confirm(wui.ConfirmProps{
		T: wui.T(deps.I18n.T), ID: "generate-feed-delete-confirm",
		TitleKey:   "generate.rail.delete.title",
		MessageKey: "generate.rail.delete.message",
		MessageArgs: []any{
			pendingDelete.Get().GetTitle(),
			pendingDelete.Get().GetSlug(),
		},
		// The slug, not the word DELETE. Typing a feed's own slug proves the
		// operator knows WHICH feed this is — the failure mode with a fixed
		// word is deleting the right-looking wrong row.
		RequiredPhrase: pendingDelete.Get().GetSlug(),
		Typed:          deleteTyped.Get(),
		OnTypedChange:  deleteTyped.Set,
		Open:           pendingDelete.Get() != nil,
		Busy:           deleting.Get(),
		OnCancel: func() {
			pendingDelete.Set(nil)
			deleteTyped.Set("")
		},
		OnConfirm: func() {
			f := pendingDelete.Get()
			if f == nil || deleting.Get() {
				return
			}
			deleting.Set(true)
			railErr.Set(nil)
			pump.Run(func(done func()) {
				defer done()
				_, err := deps.Feed.Delete(context.Background(), &affv1.FeedServiceDeleteRequest{
					FeedId: f.GetId(),
					// Version-checked: the server refuses a delete aimed at a
					// version this page has not seen, so a feed edited in
					// another tab is never removed on the strength of a stale
					// view of it.
					ExpectedVersion: f.GetVersion(),
				})
				deleting.Set(false)
				if err != nil {
					railErr.Set(err)
					return
				}
				pendingDelete.Set(nil)
				deleteTyped.Set("")
				// Leave the editor if it was showing the feed that just went.
				if selectedSlug.Get() == f.GetSlug() {
					selectedSlug.Set("")
					draft.Set(nil)
					loadedSnapshot.Set(nil)
					candidates.Set(nil)
					sampleID.Set("")
				}
				feedsRes.Reload()
			})
		},
	})

	return renderWorkbench(workbenchProps{
		Strip: strip,
		Stakes: renderStakes(stakesProps{
			Feed:    editorProps.Loaded,
			Enabled: editorProps.Loaded.GetEnabled(),
		}),
		Prompts: prompts,
		Preview: ui.CreateElement(renderSampler, samplerProps),
		// The feed list is its own disclosure, not a stowaway inside "Recipe
		// settings". It is the CRUD surface — every feed with its status,
		// stale flag, last build, 7-day spend, the enable toggle, Run Now and
		// (now) delete — and burying it under a heading about slugs and cron
		// expressions is why this app looked like it had no feed management
		// at all. Open by default when no feed is selected, because then
		// choosing one is the only thing to do.
		Feeds: ui.CreateElement(renderRail, railProps),
		// Open by default, always. Collapsing it once a feed was selected is
		// what made the app look like it had no feed management: the list,
		// the enable toggles and Run Now all vanished the moment you started
		// working. The operator can still collapse it; it just does not
		// collapse itself.
		FeedsOpen: true,
		FeedCount: len(feedsRes.Get().Value),
		// A brand-new draft has nothing to preview and several required
		// fields in the recipe form, so that is where the operator needs to
		// be looking.
		RecipeOpen:  creatingNew.Get(),
		FeedConfirm: deleteConfirm,
		Recipe: h.Fragment(
			ui.CreateElement(renderEditor, editorProps),
			ui.CreateElement(renderURLPanel, urlPanelProps{
				BaseURL: settingsRes.Get().Value.GetSettings().GetPublishing().GetPublicBaseUrl(),
				// The SAVED slug, never the draft's: a subscribe URL for a
				// slug that has not been saved is a URL that 404s.
				Slug: savedSlug(editorProps.Loaded),
			}),
		),
	})
}

// renderNotWired is shown instead of a blank page or a nil-pointer panic
// when Init has not been called yet — see doc.go's "wiring seam" section
// and deps.go's wired(). It deliberately uses DefaultTranslator directly
// (not deps.I18n, which may itself be nil at this point) since this is
// the one render path that must work with zero configuration.
func renderNotWired() ui.Node {
	return h.Div(
		h.ClassStr("af-generate af-generate--not-wired"),
		h.P(h.Text(DefaultTranslator.T("generate.notWired"))),
	)
}

// activeCancel holds the current sample stream's cancel func outside
// component state, since ui.State is for values GWC diffs/re-renders on,
// not for holding a func the render body never needs to read — a second
// ui.UseRef in Render would work too, but this keeps OnSample/OnCancel's
// closures simpler and there is only ever one in-flight sample stream at a
// time by construction (a new OnSample bumps cancelGen, which makes any
// still-running Recv loop from a previous stream stop touching state).
var activeCancel = newCancelHolder()

type cancelHolder struct{ fn func() }

func newCancelHolder() *cancelHolder  { return &cancelHolder{} }
func (c *cancelHolder) Get() func()   { return c.fn }
func (c *cancelHolder) Set(fn func()) { c.fn = fn }

// autoDisableThreshold mirrors PLAN.md §14.3's "auto-disabled after N
// consecutive failures" but that N is a Settings field
// (SystemService.GetSettings, not modeled in this page's Deps yet since
// Settings belongs to /settings — TODOS.md D4, out of this page's scope).
// A conservative built-in default keeps SampleDisabledReason meaningful
// without this page reaching into Settings itself.
const autoDisableThreshold = 5
