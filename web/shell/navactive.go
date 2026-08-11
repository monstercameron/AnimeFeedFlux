package shell

import "strings"

// navItemActive reports whether a nav destination owns the current route.
//
// Exact equality is not enough: /settings has sub-routes (/settings/provider,
// /settings/data, ...), so navigating into a section made the current path
// stop equalling "/settings" and the Settings link lost its underline and its
// aria-current="page" — the top-level nav claimed you were nowhere while you
// were plainly inside Settings.
//
// The prefix match is anchored on a SEGMENT boundary (`item + "/"`), not a
// bare strings.HasPrefix: a future "/settings-export" route must not light up
// the Settings tab just because the string starts the same way. Three fixed
// destinations make that impossible today; the boundary is here so it stays
// impossible when a fourth is added.
func navItemActive(currentPath, itemPath string) bool {
	return currentPath == itemPath || strings.HasPrefix(currentPath, itemPath+"/")
}
