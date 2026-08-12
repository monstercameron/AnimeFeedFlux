package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// esCommonMessages is the Spanish common namespace — keyed by the SAME
// exported Key* constants keys_common.go declares, never by restated string
// literals. That is deliberate: it makes key parity with English a compile-
// time property for this namespace (rename a constant and both catalogues
// move together, or neither compiles) rather than something a test has to
// notice afterwards. The three page namespaces cannot do this, because they
// hardcode their key strings at the call site by design — see
// keys_settings.go's doc comment — which is exactly why
// TestEveryLocaleHasEveryKey exists.
var esCommonMessages = gwci18n.NamespaceCatalog{
	KeyCommonGenericAuthError: {Text: "No ha funcionado. Revisa tus datos e inténtalo de nuevo."},
	KeyCommonBackoffNotice: {
		PluralArg: "count",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "Demasiados intentos. Inténtalo de nuevo dentro de {count} segundo.",
			gwci18n.PluralOther: "Demasiados intentos. Inténtalo de nuevo dentro de {count} segundos.",
		},
	},
	KeyCommonBack:       {Text: "Atrás"},
	KeyCommonSubmitting: {Text: "Procesando…"},

	KeyCommonConnectionUnreachable: {Text: "No se pudo contactar con el servidor: no se envió nada. Comprueba la conexión e inténtalo de nuevo."},
	KeyCommonBackoffCleared:        {Text: "Ya puedes intentarlo de nuevo."},

	KeyActionSave:           {Text: "Guardar"},
	KeyActionCancel:         {Text: "Cancelar"},
	KeyActionRetry:          {Text: "Reintentar"},
	KeyActionClose:          {Text: "Cerrar"},
	KeyActionDismiss:        {Text: "Descartar"},
	KeyActionConfirmDestroy: {Text: "Confirmar"},

	KeyStateLoading:      {Text: "Cargando…"},
	KeyStateEmpty:        {Text: "Aquí todavía no hay nada."},
	KeyStateError:        {Text: "Algo ha salido mal."},
	KeyStateDisabled:     {Text: "Desactivado"},
	KeyStateDisconnected: {Text: "Reconectando…"},

	// {arg1} is the phrase to type, which is itself a translated word (e.g.
	// settings.security.recoveryCodes.regenerate.confirmWord). Both sides
	// have to be translated or neither: a Spanish instruction to type
	// "REGENERATE" is a lock with the key filed off.
	KeyConfirmTypePhrase: {Text: "Escribe {arg1} para confirmar."},
	KeyConfirmTypeLabel:  {Text: "Frase de confirmación"},

	KeyKebabActionsFor: {Text: "Acciones para {arg1}"},
}
