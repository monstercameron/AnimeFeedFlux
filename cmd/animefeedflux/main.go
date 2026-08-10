// Command animefeedflux is the AnimeFeedFlux server.
//
// It boots, validates its configuration, opens and migrates the store, and
// serves the publish plane, the control plane, and the scheduler (wire.go's
// runAll) until it shuts down cleanly on SIGTERM/SIGINT.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/config"
	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
)

// Build metadata, stamped by the linker (§15.1):
//
//	-ldflags "-X main.version=... -X main.commit=... -X main.buildDate=..."
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// Exit codes are distinguished because a container restart loop looks identical
// from the outside otherwise. 2 means "you configured it wrong" — restarting
// will not help and the logs say exactly which variable.
const (
	exitOK          = 0
	exitRuntimeFail = 1
	exitBadConfig   = 2
)

func main() {
	// `animefeedflux healthcheck` is how the container checks itself. The
	// runtime image is distroless: no shell, no curl, nothing to exec. The
	// binary is the only executable in it, so it has to be able to probe its
	// own endpoint (§15.4).
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheckCmd())
	}
	os.Exit(run())
}

// healthcheckCmd probes the local publish listener and reports via exit code.
//
// It reads AFF_PUBLISH_ADDR rather than taking a flag so the compose
// healthcheck cannot drift from the address the server actually binds.
func healthcheckCmd() int {
	addr := os.Getenv("AFF_PUBLISH_ADDR")
	if addr == "" {
		fmt.Fprintln(os.Stderr, "healthcheck: AFF_PUBLISH_ADDR is not set")
		return exitBadConfig
	}
	// Inside the container the server binds 0.0.0.0; dialling that literally
	// does not work as a destination, so probe loopback on the same port.
	if host, port, err := net.SplitHostPort(addr); err == nil && (host == "" || host == "0.0.0.0" || host == "::") {
		addr = net.JoinHostPort("127.0.0.1", port)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return exitRuntimeFail
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
		return exitRuntimeFail
	}
	return exitOK
}

func run() int {
	cfg, err := config.Load(config.OSGetenv)
	if err != nil {
		// Deliberately not through the logger: configuration failed, so there
		// is no configured logger yet, and this must be readable in `docker
		// logs` with no further ceremony.
		fmt.Fprintln(os.Stderr, err)
		return exitBadConfig
	}

	log := obs.NewLogger(obs.Options{Level: cfg.LogLevel, Writer: os.Stdout})

	log.Info("starting",
		slog.String("version", version),
		slog.String("commit", commit),
		slog.String("build_date", buildDate),
		slog.String("publish_addr", cfg.PublishAddr),
		slog.String("admin_addr", cfg.AdminAddr),
		slog.String("public_base_url", cfg.PublicBaseURL.String()),
		slog.Bool("generation_enabled", cfg.GenerationEnabled),
	)

	// SIGTERM is what a container runtime sends first; SIGINT is Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	// runAll (wire.go) is the composition root: it opens the store, reclaims
	// stale runs, builds the publish plane, the control plane, and the
	// scheduler, starts all three, and shuts them down cleanly on ctx
	// cancellation (PLAN.md §15).
	return runAll(ctx, cfg, log)
}
