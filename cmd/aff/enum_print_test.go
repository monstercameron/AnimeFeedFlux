package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// The flag parsers are the CLI's entire input validation for these values. A
// silent mis-parse is the dangerous direction: `--deleted only` quietly
// meaning "exclude" would show an operator an empty list and let them
// conclude the items are gone.

func TestParseFeedKind(t *testing.T) {
	ok := map[string]affv1.FeedKind{
		"":            affv1.FeedKind_FEED_KIND_GENERATIVE, // the default
		"generative":  affv1.FeedKind_FEED_KIND_GENERATIVE,
		"GENERATIVE":  affv1.FeedKind_FEED_KIND_GENERATIVE,
		"  grounded ": affv1.FeedKind_FEED_KIND_GROUNDED,
		"aggregate":   affv1.FeedKind_FEED_KIND_AGGREGATE,
	}
	for in, want := range ok {
		got, err := parseFeedKind(in)
		if err != nil {
			t.Errorf("parseFeedKind(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("parseFeedKind(%q) = %v, want %v", in, got, want)
		}
	}
	got, err := parseFeedKind("aggregated")
	if err == nil {
		t.Fatalf("a near-miss spelling was accepted as %v", got)
	}
	// The error has to list the choices: the whole point of rejecting is to
	// tell the operator what to type instead.
	for _, want := range []string{"generative", "grounded", "aggregate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not offer %q", err, want)
		}
	}
}

func TestParseOrigin(t *testing.T) {
	ok := map[string]affv1.Origin{
		"":            affv1.Origin_ORIGIN_UNSPECIFIED, // "no filter", not a value
		"generated":   affv1.Origin_ORIGIN_GENERATED,
		"sampled":     affv1.Origin_ORIGIN_SAMPLED,
		"MANUAL":      affv1.Origin_ORIGIN_MANUAL,
		" correction": affv1.Origin_ORIGIN_CORRECTION,
	}
	for in, want := range ok {
		got, err := parseOrigin(in)
		if err != nil {
			t.Errorf("parseOrigin(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("parseOrigin(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := parseOrigin("imported"); err == nil {
		t.Error("an unknown origin was accepted")
	}
}

func TestParseRunStatus(t *testing.T) {
	ok := map[string]affv1.RunStatus{
		"":          affv1.RunStatus_RUN_STATUS_UNSPECIFIED,
		"running":   affv1.RunStatus_RUN_STATUS_RUNNING,
		"succeeded": affv1.RunStatus_RUN_STATUS_SUCCEEDED,
		"failed":    affv1.RunStatus_RUN_STATUS_FAILED,
		"Skipped":   affv1.RunStatus_RUN_STATUS_SKIPPED,
	}
	for in, want := range ok {
		got, err := parseRunStatus(in)
		if err != nil {
			t.Errorf("parseRunStatus(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("parseRunStatus(%q) = %v, want %v", in, got, want)
		}
	}
	// "success" is what the DATABASE calls it; the CLI's word is
	// "succeeded". Accepting both silently would leave two spellings of the
	// same filter in scripts, and only one of them documented.
	if _, err := parseRunStatus("success"); err == nil {
		t.Error("the database's spelling was accepted as a CLI value")
	}
}

func TestParseDeletedFilter(t *testing.T) {
	ok := map[string]affv1.DeletedFilter{
		"":        affv1.DeletedFilter_DELETED_FILTER_EXCLUDE_DELETED,
		"exclude": affv1.DeletedFilter_DELETED_FILTER_EXCLUDE_DELETED,
		"only":    affv1.DeletedFilter_DELETED_FILTER_ONLY_DELETED,
		"ALL":     affv1.DeletedFilter_DELETED_FILTER_INCLUDE_ALL,
	}
	for in, want := range ok {
		got, err := parseDeletedFilter(in)
		if err != nil {
			t.Errorf("parseDeletedFilter(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("parseDeletedFilter(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := parseDeletedFilter("include"); err == nil {
		t.Error("an unknown deleted filter was accepted")
	}
}

// --- printers ---------------------------------------------------------------

func TestPrintFeedHumanAndJSON(t *testing.T) {
	feed := &affv1.Feed{
		Id: 7, Slug: "trivia-daily", Title: "Daily Trivia",
		Kind: affv1.FeedKind_FEED_KIND_GENERATIVE, Enabled: true, Version: 3,
	}

	a, stdout, _ := newTestApp()
	if code := a.printFeed(feed); code != exitOK {
		t.Fatalf("printFeed exit code = %d", code)
	}
	out := stdout.String()
	for _, want := range []string{"trivia-daily", "Daily Trivia", "7", "true", "3"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output is missing %q:\n%s", want, out)
		}
	}

	// --json is what scripts read, so it must be parseable, not
	// human-formatted-with-quotes.
	a, stdout, _ = newTestApp()
	a.JSON = true
	if code := a.printFeed(feed); code != exitOK {
		t.Fatalf("printFeed --json exit code = %d", code)
	}
	var decoded map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, stdout.String())
	}
	if decoded["slug"] != "trivia-daily" {
		t.Errorf("--json output = %v, want the slug in it", decoded)
	}
}

func TestPrintItemHumanAndJSON(t *testing.T) {
	item := &affv1.Item{
		Id: 11, FeedId: 7, ItemKey: "2026-08-11-one", Title: "An item",
		Origin: affv1.Origin_ORIGIN_GENERATED, Link: "https://example.com/x", Version: 2,
	}

	a, stdout, _ := newTestApp()
	if code := a.printItem(item); code != exitOK {
		t.Fatalf("printItem exit code = %d", code)
	}
	out := stdout.String()
	for _, want := range []string{"2026-08-11-one", "An item", "https://example.com/x"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output is missing %q:\n%s", want, out)
		}
	}

	a, stdout, _ = newTestApp()
	a.JSON = true
	if code := a.printItem(item); code != exitOK {
		t.Fatalf("printItem --json exit code = %d", code)
	}
	var decoded map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, stdout.String())
	}
	if decoded["itemKey"] != "2026-08-11-one" && decoded["item_key"] != "2026-08-11-one" {
		t.Errorf("--json output = %v, want the item key in it", decoded)
	}
}

func TestBackupDirComesFromTheSameEnvVarAsTheServer(t *testing.T) {
	// An operator with the container's environment sourced must get the same
	// default the service uses; a CLI that defaulted somewhere else would
	// write backups the service's retention never prunes.
	a, _, _ := newTestApp()
	t.Setenv("AFF_BACKUP_DIR", "")
	if got := a.backupDir(); got != "" {
		t.Errorf("backupDir() = %q with the variable unset, want empty", got)
	}
	t.Setenv("AFF_BACKUP_DIR", "/var/backups/aff")
	if got := a.backupDir(); got != "/var/backups/aff" {
		t.Errorf("backupDir() = %q, want the value of AFF_BACKUP_DIR", got)
	}
	if os.Getenv("AFF_BACKUP_DIR") != "/var/backups/aff" {
		t.Error("t.Setenv did not take effect")
	}
}

func TestErrStringIsEmptyForNoError(t *testing.T) {
	// It feeds a report field; "<nil>" printed as a failure reason would be
	// read as an actual failure.
	if got := errString(nil); got != "" {
		t.Errorf("errString(nil) = %q, want empty", got)
	}
	if got := errString(errRPCBoom); got != "boom" {
		t.Errorf("errString = %q, want the message", got)
	}
}
