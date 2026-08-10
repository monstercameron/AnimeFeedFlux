#!/bin/sh
# scripts/build-web.sh — canonical entry point for building the admin WASM
# bundle (TODOS.md D0-02/D0-03).
#
# Every other build/deploy script in this repo lives under scripts/
# (deploy-release.sh, deploy-bootstrap.sh, promote.sh, rollback.sh, ...), so
# CI and deploy tooling have one fixed address to call. The actual build —
# the isolated-scratch-dir compile, the precompression, and the atomic
# per-file replace into the serve directory — lives in web/build.sh, right
# next to the sources it compiles, because that script resolves its own
# paths relative to itself rather than the caller's working directory (see
# its header comment). This is a thin, argument- and env-forwarding wrapper,
# not a second implementation: keeping the logic in one place is the whole
# point of D0-02 (a second copy of the scratch-dir dance is exactly the kind
# of drift that reintroduces the concurrent-build race it exists to avoid).
#
# Usage:
#   scripts/build-web.sh                  # build, stage into web/dist/
#   SERVE_DIR=/path scripts/build-web.sh  # stage somewhere else instead
set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)

exec sh "$repo_root/web/build.sh" "$@"
