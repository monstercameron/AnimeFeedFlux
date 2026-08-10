package config

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// valid is the minimum environment that must load cleanly. Tests derive from it
// so that a new required variable breaks every test once, here, rather than
// leaving some of them passing against a configuration that can no longer boot.
func valid() map[string]string {
	return map[string]string{
		"AFF_DB_PATH":         "/var/lib/aff/aff.db",
		"AFF_PUBLIC_BASE_URL": "https://anime.example.com",
		"AFF_PUBLISH_ADDR":    "127.0.0.1:9310",
		"AFF_ADMIN_ADDR":      "127.0.0.1:9311",
		"AFF_ALLOWED_ORIGINS": "https://admin.anime.example.com",
		"AFF_SECRET_KEY":      "not-a-real-key",
		"SCHEMAFLUX_API_KEY":  "not-a-real-key",
	}
}

func env(m map[string]string) Getenv {
	return func(k string) string { return m[k] }
}

func TestLoadMinimalEnvironment(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil {
		t.Fatalf("valid environment did not load: %v", err)
	}

	if got, want := c.PublicBaseURL.String(), "https://anime.example.com"; got != want {
		t.Errorf("PublicBaseURL = %q, want %q", got, want)
	}
	if got, want := c.MaxConcurrentRuns, DefaultMaxConcurrentRuns; got != want {
		t.Errorf("MaxConcurrentRuns = %d, want default %d", got, want)
	}
	if got, want := c.ScheduleJitter, DefaultScheduleJitter; got != want {
		t.Errorf("ScheduleJitter = %s, want default %s", got, want)
	}
	if got, want := c.CacheMaxBytes, int64(DefaultCacheMaxBytes); got != want {
		t.Errorf("CacheMaxBytes = %d, want default %d", got, want)
	}
	if !c.GenerationEnabled {
		t.Error("GenerationEnabled should default to true")
	}
	// RULE-1: paid provider tests must be opt-in, so this defaults off.
	if c.LiveLLM {
		t.Error("LiveLLM must default to false")
	}
}

func TestMissingRequiredAreAllReportedAtOnce(t *testing.T) {
	// The point of the validator: one boot, one message, every problem.
	_, err := Load(env(map[string]string{}))
	if err == nil {
		t.Fatal("empty environment loaded without error")
	}
	for _, name := range []string{
		"AFF_DB_PATH", "AFF_PUBLIC_BASE_URL", "AFF_PUBLISH_ADDR",
		"AFF_ADMIN_ADDR", "AFF_ALLOWED_ORIGINS", "AFF_SECRET_KEY", "SCHEMAFLUX_API_KEY",
	} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not mention missing %s:\n%v", name, err)
		}
	}
}

func TestPublicBaseURLMustBeAbsolute(t *testing.T) {
	// This is the validation worth having: the base URL is baked into every
	// guid, and a guid is forever (§5.1). A bad one does not fail loudly, it
	// ships malformed identifiers that subscribers have already stored.
	for _, tc := range []struct {
		name, value string
	}{
		{"relative", "/feeds"},
		{"scheme-less", "anime.example.com"},
		{"unsupported scheme", "ftp://anime.example.com"},
		{"no host", "https://"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := valid()
			m["AFF_PUBLIC_BASE_URL"] = tc.value
			if _, err := Load(env(m)); err == nil {
				t.Fatalf("%q was accepted as a public base URL", tc.value)
			} else if !strings.Contains(err.Error(), "AFF_PUBLIC_BASE_URL") {
				t.Fatalf("error does not name the offending variable: %v", err)
			}
		})
	}
}

func TestPublicBaseURLIsNormalised(t *testing.T) {
	m := valid()
	m["AFF_PUBLIC_BASE_URL"] = "https://anime.example.com/base/?x=1#frag"
	c, err := Load(env(m))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got, want := c.PublicBaseURL.String(), "https://anime.example.com/base"; got != want {
		t.Errorf("normalised base URL = %q, want %q", got, want)
	}
}

func TestOriginsMustBeSchemeAndHost(t *testing.T) {
	m := valid()
	m["AFF_ALLOWED_ORIGINS"] = "admin.example.com"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("a bare hostname was accepted as an origin")
	}
}

func TestOriginsSplitAndTrim(t *testing.T) {
	m := valid()
	m["AFF_ALLOWED_ORIGINS"] = " https://a.example.com , https://b.example.com/ "
	c, err := Load(env(m))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []string{"https://a.example.com", "https://b.example.com"}
	if len(c.AllowedOrigins) != len(want) {
		t.Fatalf("origins = %v, want %v", c.AllowedOrigins, want)
	}
	for i := range want {
		if c.AllowedOrigins[i] != want[i] {
			t.Errorf("origin[%d] = %q, want %q", i, c.AllowedOrigins[i], want[i])
		}
	}
}

func TestNumericAndDurationValidation(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"AFF_MAX_CONCURRENT_RUNS", "0"},
		{"AFF_MAX_CONCURRENT_RUNS", "-1"},
		{"AFF_MAX_CONCURRENT_RUNS", "many"},
		{"AFF_SCHEDULE_JITTER", "ten minutes"},
		{"AFF_SCHEDULE_JITTER", "-5m"},
		{"AFF_GENERATION_ENABLED", "yes please"},
		{"AFF_LOG_LEVEL", "chatty"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			m := valid()
			m[tc.key] = tc.value
			if _, err := Load(env(m)); err == nil {
				t.Fatalf("%s=%q was accepted", tc.key, tc.value)
			} else if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("error does not name %s: %v", tc.key, err)
			}
		})
	}
}

