#!/bin/sh
# promote.sh — move dev to main, deliberately.
#
# The flow this replaces was four commands typed from memory, and it had one
# real hole: nothing checked that dev was GREEN before it became production.
# "It passed when I pushed it" is not the same as "the tip of dev passes", and
# the gap between those two is where a broken promotion lives.
#
# This does the checks in the order that fails cheapest first, then hands over.
#
#   sh scripts/promote.sh            # verify and promote
#   sh scripts/promote.sh --check    # verify only, change nothing

set -e

cd "$(git rev-parse --show-toplevel)"

CHECK_ONLY=0
[ "$1" = "--check" ] && CHECK_ONLY=1

say()  { printf '  %s\n' "$1"; }
ok()   { printf '  ok    %s\n' "$1"; }
die()  { printf '  FAIL  %s\n' "$1" >&2; exit 1; }

printf '\nPromoting dev -> main\n\n'

# 1. Clean tree. Promoting with uncommitted work means the thing you tested is
#    not the thing you shipped.
[ -z "$(git status --porcelain)" ] || die "working tree is dirty — commit or stash first"
ok "working tree clean"

# 2. Up to date with the remote, or the ff-only merge below promotes a stale dev.
git fetch -q origin
local_dev=$(git rev-parse dev)
remote_dev=$(git rev-parse origin/dev 2>/dev/null || echo none)
[ "$local_dev" = "$remote_dev" ] || die "dev differs from origin/dev — push or pull first"
ok "dev matches origin/dev"

# 3. Something to promote.
count=$(git rev-list --count main..dev)
[ "$count" -gt 0 ] || die "main is already at dev — nothing to promote"
ok "$count commit(s) to promote"

# 4. Fast-forwardable. If this fails, main has something dev does not, which
#    means production is running code that never sat on the working branch.
git merge-base --is-ancestor main dev \
    || die "main has diverged from dev — reconcile before promoting, do not force"
ok "main is an ancestor of dev (fast-forward possible)"

# 5. CI is green at the exact commit being promoted. This is the check the
#    manual flow did not have.
if command -v gh >/dev/null 2>&1; then
    status=$(gh run list --branch dev --workflow CI --limit 1 \
             --json headSha,conclusion,status \
             --jq '.[0] | "\(.headSha) \(.status) \(.conclusion)"' 2>/dev/null || echo "")
    if [ -z "$status" ]; then
        say "warn  no CI run found for dev — cannot verify (is the workflow enabled?)"
    else
        sha=$(echo "$status" | cut -d' ' -f1)
        st=$(echo "$status" | cut -d' ' -f2)
        cc=$(echo "$status" | cut -d' ' -f3)
        [ "$sha" = "$local_dev" ] || die "latest CI run is for $sha, not the tip of dev ($local_dev)"
        [ "$st" = "completed" ] || die "CI is still $st for this commit — wait for it"
        [ "$cc" = "success" ] || die "CI concluded '$cc' for this commit — do not promote"
        ok "CI green at $(echo "$local_dev" | cut -c1-7)"
    fi
else
    say "warn  gh not installed — CI status unverified"
fi

# 6. Records. A promotion is a release, and a release with an empty changelog is
#    one nobody can read later.
if git diff --quiet "$(git rev-parse main)" dev -- CHANGELOG.md; then
    say "warn  CHANGELOG.md unchanged since the last promotion"
fi

if [ "$CHECK_ONLY" = "1" ]; then
    printf '\nAll checks passed. Nothing changed (--check).\n\n'
    exit 0
fi

printf '\nCommits being promoted:\n'
git log --oneline main..dev | sed 's/^/  /'

printf '\nPushing main DEPLOYS to production. Type PROMOTE to continue: '
read -r answer
[ "$answer" = "PROMOTE" ] || { printf 'Aborted.\n'; exit 1; }

start_branch=$(git rev-parse --abbrev-ref HEAD)
git checkout -q main
git merge --ff-only dev
AFF_PROMOTE=1 git push origin main
git checkout -q "$start_branch"

printf '\nPromoted. Watch the release workflow:\n'
printf '  gh run watch\n'
printf 'If it fails, roll back with:\n'
printf '  sh scripts/rollback.sh\n\n'
