// Package config parses and validates the process configuration.
//
// Configuration is environment-only (PLAN.md §16): no config file, and no
// secrets on disk beyond the host env_file. Everything is read once, validated
// once, and returned as a single value. There are no package-level globals, so
// a test can construct any configuration it likes without touching the real
// environment.
//
// Validation reports EVERY problem at once rather than the first. A boot that
// fails four times in a row, each naming one more missing variable, wastes four
// deploys to learn what one message could have said.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Secret is a string that must never be printed. It satisfies fmt.Stringer and
// slog.LogValuer so that logging it — deliberately or by accident, as part of
// the whole Config — yields a placeholder rather than the value.
//
// This is defence in depth, not the control: RULE-2 says a secret never reaches
// a log line in the first place, and obs.RedactingWriter is a further backstop.
// Three independent layers, because a leaked key is not recoverable by fixing
// the code afterwards.
type Secret string

// String implements fmt.Stringer.
func (s Secret) String() string { return s.redacted() }

// GoString implements fmt.GoStringer, so %#v does not leak either.
func (s Secret) GoString() string { return s.redacted() }

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.redacted()) }

// Reveal returns the underlying value. Call it only where the secret is used,
// never where it is stored or logged. The name is deliberately awkward so that
// it stands out in review.
func (s Secret) Reveal() string { return string(s) }

func (s Secret) redacted() string {
	if s == "" {
		return "[unset]"
	}
	return "[redacted]"
}

// Config is the whole configuration of the process.
type Config struct {
	// Required.
	DBPath         string
	PublicBaseURL  *url.URL
	PublishAddr    string
	AdminAddr      string
	AllowedOrigins []string
	SecretKey      Secret
	ProviderAPIKey Secret

	// Optional, with defaults.
	GenerationEnabled   bool
	MaxConcurrentRuns   int
	ProviderMaxInflight int
	ScheduleJitter      time.Duration
	CacheMaxBytes       int64
	LogLevel            slog.Level
	BackupDir           string
	SlackWebhookURL     string

	// Test-only. Gates paid provider tests (RULE-1).
	LiveLLM bool
}

// Getenv looks up an environment variable. Injecting it keeps Load testable
// without mutating the real process environment, which would make tests
// order-dependent under -shuffle and unsafe under -race.
type Getenv func(string) string

// OSGetenv reads the real environment.
func OSGetenv(k string) string { return os.Getenv(k) }

// Default values, named so they can be asserted in tests rather than duplicated
// as literals.
const (
	DefaultMaxConcurrentRuns   = 3
	DefaultProviderMaxInflight = 4
	DefaultScheduleJitter      = 10 * time.Minute
	DefaultCacheMaxBytes       = 64 << 20 // 64 MiB
	DefaultLogLevel            = slog.LevelInfo
)

// Load reads and validates the configuration.
//
// On failure it returns an error that names every problem found, one per line,
// so a misconfigured boot is fixed in a single pass.
func Load(env Getenv) (*Config, error) {
	v := &validator{env: env}

	c := &Config{
		DBPath:         v.required("AFF_DB_PATH"),
		PublishAddr:    v.required("AFF_PUBLISH_ADDR"),
		AdminAddr:      v.required("AFF_ADMIN_ADDR"),
		SecretKey:      Secret(v.required("AFF_SECRET_KEY")),
		ProviderAPIKey: Secret(v.required("SCHEMAFLUX_API_KEY")),

		BackupDir:       env("AFF_BACKUP_DIR"),
		SlackWebhookURL: env("AFF_SLACK_WEBHOOK_URL"),

		GenerationEnabled:   v.boolean("AFF_GENERATION_ENABLED", true),
		MaxConcurrentRuns:   v.positive("AFF_MAX_CONCURRENT_RUNS", DefaultMaxConcurrentRuns),
		ProviderMaxInflight: v.positive("AFF_PROVIDER_MAX_INFLIGHT", DefaultProviderMaxInflight),
		ScheduleJitter:      v.duration("AFF_SCHEDULE_JITTER", DefaultScheduleJitter),
		CacheMaxBytes:       int64(v.positive("AFF_CACHE_MAX_BYTES", DefaultCacheMaxBytes)),
		LogLevel:            v.level("AFF_LOG_LEVEL", DefaultLogLevel),
		LiveLLM:             v.boolean("AFF_LIVE_LLM", false),
	}

	c.PublicBaseURL = v.baseURL("AFF_PUBLIC_BASE_URL")
	c.AllowedOrigins = v.originList("AFF_ALLOWED_ORIGINS")

	if err := v.err(); err != nil {
		return nil, err
	}
	return c, nil
}

