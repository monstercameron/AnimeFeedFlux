package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// esShellMessages is the Spanish shell namespace (banner, expiry hold,
// header, guard notice), keyed by keys_shell.go's own constants for the same
// compile-time-parity reason es_common.go documents.
//
// "AnimeFeedFlux" is a product name and stays untranslated in every locale.
// "Admin" beside it does not — it is a word describing the thing, not part
// of the name.
var esShellMessages = gwci18n.NamespaceCatalog{
	KeyShellBannerDisconnected: {Text: "Sin conexión: reconectando…"},
	KeyShellBannerReconnectingIn: {
		PluralArg: "seconds",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "Sin conexión: reconectando dentro de {seconds} segundo…",
			gwci18n.PluralOther: "Sin conexión: reconectando dentro de {seconds} segundos…",
		},
	},

	KeyShellExpiryTitle: {Text: "Sesión caducada"},
	KeyShellExpiryBody:  {Text: "Tu sesión ha caducado. Los cambios sin guardar se conservan hasta que vuelvas a iniciar sesión."},
	KeyShellExpiryLogin: {Text: "Iniciar sesión"},

	KeyShellGuardRedirectNotice: {Text: "Te hemos redirigido porque primero tienes que iniciar sesión."},

	KeyShellSessionExpiredNotice: {Text: "Tu sesión ha caducado, así que se ha cerrado. Inicia sesión para retomarlo donde lo dejaste."},

	KeyShellNotImplemented: {Text: "Página aún no implementada: {path}"},

	KeyShellHeaderNavLabel:    {Text: "Navegación principal"},
	KeyShellHeaderNavGenerate: {Text: "Generar"},
	KeyShellHeaderNavHistory:  {Text: "Historial"},
	KeyShellHeaderNavSettings: {Text: "Ajustes"},
	KeyShellHeaderSignOut:     {Text: "Cerrar sesión"},

	KeyShellHeaderBrandLabel:     {Text: "Administración de AnimeFeedFlux"},
	KeyShellHeaderBrandHomeLabel: {Text: "Administración de AnimeFeedFlux: ir a Generar"},

	KeyShellHeaderSignOutBusy: {Text: "Cerrando sesión…"},

	KeyShellHeaderSignOutError: {Text: "No se pudo cerrar la sesión. Inténtalo de nuevo."},
}
