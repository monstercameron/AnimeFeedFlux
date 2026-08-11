#!/bin/sh
# check-styles.sh — regression guard for TODOS.md D0-24/D0-25 (PLAN.md §12.6):
# "all real styling lives in the WASM, authored with GWC's typed CSS library
# (web/shell/styles.go, web/pages/*/styles.go, web/tokens)", not in
# web/static/index.html's markup.
#
# Two checks, of DIFFERENT honesty — read both before trusting a green run:
#
#   (a) STATICALLY PROVABLE (D0-24). web/static/index.html must never regain
#       a hand-written <style> block. This is a simple, complete check: a
#       <style> tag in that file is unambiguous regardless of what's inside
#       it, so this check is a hard pass/fail with no caveats.
#
#   (b) PARTIALLY PROVABLE (D0-25). Every literal class name passed to
#       ClassStr(...) or used as a ClassMap(map[string]bool{...}) key is
#       diffed against every literal selector declared via css.Class(...) /
#       css.Global(...) across web/. A name with no matching rule is exactly
#       the failure mode that motivated this script: 103 af-*/history-*
#       classes were being emitted with zero rules anywhere, and nothing
#       caught it until a screenshot did.
#
#       What this check does NOT prove, on purpose, because a source-level
#       diff structurally cannot:
#         - It only sees class names that are Go STRING LITERALS. A class
#           name built from a variable, string concatenation, or
#           fmt.Sprintf is invisible to it — it will neither flag a missing
#           rule for one nor a used-but-unmatched literal from one.
#         - A literal name on both sides of the diff proves the SOURCE
#           declares a rule with that selector text. It proves nothing about
#           the BROWSER actually applying it: a selector typo elsewhere in
#           the same rule, a specificity loss, or a css.Global/css.Class
#           call that never runs at runtime (e.g. an emit function nobody
#           calls — the exact tokens.Emit() bug this task's history already
#           hit once) would all still pass this diff.
#         - It says nothing about *correctness* of a matched rule (right
#           token, right theme, right property) — only that a rule with that
#           selector text exists somewhere in the source.
#
#       The backstop for everything above is a BROWSER assertion, not a
#       static one — see TODOS.md D0-27(b): fold "no element has zero
#       applied CSS rules besides UA defaults" into the D5-05 walkthrough,
#       across all five routes, both themes. That check belongs there, not
#       here, because it requires a running app and a real DOM, which this
#       script deliberately does not.
#
# Usage: sh scripts/check-styles.sh
# Exit status: 0 if both checks pass, 1 if either fails.

set -eu

cd "$(dirname "$0")/.."

fail=0

INDEX_HTML="web/static/index.html"

echo "== check-styles.sh (a) index.html <style> regression guard =="
if [ ! -f "$INDEX_HTML" ]; then
  echo "FAIL: $INDEX_HTML not found"
  fail=1
elif perl -0777 -pe 's/<!--.*?-->//gs' "$INDEX_HTML" | grep -qiE '<style[ >]'; then
  echo "FAIL: $INDEX_HTML contains a <style> tag — D0-24 requires all real"
  echo "      styling to live in Go (web/shell, web/pages/*/styles.go,"
  echo "      web/tokens), not hand-written CSS in the shell HTML. See the"
  echo "      comment above index.html's <base href> for what, if anything,"
  echo "      is allowed to remain and why."
  fail=1
else
  echo "PASS: no <style> tag in $INDEX_HTML"
fi

echo
echo "== check-styles.sh (b) ClassStr/ClassMap -> css.Class/css.Global coverage (partial, see header) =="

classes_file="$(mktemp)"
rules_file="$(mktemp)"
trap 'rm -f "$classes_file" "$rules_file"' EXIT

# Every ClassStr("literal ...") argument and every literal key of a
# ClassMap(map[string]bool{"literal": ...}) call, one class name per line.
# A ClassStr/ClassMap argument can itself carry multiple space-separated
# class names (e.g. "af-banner af-banner--hidden"), so each literal is
# further split on whitespace.
perl -ne '
  while (/ClassStr\(\s*"([^"]*)"/g) { print "$_\n" for split(/\s+/, $1); }
  while (/ClassMap\(\s*map\[string\]bool\{([^}]*)\}/g) {
    my $body = $1;
    while ($body =~ /"([^"]*)"\s*:/g) { print "$_\n" for split(/\s+/, $1); }
  }
' $(find web -name '*.go') | sort -u > "$classes_file"

# Every css.Class("literal") / css.Global("literal") selector's first
# (dot-stripped, pseudo/combinator-stripped) token, one per line.
perl -ne '
  while (/css\.(?:Class|Global)\(\s*"([^"]*)"/g) {
    my $sel = (split(/\s+/, $1))[0];
    $sel =~ s/^\.//;
    $sel =~ s/[:>\[].*$//;
    print "$sel\n" if length($sel);
  }
' $(find web -name '*.go') | sort -u > "$rules_file"

total_classes=$(wc -l < "$classes_file" | tr -d ' ')
total_rules=$(wc -l < "$rules_file" | tr -d ' ')
missing=$(comm -23 "$classes_file" "$rules_file")
missing_count=$(printf '%s' "$missing" | grep -c . || true)

echo "literal class names emitted (ClassStr/ClassMap):    $total_classes"
echo "literal selectors declared (css.Class/css.Global):  $total_rules"

if [ -n "$missing" ]; then
  echo "FAIL: $missing_count class name(s) with no matching literal rule anywhere in web/:"
  printf '%s\n' "$missing" | sed 's/^/  - /'
  fail=1
else
  echo "PASS: every literal ClassStr/ClassMap name has a matching literal css.Class/css.Global selector"
fi

echo
if [ "$fail" -ne 0 ]; then
  echo "check-styles.sh: FAIL"
  echo "Reminder: check (b) passing is necessary but not sufficient — it"
  echo "does not prove the browser resolves these rules. See D0-27(b) / D5-05."
  exit 1
fi

echo "check-styles.sh: PASS (check (b)'s limits above still apply — this is not a browser proof)"