// validator accumulates problems instead of returning on the first one.
type validator struct {
	env      Getenv
	problems []string
}

func (v *validator) bad(name, msg string) {
	v.problems = append(v.problems, fmt.Sprintf("%s: %s", name, msg))
}

func (v *validator) err() error {
	if len(v.problems) == 0 {
		return nil
	}
	sort.Strings(v.problems)
	return fmt.Errorf("invalid configuration:\n  %s", strings.Join(v.problems, "\n  "))
}

func (v *validator) required(name string) string {
	s := strings.TrimSpace(v.env(name))
	if s == "" {
		v.bad(name, "is required but not set")
	}
	return s
}

func (v *validator) boolean(name string, def bool) bool {
	raw := strings.TrimSpace(v.env(name))
	if raw == "" {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		v.bad(name, fmt.Sprintf("must be a boolean (got %q)", raw))
		return def
	}
	return b
}

func (v *validator) positive(name string, def int) int {
	raw := strings.TrimSpace(v.env(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		v.bad(name, fmt.Sprintf("must be an integer (got %q)", raw))
		return def
	}
	if n <= 0 {
		v.bad(name, fmt.Sprintf("must be greater than zero (got %d)", n))
		return def
	}
	return n
}

func (v *validator) duration(name string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(v.env(name))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		v.bad(name, fmt.Sprintf("must be a duration such as 10m (got %q)", raw))
		return def
	}
	if d < 0 {
		v.bad(name, fmt.Sprintf("must not be negative (got %s)", d))
		return def
	}
	return d
}

func (v *validator) level(name string, def slog.Level) slog.Level {
	raw := strings.TrimSpace(v.env(name))
	if raw == "" {
		return def
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(raw)); err != nil {
		v.bad(name, fmt.Sprintf("must be one of debug, info, warn, error (got %q)", raw))
		return def
	}
	return l
}

// baseURL enforces that the public base URL is absolute with a scheme.
//
// This one is worth its own validator: the base URL is baked into every guid
// and every atom:link rel="self" (§5.1). A relative or scheme-less value does
// not fail loudly at boot — it produces feeds full of malformed identifiers
// that subscribers have already stored by the time anyone notices, and guids
// are forever.
func (v *validator) baseURL(name string) *url.URL {
	raw := strings.TrimSpace(v.env(name))
	if raw == "" {
		v.bad(name, "is required but not set")
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		v.bad(name, fmt.Sprintf("is not a valid URL (%v)", err))
		return nil
	}
	switch {
	case !u.IsAbs():
		v.bad(name, fmt.Sprintf("must be absolute with a scheme, e.g. https://example.com (got %q)", raw))
		return nil
	case u.Scheme != "http" && u.Scheme != "https":
		v.bad(name, fmt.Sprintf("scheme must be http or https (got %q)", u.Scheme))
		return nil
	case u.Host == "":
		v.bad(name, fmt.Sprintf("must include a host (got %q)", raw))
		return nil
	}
	// Trailing slashes make every joined path ambiguous later. Normalise once,
	// here, rather than defensively at each call site.
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u
}

func (v *validator) originList(name string) []string {
	raw := strings.TrimSpace(v.env(name))
	if raw == "" {
		v.bad(name, "is required but not set")
		return nil
	}
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		o := strings.TrimSpace(part)
		if o == "" {
			continue
		}
		u, err := url.Parse(o)
		if err != nil || !u.IsAbs() || u.Host == "" {
			// An Origin header is scheme://host[:port] and nothing else. A
			// bare hostname here would never match, so the WebSocket upgrade
			// would reject every connection with no clue why (§4).
			v.bad(name, fmt.Sprintf("each origin must be scheme://host, e.g. https://admin.example.com (got %q)", o))
			continue
		}
		out = append(out, strings.TrimRight(o, "/"))
	}
	if len(out) == 0 && len(v.problems) == 0 {
		v.bad(name, "must list at least one origin")
	}
	return out
}
