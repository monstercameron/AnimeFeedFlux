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
	"settings.nav.security":     {Text: "Security"},
	"settings.nav.provider":     {Text: "Provider"},
	"settings.nav.generation":   {Text: "Generation"},
	"settings.nav.publishing":   {Text: "Publishing"},
	"settings.nav.data":         {Text: "Data"},
	"settings.nav.about":        {Text: "About"},
	"settings.nav.unknown":      {Text: "Unknown section"},

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
	"settings.security.recoveryCodes.lowNag":                 {Text: "Running low on recovery codes — regenerate a fresh set."},
	"settings.security.recoveryCodes.regenerate.action":      {Text: "Regenerate"},
	"settings.security.recoveryCodes.regenerate.prompt":      {Text: "Type the confirmation phrase shown below to regenerate your recovery codes. Every existing code stops working immediately."},
	"settings.security.recoveryCodes.regenerate.confirmWord": {Text: "REGENERATE"},
	"settings.security.recoveryCodes.error":                  {Text: "Couldn't regenerate recovery codes."},
	"settings.security.recoveryCodes.shownOnce":              {Text: "These codes will not be shown again — save them now."},
	"settings.security.sessions.title":                       {Text: "Active sessions"},
	"settings.security.sessions.revoke":                      {Text: "Revoke"},
	"settings.security.sessions.revokeAll.action":            {Text: "Revoke all sessions"},
	"settings.security.sessions.revokeAll.prompt":            {Text: "Type the confirmation phrase shown below to revoke every session, including this one. You will be signed out."},
	"settings.security.sessions.revokeAll.confirmWord":       {Text: "REVOKE ALL"},
	"settings.security.sessions.revokeAll.warning":           {Text: "This ends every session, including this one — you will be signed out."},
	"settings.security.sessions.col.device":                  {Text: "Device"},
	"settings.security.sessions.col.ip":                      {Text: "IP address"},
	"settings.security.sessions.col.lastSeen":                {Text: "Last seen"},
	"settings.security.sessions.col.current":                 {Text: "Current"},
	"settings.security.sessions.col.actions":                 {Text: "Actions"},

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
	"settings.provider.title":                {Text: "Provider"},
	"settings.provider.activeProvider":       {Text: "Active provider"},
	"settings.provider.defaultModel":         {Text: "Default model"},
	"settings.provider.embeddingModel":       {Text: "Embedding model"},
	"settings.provider.apiKeyPresent":        {Text: "API key present: {arg1}"},
	"settings.provider.priceTable.title":     {Text: "Price table"},
	"settings.provider.priceTable.col.model": {Text: "Model"},
	"settings.provider.priceTable.col.in":    {Text: "Input $/1M tokens"},
	"settings.provider.priceTable.col.out":   {Text: "Output $/1M tokens"},
	"settings.provider.saveError":            {Text: "Couldn't save provider settings."},
	"settings.provider.saved":                {Text: "Saved."},
	"settings.provider.save":                 {Text: "Save"},

	// Generation section (render_generation.go).
	"settings.generation.title":              {Text: "Generation"},
	"settings.generation.killSwitch":         {Text: "Generation enabled"},
	"settings.generation.killSwitch.reason":  {Text: "Generation is disabled. No feed will run until this is turned back on."},
	"settings.generation.globalTokenCeiling": {Text: "Global daily token ceiling"},
	"settings.generation.globalSpendCeiling": {Text: "Global daily spend ceiling"},
	"settings.generation.defaultTokenBudget": {Text: "Default per-feed token budget"},
	"settings.generation.defaultRunBudget":   {Text: "Default per-feed run budget"},
	"settings.generation.defaultFeedWindow":  {Text: "Default feed window"},
	"settings.generation.stalenessThreshold": {Text: "Staleness threshold"},
	"settings.generation.saveError":          {Text: "Couldn't save generation settings."},
	"settings.generation.saved":              {Text: "Saved."},
	"settings.generation.save":               {Text: "Save"},

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
	"settings.data.stats.dbSize":           {Text: "Database size: {arg1}"},
	"settings.data.recipe.title":           {Text: "Feed recipe"},
	"settings.data.recipe.feed":            {Text: "Feed"},
	"settings.data.recipe.export":          {Text: "Export"},
	"settings.data.recipe.exportError":     {Text: "Couldn't export the recipe."},
	"settings.data.recipe.importTitle":     {Text: "Import recipe"},
	"settings.data.recipe.import.action":   {Text: "Import"},
	"settings.data.recipe.import.prompt":   {Text: "Type the confirmation phrase shown below to overwrite this feed's recipe with the imported file."},
	"settings.data.recipe.importError":     {Text: "Couldn't import the recipe."},
	"settings.data.recipe.importSuccess":   {Text: "Imported."},
	"settings.data.importToml.confirmWord": {Text: "IMPORT"},
	"settings.data.backup.title":           {Text: "Backup"},
	"settings.data.backup.generate":        {Text: "Generate backup"},
	"settings.data.backup.error":           {Text: "Couldn't generate a backup."},
	"settings.data.backup.download":        {Text: "Download"},
	"settings.data.vacuum.title":           {Text: "Vacuum"},
	"settings.data.vacuum.unavailable":     {Text: "Vacuum is only available from the server CLI."},

	// About section (render_about.go).
	"settings.about.title":               {Text: "About"},
	"settings.about.version":             {Text: "Version {arg1}"},
	"settings.about.build":               {Text: "Build {arg1}"},
	"settings.about.uptime":              {Text: "Uptime: {arg1}"},
	"settings.about.uptime.parts":        {Text: "{arg1}d {arg2}h {arg3}m"},
	"settings.about.feed.neverBuilt":     {Text: "Never built"},
	"settings.about.feed.lastBuildTitle": {Text: "Last successful build per feed"},
	"settings.about.feed.col.slug":       {Text: "Slug"},
	"settings.about.feed.col.lastBuild":  {Text: "Last build"},
}
