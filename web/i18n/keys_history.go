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
	"history.title":          {Text: "History"},
	"history.tabs.runs":      {Text: "Runs"},
	"history.tabs.items":     {Text: "Items"},
	"history.save":           {Text: "Save"},
	"history.cancel":         {Text: "Cancel"},
	"history.kebab":          {Text: "Actions"},
	"history.pager.previous": {Text: "Previous"},
	"history.pager.refresh":  {Text: "Refresh"},

	"history.confirm.confirm":         {Text: "Confirm"},
	"history.confirm.type_to_confirm": {Text: "Type {word} to confirm."},

	// Runs tab.
	"history.runs.filter_feed":          {Text: "Feed"},
	"history.runs.col_status":           {Text: "Status"},
	"history.runs.col_trigger":          {Text: "Trigger"},
	"history.runs.col_duration":         {Text: "Duration"},
	"history.runs.col_added_rejected":   {Text: "Added / rejected"},
	"history.runs.col_tokens":           {Text: "Tokens"},
	"history.runs.col_cost":             {Text: "Cost"},
	"history.runs.col_error":            {Text: "Error"},
	"history.runs.expand":               {Text: "Expand"},
	"history.runs.delete":               {Text: "Delete"},
	"history.runs.delete_confirm_title": {Text: "Delete this run?"},
	"history.runs.reject_reasons":       {Text: "Reject reasons"},
	"history.runs.no_rejects":           {Text: "Nothing was rejected in this run."},
	"history.runs.log":                  {Text: "Log"},
	"history.runs.status.running":       {Text: "Running"},
	"history.runs.status.succeeded":     {Text: "Succeeded"},
	"history.runs.status.failed":        {Text: "Failed"},
	"history.runs.status.skipped":       {Text: "Skipped"},
	"history.runs.status.unspecified":   {Text: "Unspecified"},
	"history.runs.trigger.cron":         {Text: "Scheduled"},
	"history.runs.trigger.manual":       {Text: "Manual"},
	"history.runs.trigger.unspecified":  {Text: "Unspecified"},
	"history.runs.error_kind.transient": {Text: "Transient error"},
	"history.runs.error_kind.invalid":   {Text: "Invalid configuration"},
	"history.runs.error_kind.fatal":     {Text: "Fatal error"},

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
	"history.items.guid_never_changes":        {Text: "The GUID never changes, even across corrections."},
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

	// Revisions panel.
	"history.items.revisions_session_notice": {Text: "Revisions are visible for this session only."},
	"history.items.no_revisions":             {Text: "No revisions yet."},

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
