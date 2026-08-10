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
	// Sampler candidate-view labels (types.go).
	"generate.sampler.view.rendered":  {Text: "Rendered"},
	"generate.sampler.view.rawFields": {Text: "Raw fields"},
	"generate.sampler.view.feedXML":   {Text: "Feed XML"},
	"generate.sampler.view.slackCard": {Text: "Slack card"},
	"generate.sampler.view.unknown":   {Text: "Unknown view"},

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
	"generate.editor.sourceKindPlaceholder":     {Text: "Source kind"},
	"generate.editor.removeSource":              {Text: "Remove source"},
	"generate.editor.addSource":                 {Text: "Add source"},
	"generate.editor.conflict.headline":         {Text: "This feed changed elsewhere while you were editing."},
	"generate.editor.conflict.keepMine":         {Text: "Keep my version"},
	"generate.editor.conflict.takeTheirs":       {Text: "Take the other version"},
	"generate.editor.conflict.perFieldHint":     {Text: "Or resolve field by field:"},
	"generate.editor.conflict.mine":             {Text: "Mine"},
	"generate.editor.conflict.theirs":           {Text: "Theirs"},
	"generate.editor.conflict.keepMineField":    {Text: "Keep mine"},
	"generate.editor.conflict.applyPerField":    {Text: "Apply per-field choices"},

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

	// Page-level fallback (render.go, before real data is wired).
	"generate.notWired": {Text: "This page is not yet wired to live data."},
}
