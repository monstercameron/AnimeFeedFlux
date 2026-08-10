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

	"github.com/monstercameron/GoWebComponents/v5/fetch"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/state"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

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
	statsRes := fetch.UseResource(func(ctx context.Context) (*affv1.SystemServiceStatsResponse, error) {
		if !wired() || deps.System == nil {
			return nil, errNotWired
		}
		return deps.System.Stats(ctx, &affv1.SystemServiceStatsRequest{})
	})

	// --- Editor state ----------------------------------------------------
	selectedSlug := ui.UseState("")
	draft := ui.UseState((*affv1.Feed)(nil))
	loadedSnapshot := ui.UseState((*affv1.Feed)(nil))
	fieldErrs := ui.UseState(FieldErrors(nil))
	saving := ui.UseState(false)
	saveErr := ui.UseState(error(nil))
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
				return
			}
			draft.Set(cloneFeed(resp.GetFeed()))
			loadedSnapshot.Set(cloneFeed(resp.GetFeed()))
			fieldErrs.Set(nil)
			saveErr.Set(nil)
			conflictTheirs.Set(nil)
			candidates.Set(nil)
			sampleID.Set("")
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
		Selected:  selectedSlug.Get(),
		OnSelect: func(slug string) {
			selectedSlug.Set(slug)
			creatingNew.Set(false)
		},
		OnNew: func() {
			selectedSlug.Set("")
			creatingNew.Set(true)
			draft.Set(&affv1.Feed{Spec: &affv1.FeedSpec{ItemsPerRun: 1, FeedWindow: 50}})
			loadedSnapshot.Set(nil)
			fieldErrs.Set(nil)
		},
		OnToggleEnabled: func(f *affv1.Feed) {
			if !connected || f == nil {
				return
			}
			go func() {
				_, err := deps.Feed.SetEnabled(context.Background(), &affv1.FeedServiceSetEnabledRequest{
					FeedId: f.GetId(), Enabled: !f.GetEnabled(), ExpectedVersion: f.GetVersion(),
				})
				if err == nil {
					feedsRes.Reload()
				}
			}()
		},
		OnRunNow: func(f *affv1.Feed) {
			if !connected || f == nil {
				return
			}
			go func() {
				deps.Feed.RunNow(context.Background(), &affv1.FeedServiceRunNowRequest{FeedId: f.GetId()})
				feedsRes.Reload()
			}()
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
			if d == nil || !connected {
				return
			}
			go func() {
				resp, err := deps.Feed.ValidateSpec(context.Background(), &affv1.FeedServiceValidateSpecRequest{
					Kind: d.GetKind(), Slug: d.GetSlug(), Spec: d.GetSpec(),
				})
				if err != nil {
					return
				}
				fieldErrs.Set(MapFieldErrors(resp.GetErrors()))
			}()
		},
		OnSave: func() {
			d := draft.Get()
			if d == nil || !connected {
				return
			}
			saving.Set(true)
			saveErr.Set(nil)
			go func() {
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
			}()
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
		Prices:          nil,
		OnSample: func() {
			d := draft.Get()
			if d == nil || d.GetId() == 0 || !connected || killedReason != "" || !ValidSampleSize(sampleSize.Get()) {
				return
			}
			gen := cancelGen.Get() + 1
			cancelGen.Set(gen)
			sampling.Set(true)
			sampleErr.Set(nil)
			candidates.Set(nil)
			ctxStream, cancel := context.WithCancel(context.Background())
			activeCancel.Set(cancel)
			go func() {
				defer sampling.Set(false)
				stream, err := deps.Sample.SampleStream(ctxStream, &affv1.SampleServiceSampleStreamRequest{
					FeedId: d.GetId(), SampleSize: sampleSize.Get(), TemperatureOverride: tempOverride.Get(),
				})
				if err != nil {
					if cancelGen.Get() == gen {
						sampleErr.Set(err)
					}
					return
				}
				var acc []*affv1.SampleCandidate
				for {
					msg, rerr := stream.Recv()
					if cancelGen.Get() != gen {
						return // superseded by a newer sample or a cancel
					}
					if rerr != nil {
						break
					}
					if msg.GetSampleId() != "" {
						sampleID.Set(msg.GetSampleId())
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
			}()
		},
		OnCancel: func() {
			cancelGen.Set(cancelGen.Get() + 1)
			if fn := activeCancel.Get(); fn != nil {
				fn()
			}
			sampling.Set(false)
		},
		OnPromote: func(candidateID string) {
			if deps.Item == nil || sampleID.Get() == "" {
				return
			}
			go func() {
				deps.Item.PromoteSample(context.Background(), &affv1.ItemServicePromoteSampleRequest{
					SampleId: sampleID.Get(), CandidateId: candidateID,
				})
			}()
		},
		OnDiscard: func() {
			if sampleID.Get() == "" {
				return
			}
			id := sampleID.Get()
			go func() {
				deps.Sample.DiscardSample(context.Background(), &affv1.SampleServiceDiscardSampleRequest{SampleId: id})
			}()
			candidates.Set(nil)
			sampleID.Set("")
		},
	}

	return h.Div(
		h.ClassStr("af-generate"),
		ui.CreateElement(renderRail, railProps),
		ui.CreateElement(renderEditor, editorProps),
		ui.CreateElement(renderSampler, samplerProps),
	)
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
