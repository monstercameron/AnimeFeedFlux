package shell

import "testing"

// TestNavItemActive covers the header's section-ownership rule, which broke
// the moment /settings gained sub-routes: exact equality made the Settings
// link lose its underline and its aria-current the instant you opened a
// section, so the top-level nav claimed you were nowhere while you were
// plainly inside Settings.
func TestNavItemActive(t *testing.T) {
	cases := []struct {
		name    string
		current string
		item    string
		want    bool
	}{
		{"exact match", "/settings", "/settings", true},
		{"a sub-route keeps the section active", "/settings/provider", "/settings", true},
		{"a deeper sub-route too", "/settings/provider/extra", "/settings", true},
		{"a different section is not active", "/history", "/settings", false},
		// The boundary. A bare strings.HasPrefix would light up Settings for
		// this, which is why the match is anchored on the "/" separator.
		{"a same-prefix sibling route must NOT match", "/settings-export", "/settings", false},
		{"a prefix of the item is not a match", "/set", "/settings", false},
		{"root-ish path does not match everything", "/", "/settings", false},
	}
	for _, c := range cases {
		if got := navItemActive(c.current, c.item); got != c.want {
			t.Errorf("%s: navItemActive(%q, %q) = %v, want %v", c.name, c.current, c.item, got, c.want)
		}
	}
}
