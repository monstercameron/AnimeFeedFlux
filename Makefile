# AnimeFeedFlux
#
# Runs on Linux and in CI. Recipe lines must arrive with LF endings — a CR makes
# make pass a trailing \r to the shell, failing as `command not found: go\r`,
# an error naming a command nobody typed. .gitattributes pins that.

SHELL := /bin/sh
BIN   := bin/animefeedflux
PKG   := ./...

# COVERPKG is PKG minus generated code, and it is what every coverage number
# in this repository is measured over.
#
# gen/aff/v1 is protoc-gen-go output: ~3,600 statements of getters, String()
# methods and reflection wiring, none of it hand-written and none of it
# reachable by a test that would tell us anything. Including it moved total
# coverage from 79% to 62% while saying nothing about the code anyone wrote,
# and the only way to "fix" that number would be to write a reflection walk
# that calls 1,191 generated getters — which is precisely the padding
# scripts/coverage-ratchet.sh's own doc comment exists to argue against.
#
# The correctness of generated code is protoc's problem, guaranteed by the
# generator and by the .proto files it reads. What we owe a test is the code
# we wrote around it.
COVERPKG := $(shell go list ./... | grep -v '/gen/')

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.buildDate=$(BUILD_DATE)

# CGO_ENABLED=0 is not a preference: the runtime image is distroless/static and
# a cgo binary will not run there (§15.1). It is also what forces the pure-Go
# SQLite driver decision recorded in §15.1.
export CGO_ENABLED := 0

.PHONY: all build run test test-race fmt fmt-check vet lint validate cover cover-wasm tidy hooks clean web help

all: fmt-check vet test ## fmt, vet, test

help: ## List targets
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN{FS=":.*?## "};{printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the server
	@mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/animefeedflux

run: build ## Build and run
	./$(BIN)

# Builds into web/dist/ by default (SERVE_DIR overrides). Not wired into
# `all`/CI yet — the admin host that serves web/dist/ (internal/publish's
# StaticHandler, mounted by cmd/animefeedflux) is being wired concurrently;
# see web/build.sh's header for the scratch-dir/atomic-replace details
# (TODOS.md D0-02/D0-03).
web: ## Build the admin WASM bundle (D0-02/D0-03)
	sh scripts/build-web.sh

test: ## Unit tests (-shuffle; no network, no API key)
	go test -shuffle=on $(PKG)

# -race cannot run on windows/arm64, so this is effectively CI-only (§17.2).
# It stays a target so CI and a human run the same command.
test-race: ## Unit tests with the race detector
	go test -race -shuffle=on $(PKG)

fmt: ## Format
	gofmt -w .

fmt-check: ## Fail if anything is unformatted
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "not gofmt'd:"; echo "$$out"; exit 1; fi; \
	echo "gofmt clean"

vet: ## go vet
	go vet $(PKG)

lint: vet ## vet + staticcheck, if installed
	@if command -v staticcheck >/dev/null 2>&1; then \
		staticcheck $(PKG); \
	else \
		echo "staticcheck not installed — CI still enforces it"; \
		echo "  go install honnef.co/go/tools/cmd/staticcheck@v0.7.0"; \
	fi

cover: ## Coverage summary (excludes generated code — see COVERPKG)
	go test -coverprofile=cover.out $(COVERPKG)
	go tool cover -func=cover.out | tail -1

# Feed-format compliance (§5.6). Gates on WARNINGS as well as errors, because
# Slack's own troubleshooting advice is to run the feed through a validator —
# this is a compatibility gate, not a tidiness one.
#
# There are no golden feeds until A2, and a target that fails because the thing
# it validates has not been written yet would be red from the day it was added.
# CI runs this as soon as go.mod exists, so it skips cleanly until then.
validate: ## Validate the rendered golden feeds
	@set -e; \
	golden=internal/render/testdata/golden; \
	if [ ! -d "$$golden" ]; then \
		echo "no $$golden yet — nothing to validate"; exit 0; \
	fi; \
	go build -o bin/affvalidate ./cmd/affvalidate; \
	echo "validating $$(ls $$golden | wc -l) golden documents"; \
	./bin/affvalidate $$golden/*.golden

# Coverage for the browser half, which `make cover` cannot see at all: 62
# files under web/ carry `//go:build js && wasm`, so a host build excludes
# them from their packages entirely and they contribute neither statements nor
# misses to any host profile. That is why web/pages/settings can report 81.7%
# on the host while its actual browser code sits at 15.5%. Requires node.
cover-wasm: ## Coverage for web/ under GOOS=js GOARCH=wasm (needs node)
	sh scripts/coverage-wasm.sh cover-wasm.out

i18n-lint: ## Fail on user-visible string literals in web/ (§12.6, D6-20)
	@if [ ! -d web ]; then echo "no web/ yet — nothing to lint"; exit 0; fi; \
	go run ./cmd/affi18n lint web

# Both directions of catalogue drift (D6-22, D6-23) are checked by web/i18n's
# own Go tests, not by `affi18n check`. That subcommand reads a flat JSON
# catalogue, and this project's catalogue is Go source — so it could never read
# ours, and this target invoked it without the required --catalogue flag and
# would simply have failed. The Go tests are also the better check here: they
# resolve keys through the real bundle, so they catch a key that exists but
# renders blank, which a JSON key-set comparison cannot see. `affi18n check`
# stays in the tree for a future non-Go catalogue.
i18n-check: ## Catalogue key drift, both directions (D6-22, D6-23)
	@if [ ! -d web/i18n ]; then echo "no catalogue yet — nothing to check"; exit 0; fi; \
	go test ./web/i18n/ -run 'TestEveryDeclaredKeyResolves|TestNoOrphanCatalogueEntries|Resolve'

# `make i18n-lint` now reports zero, so the CI `i18n` job runs both this and
# i18n-lint unconditionally and gates on them (see .github/workflows/ci.yml).
# Deliberately still NOT added to the local `all` target: `all` is fmt-check +
# vet + test, the fast inner loop, and this target shells out to build+run a
# second binary — CI is the enforcement point, `all` stays cheap.
i18n-ratchet: ## Zero-literal ratchet; the count may never rise (D6-21)
	go run ./cmd/affi18n ratchet --baseline=.github/i18n-baseline.txt web

tidy: ## go mod tidy
	go mod tidy

hooks: ## Install git hooks and the commit template
	sh scripts/setup-hooks.sh

clean: ## Remove build output
	rm -rf bin cover.out