// TestPasswordPepperDefaultsToUnconfigured is the compatibility case that matters most:
// a deployment that has never set AFF_PASSWORD_PEPPER must load cleanly with an empty
// pepper and a zero version, exactly the "no pepper" state PLAN.md §4 requires to be a
// true no-op.
func TestPasswordPepperDefaultsToUnconfigured(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.PasswordPepper != "" {
		t.Errorf("PasswordPepper = %q, want empty by default", c.PasswordPepper.Reveal())
	}
	if c.PasswordPepperVersion != 0 {
		t.Errorf("PasswordPepperVersion = %d, want 0 by default", c.PasswordPepperVersion)
	}
}

func TestPasswordPepperRequiresAPositiveVersion(t *testing.T) {
	for _, tc := range []struct {
		name, version string
	}{
		{"missing", ""},
		{"zero", "0"},
		{"negative", "-1"},
		{"not an integer", "one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := valid()
			m["AFF_PASSWORD_PEPPER"] = "a-real-256-bit-secret-from-the-environment"
			if tc.version != "" {
				m["AFF_PASSWORD_PEPPER_VERSION"] = tc.version
			}
			if _, err := Load(env(m)); err == nil {
				t.Fatalf("AFF_PASSWORD_PEPPER_VERSION=%q was accepted alongside a configured pepper", tc.version)
			} else if !strings.Contains(err.Error(), "AFF_PASSWORD_PEPPER_VERSION") {
				t.Fatalf("error does not name AFF_PASSWORD_PEPPER_VERSION: %v", err)
			}
		})
	}
}

func TestPasswordPepperVersionWithoutPepperIsRejected(t *testing.T) {
	m := valid()
	m["AFF_PASSWORD_PEPPER_VERSION"] = "1"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("a version with no AFF_PASSWORD_PEPPER was accepted")
	} else if !strings.Contains(err.Error(), "AFF_PASSWORD_PEPPER_VERSION") {
		t.Fatalf("error does not name AFF_PASSWORD_PEPPER_VERSION: %v", err)
	}
}

func TestPasswordPepperRoundTrips(t *testing.T) {
	m := valid()
	m["AFF_PASSWORD_PEPPER"] = "a-real-256-bit-secret-from-the-environment"
	m["AFF_PASSWORD_PEPPER_VERSION"] = "1"
	c, err := Load(env(m))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.PasswordPepper.Reveal() != "a-real-256-bit-secret-from-the-environment" {
		t.Errorf("PasswordPepper = %q, want the configured value", c.PasswordPepper.Reveal())
	}
	if c.PasswordPepperVersion != 1 {
		t.Errorf("PasswordPepperVersion = %d, want 1", c.PasswordPepperVersion)
	}
}

func TestOverrides(t *testing.T) {
	m := valid()
	m["AFF_MAX_CONCURRENT_RUNS"] = "7"
	m["AFF_SCHEDULE_JITTER"] = "90s"
	m["AFF_LOG_LEVEL"] = "debug"
	m["AFF_GENERATION_ENABLED"] = "false"

	c, err := Load(env(m))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.MaxConcurrentRuns != 7 {
		t.Errorf("MaxConcurrentRuns = %d, want 7", c.MaxConcurrentRuns)
	}
	if c.ScheduleJitter != 90*time.Second {
		t.Errorf("ScheduleJitter = %s, want 90s", c.ScheduleJitter)
	}
	if c.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %s, want debug", c.LogLevel)
	}
	if c.GenerationEnabled {
		t.Error("GenerationEnabled should be false")
	}
}

// RULE-2. A Secret must not reveal itself through any of the routes a value
// normally leaks by: fmt, %#v, or being logged as part of a larger struct.
func TestSecretNeverPrints(t *testing.T) {
	s := Secret("sk-thisisnotarealkey")

	// Routed through `any` on purpose. Calling fmt.Sprintf("%s", s) directly on
	// a Stringer trips staticcheck S1025 ("use String() instead") — but
	// exercising the fmt path is the entire point of this test, since that is
	// how a secret leaks in real code. The indirection keeps the assertion
	// honest without suppressing a rule that is right everywhere else.
	var boxed any = s

	for _, got := range []string{
		s.String(),
		s.GoString(),
		s.LogValue().String(),
		fmt.Sprintf("%v", boxed),
		fmt.Sprintf("%s", boxed),
		fmt.Sprintf("%#v", boxed),
		fmt.Sprintf("%v", struct{ K Secret }{s}),
		fmt.Sprintf("%+v", struct{ K Secret }{s}),
	} {
		if strings.Contains(got, "thisisnotarealkey") {
			t.Fatalf("secret leaked: %s", got)
		}
		if got == "" {
			t.Fatal("redacted form should still be a placeholder, not empty")
		}
	}

	if s.Reveal() != "sk-thisisnotarealkey" {
		t.Error("Reveal must return the underlying value")
	}
	if Secret("").String() != "[unset]" {
		t.Error("an empty secret should read as unset, not redacted")
	}
}

func TestConfigStructDoesNotLeakSecrets(t *testing.T) {
	m := valid()
	m["AFF_PASSWORD_PEPPER"] = "a-real-256-bit-secret-from-the-environment"
	m["AFF_PASSWORD_PEPPER_VERSION"] = "1"
	c, err := Load(env(m))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := fmt.Sprintf("%+v", *c)
	if strings.Contains(out, "not-a-real-key") {
		t.Fatalf("printing the whole Config leaked a secret:\n%s", out)
	}
	if strings.Contains(out, "a-real-256-bit-secret-from-the-environment") {
		t.Fatalf("printing the whole Config leaked the password pepper:\n%s", out)
	}
}
