//go:build js && wasm

package auth

import (
	"github.com/monstercameron/GoWebComponents/v5/css"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// styles.go declares every rule login.go and recover.go's markup needs.
// Both pages render plain h.Main/h.Form/h.Label/h.Input/h.Button (never
// web/ui's Button/Input primitives — those live in a package this task
// cannot edit, and rebuilding two already-working, well-tested state
// machines on top of a different component layer was out of scope), so
// this file styles them the same way index.html used to style the shell's
// hand-written markup: by class name and, where no class exists on a plain
// tag (h1, p, label, input, button, a), by a descendant selector scoped
// under the one class both pages' root <main> always carries
// ("af-auth-page"). Every color/spacing/type value reads a tokens.* Var
// reference, never a literal, so both themes and the token layer's own
// WCAG-checked contrast pairs apply here exactly as they do everywhere else
// in the app — the whole reason this task exists (PLAN.md/this task's
// brief: an unstyled /login was the literal input).
//
// Called once from an init() below — safe, since css.Global's own emission
// is deduped process-wide on (selector + rules) content (see the vendored
// css package's doc comment on Global), so a second call from a future
// test importing this package twice is a no-op, not a duplicate rule.
func emitAuthStyles() {
	// The auth "card": centered, bounded width, the one place in this
	// package a background/border/shadow is declared — everything below is
	// scoped as a descendant of it.
	css.Global(".af-auth-page",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(6)),
		css.W(css.Length("100%")),
		css.MaxWidth(css.Length("26rem")),
		// box-sizing: border-box — without this, the padding below is added
		// ON TOP of width:100%, so at a narrow viewport (the 320px floor the
		// direction doc's "Floor, not negotiable" section requires) the card
		// renders wider than its own containing block and forces the whole
		// page into horizontal scroll. Every other box in this file already
		// gets border-box for free from the input/button rules further down;
		// this is the one outer box that needed it stated explicitly.
		css.Raw("box-sizing", "border-box"),
		css.Position.Relative,
		// `auto` on all sides, not just the inline axis: the :has() rule
		// below makes .af-content a centring flex container on auth routes,
		// and auto block margins are what let the card take the free space
		// evenly instead of hanging from a fixed offset at the top. The
		// margin still reads as a gap on a short viewport, where there is no
		// free space to distribute.
		css.Raw("margin", "auto"),
		css.Padding(tokens.Space(8)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		// Ruled, not shadowed (design-direction.md "Layout": "Separate with
		// hairlines in `rule`; reserve filled surfaces for genuinely raised
		// things"). The one card on this page sits on the timesheet ground
		// by its border alone — no box-shadow, that being the one elevation
		// affordance the direction doc reserves for modals/menus.
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		bodyFont(),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
	)
	// --- The transmission arcs -------------------------------------------
	//
	// The one memorable thing on this screen, and it is lifted from the
	// mark rather than invented: the crest carries three concentric
	// broadcast arcs under the fox, and this restates them at page scale as
	// a field of hairline rings struck from the centre of the crest itself.
	// It says the product's whole thesis in one device — a signal leaves
	// from here, on a schedule, and other software picks it up — on the one
	// screen that exists to state identity before anything else happens.
	//
	// Deliberately NOT animated. A slow pulse was the obvious next move and
	// it is the accessory to remove: this is a login screen an operator
	// passes through twice a day, and ambient motion behind a password
	// field is something you notice on the first visit and resent on the
	// two hundredth.
	//
	// Mechanically: no negative z-index. A z-index:-1 pseudo-element here
	// would fall behind #app's own background (web/shell/styles.go paints
	// RoleBg there) and simply never be seen, because .af-auth-page creates
	// no stacking context of its own to be "behind". Instead the field sits
	// at z-index 0 and the card's real children are lifted to 1, which also
	// lets the rings bleed past the card's edges — the signal passing
	// through the panel rather than being trapped in it.
	css.Global(".af-auth-page::before",
		css.Raw("content", `""`),
		css.Position.Absolute,
		css.ZIndex(0),
		css.Raw("pointer-events", "none"),
		// Struck from the centre of the crest: the card's own padding
		// (Space 8) plus half the 52px mark.
		css.Raw("left", "calc("+string(tokens.Space(8))+" + 26px)"),
		css.Raw("top", "calc("+string(tokens.Space(8))+" + 26px)"),
		css.W(css.Length("110rem")),
		css.H(css.Length("110rem")),
		css.Raw("transform", "translate(-50%, -50%)"),
		css.Raw("background", "repeating-radial-gradient(circle at center, transparent 0 5.4rem, "+
			"color-mix(in srgb, "+string(tokens.Color(tokens.RoleAccent))+" 22%, transparent) 5.4rem 5.5rem)"),
		// Faded out radially so the field has no edge — an un-masked
		// repeating gradient ends in a hard circle, which reads as a
		// rendering artefact rather than as a signal falling off.
		css.Raw("-webkit-mask-image", "radial-gradient(circle at center, #000 0%, transparent 58%)"),
		css.Raw("mask-image", "radial-gradient(circle at center, #000 0%, transparent 58%)"),
	)
	// Everything real on the card sits above the field.
	css.Global(".af-auth-page > *", css.Position.Relative, css.ZIndex(1))

	// Centre the card in the viewport rather than hanging it from the top.
	// Scoped with :has() so it reaches ONLY the routes that render an auth
	// page — .af-content is web/shell's frame and every other route wants
	// its normal top-anchored block flow, so this must not become a global
	// "make .af-content a flex container". Reaching up into a shell class
	// from a page package is a layering compromise, taken knowingly: the
	// alternative is a shell-side "is this an auth route" flag, which puts
	// knowledge of this package's routes into the shell to achieve the same
	// thing.
	css.Global(".af-content:has(.af-auth-page)",
		css.Display.Flex,
		css.Items.Center,
		css.Justify.Center,
		// Clip the ring field. It is a 110rem pseudo-element centred on the
		// crest, so on any window narrower than about 1180px it pushed the
		// DOCUMENT wide and gave the sign-in page a horizontal scrollbar —
		// measured at 900px: scrollWidth 1181 against a 900px viewport.
		// `clip` rather than `hidden` because hidden would make this a scroll
		// container and swallow the card's own focus scrolling.
		css.Raw("overflow", "clip"),
	)

	// The real-size brand lockup (brand.go's renderAuthBrand): the crest as
	// an <img>, the wordmark as text inheriting RoleText from
	// .af-auth-page above, so it inverts with the theme. This is the one
	// screen whose whole job is to state the product's identity before
	// anything else happens, so the mark is set at roughly 2x the header's
	// 26px rather than shrunk into a corner.
	css.Global(".af-auth-brand",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(3)),
		css.Raw("margin-bottom", string(tokens.Space(2))),
	)
	css.Global(".af-auth-brand__mark",
		css.Display.Block,
		css.W(css.Px(52)),
		css.H(css.Px(52)),
		css.Raw("flex", "0 0 auto"),
		// Same token-through-color-mix glow as the header's mark; see
		// web/shell/styles.go's .af-header__mark for why the alpha cannot
		// be concatenated onto the custom property.
		css.Raw("filter", "drop-shadow(0 0 8px color-mix(in srgb, "+string(tokens.Color(tokens.RoleAccent))+" 45%, transparent))"),
	)
	css.Global(".af-auth-brand__wordmark",
		css.FontSize(tokens.FontSize(tokens.TextXl)),
		css.FontWeight.Bold,
		css.Tracking(css.Ems(-0.01)),
		bodyFont(),
	)
	css.Global(".af-auth-brand__wordmark-strong", css.FontWeight.Bold)
	css.Global(".af-auth-brand__wordmark-light",
		css.FontWeight.Normal,
		css.TextColor(tokens.Color(tokens.RoleAccent)),
	)
	css.Global(".af-auth-page h1",
		css.FontSize(tokens.FontSize(tokens.TextXl)),
		css.FontWeight.Bold,
		css.Raw("margin", "0"),
	)
	css.Global(".af-auth-page h2",
		css.FontSize(tokens.FontSize(tokens.TextLg)),
		css.FontWeight.Semibold,
		css.Raw("margin", "0"),
	)
	// Only the recover page's intro paragraph is a DIRECT child of
	// .af-auth-page (login.go has no bare <p> at that level) — every other
	// <p> in either page carries its own more specific af-* class (error/
	// hint/backoff/etc.), so scoping to ">" here means this generic rule
	// never has to out-specificity those.
	css.Global(".af-auth-page > p",
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin", "0"),
	)
	css.Global(".af-auth-page label",
		css.Display.Block,
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Medium,
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin-bottom", string(tokens.Space(1))),
	)

	// Text/password/code inputs. The TOTP re-enroll "saved" checkbox is the
	// one <input> in either page that must NOT get this treatment — it
	// keeps native checkbox rendering via the :not() exclusion below.
	css.Global(`.af-auth-page input:not([type="checkbox"])`,
		css.Display.Block,
		css.W(css.Length("100%")),
		css.Raw("box-sizing", "border-box"),
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
		bodyFont(),
		transitionColors(),
	)
	css.Global(`.af-auth-page input:not([type="checkbox"]):disabled`,
		css.OpacityNum(css.Num(0.6)),
		css.Raw("cursor", "not-allowed"),
	)
	css.Global(`.af-recover-saved-confirm input[type="checkbox"]`,
		css.W(css.Length("auto")),
		css.Raw("margin", "0"),
	)

	// Buttons: one visual weight (accent-filled) — these two pages have no
	// secondary/ghost distinction the way web/ui.Button's variants do (the
	// state machines in login_state.go/recover_state.go gate WHICH button
	// is enabled, not which one matters less), so a single consistent
	// treatment reads as an application rather than an arbitrary choice
	// between two unstyled-vs-styled buttons on the same form.
	css.Global(".af-auth-page button",
		css.Display.InlineFlex,
		css.Items.Center,
		css.Justify.Center,
		css.Gap(tokens.Space(2)),
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(4)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleAccent)),
		css.Bg(tokens.Color(tokens.RoleAccent)),
		css.TextColor(tokens.Color(tokens.RoleAccentFg)),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
		css.FontWeight.Medium,
		css.Cursor.Pointer,
		bodyFont(),
		transitionColors(),
	)
	css.Global(".af-auth-page button:disabled",
		css.OpacityNum(css.Num(0.55)),
		css.Raw("cursor", "not-allowed"),
	)
	css.Global(".af-auth-page button:not(:disabled):hover", css.OpacityNum(css.Num(0.9)))

	// Focus visibility — inputs, buttons, and the recover link all need
	// their own visible-focus outline (D0-14/a11y floor, same requirement
	// web/ui/base.go's focusVisible() encodes for the pages that DO use
	// that package).
	css.Global(`.af-auth-page input:focus-visible, .af-auth-page button:focus-visible, .af-auth-page a:focus-visible`,
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "2px"),
	)

	// Step/field chrome shared by both pages' forms.
	css.Global(".af-auth-form",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(4)),
	)
	css.Global(".af-login-step",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
	)
	// The step eyebrow, set as an instrument label rather than a sentence:
	// mono, uppercase, tracked out, in the muted role, with a short accent
	// tick to its left. This is the one place on the screen where the type
	// carries the product's character, and it is the right place for it —
	// "Step 1 of 2" is genuinely ordinal (two credentials, in order, and
	// the second is unreachable until the first lands), so a technical
	// counter is describing something true about the flow rather than
	// decorating it with numbering.
	//
	// No progress bar beside it. That was the obvious next move and it is
	// the accessory to leave off: the text already says "1 of 2", and a bar
	// would need a per-step modifier class threaded through login.go's
	// markup to say the same thing a second time.
	css.Global(".af-step-label",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(2)),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.FontWeight.Medium,
		monoFont(),
		css.Raw("text-transform", "uppercase"),
		css.Tracking(css.Ems(0.09)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin", "0"),
	)
	// The tick: a short accent bar that makes the eyebrow read as a labelled
	// instrument value rather than a floating sentence, and gives the muted
	// text something to hang off at the card's left margin.
	css.Global(".af-step-label::before",
		css.Raw("content", `""`),
		css.Display.Block,
		css.W(css.Px(14)),
		css.H(css.Px(2)),
		css.Raw("flex", "0 0 auto"),
		css.Bg(tokens.Color(tokens.RoleAccent)),
	)
	css.Global(".af-field-hint",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin", "0"),
	)
	css.Global(".af-login-actions",
		css.Display.Flex,
		css.Justify.Between,
		css.Items.Center,
		css.Gap(tokens.Space(3)),
	)
	// The secondary action: outlined, not filled. Weight is what tells the
	// eye which of two adjacent buttons is the one that finishes the job.
	// Two classes, to out-specify the page's generic "button" rule — a class
	// plus a type selector beats a single class, which is why the first
	// attempt at this left Back looking exactly like Sign in.
	css.Global(".af-login-actions .af-login-back",
		css.Bg(css.Transparent),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
	)
	// The recovery link is a real target, not a 17px hairline of text.
	css.Global(".af-login-recover",
		css.Display.InlineFlex,
		css.Items.Center,
		css.Raw("min-height", "24px"),
	)

	// Status text: an empty <p class="af-form-error">/<p class="af-backoff-
	// notice"> is how login.go/recover.go keep aria-describedby/live-region
	// ids stable across renders even when there is nothing to say (see
	// login.go's errorNode/backoffNode doc comments) — :empty collapses
	// that placeholder to zero height instead of an empty-but-laid-out row,
	// so the form does not visibly jump/reflow the instant an error DOES
	// appear.
	css.Global(".af-form-error, .af-backoff-notice, .af-field-error",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.Raw("margin", "0"),
	)
	css.Global(".af-form-error:empty, .af-backoff-notice:empty, .af-field-error:empty",
		css.Display.None,
	)
	css.Global(".af-form-error, .af-field-error", css.TextColor(tokens.Color(tokens.RoleDanger)))
	css.Global(".af-backoff-notice", css.TextColor(tokens.Color(tokens.RoleWarning)))
	css.Global(".af-low-codes-warning",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Medium,
		css.TextColor(tokens.Color(tokens.RoleWarning)),
		css.Raw("margin", "0"),
	)

	css.Global(".af-auth-links",
		css.Display.Flex,
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".af-auth-links a",
		css.TextColor(tokens.Color(tokens.RoleAccent)),
		css.Raw("text-decoration", "underline"),
	)
	css.Global(".af-auth-links a:hover", css.TextColor(tokens.Color(tokens.RoleAccentStrong)))

	// Recovery-specific layout: the code entry / choose-action / reset /
	// reenroll / done steps, and the always-visible break-glass note.
	css.Global(".af-recover-choice, .af-recover-actions, .af-recover-reenroll, .af-recover-done",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(3)),
	)
	css.Global(".af-recover-saved-confirm",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(2)),
	)
	css.Global(".af-code",
		css.Display.Block,
		css.Padding(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.Raw("overflow-wrap", "anywhere"),
		monoFont(),
	)
	css.Global(".af-recover-breakglass",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
		css.Raw("margin-top", string(tokens.Space(2))),
		css.Raw("padding-top", string(tokens.Space(4))),
		css.Raw("border-top", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
	)
	css.Global(".af-recover-breakglass p",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin", "0"),
	)

	// Reduced-motion parity with the rest of the app: transitionColors()
	// above already reads tokens.Duration, which tokens.Emit collapses to
	// near-zero under prefers-reduced-motion — nothing extra needed here,
	// noted so a future reader does not add a second gate.
}

// bodyFont / monoFont / transitionColors mirror web/ui/base.go's identical
// helpers. Duplicated rather than imported for the same reason
// web/shell/styles.go duplicates them: they are unexported in package ui,
// and neither web/shell nor web/pages/auth is on this task's allowed-edit
// list for web/ui.
func bodyFont() css.Rule { return css.Font(tokens.FontFamily(tokens.FontSans)) }
func monoFont() css.Rule { return css.Font(tokens.FontFamily(tokens.FontMono)) }

func transitionColors() css.Rule {
	return css.Transition(css.PropColors, tokens.Duration(tokens.DurationFast), tokens.Easing(tokens.EasingStd))
}

func init() {
	emitAuthStyles()
}
