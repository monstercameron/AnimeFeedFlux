// The prompt-template machinery lives in internal/prompttmpl — a
// stdlib-only LEAF package — and this file is the aliases that keep every
// existing caller of the generate-package names compiling unchanged.
//
// Why it moved (2026-08-15): internal/feedspec validates recipe templates
// (ValidateTemplate), and feedspec is compiled into the BROWSER for the
// /generate editor's client-side validation. Importing package generate for
// one pure function linked this ENTIRE package — the runner, and through it
// internal/llm, SchemaFlux and go-openai — into the WASM bundle, where
// check-wasm-secrets correctly failed the build on go-openai's `authToken`
// field name. The template code itself has no such dependencies, so it is
// the piece that moves; the pipeline stays here.
package generate

import "github.com/monstercameron/AnimeFeedFlux/internal/prompttmpl"

// See internal/prompttmpl for all documentation; these are pure aliases.
type (
	SourceArticle = prompttmpl.SourceArticle
	Data          = prompttmpl.Data
)

var (
	Render           = prompttmpl.Render
	ValidateTemplate = prompttmpl.ValidateTemplate
	Hash             = prompttmpl.Hash
	Season           = prompttmpl.Season
)
