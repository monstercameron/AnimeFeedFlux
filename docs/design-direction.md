# AnimeFeedFlux — visual direction

The one document every styling change follows. If a change cannot be justified
from this page, it does not belong.

## The subject, named

An admin tool for a machine that publishes on a schedule. One user. The moment
that matters is 2am, when a feed has gone quiet and they need to know why. The
interface's single job: **make the state of a generative pipeline legible, and
make spending money deliberate.**

## The anchor: the animation timesheet

Not anime characters — the production artifact. A genga/douga sheet is a ruled
grid where every row is one frame, numbered, in strict sequence, never out of
order.

That is exactly what a feed is, and it is the invariant the entire codebase is
built on (§5.5: strictly increasing, unique, never backdated — the rule whose
violation Slack punishes by silently dropping an item forever).

So the structural devices here **encode something true**: numbered rows because
the sequence genuinely carries information; ruled columns because the data is
tabular and time-ordered; a gutter because a frame belongs to a strip.

This is the discipline that keeps it from being generic. Numbering a set of
unordered cards would be decoration. Numbering a feed is a fact.

## Palette

From the object itself. Japanese timesheets print on pale green-grey stock,
ruled in red, marked up in blue pencil.

| Token | Light | Dark | Role |
|---|---|---|---|
| `paper`   | `#E8EDE6` | `#12171A` | page ground (stock / light-table) |
| `surface` | `#F3F6F1` | `#1A2126` | raised panels |
| `ink`     | `#1A1F1C` | `#E6ECE5` | primary text |
| `muted`   | `#5C6B5E` | `#93A396` | secondary text |
| `rule`    | `#757E73` | `#6B8679` | printed rule lines, borders |
| `redline` | `#B43E2A` | `#E2604A` | column rule, destructive, brand |
| `pencil`  | `#2E6E8E` | `#5FA8CE` | accent, links, focus (the blue pencil) |
| `phosphor`| `#805D07` | `#E3A857` | live / running / attention |

**These are the corrected values.** Four earlier swatches — chosen by eye from the
timesheet stock and never measured — failed the contrast floor and were replaced
against the pairings they actually have to survive:

| Token | Was | Now | Failing pairing → fixed |
|---|---|---|---|
| `rule` light    | `#A8B5A5` | `#757E73` | 1.80:1 on paper → 3.55:1 (border floor 3:1) |
| `rule` dark     | `#33403A` | `#6B8679` | 1.29:1 → 3.64:1 |
| `redline` light | `#C8452F` | `#B43E2A` | paper text on it 4.07:1 → 4.83:1 |
| `phosphor` light| `#B8860B` | `#805D07` | paper text on it 2.74:1 → 5.07:1 |

A pale rule looks right on a photograph of a real timesheet and is invisible on a
screen; the anchor supplies the intent, not the measured value. Do not "restore"
the prettier originals.

The **brand mark keeps `#C8452F`**. Its 4.07:1 against the paper-coloured arcs is
below the 4.5:1 text floor but clears the 3:1 floor that non-text graphics are
held to, and a mark whose colour shifts to satisfy a text rule stops being a mark.
`redline` as a *UI role* is `#B43E2A`; the two are deliberately not the same value.

Deliberately not the cream+serif+terracotta, near-black+acid-green, or
newspaper-broadsheet palettes that show up regardless of subject. Every value
above is traceable to the anchor.

Dark mode is not an inversion — it is the **light table**: the same sheet, lit
from beneath.

## Type

No webfonts. The CSP blocks external hosts and the bundle is already 31 MB, so
personality comes from treatment, not from a downloaded face.

- Display and body: the system sans stack, set tight (`-0.01em`) at headings.
- Micro-labels: uppercase, `0.08em` tracking, `muted`. These are the timesheet's
  printed column headers.
- **All numerals tabular** (`font-variant-numeric: tabular-nums`). Non-negotiable:
  every column of counts, costs, tokens and timestamps must align vertically.
  This single rule does more for the "instrument" feel than any typeface would.
- Monospace only for genuine identifiers: slugs, guids, item keys, cron.

## Layout

- **Rules, not cards.** Separate with hairlines in `rule`; reserve filled
  surfaces for genuinely raised things (modals, menus).
