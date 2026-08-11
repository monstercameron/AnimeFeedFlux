package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"

	"google.golang.org/protobuf/proto"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

func (a *app) cmdSystem(args []string) int {
	choices := []string{"stats", "kill-switch", "backup", "settings", "version"}
	if len(args) == 0 {
		return a.missingSubcommand("system", choices...)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "stats":
		return a.cmdSystemStats(rest)
	case "kill-switch":
		return a.cmdSystemKillSwitch(rest)
	case "backup":
		return a.cmdSystemBackup(rest)
	case "settings":
		return a.cmdSystemSettings(rest)
	case "version":
		return a.cmdSystemVersion(rest)
	default:
		return a.unknownSubcommand("system", sub, choices...)
	}
}

func (a *app) cmdSystemStats(args []string) int {
	fs := a.newFlagSet("aff system stats")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system stats: %v\n", err)
		return exitFail
	}
	resp, err := cb.System.Stats(ctx, &affv1.SystemServiceStatsRequest{})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system stats: %v\n", err)
		return exitFail
	}
	if a.JSON {
		if err := a.printProtoJSON(resp); err != nil {
			fmt.Fprintln(a.Stderr, err)
			return exitFail
		}
		return exitOK
	}
	fmt.Fprintf(a.Stdout, "feeds:              %d (%d enabled)\n", resp.GetFeedCount(), resp.GetEnabledFeedCount())
	fmt.Fprintf(a.Stdout, "items:               %d\n", resp.GetItemCount())
	fmt.Fprintf(a.Stdout, "db size:             %d bytes\n", resp.GetDbSizeBytes())
	fmt.Fprintf(a.Stdout, "spend today:         $%.4f (estimate)\n", resp.GetTodaySpendUsd())
	fmt.Fprintf(a.Stdout, "remaining today:     $%.4f (estimate)\n", resp.GetTodayRemainingBudgetUsd())
	fmt.Fprintf(a.Stdout, "generation enabled:  %t\n", resp.GetGenerationEnabled())
	return exitOK
}

// cmdSystemKillSwitch implements `aff system kill-switch [on|off]`. With no
// argument it reports the current state (via Stats, which already surfaces
// it); with an argument it flips SystemService.SetGenerationEnabled — the
// global kill switch (PLAN.md §13): existing feeds keep serving, nothing new
// generates.
func (a *app) cmdSystemKillSwitch(args []string) int {
	fs := a.newFlagSet("aff system kill-switch")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(a.Stderr, "aff system kill-switch: want at most one argument (on or off)")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system kill-switch: %v\n", err)
		return exitFail
	}

	if fs.NArg() == 0 {
		resp, err := cb.System.Stats(ctx, &affv1.SystemServiceStatsRequest{})
		if err != nil {
			fmt.Fprintf(a.Stderr, "aff system kill-switch: %v\n", err)
			return exitFail
		}
		if a.JSON {
			_ = a.printJSON(map[string]any{"generation_enabled": resp.GetGenerationEnabled()})
		} else if resp.GetGenerationEnabled() {
			fmt.Fprintln(a.Stdout, "generation is ENABLED")
		} else {
			fmt.Fprintln(a.Stdout, "generation is DISABLED (kill switch is on)")
		}
		return exitOK
	}

	var enabled bool
	switch args := fs.Arg(0); args {
	case "on":
		enabled = true
	case "off":
		enabled = false
	default:
		fmt.Fprintf(a.Stderr, "aff system kill-switch: want \"on\" or \"off\", got %q\n", args)
		return exitUsage
	}

	resp, err := cb.System.SetGenerationEnabled(ctx, &affv1.SystemServiceSetGenerationEnabledRequest{Enabled: enabled})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system kill-switch: %v\n", err)
		return exitFail
	}
	if a.JSON {
		_ = a.printJSON(map[string]any{"generation_enabled": resp.GetEnabled()})
	} else if resp.GetEnabled() {
		fmt.Fprintln(a.Stdout, "generation is now ENABLED")
	} else {
		fmt.Fprintln(a.Stdout, "generation is now DISABLED (kill switch is on)")
	}
	return exitOK
}

