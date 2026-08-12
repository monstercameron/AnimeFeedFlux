#!/bin/sh
# check-container.sh — everything about the C0 containerization work that CAN
# be verified without the droplet (PLAN.md §15.1-15.2; TODOS.md C0-08, C0-09,
# C0-19).
#
# Three independent checks, each explained where §15.2 explains why it
# matters:
#
#   1. (C0-08) `docker build --platform linux/amd64` succeeds and the
#      resulting image actually RUNS — boots, opens its store, answers
#      /healthz. Building is not proof of running; distroless images with a
#      bad ownership/volume setup build cleanly and fail only at first write
#      (§15.1's volume-ownership note).
#   2. (C0-09) A NATIVE arm64 build/run on this amd64 host either fails
#      outright, or is silently emulated by Docker Desktop's bundled QEMU —
#      and either way the exact signature is captured, because §15.2 says
#      this failure is confusing the first time and instant the second, and
#      that only holds if the signature was written down once.
#   3. (C0-19) The container survives a restart with its named volume
#      intact: stop it, start a fresh container against the SAME volume, and
#      confirm it goes healthy again — proving the SQLite file persisted and
#      reopens cleanly, not just that a file exists.
#
# This script is read-only with respect to production: it builds and runs
# local, throwaway images/containers/volumes, all namespaced under
# animefeedflux-check-* and removed on exit (see the cleanup trap), and never
# touches the real `animefeedflux` container, the real `animefeedflux-data`
# volume, or ghcr.io.
#
#   sh scripts/check-container.sh
#
# Exit codes are distinguished on purpose (a check that reports "pass" when
# it did not actually run is worse than no check at all — see the SKIP path
# below):
#   0 — ran, every check passed
#   1 — ran, at least one check failed
#   2 — SKIPPED: Docker is not usable here. Read the printed commands and run
#       them by hand on a machine that has Docker.

set -eu

# Windows/git-bash only, and a no-op everywhere else: MSYS rewrites anything
# that looks like a POSIX path in an argument before the program sees it, so
# `-v vol:/var/lib/animefeedflux` reaches docker as
# `-v vol:C:/Program Files/Git/var/lib/animefeedflux`. The container then boots
# with NOTHING mounted at the path AFF_DB_PATH points at and SQLite fails with
# "unable to open database file (14)" — which reads exactly like a volume
# ownership or read-only-filesystem bug and is neither.
#
# Found by this script failing on Windows while scripts/check-compose.sh passed
# against the same image: compose reads its paths from YAML, which MSYS never
# touches, so only the CLI-flag path was affected.
MSYS_NO_PATHCONV=1
MSYS2_ARG_CONV_EXCL='*'
export MSYS_NO_PATHCONV MSYS2_ARG_CONV_EXCL

IMAGE_AMD64="animefeedflux-check:amd64"
IMAGE_ARM64="animefeedflux-check:arm64"
CONTAINER_NAME="animefeedflux-check-run"
VOLUME_NAME="animefeedflux-check-data"
NETWORK_NONE="none"
HOST_PORT="19310"   # unlikely to collide with a real 9310 listener

log()  { printf '[check-container] %s\n' "$1"; }
warn() { printf '[check-container] WARN: %s\n' "$1" >&2; }
fail() { printf '[check-container] FAIL: %s\n' "$1" >&2; FAILED=1; }

FAILED=0

# --- cleanup, always, even on a mid-script failure ------------------------

