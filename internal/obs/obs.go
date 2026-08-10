// Package obs provides structured logging and the identifiers that make a log
// line traceable back to the work that produced it.
//
// Logs go to stdout as JSON (PLAN.md §15). Stdout matters in a container: the
// process must not manage its own files, because the runtime captures stdout
// and the json-file driver handles rotation. The consequence, accepted
// deliberately, is that these lines land in `docker logs` rather than
// journalctl beside the other services on the box.
package obs

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"sync"
)

// contextKey is unexported so no other package can collide with it.
type contextKey int

const (
	runIDKey contextKey = iota
	requestIDKey
)

// WithRunID returns a context carrying a generation run identifier.
func WithRunID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, runIDKey, id)
}

// WithRequestID returns a context carrying an HTTP request identifier.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RunID returns the run identifier in ctx, or "" if there is none.
func RunID(ctx context.Context) string { return stringValue(ctx, runIDKey) }

// RequestID returns the request identifier in ctx, or "" if there is none.
func RequestID(ctx context.Context) string { return stringValue(ctx, requestIDKey) }

func stringValue(ctx context.Context, k contextKey) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(k).(string)
	return s
}

// contextHandler copies the run and request identifiers out of the context and
// onto every record.
//
// Doing this in a Handler rather than at each call site is the point: an
// identifier that has to be remembered is missing from exactly the log line you
// need during an incident.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RunID(ctx); id != "" {
		r.AddAttrs(slog.String("run_id", id))
	}
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(as)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

// secretPattern matches credential shapes that must never appear in a log.
//
// This is a BACKSTOP, not the control (RULE-2). The controls are: secrets are
// never logged deliberately, and config.Secret redacts itself if something
// tries. This layer exists because the cost of the three of them together is a
// few microseconds per line, and the cost of a key in a log file that has
// already been shipped somewhere is unrecoverable.
var secretPattern = regexp.MustCompile(
	`(?i)` +
		`sk-[A-Za-z0-9_-]{16,}` + // OpenAI-style
		`|ghp_[A-Za-z0-9]{20,}` + // GitHub personal token
		`|github_pat_[A-Za-z0-9_]{20,}` +
		`|xox[baprs]-[A-Za-z0-9-]{10,}` + // Slack
		`|-----BEGIN [A-Z ]*PRIVATE KEY-----` +
		`|((?:SCHEMAFLUX_API_KEY|AFF_SECRET_KEY|OPENAI_API_KEY)\s*[=:]\s*)\S+`,
)

// assignPrefix captures the "NAME=" part of an assignment match so the variable
// name survives redaction and the line still says which setting was involved.
// Compiled once at package level, not per match.
var assignPrefix = regexp.MustCompile(`^((?:SCHEMAFLUX_API_KEY|AFF_SECRET_KEY|OPENAI_API_KEY)\s*[=:]\s*)`)

const redacted = "[REDACTED]"

// RedactingWriter scrubs credential shapes from anything written through it.
//
// It is safe for concurrent use: slog handlers may write from many goroutines,
// and an io.Writer is not required to be safe on its own.
type RedactingWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewRedactingWriter wraps w.
func NewRedactingWriter(w io.Writer) *RedactingWriter { return &RedactingWriter{w: w} }

// Write implements io.Writer.
//
// It reports len(p) rather than the number of bytes actually written, because
// redaction changes the length and a short count would be read by callers as a
// write error. The bytes the caller offered were all consumed; only their
// content changed.
func (rw *RedactingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	cleaned := secretPattern.ReplaceAllFunc(p, func(m []byte) []byte {
		if loc := assignPrefix.FindSubmatch(m); loc != nil {
			return append(loc[1], []byte(redacted)...)
		}
		return []byte(redacted)
	})

	if _, err := rw.w.Write(cleaned); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Options configure the logger.
type Options struct {
	Level  slog.Level
	Writer io.Writer // defaults to os.Stdout when nil, set by the caller
}

// NewLogger builds the process logger: JSON, to the given writer, with secrets
// scrubbed and context identifiers attached.
func NewLogger(opts Options) *slog.Logger {
	h := slog.NewJSONHandler(NewRedactingWriter(opts.Writer), &slog.HandlerOptions{
		Level: opts.Level,
	})
	return slog.New(contextHandler{h})
}
