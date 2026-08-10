#!/bin/sh
# deploy-verify.sh — post-deploy verification beyond "healthy" (PLAN.md §5.4,
# §5.6, §6, §19; TODOS C2-08, C5-07).
#
# The container healthcheck (deploy-release.sh) proves the process is up and
# answering. It does NOT prove the product works: the product is a URL that
# returns valid RSS/Atom/JSON Feed, honours conditional GET, and is reachable
# over the same public HTTPS path a real subscriber uses — not localhost, not
# the loopback port nginx fronts. This script is that check.
#
# Run from anywhere with `go` and network access to the public hostname —
# a CI runner, or a laptop. It does NOT need to run on the droplet.
#
#   sh scripts/deploy-verify.sh
#   sh scripts/deploy-verify.sh anime-trivia-daily anime-fact-daily
#   AFF_VERIFY_BASE_URL=https://staging.anime.earlcameron.com sh scripts/deploy-verify.sh
#
# For each feed slug x format (xml/atom/json):
#   1. GET over HTTPS, check HTTP 200 and the exact Content-Type (§6 renderer
#      contract — text/xml is a documented non-goal, not an acceptable substitute).
#   2. GET again with the validators from step 1's response (If-None-Match /
#      If-Modified-Since) and require 304 — this is the entire publish-plane
#      performance story (§5.4); if it silently regressed to always-200,
#      every honest poller starts costing full renders.
#   3. Run affvalidate (cmd/affvalidate) against the LIVE bytes from step 1,
#      not a local golden — that is the point of this being a deploy check
#      and not just a unit test.

set -eu

BASE_URL="${AFF_VERIFY_BASE_URL:-https://anime.earlcameron.com}"
HEALTHZ_URL="${AFF_VERIFY_HEALTHZ_URL:-$BASE_URL/healthz}"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

log()  { printf '[deploy-verify] %s\n' "$1"; }
fail() { printf '[deploy-verify] FAIL: %s\n' "$1" >&2; FAILED=1; }
ok()   { printf '[deploy-verify] ok:   %s\n' "$1"; }

FAILED=0

# --- 0. resolve the offline validator ---------------------------------------
#
# Prefer a pre-built binary (AFF_VALIDATE_BIN) so this script works outside
# the repo (e.g. a bare CI checkout with no go.mod nearby is not the case
# here, but a future standalone verify runner might be). Fall back to `go run`
# from the repo, which is what a checkout-then-verify CI job actually has.
if [ -n "${AFF_VALIDATE_BIN:-}" ]; then
    VALIDATE_CMD="$AFF_VALIDATE_BIN"
elif command -v go >/dev/null 2>&1 && [ -f "$REPO_ROOT/go.mod" ]; then
    VALIDATE_CMD="go -C $REPO_ROOT run ./cmd/affvalidate"
else
    fail "no validator available: set AFF_VALIDATE_BIN or run from a repo checkout with 'go' installed"
    VALIDATE_CMD=""
fi

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

# --- 1. liveness -------------------------------------------------------------

log "checking $HEALTHZ_URL"
code=$(curl -sS -o "$WORKDIR/healthz.json" -w '%{http_code}' --max-time 20 "$HEALTHZ_URL" || echo 000)
if [ "$code" = "200" ]; then
    ok "/healthz returned 200"
else
    fail "/healthz returned $code (expected 200) — aborting, nothing downstream is trustworthy"
    cat "$WORKDIR/healthz.json" >&2 2>/dev/null || true
    exit 1
fi

# --- feed slugs ---------------------------------------------------------

if [ "$#" -gt 0 ]; then
    SLUGS="$*"
else
    # §19 definition-of-done names these three explicitly. Override with
    # positional args for staging, a new feed, or a partial re-check.
    SLUGS="anime-trivia-daily anime-fact-daily anime-news-daily"
fi

# --- per-format expectations (§6 renderer contract) -------------------------
#
# text/xml is deliberately absent: internal/publish/server_test.go asserts
# Content-Type must never be text/xml for the .xml route, because some
# readers treat text/xml as "render this in the browser" instead of "this is
# a feed" — application/rss+xml is the contract, and this check exists so a
# regression there is caught here, not by a subscriber's broken preview.
format_ct() {
    case "$1" in
        xml)  echo "application/rss+xml" ;;
        atom) echo "application/atom+xml" ;;
        json) echo "application/feed+json" ;;
    esac
}

for slug in $SLUGS; do
    for fmt in xml atom json; do
        url="$BASE_URL/feeds/${slug}.${fmt}"
        body_file="$WORKDIR/${slug}.${fmt}"
        headers_file="$WORKDIR/${slug}.${fmt}.headers"

        log "GET $url"
        code=$(curl -sS -D "$headers_file" -o "$body_file" -w '%{http_code}' --max-time 20 "$url" || echo 000)

        if [ "$code" != "200" ]; then
            fail "$url returned $code (expected 200)"
            continue
        fi

        # --- content type -----------------------------------------------
        ct=$(grep -i '^content-type:' "$headers_file" | tr -d '\r' | sed 's/^[Cc]ontent-[Tt]ype:[[:space:]]*//' | head -1)
        want=$(format_ct "$fmt")
        case "$ct" in
            "$want"*) ok "$url content-type: $ct" ;;
            *) fail "$url content-type = '$ct', want prefix '$want'" ;;
        esac

        # --- conditional GET: expect 304 on replay ------------------------
        etag=$(grep -i '^etag:' "$headers_file" | tr -d '\r' | sed 's/^[Ee][Tt][Aa][Gg]:[[:space:]]*//' | head -1)
        last_mod=$(grep -i '^last-modified:' "$headers_file" | tr -d '\r' | sed 's/^[Ll]ast-[Mm]odified:[[:space:]]*//' | head -1)

        if [ -z "$etag" ] && [ -z "$last_mod" ]; then
            fail "$url sent neither ETag nor Last-Modified — conditional GET cannot be tested and honest pollers cannot cache"
        else
            cond_code=$(
                if [ -n "$etag" ] && [ -n "$last_mod" ]; then
                    curl -sS -o /dev/null -w '%{http_code}' --max-time 20 \
                        -H "If-None-Match: $etag" -H "If-Modified-Since: $last_mod" "$url"
                elif [ -n "$etag" ]; then
                    curl -sS -o /dev/null -w '%{http_code}' --max-time 20 \
                        -H "If-None-Match: $etag" "$url"
                else
                    curl -sS -o /dev/null -w '%{http_code}' --max-time 20 \
                        -H "If-Modified-Since: $last_mod" "$url"
                fi
            )
            if [ "$cond_code" = "304" ]; then
                ok "$url conditional GET -> 304"
            else
                fail "$url conditional GET -> $cond_code (expected 304) — the 304 path is the entire performance story here (§5.4)"
            fi
        fi

        # --- offline validator against the LIVE bytes ----------------------
        if [ -n "$VALIDATE_CMD" ]; then
            # Extension picks the validator's format dispatch (see
            # cmd/affvalidate). $body_file already ends in .xml/.atom/.json.
            if out=$($VALIDATE_CMD "$body_file" 2>&1); then
                ok "$url affvalidate: clean"
            else
                fail "$url affvalidate reported findings:"
                printf '%s\n' "$out" >&2
            fi
        fi
    done
done

if [ "$FAILED" -eq 0 ]; then
    log "all checks passed for: $SLUGS"
    exit 0
else
    log "one or more checks failed — see FAIL lines above"
    exit 1
fi