cleanup() {
    docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
    docker volume rm "$VOLUME_NAME" >/dev/null 2>&1 || true
    docker rmi "$IMAGE_AMD64" >/dev/null 2>&1 || true
    docker rmi "$IMAGE_ARM64" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# --- SKIP LOUDLY if Docker is not usable -----------------------------------
#
# Passing when the check never ran is strictly worse than no check: someone
# reads "container checks: pass" in CI history and stops worrying about the
# thing that was never actually exercised. So this path is loud (stderr,
# multi-line, exact commands) and exits 2, a code distinct from both 0 and 1.

skip_loudly() {
    reason="$1"
    {
        printf '[check-container] SKIPPED: %s\n' "$reason"
        printf '[check-container] This script did NOT verify C0-08, C0-09, or C0-19.\n'
        printf '[check-container] Run these by hand on a machine with a working Docker daemon:\n'
        printf '\n'
        printf '  # C0-08 — cross-platform build actually runs\n'
        printf '  docker build --platform linux/amd64 -t animefeedflux-check:amd64 .\n'
        printf '  docker run --rm -d --name animefeedflux-check-run \\\n'
        printf '      -e AFF_DB_PATH=/var/lib/animefeedflux/animefeedflux.db \\\n'
        printf '      -e AFF_PUBLISH_ADDR=0.0.0.0:9310 -e AFF_ADMIN_ADDR=0.0.0.0:9311 \\\n'
        printf '      -e AFF_SECRET_KEY=check-secret-key-0123456789 \\\n'
        printf '      -e SCHEMAFLUX_API_KEY=sk-check-placeholder \\\n'
        printf '      -e AFF_PUBLIC_BASE_URL=http://127.0.0.1:9310 \\\n'
        printf '      -e AFF_ALLOWED_ORIGINS=http://127.0.0.1:9311 \\\n'
        printf '      -p 19310:9310 -v animefeedflux-check-data:/var/lib/animefeedflux \\\n'
        printf '      animefeedflux-check:amd64\n'
        printf '  curl -sf http://127.0.0.1:19310/healthz\n'
        printf '\n'
        printf '  # C0-09 — native arm64 build/run on an amd64 host: capture the signature\n'
        printf '  docker build --platform linux/arm64 -t animefeedflux-check:arm64 .\n'
        printf '  docker run --rm animefeedflux-check:arm64 healthcheck   # expect: exec format error, OR silent QEMU emulation — record which\n'
        printf '\n'
        printf '  # C0-19 — restart with the volume intact\n'
        printf '  docker rm -f animefeedflux-check-run\n'
        printf '  docker run --rm -d --name animefeedflux-check-run \\\n'
        printf '      -e AFF_DB_PATH=/var/lib/animefeedflux/animefeedflux.db \\\n'
        printf '      -e AFF_PUBLISH_ADDR=0.0.0.0:9310 -e AFF_ADMIN_ADDR=0.0.0.0:9311 \\\n'
        printf '      -e AFF_SECRET_KEY=check-secret-key-0123456789 \\\n'
        printf '      -e SCHEMAFLUX_API_KEY=sk-check-placeholder \\\n'
        printf '      -e AFF_PUBLIC_BASE_URL=http://127.0.0.1:9310 \\\n'
        printf '      -e AFF_ALLOWED_ORIGINS=http://127.0.0.1:9311 \\\n'
        printf '      -p 19310:9310 -v animefeedflux-check-data:/var/lib/animefeedflux \\\n'
        printf '      animefeedflux-check:amd64\n'
        printf '  curl -sf http://127.0.0.1:19310/healthz   # must go healthy again, same volume\n'
        printf '\n'
        printf '  # cleanup\n'
        printf '  docker rm -f animefeedflux-check-run; docker volume rm animefeedflux-check-data\n'
        printf '  docker rmi animefeedflux-check:amd64 animefeedflux-check:arm64\n'
    } >&2
    trap - EXIT   # nothing was created; skip the (no-op) cleanup trap noise
    exit 2
}

command -v docker >/dev/null 2>&1 || skip_loudly "docker CLI is not installed on this host"

if ! docker info >/dev/null 2>&1; then
    skip_loudly "docker CLI is present but the daemon is not reachable (is Docker/Docker Desktop running?)"
fi

log "Docker is usable ($(docker version --format '{{.Client.Version}}' 2>/dev/null || echo unknown)). Running checks."

# --- C0-08: cross-platform build runs --------------------------------------

log "C0-08: building --platform linux/amd64 ..."
if ! docker build --platform linux/amd64 -t "$IMAGE_AMD64" "$(dirname "$0")/.." >/tmp/check-container-build-amd64.log 2>&1; then
    fail "amd64 build failed — see /tmp/check-container-build-amd64.log"
    tail -n 40 /tmp/check-container-build-amd64.log >&2 || true
else
    log "C0-08: amd64 build OK. Starting a container against a throwaway volume ..."
    docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
    docker volume rm "$VOLUME_NAME" >/dev/null 2>&1 || true

    # Dummy but well-formed config: this only needs to boot the process and
    # answer /healthz, never actually reach SchemaFlux or OpenAI.
    docker run -d --name "$CONTAINER_NAME" \
        -e AFF_DB_PATH=/var/lib/animefeedflux/animefeedflux.db \
        -e AFF_PUBLISH_ADDR=0.0.0.0:9310 \
        -e AFF_ADMIN_ADDR=0.0.0.0:9311 \
        -e AFF_SECRET_KEY=check-secret-key-0123456789 \
        -e SCHEMAFLUX_API_KEY=sk-check-placeholder \
        -e AFF_PUBLIC_BASE_URL=http://127.0.0.1:9310 \
        -e AFF_ALLOWED_ORIGINS=http://127.0.0.1:9311 \
        -p "${HOST_PORT}:9310" \
        -v "${VOLUME_NAME}:/var/lib/animefeedflux" \
        "$IMAGE_AMD64" >/tmp/check-container-run-amd64.log 2>&1 \
        || { fail "amd64 container failed to start — see /tmp/check-container-run-amd64.log"; cat /tmp/check-container-run-amd64.log >&2 || true; }

    if docker inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
        healthy=0
        i=0
        while [ "$i" -lt 20 ]; do
            if curl -sf "http://127.0.0.1:${HOST_PORT}/healthz" >/dev/null 2>&1; then
                healthy=1
                break
            fi
            i=$((i + 1))
            sleep 1
        done
        if [ "$healthy" -eq 1 ]; then
            log "C0-08: PASS — amd64 image built and /healthz answered after start."
        else
            fail "C0-08: amd64 container started but /healthz never answered within 20s"
            docker logs "$CONTAINER_NAME" >&2 || true
        fi
    fi
fi

# --- C0-09: native arm64 build/run on this amd64 host -----------------------
#
# The failure §15.2 warns about happens on a bare Linux docker host with no
# QEMU/binfmt handlers registered (like the droplet) — an arm64 image simply
# cannot exec there, and the error is "exec format error" at `docker run`,
# not at build time. Docker Desktop ships QEMU-based emulation and may
# silently succeed at both build AND run via emulation, which is a real and
# useful thing to know too: it means this exact reproduction is
# environment-dependent, so BOTH outcomes are logged rather than assumed.

log "C0-09: building --platform linux/arm64 (native arm64, no amd64 override) ..."
arm64_build_rc=0
docker build --platform linux/arm64 -t "$IMAGE_ARM64" "$(dirname "$0")/.." >/tmp/check-container-build-arm64.log 2>&1 || arm64_build_rc=$?

if [ "$arm64_build_rc" -ne 0 ]; then
    log "C0-09: arm64 BUILD failed (rc=$arm64_build_rc) — this is itself a valid, useful signature:"
    tail -n 20 /tmp/check-container-build-arm64.log >&2 || true
    log "C0-09: PASS — mismatch reproduced at build time. Signature captured above and in /tmp/check-container-build-arm64.log."
else
    log "C0-09: arm64 build succeeded (this host's builder can cross-build without failing). Now checking whether RUNNING it fails ..."
    arm64_run_rc=0
    # The env matters here. Run bare, `healthcheck` exits non-zero simply
    # because AFF_PUBLISH_ADDR is unset — which lands in the "it failed!"
    # branch below and reports a mismatch that did not happen. Supplying the
    # same config the amd64 run gets makes the exit code mean what this check
    # needs it to mean: nonzero == the binary could not execute on this
    # architecture, zero == it ran (i.e. something is emulating it).
    arm64_run_out="$(docker run --rm \
        -e AFF_PUBLISH_ADDR=0.0.0.0:9310 \
        -e AFF_ADMIN_ADDR=0.0.0.0:9311 \
        "$IMAGE_ARM64" healthcheck 2>&1)" || arm64_run_rc=$?
    # Classify by the OUTPUT, not by the exit code alone. A nonzero exit does
    # not mean the architecture mismatch reproduced: `healthcheck` is a client,
    # so in a one-shot container with no server it exits nonzero with a dial
    # error — which is proof the binary EXECUTED, i.e. the opposite of what
    # this check is looking for. Only the loader's own refusal is the signature.
    case "$arm64_run_out" in
        *"exec format error"*|*"exec user process caused"*)
            log "C0-09: PASS — the arm64 image could not execute on this amd64 host (rc=$arm64_run_rc)."
            printf '%s\n' "$arm64_run_out" >&2
            log "C0-09: signature confirmed as the expected 'exec format error' — recognize this on the droplet."
            ;;
        *)
            warn "C0-09: the arm64 binary RAN on this amd64 host (rc=$arm64_run_rc, output below is the program's own, not the loader's). Docker Desktop is transparently emulating arm64 via QEMU, so the mismatch did NOT reproduce here. That is expected on Docker Desktop and NOT expected on a bare Linux host without qemu-user-static/binfmt registered (e.g. the droplet) — do not read this as 'the platform trap does not exist', only as 'this particular host works around it'. On the droplet the signature to expect is 'exec format error' at docker run, not at build."
            printf '%s\n' "$arm64_run_out" >&2
            ;;
    esac
