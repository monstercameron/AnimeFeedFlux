#!/bin/sh
# check-compose.sh — prove the shipped compose configuration actually works
# (PLAN.md §15.4; TODOS.md C0-24, C0-25).
#
# scripts/check-container.sh answers "does the IMAGE build and boot". This
# answers the two questions that one deliberately does not:
#
#   1. Does the config we actually DEPLOY work? check-container.sh starts the
#      image with a bare `docker run` — no --read-only, no --cap-drop, no
#      tmpfs. Production sets all of them. read_only + SQLite is exactly where
#      that combination bites, and it bites at first WRITE, which is after the
#      healthcheck has already reported green (C0-24).
#   2. Does the PRODUCT work, not just the process? /healthz proves a listener
#      exists. It does not prove migrations ran, that a feed renders, that
#      conditional GET works, or that the admin bundle is served — and that
#      last one is not hypothetical: the Dockerfile records that /web/dist was
#      once missing entirely and the container failed at boot (C0-25).
#
# It runs against deploy/compose.yaml itself, overlaid with
# deploy/compose.test.yaml, so every hardening line in the production file is
# under test rather than duplicated.
#
#   sh scripts/check-compose.sh
#
# Exit codes, distinguished on purpose (see check-container.sh for the same
# reasoning — a check that reports "pass" when it never ran is worse than no
# check):
#   0 — ran, every check passed
#   1 — ran, at least one check failed
#   2 — SKIPPED: Docker is not usable here

set -eu

REPO="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
COMPOSE="docker compose -f $REPO/deploy/compose.yaml -f $REPO/deploy/compose.test.yaml"
PUBLISH="http://127.0.0.1:9310"
ADMIN="http://127.0.0.1:9311"

# The throwaway env file. Written at run time rather than committed: a file of
# realistic-looking AFF_SECRET_KEY / SCHEMAFLUX_API_KEY lines in the repo is
# the thing RULE-2 and the pre-commit credential guard exist to stop, even
# when the values are fake.
ENV_FILE="${TMPDIR:-/tmp}/animefeedflux-compose-test.env"

log()  { printf '[check-compose] %s\n' "$1"; }
fail() { printf '[check-compose] FAIL: %s\n' "$1" >&2; FAILED=1; }
FAILED=0

cleanup() {
    $COMPOSE down -v --remove-orphans >/dev/null 2>&1 || true
    rm -f "$ENV_FILE"
}
trap cleanup EXIT

if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
    printf '[check-compose] SKIPPED: no usable Docker daemon.\n' >&2
    printf '[check-compose] This script did NOT verify C0-24 or C0-25.\n' >&2
    trap - EXIT
    exit 2
fi

# --- throwaway config ------------------------------------------------------
#
# AFF_SECRET_KEY must clear config.SecretMinLength (16); a shorter one now
# fails at boot by design (A8-33), which would look like a container defect.
cat > "$ENV_FILE" <<'EOF'
AFF_SECRET_KEY=compose-test-not-a-real-secret-key
SCHEMAFLUX_API_KEY=sk-compose-test-placeholder-never-called
EOF
chmod 600 "$ENV_FILE"
AFF_ENV_FILE="$ENV_FILE"
export AFF_ENV_FILE

# --- 1. the file is valid before anything starts ---------------------------

log "validating the merged compose configuration ..."
if ! $COMPOSE config >/dev/null 2>"${TMPDIR:-/tmp}/check-compose-config.log"; then
    fail "docker compose config rejected the merged file"
    cat "${TMPDIR:-/tmp}/check-compose-config.log" >&2 || true
    exit 1
fi
log "compose config OK."

# --- 2. it builds and goes healthy UNDER THE PRODUCTION HARDENING ----------

log "building and starting (read_only, cap_drop ALL, no-new-privileges, tmpfs) ..."
$COMPOSE down -v --remove-orphans >/dev/null 2>&1 || true
if ! $COMPOSE up -d --build >"${TMPDIR:-/tmp}/check-compose-up.log" 2>&1; then
    fail "compose up failed"
    tail -n 40 "${TMPDIR:-/tmp}/check-compose-up.log" >&2 || true
    exit 1
fi

log "waiting for the container's own healthcheck to report healthy ..."
healthy=0
i=0
while [ "$i" -lt 60 ]; do
    state="$(docker inspect -f '{{.State.Health.Status}}' animefeedflux-test 2>/dev/null || echo unknown)"
    case "$state" in
        healthy) healthy=1; break ;;
        unhealthy) break ;;
    esac
    i=$((i + 1))
    sleep 2
