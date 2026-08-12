#!/bin/sh
# Coverage for the browser half of this application.
#
# `go test ./...` on the host does not skip web/'s browser code because it is
# untested — it skips it because it cannot see it. 62 files carry
# `//go:build js && wasm`, so a host build excludes them from the package
# entirely, and they contribute neither statements nor misses to any host
# coverage profile. The effect is a number that looks reassuring and is
# measuring something else: web/pages/settings reported 81.7% on the host
# while its actual browser code — every render function, every control, every
# panel — sat at 15.5%.
#
# This script runs the same packages under GOOS=js GOARCH=wasm, which is the
# only way those files are compiled at all, and writes a normal Go coverage
# profile for them.
#
# Usage:
#   scripts/coverage-wasm.sh [outfile]      # default: cover-wasm.out
#
# Requires node (the same one Playwright already needs).
#
# # Two Windows-specific workarounds, both load-bearing
#
#  1. GOROOT/lib/wasm/go_js_wasm_exec is a bash script, and `go test -exec`
#     needs something the OS can execute directly. On Windows that means a
#     .cmd shim, generated below, rather than the shipped wrapper.
#  2. The wasm process cannot GENERATE the coverage report itself: Go's js
#     syscall layer has no O_DIRECTORY, so the in-process step that reads back
#     the counter directory fails with "O_DIRECTORY is not supported on
#     Windows" — after having successfully WRITTEN every counter file. So this
#     asks for raw counters via -test.gocoverdir and converts them on the host
#     with `go tool covdata textfmt`, which sidesteps the read entirely. The
#     `go test` invocations are therefore EXPECTED to report FAIL on that step
#     alone; real test failures are detected separately below.
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
out=${1:-cover-wasm.out}
covdir="$repo_root/.covdata-wasm"

command -v node >/dev/null 2>&1 || {
	echo "coverage-wasm: node is required to run wasm tests" >&2
	exit 1
}

goroot=$(go env GOROOT)
shim="$covdir/go_js_wasm_exec.cmd"

rm -rf "$covdir"
mkdir -p "$covdir"

case "$(uname -s 2>/dev/null || echo unknown)" in
MINGW* | MSYS* | CYGWIN* | Windows_NT)
	printf '@echo off\r\nnode "%s\\lib\\wasm\\wasm_exec_node.js" %%*\r\n' "$goroot" >"$shim"
	exec_wrapper="$shim"
	;;
*)
	exec_wrapper="$goroot/lib/wasm/go_js_wasm_exec"
	;;
esac

status=0
for pkg in $(GOOS=js GOARCH=wasm go list ./web/... 2>/dev/null); do
	short=${pkg#github.com/monstercameron/AnimeFeedFlux/}
	log=$(GOOS=js GOARCH=wasm go test -exec="$exec_wrapper" -cover "./$short/" \
		-args -test.gocoverdir="$covdir" 2>&1 || true)

	# Distinguish a real test failure from the expected report-generation
	# failure described in the header. Only the former is a problem.
	if printf '%s\n' "$log" | grep -qE '^--- FAIL|^panic:'; then
		echo "FAIL $short"
		printf '%s\n' "$log" | grep -E '^--- FAIL|^panic:' | sed 's/^/      /'
		status=1
	else
		echo "ok   $short"
	fi
done

go tool covdata textfmt -i="$covdir" -o="$repo_root/$out"
echo ""
echo "wasm coverage profile: $out"
go tool cover -func="$repo_root/$out" | tail -1

exit $status
