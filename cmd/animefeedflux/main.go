// Command animefeedflux is the AnimeFeedFlux server.
//
// At A0 it does one thing: boot, validate its configuration, serve /healthz on
// the publish listener, and shut down cleanly. Everything else in PLAN.md
// arrives in later phases; this exists so that configuration, logging, signal
// handling and the container's healthcheck are proven before anything depends
// on them.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	os.Exit(run())
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
		slog.String("public_base_url", cfg.PublicBaseURL.String()),
		slog.Bool("generation_enabled", cfg.GenerationEnabled),
	)

	// SIGTERM is what a container runtime sends first; SIGINT is Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	srv := &http.Server{
		Addr:    cfg.PublishAddr,
		Handler: publishMux(log),

		// Explicit, never the zero-value defaults. An internet-facing server
		// with no timeouts holds a connection open until the client decides
		// otherwise (§6).
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("publish listener up", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		log.Error("publish listener failed", slog.Any("error", err))
		return exitRuntimeFail
	case <-ctx.Done():
		log.Info("shutdown signal received, draining")
	}

	// Drain rather than drop. A reader mid-fetch gets its bytes; anything still
	// open at the deadline is cut.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", slog.Any("error", err))
		return exitRuntimeFail
	}

	log.Info("stopped cleanly")
	return exitOK
}

// publishMux is the public, read-only plane (§2). Until A9 it serves only
// health.
func publishMux(log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz(log))
	return mux
}

// health is the shape of the /healthz body. It is deliberately JSON from the
// start: the container healthcheck only reads the status code, but a human
// debugging a deploy reads the body, and "ok" tells them nothing about which
// build they are looking at.
type health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

func healthz(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)

		if err := json.NewEncoder(w).Encode(health{
			Status:  "ok",
			Version: version,
			Commit:  commit,
		}); err != nil {
			// The response is already committed, so this can only be logged.
			log.WarnContext(r.Context(), "writing healthz body failed", slog.Any("error", err))
		}
	}
}
