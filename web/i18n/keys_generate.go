package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// Generate-namespace catalogue (TODOS.md D6-11, PLAN.md §12.3). Populated
// from the literal key strings web/pages/generate already passes to its own
// Translator interface (i18n.go: "this package must not import web/i18n" —
// see that file's doc comment) — grepped from render_rail.go,
// render_editor.go, render_sampler.go, logic.go and types.go, since that
// package has no Go key CONSTANTS of its own to read (it is mid-write by a
// concurrent agent; see this package's own catalog.go doc comment on why
// that boundary exists).
//
// Every catalogue key below keeps the "generate." prefix baked into the map
// key itself (e.g. "generate.editor.slug", not "editor.slug"), deliberately
// NOT matching auth's convention of a bare "login.title" inside the "auth"
// namespace. That is not an inconsistency this file introduces by
// accident: web/pages/generate's own call sites already hardcode the full
// "generate.xxx" string as the key argument (it cannot import this package
// to use a NS()-bound short key), so keeping the catalogue key spelled the
// same way is what makes `enCatalog[NSGenerate]["generate.editor.slug"]`
// resolve the moment any future wiring calls
// Bundle.Translate(locale, NSGenerate, "generate.editor.slug", ...) with
// that exact string — the alternative (stripping the prefix here) would
// require renaming call sites in a package this wave does not own.
//
// Positional args use the arg1/arg2/... convention documented in
// keys_common.go's package doc comment, in the ORDER web/pages/generate's
// call sites already pass them (see e.g. logic.go's
// t.T("generate.editor.cron.readback.weekly", name, clockString, timezone)
// → {arg1}={name}, {arg2}=clock, {arg3}=timezone).
var generateMessages = gwci18n.NamespaceCatalog{
	// Shared "label: value" composition, used everywhere this page needs to
	// show a translated label next to an already-formatted value (a slug,
	// an error's text, a relative-time string, a count) rather than
	// building that pairing with fmt/concatenation (PLAN.md §12.6). {arg1}
	// is the label, {arg2} is the value.
	"generate.common.labelValue": {Text: "{arg1}: {arg2}"},
	// generate.common.errorText is a direct passthrough for an error's own
	// message (already resolved to text by errorText(err) — see i18n.go),
	// used where the UI shows the raw error with no label prefix.
	"generate.common.errorText": {Text: "{arg1}"},

	// generate.errors.disconnected/generate.errors.unexpected are D0-10's
	// three-way mutation-error split (errors.go's mutationErrorText): a
	// disconnected refusal never reached the server, so it gets its own
	// reassuring wording rather than a bare error string — the operator's
	// only correct move is to wait for reconnect and, if they want, retry.
	// generate.errors.unexpected is everything that is neither a
	// disconnection nor a server-issued rejection (generate.common.errorText
	// covers that last case), kept visibly distinct so it never reads as
	// "the server said no" when nobody actually said anything.
	"generate.errors.disconnected": {Text: "You're offline — this wasn't sent. It will not be retried automatically; try again once reconnected."},
	"generate.errors.unexpected":   {Text: "Unexpected error: {arg1}"},

	// Sampler candidate-view labels (types.go).
	"generate.sampler.view.rendered":  {Text: "Rendered"},
	"generate.sampler.view.rawFields": {Text: "Raw fields"},
	"generate.sampler.view.feedXML":   {Text: "Feed XML"},
	"generate.sampler.view.slackCard": {Text: "Slack card"},
	"generate.sampler.view.unknown":   {Text: "Unknown view"},

	// generate.sampler.viewTabs.label is the accessible name for the
	// four-candidate-view tablist (web/ui.Tabs, adopted in render_sampler.go
	// for its roving-tabindex arrow-key navigation — a hand-rolled tab strip
	// had none). generate.sampler.candidateTabs.label is the same for the
	// per-candidate tab strip; generate.sampler.candidateTab.1..5 are that
	// strip's own tab labels — a fixed, bounded set of five (not one
	// interpolated key) because web/ui.Tab has no argument slot to
	// interpolate an ordinal into a shared label (see this task's final
	// report), and ValidSampleSize (logic.go) already caps candidates at
	// five, so a sixth position can never occur.
	"generate.sampler.viewTabs.label":      {Text: "Candidate view"},
	"generate.sampler.candidateTabs.label": {Text: "Candidates"},
	"generate.sampler.candidateTab.1":      {Text: "1"},
	"generate.sampler.candidateTab.2":      {Text: "2"},
	"generate.sampler.candidateTab.3":      {Text: "3"},
	"generate.sampler.candidateTab.4":      {Text: "4"},
	"generate.sampler.candidateTab.5":      {Text: "5"},

	// Editor (render_editor.go, logic.go).
	"generate.editor.noSelection":               {Text: "Select a feed from the rail, or create a new one."},
	"generate.editor.slug":                      {Text: "Slug"},
	"generate.editor.slug.immutableReason":      {Text: "The slug can't be changed after a feed is created."},
	"generate.editor.title":                     {Text: "Title"},
	"generate.editor.description":               {Text: "Description"},
	"generate.editor.language":                  {Text: "Language"},
	"generate.editor.kind":                      {Text: "Kind"},
	"generate.editor.kind.generative":           {Text: "Generative"},
	"generate.editor.kind.grounded":             {Text: "Grounded"},
	"generate.editor.kind.aggregate":            {Text: "Aggregate"},
	"generate.editor.kind.unspecified":          {Text: "Unspecified"},
	"generate.editor.schedule":                  {Text: "Schedule"},
	"generate.editor.cron":                      {Text: "Cron expression"},
	"generate.editor.timezone":                  {Text: "Timezone"},
	"generate.editor.nextRunsUnavailable":       {Text: "Next runs unavailable"},
	"generate.editor.cron.readback.raw":         {Text: "{arg1} ({arg2})"},
	"generate.editor.cron.readback.everyMinute": {Text: "Every minute ({arg1})"},
	"generate.editor.cron.readback.daily":       {Text: "Daily at {arg1} ({arg2})"},
	"generate.editor.cron.readback.weekly":      {Text: "Every {arg1} at {arg2} ({arg3})"},
	"generate.editor.cron.readback.monthly":     {Text: "Monthly on day {arg1} at {arg2} ({arg3})"},
	"generate.editor.modelParams":               {Text: "Model parameters"},
	"generate.editor.model":                     {Text: "Model"},
	"generate.editor.temperature":               {Text: "Temperature"},
	"generate.editor.itemsPerRun":               {Text: "Items per run"},
	"generate.editor.feedWindow":                {Text: "Feed window"},
	"generate.editor.prompts":                   {Text: "Prompts"},
	"generate.editor.promptVariablesHint":       {Text: "Variables available in prompts are listed below."},
	"generate.editor.systemPrompt":              {Text: "System prompt"},
	"generate.editor.userPrompt":                {Text: "User prompt"},
	"generate.editor.noveltyAndBudgets":         {Text: "Novelty and budgets"},
	"generate.editor.noveltyThreshold":          {Text: "Novelty threshold"},
	"generate.editor.dailyTokenBudget":          {Text: "Daily token budget"},
	"generate.editor.dailyRunBudget":            {Text: "Daily run budget"},
	"generate.editor.validate":                  {Text: "Validate"},
	"generate.editor.save":                      {Text: "Save"},
	"generate.editor.sources":                   {Text: "Sources"},
	// generate.editor.sourceUrl/sourceKind are the per-source row fields'
	// visible/accessible labels (web/ui.Input, adopted in render_editor.go).
	// The URL field previously had NO label at all (a bare h.Input with no
	// h.Label sibling) — a real a11y gap web/ui.Input's required LabelKey
	// closes; the kind field had only a placeholder, which is a hint, not a
	// label a screen reader announces as the field's name.
	"generate.editor.sourceUrl":              {Text: "URL"},
	"generate.editor.sourceKind":             {Text: "Kind"},
	"generate.editor.sourceKindPlaceholder":  {Text: "Source kind"},
	"generate.editor.removeSource":           {Text: "Remove source"},
	"generate.editor.addSource":              {Text: "Add source"},
	"generate.editor.conflict.headline":      {Text: "This feed changed elsewhere while you were editing."},
	"generate.editor.conflict.keepMine":      {Text: "Keep my version"},
	"generate.editor.conflict.takeTheirs":    {Text: "Take the other version"},
	"generate.editor.conflict.perFieldHint":  {Text: "Or resolve field by field:"},
	"generate.editor.conflict.mine":          {Text: "Mine"},
	"generate.editor.conflict.theirs":        {Text: "Theirs"},
	"generate.editor.conflict.keepMineField": {Text: "Keep mine"},
	"generate.editor.conflict.applyPerField": {Text: "Apply per-field choices"},

	// Rail (render_rail.go).
	"generate.rail.killSwitchActive":   {Text: "Generation disabled by the global kill switch."},
	"generate.rail.disconnected":       {Text: "Disconnected."},
	"generate.rail.loading":            {Text: "Loading feeds…"},
	"generate.rail.error":              {Text: "Error"},
	"generate.rail.empty":              {Text: "No feeds yet."},
	"generate.rail.title":              {Text: "Feeds"},
	"generate.rail.newFeed":            {Text: "New feed"},
	"generate.rail.neverBuilt":         {Text: "Never built"},
	"generate.rail.stale":              {Text: "Stale"},
	"generate.rail.lastBuild":          {Text: "Last build"},
	"generate.rail.nextRun":            {Text: "Next run"},
	"generate.rail.nextRunUnavailable": {Text: "unavailable"},
	"generate.rail.runNow":             {Text: "Run now"},
	"generate.rail.disable":            {Text: "Disable"},
	"generate.rail.enable":             {Text: "Enable"},
	// generate.rail.enabledLabel is web/ui.Toggle's visible label (adopted
	// in render_rail.go in place of the old two-state "Enable"/"Disable"
	// verb button) — a real switch names the STATE it shows ("Enabled",
	// with the switch position saying on/off), not the action a click would
	// take, unlike generate.rail.enable/disable above, which are kept for
	// reference but no longer called.
	"generate.rail.enabledLabel": {Text: "Enabled"},
	// generate.rail.slugPath renders a feed's slug as its own row-relative
	// path. The slug itself ({arg1}) is an identifier and stays
	// untranslated content (TODOS.md D6-19); only the "/" framing is
	// interface formatting, which is why this still goes through the
	// catalogue instead of an "/%s" Textf.
	"generate.rail.slugPath": {Text: "/{arg1}"},

	// Sampler (render_sampler.go, logic.go).
	"generate.sampler.selectOrSaveFeed":          {Text: "Select or save a feed to sample it."},
	"generate.sampler.estimateUnavailable":       {Text: "Estimate unavailable"},
	"generate.sampler.estimatedCost":             {Text: "Estimated cost: {arg1}"},
	"generate.sampler.size":                      {Text: "Size"},
	"generate.sampler.temperatureOverride":       {Text: "Temperature override"},
	"generate.sampler.remainingBudget":           {Text: "Remaining budget: {arg1}"},
	"generate.sampler.sampleButton":              {Text: "Sample ({arg1})"},
	"generate.sampler.cancel":                    {Text: "Cancel"},
	"generate.sampler.disconnected":              {Text: "Disconnected — sampling paused."},
	"generate.sampler.streaming":                 {Text: "Streaming…"},
	"generate.sampler.empty":                     {Text: "No candidates yet."},
	"generate.sampler.groundedSources":           {Text: "Grounded sources"},
	"generate.sampler.failedLinks":               {Text: "Failed links"},
	"generate.sampler.candidateCost":             {Text: "Candidate cost"},
	"generate.sampler.promote":                   {Text: "Promote"},
	"generate.sampler.discard":                   {Text: "Discard"},
	"generate.sampler.disabled.globalKillSwitch": {Text: "Generation is disabled by the global kill switch."},
	"generate.sampler.disabled.feedDisabled":     {Text: "This feed is disabled."},
	"generate.sampler.disabled.autoDisabled":     {Text: "Auto-disabled after {arg1} consecutive failures."},
	"generate.sampler.novelty.unknown":           {Text: "Novelty unknown"},
	"generate.sampler.novelty.novel":             {Text: "Novel"},
	"generate.sampler.novelty.novelNear":         {Text: "Similar ({arg1}) to \"{arg2}\""},
	"generate.sampler.novelty.rejected":          {Text: "Rejected — too similar ({arg1}) to \"{arg2}\""},

	// generate.sampler.candidateCostDetail is the candidate cost line,
	// including its token counts — {arg1} the translated "Candidate cost"
	// label, {arg2} the already-formatted currency amount, {arg3}/{arg4}
	// the already locale-formatted in/out token counts.
	"generate.sampler.candidateCostDetail": {Text: "{arg1}: {arg2} (tokens in={arg3} out={arg4})"},

	// Page-level fallback (render.go, before real data is wired).
	// --- The workbench layout (web/pages/generate/render_workbench.go) ------
	"generate.workbench.feed":            {Text: "Feed"},
	"generate.workbench.chooseFeed":      {Text: "Choose a feed…"},
	"generate.workbench.stakes.disabled": {Text: "Disabled — scheduled runs will not fire"},
	// {arg1} the cron expression, {arg2} its timezone.
	"generate.workbench.stakes.schedule": {Text: "{arg1} ({arg2})"},
	// {arg1} daily token budget, {arg2} daily run budget.
	"generate.workbench.stakes.budget":    {Text: "{arg1} tokens/day · {arg2} runs/day"},
	"generate.workbench.retryFeeds":       {Text: "Retry"},
	"generate.workbench.feedsUnavailable": {Text: "Couldn't load feeds"},
	"generate.workbench.newFeed":          {Text: "+ New"},
	"generate.workbench.model":            {Text: "Model"},
	"generate.workbench.modelDefault":     {Text: "Default model"},
	// {arg1} is a model id the recipe names but the provider's list does not
	// include — a deprecated id, or one served by a custom endpoint.
	"generate.workbench.modelUnlisted":   {Text: "{arg1} (not listed)"},
	"generate.workbench.modelGroupChat":  {Text: "Text models"},
	"generate.workbench.modelGroupOther": {Text: "Other models"},
	"generate.workbench.effort":          {Text: "Effort"},
	"generate.workbench.effort.smart":    {Text: "Smart"},
	"generate.workbench.effort.fast":     {Text: "Fast"},
	"generate.workbench.effort.quick":    {Text: "Quick"},
	"generate.workbench.temp":            {Text: "Temperature override"},
	// The strip is too narrow for a visible label on this one field, so the
	// placeholder carries the name; the accessible name is on aria-label.
	"generate.workbench.temp.inert":      {Text: "Temperature override — carried with the sample but not yet applied by the provider (PLAN §8.1)"},
	"generate.workbench.tempPlaceholder": {Text: "temp"},
	"generate.workbench.size":            {Text: "Candidates"},
	"generate.workbench.sizeN":           {Text: "{arg1}×"},
	// The verb of the page. "Preview", not "Sample": it names what you get
	// back, and it is the same word the pane it fills is called.
	"generate.workbench.preview":        {Text: "Preview"},
	"generate.workbench.previewing":     {Text: "Generating…"},
	"generate.workbench.recipeSettings": {Text: "Recipe settings — slug, schedule, budgets, window, sources"},
	"generate.workbench.insertVariable": {Text: "Insert:"},
	// {arg1} = the template identifier, e.g. {{.Today}}.
	"generate.workbench.insertNamed": {Text: "Insert {arg1} at the cursor"},
	"generate.workbench.noFeed":      {Text: "Pick a feed above, or start a new one, to write its prompts."},
	"generate.workbench.systemHint":  {Text: "Standing instructions. Sent with every run."},
	"generate.workbench.userHint":    {Text: "The per-run request. Template variables are filled in at generation time."},

	"generate.notWired": {Text: "This page is not yet wired to live data."},

	// The subscribe-URL panel (web/pages/generate/render_urls.go). The URL
	// is this product's entire deliverable, and until this panel existed
	// nothing on the authoring page told an operator where the finished feed
	// lives — they had to join a base URL from Settings to a slug from the
	// editor and remember which extension each format uses.
	"generate.urls.title": {Text: "Subscribe URLs"},
	"generate.urls.index": {Text: "All feeds"},
	"generate.urls.rss":   {Text: "RSS"},
	"generate.urls.atom":  {Text: "Atom"},
	"generate.urls.json":  {Text: "JSON Feed"},
	// The button names the action; the two result states name what happened.
	// "Copy" -> "Copied" keeps the same verb, so the control reads as one
	// thing in two states rather than as two controls.
	"generate.urls.copy":       {Text: "Copy"},
	"generate.urls.copied":     {Text: "Copied"},
	"generate.urls.copyFailed": {Text: "Couldn't copy"},
	// The accessible name, so four adjacent "Copy" buttons are told apart by
	// a screen reader. {arg1} is the row's own label.
	"generate.urls.copyNamed": {Text: "Copy the {arg1} URL"},
	"generate.urls.baseUnset": {Text: "Set a public base URL in Settings → Publishing to see subscribe URLs."},
}
