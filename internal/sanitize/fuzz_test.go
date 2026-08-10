package sanitize

import (
	"strings"
	"testing"
)

// FuzzHTMLNeverEmitsDisallowed asserts the allowlist property holds for
// arbitrary input: no matter what garbage goes in, the output never
// contains a disallowed tag, a disallowed attribute, or an unsafe href
// scheme.
//
// This is deliberately a STRUCTURAL property, not a substring check over
// the raw output bytes. An earlier version of this fuzz target asserted
// the output never contains "<script", "javascript:", " on<letter>=" etc.
// ANYWHERE in the string, and that is the wrong property: HTML() keeps
// text content, and plain text is allowed to contain those characters —
// "javascript:" as a book title fragment, "onA=" in prose about variable
// assignment, none of that is markup and none of it is dangerous, because
// it is never parsed as an href or an attribute. Substring-matching over
// the whole output cannot distinguish "these bytes are inert text" from
// "these bytes are a live attribute", so it either false-positives on
// legitimate content or (worse) invites "fixing" the sanitizer by mangling
// text it should have left alone. Do not revert to substring matching over
// the raw output for this reason — reparse the output's markup instead,
// the same way any real consumer (a feed reader) would.
func FuzzHTMLNeverEmitsDisallowed(f *testing.F) {
	seeds := []string{
		`<script>alert(1)</script>`,
		`<SCRIPT>alert(1)</SCRIPT>`,
		`<ScRiPt src="//evil.com/x.js"></script>`,
		`<div onerror="alert(1)">hi</div>`,
		`<img onerror="alert(1)" src="x">`,
		`<a href="javascript:alert(1)">click</a>`,
		`<a href="JaVaScRiPt:alert(1)">click</a>`,
		`<a href="data:text/html,<script>alert(1)</script>">click</a>`,
		`<a href="vbscript:msgbox(1)">t</a>`,
		`<iframe src="https://evil.example/"></iframe>`,
		`<IFRAME src="https://evil.example/"></IFRAME>`,
		`<style>body{background:url(javascript:alert(1))}</style>`,
		`<STYLE>body{background:url(javascript:alert(1))}</STYLE>`,
		`<p style="color:red">hi</p>`,
		`<!-- <script>alert(1)</script> -->`,
		`<a href="http://example.com'>click</a>`,
		`< script>alert(1)</script>`,
		`<p>hello <b`,
		`<svg onload="alert(1)">`,
		`<a href onmouseover="alert(1)">t</a>`,
		`<p>こんにちは 😀 世界</p>`,
		``,
		`<`,
		`<>`,
		`</p>`,
		`<p><div>inner</div></p>`,
		`<img src="x"/>`,
		`<a href="/path">t</a>`,
		`<a href="//evil.com">t</a>`,
		"<a href=\"java\tscript:alert(1)\">t</a>",
		`<body onload="alert(1)"><script>alert(2)</script></body>`,
		// Plain text that must survive untouched — these are the cases
		// the old, over-strict substring property broke.
		` onA=`,
		`JAVASCRiPt:`,
		`read "JavaScript: The Good Parts"`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, in string) {
		out := HTML(in)

		// 1 & 4: reparse the sanitizer's own output the same way a real
		// consumer would, and assert every tag name found is allowlisted.
		// This subsumes "no <script/<iframe/<style tag opening survives"
		// far more robustly than a naive Contains over the whole string,
		// because it only judges bytes that actually parse as a tag.
		for _, tok := range tokenize(out) {
			switch tok.kind {
			case tokenStartTag, tokenEndTag, tokenSelfClose:
				if !allowedTags[tok.name] {
					t.Fatalf("disallowed tag %q survived\ninput: %q\noutput: %q", tok.name, in, out)
				}
				if tok.kind == tokenEndTag {
					continue
				}
				// 2: the only attributes ever allowed to survive are
				// href on <a> (attacker-controlled, scheme-checked
				// below), plus rel/target on <a>, which the sanitizer
				// itself adds as fixed constants — never derived from
				// input — so they carry no attacker-controlled data.
				for key, val := range tok.attrs {
					switch {
					case tok.name == "a" && key == "href":
						// checked in step 3 below
					case tok.name == "a" && key == "rel" && val == "nofollow noopener":
					case tok.name == "a" && key == "target" && val == "_blank":
					default:
						t.Fatalf("disallowed attribute %q=%q on <%s> survived\ninput: %q\noutput: %q",
							key, val, tok.name, in, out)
					}
				}
				// 3: any surviving href must be http or https.
				if tok.name == "a" {
					if href, ok := tok.attrs["href"]; ok {
						lower := strings.ToLower(href)
						if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
							t.Fatalf("unsafe href scheme survived: %q\ninput: %q\noutput: %q", href, in, out)
						}
					}
				}
			}
		}

		// Idempotence: a sanitizer that changes its mind on a second pass
		// is a real bug — it means the output of a "safe" pass is not
		// itself stable input, which callers rendering already-sanitized
		// content a second time would rely on.
		if again := HTML(out); again != out {
			t.Fatalf("HTML is not idempotent\ninput:  %q\noutput: %q\nagain:  %q", in, out, again)
		}
	})
}
