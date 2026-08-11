package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// esSettingsMessages is the Spanish settings namespace — the largest of the
// six, covering all seven sections of /settings.
//
// # The confirmation words ARE translated, deliberately
//
// settings.security.recoveryCodes.regenerate.confirmWord and its three
// siblings (revokeAll, importToml, vacuum) render as Spanish words here:
// REGENERAR, REVOCAR TODO, IMPORTAR, COMPACTAR. That is safe and it is
// required. Safe, because the typed-confirmation gate compares the operator's
// input against the SAME catalogue lookup it displays — render_security.go
// passes RequiredPhrase: t(ConfirmationWordKey(...)) — so both sides move
// together by construction. Required, because the whole mechanism is a
// deliberate speed bump: a Spanish-speaking operator asked to type
// "REGENERATE" is being asked to copy a shape rather than to read a word,
// and a gate you pass without reading is not a gate.
//
// # Vocabulary held fixed
//
//   - "recovery code" → "código de recuperación"; "session" → "sesión";
//     "revoke" → "revocar"; "backup" → "copia de seguridad".
//   - "vacuum" → "compactar". The SQLite command is called VACUUM, but this
//     control's audience is the operator deciding whether to accept downtime,
//     not the DBA typing SQL, and "compactar" says what it does.
//   - "provider" → "proveedor", "endpoint" stays "endpoint", "feed" stays
//     "feed" — identifiers and operator surface (TODOS.md D6-19).
//   - "Open Graph", "Cache-Control", "TTL", "TOML", "USD", "RSS" are names
//     of formats and headers and never translate.
var esSettingsMessages = gwci18n.NamespaceCatalog{
	"settings.title":            {Text: "Ajustes"},
	"settings.notWired.message": {Text: "Esta página todavía no está conectada a datos reales."},
	"settings.nav.label":        {Text: "Secciones de ajustes"},
	"settings.nav.security":     {Text: "Seguridad"},
	"settings.nav.provider":     {Text: "Proveedor"},
	"settings.nav.generation":   {Text: "Generación"},
	"settings.nav.publishing":   {Text: "Publicación"},
	"settings.nav.data":         {Text: "Datos"},
	"settings.nav.appearance":   {Text: "Apariencia"},
	"settings.nav.about":        {Text: "Acerca de"},
	"settings.nav.unknown":      {Text: "Sección desconocida"},

	"settings.common.state.errorDetail":     {Text: "Algo ha salido mal: {arg1}"},
	"settings.common.state.disabledGeneric": {Text: "Este panel no está disponible ahora mismo."},
	"settings.common.yes":                   {Text: "Sí"},
	"settings.common.no":                    {Text: "No"},

	"settings.security.title":                                      {Text: "Seguridad"},
	"settings.security.sessions.current":                           {Text: "{arg1}"},
	"settings.security.passwordPolicy.hint":                        {Text: "Entre {arg1} y {arg2} caracteres."},
	"settings.security.changePassword.title":                       {Text: "Cambiar la contraseña"},
	"settings.security.changePassword.current":                     {Text: "Contraseña actual"},
	"settings.security.changePassword.totp":                        {Text: "Código de 6 dígitos"},
	"settings.security.changePassword.new":                         {Text: "Contraseña nueva"},
	"settings.security.changePassword.error":                       {Text: "No se pudo cambiar la contraseña. Revisa tus datos e inténtalo de nuevo."},
	"settings.security.changePassword.success":                     {Text: "Contraseña cambiada."},
	"settings.security.changePassword.submit":                      {Text: "Cambiar la contraseña"},
	"settings.security.changePassword.revokesOtherSessionsWarning": {Text: "Cambiar la contraseña cierra todas las demás sesiones activas."},
	"settings.security.reenrollTotp.title":                         {Text: "Volver a configurar la autenticación en dos pasos"},
	"settings.security.reenrollTotp.currentPassword":               {Text: "Contraseña actual"},
	"settings.security.reenrollTotp.error":                         {Text: "No se pudo volver a configurar. Revisa tu contraseña e inténtalo de nuevo."},
	"settings.security.reenrollTotp.submit":                        {Text: "Volver a configurar"},
	"settings.security.reenrollTotp.shownOnce":                     {Text: "Escanea esto ahora en tu aplicación de autenticación: no se volverá a mostrar."},
	"settings.security.recoveryCodes.title":                        {Text: "Códigos de recuperación"},
	"settings.security.recoveryCodes.remaining": {
		PluralArg: "arg1",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "Queda {arg1} código de recuperación.",
			gwci18n.PluralOther: "Quedan {arg1} códigos de recuperación.",
		},
	},
	"settings.security.recoveryCodes.lowNag":                  {Text: "Te quedan pocos códigos de recuperación: genera un juego nuevo."},
	"settings.security.recoveryCodes.regenerate.action":       {Text: "Regenerar"},
	"settings.security.recoveryCodes.regenerate.confirmTitle": {Text: "Regenerar los códigos de recuperación"},
	"settings.security.recoveryCodes.regenerate.prompt":       {Text: "Escribe la frase de confirmación que aparece abajo para regenerar tus códigos de recuperación. Todos los códigos actuales dejarán de funcionar de inmediato."},
	"settings.security.recoveryCodes.regenerate.confirmWord":  {Text: "REGENERAR"},
	"settings.security.recoveryCodes.error":                   {Text: "No se pudieron regenerar los códigos de recuperación."},
	"settings.security.recoveryCodes.shownOnce":               {Text: "Estos códigos no se volverán a mostrar: guárdalos ahora."},
	"settings.security.sessions.title":                        {Text: "Sesiones activas"},
	"settings.security.sessions.caption":                      {Text: "Sesiones activas"},
	"settings.security.sessions.revoked":                      {Text: "Revocada"},
	"settings.security.sessions.hiddenRevoked": {
		PluralArg: "arg1",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "No se muestra {arg1} sesión revocada más antigua.",
			gwci18n.PluralOther: "No se muestran {arg1} sesiones revocadas más antiguas.",
		},
	},
	"settings.security.sessions.revokeError":            {Text: "No se pudo revocar esa sesión. Puede que ya no exista: actualiza para comprobarlo."},
	"settings.security.sessions.revoke":                 {Text: "Revocar"},
	"settings.security.sessions.revokeAll.action":       {Text: "Revocar todas las sesiones"},
	"settings.security.sessions.revokeAll.confirmTitle": {Text: "Revocar todas las sesiones"},
	"settings.security.sessions.revokeAll.prompt":       {Text: "Escribe la frase de confirmación que aparece abajo para revocar todas las sesiones, incluida esta. Se cerrará tu sesión."},
	"settings.security.sessions.revokeAll.confirmWord":  {Text: "REVOCAR TODO"},
	"settings.security.sessions.revokeAll.warning":      {Text: "Esto cierra todas las sesiones, incluida esta: se cerrará tu sesión."},
	"settings.security.sessions.col.device":             {Text: "Dispositivo"},
	"settings.security.sessions.col.ip":                 {Text: "Dirección IP"},
	"settings.security.sessions.col.lastSeen":           {Text: "Última actividad"},
	"settings.security.sessions.col.current":            {Text: "Actual"},
	"settings.security.sessions.col.actions":            {Text: "Acciones"},

	"settings.publishing.title":           {Text: "Publicación"},
	"settings.publishing.baseUrl":         {Text: "URL base"},
	"settings.publishing.baseUrl.invalid": {Text: "Introduce una URL válida."},
	"settings.publishing.author":          {Text: "Autor"},
	"settings.publishing.contact":         {Text: "Contacto"},
	"settings.publishing.copyright":       {Text: "Derechos de autor"},
	"settings.publishing.ttlMinutes":      {Text: "TTL (minutos)"},
	"settings.publishing.cacheControl":    {Text: "Cabecera Cache-Control"},
	"settings.publishing.ogImage":         {Text: "URL de la imagen Open Graph"},
	"settings.publishing.ogImage.invalid": {Text: "Introduce una URL de imagen válida."},
	"settings.publishing.saveError":       {Text: "No se pudieron guardar los ajustes de publicación."},
	"settings.publishing.saved":           {Text: "Guardado."},
	"settings.publishing.save":            {Text: "Guardar"},

	"settings.provider.model.choose":            {Text: "Elige un modelo…"},
	"settings.provider.model.group.recommended": {Text: "Para este campo"},
	"settings.provider.model.group.other":       {Text: "Otros modelos"},

	"settings.provider.model.notListed":   {Text: "{arg1} (el proveedor no lo lista)"},
	"settings.provider.model.unreachable": {Text: "No se pudo contactar con el servidor para listar los modelos: escribe el id del modelo a mano."},

	"settings.provider.connection.title": {Text: "Conexión"},
	"settings.provider.connection.help":  {Text: "Sirve cualquier endpoint compatible con OpenAI: un servidor de modelos local, una pasarela o un revendedor. Las claves se leen del entorno del servidor y nunca se guardan aquí."},
	"settings.provider.profile.label":    {Text: "Endpoint"},
	"settings.provider.profile.builtin":  {Text: "OpenAI (integrado)"},
	"settings.provider.profile.add":      {Text: "Añadir endpoint"},
	"settings.provider.profile.name":     {Text: "Nombre"},
	"settings.provider.profile.baseUrl":  {Text: "URL base"},

	"settings.provider.profile.keyEnv":     {Text: "Variable de la clave"},
	"settings.provider.profile.keySet":     {Text: "Clave encontrada"},
	"settings.provider.profile.keyMissing": {Text: "La variable de la clave no está definida en el servidor"},
	"settings.provider.profile.noKeyEnv":   {Text: "Sin variable de clave con ese nombre"},

	"settings.provider.profile.noKeyOption": {Text: "{arg1}: {arg2} no está definida"},
	"settings.provider.profile.remove":      {Text: "Quitar el endpoint {arg1}"},

	"settings.provider.model.title":  {Text: "Modelo y esfuerzo"},
	"settings.provider.effort.label": {Text: "Esfuerzo"},
	"settings.provider.effort.help":  {Text: "Cuánto trabaja el modelo en cada ejecución. Más esfuerzo cuesta más y tarda más."},
	"settings.provider.effort.smart": {Text: "Cuidadoso: el más exhaustivo"},
	"settings.provider.effort.fast":  {Text: "Rápido: equilibrado"},
	"settings.provider.effort.quick": {Text: "Muy rápido: el más barato"},

	"settings.provider.priceTable.add":  {Text: "Añadir tarifa"},
	"settings.provider.priceTable.help": {Text: "Se usa para estimar el coste de las ejecuciones. Los precios publicados cambian, y una tarifa desactualizada hace que todas las cifras de coste de aquí sean erróneas."},

	"settings.provider.cost.title":  {Text: "Gasto"},
	"settings.provider.cost.window": {Text: "Periodo"},

	"settings.provider.cost.windowDays":    {Text: "Últimos {arg1} días"},
	"settings.provider.cost.windowCaption": {Text: "Total de los últimos {arg1} días"},
	"settings.provider.cost.empty":         {Text: "Todavía no hay ejecuciones: no se ha gastado nada."},

	"settings.provider.cost.bucket": {Text: "{arg1}: {arg2} en {arg3} ejecuciones"},

	"settings.provider.cost.chartLabel": {Text: "Gasto diario a lo largo de {arg1} días, {arg2} en total"},

	"settings.provider.title":                {Text: "Proveedor"},
	"settings.provider.activeProvider":       {Text: "Proveedor activo"},
	"settings.provider.defaultModel":         {Text: "Modelo predeterminado"},
	"settings.provider.embeddingModel":       {Text: "Modelo de embeddings"},
	"settings.provider.apiKeyPresent":        {Text: "Clave de API presente: {arg1}"},
	"settings.provider.priceTable.title":     {Text: "Tabla de precios"},
	"settings.provider.priceTable.caption":   {Text: "Tabla de precios"},
	"settings.provider.priceTable.col.model": {Text: "Modelo"},

	"settings.provider.priceTable.err.emptyModel": {Text: "La fila {arg1} no tiene modelo: una tarifa sin modelo no se aplica nunca a nada."},
	"settings.provider.priceTable.err.duplicate":  {Text: "La fila {arg1} repite un modelo que ya tiene tarifa."},
	"settings.provider.priceTable.err.negative":   {Text: "La fila {arg1} tiene una tarifa negativa."},
	"settings.provider.priceTable.col.in":         {Text: "$ de entrada por 1K tokens"},
	"settings.provider.priceTable.col.out":        {Text: "$ de salida por 1K tokens"},
	"settings.provider.saveError":                 {Text: "No se pudieron guardar los ajustes del proveedor."},
	"settings.provider.saved":                     {Text: "Guardado."},
	"settings.provider.save":                      {Text: "Guardar"},

	"settings.generation.title":                   {Text: "Generación"},
	"settings.generation.killSwitch":              {Text: "Generación activada"},
	"settings.generation.killSwitch.reason":       {Text: "La generación está desactivada. Ningún feed se ejecutará hasta que se vuelva a activar."},
	"settings.generation.group.ceilings":          {Text: "Topes globales"},
	"settings.generation.group.ceilings.help":     {Text: "El límite de seguridad de toda la instalación, sumando todos los feeds. 0 significa sin límite."},
	"settings.generation.group.feedDefaults":      {Text: "Valores por defecto de cada feed"},
	"settings.generation.group.feedDefaults.help": {Text: "Con qué empieza un feed recién creado. Cambiar esto no altera los feeds que ya existen."},
	"settings.generation.group.staleness":         {Text: "Desactualización"},
	"settings.generation.globalTokenCeiling":      {Text: "Tope global diario de tokens (tokens)"},
	"settings.generation.globalSpendCeiling":      {Text: "Tope global diario de gasto (USD)"},
	"settings.generation.defaultTokenBudget":      {Text: "Presupuesto de tokens por feed por defecto (tokens)"},
	"settings.generation.defaultRunBudget":        {Text: "Presupuesto de ejecuciones por feed por defecto (ejecuciones/día)"},
	"settings.generation.defaultFeedWindow":       {Text: "Ventana del feed por defecto (elementos)"},
	"settings.generation.stalenessThreshold":      {Text: "Umbral de desactualización (minutos)"},
	"settings.generation.saveError":               {Text: "No se pudieron guardar los ajustes de generación."},
	"settings.generation.saved":                   {Text: "Guardado."},
	"settings.generation.save":                    {Text: "Guardar"},

	"settings.data.title": {Text: "Datos"},
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
			gwci18n.PluralOne:   "{arg1} elemento",
			gwci18n.PluralOther: "{arg1} elementos",
		},
	},
	"settings.data.stats.dbSize": {Text: "Tamaño de la base de datos: {arg1}"},
	"settings.data.stats.feeds":  {Text: "Feeds"},
	"settings.data.stats.items":  {Text: "Elementos"},
	"settings.data.stats.size":   {Text: "Tamaño de la base de datos"},

	"settings.data.recipe.title": {Text: "Exportar una receta"},
	"settings.data.recipe.help":  {Text: "Descarga la configuración de un feed (prompts, programación, presupuestos) como un archivo que puedes guardar o pasar a otra instalación."},

	"settings.data.recipe.import.help":        {Text: "Sustituye la configuración de un feed por la de abajo. El feed conserva sus elementos y su URL; todo lo demás se sobrescribe."},
	"settings.data.recipe.import.placeholder": {Text: "Pega aquí una receta exportada"},
	"settings.data.backup.help":               {Text: "Una instantánea de toda la base de datos (todos los feeds, elementos y ejecuciones) en un único archivo descargable."},
	"settings.data.recipe.feed":               {Text: "Feed"},
	"settings.data.recipe.export":             {Text: "Exportar"},

	"settings.data.recipe.exportTitle":         {Text: "Receta exportada"},
	"settings.data.recipe.exportError":         {Text: "No se pudo exportar la receta."},
	"settings.data.recipe.importTitle":         {Text: "Importar receta"},
	"settings.data.recipe.import.action":       {Text: "Importar"},
	"settings.data.recipe.import.confirmTitle": {Text: "Importar receta"},
	"settings.data.recipe.import.prompt":       {Text: "Escribe la frase de confirmación que aparece abajo para sobrescribir la receta de este feed con el archivo importado."},
	"settings.data.recipe.importError":         {Text: "No se pudo importar la receta."},
	"settings.data.recipe.importSuccess":       {Text: "Importada."},
	"settings.data.importToml.confirmWord":     {Text: "IMPORTAR"},
	"settings.data.backup.title":               {Text: "Copia de seguridad"},
	"settings.data.backup.generate":            {Text: "Generar copia de seguridad"},
	"settings.data.backup.error":               {Text: "No se pudo generar la copia de seguridad."},
	"settings.data.backup.download":            {Text: "Descargar"},
	"settings.data.backup.currentPassword":     {Text: "Contraseña actual"},
	"settings.data.backup.totp":                {Text: "Código del autenticador"},
	"settings.data.backup.credentialsWarning":  {Text: "El archivo de copia de seguridad contiene todas las credenciales de esta base de datos, incluidos el hash de la contraseña de administrador y el secreto cifrado del autenticador. Confirma que eres tú antes de generar una."},
	"settings.data.vacuum.title":               {Text: "Compactar"},

	"settings.data.vacuum.unavailable": {Text: "Compactar solo está disponible desde la CLI del servidor."},
	"settings.data.vacuum.action":      {Text: "Compactar la base de datos"},
	"settings.data.vacuum.description": {Text: "Reconstruye el archivo de la base de datos para recuperar el espacio que dejaron las filas eliminadas."},

	"settings.data.vacuum.estimate.brief":    {Text: "La aplicación no estará disponible mientras se ejecuta; con el tamaño actual debería ser breve."},
	"settings.data.vacuum.estimate.moderate": {Text: "La aplicación no estará disponible mientras se ejecuta, probablemente unos minutos con el tamaño actual."},
	"settings.data.vacuum.estimate.long":     {Text: "La aplicación no estará disponible mientras se ejecuta, y con el tamaño actual puede tardar mucho. Haz antes una copia de seguridad."},
	"settings.data.vacuum.confirmTitle":      {Text: "¿Compactar la base de datos?"},
	"settings.data.vacuum.confirmPrompt":     {Text: "{arg1} Una vez empieza, no se puede interrumpir."},

	"settings.data.vacuum.confirmWord":  {Text: "COMPACTAR"},
	"settings.data.vacuum.running":      {Text: "Compactando: la aplicación no estará disponible hasta que termine."},
	"settings.data.vacuum.error":        {Text: "La compactación ha fallado: {arg1}"},
	"settings.data.vacuum.result.sizes": {Text: "La base de datos ha pasado de {arg1} a {arg2}."},
	"settings.data.vacuum.result.duration": {
		PluralArg: "arg1",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "Ha tardado {arg1} minuto y {arg2} segundos.",
			gwci18n.PluralOther: "Ha tardado {arg1} minutos y {arg2} segundos.",
		},
	},

	"settings.common.disconnectedReason": {Text: "Reconectando con el servidor: estos controles no están disponibles hasta que vuelva."},

	// Appearance section (render_appearance.go) — the language selector and
	// the theme control that moved here out of the header.
	"settings.appearance.title": {Text: "Apariencia"},
	"settings.appearance.help":  {Text: "Cómo se ve y se lee esta aplicación en este dispositivo. Ambos ajustes se guardan solo en este navegador: acompañan al navegador, no a tu cuenta, y no cambian nada de lo que publican tus feeds."},

	"settings.appearance.language.title": {Text: "Idioma"},
	"settings.appearance.language.label": {Text: "Idioma de la interfaz"},
	"settings.appearance.language.help":  {Text: "Cambia al instante todas las pantallas y controles. El contenido de los feeds no se ve afectado: cada feed se publica en el idioma que indique su receta."},
	// Deliberately untranslated in every catalogue — see the note on this key
	// in keys_settings.go. Present here so the parity test passes and so
	// nobody later "fixes" its absence by translating it.
	"settings.appearance.language.machineNote": {Text: "Las traducciones que no están en inglés las escribió el modelo que creó esta función y no las ha revisado ningún hablante nativo."},

	"settings.appearance.theme.title":  {Text: "Tema"},
	"settings.appearance.theme.label":  {Text: "Tema de color"},
	"settings.appearance.theme.help":   {Text: "«Igual que el sistema» sigue el ajuste de claro/oscuro de tu sistema operativo, incluido un cambio programado a una hora concreta."},
	"settings.appearance.theme.system": {Text: "Igual que el sistema"},
	"settings.appearance.theme.light":  {Text: "Claro"},
	"settings.appearance.theme.dark":   {Text: "Oscuro"},

	"settings.about.title": {Text: "Acerca de"},

	"settings.about.what.title":    {Text: "Qué hace esta aplicación"},
	"settings.about.what.body":     {Text: "Publica feeds que se escriben solos. Tú describes qué debe contener un feed y con qué frecuencia debe actualizarse; la aplicación escribe cada entrada nueva por ti y la publica como un feed, de modo que un lector de feeds o un canal de Slack puede suscribirse y recibir las entradas según van apareciendo."},
	"settings.about.what.generate": {Text: "Generar: describe un feed, previsualiza lo que produciría y ejecútalo."},
	"settings.about.what.history":  {Text: "Historial: todas las ejecuciones que ha hecho la aplicación, qué produjeron y cuánto costaron."},
	"settings.about.what.settings": {Text: "Ajustes: qué servicio de IA escribe las entradas, cada cuánto se actualizan los feeds, dónde se publican, el inicio de sesión y tus datos guardados."},

	"settings.about.built.title":   {Text: "Sobre qué funciona"},
	"settings.about.built.server":  {Text: "El servidor es un único programa que guarda todo (feeds, entradas, ajustes e historial) en un solo archivo de base de datos, en la máquina donde se ejecuta. No hay nada más que instalar ni conectar."},
	"settings.about.built.client":  {Text: "Esta página es la interfaz de la propia aplicación, hablando con ese servidor por una conexión permanente. Si la conexión se cae, un aviso lo indica y los controles se desactivan hasta que vuelve."},
	"settings.about.built.ai":      {Text: "El texto de cada entrada lo escribe un servicio de IA que tú eliges y pagas, configurado en Proveedor. No se genera nada hasta que configures uno."},
	"settings.about.built.formats": {Text: "Cada feed se publica a la vez en los tres formatos de feed estándar, para que puedas suscribirte con el lector que uses."},

	"settings.about.install.title": {Text: "Esta instalación"},
	"settings.about.install.help":  {Text: "Estas tres líneas identifican la copia de la aplicación que estás viendo. Conviene citarlas si alguna vez informas de un problema."},
	"settings.about.version":       {Text: "Versión {arg1}: qué versión de la aplicación se está ejecutando."},
	"settings.about.build":         {Text: "Build {arg1}: el código exacto con el que se compiló esta copia."},
	"settings.about.uptime":        {Text: "Lleva funcionando {arg1}: tiempo desde el último arranque del servidor. Se reinicia cada vez que el servidor se reinicia y no dice nada sobre tus feeds."},
	"settings.about.uptime.parts":  {Text: "{arg1}d {arg2}h {arg3}min"},

	"settings.about.feed.neverBuilt":     {Text: "Todavía sin publicar"},
	"settings.about.feed.lastBuildTitle": {Text: "Tus feeds"},
	"settings.about.feed.help":           {Text: "Una fila por cada feed que tengas configurado. El nombre que se muestra es el nombre corto que aparece en la dirección web del feed, y la hora que hay al lado es cuándo publicó ese feed una versión nueva por última vez."},
	"settings.about.feed.caption":        {Text: "Cada feed y cuándo publicó por última vez"},
	"settings.about.feed.col.slug":       {Text: "Feed"},
	"settings.about.feed.col.lastBuild":  {Text: "Última publicación"},
}
