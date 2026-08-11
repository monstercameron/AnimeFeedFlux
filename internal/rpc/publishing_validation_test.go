package rpc

import (
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Publishing validation covered public_base_url and nothing else: TTL could
// be saved negative, and og:image accepted any string at all (A5-28).
func savePublishing(t *testing.T, s *SystemServer, p *affv1.Settings_Publishing) error {
	t.Helper()
	_, err := s.UpdateSettings(t.Context(), &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{Publishing: p},
	})
	return err
}

func validPublishing() *affv1.Settings_Publishing {
	return &affv1.Settings_Publishing{PublicBaseUrl: "https://feeds.example.com"}
}

func TestPublishingRejectsNonsenseTTL(t *testing.T) {
	s := NewSystemServer(sysTestStore(t), nil)

	p := validPublishing()
	p.DefaultTtlMinutes = -5
	err := savePublishing(t, s, p)
	if err == nil {
		t.Fatal("a negative TTL was accepted; it renders as <ttl>-5</ttl>")
	}
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}

	p.DefaultTtlMinutes = sysPublishingTTLMax + 1
	if err := savePublishing(t, s, p); err == nil {
		t.Error("a TTL past the ceiling was accepted")
	}

	// The boundary and zero are both fine: zero means "unset".
	for _, ttl := range []int32{0, 15, sysPublishingTTLMax} {
		p.DefaultTtlMinutes = ttl
		if err := savePublishing(t, s, p); err != nil {
			t.Errorf("TTL %d was refused: %v", ttl, err)
		}
	}
}

func TestPublishingRejectsUnfetchableOgImage(t *testing.T) {
	s := NewSystemServer(sysTestStore(t), nil)

	for _, bad := range []string{
		"javascript:alert(1)", // emitted into og:image in the page head
		"not a url at all",
		"/relative/path.png",
		"ftp://example.com/img.png",
	} {
		p := validPublishing()
		p.DefaultOgImage = bad
		if err := savePublishing(t, s, p); err == nil {
			t.Errorf("og:image %q was accepted", bad)
		}
	}

	// Absent and absolute-http(s) are both fine.
	for _, ok := range []string{"", "https://cdn.example.com/og.png", "http://example.com/og.png"} {
		p := validPublishing()
		p.DefaultOgImage = ok
		if err := savePublishing(t, s, p); err != nil {
			t.Errorf("og:image %q was refused: %v", ok, err)
		}
	}
}

// Now that public_base_url actually reaches the publish plane, a stored
// trailing slash would double-slash every guid, atom:link and subscribe URL
// at once. It is normalised on save so there is one canonical form in the
// database rather than every consumer remembering to trim.
func TestPublishingStripsTrailingSlashOnSave(t *testing.T) {
	s := NewSystemServer(sysTestStore(t), nil)

	p := validPublishing()
	p.PublicBaseUrl = "https://feeds.example.com/"
	if err := savePublishing(t, s, p); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	resp, err := s.GetSettings(t.Context(), &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got, want := resp.GetSettings().GetPublishing().GetPublicBaseUrl(), "https://feeds.example.com"; got != want {
		t.Errorf("stored base URL = %q, want %q", got, want)
	}
}

// A rejected publishing section must leave the rest of the request unwritten
// — the reason validation runs before the transaction opens.
func TestPublishingRejectionLeavesOtherSectionsAlone(t *testing.T) {
	s := NewSystemServer(sysTestStore(t), nil)

	before, err := s.GetSettings(t.Context(), &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	wantEffort := before.GetSettings().GetProvider().GetEffort()

	bad := validPublishing()
	bad.DefaultTtlMinutes = -1
	_, err = s.UpdateSettings(t.Context(), &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{
			Publishing: bad,
			Provider:   &affv1.Settings_Provider{Effort: "fast"},
		},
	})
	if err == nil {
		t.Fatal("the request was accepted despite an invalid TTL")
	}
	after, err := s.GetSettings(t.Context(), &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got := after.GetSettings().GetProvider().GetEffort(); got != wantEffort {
		t.Errorf("provider effort became %q on a REJECTED request; it was half-applied", got)
	}
}
