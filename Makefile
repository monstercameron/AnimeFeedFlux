# AnimeFeedFlux
#
# Runs on Linux and in CI. Recipe lines must arrive with LF endings — a CR makes
# make pass a trailing \r to the shell, failing as `command not found: go\r`,
# an error naming a command nobody typed. .gitattributes pins that.

SHELL := /bin/sh
BIN   := bin/animefeedflux
PKG   := ./...

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

.PHONY: all build run test test-race fmt fmt-check vet lint validate cover tidy hooks clean web help

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

cover: ## Coverage summary
	go test -coverprofile=cover.out $(PKG)
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

i18n-lint: ## Fail on user-visible string literals in web/ (§12.6, D6-20)
	@if [ ! -d web ]; then echo "no web/ yet — nothing to lint"; exit 0; fi; \
	go run ./cmd/affi18n lint web

i18n-check: ## Catalogue key drift, both directions (D6-22, D6-23)
	@if [ ! -d web/i18n ]; then echo "no catalogue yet — nothing to check"; exit 0; fi; \
	go run ./cmd/affi18n check web

# The ratchet is deliberately NOT in `all` yet. Phase D is mid-build and the
# gate currently reports real literals; wiring it in before web/ is clean would
# make `make all` red from the day it was added, which is how a gate gets
# ignored — the same reasoning that kept CI's Go jobs skipping until A0 landed.
# It goes into `all` and into CI once `make i18n-lint` reports zero (D6-21).
i18n-ratchet: ## Zero-literal ratchet; the count may never rise (D6-21)
	go run ./cmd/affi18n ratchet --baseline=.github/i18n-baseline.txt web

tidy: ## go mod tidy
	go mod tidy

hooks: ## Install git hooks and the commit template
	sh scripts/setup-hooks.sh

clean: ## Remove build output
	rm -rf bin cover.out
