package bridge

import (
	"context"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/keepalive"
)

func alwaysValidValidator() SessionValidatorFunc {
	return func(context.Context, string, time.Time) (Session, error) {
		return Session{UserID: "cam"}, nil
	}
}

func baseTestConfig() Config {
	return Config{
		SessionCookieName: "session",
		AllowedOrigins:    []string{"https://admin.example.com"},
		Validator:         alwaysValidValidator(),
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(Config) Config
		wantErr bool
	}{
		{"valid", func(c Config) Config { return c }, false},
		{"missing cookie name", func(c Config) Config { c.SessionCookieName = ""; return c }, true},
		{"blank cookie name", func(c Config) Config { c.SessionCookieName = "   "; return c }, true},
		{"missing origins", func(c Config) Config { c.AllowedOrigins = nil; return c }, true},
		{"blank origin entry", func(c Config) Config { c.AllowedOrigins = []string{" "}; return c }, true},
		{"missing validator", func(c Config) Config { c.Validator = nil; return c }, true},
		{"negative revalidate interval", func(c Config) Config { c.RevalidateInterval = -1; return c }, true},
		{"negative max message bytes", func(c Config) Config { c.MaxMessageBytes = -1; return c }, true},
		{"negative max active connections", func(c Config) Config { c.MaxActiveConnections = -1; return c }, true},
		{"negative max connections per client", func(c Config) Config { c.MaxConnectionsPerClient = -1; return c }, true},
		{"negative max upgrades per client per minute", func(c Config) Config { c.MaxUpgradesPerClientPerMinute = -1; return c }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.mutate(baseTestConfig()).validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestConfigWithDefaults_Keepalive locks in the pairing rationale in
// server.go's comments: when the caller does not override keepalive
// settings, NewServer must use the policy paired against grpctunnel's
// client-side default cadence, not grpc-go's own zero-value defaults
// (5 minute MinTime, PermitWithoutStream: false) which would reproduce the
// GOAWAY flap PLAN.md §3 calls out.
func TestConfigWithDefaults_Keepalive(t *testing.T) {
	resolved := baseTestConfig().withDefaults()

	if resolved.KeepaliveEnforcementPolicy == nil || *resolved.KeepaliveEnforcementPolicy != defaultKeepaliveEnforcementPolicy {
		t.Fatalf("KeepaliveEnforcementPolicy = %+v, want %+v", resolved.KeepaliveEnforcementPolicy, defaultKeepaliveEnforcementPolicy)
	}
	if resolved.KeepaliveServerParams == nil || *resolved.KeepaliveServerParams != defaultKeepaliveServerParams {
		t.Fatalf("KeepaliveServerParams = %+v, want %+v", resolved.KeepaliveServerParams, defaultKeepaliveServerParams)
	}
	if resolved.RevalidateInterval != defaultRevalidateInterval {
		t.Fatalf("RevalidateInterval = %v, want %v", resolved.RevalidateInterval, defaultRevalidateInterval)
	}
	if _, ok := resolved.Clock.(RealClock); !ok {
		t.Fatalf("Clock default = %T, want RealClock", resolved.Clock)
	}
	if resolved.MaxMessageBytes != defaultMaxMessageBytes {
		t.Fatalf("MaxMessageBytes = %d, want %d", resolved.MaxMessageBytes, defaultMaxMessageBytes)
	}
	if resolved.MaxActiveConnections != defaultMaxActiveConnections {
		t.Fatalf("MaxActiveConnections = %d, want %d", resolved.MaxActiveConnections, defaultMaxActiveConnections)
	}
	if resolved.MaxConnectionsPerClient != defaultMaxConnectionsPerClient {
		t.Fatalf("MaxConnectionsPerClient = %d, want %d", resolved.MaxConnectionsPerClient, defaultMaxConnectionsPerClient)
	}
	if resolved.MaxUpgradesPerClientPerMinute != defaultMaxUpgradesPerClientPerMinute {
		t.Fatalf("MaxUpgradesPerClientPerMinute = %d, want %d", resolved.MaxUpgradesPerClientPerMinute, defaultMaxUpgradesPerClientPerMinute)
	}
}

// TestConfigWithDefaults_LimitsNotOverridden proves explicit caller-supplied
// limits survive withDefaults unchanged, the same additive-not-overwrite
// property TestConfigWithDefaults_CustomKeepaliveIsNotOverridden pins down
// for the keepalive fields.
func TestConfigWithDefaults_LimitsNotOverridden(t *testing.T) {
	cfg := baseTestConfig()
	cfg.MaxMessageBytes = 1
	cfg.MaxActiveConnections = 2
	cfg.MaxConnectionsPerClient = 3
	cfg.MaxUpgradesPerClientPerMinute = 4

	resolved := cfg.withDefaults()

	if resolved.MaxMessageBytes != 1 {
		t.Fatalf("MaxMessageBytes = %d, want caller override 1", resolved.MaxMessageBytes)
	}
	if resolved.MaxActiveConnections != 2 {
		t.Fatalf("MaxActiveConnections = %d, want caller override 2", resolved.MaxActiveConnections)
	}
	if resolved.MaxConnectionsPerClient != 3 {
		t.Fatalf("MaxConnectionsPerClient = %d, want caller override 3", resolved.MaxConnectionsPerClient)
	}
	if resolved.MaxUpgradesPerClientPerMinute != 4 {
		t.Fatalf("MaxUpgradesPerClientPerMinute = %d, want caller override 4", resolved.MaxUpgradesPerClientPerMinute)
	}
}

// TestConfigWithDefaults_CustomKeepaliveIsNotOverridden proves an explicit
// caller-supplied policy survives withDefaults unchanged — the defaulting
// must be additive, not a blanket overwrite.
func TestConfigWithDefaults_CustomKeepaliveIsNotOverridden(t *testing.T) {
	customPolicy := keepalive.EnforcementPolicy{MinTime: 5 * time.Second, PermitWithoutStream: false}
	customParams := keepalive.ServerParameters{Time: 10 * time.Second, Timeout: 5 * time.Second}

	cfg := baseTestConfig()
	cfg.KeepaliveEnforcementPolicy = &customPolicy
	cfg.KeepaliveServerParams = &customParams
	cfg.RevalidateInterval = 5 * time.Second

	resolved := cfg.withDefaults()

	if *resolved.KeepaliveEnforcementPolicy != customPolicy {
		t.Fatalf("KeepaliveEnforcementPolicy = %+v, want caller override %+v", resolved.KeepaliveEnforcementPolicy, customPolicy)
	}
	if *resolved.KeepaliveServerParams != customParams {
		t.Fatalf("KeepaliveServerParams = %+v, want caller override %+v", resolved.KeepaliveServerParams, customParams)
	}
	if resolved.RevalidateInterval != 5*time.Second {
		t.Fatalf("RevalidateInterval = %v, want caller override 5s", resolved.RevalidateInterval)
	}
}

// TestCheckOrigin is the exact-match unit test the ticket calls for by
// name: an allowlist entry must not match a request whose Origin merely has
// it as a prefix ("https://admin.example.com.evil.tld" is a distinct,
// attacker-controlled origin string — not a subdomain of
// admin.example.com).
func TestCheckOrigin(t *testing.T) {
	s := &server{allowedOrigins: map[string]struct{}{
		"https://admin.example.com": {},
	}}

	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{"exact match", "https://admin.example.com", true},
		{"trailing slash tolerated", "https://admin.example.com/", true},
		{"evil suffix must be refused", "https://admin.example.com.evil.tld", false},
		{"subdomain must be refused", "https://evil.admin.example.com", false},
		{"prefix-only host must be refused", "https://admin.example.co", false},
		{"scheme mismatch refused", "http://admin.example.com", false},
		{"port mismatch refused", "https://admin.example.com:8443", false},
		{"missing origin refused", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{Header: http.Header{}}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := s.checkOrigin(r); got != tc.want {
				t.Errorf("checkOrigin(%q) = %v, want %v", tc.origin, got, tc.want)
			}
		})
	}

	if s.checkOrigin(nil) {
		t.Errorf("checkOrigin(nil) = true, want false")
	}
}
