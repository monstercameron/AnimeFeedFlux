#!/bin/sh
# rollback.sh — put the previous image back.
#
# Rollback is NOT a git operation. main is a pointer; production runs a pinned
# image tag. Reverting main would rebuild from source, which is slow, produces a
# new artefact, and is not what you want when production is down — you want the
# bytes that were working ten minutes ago.
#
# This re-deploys an existing immutable sha- tag. That is the whole reason
# §15.2 pins sha- tags and never deploys `latest`.
#
#   sh scripts/rollback.sh                 # roll back to the previously deployed tag
#   sh scripts/rollback.sh sha-abc1234     # roll back to a specific tag
#   sh scripts/rollback.sh --list          # show recent deployable tags

set -e

cd "$(git rev-parse --show-toplevel)"

REPO=$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null || echo "monstercameron/AnimeFeedFlux")

if [ "$1" = "--list" ]; then
    printf '\nRecent images:\n'
    gh api "users/monstercameron/packages/container/animefeedflux/versions" \
        --jq '.[0:15][] | "  \(.metadata.container.tags | join(", "))  \(.created_at)"' \
        2>/dev/null || printf '  (no packages yet, or not published)\n'
    printf '\nRecent commits on main (their images are sha-<short>):\n'
    git log --oneline -10 main | sed 's/^/  /'
    exit 0
fi

TAG="$1"

if [ -z "$TAG" ]; then
    # The deploy step records what it replaced. Prefer that over guessing.
    if [ -n "$AFF_DEPLOY_HOST" ] && [ -n "$AFF_DEPLOY_USER" ]; then
        TAG=$(ssh -o BatchMode=yes "$AFF_DEPLOY_USER@$AFF_DEPLOY_HOST" \
              'cat /opt/animefeedflux/.previous-tag 2>/dev/null' || echo "")
    fi
    if [ -z "$TAG" ]; then
        # Fall back to the commit before the tip of main.
        prev=$(git rev-parse --short main~1 2>/dev/null || echo "")
        [ -n "$prev" ] && TAG="sha-$prev"
    fi
fi

[ -n "$TAG" ] || { echo "could not determine a tag — pass one, or use --list" >&2; exit 1; }

case "$TAG" in
    latest)
        echo "refusing: 'latest' is not a rollback target, it is the thing you are rolling back from" >&2
        exit 1 ;;
esac

printf '\nRolling production back to: %s\n' "$TAG"
printf 'This redeploys an existing image. It does NOT change main.\n'
printf '\nType ROLLBACK to continue: '
read -r answer
[ "$answer" = "ROLLBACK" ] || { printf 'Aborted.\n'; exit 1; }

gh workflow run release.yml --repo "$REPO" --ref main -f image_tag="$TAG"

printf '\nTriggered. Watch it:\n  gh run watch --repo %s\n' "$REPO"
printf '\nAfterwards: main still points at the bad commit. Fix forward on dev and\n'
printf 'promote again — do not leave production and main disagreeing for long,\n'
printf 'because the next promotion will redeploy whatever main says.\n\n'
