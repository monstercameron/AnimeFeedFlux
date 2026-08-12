package rpc

import (
	"strings"
	"unicode"
)

// itemsearch.go turns what an operator typed into an FTS5 query.
//
// # Why the raw string was not good enough
//
// ItemService.List used to pass the search box's contents straight to
// `items_fts MATCH ?`, which has two consequences an operator experiences as
// the search being broken.
//
// **Typing a prefix finds nothing.** FTS5 matches whole tokens, so "triv"
// does not match "trivia" — the operator has to finish the word before
// anything appears, in a field that searches as they type. That was the
// reported complaint, and it is the more annoying of the two because the
// search looks like it is working: it returns zero results confidently.
//
// **FTS5 syntax leaks.** The query string is a language, not a phrase: `AND`,
// `OR`, `NOT`, `NEAR`, `*`, `^`, `:`, `-` and `"` all mean something. An
// apostrophe in "Fire Force's" or an unbalanced quote is a syntax ERROR, so
// the RPC fails and the UI shows a failure for what the operator experienced
// as ordinary typing. Meanwhile a title containing the word "and" cannot be
// searched for literally.
//
// # What this does instead
//
// Every run of letters and digits becomes one quoted prefix term, and the
// terms are ANDed by FTS5's default. `triv ques` becomes `"triv"* "ques"*`,
// which finds "Trivia question". Anything that is not a letter or a digit is a
// separator, so no operator character can reach the query language: the search
// box searches text, and only text.
//
// The cost of this is deliberate and worth stating: an operator cannot type
// FTS5 operators to build a precise query. Nobody using this box wants to —
// it sits above a list of at most a few thousand items and is used to find one
// of them by name.

// itemFTSQuery builds the MATCH expression for a search box's contents.
//
// It returns "" when there is nothing searchable (empty input, or input made
// entirely of punctuation). Callers must treat that as "no search filter"
// rather than as a query that matches nothing — a box containing only "..."
// has not asked a question.
func itemFTSQuery(raw string) string {
	terms := itemSearchTerms(raw)
	if len(terms) == 0 {
		return ""
	}
	var b strings.Builder
	for i, term := range terms {
		if i > 0 {
			b.WriteByte(' ')
		}
		// Quoted, so the term is a literal string rather than a keyword, then
		// starred for prefix matching. FTS5 spells a prefix query "abc"* —
		// the star must sit outside the quotes.
		b.WriteByte('"')
		b.WriteString(term)
		b.WriteString(`"*`)
	}
	return b.String()
}

// itemSearchTerms splits input into searchable terms.
//
// A term is a run of letters or digits. Everything else separates, including
// the apostrophe: FTS5's default (unicode61) tokenizer would split "Force's"
// into "force" and "s" anyway, so treating the apostrophe as a separator here
// produces the same tokens the index holds, rather than a term that can never
// match.
//
// itemSearchMaxTerms bounds the result. A pasted paragraph would otherwise
// become a hundred-term AND query — slow, and certain to match nothing, which
// is a worse answer than searching on its first few words.
func itemSearchTerms(raw string) []string {
	var (
		terms []string
		cur   strings.Builder
	)
	flush := func() {
		if cur.Len() > 0 {
			terms = append(terms, cur.String())
			cur.Reset()
		}
	}
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
			continue
		}
		flush()
		if len(terms) >= itemSearchMaxTerms {
			return terms
		}
	}
	flush()
	if len(terms) > itemSearchMaxTerms {
		terms = terms[:itemSearchMaxTerms]
	}
	return terms
}

// itemSearchMaxTerms caps how many words one search considers.
const itemSearchMaxTerms = 12
