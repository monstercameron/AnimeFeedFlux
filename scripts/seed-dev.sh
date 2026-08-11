#!/bin/sh
# scripts/seed-dev.sh — builds and runs cmd/affseed, the LOCAL-ONLY,
# DEV-ONLY seeder, against the dev database.
#
# Every other build/deploy script in this repo lives under scripts/ and
# resolves its own paths relative to itself, not the caller's working
# directory (see web/build.sh's header comment, which this script's layout
# mirrors: an isolated `mktemp -d` scratch dir for the build, cleaned up via
# a trap, so a concurrent `go build` of the same package elsewhere in the
# repo can never interleave with this one's binary). Unlike web/build.sh
# there is no separate `cmd/affseed/build.sh` to delegate to — the whole job
# here is "build one Go binary, run it once" — so the build and the run both
# live in this one wrapper.
#
# What this actually does:
#   1. Loads the dev environment from .devrun/env.sh, if present, exactly
#      the same file `aff`/the admin server already source from
#      (AFF_DB_PATH, AFF_SECRET_KEY, etc.) — so this script's notion of
#      "the dev database" always matches the running dev server's.
#   2. Builds cmd/affseed into an isolated scratch dir.
#   3. Runs it against $AFF_DB_PATH, forwarding any extra arguments (e.g.
#      --force) straight through.
#
# Usage:
#   scripts/seed-dev.sh            # seed .devrun/aff.db (refuses if it
#                                   # already has feeds)
#   scripts/seed-dev.sh --force    # seed on top of existing feeds anyway
set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)

cd "$repo_root"

if [ -f .devrun/env.sh ]; then
	# shellcheck disable=SC1091
	. ./.devrun/env.sh
fi

: "${AFF_DB_PATH:?AFF_DB_PATH is not set — source .devrun/env.sh or export it yourself before running this script}"

scratch=$(mktemp -d "${TMPDIR:-/tmp}/aff-seed-build.XXXXXX")
cleanup() { rm -rf "$scratch"; }
trap cleanup EXIT INT TERM

echo "scripts/seed-dev.sh: building cmd/affseed in isolated scratch dir $scratch"
go build -o "$scratch/affseed" ./cmd/affseed

echo "scripts/seed-dev.sh: seeding $AFF_DB_PATH"
"$scratch/affseed" -db "$AFF_DB_PATH" "$@"
