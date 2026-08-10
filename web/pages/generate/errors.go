//go:build js && wasm

package generatepage

import (
	"errors"
	"fmt"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// errNotWired is returned by UseResource loaders when Init has not been
// called yet (see deps.go) — surfaced through the normal ListError state
// rather than a panic, so an incremental build-out where the page is
// routed but not yet wired still renders something legible.
var errNotWired = errors.New("generate page: control-plane clients not initialized (see generatepage.Init)")

// errSample wraps a terminal SampleStream error (PLAN.md §8's ErrorKind
// taxonomy, carried on SampleServiceSampleStreamResponse) so the sampler
// pane can show the taxonomy's kind, not just a bare message — "the API
// failed" is exactly what PLAN.md §8 says is not actionable at 4am, and
// that reasoning applies just as much to what the operator sees on
// screen as to a log line.
type errSample struct {
	kind    affv1.ErrorKind
	message string
}

func (e errSample) Error() string {
	return fmt.Sprintf("%s: %s", e.kind.String(), e.message)
}
