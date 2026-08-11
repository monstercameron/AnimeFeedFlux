package i18n

import (
	"os"
	"strings"

	"github.com/monstercameron/AnimeFeedFlux/internal/i18nlint"
	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
)

// This file wires internal/i18nlint's Pseudolocalize into an actual,
// usable pseudolocale mode for TODOS.md D6-24 ("a build or runtime mode
// that renders every string lengthened and bracketed, so truncation and
// clipped layouts surface now rather than via the first real translator").
//
// Two switches, both read once (at NewBundle() call time, not cached at
// package init, so a test can toggle the env var mid-run):
//
//   - Build mode: `go build -ldflags "-X
//     github.com/monstercameron/AnimeFeedFlux/web/i18n.pseudolocaleFlag=1"
//     ./web` bakes the switch into the binary — no environment needed at
//     run time, which matters for a GOOS=js/wasm build where the browser
//     supplies no process environment unless wasm_exec.js's `go.env` is
//     populated for it.
//   - Runtime mode: `AFI18N_PSEUDOLOCALE=1` read via os.Getenv, for a host
//     build/dev-server run or a test (see pseudo_test.go) where re-linking
//     for one flag is more friction than it's worth.
//
// cmd/affi18n's `pseudo-catalog` subcommand drives PseudoCatalog directly
// against the REAL enCatalog (not ad hoc strings) as the build's
// verification step: every message wave D6-11..14 added gets pushed
// through Pseudolocalize and printed, so a placeholder-mangling bug or a
// panic on real catalogue content is caught by running the tool against
// what's actually shipped, not by trusting the pseudo package's own unit
// tests to have picked representative inputs.
const pseudolocaleEnv = "AFI18N_PSEUDOLOCALE"

// pseudolocaleFlag is the -ldflags -X build-time override described above.
// Empty (the default) defers entirely to the environment variable.
var pseudolocaleFlag string

// PseudolocaleEnabled reports whether NewBundle should register the
// pseudolocalized catalogue instead of the literal one — true if either
// the build-time flag or the AFI18N_PSEUDOLOCALE environment variable is
// set to a truthy value.
func PseudolocaleEnabled() bool {
	return truthy(pseudolocaleFlag) || truthy(os.Getenv(pseudolocaleEnv))
}

// truthy treats "", "0", "false", "off", "no" (case-insensitively, with
// surrounding whitespace trimmed) as unset — everything else, including a
// bare "1" or "true" or "yes", as set. Matches the loose convention this
// repo's other env-gated tests/tools use (see feedback-never-run-paid-
// tests-in-verification's env-var gating pattern).
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// PseudoCatalog returns a copy of enCatalog (the SAME merged-with-common
// catalogue NewBundle registers under normal conditions — see catalog.go's
// mergeCommonIntoNamespace) with every Message's user-visible text run
// through i18nlint.Pseudolocalize. Exported so cmd/affi18n's
// pseudo-catalog subcommand can render the real catalogue's pseudolocale
// form for inspection/CI without duplicating enCatalog's assembly.
func PseudoCatalog() gwci18n.Catalog {
	return pseudolocalizeCatalog(enCatalog)
}

func pseudolocalizeCatalog(c gwci18n.Catalog) gwci18n.Catalog {
	out := make(gwci18n.Catalog, len(c))
	for ns, msgs := range c {
		nsOut := make(gwci18n.NamespaceCatalog, len(msgs))
		for key, msg := range msgs {
			nsOut[key] = pseudolocalizeMessage(msg)
		}
		out[ns] = nsOut
	}
	return out
}

// pseudolocalizeMessage widens/brackets every text field a Message can
// carry (Text, every Plural category, every Select case, Default) and
// leaves everything else (PluralArg, SelectArg — argument NAMES, not
// user-visible text) untouched. i18nlint.Pseudolocalize already preserves
// %-verb and {brace} interpolation placeholders byte-for-byte within each
// field, so a Message with {arg1}/{count}-style placeholders round-trips
// with its placeholders intact.
func pseudolocalizeMessage(m gwci18n.Message) gwci18n.Message {
	out := m
	if m.Text != "" {
		out.Text = i18nlint.Pseudolocalize(m.Text)
	}
	if m.Plural != nil {
		out.Plural = make(map[gwci18n.PluralCategory]string, len(m.Plural))
		for cat, text := range m.Plural {
			out.Plural[cat] = i18nlint.Pseudolocalize(text)
		}
	}
	if m.Select != nil {
		out.Select = make(map[string]string, len(m.Select))
		for sel, text := range m.Select {
			out.Select[sel] = i18nlint.Pseudolocalize(text)
		}
	}
	if m.Default != "" {
		out.Default = i18nlint.Pseudolocalize(m.Default)
	}
	return out
}
