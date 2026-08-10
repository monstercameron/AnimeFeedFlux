#!/bin/sh
# Sanity-check the git hooks in a throwaway repo.
#
# Hooks are code, and untested hooks rot silently — the failure mode is a guard
# everyone believes is running that has quietly stopped catching anything. Run
# this after touching .githooks/:
#
#   sh scripts/test-hooks.sh
SRC=$(git rev-parse --show-toplevel)
T=$(mktemp -d)
pass=0; fail=0

ck() { # ck <desc> <expected-exit> <actual-exit>
  if [ "$2" = "$3" ]; then pass=$((pass+1)); printf 'PASS  %s\n' "$1"
  else fail=$((fail+1)); printf 'FAIL  %s (expected exit %s, got %s)\n' "$1" "$2" "$3"; fi
}

cd "$T" || exit 1
git init -q -b main .
git config user.email t@t; git config user.name t
mkdir -p .githooks
cp "$SRC/.githooks/pre-commit" "$SRC/.githooks/pre-push" .githooks/
chmod +x .githooks/*
echo doc > README.md
git add -A; git commit -qm "base"
git branch dev; git checkout -q dev

echo "--- pre-commit ---"

echo "hello" > a.txt; git add a.txt
sh .githooks/pre-commit >/dev/null 2>&1; ck "plain file allowed" 0 $?
git reset -q; rm a.txt

printf 'SCHEMAFLUX_API_KEY=sk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n' > s.txt; git add -f s.txt
sh .githooks/pre-commit >/dev/null 2>&1; ck "assigned key blocked" 1 $?
git reset -q; rm s.txt

printf 'Set SCHEMAFLUX_API_KEY in the environment. See RULE-2.\n' > d.md; git add d.md
sh .githooks/pre-commit >/dev/null 2>&1; ck "doc mention allowed" 0 $?
git reset -q; rm d.md

# Test fixtures must be able to contain something key-shaped, or you cannot
# prove the guard fires. Placeholders are allowed; realistic values are not.
printf 'const k = "SCHEMAFLUX_API_KEY=hunter2hunter2hunter2"\n' > f_test.go; git add f_test.go
sh .githooks/pre-commit >/dev/null 2>&1; ck "placeholder fixture allowed" 0 $?
git reset -q; rm f_test.go

printf 'const k = "SCHEMAFLUX_API_KEY=Xk9Qm2Vb7Rt4Zp1Ls8Nw3Yd6"\n' > g_test.go; git add g_test.go
sh .githooks/pre-commit >/dev/null 2>&1; ck "realistic key in a test blocked" 1 $?
git reset -q; rm g_test.go

printf 'k\n' > secret.key; git add -f secret.key
sh .githooks/pre-commit >/dev/null 2>&1; ck "*.key blocked" 1 $?
git reset -q; rm secret.key

printf 'x\n' > app.db; git add -f app.db
sh .githooks/pre-commit >/dev/null 2>&1; ck "*.db blocked" 1 $?
git reset -q; rm app.db

printf 'x\n' > .env; git add -f .env
sh .githooks/pre-commit >/dev/null 2>&1; ck ".env blocked" 1 $?
git reset -q; rm .env

printf 'k\n' > secret.key; git add -f secret.key
AFF_SKIP_HOOKS=1 sh .githooks/pre-commit >/dev/null 2>&1; ck "AFF_SKIP_HOOKS bypasses" 0 $?
git reset -q; rm secret.key

printf 'BEGIN\n-----BEGIN RSA PRIVATE KEY-----\n' > p.txt; git add p.txt
sh .githooks/pre-commit >/dev/null 2>&1; ck "private key block blocked" 1 $?
git reset -q; rm p.txt

# Go path: go.mod present, Go staged, code is broken -> must fail
printf 'module x\n\ngo 1.26\n' > go.mod
printf 'package main\nfunc main() { this is not go }\n' > main.go
git add go.mod main.go
sh .githooks/pre-commit >/dev/null 2>&1; ck "broken Go blocked" 1 $?

# Formatting: valid Go, deliberately misformatted -> must fail before build
git reset -q
printf 'package main\nimport "fmt"\nfunc main()  {\nfmt.Println( "x" )\n}\n' > main.go
git add go.mod main.go
sh .githooks/pre-commit >/dev/null 2>&1; ck "unformatted Go blocked" 1 $?

# Same file, gofmt'd -> must pass all of fmt, build, vet, test
gofmt -w main.go 2>/dev/null
git add main.go
sh .githooks/pre-commit >/dev/null 2>&1; ck "formatted Go allowed" 0 $?

# Go present but only docs staged -> skip Go checks
git reset -q
printf 'notes\n' > notes.md; git add notes.md
sh .githooks/pre-commit >/dev/null 2>&1; ck "docs-only skips Go checks" 0 $?
git reset -q; rm -f go.mod main.go notes.md

echo "--- pre-push ---"

pp() { printf '%s\n' "$1" | sh .githooks/pre-push origin https://example/x >/dev/null 2>&1; echo $?; }
DEV=$(git rev-parse dev)
MAIN=$(git rev-parse main)
ZERO=0000000000000000000000000000000000000000

r=$(pp "refs/heads/dev $DEV refs/heads/dev $ZERO");            ck "push dev allowed" 0 "$r"
r=$(pp "refs/heads/dev $DEV refs/heads/main $ZERO");           ck "dev->main refspec blocked" 1 "$r"
r=$(pp "refs/heads/main $MAIN refs/heads/main $ZERO");         ck "main without AFF_PROMOTE blocked" 1 "$r"

r=$(printf 'refs/heads/main %s refs/heads/main %s\n' "$MAIN" "$ZERO" | AFF_PROMOTE=1 sh .githooks/pre-push origin u >/dev/null 2>&1; echo $?)
ck "main contained in dev promotes" 0 "$r"

# Make main diverge: commit on main that dev does not have
git checkout -q main; echo x > x.txt; git add x.txt; git commit -qm "only on main"
AHEAD=$(git rev-parse main); git checkout -q dev
r=$(printf 'refs/heads/main %s refs/heads/main %s\n' "$AHEAD" "$ZERO" | AFF_PROMOTE=1 sh .githooks/pre-push origin u >/dev/null 2>&1; echo $?)
ck "main not contained in dev blocked" 1 "$r"

r=$(printf 'refs/heads/main %s refs/heads/main %s\n' "$ZERO" "$MAIN" | sh .githooks/pre-push origin u >/dev/null 2>&1; echo $?)
ck "branch deletion ignored" 0 "$r"

printf '\n%s passed, %s failed\n' "$pass" "$fail"
cd /; rm -rf "$T"
[ "$fail" = 0 ]
