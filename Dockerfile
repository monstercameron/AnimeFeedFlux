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
COPY --from=build --chown=65532:65532 /out/animefeedflux /usr/local/bin/animefeedflux
COPY --from=build --chown=65532:65532 /tmp /var/lib/animefeedflux

USER 65532:65532

# Loopback only. Docker writes its own DNAT rules ahead of the host firewall
# chain, so publishing to 0.0.0.0 would expose this PAST ufw — which on this
# design would put the admin plane on the internet (§19). The compose file
# binds 127.0.0.1 explicitly; this EXPOSE is documentation, not a policy.
EXPOSE 9310 9311

VOLUME ["/var/lib/animefeedflux"]

ENTRYPOINT ["/usr/local/bin/animefeedflux"]
