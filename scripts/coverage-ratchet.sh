#!/bin/sh
# Coverage ratchet, not a target (PLAN.md §17.2): the number may hold steady
# or improve, but it may not go down. Chasing a target percentage produces
# tests written to touch lines rather than assert behaviour — a real gain in
# confidence comes from testing behaviour, and a percentage target rewards
# padding coverage instead. A ratchet sidesteps that: it only stops erosion,
# it never demands the number climb.
#
# Usage: scripts/coverage-ratchet.sh <coverage-profile>
#
# Reads the stored baseline from .github/coverage-baseline.txt, computes
# current total coverage from the given `go test -coverprofile` output, and
# fails if current coverage is more than TOLERANCE percentage points below
# the baseline. TOLERANCE exists to absorb noise from randomised test
# ordering (-shuffle=on) and the odd untested branch that shifts the total
# by a fraction of a point — not to forgive a real regression.
#
# What is MEASURED matters as much as the threshold. Since 2026-08-11 the
# profile this reads is built over `go list ./... | grep -v /gen/`, not
# `./...`: gen/aff/v1 is protoc output, ~3,600 statements nobody wrote, and
# including it reported the repository at 62% while the hand-written code sat
# at 79%. That gap is not a coverage problem, it is a measurement problem, and
# the only way to close it by writing tests would be a reflection walk calling
# 1,191 generated getters — the exact "tests written to touch lines rather
# than assert behaviour" this file's opening paragraph exists to reject. See
# the Makefile's COVERPKG comment and the CI workflow's Coverage ratchet step,
# which must stay in agreement with each other or the ratchet compares two
# different populations.
#
# If current coverage is HIGHER than the baseline, this only prints the new
# number and how to adopt it. It never rewrites the baseline file itself: a
# ratchet that silently raises its own floor on every green run would just
# as silently let that floor slide back down on the next slightly-worse run,
# which stops it being a ratchet at all. Raising the floor is a deliberate,
# reviewable, human action.

set -eu

TOLERANCE=0.5

profile="${1:-cover.out}"
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
baseline_file="$script_dir/../.github/coverage-baseline.txt"

if [ ! -f "$profile" ]; then
  echo "::error::coverage profile not found: $profile" >&2
  exit 1
fi

if [ ! -f "$baseline_file" ]; then
  echo "::error::no baseline at $baseline_file — seed it by running this script's" \
       "current-coverage computation once and committing the number" >&2
  exit 1
fi

baseline=$(tr -d '[:space:]' < "$baseline_file")
current=$(go tool cover -func="$profile" | tail -1 | awk '{print $3}' | tr -d '%')

if [ -z "$current" ]; then
  echo "::error::could not read total coverage from $profile" >&2
  exit 1
fi

echo "coverage: current=${current}% baseline=${baseline}% tolerance=${TOLERANCE}pp"

below=$(awk -v c="$current" -v b="$baseline" -v t="$TOLERANCE" \
  'BEGIN { print (c < (b - t)) ? "1" : "0" }')

if [ "$below" = "1" ]; then
  echo "::error::coverage dropped to ${current}%, more than ${TOLERANCE}pp below the ${baseline}% baseline in .github/coverage-baseline.txt"
  exit 1
fi

improved=$(awk -v c="$current" -v b="$baseline" 'BEGIN { print (c > b) ? "1" : "0" }')
if [ "$improved" = "1" ]; then
  echo "::notice::coverage improved to ${current}% (baseline is ${baseline}%)."
  echo "To raise the floor, edit .github/coverage-baseline.txt to ${current} by hand in its own commit — this script will not do it for you."
fi

echo "coverage ratchet OK"
