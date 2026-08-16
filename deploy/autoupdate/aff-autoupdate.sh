#!/bin/sh
# aff-autoupdate.sh — deploy the newest v* release tag when it changes.
#
# The droplet's admin/SSH surface is IP-allowlisted to the home address, so
# GitHub's runners can never push a deploy in; updates are therefore PULLED
# from the box's side. This script is the whole mechanism: a systemd timer
# (aff-autoupdate.timer, installed by deploy-bootstrap.sh) runs it every ten
# minutes, it compares the newest v* tag on the repo against the tag pinned
# in /opt/animefeedflux/compose.yaml, and when they differ it hands off to
# deploy-release.sh — which does the actual pull/recreate AND blocks on the
# container's healthcheck, so a bad release fails loudly here (journalctl -u
# aff-autoupdate) rather than silently crash-looping.
#
# Releasing therefore stays exactly the repo's own ritual and nothing more:
# promote dev -> main, tag vX.Y.Z on main, push the tag. The Release
# workflow builds ghcr.io/monstercameron/animefeedflux:vX.Y.Z, and within
# ten minutes this box is running it. `latest` and sha- tags are ignored on
# purpose — §15.2 pins deploys to immutable, deliberate version tags.
#
# Idempotent and safe to run by hand: `sh /usr/local/bin/aff-autoupdate.sh`.
set -eu

SRC="${AFF_SRC_DIR:-/opt/animefeedflux-src}"
APP="${AFF_APP_DIR:-/opt/animefeedflux}"
IMAGE="ghcr.io/monstercameron/animefeedflux"

log() { printf '[aff-autoupdate] %s\n' "$1"; }

[ -d "$SRC/.git" ] || { log "ERROR: $SRC is not a git checkout — run deploy-bootstrap.sh"; exit 1; }
[ -f "$APP/compose.yaml" ] || { log "ERROR: $APP/compose.yaml missing — run deploy-bootstrap.sh"; exit 1; }

# Freshen the checkout first: deploy-release.sh (and this script) evolve with
# the repo, and an update must run the release procedure the RELEASE expects,
# not the one from whenever the box was bootstrapped.
git -C "$SRC" fetch -q --tags origin
git -C "$SRC" reset -q --hard origin/main

latest=$(git -C "$SRC" tag -l 'v*' --sort=-v:refname | head -n1)
if [ -z "$latest" ]; then
    log "no v* tags exist yet — nothing to deploy"
    exit 0
fi

current=$(grep -oE 'image:.*animefeedflux:[^[:space:]]+' "$APP/compose.yaml" | sed 's/^.*://' || true)
if [ "$current" = "$latest" ]; then
    log "up to date ($current)"
    exit 0
fi

# The tag can exist minutes before the Release workflow finishes publishing
# its image. Deploying into that window would fail the pull; skip quietly and
# let the next tick retry instead.
if ! docker manifest inspect "$IMAGE:$latest" >/dev/null 2>&1; then
    log "tag $latest exists but $IMAGE:$latest is not published yet — retrying next tick"
    exit 0
fi

log "updating: $current -> $latest"
exec sh "$SRC/scripts/deploy-release.sh" "$latest" "$APP"
