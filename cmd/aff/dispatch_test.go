package main

import (
	"strings"
	"testing"
)

func TestUnknownTopLevelCommandExitsUsage(t *testing.T) {
	a, _, stderr := newTestApp()
	code := a.run([]string{"bogus"})
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), `unknown command "bogus"`) {
		t.Fatalf("stderr = %q, want it to name the unknown command", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("stderr = %q, want usage text", stderr.String())
	}
}

func TestNoArgsExitsUsage(t *testing.T) {
	a, _, stderr := newTestApp()
	code := a.run(nil)
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("stderr = %q, want usage text", stderr.String())
	}
}

func TestHelpExitsOK(t *testing.T) {
	a, stdout, _ := newTestApp()
	code := a.run([]string{"help"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("stdout = %q, want usage text", stdout.String())
	}
}

// TestUnknownGroupSubcommandExitsUsage covers every command-group
// dispatcher (feed/item/recipe/system/admin): an unrecognised or missing
// subcommand must exit non-zero with a usage message, never fall through to
// a network call.
func TestUnknownGroupSubcommandExitsUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"feed missing subcommand", []string{"feed"}},
		{"feed unknown subcommand", []string{"feed", "bogus"}},
		{"item missing subcommand", []string{"item"}},
		{"item unknown subcommand", []string{"item", "bogus"}},
		{"recipe missing subcommand", []string{"recipe"}},
		{"recipe unknown subcommand", []string{"recipe", "bogus"}},
		{"system missing subcommand", []string{"system"}},
		{"system unknown subcommand", []string{"system", "bogus"}},
		{"admin missing subcommand", []string{"admin"}},
		{"admin unknown subcommand", []string{"admin", "bogus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _, stderr := newTestApp()
			code := a.run(tc.args)
			if code != exitUsage {
				t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitUsage, stderr.String())
			}
			if stderr.Len() == 0 {
				t.Fatal("expected a usage message on stderr")
			}
		})
	}
}

// TestBadFlagExitsUsage checks flag-parsing failures propagate as exitUsage,
// not exitFail — a malformed invocation is a usage error, not a runtime one.
func TestBadFlagExitsUsage(t *testing.T) {
	a, _, _ := newTestApp()
	code := a.run([]string{"feed", "list", "--not-a-real-flag"})
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
}

// TestFeedGetRequiresSlugOrID exercises a positional/flag validation path
// that never reaches the network — the client should stay nil-safe.
func TestFeedGetRequiresSlugOrID(t *testing.T) {
	a, _, stderr := newTestApp()
	code := a.run([]string{"feed", "get"})
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "slug") {
		t.Fatalf("stderr = %q, want it to mention the missing slug/--id", stderr.String())
	}
}
