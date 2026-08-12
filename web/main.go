//go:build js && wasm

// Command web is the AnimeFeedFlux admin WASM entrypoint (TODOS.md D0-01):
// mounts the shell (routing, auth guard, DISCONNECTED banner) into the
// page's #app element and blocks forever via utils.WaitForever, matching
// the documented GWC entrypoint pattern (see web/shell's package doc
// comment for why this is built on GoWebComponents v5, not the earlier
// hand-rolled syscall/js version).
//
// # This file is the client-side composition root
//
// Mirroring what cmd/animefeedflux/wire.go is for the server: web/shell.
// Mount owns dialing the control-plane connection and mounting the
// router; wirePages (this file) is the one place that reads the dialed
// *wsconn.Conn and the shell's one i18n Bundle and hands each of the four
// page packages exactly the interfaces it declared it needed in its own
// Deps/Init — auth.Init, generatepage.Init, historypage.Init,
// settings.Init. Each page package deliberately declared its own minimal
// structural interfaces rather than importing each other (or web/wsconn,
// or web/i18n) directly; this file satisfies those interfaces from the
// outside without editing any of them.
//
// Before this file existed, four page packages were fully built and unit
// tested but never registered with web/shell's router at all (auth,
// history) or registered with an init() that self-registers a page body
// which then never had Init(Deps) called on it (generate, settings) — so
// every route rendered the shell's placeholder regardless of how complete
// the underlying page was. Blank-importing the four page packages below
// is what makes their own init()-time shell.RegisterPage calls run at
// all; wirePages is what makes the resulting page bodies do more than
// show their own "not wired" state.
package main

import (
	"context"
	"strconv"
	"time"

	// The IANA timezone database, compiled INTO the bundle.
	//
	// time.LoadLocation reads /usr/share/zoneinfo, and a browser has no
	// filesystem — so every LoadLocation in web/ (i18n/adapter.go's
	// FormatDateTime, and both page-level Formatters) fails there and falls
	// back to UTC. The effect is quiet and wrong in the worst way: a feed
	// scheduled "7am America/New_York" renders its times as UTC while the
	// label beside them still says New York, so the operator reads a
	// five-hour lie with no indication anything failed.
	//
	// It does not reproduce under `go test` — node HAS a filesystem, so the
	// wasm test runner resolves zones fine and the browser is the only place
	// this breaks.
	//
	// Costs roughly 450KB uncompressed in a bundle that is already 34MB raw
	// (7MB gzipped, A8-49 tracks shrinking it), which is noise against being
	// correct about the thing this entire application schedules on.
	_ "time/tzdata"

	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/utils"

	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
	"github.com/monstercameron/AnimeFeedFlux/web/wsconn"

	authpage "github.com/monstercameron/AnimeFeedFlux/web/pages/auth"
	generatepage "github.com/monstercameron/AnimeFeedFlux/web/pages/generate"
	historypage "github.com/monstercameron/AnimeFeedFlux/web/pages/history"
	settingspage "github.com/monstercameron/AnimeFeedFlux/web/pages/settings"
)

func main() {
	// shell.Mount dials the control plane, resolves the initial ANON/AUTH
	// state, calls wirePages with the result (before anything renders —
	// see Mount's own doc comment on why the ordering matters), then
	// registers the five routes and mounts the router.
	shell.Mount(context.Background(), "#app", wirePages)

	// Block forever: returning from main tears down the Go runtime and
	// the whole app goes dead, same as every wasm_exec.js program.
	utils.WaitForever()
}

// wirePages is the composition root's one job: turn a dialed *wsconn.Conn
// (or nil, if the initial dial failed outright — see wsconn.Connect's
// error path in web/shell/app.go's Mount) plus the shell's one i18n
// Bundle (shell.Bundle) into each page package's own Deps/Init call.
//
// conn == nil means the control plane could not be reached AT ALL at
// boot. There is no live client to hand any page in that case — per this
// task's brief ("do not fabricate a Deps value to make it compile"),
// wirePages simply returns without calling any page's Init, leaving every
// page in its own honestly-labeled not-wired state (renderNotWired in
// generate/settings/history, the DISCONNECTED-reusing fallback in auth)
// rather than a Deps built from nil clients that would panic the first
// time a page actually tried to call one.
func wirePages(conn *wsconn.Conn) {
	if conn == nil {
		return
	}

	bundle := shell.Bundle()

	// Everything goes over the WebSocket, login included: §3's transport
	// rule is one bridge, not one bridge plus an HTTP side door.
	authpage.Init(conn.Auth, conn.Auth)

	generatepage.Init(generatepage.Deps{
		Feed:       conn.Feed,
		Sample:     conn.Sample,
		Run:        conn.Run,
		System:     conn.System,
		Item:       conn.Item,
		I18n:       bundleTranslator{bundle: bundle, ns: afi18n.NSGenerate},
		Formatters: bundleFormatters{},
	})

	historypage.Init(historypage.Deps{
		Runs:  conn.Run,
		Items: conn.Item,
		Feeds: conn.Feed,
		T:     bundleCatalog{bundle: bundle},
	})

	settingspage.Init(settingspage.Deps{
		Auth:       conn.Auth,
		System:     conn.System,
		Feed:       conn.Feed,
		I18n:       bundleTranslator{bundle: bundle, ns: afi18n.NSSettings},
		Formatters: bundleFormatters{},
	})
}

