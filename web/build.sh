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
	GOOS=js GOARCH=wasm go build -trimpath -o "$scratch/app.wasm" ./web
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

# --- 4. Stage into the serve directory, one atomic replace per file. ---
mkdir -p "$serve_dir"
for f in app.wasm app.wasm.gz wasm_exec.js index.html; do
	tmp_target="$serve_dir/.$f.tmp.$$"
	cp "$scratch/$f" "$tmp_target"
	mv "$tmp_target" "$serve_dir/$f"
done

echo "web/build.sh: staged into $serve_dir"
ls -l "$serve_dir"
