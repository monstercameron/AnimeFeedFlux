package rpc

import "testing"

func TestItemFTSQuery(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// The reported bug: a prefix found nothing, because FTS5 matches
			// whole tokens unless asked for a prefix.
			name: "prefix matches the rest of the word",
			in:   "triv",
			want: `"triv"*`,
		},
		{
			name: "every word is a prefix, ANDed",
			in:   "triv ques",
			want: `"triv"* "ques"*`,
		},
		{
			// FTS5 keywords must not reach the query language, or a title
			// containing the word "and" becomes unsearchable.
			name: "operators are searched for, not obeyed",
			in:   "fire AND force",
			want: `"fire"* "AND"* "force"*`,
		},
		{
			// An unbalanced quote used to be a syntax error, so the RPC
			// failed and the operator saw a broken page for ordinary typing.
			name: "quotes cannot break the query",
			in:   `he said "hello`,
			want: `"he"* "said"* "hello"*`,
		},
		{
			name: "punctuation separates",
			in:   "spy x family: mission",
			want: `"spy"* "x"* "family"* "mission"*`,
		},
		{
			// unicode61 splits on the apostrophe when indexing, so splitting
			// here too is what makes the term match what is in the index.
			name: "apostrophes split the way the index splits them",
			in:   "force's",
			want: `"force"* "s"*`,
		},
		{
			name: "digits are searchable",
			in:   "trivia 20",
			want: `"trivia"* "20"*`,
		},
		{
			name: "non-latin scripts survive",
			in:   "進撃",
			want: `"進撃"*`,
		},
		{
			// "" means no filter, NOT a query that matches nothing: a box
			// holding only punctuation has not asked a question.
			name: "nothing searchable yields no query",
			in:   "  ... -- ",
			want: "",
		},
		{name: "empty", in: "", want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := itemFTSQuery(c.in); got != c.want {
				t.Errorf("itemFTSQuery(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestItemFTSQueryCapsTermCount keeps a pasted paragraph from becoming a
// hundred-term AND that is both slow and certain to match nothing.
func TestItemFTSQueryCapsTermCount(t *testing.T) {
	in := ""
	for i := 0; i < 40; i++ {
		in += "word "
	}
	got := itemSearchTerms(in)
	if len(got) != itemSearchMaxTerms {
		t.Errorf("term count = %d, want the cap of %d", len(got), itemSearchMaxTerms)
	}
}
