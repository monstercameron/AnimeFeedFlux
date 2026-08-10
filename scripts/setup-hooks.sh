#!/bin/sh
# Opt into this repository's hooks and commit template.
#
# Both settings live in .git/config, which is per-clone and NOT tracked, so a
# committed file cannot enable itself. That is why this script exists and why
# CONTRIBUTING.md tells you to run it once per clone rather than assuming the
# hooks are live. A hook everyone believes is running and isn't is worse than
# no hook.
#
#   sh scripts/setup-hooks.sh
#
# Verify with:  git config core.hooksPath   (expects: .githooks)

set -e

cd "$(git rev-parse --show-toplevel)"

git config core.hooksPath .githooks
git config commit.template .gitmessage

# Git for Windows does not always preserve the executable bit on checkout. The
# bit is recorded in the index (see the chmod +x in the commit that added these),
# but set it locally too so the hooks run from a fresh clone on any platform.
chmod +x .githooks/* 2>/dev/null || true

echo "hooks:    $(git config core.hooksPath)"
echo "template: $(git config commit.template)"
echo
echo "pre-commit runs build, vet and short unit tests when Go files are staged."
echo "pre-push refuses accidental pushes to main; promotion needs AFF_PROMOTE=1."
echo "Emergency bypass is AFF_SKIP_HOOKS=1, and a red test is not an emergency."