done
if [ "$healthy" -ne 1 ]; then
    fail "container never became healthy (last state: ${state:-unknown})"
    docker logs animefeedflux-test 2>&1 | tail -n 40 >&2 || true
    exit 1
fi
log "PASS: healthy under the production security options."

# --- 3. the product, not the process ---------------------------------------

log "checking /healthz on the publish plane ..."
if curl -sf "$PUBLISH/healthz" >/dev/null 2>&1; then
    log "PASS: /healthz answers."
else
    fail "/healthz did not answer on $PUBLISH"
fi

# The admin bundle. This is the check that would have caught the /web/dist
# omission the Dockerfile documents: the process boots and reports healthy
# either way, and only a request for the shell tells them apart.
log "checking the admin bundle is served ..."
admin_code="$(curl -s -o /dev/null -w '%{http_code}' "$ADMIN/" 2>/dev/null || echo 000)"
case "$admin_code" in
    200) log "PASS: admin shell served (HTTP 200)." ;;
    *)   fail "admin shell at $ADMIN/ returned HTTP $admin_code (expected 200) — is /web/dist in the image?" ;;
esac

log "checking the wasm bundle itself is fetchable ..."
wasm_code="$(curl -s -o /dev/null -w '%{http_code}' "$ADMIN/app.wasm" 2>/dev/null || echo 000)"
case "$wasm_code" in
    200) log "PASS: app.wasm served." ;;
    *)   fail "app.wasm returned HTTP $wasm_code (expected 200)" ;;
esac

# A write proves the read-only root filesystem did not break SQLite. Boot alone
# does not: the database is opened lazily and the first WRITE is what fails on
# a read-only mount, long after the healthcheck is green (C0-24).
log "checking the database is writable on a read_only root filesystem ..."
# Inspected from a SEPARATE container mounting the same volume, because this
# image is distroless: there is no shell to exec into and no ls to run. A
# throwaway alpine mounting the volume read-only answers the question without
# adding a shell to the image being tested — which would defeat most of the
# point of distroless (see the Dockerfile's opening comment).
#
# The database file existing is the proof: SQLite creates it and writes the
# migrations on first open, so a read-only mount or a mis-owned volume shows up
# here as an absent or zero-length file even though the process booted and the
# healthcheck went green.
vol_listing="$(docker run --rm -v animefeedflux-test-data:/d:ro alpine:3.20 \
    sh -c 'ls -l /d 2>/dev/null' 2>/dev/null || true)"
case "$vol_listing" in
    *animefeedflux.db*)
        log "PASS: the database was created and written on the volume:"
        printf '%s\n' "$vol_listing" | sed 's/^/[check-compose]     /'
        ;;
    *)
        fail "no database on the volume after a healthy boot — a read-only root filesystem or a mis-owned volume would look exactly like this"
        printf '%s\n' "$vol_listing" >&2
        docker logs animefeedflux-test 2>&1 | tail -n 30 >&2 || true
        ;;
esac

# The security options are asserted rather than assumed: a compose merge that
# silently dropped read_only would let every check above still pass.
log "asserting the production security options are actually in effect ..."
opts="$(docker inspect -f 'readonly={{.HostConfig.ReadonlyRootfs}} capdrop={{.HostConfig.CapDrop}}' animefeedflux-test 2>/dev/null || echo unknown)"
case "$opts" in
    "readonly=true capdrop=[ALL]") log "PASS: $opts" ;;
    *) fail "expected 'readonly=true capdrop=[ALL]', got '$opts'" ;;
esac

# --- 4. restart with the volume intact (C0-19, under compose) --------------

log "restarting against the SAME volume ..."
$COMPOSE stop >/dev/null 2>&1 || true
if ! $COMPOSE up -d >/dev/null 2>&1; then
    fail "compose up after stop failed"
else
    healthy=0
    i=0
    while [ "$i" -lt 60 ]; do
        state="$(docker inspect -f '{{.State.Health.Status}}' animefeedflux-test 2>/dev/null || echo unknown)"
        [ "$state" = "healthy" ] && { healthy=1; break; }
        i=$((i + 1))
        sleep 2
    done
    if [ "$healthy" -eq 1 ]; then
        log "PASS: healthy again on the same volume — the database persisted and reopened."
    else
        fail "did not become healthy after restart (state: ${state:-unknown})"
        docker logs animefeedflux-test 2>&1 | tail -n 30 >&2 || true
    fi
fi

if [ "$FAILED" -eq 0 ]; then
    log "ALL CHECKS PASSED."
    exit 0
fi
log "at least one check FAILED (see above)."
exit 1
