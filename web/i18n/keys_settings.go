package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// Settings-namespace catalogue (TODOS.md D6-13, PLAN.md §12.5). Populated
// from the literal key strings web/pages/settings already passes to its own
// Translator interface (i18n.go: "this package must not import web/i18n")
// — grepped from render.go, render_security.go, render_publishing.go,
// render_provider.go, render_generation.go, render_data.go, render_about.go,
// deps.go and confirm.go.
//
// Same "generate."/"history."-prefix-kept-in-the-key rationale as
// keys_generate.go applies here (see that file's doc comment) — this
// package's own call sites already hardcode the full "settings.xxx"
// string.
//
// Positional args use the arg1/arg2/... convention from keys_common.go.
//
// One deliberate omission: the three confirmModal PromptKey values
// (settings.security.recoveryCodes.regenerate.prompt,
// settings.security.sessions.revokeAll.prompt,
// settings.data.recipe.import.prompt) are rendered via render.go's
// `t(props.PromptKey)` — called with NO args — unlike history's equivalent
// (which interpolates {word}). Their Text is therefore self-contained
// prose, not a {word}-shaped template; do not add a placeholder here that
// the current call site never fills.
var settingsMessages = gwci18n.NamespaceCatalog{
	"settings.title":            {Text: "Settings"},
	"settings.notWired.message": {Text: "This page is not yet wired to live data."},
	"settings.nav.label":        {Text: "Settings sections"},
	"settings.nav.security":     {Text: "Security"},
	"settings.nav.provider":     {Text: "Provider"},
	"settings.nav.generation":   {Text: "Generation"},
	"settings.nav.publishing":   {Text: "Publishing"},
	"settings.nav.data":         {Text: "Data"},
	"settings.nav.about":        {Text: "About"},
	"settings.nav.unknown":      {Text: "Unknown section"},

	// Shared six-state-matrix text (render.go's screenWrapper, now backed by
	// web/ui.StatePanel — TODOS.md D0-13/D0-14/D6-13). StatePanel's own
	// loading/empty/disabled-label/disconnected copy is the common.state.*
	// catalogue (web/i18n/keys_common.go) shared by every page; these two
	// entries are the settings-specific pieces StatePanelProps lets a caller
	// customize: the interpolated error detail, and the (currently generic,
	// since no call site sets ScreenInputs.DisabledReason to a real message
	// yet — see render.go's screenWrapper doc comment) disabled-panel reason.
	"settings.common.state.errorDetail":     {Text: "Something went wrong: {arg1}"},
	"settings.common.state.disabledGeneric": {Text: "This panel is currently unavailable."},
	"settings.common.yes":                   {Text: "Yes"},
	"settings.common.no":                    {Text: "No"},

	// Security section (render_security.go, confirm.go).
	"settings.security.title":                                      {Text: "Security"},
	"settings.security.sessions.current":                           {Text: "{arg1}"},
	"settings.security.passwordPolicy.hint":                        {Text: "Between {arg1} and {arg2} characters."},
	"settings.security.changePassword.title":                       {Text: "Change password"},
	"settings.security.changePassword.current":                     {Text: "Current password"},
	"settings.security.changePassword.totp":                        {Text: "6-digit code"},
	"settings.security.changePassword.new":                         {Text: "New password"},
	"settings.security.changePassword.error":                       {Text: "Couldn't change password. Check your details and try again."},
	"settings.security.changePassword.success":                     {Text: "Password changed."},
	"settings.security.changePassword.submit":                      {Text: "Change password"},
	"settings.security.changePassword.revokesOtherSessionsWarning": {Text: "Changing your password ends every other active session."},
	"settings.security.reenrollTotp.title":                         {Text: "Re-enroll two-factor authentication"},
	"settings.security.reenrollTotp.currentPassword":               {Text: "Current password"},
	"settings.security.reenrollTotp.error":                         {Text: "Couldn't re-enroll. Check your password and try again."},
	"settings.security.reenrollTotp.submit":                        {Text: "Re-enroll"},
	"settings.security.reenrollTotp.shownOnce":                     {Text: "Scan this into your authenticator app now — it will not be shown again."},
	"settings.security.recoveryCodes.title":                        {Text: "Recovery codes"},
	"settings.security.recoveryCodes.remaining": {
		PluralArg: "arg1",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "{arg1} recovery code remaining.",
			gwci18n.PluralOther: "{arg1} recovery codes remaining.",
		},
	},
	"settings.security.recoveryCodes.lowNag":                  {Text: "Running low on recovery codes — regenerate a fresh set."},
	"settings.security.recoveryCodes.regenerate.action":       {Text: "Regenerate"},
	"settings.security.recoveryCodes.regenerate.confirmTitle": {Text: "Regenerate recovery codes"},
	"settings.security.recoveryCodes.regenerate.prompt":       {Text: "Type the confirmation phrase shown below to regenerate your recovery codes. Every existing code stops working immediately."},
	"settings.security.recoveryCodes.regenerate.confirmWord":  {Text: "REGENERATE"},
	"settings.security.recoveryCodes.error":                   {Text: "Couldn't regenerate recovery codes."},
	"settings.security.recoveryCodes.shownOnce":               {Text: "These codes will not be shown again — save them now."},
	"settings.security.sessions.title":                        {Text: "Active sessions"},
	"settings.security.sessions.caption":                      {Text: "Active sessions"},
	"settings.security.sessions.revoked":                      {Text: "Revoked"},
	// {arg1} = how many older revoked sessions are not listed.
	"settings.security.sessions.hiddenRevoked": {
		PluralArg: "arg1",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "{arg1} older revoked session is not shown.",
			gwci18n.PluralOther: "{arg1} older revoked sessions are not shown.",
		},
	},
	"settings.security.sessions.revoke":                 {Text: "Revoke"},
	"settings.security.sessions.revokeAll.action":       {Text: "Revoke all sessions"},
	"settings.security.sessions.revokeAll.confirmTitle": {Text: "Revoke all sessions"},
	"settings.security.sessions.revokeAll.prompt":       {Text: "Type the confirmation phrase shown below to revoke every session, including this one. You will be signed out."},
	"settings.security.sessions.revokeAll.confirmWord":  {Text: "REVOKE ALL"},
	"settings.security.sessions.revokeAll.warning":      {Text: "This ends every session, including this one — you will be signed out."},
	"settings.security.sessions.col.device":             {Text: "Device"},
	"settings.security.sessions.col.ip":                 {Text: "IP address"},
	"settings.security.sessions.col.lastSeen":           {Text: "Last seen"},
	"settings.security.sessions.col.current":            {Text: "Current"},
	"settings.security.sessions.col.actions":            {Text: "Actions"},

	// Publishing section (render_publishing.go).
	"settings.publishing.title":           {Text: "Publishing"},
	"settings.publishing.baseUrl":         {Text: "Base URL"},
	"settings.publishing.baseUrl.invalid": {Text: "Enter a valid URL."},
	"settings.publishing.author":          {Text: "Author"},
	"settings.publishing.contact":         {Text: "Contact"},
	"settings.publishing.copyright":       {Text: "Copyright"},
	"settings.publishing.ttlMinutes":      {Text: "TTL (minutes)"},
	"settings.publishing.cacheControl":    {Text: "Cache-Control header"},
	"settings.publishing.ogImage":         {Text: "Open Graph image URL"},
	"settings.publishing.ogImage.invalid": {Text: "Enter a valid image URL."},
	"settings.publishing.saveError":       {Text: "Couldn't save publishing settings."},
	"settings.publishing.saved":           {Text: "Saved."},
	"settings.publishing.save":            {Text: "Save"},

	// Provider section (render_provider.go).
	// The provider-hydrated model menus (web/pages/settings/modelselect.go).
	"settings.provider.model.choose":            {Text: "Choose a model…"},
	"settings.provider.model.group.recommended": {Text: "For this field"},
	"settings.provider.model.group.other":       {Text: "Other models"},
	// {arg1} is the saved id. It is kept selectable even when the provider
	// does not list it, so opening this page cannot silently re-point a
	// working feed at a different model.
	"settings.provider.model.notListed":   {Text: "{arg1} (not listed by the provider)"},
	"settings.provider.model.unreachable": {Text: "Couldn't reach the server to list models — type the model id instead."},
	// --- Provider: connection ------------------------------------------------
	"settings.provider.connection.title": {Text: "Connection"},
	"settings.provider.connection.help":  {Text: "Any OpenAI-compatible endpoint works — a local model server, a gateway, or a reseller. Keys are read from the server's environment and never stored here."},
	"settings.provider.profile.label":    {Text: "Endpoint"},
	"settings.provider.profile.builtin":  {Text: "OpenAI (built in)"},
	"settings.provider.profile.add":      {Text: "Add endpoint"},
	"settings.provider.profile.name":     {Text: "Name"},
	"settings.provider.profile.baseUrl":  {Text: "Base URL"},
	// "Key variable", never "Key": the field holds the NAME of an
	// environment variable. Calling it a key would invite someone to paste
	// one into a box that gets written to SQLite and into every backup.
	"settings.provider.profile.keyEnv":     {Text: "Key variable"},
	"settings.provider.profile.keySet":     {Text: "Key found"},
	"settings.provider.profile.keyMissing": {Text: "Key variable not set on the server"},
	"settings.provider.profile.noKeyEnv":   {Text: "No key variable named"},
	// {arg1} endpoint name, {arg2} the variable that is missing.
	"settings.provider.profile.noKeyOption": {Text: "{arg1} — {arg2} is not set"},
	"settings.provider.profile.remove":      {Text: "Remove the {arg1} endpoint"},

	// --- Provider: model and effort ---------------------------------------
	"settings.provider.model.title":  {Text: "Model and effort"},
	"settings.provider.effort.label": {Text: "Effort"},
	// Named for what it costs and buys, since that is the whole trade.
	"settings.provider.effort.help":  {Text: "How hard the model works on each run. Higher effort costs more and takes longer."},
	"settings.provider.effort.smart": {Text: "Smart — most thorough"},
	"settings.provider.effort.fast":  {Text: "Fast — balanced"},
	"settings.provider.effort.quick": {Text: "Quick — cheapest"},

	// --- Provider: rates ---------------------------------------------------
	"settings.provider.priceTable.add":  {Text: "Add rate"},
	"settings.provider.priceTable.help": {Text: "Used to estimate run costs. Published prices change, and a stale rate makes every cost number here wrong."},

	// --- Provider: cost history -------------------------------------------
	"settings.provider.cost.title":  {Text: "Spend"},
	"settings.provider.cost.window": {Text: "Time range"},
	// {arg1} = number of days.
	"settings.provider.cost.windowDays":    {Text: "Last {arg1} days"},
	"settings.provider.cost.windowCaption": {Text: "Total over the last {arg1} days"},
	"settings.provider.cost.empty":         {Text: "No runs yet — nothing has been spent."},
	// {arg1} date, {arg2} amount, {arg3} run count. Runs are named because
	// "$0.00 over 3 runs" and "$0.00 over 0 runs" are different situations.
	"settings.provider.cost.bucket": {Text: "{arg1}: {arg2} over {arg3} runs"},
	// {arg1} day count, {arg2} total — the chart's accessible summary.
	"settings.provider.cost.chartLabel": {Text: "Daily spend over {arg1} days, {arg2} in total"},

	"settings.provider.title":                {Text: "Provider"},
	"settings.provider.activeProvider":       {Text: "Active provider"},
	"settings.provider.defaultModel":         {Text: "Default model"},
	"settings.provider.embeddingModel":       {Text: "Embedding model"},
	"settings.provider.apiKeyPresent":        {Text: "API key present: {arg1}"},
	"settings.provider.priceTable.title":     {Text: "Price table"},
	"settings.provider.priceTable.caption":   {Text: "Price table"},
	"settings.provider.priceTable.col.model": {Text: "Model"},
	// PER 1K, not per 1M. The wire field is usd_per_1k_tokens_in/out and its
	// only consumer (web/pages/generate/logic.go) divides by 1000, so a
	// header reading "$/1M" invited an operator to enter a figure a thousand
	// times too large — and providers quote per-1M, so they would have.
	// {arg1} is the 1-based row number.
	"settings.provider.priceTable.err.emptyModel": {Text: "Row {arg1} has no model — a rate with no model never applies to anything."},
	"settings.provider.priceTable.err.duplicate":  {Text: "Row {arg1} repeats a model that already has a rate."},
	"settings.provider.priceTable.err.negative":   {Text: "Row {arg1} has a negative rate."},
	"settings.provider.priceTable.col.in":         {Text: "Input $ per 1K tokens"},
	"settings.provider.priceTable.col.out":        {Text: "Output $ per 1K tokens"},
	"settings.provider.saveError":                 {Text: "Couldn't save provider settings."},
	"settings.provider.saved":                     {Text: "Saved."},
	"settings.provider.save":                      {Text: "Save"},

	// Generation section (render_generation.go).
	"settings.generation.title":                   {Text: "Generation"},
	"settings.generation.killSwitch":              {Text: "Generation enabled"},
	"settings.generation.killSwitch.reason":       {Text: "Generation is disabled. No feed will run until this is turned back on."},
	"settings.generation.group.ceilings":          {Text: "Global ceilings"},
	"settings.generation.group.ceilings.help":     {Text: "The backstop for the whole deployment, across every feed. 0 means no limit."},
	"settings.generation.group.feedDefaults":      {Text: "Per-feed defaults"},
	"settings.generation.group.feedDefaults.help": {Text: "What a newly created feed starts with. Changing these does not alter feeds that already exist."},
	"settings.generation.group.staleness":         {Text: "Staleness"},
	"settings.generation.globalTokenCeiling":      {Text: "Global daily token ceiling (tokens)"},
	"settings.generation.globalSpendCeiling":      {Text: "Global daily spend ceiling (USD)"},
	"settings.generation.defaultTokenBudget":      {Text: "Default per-feed token budget (tokens)"},
	"settings.generation.defaultRunBudget":        {Text: "Default per-feed run budget (runs/day)"},
	"settings.generation.defaultFeedWindow":       {Text: "Default feed window (items)"},
	"settings.generation.stalenessThreshold":      {Text: "Staleness threshold (minutes)"},
	"settings.generation.saveError":               {Text: "Couldn't save generation settings."},
	"settings.generation.saved":                   {Text: "Saved."},
	"settings.generation.save":                    {Text: "Save"},

	// Data section (render_data.go, confirm.go).
	"settings.data.title": {Text: "Data"},
	"settings.data.stats.feedCount": {
		PluralArg: "arg1",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "{arg1} feed",
			gwci18n.PluralOther: "{arg1} feeds",
		},
	},
	"settings.data.stats.itemCount": {
		PluralArg: "arg1",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "{arg1} item",
			gwci18n.PluralOther: "{arg1} items",
		},
	},
	"settings.data.stats.dbSize":  {Text: "Database size: {arg1}"},
	"settings.data.recipe.title":  {Text: "Feed recipe"},
	"settings.data.recipe.feed":   {Text: "Feed"},
	"settings.data.recipe.export": {Text: "Export"},
	// The accessible name for the read-only export output box.
	"settings.data.recipe.exportTitle":         {Text: "Exported recipe"},
	"settings.data.recipe.exportError":         {Text: "Couldn't export the recipe."},
	"settings.data.recipe.importTitle":         {Text: "Import recipe"},
	"settings.data.recipe.import.action":       {Text: "Import"},
	"settings.data.recipe.import.confirmTitle": {Text: "Import recipe"},
	"settings.data.recipe.import.prompt":       {Text: "Type the confirmation phrase shown below to overwrite this feed's recipe with the imported file."},
	"settings.data.recipe.importError":         {Text: "Couldn't import the recipe."},
	"settings.data.recipe.importSuccess":       {Text: "Imported."},
	"settings.data.importToml.confirmWord":     {Text: "IMPORT"},
	"settings.data.backup.title":               {Text: "Backup"},
	"settings.data.backup.generate":            {Text: "Generate backup"},
	"settings.data.backup.error":               {Text: "Couldn't generate a backup."},
	"settings.data.backup.download":            {Text: "Download"},
	"settings.data.vacuum.title":               {Text: "Vacuum"},
	// Orphan, kept: `settings.data.vacuum.unavailable` has no call site.
	// It predates the wired vacuum control below and describes a state the
	// UI no longer has (the action IS reachable here now), so it is a
	// deletion candidate rather than a key to wire up — flagged, not
	// removed, since removing catalogue entries is not this pass's job.
	"settings.data.vacuum.unavailable": {Text: "Vacuum is only available from the server CLI."},

	// Everything below was REFERENCED by web/pages/settings and absent from
	// this catalogue, so each rendered its own raw key as visible interface
	// text (D6-07's documented degrade). Found 2026-08-10 by driving a real
	// browser session to /settings: the sign-out button at the foot of the
	// Security section literally read "settings.settings.security.signOut
	// .action" — doubled prefix, because the missing-key fallback prepends
	// the namespace to a key that already carries it. These are TODOS.md
	// D6-22's outstanding `settings` list.
	//
	// Vacuum blocks the database while it runs, so its copy says so plainly
	// and up front rather than after the fact — D4-10's whole point.
	"settings.data.vacuum.action":      {Text: "Vacuum database"},
	"settings.data.vacuum.description": {Text: "Rebuilds the database file to reclaim space freed by deleted rows."},
	// Three coarse buckets, chosen by reported DB size. Each one names the
	// consequence (the app is unavailable) before the duration, because the
	// duration alone reads as a progress note rather than a warning.
	"settings.data.vacuum.estimate.brief":    {Text: "The app will be unavailable while this runs — expected to be brief at the current size."},
	"settings.data.vacuum.estimate.moderate": {Text: "The app will be unavailable while this runs, likely for a few minutes at the current size."},
	"settings.data.vacuum.estimate.long":     {Text: "The app will be unavailable while this runs, possibly for a long time at the current size. Take a backup first."},
	"settings.data.vacuum.confirmTitle":      {Text: "Vacuum the database?"},
	"settings.data.vacuum.confirmPrompt":     {Text: "{arg1} This cannot be interrupted once it starts."},
	// The typed-confirmation phrase (§12.6: irreversible actions require
	// typed confirmation). A single unambiguous word, not the section title.
	"settings.data.vacuum.confirmWord":  {Text: "VACUUM"},
	"settings.data.vacuum.running":      {Text: "Vacuuming — the app is unavailable until this finishes."},
	"settings.data.vacuum.error":        {Text: "Vacuum failed: {arg1}"},
	"settings.data.vacuum.result.sizes": {Text: "Database went from {arg1} to {arg2}."},
	"settings.data.vacuum.result.duration": {
		PluralArg: "arg1",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "Took {arg1} minute {arg2} seconds.",
			gwci18n.PluralOther: "Took {arg1} minutes {arg2} seconds.",
		},
	},

	// Shown on every settings section while the control plane is down. It
	// states why the controls are inert; the reconnect banner above already
	// states that it is reconnecting, so this does not repeat that.
	"settings.common.disconnectedReason": {Text: "Reconnecting to the server — these controls are unavailable until it comes back."},

	// The three settings.security.signOut.* keys were removed 2026-08-10
	// with the control they labelled: the sign-out button at the foot of
	// Settings is gone, since web/shell/header.go carries one on every
	// authenticated route and two controls performing the same action in one
	// app is one too many. Left as a note rather than silently deleted so
	// the next person to grep for them finds out where sign-out went.

	// About section (render_about.go).
	"settings.about.title":               {Text: "About"},
	"settings.about.version":             {Text: "Version {arg1}"},
	"settings.about.build":               {Text: "Build {arg1}"},
	"settings.about.uptime":              {Text: "Uptime: {arg1}"},
	"settings.about.uptime.parts":        {Text: "{arg1}d {arg2}h {arg3}m"},
	"settings.about.feed.neverBuilt":     {Text: "Never built"},
	"settings.about.feed.lastBuildTitle": {Text: "Last successful build per feed"},
	"settings.about.feed.caption":        {Text: "Last successful build per feed"},
	"settings.about.feed.col.slug":       {Text: "Slug"},
	"settings.about.feed.col.lastBuild":  {Text: "Last build"},
}
