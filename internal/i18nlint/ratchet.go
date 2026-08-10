package i18nlint

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Ratchet implements TODOS.md D6-21: the count of flagged literals in web/
// starts at zero and may never rise. It follows the reasoning and
// conventions of scripts/coverage-ratchet.sh — read that script's header
// before changing this function, so the two ratchets in this repo don't
// contradict each other.
//
// Differences from the coverage ratchet, both deliberate:
//
//   - Coverage is a percentage with test-ordering noise, so that script
//     allows a small TOLERANCE band below the baseline. A literal count is
//     an exact integer with no such noise — one un-caught hardcoded string
//     is exactly one too many — so this ratchet allows no tolerance: count
//     must be <= baseline, full stop.
//   - "Improvement" points opposite ways: less coverage is bad, more
//     coverage is good; fewer literals is good, more is bad. A drop below
//     baseline is therefore the good outcome here, not the one to guard
//     against — but exactly like the coverage script, Ratchet never
//     rewrites baselinePath itself, in either direction. Lowering the
//     floor after a real cleanup is a deliberate, reviewable, human edit,
//     made in its own commit; Ratchet only decides pass/fail and lets the
//     caller (cmd/affi18n) report the numbers so a human can act on them.
//   - A missing baseline file is an error, not an implicit zero: the file
//     must exist and be seeded (with "0", since D6-21 fixes the starting
//     value) in a reviewable commit before the gate can run — silently
//     treating "missing" as "zero" would let the first CI run on a fresh
//     checkout invent its own floor.
func Ratchet(count int, baselinePath string) error {
	if count < 0 {
		return fmt.Errorf("i18nlint: negative literal count %d", count)
	}

	data, err := os.ReadFile(baselinePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf(
				"i18nlint: no baseline at %s — seed it by committing a file containing \"0\" "+
					"(D6-21 starts the ratchet at zero)", baselinePath)
		}
		return fmt.Errorf("i18nlint: reading baseline %s: %w", baselinePath, err)
	}

	raw := strings.TrimSpace(string(data))
	baseline, err := strconv.Atoi(raw)
	if err != nil || baseline < 0 {
		return fmt.Errorf("i18nlint: corrupt baseline at %s: %q is not a non-negative integer", baselinePath, raw)
	}

	if count > baseline {
		return fmt.Errorf(
			"i18nlint: literal count is %d, above the %d baseline in %s — the ratchet may not rise (D6-21)",
			count, baseline, baselinePath)
	}

	return nil
}
