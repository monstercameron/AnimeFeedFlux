package i18n

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
)

// locale_test.go guards the two properties that make a second locale safe to
// ship, plus the locale-selection logic itself.
//
// The catalogue-completeness tests in catalog_test.go answer "does every key
// the code references exist?" for English. These answer the question a second
// locale introduces: "does every OTHER locale carry the same keys, with the
// same placeholders?" Both failure modes are invisible at compile time and
// both render as something an operator sees — an English sentence in the
// middle of a Spanish page, or a literal "{arg1}" where a number should be.

// localeCatalogs is every locale this app registers, by tag. Built from the
// same two package vars NewBundle registers so a third locale added later is
// covered by these tests the moment it is added here.
var localeCatalogs = map[string]gwci18n.Catalog{
	LocaleEnglish: enCatalog,
	LocaleSpanish: esCatalog,
}

// TestEveryShippedLocaleIsRegistered is the first thing that would go wrong
// with a third locale: SupportedLocales drives the SELECTOR, localeCatalogs
// (and NewBundle) drive what actually exists. Add a language to the list and
// forget the catalogue, and the control offers a language that renders as
// English with a logged missing key for every string on screen.
func TestEveryShippedLocaleIsRegistered(t *testing.T) {
	for _, opt := range SupportedLocales() {
		if _, ok := localeCatalogs[opt.Tag]; !ok {
			t.Errorf("locale %q is offered by SupportedLocales but has no catalogue", opt.Tag)
		}
		if strings.TrimSpace(opt.Endonym) == "" {
			t.Errorf("locale %q has no endonym — the selector would render a blank option", opt.Tag)
		}
	}
	// And the reverse: a catalogue nobody can select is dead weight that
	// still costs bundle size.
	for tag := range localeCatalogs {
		if !IsSupportedLocale(tag) {
			t.Errorf("catalogue %q is registered but not offered by SupportedLocales — unreachable", tag)
		}
	}
}

// TestEveryLocaleHasEveryKey is the parity check the three page namespaces
// need, since they key their catalogues with restated string literals rather
// than shared constants (see keys_settings.go's doc comment on why) and so
// have no compile-time link between English and its translations.
//
// A missing key is not silent in production — Bundle.Translate falls back to
// DefaultLocale, so it renders English — but "renders English" inside a
// Spanish page is exactly the bug this catches, and it catches it at the
// moment the key is added rather than the moment someone notices.
func TestEveryLocaleHasEveryKey(t *testing.T) {
	for tag, catalog := range localeCatalogs {
		if tag == DefaultLocale {
			continue
		}
		for ns, enMessages := range enCatalog {
			translated, ok := catalog[ns]
			if !ok {
				t.Errorf("locale %q has no %q namespace at all", tag, ns)
				continue
			}
			missing := make([]string, 0)
			for key := range enMessages {
				if _, found := translated[key]; !found {
					missing = append(missing, key)
				}
			}
			sort.Strings(missing)
			for _, key := range missing {
				t.Errorf("locale %q, namespace %q: missing key %q (it will render in English)", tag, ns, key)
			}
		}
	}
}

// TestNoLocaleHasExtraKeys catches the opposite drift: a key that was
// RENAMED or removed in English but left behind in a translation. Those are
// pure cost — they are never looked up, they never fail, and they quietly
// accumulate until nobody can tell which entries are live.
func TestNoLocaleHasExtraKeys(t *testing.T) {
	for tag, catalog := range localeCatalogs {
		if tag == DefaultLocale {
			continue
		}
		for ns, translated := range catalog {
			enMessages, ok := enCatalog[ns]
			if !ok {
				t.Errorf("locale %q declares namespace %q, which English does not have", tag, ns)
				continue
			}
			orphans := make([]string, 0)
			for key := range translated {
				if _, found := enMessages[key]; !found {
					orphans = append(orphans, key)
				}
			}
			sort.Strings(orphans)
			for _, key := range orphans {
				t.Errorf("locale %q, namespace %q: key %q has no English counterpart — renamed or dead", tag, ns, key)
			}
		}
	}
}

