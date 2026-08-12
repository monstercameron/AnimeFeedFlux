#!/bin/sh
# web/build.sh — builds the admin WASM bundle and stages it for serving.
#
# TODOS.md D0-02/D0-03:
#   - "WASM build in an isolated scratch directory — the known
#     concurrent-build race." Building straight into the serve directory
#     means two builds running at once (a CI job and a manual rebuild, or
#     two overlapping CI runs) can interleave a partially-written .wasm
#     with a reader mid-request. This script instead builds entirely into
#     a fresh `mktemp -d` scratch directory that nothing else can be
#     writing into, and only touches the serve directory at the very end,
#     one atomic per-file replace at a time (temp-file-then-`mv`, which is
#     atomic on both the ext4/tmpfs targets this ships to and NTFS/git-bash
#     locally, since `mv` within the same filesystem is a rename, not a
#     copy).
#   - Emit `.wasm.gz` and serve it with the right Content-Encoding: this
#     script produces the .gz; internal/publish or cmd/animefeedflux (a
#     later wave, once the admin host is actually wired up — see
#     web/wsconn's DefaultEndpoint doc comment for the same "not wired yet"
#     note on the server side) is responsible for setting
#     Content-Encoding: gzip when it serves the file this script writes.
#
# Usage:
#   web/build.sh                  # build, stage into web/dist/
#   SERVE_DIR=/path web/build.sh  # stage somewhere else instead
#
# Run from anywhere; paths are resolved relative to this script's own
# location, not the caller's working directory.
set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)
serve_dir=${SERVE_DIR:-"$script_dir/dist"}

scratch=$(mktemp -d "${TMPDIR:-/tmp}/aff-web-build.XXXXXX")
cleanup() { rm -rf "$scratch"; }
trap cleanup EXIT INT TERM

echo "web/build.sh: building in isolated scratch dir $scratch"

# --- 1. Compile to WASM, entirely inside the scratch dir. ---
(
	cd "$repo_root"
	# DEV=1 builds the dev-only login prefill (web/pages/auth/devfill_on.go),
	# which needs a password and TOTP secret. They are passed by -ldflags, never
	# committed, and the whole path is behind a build tag so a release bundle
	# cannot contain them even by accident — scripts/check-wasm-secrets.sh greps
	# the built artifact for exactly this class of mistake.
	devtags=""
	devldflags=""
	if [ "${DEV:-}" = "1" ]; then
		if [ -z "${AFF_DEV_PASSWORD:-}" ]; then
			echo "web/build.sh: DEV=1 needs AFF_DEV_PASSWORD (and AFF_DEV_TOTP_SECRET)" >&2
			exit 2
		fi
		devtags="-tags devui"
		pkg="github.com/monstercameron/AnimeFeedFlux/web/pages/auth"
		# base64 because -ldflags word-splits on spaces and a passphrase has
		# them by design, and because it keeps the plaintext out of `ps`.
		pw_b64=$(printf %s "$AFF_DEV_PASSWORD" | base64 | tr -d '
')
		totp_b64=$(printf %s "${AFF_DEV_TOTP_SECRET:-}" | base64 | tr -d '
')
		devldflags="-X ${pkg}.devPasswordB64=${pw_b64} -X ${pkg}.devTOTPSecretB64=${totp_b64}"
		echo "web/build.sh: DEV BUILD — login form will be pre-filled. Never deploy this bundle."
	fi
	# -s -w strips the symbol table and DWARF (TODOS.md A8-49). Every other
	# performance item in this codebase is worth microseconds per render; this
	# is worth seconds on a cold load over a slow connection, which is the only
	# performance number an operator actually experiences.
	#
	# The cost is worse stack traces, and it is smaller here than it looks:
	# Go/wasm panics already surface poorly through the JS glue, and this app's
	# real diagnostics are server-side. A dev build keeps its symbols, because
	# that is the build where a trace is worth reading.
	stripflags="-s -w"
	if [ "${DEV:-}" = "1" ]; then
		stripflags=""
	fi
	# shellcheck disable=SC2086  # devtags/devldflags/stripflags are intentionally word-split
	GOOS=js GOARCH=wasm go build -trimpath $devtags -ldflags "$stripflags $devldflags" -o "$scratch/app.wasm" ./web
)

# --- 2. Gzip it (also inside the scratch dir). ---
gzip -9 -k -f "$scratch/app.wasm"
# gzip -k leaves app.wasm; app.wasm.gz is the compressed sibling.

# --- 3. Copy in the Go-distribution glue script and the HTML shell. ---
goroot=$(go env GOROOT)
wasm_exec="$goroot/lib/wasm/wasm_exec.js"
if [ ! -f "$wasm_exec" ]; then
	# Go < 1.24 shipped this at a different path; fall back rather than
	# failing outright on an older toolchain.
	wasm_exec="$goroot/misc/wasm/wasm_exec.js"
fi
if [ ! -f "$wasm_exec" ]; then
	echo "web/build.sh: cannot find wasm_exec.js under GOROOT ($goroot)" >&2
	exit 1
fi
cp "$wasm_exec" "$scratch/wasm_exec.js"
cp "$script_dir/static/index.html" "$scratch/index.html"

# The brand icons: docs/design-direction.md's brand assets. Staged
# alongside the bundle the same way index.html is — copied into the
# isolated scratch dir first, then atomic-replaced into the serve dir below
# — so a concurrent build/reader can never observe a half-written file, the
# same guarantee D0-02/D0-03 give the wasm/gzip/html trio above. They are
# small, static, and never change per-build (unlike app.wasm), but nothing
# about this script's staging discipline is worth special-casing for that.
# Copied out of internal/brand, which is where the artwork actually lives —
# the server binary embeds the same files from there (see that package's doc
# comment). Staging from one source rather than keeping a second copy under
# web/static is what stops the tab icon and the publish plane's
# /favicon.ico from silently becoming two different logos.
for icon in favicon-32.png favicon-180.png favicon-512.png favicon.ico og-default.png; do
	cp "$repo_root/internal/brand/$icon" "$scratch/$icon"
done

# --- 4. Stage into the serve directory, one atomic replace per file. ---
mkdir -p "$serve_dir"
for f in app.wasm app.wasm.gz wasm_exec.js index.html \
	favicon-32.png favicon-180.png favicon-512.png favicon.ico og-default.png; do
	tmp_target="$serve_dir/.$f.tmp.$$"
	cp "$scratch/$f" "$tmp_target"
	mv "$tmp_target" "$serve_dir/$f"
done

echo "web/build.sh: staged into $serve_dir"
ls -l "$serve_dir"
