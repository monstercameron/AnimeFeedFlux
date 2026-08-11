// Package brand holds the product's icon artwork and is the single place it
// is stored.
//
// The files live here rather than in web/static, which is where the admin
// shell's index.html lives and where they were first put, because they have
// two consumers with incompatible needs. The publish plane (PLAN.md §6)
// serves /favicon.ico to the open internet and must not depend on a
// directory being present on disk next to the binary: it runs from
// distroless/static with a read-only root filesystem (§15.4), and a missing
// icon file must not be able to turn into a request-path error. Embedding
// makes the icon part of the binary, so "the file is not there" stops being
// a state the server can be in. web/build.sh copies the same files out of
// this package into the admin serve directory, so the two surfaces cannot
// drift: there is one copy on disk, not two that must be kept in step.
//
// The artwork itself is raster, deliberately. See web/shell/header.go's
// markSrc doc comment for why there is no SVG here and why the wordmark is
// rendered as text rather than shipped as an image.
package brand

import (
	_ "embed"
)

// FaviconICO is the multi-resolution (16/32/48) icon served at
// /favicon.ico. That exact path is requested unprompted by browsers,
// crawlers and Slack's unfurler regardless of what <link rel="icon"> a
// document declares, which is why it exists as a distinct asset rather than
// relying on the PNGs the admin shell links.
//
//go:embed favicon.ico
var FaviconICO []byte

// OGDefault is the 1200x630 Open Graph image used as the per-feed default
// (PLAN.md §5.5: "without these the unfurl is a bare URL").
//
// It is the only asset here that is NOT transparent, and that is the point:
// an unfurl card is composited on whatever background the client chooses,
// and Slack's dark theme would swallow a transparent lockup whose wordmark
// is dark navy. This one carries its own dark ground.
//
//go:embed og-default.png
var OGDefault []byte
