package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// History-namespace catalogue (TODOS.md D6-12, PLAN.md §12.4). Populated
// from the literal key strings web/pages/history already passes to its own
// Catalog interface (catalog.go: "web/i18n is owned by another agent and
// this package deliberately does not import it") — grepped from root.go,
// runs_ui.go, items_ui.go, forms_ui.go, confirm_ui.go and backdating.go.
//
// Unlike generate/settings (whose Translator is `T(key string, args
// ...any) string`, positional), history's Catalog is
// `T(key string, args map[string]any) string` — NAMED args, already. Every
// non-nil call site in that package supplies its own argument name (e.g.
// confirm_ui.go's `map[string]any{"word": props.MatchWord}`), so this
// catalogue's interpolated keys use those exact names rather than this
// package's arg1/arg2 convention (keys_common.go/keys_generate.go), which
// exists only to work around a positional-only signature history does not
// have.
//
// history.items.selected_count is a documented exception: its call site
// (items_ui.go) does `h.Textf(props.T.T("history.items.selected_count",
// nil), selection.Get().Count())` — the TRANSLATED STRING is itself used
// as a Printf template applied to the count afterward, not interpolated by
// this catalogue's {name} mechanism. Its Text is therefore a "%d ..." Go
// format verb, on purpose, not a bug.
//
// Two keys keep their call sites' snake_case/underscore spelling verbatim
// (history.runs.delete_confirm_title, history.confirm.type_to_confirm) —
// inconsistent with the rest of this namespace's dot-camelCase, but this
// package does not own web/pages/history and cannot rename its call sites;
// see this wave's report for the list of things flagged rather than fixed.
var historyMessages = gwci18n.NamespaceCatalog{
	// Added 2026-08-10. wiring.go's renderNotWired had a doc comment
	// arguing that, since this key was absent, D6-07's "a missing key
	// renders the key itself" made rendering "history.notWired" the correct
	// behaviour rather than "inventing wording here". D6-07 describes a
	// degrade, not a design: `generate.notWired` and
	// `settings.notWired.message` both carry real copy for the identical
	// state, so this was the one surface of three that showed an operator a
	// key. Same wording as its two neighbours, deliberately.
	"history.notWired":       {Text: "This page is not yet wired to live data."},
	"history.title":          {Text: "History"},
	"history.tabs.runs":      {Text: "Runs"},
	"history.tabs.items":     {Text: "Items"},
	"history.save":           {Text: "Save"},
	"history.cancel":         {Text: "Cancel"},
	"history.kebab":          {Text: "Actions"},
	"history.pager.previous": {Text: "Previous"},
	// Added 2026-08-10 with the control it labels: both tables rendered
	// Previous and Refresh and no forward control at all, so every page past
	// the first was unreachable. See runs_ui.go's "load-ok" reducer case.
	"history.pager.next":    {Text: "Next"},
	"history.pager.refresh": {Text: "Refresh"},
	// Added with the numbered pager. Previous/Next alone never said where you
	// were or how much was left, and there was no way to reach page 9 except
	// by pressing Next eight times.
	"history.pager.status": {Text: "Page {page} of {total}"},

	"history.confirm.confirm":         {Text: "Confirm"},
	"history.confirm.type_to_confirm": {Text: "Type {word} to confirm."},

	// D0-10's three-way mutation-error split (mutationerror_js.go's
	// mutationErrorText) — disconnected/rejected/unexpected, so a refusal
	// that never reached the server, a rejection the server actually
	// issued, and anything else never render the same undifferentiated
	// text. Mirrors generate.errors.* (keys_generate.go) in shape, adapted
	// to this package's named-args Catalog.
	"history.errors.disconnected": {Text: "You're offline — this wasn't sent. It will not be retried automatically; try again once reconnected."},
	"history.errors.rejected":     {Text: "{message}"},
	"history.errors.unexpected":   {Text: "Unexpected error: {message}"},

	// Runs tab.
	"history.runs.filter_feed":     {Text: "Feed"},
	"history.runs.filter_feed_any": {Text: "All feeds"},
	"history.runs.filter_status":   {Text: "Status"},
	// "Any status" rather than "All statuses": the control filters TO a
	// status, so its unset value is the absence of that filter, not a
	// selection of every status at once.
	"history.runs.filter_status_any":    {Text: "Any status"},
	"history.runs.filter_after":         {Text: "Started after"},
	"history.runs.filter_before":        {Text: "Started before"},
	"history.runs.filter_clear":         {Text: "Clear filters"},
	"history.runs.col_started":          {Text: "Started"},
	"history.runs.col_status":           {Text: "Status"},
	"history.runs.col_trigger":          {Text: "Trigger"},
	"history.runs.col_duration":         {Text: "Duration"},
	"history.runs.col_added_rejected":   {Text: "Added / rejected"},
	"history.runs.col_tokens":           {Text: "Tokens"},
	"history.runs.col_cost":             {Text: "Cost"},
	"history.runs.col_error":            {Text: "Error"},
	"history.runs.expand":               {Text: "Expand"},
	"history.runs.collapse":             {Text: "Collapse"},
	"history.runs.delete":               {Text: "Delete"},
	"history.runs.delete_confirm_title": {Text: "Delete this run?"},
	"history.runs.reject_reasons":       {Text: "Reject reasons"},
	"history.runs.no_rejects":           {Text: "Nothing was rejected in this run."},
	"history.runs.log":                  {Text: "Log"},

	// The expanded run panel. It showed reject reasons and a log and nothing
	// else, so a failed run's expansion said "Nothing was rejected" and
	// stopped — while Run.error, the actual provider message, was carried by
	// the RPC and rendered nowhere in the app. The table's Error column shows
	// only error_kind, a coarse token ("transient", "fatal"), which says what
	// class of thing went wrong and never what did.
	"history.runs.detail.error_kind":    {Text: "Kind"},
	"history.runs.detail.error_message": {Text: "Message"},
	"history.runs.detail.started":       {Text: "Started"},
	"history.runs.detail.finished":      {Text: "Finished"},
	"history.runs.detail.duration":      {Text: "Duration"},
	// Shown only while a run has not finished: heartbeat_at is what separates
	// "still working" from "the process died and left the row open" (§10/§15),
	// and it was surfaced on the wire for exactly that and never displayed.
	"history.runs.detail.heartbeat":      {Text: "Last heartbeat"},
	"history.runs.detail.tokens_in":      {Text: "Tokens in"},
	"history.runs.detail.tokens_out":     {Text: "Tokens out"},
	"history.runs.detail.tokens_total":   {Text: "Tokens total"},
	"history.runs.detail.est_cost":       {Text: "Estimated cost"},
	"history.runs.detail.cost_estimated": {Text: "Estimated: the provider reports no usage figures, so this is computed from token counts at the prompt/response boundary."},
	"history.runs.no_log":                {Text: "This run recorded no log."},
	"history.runs.log_unavailable":       {Text: "Couldn't load this run's log. Refresh to try again."},
	"history.runs.status.running":        {Text: "Running"},
	"history.runs.status.succeeded":      {Text: "Succeeded"},
	"history.runs.status.failed":         {Text: "Failed"},
	"history.runs.status.skipped":        {Text: "Skipped"},
	"history.runs.status.unspecified":    {Text: "Unspecified"},
	"history.runs.trigger.cron":          {Text: "Scheduled"},
	"history.runs.trigger.manual":        {Text: "Manual"},
	"history.runs.trigger.unspecified":   {Text: "Unspecified"},
	"history.runs.error_kind.transient":  {Text: "Transient error"},
	"history.runs.error_kind.invalid":    {Text: "Invalid configuration"},
	"history.runs.error_kind.fatal":      {Text: "Fatal error"},

	// history.runs.added_rejected_value/tokens_value replace two "%d / %d"
	// Textf calls in runs_ui.go's added/rejected and tokens-in/out table
	// cells. Values are already locale-formatted (gwci18n.FormatNumber)
	// before interpolation — this key only owns the "X / Y" shape, not the
	// digit grouping.
	"history.runs.added_rejected_value": {Text: "{added} / {rejected}"},
	"history.runs.tokens_value":         {Text: "{in} / {out}"},
	// history.runs.reject_reason_count pairs a reject reason (an
	// identifier/code — duplicate, novelty-gate, byte-equality-link-
	// mismatch, per common.pb.go's RejectReason doc comment — not prose,
	// TODOS.md D6-19) with its already locale-formatted count.
	"history.runs.reject_reason_count": {Text: "{reason}: {count}"},

	// history.runs.reject.* turn the server's wire tokens into sentences.
	// The tokens themselves stay on screen next to them: the sentence is for
	// deciding what to do, the token is for grepping the logs (A8-30).
	// Kept in step with internal/generate by web/pages/history's
	// TestEveryGenerateReasonHasALabel.
	"history.runs.reject.invalid_utf8":               {Text: "The model returned bytes that are not valid text."},
	"history.runs.reject.control_chars":              {Text: "The text contained control characters, which would not render."},
	"history.runs.reject.title_required":             {Text: "No title."},
	"history.runs.reject.title_too_short":            {Text: "The title was too short to say anything."},
	"history.runs.reject.title_too_long":             {Text: "The title was longer than the limit."},
	"history.runs.reject.title_trailing_punctuation": {Text: "The title ended in punctuation, which reads as truncated in a feed reader."},
	"history.runs.reject.summary_required":           {Text: "No summary."},
	"history.runs.reject.summary_exceeds_hard_cap":   {Text: "The summary was longer than the hard cap."},
	"history.runs.reject.summary_contains_html":      {Text: "The summary contained HTML, which is published as plain text."},
	"history.runs.reject.body_html_required":         {Text: "No body."},
	"history.runs.reject.body_html_relative_link":    {Text: "The body linked somewhere relative, which resolves to nothing outside this site."},
	"history.runs.reject.answer_leaked_into_summary": {Text: "The summary gave away the answer the body was supposed to reveal."},
	"history.runs.reject.tags_too_many":              {Text: "More tags than the limit allows."},
	"history.runs.reject.tags_not_lowercase":         {Text: "A tag was not lowercase, so it would split into a duplicate of an existing tag."},
	"history.runs.reject.link_required_grounded":     {Text: "No source link, and this feed requires every item to be grounded in one."},
	"history.runs.reject.link_invalid":               {Text: "The source link was not a usable URL."},
	"history.runs.reject.link_not_in_candidate_set":  {Text: "The source link was not one of the sources given to the model — it was invented."},
	"history.runs.reject.novelty_duplicate":          {Text: "This repeats an item the feed has already published."},
	"history.runs.reject.novelty_check_failed":       {Text: "The duplicate check could not run, so the item was refused rather than risked."},

	// Items tab.
	"history.items.filter_query":      {Text: "Search"},
	"history.items.filter_deleted":    {Text: "Deleted items"},
	"history.items.deleted.exclude":   {Text: "Exclude deleted"},
	"history.items.deleted.only":      {Text: "Deleted only"},
	"history.items.deleted.all":       {Text: "All"},
	"history.items.create":            {Text: "New item"},
	"history.items.create_title":      {Text: "New item"},
	"history.items.edit_title":        {Text: "Edit item"},
	"history.items.selected_count":    {Text: "%d item(s) selected"},
	"history.items.bulk_delete":       {Text: "Delete selected"},
	"history.items.bulk_restore":      {Text: "Restore selected"},
	"history.items.col_title":         {Text: "Title"},
	"history.items.col_origin":        {Text: "Origin"},
	"history.items.col_published":     {Text: "Published"},
	"history.items.col_status":        {Text: "Status"},
	"history.items.status.published":  {Text: "Published"},
	"history.items.status.deleted":    {Text: "Deleted"},
	"history.items.edit":              {Text: "Edit"},
	"history.items.revisions":         {Text: "Revisions"},
	"history.items.delete":            {Text: "Delete"},
	"history.items.restore":           {Text: "Restore"},
	"history.items.revert":            {Text: "Revert"},
	"history.items.origin.generated":  {Text: "Generated"},
	"history.items.origin.sampled":    {Text: "Sampled"},
	"history.items.origin.manual":     {Text: "Manual"},
	"history.items.origin.correction": {Text: "Correction"},

	// Item edit form (forms_ui.go).
	"history.items.guid_never_changes":       {Text: "The GUID never changes, even across corrections."},
	"history.items.filter_query_placeholder": {Text: "Search titles and text…"},
	// {arg1} names what the ⋯ acts on, so twenty-five triggers do not all
	// read "Actions" to a screen reader.
	"history.kebab.for":          {Text: "Actions for {arg1}"},
	"history.items.bulk_actions": {Text: "the selected items"},
	// {title} is the item's own title, so a screen reader hears which row a
	// checkbox belongs to rather than twenty-five identical "checkbox".
	"history.items.select_row":                {Text: "Select {title}"},
	"history.items.select_all":                {Text: "Select every item on this page"},
	"history.items.title_required":            {Text: "A title is required."},
	"history.items.field_feed":                {Text: "Feed"},
	"history.items.field_feed_none":           {Text: "No feeds available — create a feed before adding an item to it."},
	"history.items.field_title":               {Text: "Title"},
	"history.items.field_summary":             {Text: "Summary"},
	"history.items.field_body":                {Text: "Body"},
	"history.items.field_link":                {Text: "Link"},
	"history.items.field_tags":                {Text: "Tags"},
	"history.items.field_published_at":        {Text: "Published at"},
	"history.items.backdate_blocked":          {Text: "This would backdate the item before the feed's newest published item — Slack would never see it. Blocked."},
	"history.items.backdate_override_confirm": {Text: "I understand this backdates the item and Slack may never see it."},
	"history.items.backdate_override_warning": {Text: "Backdated — Slack's bookmark means this item may never be delivered."},

	// Correction workflow (D6-12: "the correction-vs-edit explanation").
	"history.items.publish_correction":         {Text: "Publish correction"},
	"history.items.publish_correction_confirm": {Text: "Publish correction"},
	"history.items.no_retraction_notice":       {Text: "There is no retraction — the original stays visible; only a correction is published alongside it."},
	"history.items.correction_title":           {Text: "Correction title"},
	"history.items.correction_summary":         {Text: "Correction summary"},
	"history.items.correction_body":            {Text: "Correction body"},

	// Revisions panel (TODOS.md D3-15: real ItemService.ListRevisions/
	// RevertRevision, not the earlier session-local snapshot stopgap).
	"history.items.no_revisions": {Text: "No revisions yet."},
	// history.items.revert_notice sits next to the Revert control (PLAN.md
	// §12.4: revert "is an ordinary edit, not a rewind") — "revert" reads
	// like undo, so this states plainly that it creates a new revision and
	// never deletes or rewrites the ones in between.
	"history.items.revert_notice": {Text: "Reverting creates a new revision recording this change — it never deletes or rewrites history."},
	// history.items.revert_conflict/revert_conflict_reload are
	// IsVersionConflict's (mutationerror.go) dedicated wording: the item
	// changed since this page last loaded it, so the operator gets a real
	// choice (reload, then decide) instead of a silent clobber or an
	// undifferentiated "rejected" message.
	"history.items.revert_conflict":        {Text: "This item changed since it was loaded, so the revert was not applied."},
	"history.items.revert_conflict_reload": {Text: "Reload the latest version"},

	// Shared six-state matrix, scoped to this page (see keys_common.go's
	// state.* for the shared web/ui version — this page's own Catalog
	// interface does not go through web/ui/state.go, so it keeps its own
	// copy under this namespace rather than reaching across packages).
	"history.state.loading":      {Text: "Loading…"},
	"history.state.empty":        {Text: "Nothing here yet."},
	"history.state.error":        {Text: "Something went wrong."},
	"history.state.disabled":     {Text: "Disabled"},
	"history.state.disconnected": {Text: "Reconnecting…"},
}
