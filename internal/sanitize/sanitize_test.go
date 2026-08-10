package sanitize

import "testing"

func TestHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "script tag with content is fully discarded",
			in:   `<script>alert(1)</script>`,
			want: ``,
		},
		{
			name: "onerror attribute stripped, disallowed tag unwrapped keeping text",
			in:   `<div onerror="alert(1)">hi</div>`,
			want: `hi`,
		},
		{
			name: "javascript href rejected, anchor kept without href",
			in:   `<a href="javascript:alert(1)">click</a>`,
			want: `<a>click</a>`,
		},
		{
			name: "data href rejected",
			in:   `<a href="data:text/html,<script>alert(1)</script>">click</a>`,
			want: `<a>click</a>`,
		},
		{
			name: "uppercase SCRIPT tag treated same as lowercase",
			in:   `<SCRIPT>alert(1)</SCRIPT>`,
			want: ``,
		},
		{
			name: "malformed unclosed tag treated as literal, escaped text",
			in:   `<p>hello <b`,
			want: `<p>hello &lt;b</p>`,
		},
		{
			name: "nested allowed tags preserved",
			in:   `<p>Hello <strong>world</strong>!</p>`,
			want: `<p>Hello <strong>world</strong>!</p>`,
		},
		{
			name: "iframe fully dropped, no text content to preserve",
			in:   `<iframe src="https://evil.example/"></iframe>`,
			want: ``,
		},
		{
			name: "img with onerror dropped entirely (void element, no text)",
			in:   `<img onerror="alert(1)" src="x">`,
			want: ``,
		},
		{
			name: "style attribute dropped, tag and text kept",
			in:   `<p style="color:red">hi</p>`,
			want: `<p>hi</p>`,
		},
		{
			name: "html comment removed entirely",
			in:   `<!-- comment --><p>text</p>`,
			want: `<p>text</p>`,
		},
		{
			name: "mismatched quote in attribute causes tag to be treated as literal, escaped text",
			in:   `<a href="http://example.com'>click</a>`,
			want: `&lt;a href="http://example.com'>click&lt;/a>`,
		},
		{
			name: "whitespace before tag name still recognized as a tag",
			in:   `< p>hi</p>`,
			want: `<p>hi</p>`,
		},
		{
			name: "CJK and emoji text passes through unchanged",
			in:   `<p>こんにちは 😀 世界</p>`,
			want: `<p>こんにちは 😀 世界</p>`,
		},
		{
			name: "empty input",
			in:   ``,
			want: ``,
		},
		{
			name: "class attribute dropped on allowed tag",
			in:   `<p class="x">hi</p>`,
			want: `<p>hi</p>`,
		},
		{
			name: "lists preserved",
			in:   `<ul><li>one</li><li>two</li></ul>`,
			want: `<ul><li>one</li><li>two</li></ul>`,
		},
		{
			name: "blockquote preserved",
			in:   `<blockquote>quote</blockquote>`,
			want: `<blockquote>quote</blockquote>`,
		},
		{
			name: "code preserved",
			in:   `<code>x=1</code>`,
			want: `<code>x=1</code>`,
		},
		{
			name: "br normalized to self-closed form",
			in:   `<br>`,
			want: `<br />`,
		},
		{
			name: "br already self-closed",
			in:   `<br/>`,
			want: `<br />`,
		},
		{
			name: "https link with query and fragment kept, rel/target added",
			in:   `<a href="https://example.com/x?y=1#z">t</a>`,
			want: `<a href="https://example.com/x?y=1#z" rel="nofollow noopener" target="_blank">t</a>`,
		},
		{
			name: "relative url rejected",
			in:   `<a href="/path">t</a>`,
			want: `<a>t</a>`,
		},
		{
			name: "scheme-relative url rejected",
			in:   `<a href="//evil.com">t</a>`,
			want: `<a>t</a>`,
		},
		{
			name: "vbscript href rejected",
			in:   `<a href="vbscript:msgbox(1)">t</a>`,
			want: `<a>t</a>`,
		},
		{
			name: "disallowed tag nested in allowed tag unwrapped",
			in:   `<p><div>inner</div></p>`,
			want: `<p>inner</p>`,
		},
		{
			name: "self-closing disallowed img dropped",
			in:   `<img src="x"/>`,
			want: ``,
		},
		{
			name: "bare href attribute with no value rejected",
			in:   `<a href>t</a>`,
			want: `<a>t</a>`,
		},
		{
			name: "html entities in text left untouched",
			in:   `<p>Tom &amp; Jerry</p>`,
			want: `<p>Tom &amp; Jerry</p>`,
		},
		{
			name: "uppercase EM tag lowercased",
			in:   `<EM>hi</EM>`,
			want: `<em>hi</em>`,
		},
		{
			name: "uppercase HREF attribute name recognized",
			in:   `<a HREF="https://x.com">t</a>`,
			want: `<a href="https://x.com" rel="nofollow noopener" target="_blank">t</a>`,
		},
		{
			name: "tab-obfuscated javascript scheme rejected",
			in:   "<a href=\"java\tscript:alert(1)\">t</a>",
			want: `<a>t</a>`,
		},
		{
			name: "style tag with content fully discarded",
			in:   `<style>body{background:url(javascript:alert(1))}</style><p>ok</p>`,
			want: `<p>ok</p>`,
		},
		{
			name: "unclosed allowed tag auto-closed at end of input",
			in:   `<p>no closing tag`,
			want: `<p>no closing tag</p>`,
		},
		{
			name: "stray unmatched end tag ignored",
			in:   `hello</p>world`,
			want: `helloworld`,
		},
		{
			name: "multiple links get independent nofollow/target",
			in:   `<a href="https://a.com">a</a> and <a href="https://b.com">b</a>`,
			want: `<a href="https://a.com" rel="nofollow noopener" target="_blank">a</a> and <a href="https://b.com" rel="nofollow noopener" target="_blank">b</a>`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := HTML(c.in)
			if got != c.want {
				t.Errorf("HTML(%q) =\n  %q\nwant\n  %q", c.in, got, c.want)
			}
		})
	}
}
