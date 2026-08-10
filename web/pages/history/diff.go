package history

import "strings"

// DiffOp classifies one line of a computed diff.
type DiffOp int

const (
	DiffEqual DiffOp = iota
	DiffDelete
	DiffInsert
)

// DiffLine is one line of a computed diff, tagged with how it differs
// between the "before" and "after" text.
type DiffLine struct {
	Op   DiffOp
	Text string
}

// diffLineCap bounds the LCS table this package builds (O(n*m) cells).
// Item title/summary/link fields are always small; body_html is the only
// field that could be large. Past this many lines, DiffLines falls back
// to a two-line whole-field replace instead of doing a very large LCS —
// still correct (nothing here is used for the actual write, only for
// display), just coarser.
const diffLineCap = 4000

// DiffLines computes a minimal line-level diff between before and after
// using a classic LCS backtrack. It is pure and deterministic so the
// revision diff view (PLAN.md §12.4: "revision history with a diff view")
// is unit-testable without rendering anything.
func DiffLines(before, after string) []DiffLine {
	a := splitLines(before)
	b := splitLines(after)

	if len(a) > diffLineCap || len(b) > diffLineCap {
		var out []DiffLine
		if before != after {
			out = append(out, DiffLine{Op: DiffDelete, Text: before})
			out = append(out, DiffLine{Op: DiffInsert, Text: after})
			return out
		}
		return []DiffLine{{Op: DiffEqual, Text: before}}
	}

	// lcs[i][j] = length of the LCS of a[i:], b[j:]
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var out []DiffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{Op: DiffEqual, Text: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Op: DiffDelete, Text: a[i]})
			i++
		default:
			out = append(out, DiffLine{Op: DiffInsert, Text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{Op: DiffDelete, Text: a[i]})
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{Op: DiffInsert, Text: b[j]})
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// FieldDiff names one edited field alongside its computed diff.
type FieldDiff struct {
	Field string
	Lines []DiffLine
}

// Changed reports whether the diff contains any non-equal line — used to
// skip rendering fields that did not actually change between two
// snapshots.
func (f FieldDiff) Changed() bool {
	for _, l := range f.Lines {
		if l.Op != DiffEqual {
			return true
		}
	}
	return false
}
