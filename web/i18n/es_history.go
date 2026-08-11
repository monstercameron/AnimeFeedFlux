package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// esHistoryMessages is the Spanish history namespace (/history's Runs and
// Items tabs).
//
// Keys are restated string literals here rather than constants, because
// keys_history.go itself hardcodes them (see its doc comment: web/pages/
// history passes literals to its own Catalog interface and must not import
// this package). That makes drift possible in a way es_common.go's
// constant-keyed map prevents, which is precisely what
// TestEveryLocaleHasEveryKey exists to catch: add a key to
// historyMessages and forget this file, and that test names the key.
//
// Vocabulary held fixed across this namespace:
//
//   - "run" → "ejecución". Not "carrera", not "pasada".
//   - "item" → "elemento", the standard Spanish UI term for a list entry.
//   - "feed", "GUID", "slug", "Slack", "TOTP", "cron" stay in English —
//     identifiers and operator surface, per TODOS.md D6-19.
//
// One English string is deliberately NOT translated here:
// history.errors.rejected is the bare "{message}" passthrough for a
// server-sent rejection reason. Its Spanish form is identical because there
// is nothing in it to translate — the server's message is whatever the
// server said, in whatever language it said it.
var esHistoryMessages = gwci18n.NamespaceCatalog{
	"history.notWired":       {Text: "Esta página todavía no está conectada a datos reales."},
	"history.title":          {Text: "Historial"},
	"history.tabs.runs":      {Text: "Ejecuciones"},
	"history.tabs.items":     {Text: "Elementos"},
	"history.save":           {Text: "Guardar"},
	"history.cancel":         {Text: "Cancelar"},
	"history.kebab":          {Text: "Acciones"},
	"history.pager.previous": {Text: "Anterior"},

	"history.pager.next":    {Text: "Siguiente"},
	"history.pager.refresh": {Text: "Actualizar"},
	"history.pager.status":  {Text: "Página {page} de {total}"},

	"history.confirm.confirm":         {Text: "Confirmar"},
	"history.confirm.type_to_confirm": {Text: "Escribe {word} para confirmar."},

	"history.errors.disconnected": {Text: "Estás sin conexión: esto no se ha enviado. No se reintentará automáticamente; vuelve a intentarlo cuando se restablezca la conexión."},
	"history.errors.rejected":     {Text: "{message}"},
	"history.errors.unexpected":   {Text: "Error inesperado: {message}"},

	"history.runs.filter_feed":     {Text: "Feed"},
	"history.runs.filter_feed_any": {Text: "Todos los feeds"},
	"history.runs.filter_status":   {Text: "Estado"},

	"history.runs.filter_status_any":    {Text: "Cualquier estado"},
	"history.runs.filter_after":         {Text: "Iniciada después de"},
	"history.runs.filter_before":        {Text: "Iniciada antes de"},
	"history.runs.filter_clear":         {Text: "Borrar filtros"},
	"history.runs.col_status":           {Text: "Estado"},
	"history.runs.col_trigger":          {Text: "Origen"},
	"history.runs.col_duration":         {Text: "Duración"},
	"history.runs.col_added_rejected":   {Text: "Añadidos / rechazados"},
	"history.runs.col_tokens":           {Text: "Tokens"},
	"history.runs.col_cost":             {Text: "Coste"},
	"history.runs.col_error":            {Text: "Error"},
	"history.runs.expand":               {Text: "Desplegar"},
	"history.runs.collapse":             {Text: "Plegar"},
	"history.runs.delete":               {Text: "Eliminar"},
	"history.runs.delete_confirm_title": {Text: "¿Eliminar esta ejecución?"},
	"history.runs.reject_reasons":       {Text: "Motivos de rechazo"},
	"history.runs.no_rejects":           {Text: "En esta ejecución no se rechazó nada."},
	"history.runs.log":                  {Text: "Registro"},
	"history.runs.no_log":               {Text: "Esta ejecución no registró ningún registro."},
	"history.runs.detail.error_kind":    {Text: "Tipo"},
	"history.runs.detail.error_message": {Text: "Mensaje"},
	"history.runs.detail.started":       {Text: "Inicio"},
	"history.runs.detail.finished":      {Text: "Fin"},
	"history.runs.detail.duration":      {Text: "Duración"},

	"history.runs.detail.heartbeat":      {Text: "Última señal de actividad"},
	"history.runs.detail.tokens_in":      {Text: "Tokens de entrada"},
	"history.runs.detail.tokens_out":     {Text: "Tokens de salida"},
	"history.runs.detail.tokens_total":   {Text: "Tokens en total"},
	"history.runs.detail.est_cost":       {Text: "Coste estimado"},
	"history.runs.detail.cost_estimated": {Text: "Estimado: el proveedor no informa de cifras de uso, así que esto se calcula a partir del recuento de tokens en el límite entre prompt y respuesta."},
	"history.runs.log_unavailable":       {Text: "No se pudo cargar el registro de esta ejecución. Actualiza para volver a intentarlo."},
	"history.runs.status.running":        {Text: "En curso"},
	"history.runs.status.succeeded":      {Text: "Correcta"},
	"history.runs.status.failed":         {Text: "Fallida"},
	"history.runs.status.skipped":        {Text: "Omitida"},
	"history.runs.status.unspecified":    {Text: "Sin especificar"},
	"history.runs.trigger.cron":          {Text: "Programada"},
	"history.runs.trigger.manual":        {Text: "Manual"},
	"history.runs.trigger.unspecified":   {Text: "Sin especificar"},
	"history.runs.error_kind.transient":  {Text: "Error transitorio"},
	"history.runs.error_kind.invalid":    {Text: "Configuración no válida"},
	"history.runs.error_kind.fatal":      {Text: "Error irrecuperable"},

	"history.runs.added_rejected_value": {Text: "{added} / {rejected}"},
	"history.runs.tokens_value":         {Text: "{in} / {out}"},

	"history.runs.reject_reason_count": {Text: "{reason}: {count}"},

	"history.items.filter_query":      {Text: "Buscar"},
	"history.items.filter_deleted":    {Text: "Elementos eliminados"},
	"history.items.deleted.exclude":   {Text: "Excluir eliminados"},
	"history.items.deleted.only":      {Text: "Solo eliminados"},
	"history.items.deleted.all":       {Text: "Todos"},
	"history.items.create":            {Text: "Elemento nuevo"},
	"history.items.create_title":      {Text: "Elemento nuevo"},
	"history.items.edit_title":        {Text: "Editar elemento"},
	"history.items.selected_count":    {Text: "%d elemento(s) seleccionado(s)"},
	"history.items.bulk_delete":       {Text: "Eliminar seleccionados"},
	"history.items.bulk_restore":      {Text: "Restaurar seleccionados"},
	"history.items.col_title":         {Text: "Título"},
	"history.items.col_origin":        {Text: "Procedencia"},
	"history.items.col_published":     {Text: "Publicado"},
	"history.items.col_status":        {Text: "Estado"},
	"history.items.status.published":  {Text: "Publicado"},
	"history.items.status.deleted":    {Text: "Eliminado"},
	"history.items.edit":              {Text: "Editar"},
	"history.items.revisions":         {Text: "Revisiones"},
	"history.items.delete":            {Text: "Eliminar"},
	"history.items.restore":           {Text: "Restaurar"},
	"history.items.revert":            {Text: "Revertir"},
	"history.items.origin.generated":  {Text: "Generado"},
	"history.items.origin.sampled":    {Text: "De muestra"},
	"history.items.origin.manual":     {Text: "Manual"},
	"history.items.origin.correction": {Text: "Corrección"},

	"history.items.guid_never_changes":       {Text: "El GUID nunca cambia, ni siquiera con las correcciones."},
	"history.items.filter_query_placeholder": {Text: "Busca en títulos y texto…"},

	"history.kebab.for":                       {Text: "Acciones para {arg1}"},
	"history.items.bulk_actions":              {Text: "los elementos seleccionados"},
	"history.items.select_row":                {Text: "Seleccionar {title}"},
	"history.items.select_all":                {Text: "Seleccionar todos los elementos de esta página"},
	"history.items.title_required":            {Text: "El título es obligatorio."},
	"history.items.field_feed":                {Text: "Feed"},
	"history.items.field_feed_none":           {Text: "No hay ningún feed disponible: crea un feed antes de añadirle un elemento."},
	"history.items.field_title":               {Text: "Título"},
	"history.items.field_summary":             {Text: "Resumen"},
	"history.items.field_body":                {Text: "Cuerpo"},
	"history.items.field_link":                {Text: "Enlace"},
	"history.items.field_tags":                {Text: "Etiquetas"},
	"history.items.field_published_at":        {Text: "Fecha de publicación"},
	"history.items.backdate_blocked":          {Text: "Esto fecharía el elemento antes del último elemento publicado del feed, y Slack nunca lo vería. Bloqueado."},
	"history.items.backdate_override_confirm": {Text: "Entiendo que esto fecha el elemento hacia atrás y que puede que Slack nunca lo vea."},
	"history.items.backdate_override_warning": {Text: "Fechado hacia atrás: por el marcador de Slack, puede que este elemento no llegue nunca."},

	"history.items.publish_correction":         {Text: "Publicar corrección"},
	"history.items.publish_correction_confirm": {Text: "Publicar corrección"},
	"history.items.no_retraction_notice":       {Text: "No hay retractación: el original sigue visible y solo se publica una corrección junto a él."},
	"history.items.correction_title":           {Text: "Título de la corrección"},
	"history.items.correction_summary":         {Text: "Resumen de la corrección"},
	"history.items.correction_body":            {Text: "Cuerpo de la corrección"},

	"history.items.no_revisions": {Text: "Todavía no hay revisiones."},

	"history.items.revert_notice": {Text: "Revertir crea una revisión nueva que registra este cambio: nunca elimina ni reescribe el historial."},

	"history.items.revert_conflict":        {Text: "Este elemento ha cambiado desde que se cargó, así que la reversión no se aplicó."},
	"history.items.revert_conflict_reload": {Text: "Cargar la versión más reciente"},

	"history.state.loading":      {Text: "Cargando…"},
	"history.state.empty":        {Text: "Aquí todavía no hay nada."},
	"history.state.error":        {Text: "Algo ha salido mal."},
	"history.state.disabled":     {Text: "Desactivado"},
	"history.state.disconnected": {Text: "Reconectando…"},
}