// --- Adapters from the shell's one real gwci18n.Bundle to each page's
// own declared Translator/Formatters/Catalog interface ------------------
//
// generatepage.Translator, settings.Translator, and history.Catalog are
// three separately-declared interfaces (each page package deliberately
// does not import web/i18n — see each package's doc.go), but all three
// are satisfied the same way: look the key up in the one real Bundle
// web/shell already built and gwci18n.Provider already mounted (shell.
// Bundle()), rather than each page's own zero-configuration fallback
// (fallbackTranslator/fallbackCatalog), which stays reachable only for
// as long as wirePages hasn't run yet (before Mount's wire callback
// fires) or conn is nil.

// bundleTranslator satisfies generatepage.Translator and
// settings.Translator (both `T(key string, args ...any) string`) against
// one namespace of the real Bundle. Positional args are converted to
// gwci18n.Arguments using the same "arg1", "arg2", ... (1-indexed)
// convention web/i18n.NewLabelResolver already uses for web/ui's callers
// (see that function's doc comment) and web/i18n/keys_common.go documents
// for every common.* key that takes an argument — generate.*/settings.*
// keys follow the identical convention (confirmed by reading how
// deps.I18n.T is called at each site, e.g. settings/render.go's
// t("common.state.errorDetail", err.Error())).
type bundleTranslator struct {
	bundle *gwci18n.Bundle
	ns     string
}

// T resolves against afi18n.CurrentLocale() — read on every call, never
// captured in the struct — with DefaultLocale as the fallback. This is the
// single seam that makes the language selector work at all: wirePages runs
// once at boot and hands each page a translator it keeps forever, so a
// locale captured here would be the language the app started in, for the
// life of the tab. Falling back to DefaultLocale means a key missing from a
// non-English catalogue renders its English text rather than its own name.
func (t bundleTranslator) T(key string, args ...any) string {
	return t.bundle.Translate(afi18n.CurrentLocale(), t.ns, key, positionalArguments(args), afi18n.DefaultLocale)
}

// positionalArguments mirrors web/i18n.NewLabelResolver's own unexported
// helper of the same shape (that package cannot export it without also
// exporting gwci18n.Arguments' construction rules to callers who don't
// need them) — duplicated here rather than imported since it is three
// lines of pure conversion, not shared state.
func positionalArguments(args []any) gwci18n.Arguments {
	if len(args) == 0 {
		return nil
	}
	out := make(gwci18n.Arguments, len(args))
	for i, a := range args {
		out["arg"+strconv.Itoa(i+1)] = a
	}
	return out
}

// bundleFormatters satisfies both generatepage.Formatters (DateTime/
// Currency/RelativeTime) and settings.Formatters (those three plus
// ByteSize) — a Go interface only needs the methods it declares to be
// satisfied, so one implementing type covers both narrower and wider
// callers. Every method delegates to web/i18n's already-locale-aware
// FormatDateTime/FormatCurrencyUSD/FormatRelativeTime/FormatByteSize
// (adapter.go), never fmt.Sprintf, matching PLAN.md §12.6.
type bundleFormatters struct{}

func (bundleFormatters) DateTime(t time.Time, tz string) string {
	return afi18n.FormatDateTime(t, tz)
}

func (bundleFormatters) Currency(usd float64) string {
	return afi18n.FormatCurrencyUSD(usd)
}

func (bundleFormatters) RelativeTime(t, now time.Time) string {
	return afi18n.FormatRelativeTime(t, now)
}

// Number renders a count with locale grouping — token counts on this page run
// to four and five digits, and "1761 tokens" is harder to read at a glance
// than "1,761".
func (bundleFormatters) Number(v int64) string {
	return afi18n.FormatCount(v)
}

// Percent renders novelty similarity. Two digits, because the difference
// between 87% and 87.34% is never the difference between accepting a
// candidate and rejecting it.
func (bundleFormatters) Percent(fraction float64) string {
	return afi18n.FormatPercent(fraction, 0)
}

func (bundleFormatters) ByteSize(n int64) string {
	return afi18n.FormatByteSize(n)
}

// bundleCatalog satisfies history.Catalog (`T(key string, args
// map[string]any) string`) against the real Bundle's NSHistory namespace.
// No positional-argument bridging needed here — history.Catalog already
// takes named arguments directly, so this converts straight to
// gwci18n.Arguments (a plain map[string]any under the hood).
type bundleCatalog struct {
	bundle *gwci18n.Bundle
}

func (c bundleCatalog) T(key string, args map[string]any) string {
	// Same read-at-call-time rule as bundleTranslator.T above.
	return c.bundle.Translate(afi18n.CurrentLocale(), afi18n.NSHistory, key, gwci18n.Arguments(args), afi18n.DefaultLocale)
}
