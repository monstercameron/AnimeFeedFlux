#!/bin/sh
# deploy-release.sh — pin an image tag and roll it out (PLAN.md §15.2-15.3,
# TODOS C1-11/C1-12).
#
# This is what a deploy job invokes over SSH, on the droplet, in
# /opt/animefeedflux: write the tag into compose.yaml, pull, recreate, then —
# the part a plain `docker compose up -d` does NOT give you — block until the
# container's own healthcheck says healthy, and fail loudly if it never does.
# Without that gate the caller (a CI job, or a human at a terminal) sees exit
# 0 and believes the release worked while the service crash-loops behind it
# (§15.3). "docker compose up -d succeeded" and "the app is up" are different
# claims; this script only makes the second one.
#
#   sh scripts/deploy-release.sh sha-abc1234
#   sh scripts/deploy-release.sh sha-abc1234 /opt/animefeedflux   # explicit app dir
#
# Also used for rollback: pass any previously-built sha- tag and it does
# exactly the same thing, because a rollback IS a release to an older tag
# (scripts/rollback.sh drives this indirectly via the release workflow).

set -eu

TAG="${1:-}"
APP_DIR="${2:-/opt/animefeedflux}"
HEALTH_RETRIES="${AFF_HEALTH_RETRIES:-30}"   # 30 * 10s = 5 minutes, matches the CI job's budget
HEALTH_INTERVAL="${AFF_HEALTH_INTERVAL:-10}"
CONTAINER_NAME="${AFF_CONTAINER_NAME:-animefeedflux}"

log() { printf '[deploy-release] %s\n' "$1"; }
die() { printf '[deploy-release] ERROR: %s\n' "$1" >&2; exit 1; }

[ -n "$TAG" ] || die "usage: deploy-release.sh <image-tag> [app-dir] — no tag given"

# `latest` is never a deploy target (§15.2): it is a moving pointer, and
# pinning it defeats the entire point of immutable sha- tags — "what is
# running" would stop being answerable the moment `latest` moves again.
case "$TAG" in
    latest) die "refusing to deploy 'latest' — pin an immutable sha-<commit> or v<semver> tag" ;;
esac

[ -d "$APP_DIR" ] || die "$APP_DIR does not exist — run deploy-bootstrap.sh first"
COMPOSE_FILE="$APP_DIR/compose.yaml"
[ -f "$COMPOSE_FILE" ] || die "$COMPOSE_FILE missing — run deploy-bootstrap.sh first"

cd "$APP_DIR"

log "deploying tag: $TAG"

# --- 1. record what's currently running, for rollback ----------------------
#
# rollback.sh (unmodified, do not touch) reads this file over SSH when no
# explicit tag is given. Written BEFORE the compose edit so a deploy that
# fails partway still leaves an accurate "what was running before this
# attempt" behind, not "what this attempt tried to become".
if [ -f "$COMPOSE_FILE" ]; then
    prev=$(grep -oE 'image:.*animefeedflux:[^[:space:]]+' "$COMPOSE_FILE" | sed 's/^.*://' || true)
    if [ -n "$prev" ] && [ "$prev" != "$TAG" ]; then
        echo "$prev" > "$APP_DIR/.previous-tag"
        log "recorded previous tag for rollback: $prev"
    fi
fi

# --- 2. write the new tag into compose.yaml ---------------------------------

# In-place edit of the pinned tag only. Anchored on the image name so a stray
# `image:` line elsewhere in the file (there is only one service today, but
# this must not silently rewrite the wrong line if that changes) is never
# touched.
sed -i "s|\(image:[[:space:]]*ghcr\.io/[^:]*animefeedflux\):.*|\1:${TAG}|" "$COMPOSE_FILE"

if ! grep -q "animefeedflux:${TAG}\$" "$COMPOSE_FILE" && ! grep -q "animefeedflux:${TAG}[[:space:]]" "$COMPOSE_FILE"; then
    die "failed to write tag $TAG into $COMPOSE_FILE — check the image: line format"
fi
log "compose.yaml now pins $TAG"

# --- 3. pull and recreate ---------------------------------------------------

log "pulling image..."
docker compose -f "$COMPOSE_FILE" pull

log "recreating container..."
# --remove-orphans: this compose file is the only source of truth for what
# should be running in this project; anything else left behind by an older
# compose.yaml shape should not silently keep running.
docker compose -f "$COMPOSE_FILE" up -d --remove-orphans

# --- 4. wait on the healthcheck ---------------------------------------------
#
# SQLite has exactly one writer (§14.5, §15.3): compose's default recreate
# order stops the old container before starting the new one, so there is a
# brief real gap here, not overlap. That gap is why this loop tolerates
# "container not found yet" for the first couple of iterations rather than
# failing immediately.
log "waiting for healthy (up to $((HEALTH_RETRIES * HEALTH_INTERVAL))s)..."
i=0
while [ "$i" -lt "$HEALTH_RETRIES" ]; do
    status=$(docker inspect -f '{{.State.Health.Status}}' "$CONTAINER_NAME" 2>/dev/null || echo "starting")
    case "$status" in
        healthy)
            log "healthy after $((i * HEALTH_INTERVAL))s"
            log "deploy of $TAG complete"
            exit 0
            ;;
        unhealthy)
            log "container reported UNHEALTHY — last 50 log lines:"
            docker compose -f "$COMPOSE_FILE" logs --tail=50 || true
            die "release $TAG never became healthy (unhealthy) — service is NOT confirmed up"
            ;;
    esac
    i=$((i + 1))
    sleep "$HEALTH_INTERVAL"
done

log "timed out waiting for healthy — last 50 log lines:"
docker compose -f "$COMPOSE_FILE" logs --tail=50 || true
die "release $TAG never became healthy within $((HEALTH_RETRIES * HEALTH_INTERVAL))s — treat this release as FAILED, consider scripts/rollback.sh"