func (a *app) cmdSystemBackup(args []string) int {
	fs := a.newFlagSet("aff system backup")
	out := fs.String("out", "", "write the backup to this file (required — the DB file is binary, not printable)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *out == "" {
		fmt.Fprintln(a.Stderr, "aff system backup: --out is required")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system backup: %v\n", err)
		return exitFail
	}
	resp, err := cb.System.Backup(ctx, &affv1.SystemServiceBackupRequest{})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system backup: %v\n", err)
		return exitFail
	}
	if err := os.WriteFile(*out, resp.GetDbFile(), 0o600); err != nil {
		fmt.Fprintf(a.Stderr, "aff system backup: writing %s: %v\n", *out, err)
		return exitFail
	}
	if a.JSON {
		_ = a.printJSON(map[string]any{"filename": resp.GetFilename(), "bytes": len(resp.GetDbFile()), "wrote": *out})
	} else {
		fmt.Fprintf(a.Stdout, "Wrote %d bytes to %s (server filename: %s).\n", len(resp.GetDbFile()), *out, resp.GetFilename())
	}
	return exitOK
}

func (a *app) cmdSystemVersion(args []string) int {
	fs := a.newFlagSet("aff system version")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system version: %v\n", err)
		return exitFail
	}
	resp, err := cb.System.Version(ctx, &affv1.SystemServiceVersionRequest{})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system version: %v\n", err)
		return exitFail
	}
	if a.JSON {
		if err := a.printProtoJSON(resp); err != nil {
			fmt.Fprintln(a.Stderr, err)
			return exitFail
		}
		return exitOK
	}
	fmt.Fprintf(a.Stdout, "version: %s\nbuild:   %s\n", resp.GetVersion(), resp.GetBuild())
	return exitOK
}

// cmdSystemSettings implements `aff system settings get|set` — the CLI's
// only way to read or change SystemService's Provider/Generation/Publishing
// settings (PLAN.md §12.5, §13) without the browser admin UI, which today
// has never been loaded. Follows the "stats"/"kill-switch" conventions in
// this file exactly: same flag set, same auth path, same JSON/human output
// split, same exit codes.
func (a *app) cmdSystemSettings(args []string) int {
	choices := []string{"get", "set"}
	if len(args) == 0 {
		return a.missingSubcommand("system settings", choices...)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "get":
		return a.cmdSystemSettingsGet(rest)
	case "set":
		return a.cmdSystemSettingsSet(rest)
	default:
		return a.unknownSubcommand("system settings", sub, choices...)
	}
}

// cmdSystemSettingsGet reads and prints the full settings singleton. The
// provider section's API key is never fetched or forwarded — the proto
// response only ever carries ApiKeyPresent, a live-computed boolean
// (internal/rpc/system.go's sysLoadProvider) — so nothing here needs to mask
// anything: there is no key field to accidentally print.
func (a *app) cmdSystemSettingsGet(args []string) int {
	fs := a.newFlagSet("aff system settings get")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system settings get: %v\n", err)
		return exitFail
	}
	resp, err := cb.System.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system settings get: %v\n", err)
		return exitFail
	}
	if a.JSON {
		if err := a.printProtoJSON(resp); err != nil {
			fmt.Fprintln(a.Stderr, err)
			return exitFail
		}
		return exitOK
	}
	printSettingsHuman(a.Stdout, resp.GetSettings())
	return exitOK
}

// printSettingsHuman renders the three settings sections the way
// cmdSystemStats renders stats: fixed labels, no table library. Provider's
// price table is summarised as a count in this view — editing it is not
// exposed by this CLI (see cmdSystemSettingsSet's doc), and printing every
// row here would bury the fields an operator actually came to change; the
// full table is still available via --json.
func printSettingsHuman(w interface{ Write([]byte) (int, error) }, s *affv1.Settings) {
	p := s.GetProvider()
	fmt.Fprintln(w, "provider:")
	fmt.Fprintf(w, "  active provider:    %s\n", p.GetActiveProvider())
	fmt.Fprintf(w, "  default model:      %s\n", p.GetDefaultModel())
	fmt.Fprintf(w, "  embedding model:    %s\n", p.GetEmbeddingModel())
	fmt.Fprintf(w, "  api key present:    %t\n", p.GetApiKeyPresent())
	fmt.Fprintf(w, "  price table:        %d entries (see --json for detail)\n", len(p.GetPriceTable()))

	g := s.GetGeneration()
	fmt.Fprintln(w, "generation:")
	fmt.Fprintf(w, "  generation enabled:          %t (change with: aff system kill-switch on|off)\n", g.GetEnabled())
	fmt.Fprintf(w, "  global daily token ceiling:  %d\n", g.GetGlobalDailyTokenCeiling())
	fmt.Fprintf(w, "  global daily spend ceiling:  $%.4f\n", g.GetGlobalDailySpendCeilingUsd())
	fmt.Fprintf(w, "  default daily token budget:  %d\n", g.GetDefaultDailyTokenBudget())
	fmt.Fprintf(w, "  default daily run budget:    %d\n", g.GetDefaultDailyRunBudget())
	fmt.Fprintf(w, "  default feed window:         %d\n", g.GetDefaultFeedWindow())
	fmt.Fprintf(w, "  staleness threshold minutes: %d\n", g.GetStalenessThresholdMinutes())

	pub := s.GetPublishing()
	fmt.Fprintln(w, "publishing:")
	fmt.Fprintf(w, "  public base url:       %s\n", pub.GetPublicBaseUrl())
	fmt.Fprintf(w, "  default author:        %s\n", pub.GetDefaultAuthor())
	fmt.Fprintf(w, "  default contact:       %s\n", pub.GetDefaultContact())
	fmt.Fprintf(w, "  default copyright:     %s\n", pub.GetDefaultCopyright())
	fmt.Fprintf(w, "  default ttl minutes:   %d\n", pub.GetDefaultTtlMinutes())
	fmt.Fprintf(w, "  default cache control: %s\n", pub.GetDefaultCacheControl())
	fmt.Fprintf(w, "  default og image:      %s\n", pub.GetDefaultOgImage())
}

