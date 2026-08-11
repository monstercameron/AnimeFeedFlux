package main

import (
	"fmt"
	"os"
	"sort"

	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
)

// runPseudoCatalog implements `affi18n pseudo-catalog` (TODOS.md D6-24):
// runs the REAL, shipped "en" catalogue (afi18n.PseudoCatalog — the same
// merged-with-common catalogue NewBundle registers, see web/i18n/catalog.
// go's mergeCommonIntoNamespace) through i18nlint.Pseudolocalize and
// prints every entry as "namespace.key = pseudolocalized text".
//
// This is the pseudolocale "build" as a checkable artifact: `affi18n
// pseudo` (pseudo.go) only ever exercised ad hoc CLI strings or stdin, so
// a placeholder-mangling regression in a real catalogue entry (a
// {{double-brace}} template, a bare "%" from a literal percent sign, a
// multi-arg printf verb) could ship uncaught. Running this against the
// actual catalogue instead means every string wave D6-11..14 added gets
// pushed through the transform, so truncation-prone widened text and
// placeholder handling are checked against what the app really renders,
// not a hand-picked sample.
//
// Exits 1 if any entry's "%" or "{" count changes across
// pseudolocalization — the exact false-failure-producing bug
// internal/i18nlint/pseudo.go's doc comment warns a pseudolocalizer must
// never introduce ("a pseudolocalizer that mangles a placeholder produces
// a false test failure on every run that touches it, which is how a
// pseudolocale build gets disabled") — so a regression here fails the
// same way `affi18n lint`/`check`/`ratchet` do: loud, on stdout, non-zero
// exit.
func runPseudoCatalog(stdout, stderr *os.File) int {
	real := afi18n.Catalog()
	pseudo := afi18n.PseudoCatalog()

	type row struct{ ns, key, text string }
	var rows []row
	problems := 0
	for ns, msgs := range pseudo {
		for key, msg := range msgs {
			text := primaryText(msg)
			rows = append(rows, row{ns, key, text})

			origMsg := real[ns][key]
			origText := primaryText(origMsg)
			if countByte(origText, '%') != countByte(text, '%') || countByte(origText, '{') != countByte(text, '{') {
				fmt.Fprintf(stdout, "%s.%s: placeholder mismatch after pseudolocalization (original %q, pseudo %q)\n", ns, key, origText, text)
				problems++
			}
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ns != rows[j].ns {
			return rows[i].ns < rows[j].ns
		}
		return rows[i].key < rows[j].key
	})
	for _, r := range rows {
		fmt.Fprintf(stdout, "%s.%s = %s\n", r.ns, r.key, r.text)
	}

	if problems > 0 {
		fmt.Fprintf(stderr, "affi18n pseudo-catalog: %d entries with placeholder mismatches\n", problems)
		return 1
	}
	fmt.Fprintf(stdout, "affi18n pseudo-catalog: %d entries, 0 placeholder mismatches\n", len(rows))
	return 0
}

// primaryText picks one representative string off a Message to run the
// placeholder-count check against: Text if set, else the first Plural
// category found, else the first Select case, else Default. Iteration
// order over the Plural/Select maps is nondeterministic, but every
// category/case of a well-formed Message shares the same placeholder set
// (they're the same sentence in different plural/select forms), so which
// one gets picked doesn't change whether the check is meaningful.
func primaryText(msg gwci18n.Message) string {
	if msg.Text != "" {
		return msg.Text
	}
	for _, v := range msg.Plural {
		return v
	}
	for _, v := range msg.Select {
		return v
	}
	return msg.Default
}

func countByte(s string, b byte) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			n++
		}
	}
	return n
}
