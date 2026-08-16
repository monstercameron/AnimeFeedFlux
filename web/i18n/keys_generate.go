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
	"generate.sampler.view.embed":     {Text: "Embed"},
	"generate.sampler.view.slackCard": {Text: "Slack card"},
	"generate.sampler.view.unknown":   {Text: "Unknown view"},

	// The embed view is the one candidate view rendered as a live iframe
	// rather than as text, so it needs an accessible name of its own — a
	// frame with no title is announced as "frame" and nothing else.
	"generate.sampler.view.embedFrameTitle": {Text: "Embed preview"},

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
	"generate.editor.noSelection":                  {Text: "Select a feed from the rail, or create a new one."},
	"generate.editor.slug":                         {Text: "Slug"},
	"generate.editor.slug.immutableReason":         {Text: "The slug can't be changed after a feed is created."},
	"generate.editor.title":                        {Text: "Title"},
	"generate.editor.description":                  {Text: "Description"},
	"generate.editor.language":                     {Text: "Language"},
	"generate.editor.kind":                         {Text: "Kind"},
	"generate.editor.kind.generative":              {Text: "Generative"},
	"generate.editor.kind.grounded":                {Text: "Grounded"},
	"generate.editor.kind.aggregate":               {Text: "Aggregate"},
	"generate.editor.kind.unspecified":             {Text: "Unspecified"},
	"generate.editor.schedule":                     {Text: "Schedule"},
	"generate.editor.schedule.mode":                {Text: "Runs"},
	"generate.editor.schedule.mode.scheduled":      {Text: "On a schedule"},
	"generate.editor.schedule.mode.adhoc":          {Text: "Only when I run it"},
	"generate.editor.schedule.mode.watch":          {Text: "On a schedule, post only when something happens"},
	"generate.editor.schedule.mode.help.scheduled": {Text: "Every firing generates and publishes."},
	"generate.editor.schedule.mode.help.adhoc":     {Text: "Nothing fires automatically — Run Now is this feed's only trigger, and it is never flagged stale."},
	"generate.editor.schedule.mode.help.watch":     {Text: "The schedule below is a check, not a quota: each firing the model looks for something worth posting, and quiet checks skip silently. To check the live web, make this a grounded feed and add sources — every check fetches them fresh and an item is released only when one carries a genuine, new development. Quiet stretches never flag the feed stale."},
	"generate.editor.cron":                         {Text: "Cron expression"},
	"generate.editor.timezone":                     {Text: "Timezone"},
	"generate.editor.nextRunsUnavailable":          {Text: "Next runs unavailable"},
	"generate.editor.cron.readback.raw":            {Text: "{arg1} ({arg2})"},
	"generate.editor.cron.readback.everyMinute":    {Text: "Every minute ({arg1})"},
	"generate.editor.cron.readback.daily":          {Text: "Daily at {arg1} ({arg2})"},
	"generate.editor.cron.readback.weekly":         {Text: "Every {arg1} at {arg2} ({arg3})"},
	"generate.editor.cron.readback.monthly":        {Text: "Monthly on day {arg1} at {arg2} ({arg3})"},
	"generate.editor.modelParams":                  {Text: "Model parameters"},
	"generate.editor.model":                        {Text: "Model"},
	"generate.editor.temperature":                  {Text: "Temperature"},
	"generate.editor.webSearch":                    {Text: "Web search"},
	"generate.editor.webSearchHint":                {Text: "Lets the model search the live web during generation. Without it the model has no web access at all. The model decides each run whether to search."},
	"generate.editor.itemsPerRun":                  {Text: "Items per run"},
	"generate.editor.feedWindow":                   {Text: "Feed window"},
	"generate.editor.systemPrompt":                 {Text: "System prompt"},
	"generate.editor.userPrompt":                   {Text: "User prompt"},
	"generate.editor.noveltyAndBudgets":            {Text: "Novelty and budgets"},
	"generate.editor.noveltyThreshold":             {Text: "Novelty threshold"},
	"generate.editor.dailyTokenBudget":             {Text: "Daily token budget"},
	"generate.editor.dailyRunBudget":               {Text: "Daily run budget"},
	"generate.editor.validate":                     {Text: "Validate"},
	"generate.editor.save":                         {Text: "Save"},
	"generate.editor.sources":                      {Text: "Sources"},
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
	// {arg1} is the feed title, so the ⋯ trigger has a name that says which
	// row it belongs to rather than twenty-five identical "Actions".
	"generate.rail.actionsFor":   {Text: "Actions for {arg1}"},
	"generate.rail.delete":       {Text: "Delete feed"},
	"generate.rail.delete.title": {Text: "Delete this feed?"},
	// {arg1} the feed title, {arg2} the slug an operator must type.
	"generate.rail.delete.message":  {Text: "Deleting {arg1} stops its scheduled runs and its feed URL will no longer resolve. Existing subscribers will see the feed disappear. Type {arg2} to confirm."},
	"generate.rail.delete.error":    {Text: "Couldn't delete that feed."},
	"generate.rail.delete.conflict": {Text: "That feed changed while this page was open. Refresh and try again."},
	"generate.rail.runNow":          {Text: "Run now"},
	"generate.rail.disable":         {Text: "Disable"},
	"generate.rail.enable":          {Text: "Enable"},
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
	// compactMeta is the sidebar row's one meta line: slug + last build,
	// combined rather than the two separate labelled lines the old
	// full-detail card used ("Last build: 3 days ago" / "Next run:
	// unavailable" / a spend figure) — a compact row has room for one line,
	// not three, and "Next run: unavailable" said exactly that on every
	// row without exception (logic.go's JitteredRuns doc comment: no RPC
	// computes a real jittered next-fire-time), which earns it no space at
	// all, compact or not.
	"generate.rail.compactMeta": {Text: "/{arg1} · {arg2}"},

	// Sampler (render_sampler.go, logic.go).
	// No longer "select or SAVE": SampleService takes a SampleDraft, so a
	// preview runs against whatever is on screen, saved or not. The copy said
	// otherwise for as long as that was true and kept saying it afterwards.
	"generate.sampler.selectOrSaveFeed":          {Text: "Pick a feed to preview. Unsaved prompt edits are included."},
	"generate.sampler.estimateUnavailable":       {Text: "Estimate unavailable"},
	"generate.sampler.estimatedCost":             {Text: "Estimated cost: {arg1}"},
	"generate.sampler.size":                      {Text: "Size"},
	"generate.sampler.temperatureOverride":       {Text: "Temperature override"},
	"generate.sampler.remainingBudget":           {Text: "Remaining budget: {arg1}"},
	"generate.sampler.sampleButton":              {Text: "Sample ({arg1})"},
	"generate.sampler.cancel":                    {Text: "Cancel"},
	"generate.sampler.disconnected":              {Text: "Disconnected — sampling paused."},
	"generate.sampler.streaming":                 {Text: "Streaming…"},
	"generate.sampler.empty":                     {Text: "No candidates yet — press Preview to generate one."},
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
	"generate.workbench.newFeedOption":   {Text: "New feed — not saved yet"},
	"generate.workbench.chooseFeed":      {Text: "Choose a feed…"},
	"generate.workbench.stakes.disabled": {Text: "Disabled — scheduled runs will not fire"},
	// {arg1} the cron expression, {arg2} its timezone.
	"generate.workbench.stakes.schedule": {Text: "{arg1} ({arg2})"},
	// {arg1} daily token budget, {arg2} daily run budget.
	"generate.workbench.stakes.budget":    {Text: "{arg1} tokens/day · {arg2} runs/day"},
	"generate.workbench.retryFeeds":       {Text: "Retry"},
	"generate.workbench.feedsUnavailable": {Text: "Couldn't load feeds"},
	"generate.workbench.saveChanges":      {Text: "Save"},
	"generate.workbench.saving":           {Text: "Saving…"},
	// The rest state: not a call to action, a statement that there is nothing
	// to save. It is the same control, so it says so in the same place.
	"generate.workbench.saved":        {Text: "Saved"},
	"generate.workbench.menu.label":   {Text: "Actions for {arg1}"},
	"generate.workbench.menu.runNow":  {Text: "Run now"},
	"generate.workbench.menu.enable":  {Text: "Enable feed"},
	"generate.workbench.menu.disable": {Text: "Disable feed"},
	"generate.workbench.menu.delete":  {Text: "Delete feed"},
	"generate.workbench.newFeed":      {Text: "New feed"},
	"generate.workbench.model":        {Text: "Model"},
	// The empty model value is a real, meaningful choice since 2026-08-15:
	// the feed runs on whatever /settings/provider's default model is, NOW
	// AND WHENEVER THAT SETTING CHANGES — a per-feed model is an override.
	// The label says so; "Default model" read as a placeholder.
	"generate.workbench.modelDefault": {Text: "Global default (from Settings)"},
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
	"generate.workbench.preview":    {Text: "Preview"},
	"generate.workbench.previewing": {Text: "Generating…"},
	// {arg1} is how many feeds exist, so the collapsed summary still answers
	// "do I have any?" without being opened.
	// The run-status line under the strip (web/pages/generate/render_runstatus.go).
	// {arg1} is the feed's slug.
	"generate.runStatus.starting": {Text: "Starting a run for {arg1}…"},
	"generate.runStatus.running":  {Text: "Running {arg1} — this can take a minute."},
	// {arg1} items added, {arg2} rejected, {arg3} tokens, {arg4} cost.
	"generate.runStatus.succeeded": {Text: "Run finished: {arg1} added, {arg2} rejected, {arg3} tokens, {arg4}."},
	// A budget cap is not a failure, and saying so stops it reading as one.
	"generate.runStatus.skipped": {Text: "Run skipped — a budget cap stopped it before the provider was called."},
	// {arg1} is the error kind (§8's taxonomy), so a provider blip does not
	// read the same as a broken recipe.
	"generate.runStatus.failed": {Text: "Run failed: {arg1}. Open the history for the log."},
	// {arg1} is the refusal's own message.
	"generate.runStatus.refused":             {Text: "Couldn't start the run: {arg1}"},
	"generate.runStatus.viewHistory":         {Text: "See this feed's runs"},
	"generate.runStatus.dismiss":             {Text: "Dismiss this run's status"},
	"generate.runStatus.errorKind.transient": {Text: "a temporary provider or network problem"},
	"generate.runStatus.errorKind.invalid":   {Text: "the model's output did not pass validation"},
	"generate.runStatus.errorKind.fatal":     {Text: "a configuration problem that will not fix itself"},
	"generate.runStatus.errorKind.unknown":   {Text: "an unrecorded reason"},
	"generate.workbench.menu.history":        {Text: "See this feed's runs"},
	"generate.rail.history":                  {Text: "See this feed's runs"},
	"generate.workbench.feedsSummary":        {Text: "Feeds ({arg1})"},
	"generate.workbench.recipeSettings":      {Text: "Recipe settings — slug, budgets, window, sources"},
	"generate.workbench.insertVariable":      {Text: "Insert:"},
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

	// Schedule builder (render_schedule.go / schedulecontrol.go). Replaces the
	// raw cron field: cron cannot express "every other Thursday", "every 3
	// weeks" or "the second Tuesday" at all, so the control had to change
	// shape before the vocabulary could.
	"generate.editor.schedule.repeats":             {Text: "Repeats"},
	"generate.editor.schedule.every":               {Text: "Every"},
	"generate.editor.schedule.startingOn":          {Text: "Starting on"},
	"generate.editor.schedule.startingHelp":        {Text: "Which cycle the interval counts from. It only matters when you repeat every 2nd, 3rd and so on — it is what decides which Thursday “every other Thursday” means."},
	"generate.editor.schedule.onDays":              {Text: "On these days"},
	"generate.editor.schedule.onThe":               {Text: "On the"},
	"generate.editor.schedule.dayOfMonth":          {Text: "Day of the month"},
	"generate.editor.schedule.monthlyMode":         {Text: "Repeat on"},
	"generate.editor.schedule.monthlyMode.day":     {Text: "a day of the month"},
	"generate.editor.schedule.monthlyMode.weekday": {Text: "a weekday"},
	"generate.editor.schedule.timeOfDay":           {Text: "Time of day"},

	"generate.editor.schedule.unit.day.singular":   {Text: "day"},
	"generate.editor.schedule.unit.day.plural":     {Text: "days"},
	"generate.editor.schedule.unit.week.singular":  {Text: "week"},
	"generate.editor.schedule.unit.week.plural":    {Text: "weeks"},
	"generate.editor.schedule.unit.month.singular": {Text: "month"},
	"generate.editor.schedule.unit.month.plural":   {Text: "months"},
	"generate.editor.schedule.unit.year.singular":  {Text: "year"},
	"generate.editor.schedule.unit.year.plural":    {Text: "years"},

	"generate.editor.schedule.weekday.sunday":          {Text: "Sunday"},
	"generate.editor.schedule.weekday.monday":          {Text: "Monday"},
	"generate.editor.schedule.weekday.tuesday":         {Text: "Tuesday"},
	"generate.editor.schedule.weekday.wednesday":       {Text: "Wednesday"},
	"generate.editor.schedule.weekday.thursday":        {Text: "Thursday"},
	"generate.editor.schedule.weekday.friday":          {Text: "Friday"},
	"generate.editor.schedule.weekday.saturday":        {Text: "Saturday"},
	"generate.editor.schedule.weekday.short.sunday":    {Text: "Sun"},
	"generate.editor.schedule.weekday.short.monday":    {Text: "Mon"},
	"generate.editor.schedule.weekday.short.tuesday":   {Text: "Tue"},
	"generate.editor.schedule.weekday.short.wednesday": {Text: "Wed"},
	"generate.editor.schedule.weekday.short.thursday":  {Text: "Thu"},
	"generate.editor.schedule.weekday.short.friday":    {Text: "Fri"},
	"generate.editor.schedule.weekday.short.saturday":  {Text: "Sat"},

	"generate.editor.schedule.ordinal.first":  {Text: "first"},
	"generate.editor.schedule.ordinal.second": {Text: "second"},
	"generate.editor.schedule.ordinal.third":  {Text: "third"},
	"generate.editor.schedule.ordinal.fourth": {Text: "fourth"},
	"generate.editor.schedule.ordinal.last":   {Text: "last"},
	"generate.editor.schedule.lastDayOption":  {Text: "Last day of the month"},

	// Readback. Assembled from keys rather than concatenated fragments — a
	// sentence built by joining words assumes English word order (PLAN.md
	// §12.6).
	"generate.editor.schedule.readback.at":            {Text: "{arg1} ({arg2})"},
	"generate.editor.schedule.readback.daily":         {Text: "Every day at {arg1}."},
	"generate.editor.schedule.readback.dailyEvery":    {Text: "Every {arg1} days at {arg2}."},
	"generate.editor.schedule.readback.weekly":        {Text: "Every week on {arg1}, at {arg2}."},
	"generate.editor.schedule.readback.weeklyEvery":   {Text: "Every {arg1} weeks on {arg2}, at {arg3}."},
	"generate.editor.schedule.readback.monthly":       {Text: "Every month on {arg1}, at {arg2}."},
	"generate.editor.schedule.readback.monthlyEvery":  {Text: "Every {arg1} {arg2} on {arg3}, at {arg4}."},
	"generate.editor.schedule.readback.onDay":         {Text: "day {arg1}"},
	"generate.editor.schedule.readback.lastDay":       {Text: "the last day"},
	"generate.editor.schedule.readback.nthWeekday":    {Text: "the {arg1} {arg2}"},
	"generate.editor.schedule.readback.listSeparator": {Text: ", "},
	"generate.editor.schedule.readback.listAnd":       {Text: "{arg1} and {arg2}"},

	"generate.editor.schedule.preview":      {Text: "Next runs"},
	"generate.editor.schedule.previewHelp":  {Text: "Worked out here from the settings above, not from what is saved. Shown before the small per-feed offset the scheduler adds to spread load."},
	"generate.editor.schedule.previewNone":  {Text: "This schedule never runs. Check the day and the interval."},
	"generate.editor.schedule.previewError": {Text: "Can’t work out the next runs: {arg1}"},

	// The cron escape hatch, kept but demoted.
	"generate.editor.schedule.advanced":     {Text: "Use a cron expression instead"},
	"generate.editor.schedule.advancedHelp": {Text: "For shapes the builder does not cover, such as every 15 minutes, or weekdays at 9 and 17. Leave this off unless you need one."},
}