// validateSettingsBaseURL mirrors internal/rpc/system.go's
// sysValidateBaseURL exactly (absolute URL, http or https scheme) so a bad
// base URL fails here, locally, with the same rule the server would apply —
// PLAN.md §12.5: "these land in every channel element, so they are
// validated (absolute URL, correct scheme) on save." Duplicated rather than
// imported because internal/rpc is server-side package the CLI has no other
// reason to depend on, and the rule is three lines long.
func validateSettingsBaseURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("public_base_url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("public_base_url: %w", err)
	}
	if !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("public_base_url must be an absolute URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("public_base_url: scheme %q must be http or https", u.Scheme)
	}
	return nil
}

// settingsChange is one touched field's before/after value, for the "show
// what changed" output both cmdSystemSettingsSet output paths share.
type settingsChange struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

// reconcileSettingsChanges overwrites each change's After with the value
// UpdateSettings actually echoed back, keyed by proto field name. Unknown
// field names are left as whatever cmdSystemSettingsSet already put there
// (defensive only — every entry it appends uses one of these names).
func reconcileSettingsChanges(changes []settingsChange, after *affv1.Settings) {
	g := after.GetGeneration()
	pub := after.GetPublishing()
	for i := range changes {
		switch changes[i].Field {
		case "global_daily_spend_ceiling_usd":
			changes[i].After = g.GetGlobalDailySpendCeilingUsd()
		case "global_daily_token_ceiling":
			changes[i].After = g.GetGlobalDailyTokenCeiling()
		case "default_daily_token_budget":
			changes[i].After = g.GetDefaultDailyTokenBudget()
		case "default_daily_run_budget":
			changes[i].After = g.GetDefaultDailyRunBudget()
		case "default_feed_window":
			changes[i].After = g.GetDefaultFeedWindow()
		case "staleness_threshold_minutes":
			changes[i].After = g.GetStalenessThresholdMinutes()
		case "public_base_url":
			changes[i].After = pub.GetPublicBaseUrl()
		case "default_author":
			changes[i].After = pub.GetDefaultAuthor()
		case "default_contact":
			changes[i].After = pub.GetDefaultContact()
		case "default_copyright":
			changes[i].After = pub.GetDefaultCopyright()
		case "default_ttl_minutes":
			changes[i].After = pub.GetDefaultTtlMinutes()
		case "default_cache_control":
			changes[i].After = pub.GetDefaultCacheControl()
		case "default_og_image":
			changes[i].After = pub.GetDefaultOgImage()
		}
	}
}

