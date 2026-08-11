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

	// OffsiteDir, if set, is where the nightly ops scheduler (internal/ops's
	// Scheduler, wired in cmd/animefeedflux's runAll) additionally writes an
	// encrypted off-box copy of that night's backup — see
	// ops.SchedulerConfig.OffsiteDir's doc comment for exactly what this
	// does and does not provide. Empty (the default) means no off-box copy
	// is attempted; the nightly job alerts on that absence rather than only
	// logging it (TODOS.md C4-03), so leaving this unset is a visible,
	// deliberate choice, not a silent gap.
	OffsiteDir string

	// OTelEnabled/OTelExporter/TraceSampleRatio/OTelServiceName configure
	// obs.Setup (PLAN.md §15.0a, §16). Instrumentation itself always runs
	// unconditionally at every call site; only whether it's ever exported
	// anywhere is gated by these. OTelEnabled defaults to false so a
	// deployment that never touches these variables gets the same
	// zero-overhead no-op providers it always has.
	// MonthlySpendCeilingUSD is the calendar-month provider-spend ceiling
	// (§13, §19). Zero means unlimited — an unset ceiling must not read as
	// "zero budget", which would stop all generation on the first run and
	// look exactly like a broken kill switch.
	//
	// A daily cap does not bound a month: thirty-one days at the daily limit
	// is a month at thirty-one times it, and that failure is a slow bleed
	// that never trips a single daily check. The provider bills monthly, so
	// the limit that matches the thing being protected is monthly too.
	MonthlySpendCeilingUSD float64

	OTelEnabled      bool
	OTelExporter     string // "otlp" or "stdout"; defaults to "otlp" when OTelEnabled
	TraceSampleRatio float64
	OTelServiceName  string

	// AdminStaticDir is the directory web/build.sh stages the compiled
	// admin WASM bundle into (app.wasm, app.wasm.gz, wasm_exec.js,
	// index.html — see internal/publish/static.go's NewStaticHandler).
	// Defaults to "web/dist", the same default web/build.sh itself uses
	// (SERVE_DIR), so an unconfigured dev box and an unconfigured
	// container agree on where to look without either side hardcoding the
	// other's default. A missing directory is normal before `make web`/
	// web/build.sh has run — that is NOT validated here (existence is a
	// startup-time filesystem check, cmd/animefeedflux's composition
	// root's job, not config parsing's) — so Load never fails because the
	// bundle hasn't been built yet.
	AdminStaticDir string

	// PasswordPepper is the optional second secret PLAN.md §4 describes
	// (HMAC-SHA256 mixed into the admin credential before it is hashed).
	// Empty means "no pepper configured" — the default, and the case that
	// must keep every deployment working exactly as it does today.
	// PasswordPepperVersion travels with it so a stolen database's
	// pepper_version column can be told apart from "hashed under a
	// different pepper generation" during rotation (SEC-08). It is
	// meaningless, and validated as unset, whenever PasswordPepper is
	// empty.
	PasswordPepper        Secret
	PasswordPepperVersion int

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
	// DefaultAdminStaticDir mirrors web/build.sh's own default SERVE_DIR
	// ("$script_dir/dist", i.e. web/dist relative to the repo root) so the
	// two sides of the build/serve split agree without either hardcoding
	// the other's value.
	DefaultAdminStaticDir = "web/dist"
	// DefaultTraceSampleRatio matches obs.Setup's own fallback (otel.go),
	// named here so §16's table and obs's default cannot silently drift
	// apart from each other.
	DefaultTraceSampleRatio = 0.05
	// DefaultOTelServiceName is what OTEL_SERVICE_NAME defaults to when
	// unset — the standard OTel env var, not an AFF_-prefixed one (§16).
	DefaultOTelServiceName = "animefeedflux"
	// defaultOTelExporterWhenEnabled is AFF_OTEL_EXPORTER's default the
	// moment AFF_OTEL_ENABLED=1 and no exporter was named explicitly (§16:
	// "default otlp when enabled"). It is not exported because it only ever
	// applies inside Load, never as a standalone default a caller could want
	// independent of OTelEnabled.
	defaultOTelExporterWhenEnabled = "otlp"
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
		OffsiteDir:      env("AFF_OFFSITE_DIR"),
		AdminStaticDir:  v.optionalWithDefault("AFF_ADMIN_STATIC_DIR", DefaultAdminStaticDir),

		GenerationEnabled:   v.boolean("AFF_GENERATION_ENABLED", true),
		MaxConcurrentRuns:   v.positive("AFF_MAX_CONCURRENT_RUNS", DefaultMaxConcurrentRuns),
		ProviderMaxInflight: v.positive("AFF_PROVIDER_MAX_INFLIGHT", DefaultProviderMaxInflight),
		ScheduleJitter:      v.duration("AFF_SCHEDULE_JITTER", DefaultScheduleJitter),
		CacheMaxBytes:       int64(v.positive("AFF_CACHE_MAX_BYTES", DefaultCacheMaxBytes)),
		LogLevel:            v.level("AFF_LOG_LEVEL", DefaultLogLevel),
		LiveLLM:             v.boolean("AFF_LIVE_LLM", false),

		OTelEnabled:            v.boolean("AFF_OTEL_ENABLED", false),
		MonthlySpendCeilingUSD: v.nonNegativeFloat("AFF_MONTHLY_SPEND_CEILING_USD"),
		TraceSampleRatio:       v.ratio("AFF_TRACE_SAMPLE_RATIO", DefaultTraceSampleRatio),
		OTelServiceName:        v.optionalWithDefault("OTEL_SERVICE_NAME", DefaultOTelServiceName),
	}
	c.OTelExporter = v.otelExporter("AFF_OTEL_EXPORTER", c.OTelEnabled)

	c.PublicBaseURL = v.baseURL("AFF_PUBLIC_BASE_URL")
	c.AllowedOrigins = v.originList("AFF_ALLOWED_ORIGINS")

	c.PasswordPepper = Secret(strings.TrimSpace(env("AFF_PASSWORD_PEPPER")))
	c.PasswordPepperVersion = v.pepperVersion("AFF_PASSWORD_PEPPER_VERSION", c.PasswordPepper != "")

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

