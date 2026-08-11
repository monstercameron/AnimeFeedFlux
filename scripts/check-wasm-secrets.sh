#!/bin/sh
# scripts/check-wasm-secrets.sh — SEC-25 (TODOS.md), D-phase check.
#
# PLAN.md §4's whole session design rests on one property: "the token never
# touches JavaScript or WASM" — the client cannot read its own credential,
# which is what makes an XSS in the admin app survivable. That property is
# an assertion until something greps the actual compiled artifact for
# evidence it was violated. This script is that something.
#
# It checks two things in the BUILT wasm bundle:
#   1. The session cookie name (internal/auth/session.go's cookieName,
#      "__Host-aff_session") must not appear anywhere in the binary. If it
#      did, that would mean something in the client is at minimum naming
#      the cookie — the first step toward reading it — when the design
#      requires the browser to never even reference it.
#   2. The obvious client-side storage sinks the design forbids —
#      localStorage, sessionStorage, IndexedDB — are reported if present at
#      all (informational: this codebase's UI framework, GoWebComponents,
#      ships a generic localStorage helper used for non-secret things like
#      theme/i18n prefs, so bare presence is expected noise) and escalated
#      to a hard FAIL only if a storage-sink string sits near a
#      token/session identifier in the binary, which would suggest an
#      actual attempt to persist the credential rather than an unrelated
#      framework capability.
#
# Usage:
#   scripts/check-wasm-secrets.sh                # build via build-web.sh, then scan web/dist/app.wasm
#   scripts/check-wasm-secrets.sh path/to/app.wasm  # scan an already-built bundle, skip building
#
# Exit codes:
#   0 — scanned, nothing found.
#   1 — scanned, found something. This is a real failure.
#   2 — SKIPPED: no bundle could be found or built. This is NOT a pass. A
#       check that silently exits 0 when it never actually ran is worse
#       than no check at all, so this path is loud (stderr banner) and
#       non-zero specifically so it cannot be mistaken for success in CI
#       output or in a $?  check.
#
# The bundle is on the order of tens of MB, so every scan below greps the
# file by path (grep streams it) rather than ever doing something like
# `content=$(cat "$bundle")`, which would load the whole thing into a shell
# variable for no reason.
set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)

skip() {
	echo "" >&2
	echo "=================================================================" >&2
	echo "check-wasm-secrets: SKIPPED — this check did NOT run." >&2
	echo "=================================================================" >&2
	echo "$1" >&2
	echo "" >&2
	echo "To run it for real:" >&2
	echo "    scripts/build-web.sh                    # builds web/dist/app.wasm" >&2
	echo "    scripts/check-wasm-secrets.sh            # builds (if needed) and scans it" >&2
	echo "  or, against an already-built bundle:" >&2
	echo "    scripts/check-wasm-secrets.sh /path/to/app.wasm" >&2
	echo "=================================================================" >&2
	exit 2
}

