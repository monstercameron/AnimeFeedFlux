# AnimeFeedFlux — multi-stage build (PLAN.md §15.1).
#
# The runtime stage carries no toolchain, no shell and no package manager. That
# is most of what containerising this actually buys: a static binary on the host
# would already be sandboxed by systemd, but it would still sit next to a shell.

# --- build ---------------------------------------------------------------
#
# Pinned by digest-free tag deliberately: dependabot watches this file and an
# update arrives as a PR to read, not as a silent base-image drift.
FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first, as their own layer. They change far less often than the
# source, so an edit to one .go file must not re-download the module graph.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# CGO_ENABLED=0 is not a preference. The runtime stage is distroless/static and
# a cgo binary will not run there at all. It is also the constraint that forced
# the pure-Go SQLite driver in A0-16 — see PLAN.md §3.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags "-s -w \
            -X main.version=${VERSION} \
            -X main.commit=${COMMIT} \
            -X main.buildDate=${BUILD_DATE}" \
        -o /out/animefeedflux ./cmd/animefeedflux

# The admin plane serves a compiled GWC/WASM bundle (internal/publish's
# StaticHandler, config.DefaultAdminStaticDir = "web/dist") — this was never
# wired into the image, so a container built from this Dockerfile alone would
# fail NewStaticHandler's ReadDir at boot and never go healthy.
# scripts/build-web.sh is "the canonical entry point" its own header
# names (CI and deploy tooling call it, not web/build.sh directly), so it is
# called here too rather than either re-implementing the build or bypassing
# the fixed address every other caller uses. It execs web/build.sh, which
# does the actual isolated-scratch-dir compile and atomic per-file replace
# (TODOS.md D0-02/D0-03). Needs real gzip -k support, which some BusyBox
# builds lack, hence GNU gzip below.
RUN apk add --no-cache gzip
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    sh scripts/build-web.sh

# The empty, correctly-owned directory the runtime stage adopts as its data
# directory. Created here because distroless has no shell to mkdir with, and
# created EMPTY on purpose — see the runtime stage's comment on why /tmp was
# the wrong donor.
RUN mkdir -p /out/emptydata && chown 65532:65532 /out/emptydata

# --- runtime -------------------------------------------------------------
FROM gcr.io/distroless/static:nonroot

# The data directory is created HERE, owned by the runtime user, and that is
# load-bearing rather than tidiness. Docker initialises an empty NAMED VOLUME
# from the image path including its ownership, so a directory created as root
# yields a volume the nonroot process cannot write — and SQLite fails on its
# first write, long after the container reports healthy.
#
# Bind mounts do NOT inherit ownership this way and must be chowned on the host.
# distroless has no shell, so the directory is staged in the build stage with
# the right ownership and copied across.
#
# It is staged as a directory created FOR this purpose, not as /tmp. Copying
# /tmp was the original trick — it is guaranteed to exist and needs no mkdir —
# but /tmp in the build stage is not empty: apk, `go build` and
# scripts/build-web.sh all use it. Whatever they happened to leave behind was
# copied in as the initial contents of the data directory, and therefore as the
# initial contents of every fresh named volume.
COPY --from=build --chown=65532:65532 /out/animefeedflux /usr/local/bin/animefeedflux
COPY --from=build --chown=65532:65532 /out/emptydata /var/lib/animefeedflux

# web/build.sh's default SERVE_DIR is "<repo>/web/dist"; here that is
# /src/web/dist inside the build stage. Landing it at /web/dist in the
# runtime image matters because config.DefaultAdminStaticDir is the
# *relative* path "web/dist" and this image sets no WORKDIR (distroless's
# default is "/"), so the unconfigured default resolves to exactly this
# path. AFF_ADMIN_STATIC_DIR can still override it if the layout ever
# changes; nothing here requires that env var to be set.
COPY --from=build --chown=65532:65532 /src/web/dist /web/dist

USER 65532:65532

# Loopback only. Docker writes its own DNAT rules ahead of the host firewall
# chain, so publishing to 0.0.0.0 would expose this PAST ufw — which on this
# design would put the admin plane on the internet (§19). The compose file
# binds 127.0.0.1 explicitly; this EXPOSE is documentation, not a policy.
EXPOSE 9310 9311

VOLUME ["/var/lib/animefeedflux"]

ENTRYPOINT ["/usr/local/bin/animefeedflux"]
