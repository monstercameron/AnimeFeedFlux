//go:build js && wasm

package auth

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// brand.go gives /login and /recover the one thing the design brief calls
// out as unique to them: "the logo lockup belongs here at real size, not
// shrunk into a corner." web/shell's header (out of this task's allowed
// paths) already renders the same mark at nav-bar scale (132x24) on every
// authed route — that is correct for a persistent nav, but wrong for the
// one screen whose entire job is to state the product's identity before
// anything else happens. This file renders the identical mark, at roughly
// 2x that size, ABOVE the auth card rather than inside the header's small
// top-left corner.
//
// Until 2026-08-10 this file carried a hand-drawn SVG of the previous mark,
// duplicated by hand from web/shell/header.go with a comment admitting the
// two had to be kept in sync manually. Both are now an <img> against the
// same asset (see header.go's markSrc for why the brand cannot be an inline
// vector), so there is no second copy to drift: the file on disk is the
// single definition, and neither package needs RawHTMLUnsafe any more.
const authMarkSrc = "/favicon-512.png"

// renderAuthBrand is the real-size lockup both LoginPage and RecoverPage
// mount as the first child of .af-auth-page, ahead of the page's own <h1>.
//
// The lockup is composed here rather than shipped as one image, for the
// reason header.go's markSrc doc comment gives: the artwork's wordmark is
// dark navy inside its own glow, so it neither extracts cleanly nor
// survives dark mode. The crest is the image; the wordmark is text. The
// accessible name lives on the wrapper (role="img" + aria-label) with the
// <img> itself decorative and the wordmark aria-hidden, so a screen reader
// announces "AnimeFeedFlux" once rather than three times.
func renderAuthBrand() ui.Node {
	return h.Div(
		h.ClassStr("af-auth-brand"),
		h.Role("img"),
		h.Aria("label", "AnimeFeedFlux"),
		h.Img(h.ClassStr("af-auth-brand__mark"), h.Src(authMarkSrc), h.Alt(""), h.Aria("hidden", "true")),
		h.Span(
			h.ClassStr("af-auth-brand__wordmark"),
			h.Aria("hidden", "true"),
			// Brand exemption — see web/shell/header.go's renderHeaderBrand
			// for the reasoning; the product name is a proper noun that is
			// the same in every locale.
			h.Span(h.ClassStr("af-auth-brand__wordmark-strong"), h.Text("Anime")), //nolint:i18n -- brand name, not translatable copy
			h.Span(h.ClassStr("af-auth-brand__wordmark-light"), h.Text("Feed")),   //nolint:i18n -- brand name, not translatable copy
			h.Span(h.ClassStr("af-auth-brand__wordmark-strong"), h.Text("Flux")),  //nolint:i18n -- brand name, not translatable copy
		),
	)
}