// cmdSystemSettingsSet flips one or more Generation/Publishing fields.
//
// UpdateSettings replaces each *section* wholesale (internal/rpc/system.go:
// "UpdateSettings replaces only the sections present in the request" — but
// within a present section, every field of that section's message is
// written, not merged). Sending a Generation message with only
// global_daily_spend_ceiling_usd set would silently zero
// default_daily_token_budget, default_daily_run_budget, default_feed_window
// and staleness_threshold_minutes — turning "raise the spend ceiling" into
// "reset every generation default" with nothing to show for it until the
// next run. So this command always does read-modify-write: fetch the
// current settings, clone the touched section(s), overwrite only the
// field(s) whose flag was actually passed (detected via fs.Visit, not a
// zero-value check — 0 is a valid budget), and send the whole
// mutated section back.
//
// Provider settings (active_provider, default_model, embedding_model,
// price_table) are read-only from this command. price_table is a repeated
// structured message with no clean flag-per-row shape, and the scalar
// provider fields are cheap to add later but out of scope for what PLAN.md
// §12.5/§13 flagged as the gap (spend ceiling, default budgets, staleness,
// publishing defaults) — see the CLI's help text. Generation.enabled is
// deliberately excluded from the settable flags here too: `aff system
// kill-switch on|off` already owns that field via SetGenerationEnabled, and
// giving it a second mutation path here would let the two commands race
// each other's read-modify-write with no coordination.
//
// No interactive confirmation, even for the spend ceiling, despite that
// being how an accidental raise turns into real money: every other gRPC
// mutation in this file (feed delete, kill-switch, feed update) already
// skips confirmation and relies on scriptability plus visible before/after
// output instead — confirm.go's prompt is reserved for the LOCAL-ONLY
// filesystem commands (backup/restore/prune/crypt) where the risk is
// destroying the only copy of the database, not for gRPC calls the server
// itself can be asked to reverse. Adding a prompt here only on this one
// flag would be an inconsistent special case; the mitigation instead is
// making the change loud — printed before/after, non-zero exit on failure,
// and --json for scripts that want to log it.
func (a *app) cmdSystemSettingsSet(args []string) int {
	fs := a.newFlagSet("aff system settings set")

	baseURL := fs.String("base-url", "", "public base URL for links/guids/atom:link rel=self (absolute, http or https)")
	author := fs.String("author", "", "default feed author")
	contact := fs.String("contact", "", "default feed contact")
	copyrightLine := fs.String("copyright", "", "default copyright line")
	ttlMinutes := fs.Int("ttl-minutes", 0, "default channel TTL in minutes")
	cacheControl := fs.String("cache-control", "", "default Cache-Control header value")
	ogImage := fs.String("og-image", "", "default og:image URL")

	spendCeiling := fs.Float64("spend-ceiling-usd", 0, "global daily spend ceiling in USD (0 disables the ceiling)")
	tokenCeiling := fs.Int64("token-ceiling", 0, "global daily token ceiling (0 disables the ceiling)")
	defaultTokenBudget := fs.Int64("default-token-budget", 0, "default per-feed daily token budget for new feeds")
	defaultRunBudget := fs.Int("default-run-budget", 0, "default per-feed daily run budget for new feeds")
	defaultFeedWindow := fs.Int("default-feed-window", 0, "default feed window (item count) for new feeds")
	stalenessMinutes := fs.Int("staleness-minutes", 0, "minutes past which a feed is flagged stale")

	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff system settings set: takes flags only, no positional arguments")
		return exitUsage
	}

	touched := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { touched[f.Name] = true })

	publishingTouched := touched["base-url"] || touched["author"] || touched["contact"] ||
		touched["copyright"] || touched["ttl-minutes"] || touched["cache-control"] || touched["og-image"]
	generationTouched := touched["spend-ceiling-usd"] || touched["token-ceiling"] ||
		touched["default-token-budget"] || touched["default-run-budget"] ||
		touched["default-feed-window"] || touched["staleness-minutes"]

	if !publishingTouched && !generationTouched {
		fmt.Fprintln(a.Stderr, "aff system settings set: want at least one setting flag (see -h)")
		return exitUsage
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system settings set: %v\n", err)
		return exitFail
	}

	getResp, err := cb.System.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system settings set: reading current settings: %v\n", err)
		return exitFail
	}
	current := getResp.GetSettings()

	var changes []settingsChange
	req := &affv1.SystemServiceUpdateSettingsRequest{Settings: &affv1.Settings{}}

	if generationTouched {
		before := current.GetGeneration()
		g, _ := proto.Clone(before).(*affv1.Settings_Generation)
		if g == nil {
			g = &affv1.Settings_Generation{}
		}
		if touched["spend-ceiling-usd"] {
			changes = append(changes, settingsChange{"global_daily_spend_ceiling_usd", before.GetGlobalDailySpendCeilingUsd(), *spendCeiling})
			g.GlobalDailySpendCeilingUsd = *spendCeiling
		}
		if touched["token-ceiling"] {
			changes = append(changes, settingsChange{"global_daily_token_ceiling", before.GetGlobalDailyTokenCeiling(), *tokenCeiling})
			g.GlobalDailyTokenCeiling = *tokenCeiling
		}
		if touched["default-token-budget"] {
			changes = append(changes, settingsChange{"default_daily_token_budget", before.GetDefaultDailyTokenBudget(), *defaultTokenBudget})
			g.DefaultDailyTokenBudget = *defaultTokenBudget
		}
		if touched["default-run-budget"] {
			changes = append(changes, settingsChange{"default_daily_run_budget", before.GetDefaultDailyRunBudget(), int32(*defaultRunBudget)})
			g.DefaultDailyRunBudget = int32(*defaultRunBudget)
		}
		if touched["default-feed-window"] {
			changes = append(changes, settingsChange{"default_feed_window", before.GetDefaultFeedWindow(), int32(*defaultFeedWindow)})
			g.DefaultFeedWindow = int32(*defaultFeedWindow)
		}
		if touched["staleness-minutes"] {
			changes = append(changes, settingsChange{"staleness_threshold_minutes", before.GetStalenessThresholdMinutes(), int32(*stalenessMinutes)})
			g.StalenessThresholdMinutes = int32(*stalenessMinutes)
		}
		// enabled is intentionally carried over unchanged (see doc comment):
		// this section is a whole-message replace, so the current value
		// must be re-sent or SetGenerationEnabled's last write would be
		// wiped the next time an operator touches any other Generation
		// field.
		g.Enabled = before.GetEnabled()
		req.Settings.Generation = g
	}

	if publishingTouched {
		before := current.GetPublishing()
		p, _ := proto.Clone(before).(*affv1.Settings_Publishing)
		if p == nil {
			p = &affv1.Settings_Publishing{}
		}
		if touched["base-url"] {
			changes = append(changes, settingsChange{"public_base_url", before.GetPublicBaseUrl(), *baseURL})
			p.PublicBaseUrl = *baseURL
		}
		if touched["author"] {
			changes = append(changes, settingsChange{"default_author", before.GetDefaultAuthor(), *author})
			p.DefaultAuthor = *author
		}
		if touched["contact"] {
			changes = append(changes, settingsChange{"default_contact", before.GetDefaultContact(), *contact})
			p.DefaultContact = *contact
		}
		if touched["copyright"] {
			changes = append(changes, settingsChange{"default_copyright", before.GetDefaultCopyright(), *copyrightLine})
			p.DefaultCopyright = *copyrightLine
		}
		if touched["ttl-minutes"] {
			changes = append(changes, settingsChange{"default_ttl_minutes", before.GetDefaultTtlMinutes(), int32(*ttlMinutes)})
			p.DefaultTtlMinutes = int32(*ttlMinutes)
		}
		if touched["cache-control"] {
			changes = append(changes, settingsChange{"default_cache_control", before.GetDefaultCacheControl(), *cacheControl})
			p.DefaultCacheControl = *cacheControl
		}
		if touched["og-image"] {
			changes = append(changes, settingsChange{"default_og_image", before.GetDefaultOgImage(), *ogImage})
			p.DefaultOgImage = *ogImage
		}

		// Validate locally, on the FINAL effective value (whether base-url
		// itself was touched or only carried over from the current
		// section) — §12.5's rule applies to whatever public_base_url ends
		// up in this request, and UpdateSettings would apply exactly this
		// check server-side (internal/rpc/system.go's sysValidateBaseURL)
		// after a full round trip. Failing here means a typo in --author
		// never even reaches the network if the base URL was never
		// configured, rather than surfacing as a confusing "public
		// base_url is required" from a command that only touched --author.
		if err := validateSettingsBaseURL(p.GetPublicBaseUrl()); err != nil {
			fmt.Fprintf(a.Stderr, "aff system settings set: %v\n", err)
			return exitUsage
		}

		req.Settings.Publishing = p
	}

	updResp, err := cb.System.UpdateSettings(ctx, req)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff system settings set: %v\n", err)
		return exitFail
	}
	// Reconcile "after" against what the server actually persisted and
	// echoed back, not merely what this process sent — the two should
	// always agree on success, but showing the operator the server's own
	// account is strictly more trustworthy than replaying our own request.
	reconcileSettingsChanges(changes, updResp.GetSettings())

	if a.JSON {
		if err := a.printJSON(map[string]any{"changed": changes}); err != nil {
			fmt.Fprintln(a.Stderr, err)
			return exitFail
		}
		return exitOK
	}
	if len(changes) == 0 {
		fmt.Fprintln(a.Stdout, "nothing changed")
		return exitOK
	}
	for _, c := range changes {
		fmt.Fprintf(a.Stdout, "%-32s %v -> %v\n", c.Field+":", c.Before, c.After)
	}
	return exitOK
}
