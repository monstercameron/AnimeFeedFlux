package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// esGenerateMessages is the Spanish generate namespace (/generate: the feed
// workbench, the recipe editor, the sampler, the run status strip).
//
// Vocabulary held fixed across this namespace, chosen once so the same
// object is not called three things on one screen:
//
//   - "prompt" stays "prompt". It is the industry term in Spanish technical
//     usage too, and "indicación" would be a word this operator has never
//     seen next to the model picker it sits beside.
//   - "candidate" → "candidato" (a sampled, not-yet-promoted item).
//   - "novelty" → "novedad"; "grounded" → "con fundamento"; "aggregate" →
//     "agregado" — the three feed kinds are domain nouns, so they read as
//     labels rather than descriptions.
//   - "slug", "cron", "feed", "token", "temperature" stay in English:
//     identifiers and operator surface (TODOS.md D6-19). "Temperature" is
//     borderline prose, but it names a provider API parameter the operator
//     will next see spelled that way in the provider's own docs.
//
// generate.common.errorText ("{arg1}") and generate.rail.slugPath ("/{arg1}")
// are pure passthrough templates with no words in them; they are present and
// identical here because omitting them would make the parity test right to
// complain, not because there was anything to translate.
var esGenerateMessages = gwci18n.NamespaceCatalog{
	"generate.common.labelValue": {Text: "{arg1}: {arg2}"},

	"generate.common.errorText": {Text: "{arg1}"},

	"generate.errors.disconnected": {Text: "Estás sin conexión: esto no se ha enviado. No se reintentará automáticamente; vuelve a intentarlo cuando se restablezca la conexión."},
	"generate.errors.unexpected":   {Text: "Error inesperado: {arg1}"},

	"generate.sampler.view.rendered":  {Text: "Renderizado"},
	"generate.sampler.view.rawFields": {Text: "Campos sin formato"},
	"generate.sampler.view.feedXML":   {Text: "XML del feed"},
	"generate.sampler.view.embed":     {Text: "Incrustado"},
	"generate.sampler.view.slackCard": {Text: "Tarjeta de Slack"},
	"generate.sampler.view.unknown":   {Text: "Vista desconocida"},

	"generate.sampler.view.embedFrameTitle": {Text: "Vista previa del incrustado"},

	"generate.sampler.viewTabs.label":      {Text: "Vista del candidato"},
	"generate.sampler.candidateTabs.label": {Text: "Candidatos"},
	"generate.sampler.candidateTab.1":      {Text: "1"},
	"generate.sampler.candidateTab.2":      {Text: "2"},
	"generate.sampler.candidateTab.3":      {Text: "3"},
	"generate.sampler.candidateTab.4":      {Text: "4"},
	"generate.sampler.candidateTab.5":      {Text: "5"},

	"generate.editor.noSelection":                  {Text: "Selecciona un feed en la lista lateral, o crea uno nuevo."},
	"generate.editor.slug":                         {Text: "Slug"},
	"generate.editor.slug.immutableReason":         {Text: "El slug no se puede cambiar una vez creado el feed."},
	"generate.editor.title":                        {Text: "Título"},
	"generate.editor.description":                  {Text: "Descripción"},
	"generate.editor.language":                     {Text: "Idioma"},
	"generate.editor.kind":                         {Text: "Tipo"},
	"generate.editor.kind.generative":              {Text: "Generativo"},
	"generate.editor.kind.grounded":                {Text: "Con fundamento"},
	"generate.editor.kind.aggregate":               {Text: "Agregado"},
	"generate.editor.kind.unspecified":             {Text: "Sin especificar"},
	"generate.editor.schedule":                     {Text: "Programación"},
	"generate.editor.schedule.mode":                {Text: "Se ejecuta"},
	"generate.editor.schedule.mode.scheduled":      {Text: "Según la programación"},
	"generate.editor.schedule.mode.adhoc":          {Text: "Solo cuando yo lo ejecute"},
	"generate.editor.schedule.mode.watch":          {Text: "Según la programación, publicar solo cuando pase algo"},
	"generate.editor.schedule.mode.help.scheduled": {Text: "Cada disparo genera y publica."},
	"generate.editor.schedule.mode.help.adhoc":     {Text: "Nada se dispara automáticamente: «Ejecutar ahora» es el único disparador de este feed, y nunca se marca como desactualizado."},
	"generate.editor.schedule.mode.help.watch":     {Text: "La programación de abajo es una comprobación, no una cuota: en cada disparo el modelo busca algo que valga la pena publicar, y las comprobaciones sin novedades se omiten en silencio. Para comprobar la web en vivo, haz que este feed sea de tipo grounded y añade fuentes: cada comprobación las consulta al momento y solo se publica un elemento cuando alguna trae una novedad genuina. Los periodos tranquilos nunca marcan el feed como desactualizado."},
	"generate.editor.cron":                         {Text: "Expresión cron"},
	"generate.editor.timezone":                     {Text: "Zona horaria"},
	"generate.editor.nextRunsUnavailable":          {Text: "Próximas ejecuciones no disponibles"},
	"generate.editor.cron.readback.raw":            {Text: "{arg1} ({arg2})"},
	"generate.editor.cron.readback.everyMinute":    {Text: "Cada minuto ({arg1})"},
	"generate.editor.cron.readback.daily":          {Text: "A diario a las {arg1} ({arg2})"},
	"generate.editor.cron.readback.weekly":         {Text: "Cada {arg1} a las {arg2} ({arg3})"},
	"generate.editor.cron.readback.monthly":        {Text: "Cada mes el día {arg1} a las {arg2} ({arg3})"},
	"generate.editor.modelParams":                  {Text: "Parámetros del modelo"},
	"generate.editor.model":                        {Text: "Modelo"},
	"generate.editor.temperature":                  {Text: "Temperature"},
	"generate.editor.itemsPerRun":                  {Text: "Elementos por ejecución"},
	"generate.editor.feedWindow":                   {Text: "Ventana del feed"},
	"generate.editor.systemPrompt":                 {Text: "Prompt de sistema"},
	"generate.editor.userPrompt":                   {Text: "Prompt de usuario"},
	"generate.editor.noveltyAndBudgets":            {Text: "Novedad y presupuestos"},
	"generate.editor.noveltyThreshold":             {Text: "Umbral de novedad"},
	"generate.editor.dailyTokenBudget":             {Text: "Presupuesto diario de tokens"},
	"generate.editor.dailyRunBudget":               {Text: "Presupuesto diario de ejecuciones"},
	"generate.editor.validate":                     {Text: "Validar"},
	"generate.editor.save":                         {Text: "Guardar"},
	"generate.editor.sources":                      {Text: "Fuentes"},

	"generate.editor.sourceUrl":              {Text: "URL"},
	"generate.editor.sourceKind":             {Text: "Tipo"},
	"generate.editor.sourceKindPlaceholder":  {Text: "Tipo de fuente"},
	"generate.editor.removeSource":           {Text: "Quitar fuente"},
	"generate.editor.addSource":              {Text: "Añadir fuente"},
	"generate.editor.conflict.headline":      {Text: "Este feed ha cambiado en otro sitio mientras lo editabas."},
	"generate.editor.conflict.keepMine":      {Text: "Conservar mi versión"},
	"generate.editor.conflict.takeTheirs":    {Text: "Tomar la otra versión"},
	"generate.editor.conflict.perFieldHint":  {Text: "O resuélvelo campo por campo:"},
	"generate.editor.conflict.mine":          {Text: "La mía"},
	"generate.editor.conflict.theirs":        {Text: "La otra"},
	"generate.editor.conflict.keepMineField": {Text: "Conservar la mía"},
	"generate.editor.conflict.applyPerField": {Text: "Aplicar las decisiones campo por campo"},

	"generate.rail.killSwitchActive":   {Text: "Generación desactivada por el interruptor general."},
	"generate.rail.disconnected":       {Text: "Sin conexión."},
	"generate.rail.loading":            {Text: "Cargando feeds…"},
	"generate.rail.error":              {Text: "Error"},
	"generate.rail.empty":              {Text: "Todavía no hay feeds."},
	"generate.rail.title":              {Text: "Feeds"},
	"generate.rail.newFeed":            {Text: "Feed nuevo"},
	"generate.rail.neverBuilt":         {Text: "Nunca generado"},
	"generate.rail.stale":              {Text: "Desactualizado"},
	"generate.rail.lastBuild":          {Text: "Última generación"},
	"generate.rail.nextRun":            {Text: "Próxima ejecución"},
	"generate.rail.nextRunUnavailable": {Text: "no disponible"},

	"generate.rail.actionsFor":   {Text: "Acciones para {arg1}"},
	"generate.rail.delete":       {Text: "Eliminar feed"},
	"generate.rail.delete.title": {Text: "¿Eliminar este feed?"},

	"generate.rail.delete.message":  {Text: "Eliminar {arg1} detiene sus ejecuciones programadas y su URL dejará de funcionar. Quienes estén suscritos verán desaparecer el feed. Escribe {arg2} para confirmar."},
	"generate.rail.delete.error":    {Text: "No se pudo eliminar ese feed."},
	"generate.rail.delete.conflict": {Text: "Ese feed ha cambiado mientras esta página estaba abierta. Actualiza e inténtalo de nuevo."},
	"generate.rail.runNow":          {Text: "Ejecutar ahora"},
	"generate.rail.disable":         {Text: "Desactivar"},
	"generate.rail.enable":          {Text: "Activar"},

	"generate.rail.enabledLabel": {Text: "Activado"},

	"generate.rail.slugPath":    {Text: "/{arg1}"},
	"generate.rail.compactMeta": {Text: "/{arg1} · {arg2}"},

	"generate.sampler.selectOrSaveFeed":          {Text: "Elige un feed para la vista previa. Se incluyen los cambios de prompt sin guardar."},
	"generate.sampler.estimateUnavailable":       {Text: "Estimación no disponible"},
	"generate.sampler.estimatedCost":             {Text: "Coste estimado: {arg1}"},
	"generate.sampler.size":                      {Text: "Cantidad"},
	"generate.sampler.temperatureOverride":       {Text: "Temperature personalizada"},
	"generate.sampler.remainingBudget":           {Text: "Presupuesto restante: {arg1}"},
	"generate.sampler.sampleButton":              {Text: "Muestrear ({arg1})"},
	"generate.sampler.cancel":                    {Text: "Cancelar"},
	"generate.sampler.disconnected":              {Text: "Sin conexión: muestreo en pausa."},
	"generate.sampler.streaming":                 {Text: "Recibiendo…"},
	"generate.sampler.empty":                     {Text: "Todavía no hay candidatos: pulsa Vista previa para generar uno."},
	"generate.sampler.groundedSources":           {Text: "Fuentes consultadas"},
	"generate.sampler.failedLinks":               {Text: "Enlaces fallidos"},
	"generate.sampler.candidateCost":             {Text: "Coste del candidato"},
	"generate.sampler.promote":                   {Text: "Promover"},
	"generate.sampler.discard":                   {Text: "Descartar"},
	"generate.sampler.disabled.globalKillSwitch": {Text: "La generación está desactivada por el interruptor general."},
	"generate.sampler.disabled.feedDisabled":     {Text: "Este feed está desactivado."},
	"generate.sampler.disabled.autoDisabled":     {Text: "Desactivado automáticamente tras {arg1} fallos consecutivos."},
	"generate.sampler.novelty.unknown":           {Text: "Novedad desconocida"},
	"generate.sampler.novelty.novel":             {Text: "Novedoso"},
	"generate.sampler.novelty.novelNear":         {Text: "Parecido ({arg1}) a «{arg2}»"},
	"generate.sampler.novelty.rejected":          {Text: "Rechazado: demasiado parecido ({arg1}) a «{arg2}»"},

	"generate.sampler.candidateCostDetail": {Text: "{arg1}: {arg2} (tokens entrada={arg3} salida={arg4})"},

	"generate.workbench.feed":            {Text: "Feed"},
	"generate.workbench.chooseFeed":      {Text: "Elige un feed…"},
	"generate.workbench.newFeedOption":   {Text: "Feed nuevo: sin guardar"},
	"generate.workbench.stakes.disabled": {Text: "Desactivado: las ejecuciones programadas no se lanzarán"},
	"generate.workbench.stakes.schedule": {Text: "{arg1} ({arg2})"},

	"generate.workbench.stakes.budget":    {Text: "{arg1} tokens/día · {arg2} ejecuciones/día"},
	"generate.workbench.retryFeeds":       {Text: "Reintentar"},
	"generate.workbench.feedsUnavailable": {Text: "No se pudieron cargar los feeds"},
	"generate.workbench.saveChanges":      {Text: "Guardar"},
	"generate.workbench.saving":           {Text: "Guardando…"},

	"generate.workbench.saved":        {Text: "Guardado"},
	"generate.workbench.menu.label":   {Text: "Acciones para {arg1}"},
	"generate.workbench.menu.runNow":  {Text: "Ejecutar ahora"},
	"generate.workbench.menu.enable":  {Text: "Activar feed"},
	"generate.workbench.menu.disable": {Text: "Desactivar feed"},
	"generate.workbench.menu.delete":  {Text: "Eliminar feed"},
	"generate.workbench.newFeed":      {Text: "Feed nuevo"},
	"generate.workbench.model":        {Text: "Modelo"},
	"generate.workbench.modelDefault": {Text: "Predeterminado global (de Ajustes)"},

	"generate.workbench.modelUnlisted":   {Text: "{arg1} (no listado)"},
	"generate.workbench.modelGroupChat":  {Text: "Modelos de texto"},
	"generate.workbench.modelGroupOther": {Text: "Otros modelos"},
	"generate.workbench.effort":          {Text: "Esfuerzo"},
	"generate.workbench.effort.smart":    {Text: "Cuidadoso"},
	"generate.workbench.effort.fast":     {Text: "Rápido"},
	"generate.workbench.effort.quick":    {Text: "Muy rápido"},
	"generate.workbench.temp":            {Text: "Temperature personalizada"},
	"generate.workbench.temp.inert":      {Text: "Temperature personalizada: se envía con la muestra, pero el proveedor todavía no la aplica (PLAN §8.1)"},
	"generate.workbench.tempPlaceholder": {Text: "temp"},
	"generate.workbench.size":            {Text: "Candidatos"},
	"generate.workbench.sizeN":           {Text: "{arg1}×"},

	"generate.workbench.preview":    {Text: "Vista previa"},
	"generate.workbench.previewing": {Text: "Generando…"},

	"generate.runStatus.starting": {Text: "Iniciando una ejecución de {arg1}…"},
	"generate.runStatus.running":  {Text: "Ejecutando {arg1}: esto puede tardar un minuto."},

	"generate.runStatus.succeeded": {Text: "Ejecución terminada: {arg1} añadidos, {arg2} rechazados, {arg3} tokens, {arg4}."},

	"generate.runStatus.skipped": {Text: "Ejecución omitida: un tope de presupuesto la detuvo antes de llamar al proveedor."},

	"generate.runStatus.failed": {Text: "La ejecución ha fallado: {arg1}. Abre el historial para ver el registro."},

	"generate.runStatus.refused":             {Text: "No se pudo iniciar la ejecución: {arg1}"},
	"generate.runStatus.viewHistory":         {Text: "Ver las ejecuciones de este feed"},
	"generate.runStatus.dismiss":             {Text: "Descartar el estado de esta ejecución"},
	"generate.runStatus.errorKind.transient": {Text: "un problema temporal del proveedor o de la red"},
	"generate.runStatus.errorKind.invalid":   {Text: "la salida del modelo no pasó la validación"},
	"generate.runStatus.errorKind.fatal":     {Text: "un problema de configuración que no se va a arreglar solo"},
	"generate.runStatus.errorKind.unknown":   {Text: "un motivo no registrado"},
	"generate.workbench.menu.history":        {Text: "Ver las ejecuciones de este feed"},
	"generate.rail.history":                  {Text: "Ver las ejecuciones de este feed"},

	"generate.workbench.feedsSummary":   {Text: "Feeds ({arg1})"},
	"generate.workbench.recipeSettings": {Text: "Ajustes de la receta: slug, presupuestos, ventana, fuentes"},

	"generate.workbench.insertVariable": {Text: "Insertar:"},

	"generate.workbench.insertNamed": {Text: "Insertar {arg1} en el cursor"},
	"generate.workbench.noFeed":      {Text: "Elige un feed arriba, o empieza uno nuevo, para escribir sus prompts."},
	"generate.workbench.systemHint":  {Text: "Instrucciones permanentes. Se envían en cada ejecución."},
	"generate.workbench.userHint":    {Text: "La petición de cada ejecución. Las variables de plantilla se rellenan al generar."},

	"generate.notWired": {Text: "Esta página todavía no está conectada a datos reales."},

	"generate.urls.title": {Text: "URLs de suscripción"},
	"generate.urls.index": {Text: "Todos los feeds"},
	"generate.urls.rss":   {Text: "RSS"},
	"generate.urls.atom":  {Text: "Atom"},
	"generate.urls.json":  {Text: "JSON Feed"},

	"generate.urls.copy":       {Text: "Copiar"},
	"generate.urls.copied":     {Text: "Copiado"},
	"generate.urls.copyFailed": {Text: "No se pudo copiar"},

	"generate.urls.copyNamed": {Text: "Copiar la URL de {arg1}"},

	"generate.urls.baseUnset": {Text: "Define una URL base pública en Ajustes → Publicación para ver las URLs de suscripción."},

	// Constructor de programación (render_schedule.go).
	"generate.editor.schedule.repeats":             {Text: "Se repite"},
	"generate.editor.schedule.every":               {Text: "Cada"},
	"generate.editor.schedule.startingOn":          {Text: "A partir del"},
	"generate.editor.schedule.startingHelp":        {Text: "Desde qué ciclo cuenta el intervalo. Solo importa si repites cada 2.º, 3.º, etc.: es lo que decide qué jueves significa «cada dos jueves»."},
	"generate.editor.schedule.onDays":              {Text: "Estos días"},
	"generate.editor.schedule.onThe":               {Text: "El"},
	"generate.editor.schedule.dayOfMonth":          {Text: "Día del mes"},
	"generate.editor.schedule.monthlyMode":         {Text: "Repetir en"},
	"generate.editor.schedule.monthlyMode.day":     {Text: "un día del mes"},
	"generate.editor.schedule.monthlyMode.weekday": {Text: "un día de la semana"},
	"generate.editor.schedule.timeOfDay":           {Text: "Hora del día"},

	"generate.editor.schedule.unit.day.singular":   {Text: "día"},
	"generate.editor.schedule.unit.day.plural":     {Text: "días"},
	"generate.editor.schedule.unit.week.singular":  {Text: "semana"},
	"generate.editor.schedule.unit.week.plural":    {Text: "semanas"},
	"generate.editor.schedule.unit.month.singular": {Text: "mes"},
	"generate.editor.schedule.unit.month.plural":   {Text: "meses"},
	"generate.editor.schedule.unit.year.singular":  {Text: "año"},
	"generate.editor.schedule.unit.year.plural":    {Text: "años"},

	"generate.editor.schedule.weekday.sunday":          {Text: "domingo"},
	"generate.editor.schedule.weekday.monday":          {Text: "lunes"},
	"generate.editor.schedule.weekday.tuesday":         {Text: "martes"},
	"generate.editor.schedule.weekday.wednesday":       {Text: "miércoles"},
	"generate.editor.schedule.weekday.thursday":        {Text: "jueves"},
	"generate.editor.schedule.weekday.friday":          {Text: "viernes"},
	"generate.editor.schedule.weekday.saturday":        {Text: "sábado"},
	"generate.editor.schedule.weekday.short.sunday":    {Text: "dom"},
	"generate.editor.schedule.weekday.short.monday":    {Text: "lun"},
	"generate.editor.schedule.weekday.short.tuesday":   {Text: "mar"},
	"generate.editor.schedule.weekday.short.wednesday": {Text: "mié"},
	"generate.editor.schedule.weekday.short.thursday":  {Text: "jue"},
	"generate.editor.schedule.weekday.short.friday":    {Text: "vie"},
	"generate.editor.schedule.weekday.short.saturday":  {Text: "sáb"},

	"generate.editor.schedule.ordinal.first":  {Text: "primer"},
	"generate.editor.schedule.ordinal.second": {Text: "segundo"},
	"generate.editor.schedule.ordinal.third":  {Text: "tercer"},
	"generate.editor.schedule.ordinal.fourth": {Text: "cuarto"},
	"generate.editor.schedule.ordinal.last":   {Text: "último"},
	"generate.editor.schedule.lastDayOption":  {Text: "Último día del mes"},

	"generate.editor.schedule.readback.at":            {Text: "{arg1} ({arg2})"},
	"generate.editor.schedule.readback.daily":         {Text: "Todos los días a las {arg1}."},
	"generate.editor.schedule.readback.dailyEvery":    {Text: "Cada {arg1} días a las {arg2}."},
	"generate.editor.schedule.readback.weekly":        {Text: "Todas las semanas los {arg1}, a las {arg2}."},
	"generate.editor.schedule.readback.weeklyEvery":   {Text: "Cada {arg1} semanas los {arg2}, a las {arg3}."},
	"generate.editor.schedule.readback.monthly":       {Text: "Todos los meses el {arg1}, a las {arg2}."},
	"generate.editor.schedule.readback.monthlyEvery":  {Text: "Cada {arg1} {arg2} el {arg3}, a las {arg4}."},
	"generate.editor.schedule.readback.onDay":         {Text: "día {arg1}"},
	"generate.editor.schedule.readback.lastDay":       {Text: "último día"},
	"generate.editor.schedule.readback.nthWeekday":    {Text: "{arg1} {arg2}"},
	"generate.editor.schedule.readback.listSeparator": {Text: ", "},
	"generate.editor.schedule.readback.listAnd":       {Text: "{arg1} y {arg2}"},

	"generate.editor.schedule.preview":      {Text: "Próximas ejecuciones"},
	"generate.editor.schedule.previewHelp":  {Text: "Calculadas aquí a partir de los ajustes de arriba, no de lo que hay guardado. Se muestran sin el pequeño desfase por feed que añade el planificador para repartir la carga."},
	"generate.editor.schedule.previewNone":  {Text: "Esta programación no se ejecuta nunca. Revisa el día y el intervalo."},
	"generate.editor.schedule.previewError": {Text: "No se pueden calcular las próximas ejecuciones: {arg1}"},

	"generate.editor.schedule.advanced":     {Text: "Usar una expresión cron en su lugar"},
	"generate.editor.schedule.advancedHelp": {Text: "Para casos que el constructor no cubre, como cada 15 minutos, o entre semana a las 9 y a las 17. Déjalo desactivado salvo que lo necesites."},
}