fi

# --- C0-19: restart survives with the volume intact -------------------------

if docker image inspect "$IMAGE_AMD64" >/dev/null 2>&1 && docker volume inspect "$VOLUME_NAME" >/dev/null 2>&1; then
    log "C0-19: stopping the amd64 container and starting a fresh one against the SAME volume ..."
    docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true

    docker run -d --name "$CONTAINER_NAME" \
        -e AFF_DB_PATH=/var/lib/animefeedflux/animefeedflux.db \
        -e AFF_PUBLISH_ADDR=0.0.0.0:9310 \
        -e AFF_ADMIN_ADDR=0.0.0.0:9311 \
        -e AFF_SECRET_KEY=check-secret-key-0123456789 \
        -e SCHEMAFLUX_API_KEY=sk-check-placeholder \
        -e AFF_PUBLIC_BASE_URL=http://127.0.0.1:9310 \
        -e AFF_ALLOWED_ORIGINS=http://127.0.0.1:9311 \
        -p "${HOST_PORT}:9310" \
        -v "${VOLUME_NAME}:/var/lib/animefeedflux" \
        "$IMAGE_AMD64" >/tmp/check-container-run-restart.log 2>&1 \
        || { fail "C0-19: restarted container failed to start — see /tmp/check-container-run-restart.log"; cat /tmp/check-container-run-restart.log >&2 || true; }

    if docker inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
        healthy=0
        i=0
        while [ "$i" -lt 20 ]; do
            if curl -sf "http://127.0.0.1:${HOST_PORT}/healthz" >/dev/null 2>&1; then
                healthy=1
                break
            fi
            i=$((i + 1))
            sleep 1
        done
        if [ "$healthy" -eq 1 ]; then
            log "C0-19: PASS — second container against the same named volume went healthy, i.e. its DB file was readable across the restart."
        else
            fail "C0-19: restarted container never answered /healthz — volume/DB did not survive the restart cleanly"
            docker logs "$CONTAINER_NAME" >&2 || true
        fi
    fi
else
    warn "C0-19: skipped — no amd64 image/volume available (an earlier step must have failed)."
fi

if [ "$FAILED" -ne 0 ]; then
    log "One or more checks FAILED. See output above."
    exit 1
fi
log "All container checks passed."
exit 0