// optionalWithDefault returns the trimmed env value, or def when unset. There
// is nothing to validate about an arbitrary string (unlike positive/duration/
// level below), so this never adds a problem — it exists purely so a
// string-valued optional setting follows the same "env empty -> named
// default" shape as the typed ones instead of being spelled out ad hoc at
// each call site.
func (v *validator) optionalWithDefault(name, def string) string {
	s := strings.TrimSpace(v.env(name))
	if s == "" {
		return def
	}
	return s
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

// ratio validates a float64 in [0,1] — used only for AFF_TRACE_SAMPLE_RATIO
// today, but named generically since "a fraction between 0 and 1" is not
// specific to tracing.
// nonNegativeFloat reads an optional non-negative float. Unset yields 0,
// which every caller here reads as "no limit" — see MonthlySpendCeilingUSD
// for why the zero value must mean unlimited rather than zero budget.
func (v *validator) nonNegativeFloat(name string) float64 {
	raw := strings.TrimSpace(v.env(name))
	if raw == "" {
		return 0
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		v.bad(name, fmt.Sprintf("must be a number (got %q)", raw))
		return 0
	}
	if f < 0 {
		v.bad(name, fmt.Sprintf("must not be negative (got %v)", f))
		return 0
	}
	return f
}

func (v *validator) ratio(name string, def float64) float64 {
	raw := strings.TrimSpace(v.env(name))
	if raw == "" {
		return def
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		v.bad(name, fmt.Sprintf("must be a number between 0 and 1 (got %q)", raw))
		return def
	}
	if f < 0 || f > 1 {
		v.bad(name, fmt.Sprintf("must be between 0 and 1 (got %v)", f))
		return def
	}
	return f
}

// otelExporter validates AFF_OTEL_EXPORTER against the two values obs.Setup
// understands (otel.go's setupTraces/setupMetrics switches) and applies
// §16's "default otlp when enabled" rule: an unset exporter is fine (and
// meaningless) while tracing is off, but the moment AFF_OTEL_ENABLED=1 an
// unset exporter would otherwise silently mean "record everything, export
// nothing" (obs.Setup's "", "none" branch) — surprising for a value whose
// whole point was turning export ON. enabled is decided ahead of this call
// (AFF_OTEL_ENABLED is parsed first in Load) so this function only supplies
// the exporter default, not the enabled default.
func (v *validator) otelExporter(name string, enabled bool) string {
	raw := strings.TrimSpace(v.env(name))
	if raw != "" && raw != "otlp" && raw != "stdout" {
		v.bad(name, fmt.Sprintf("must be otlp or stdout (got %q)", raw))
		return raw
	}
	if raw == "" && enabled {
		return defaultOTelExporterWhenEnabled
	}
	return raw
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

// pepperVersion validates AFF_PASSWORD_PEPPER_VERSION against whether a
// pepper is actually configured. A pepper without a recorded generation
// makes rotation impossible (PLAN.md §4: "a pepper_version column from day
// one... without it rotation is impossible, not merely hard"), so a
// configured pepper REQUIRES a positive version; conversely a version set
// with no pepper is very likely a leftover from removing
// AFF_PASSWORD_PEPPER and is flagged rather than silently ignored, since a
// deployment believing it still peppers new hashes when it does not is
// exactly the silent-regression PLAN.md §4 warns against.
func (v *validator) pepperVersion(name string, pepperConfigured bool) int {
	raw := strings.TrimSpace(v.env(name))
	if raw == "" {
		if pepperConfigured {
			v.bad(name, "must be a positive integer when AFF_PASSWORD_PEPPER is set, so the generation a credential was hashed under can be recorded for rotation")
			return 0
		}
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		v.bad(name, fmt.Sprintf("must be an integer (got %q)", raw))
		return 0
	}
	if n <= 0 {
		v.bad(name, fmt.Sprintf("must be a positive integer (got %d)", n))
		return 0
	}
	if !pepperConfigured {
		v.bad(name, "is set but AFF_PASSWORD_PEPPER is not; a pepper version with no pepper means nothing and is likely a leftover from removing the pepper")
		return 0
	}
	return n
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