if [ $# -ge 1 ]; then
	bundle=$1
else
	bundle="$repo_root/web/dist/app.wasm"
	if [ ! -f "$bundle" ]; then
		echo "check-wasm-secrets: no bundle at $bundle; building via scripts/build-web.sh ..." >&2
		if ! sh "$script_dir/build-web.sh" >&2; then
			skip "scripts/build-web.sh failed (see its output above) — cannot scan a bundle that didn't build."
		fi
	fi
fi

if [ ! -f "$bundle" ]; then
	skip "no bundle found at: $bundle"
fi
if [ ! -s "$bundle" ]; then
	skip "bundle at $bundle exists but is empty (0 bytes) — treating as not built."
fi

size=$(wc -c <"$bundle" | tr -d ' ')

# --- Derive the cookie name from its single source of truth rather than
# duplicating the literal here, so this check cannot silently drift from
# internal/auth/session.go if the cookie is ever renamed. --------------------
session_go="$repo_root/internal/auth/session.go"
cookie_name=""
if [ -f "$session_go" ]; then
	cookie_line=$(grep -m1 '^const cookieName = ' "$session_go" || true)
	if [ -n "$cookie_line" ]; then
		cookie_name=$(printf '%s\n' "$cookie_line" | sed -E 's/^const cookieName = "(.*)"$/\1/')
	fi
fi
if [ -z "$cookie_name" ]; then
	# Fallback only: internal/auth/session.go moved or changed shape.
	# PLAN.md §4 / TODOS.md SEC-21 fix this literal, so a hardcoded fallback
	# is still a faithful check, just one that can drift silently — hence
	# the warning.
	cookie_name="__Host-aff_session"
	echo "check-wasm-secrets: WARNING: could not read cookieName from $session_go; falling back to hardcoded \"$cookie_name\"" >&2
fi

echo "check-wasm-secrets: scanning $bundle ($size bytes) for cookie name \"$cookie_name\""

fail=0

# --- Check 1: the cookie name itself. ---------------------------------------
cookie_hits=$(LC_ALL=C grep -a -c -F -- "$cookie_name" "$bundle" || true)
if [ "$cookie_hits" -gt 0 ]; then
	echo "check-wasm-secrets: FAIL — cookie name \"$cookie_name\" appears $cookie_hits time(s) in $bundle" >&2
	fail=1
else
	echo "check-wasm-secrets: OK — cookie name absent from bundle"
fi

# --- Check 2: forbidden storage sinks, reported; escalated only alongside a
# token/session identifier. ---------------------------------------------------
sinks="localStorage sessionStorage indexedDB IndexedDB"
identifiers="$cookie_name aff_session session_token sessionToken authToken SessionToken"

sink_hit=0
for sink in $sinks; do
	n=$(LC_ALL=C grep -a -c -F -- "$sink" "$bundle" || true)
	if [ "$n" -gt 0 ]; then
		sink_hit=1
		echo "check-wasm-secrets: NOTE — storage sink \"$sink\" appears $n time(s) in $bundle (informational; not a fail by itself — see header comment)"
	fi
done
if [ "$sink_hit" -eq 0 ]; then
	echo "check-wasm-secrets: OK — no localStorage/sessionStorage/IndexedDB strings in bundle"
fi

identifier_hit=0
for id in $identifiers; do
	n=$(LC_ALL=C grep -a -c -F -- "$id" "$bundle" || true)
	if [ "$n" -gt 0 ]; then
		identifier_hit=1
		echo "check-wasm-secrets: FAIL — token/session identifier \"$id\" appears $n time(s) in $bundle" >&2
		fail=1
	fi
done
if [ "$identifier_hit" -eq 0 ]; then
	echo "check-wasm-secrets: OK — no token/session identifier strings in bundle"
fi

# --- Check 3: a storage sink and a token/session identifier co-occurring
# within a short window is the actual attack shape (client code building
# something like localStorage.setItem("aff_session", ...)) — escalate to
# FAIL even if check 2 above didn't already fail on the identifier alone,
# and even if grep -P (PCRE, for bounded-distance lookaround) isn't
# available anywhere to run it.
if [ "$sink_hit" -eq 1 ] && command -v grep >/dev/null 2>&1 && printf 'x' | grep -P 'x' >/dev/null 2>&1; then
	for sink in $sinks; do
		for id in $identifiers; do
			if LC_ALL=C grep -a -P -q -- "(?s)${sink}.{0,300}${id}|${id}.{0,300}${sink}" "$bundle" 2>/dev/null; then
				echo "check-wasm-secrets: FAIL — \"$sink\" found within 300 bytes of \"$id\" in $bundle — looks like an actual attempt to persist the credential, not incidental framework noise" >&2
				fail=1
			fi
		done
	done
elif [ "$sink_hit" -eq 1 ]; then
	echo "check-wasm-secrets: NOTE — grep -P unavailable, skipped the storage-sink/identifier proximity check (identifiers were already scanned individually above)"
fi

if [ "$fail" -ne 0 ]; then
	echo "" >&2
	echo "check-wasm-secrets: FAILED — see above." >&2
	exit 1
fi

echo "check-wasm-secrets: PASS — $bundle scanned clean"
exit 0
