package urlnorm

import "testing"

func TestNormalize_TrackingParamsStrippedRealParamsSurvive(t *testing.T) {
	got, err := Normalize("https://example.com/article?id=42&utm_source=twitter&utm_campaign=fall&fbclid=abc123&gclid=xyz&mc_cid=1&mc_eid=2&igshid=3&ref=home&ref_src=twsrc&page=2")
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	want := "https://example.com/article?id=42&page=2"
	if got != want {
		t.Errorf("Normalize() = %q, want %q", got, want)
	}
}

func TestNormalize_DefaultPortsRemoved(t *testing.T) {
	cases := map[string]string{
		"http://example.com:80/path":   "http://example.com/path",
		"https://example.com:443/path": "https://example.com/path",
		"http://example.com:8080/path": "http://example.com:8080/path",
	}
	for in, want := range cases {
		got, err := Normalize(in)
		if err != nil {
			t.Fatalf("Normalize(%q) returned error: %v", in, err)
		}
		if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalize_FragmentDropped(t *testing.T) {
	got, err := Normalize("https://example.com/article#section-2")
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	want := "https://example.com/article"
	if got != want {
		t.Errorf("Normalize() = %q, want %q", got, want)
	}
}

func TestNormalize_RejectsRelativeAndSchemelessURLs(t *testing.T) {
	cases := []string{
		"/some/path",
		"some/path",
		"example.com/path",
		"//example.com/path",
		"",
	}
	for _, in := range cases {
		if _, err := Normalize(in); err == nil {
			t.Errorf("Normalize(%q) succeeded, want error", in)
		}
	}
}

func TestNormalize_DeterministicParameterOrdering(t *testing.T) {
	a, err := Normalize("https://example.com/x?b=2&a=1&c=3")
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	b, err := Normalize("https://example.com/x?c=3&a=1&b=2")
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if a != b {
		t.Errorf("differently ordered query strings normalized to different values: %q vs %q", a, b)
	}
	want := "https://example.com/x?a=1&b=2&c=3"
	if a != want {
		t.Errorf("Normalize() = %q, want %q", a, want)
	}
}

func TestNormalize_LowercasesSchemeAndHostNotPath(t *testing.T) {
	got, err := Normalize("HTTPS://Example.COM/Some/Path")
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	want := "https://example.com/Some/Path"
	if got != want {
		t.Errorf("Normalize() = %q, want %q", got, want)
	}
}

func TestNormalize_TrailingSlashRemovedFromNonEmptyPath(t *testing.T) {
	got, err := Normalize("https://example.com/article/")
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	want := "https://example.com/article"
	if got != want {
		t.Errorf("Normalize() = %q, want %q", got, want)
	}

	// Root path must NOT be reduced to empty.
	got, err = Normalize("https://example.com/")
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	want = "https://example.com/"
	if got != want {
		t.Errorf("Normalize() = %q, want %q", got, want)
	}
}

func TestEqual(t *testing.T) {
	if !Equal("https://Example.com:443/a/?utm_source=x&id=1", "https://example.com/a?id=1") {
		t.Error("expected the same URL written two ways to be Equal")
	}
	if Equal("https://example.com/a", "https://example.com/b") {
		t.Error("expected different URLs to not be Equal")
	}
	if Equal("not a url", "https://example.com/a") {
		t.Error("expected Equal to return false when a URL fails to normalize")
	}
	if Equal("https://example.com/a", "not a url") {
		t.Error("expected Equal to return false when a URL fails to normalize")
	}
}

func TestMustNormalize_PanicsOnInvalid(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected MustNormalize to panic on an unnormalisable URL")
		}
	}()
	MustNormalize("/relative/path")
}

// TestNormalize_Idempotent is the load-bearing property for §9.6: a value
// that has already been normalized must be a fixed point of Normalize, or
// the byte-equality link-integrity check can reject a good link.
func TestNormalize_Idempotent(t *testing.T) {
	inputs := []string{
		"https://example.com/article?utm_source=x&id=1",
		"HTTP://Example.COM:80/Path/?b=2&a=1&utm_campaign=y",
		"https://example.com:443/",
		"https://example.com/a/b/c/",
		"https://example.com/path?a=1&a=2&b=3",
		"https://example.com/%20path?q=%2Fx%2Fy",
		"https://example.com/article#frag",
		"https://sub.example.com:8443/x?utm_medium=email&ref=foo&keep=1",
		"https://example.com/?",
		"https://user:pass@example.com/secure",
	}
	for _, in := range inputs {
		once, err := Normalize(in)
		if err != nil {
			t.Fatalf("Normalize(%q) returned error: %v", in, err)
		}
		twice, err := Normalize(once)
		if err != nil {
			t.Fatalf("Normalize(Normalize(%q)) returned error: %v", in, err)
		}
		if once != twice {
			t.Errorf("not idempotent for %q: Normalize()=%q, Normalize(Normalize())=%q", in, once, twice)
		}
	}
}

// FuzzNormalizeIdempotent asserts Normalize(Normalize(u)) == Normalize(u) for
// arbitrary input, skipping inputs that fail to normalize at all. This is the
// property §9.6's byte-equality link check depends on.
func FuzzNormalizeIdempotent(f *testing.F) {
	seeds := []string{
		"https://example.com/article?utm_source=x&id=1",
		"HTTP://Example.COM:80/Path/?b=2&a=1",
		"https://example.com:443/",
		"not a url",
		"/relative/path",
		"https://example.com/a/b/c/#frag",
		"https://user:pass@example.com/secure?gclid=1&keep=2",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		once, err := Normalize(raw)
		if err != nil {
			t.Skip("input does not normalize")
		}
		twice, err := Normalize(once)
		if err != nil {
			t.Fatalf("Normalize(%q) succeeded but Normalize(%q) failed: %v", raw, once, err)
		}
		if once != twice {
			t.Fatalf("not idempotent: Normalize(%q)=%q, Normalize(%q)=%q", raw, once, once, twice)
		}
	})
}