- **Radius 3px**, uniformly. Enough to not be brutalist, too little to be a SaaS
  card deck.
- A numbered gutter on sequences: runs, items, revisions.
- The `redline` is a vertical column divider, used sparingly — it is the loudest
  thing on the page and should mark exactly one boundary per view.

## Signature: the tape

The one memorable element. A horizontal strip on each feed showing its recent
publish history as ticks along time — one tick per item, positioned by
`published_at`.

It earns its place because it makes the product's core invariant visible at a
glance: ticks march forward and never collide. A gap is a missed run. A cluster
is a backfill. A feed that stopped is instantly obvious in a way a
"last built 4 hours ago" label never is.

Spend the boldness here. Everything around it stays quiet.

## Floor, not negotiable

Responsive to 320px; visible keyboard focus everywhere (2px `pencil` ring, 3:1
against every surface it lands on); `prefers-reduced-motion` respected at the
token layer; contrast tested in both themes.

## Brand assets

**Replaced 2026-08-10.** The two hand-drawn SVGs this section used to describe —
a timesheet cel with a sprocket gutter and the RSS arcs struck from its origin
corner, plus a lockup with a red column rule — no longer exist. The mark is now
the kitsune crest: a fox's head over three broadcast arcs, inside a hex shield,
with circuit tendrils leaving its right edge. Everything below describes what
actually ships.

- `internal/brand/favicon-{32,180,512}.png`, `internal/brand/favicon.ico` — the
  crest, transparent, cropped to the shield. **Raster, not vector, and that is
  not a shortcut**: the artwork is a render with gradients and interior shading,
  and a hand-traced SVG would be a different mark wearing the same name.
- `internal/brand/og-default.png` — 1200×630, for Slack unfurls (§5.5). The one
  asset that is *not* transparent: an unfurl card is composited on whoever's
  theme, and a dark-navy wordmark on transparency vanishes in Slack's dark
  theme. It carries its own ground.
- **There is no lockup asset.** In the source artwork the wordmark is dark navy
  sitting inside its own bright glow, so it cannot be keyed out (loosen the key
  and the mark ships inside a blue cloud twice its size; tighten it and the text
  dissolves first) and, being dark navy, it would be invisible in dark mode
  anyway. The wordmark is therefore rendered as HTML text next to the crest —
  crisp at every size, and it follows `color` into dark mode. The mark keeps its
  own colour; a brand mark that changes hue is not a brand mark.
- The glow is **not** baked into any asset. It is a `drop-shadow` filter reading
  the accent token, so it follows the theme instead of being one fixed colour
  and radius on every surface.

## Palette, corrected 2026-08-10

Two changes came with the new mark, both in `web/tokens/theme.go`:

- `accent` moved off the muted teal-blue `#2E6E8E` onto the crest's own electric
  blue — `#2A5FD8` light, `#5B8CFF` dark — at the same contrast headroom the old
  value had. The mark is the loudest blue on any screen it appears on, and an
  accent a few degrees off it reads as a mismatch rather than as a second
  colour.
- `theme-color` in `web/static/index.html` was carrying the **danger** swatch
  (`#C8452F`/`#E2604A`), painting the browser's own chrome in the colour
  reserved for destructive states. It now tracks `accent`.

`danger` stays the redline. It is the one loud boundary per view, and that is
still the right job for it.

## The signature: transmission arcs

The auth screen (`web/pages/auth/styles.go`) is backed by a field of hairline
concentric rings struck from the centre of the crest, restating the mark's own
broadcast arcs at page scale. It says the product's thesis in one device — a
signal leaves from here, on a schedule, and other software picks it up — on the
one screen whose whole job is to state identity.

Not animated, deliberately. A slow pulse was the obvious next move; it is the
accessory to leave off, on a screen an operator passes through twice a day.

## Appearance control

Dark mode is switched in the header, not in Settings, and the reason is
structural rather than aesthetic: `/settings` is behind the session, and the
login screen is where an operator working at night first meets this
application. Three explicit states — `Match system` (default), `Light`, `Dark`
— because a two-state toggle has to guess an initial value and silently stops
tracking the OS for everyone. See `web/shell/theme.go`.