// messagePlaceholderPattern matches the {name} interpolation slots gwci18n's
// Message templates use. Both conventions in this app are covered: the
// positional arg1/arg2 form web/ui's T signature forces, and the named form
// ({count}, {seconds}, {path}, {uri}) the shell and page catalogues use.
var messagePlaceholderPattern = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9_]*)\}`)

// TestPlaceholderParityAcrossLocales is the sharpest of these tests, because
// its failure mode is the one that actually breaks rather than degrades.
//
// Placeholders are substituted BY NAME. A translator who renders "{count}
// recovery codes left" as "Quedan {cuenta} códigos" produces a string that
// compiles, ships, and renders the literal text "{cuenta}" to the operator —
// the number is simply gone. Worse, a DROPPED placeholder loses information
// silently: "Version {arg1}" translated as "Versión" looks perfectly fine
// until someone asks which version.
//
// Comparison is on the SET of names, not the count or the order: word order
// legitimately differs between languages, and repeating a placeholder is
// legitimate too.
func TestPlaceholderParityAcrossLocales(t *testing.T) {
	for tag, catalog := range localeCatalogs {
		if tag == DefaultLocale {
			continue
		}
		for ns, enMessages := range enCatalog {
			translated, ok := catalog[ns]
			if !ok {
				continue // reported by TestEveryLocaleHasEveryKey
			}
			for key, enMsg := range enMessages {
				trMsg, found := translated[key]
				if !found {
					continue // reported by TestEveryLocaleHasEveryKey
				}
				want := messagePlaceholders(enMsg)
				got := messagePlaceholders(trMsg)
				if !equalStringSets(want, got) {
					t.Errorf("locale %q, namespace %q, key %q: placeholders differ\n  en: %v\n  %s: %v",
						tag, ns, key, want, tag, got)
				}
			}
		}
	}
}

// TestPluralFormsSurviveTranslation checks the other half of a plural
// message: a translated entry that carries Text where English carries Plural
// renders the same string for 1 and for 5, which reads as broken grammar in
// every language that has a plural rule at all.
func TestPluralFormsSurviveTranslation(t *testing.T) {
	for tag, catalog := range localeCatalogs {
		if tag == DefaultLocale {
			continue
		}
		for ns, enMessages := range enCatalog {
			translated, ok := catalog[ns]
			if !ok {
				continue
			}
			for key, enMsg := range enMessages {
				trMsg, found := translated[key]
				if !found {
					continue
				}
				if len(enMsg.Plural) > 0 && len(trMsg.Plural) == 0 {
					t.Errorf("locale %q, namespace %q, key %q: English is plural-keyed but the translation is not", tag, ns, key)
				}
				if enMsg.PluralArg != trMsg.PluralArg {
					t.Errorf("locale %q, namespace %q, key %q: PluralArg differs (en %q vs %q) — the count would never select a form",
						tag, ns, key, enMsg.PluralArg, trMsg.PluralArg)
				}
			}
		}
	}
}

// TestTranslatedTextIsNotIdenticalToEnglish is a soft check with a
// deliberately generous allowlist. It exists to catch the copy-paste-and-
// forget failure — a namespace stubbed out with English text to make the
// parity test pass and never revisited — without flagging the many entries
// that are IDENTICAL for good reason: pure passthrough templates, format
// names, and words Spanish borrows unchanged.
func TestTranslatedTextIsNotIdenticalToEnglish(t *testing.T) {
	// Entries whose Spanish is legitimately the same as English.
	sameByDesign := map[string]bool{
		// Pure passthrough templates: no words at all.
		"generate.common.labelValue": true,
		"generate.common.errorText":  true,
		"generate.rail.slugPath":     true,
		"generate.rail.compactMeta":  true,
		// "Feed" is already kept as-is elsewhere in Spanish (generate.rail.newFeed,
		// generate.workbench.newFeed/chooseFeed) — a borrowed term, not left untranslated.
		"generate.workbench.feedsSummary": true,
		// The schedule readback's glue: a comma-space list separator, and a
		// "{time} ({zone})" template with no words in it. Both are punctuation
		// and placeholders, and Spanish uses the same ones.
		"generate.editor.schedule.readback.listSeparator": true,
		"generate.editor.schedule.readback.at":            true,
		"generate.workbench.stakes.schedule":              true,
		"generate.workbench.sizeN":                        true,
		"generate.editor.cron.readback.raw":               true,
		"history.errors.rejected":                         true,
		"history.runs.added_rejected_value":               true,
		"history.runs.tokens_value":                       true,
		"history.runs.reject_reason_count":                true,
		"settings.security.sessions.current":              true,
		// A backend identifier, not a word: "openai" is what the operator
		// literally types, in every language.
		"settings.provider.activeProvider.placeholder": true,
		// Provider brand names: identical in every language by definition.
		// (backend.openai carries a translated "(default)" suffix, so it is
		// not in this list.)
		"settings.provider.backend.anthropic":       true,
		"settings.provider.backend.openrouter":      true,
		"settings.provider.backend.cerebras":        true,
		"settings.provider.backend.deepseek":        true,
		"settings.provider.backend.qwen":            true,
		"settings.provider.backend.zai":             true,
		"settings.provider.profile.namePlaceholder": true,
		// Format and protocol names, and borrowed terms.
		"generate.urls.rss":                  true,
		"generate.urls.atom":                 true,
		"generate.urls.json":                 true,
		"generate.editor.sourceUrl":          true,
		"generate.editor.slug":               true,
		"generate.editor.temperature":        true,
		"generate.rail.error":                true,
		"generate.rail.title":                true,
		"generate.workbench.feed":            true,
		"generate.workbench.tempPlaceholder": true,
		"generate.sampler.candidateTab.1":    true,
		"generate.sampler.candidateTab.2":    true,
		"generate.sampler.candidateTab.3":    true,
		"generate.sampler.candidateTab.4":    true,
		"generate.sampler.candidateTab.5":    true,
		"history.runs.filter_feed":           true,
		"history.runs.col_tokens":            true,
		"history.runs.col_error":             true,
		"history.runs.trigger.manual":        true,
		"history.items.origin.manual":        true,
		"history.items.field_feed":           true,
		"settings.data.recipe.feed":          true,
		"settings.data.stats.feeds":          true,
		// The About table's first column is headed "Feed" in both languages
		// — Spanish borrows the word, and the column holds feed slugs.
		"settings.about.feed.col.slug":    true,
		"settings.provider.profile.label": true,
		"settings.common.no":              true,
		"settings.about.uptime.parts":     true,
	}

	for tag, catalog := range localeCatalogs {
		if tag == DefaultLocale {
			continue
		}
		identical := 0
		total := 0
		for ns, enMessages := range enCatalog {
			translated, ok := catalog[ns]
			if !ok {
				continue
			}
			for key, enMsg := range enMessages {
				trMsg, found := translated[key]
				if !found || enMsg.Text == "" || sameByDesign[key] {
					continue
				}
				total++
				if enMsg.Text == trMsg.Text {
					identical++
					t.Errorf("locale %q, key %q: translation is byte-identical to English (%q) — untranslated, or add it to sameByDesign with a reason",
						tag, key, enMsg.Text)
				}
			}
		}
		if total == 0 {
			t.Errorf("locale %q: no comparable entries found at all — the catalogue is probably empty", tag)
		}
	}
}

// --- Locale selection -------------------------------------------------

func TestSetCurrentLocaleRefusesUnknownTags(t *testing.T) {
	t.Cleanup(func() { SetCurrentLocale(DefaultLocale) })

	if !SetCurrentLocale(LocaleSpanish) {
		t.Fatalf("SetCurrentLocale(%q) returned false for a shipped locale", LocaleSpanish)
	}
	if got := CurrentLocale(); got != LocaleSpanish {
		t.Fatalf("CurrentLocale() = %q, want %q", got, LocaleSpanish)
	}

	// The important half: an unknown tag must not be stored. Storing it
	// would leave every lookup taking the missing-key fallback and logging,
	// for every string in the app.
	if SetCurrentLocale("kl") {
		t.Error("SetCurrentLocale(\"kl\") returned true for a locale with no catalogue")
	}
	if got := CurrentLocale(); got != LocaleSpanish {
		t.Errorf("a refused SetCurrentLocale changed the current locale to %q — it must be left alone", got)
	}
}

func TestNegotiateLocale(t *testing.T) {
	cases := []struct {
		name        string
		preferences []string
		want        string
	}{
		{"exact match", []string{"es"}, LocaleSpanish},
		{"regional variant matches the primary subtag", []string{"es-MX"}, LocaleSpanish},
		{"UN M.49 region code", []string{"es-419"}, LocaleSpanish},
		{"underscore separator", []string{"es_ES"}, LocaleSpanish},
		{"case insensitive", []string{"ES-es"}, LocaleSpanish},
		{"first match wins over later ones", []string{"fr", "es", "en"}, LocaleSpanish},
		{"unshipped languages fall through to a shipped one", []string{"de", "ja", "en-GB"}, LocaleEnglish},
		{"nothing shipped matches", []string{"de", "ja"}, ""},
		{"empty list", nil, ""},
		{"blank entries are skipped", []string{"", "  ", "es"}, LocaleSpanish},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NegotiateLocale(tc.preferences); got != tc.want {
				t.Errorf("NegotiateLocale(%q) = %q, want %q", tc.preferences, got, tc.want)
			}
		})
	}
}

func TestEndonymForUnknownTagReturnsTheTag(t *testing.T) {
	// A stored preference from a build that shipped a language this one does
	// not should render as something visible and correctable, never blank.
	if got := EndonymFor("kl"); got != "kl" {
		t.Errorf("EndonymFor(\"kl\") = %q, want the tag itself", got)
	}
}

func TestSupportedLocalesReturnsACopy(t *testing.T) {
	first := SupportedLocales()
	first[0].Endonym = "mutated"
	second := SupportedLocales()
	if second[0].Endonym == "mutated" {
		t.Error("SupportedLocales returned the package's own slice — a caller can corrupt the selector")
	}
}

// TestTranslateFallsBackToEnglishForAMissingKey pins the behaviour the whole
// design leans on: an untranslated key degrades to English rather than to its
// own raw name. Every t() call site in the app passes DefaultLocale as the
// fallback for exactly this reason.
func TestTranslateFallsBackToEnglishForAMissingKey(t *testing.T) {
	b := NewBundle()
	got := b.Translate(LocaleSpanish, NSSettings, "settings.title", nil, DefaultLocale)
	if got != "Ajustes" {
		t.Fatalf("Spanish lookup returned %q, want the Spanish text — the catalogue may not be registered", got)
	}

	// A key that exists in neither catalogue still renders its own name and
	// logs (D6-07), which is the documented last resort.
	missing := b.Translate(LocaleSpanish, NSSettings, "settings.doesNotExist", nil, DefaultLocale)
	if !strings.Contains(missing, "settings.doesNotExist") {
		t.Errorf("a wholly missing key rendered %q, want the key itself", missing)
	}
}

// --- helpers ----------------------------------------------------------

// messagePlaceholders collects every {name} in a Message, across its Text and all
// of its plural forms, as a set.
func messagePlaceholders(m gwci18n.Message) []string {
	seen := map[string]bool{}
	collect := func(s string) {
		for _, match := range messagePlaceholderPattern.FindAllStringSubmatch(s, -1) {
			seen[match[1]] = true
		}
	}
	collect(m.Text)
	for _, form := range m.Plural {
		collect(form)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
