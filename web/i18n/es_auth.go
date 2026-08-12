package i18n

import gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"

// esAuthMessages is the Spanish auth namespace (/login, /recover), keyed by
// keys_auth.go's own constants.
//
// Two terms are held fixed across every string here, because inconsistency
// in security copy is not a style problem, it is a comprehension problem —
// an operator locked out at 2am reading three names for the same object has
// to work out whether they are the same object:
//
//   - "código de recuperación" for recovery code, never "código de rescate".
//   - "autenticación en dos pasos" for two-factor authentication, matching
//     what Spanish-language authenticator apps themselves say.
//
// "aff admin reset" in KeyRecoverBreakGlassCommandNote is a command to type
// and is left exactly as-is; translating a command is how you get a support
// ticket about a command that does not exist.
var esAuthMessages = gwci18n.NamespaceCatalog{
	KeyLoginTitle:             {Text: "Iniciar sesión"},
	KeyLoginPasswordStepLabel: {Text: "Paso 1 de 2: contraseña"},
	KeyLoginPasswordLabel:     {Text: "Contraseña"},
	KeyLoginContinue:          {Text: "Continuar"},
	KeyLoginTOTPStepLabel:     {Text: "Paso 2 de 2: código de autenticación"},
	KeyLoginTOTPLabel:         {Text: "Código de 6 dígitos"},
	KeyLoginTOTPHint:          {Text: "Introduce el código actual de tu aplicación de autenticación."},
	KeyLoginSubmit:            {Text: "Iniciar sesión"},
	KeyLoginRecoverLink:       {Text: "¿No puedes iniciar sesión? Recupera tu cuenta"},

	KeyRecoverTitle: {Text: "Recupera tu cuenta"},
	KeyRecoverIntro: {Text: "Hay exactamente dos formas de volver a entrar: un código de recuperación introducido aquí abajo, o el acceso de emergencia en el propio servidor. No existe ningún «enviarme un enlace de restablecimiento»: este sistema no tiene infraestructura de correo."},

	KeyRecoverCodeStepLabel: {Text: "Código de recuperación"},
	KeyRecoverCodeLabel:     {Text: "Código de recuperación"},
	KeyRecoverCodeHint:      {Text: "Uno de los códigos de un solo uso que se mostraron al activar la autenticación en dos pasos. Cada código sirve una única vez."},
	KeyRecoverCodeSubmit:    {Text: "Continuar"},
	KeyRecoverRemainingCodes: {
		PluralArg: "count",
		Plural: map[gwci18n.PluralCategory]string{
			gwci18n.PluralOne:   "Queda {count} código de recuperación.",
			gwci18n.PluralOther: "Quedan {count} códigos de recuperación.",
		},
	},
	KeyRecoverLowCodesWarning: {Text: "Te quedan pocos códigos de recuperación: genera un juego nuevo desde Ajustes en cuanto vuelvas a iniciar sesión."},

	KeyRecoverChooseActionHeading: {Text: "Elige una acción"},
	KeyRecoverChooseActionIntro:   {Text: "Esta sesión de recuperación es válida durante 10 minutos y termina en cuanto completes cualquiera de las acciones de abajo: elige la que necesites. Si además quieres hacer la otra, usa después un segundo código de recuperación, o usa el acceso de emergencia, que hace las dos a la vez."},
	KeyRecoverChoosePasswordReset: {Text: "Establecer una contraseña nueva"},
	KeyRecoverChooseReenrollTOTP:  {Text: "Volver a configurar la autenticación en dos pasos"},

	KeyRecoverNewPasswordLabel:     {Text: "Contraseña nueva"},
	KeyRecoverConfirmPasswordLabel: {Text: "Confirma la contraseña nueva"},
	KeyRecoverPasswordMismatch:     {Text: "Las contraseñas no coinciden."},
	KeyRecoverPasswordTooShort:     {Text: "La contraseña debe tener al menos {min} caracteres."},
	KeyRecoverPasswordTooLong:      {Text: "La contraseña puede tener como máximo {max} caracteres."},
	KeyRecoverResetSubmit:          {Text: "Establecer contraseña nueva"},

	KeyRecoverReenrollHeading:     {Text: "Volver a configurar la autenticación en dos pasos"},
	KeyRecoverReenrollIntro:       {Text: "Esto sustituye tu clave de autenticación. Escanea el código nuevo en tu aplicación de autenticación antes de cerrar sesión: la anterior deja de funcionar de inmediato."},
	KeyRecoverReenrollSubmit:      {Text: "Volver a configurar"},
	KeyRecoverReenrollProvisioned: {Text: "Escanea esto ahora en tu aplicación de autenticación: no se volverá a mostrar: {uri}"},

	KeyRecoverDoneHeading:   {Text: "Listo"},
	KeyRecoverDoneBody:      {Text: "Esta sesión de recuperación ha terminado y se han cerrado todas las demás sesiones. Vuelve a iniciar sesión con tu credencial nueva."},
	KeyRecoverDoneGoToLogin: {Text: "Ir a iniciar sesión"},
	KeyRecoverCancel:        {Text: "Cancelar e iniciar sesión"},

	KeyRecoverSavedConfirm: {Text: "Lo he guardado en mi aplicación de autenticación"},

	KeyRecoverBreakGlassHeading:     {Text: "¿Sin acceso por completo?"},
	KeyRecoverBreakGlassBody:        {Text: "Si tampoco tienes códigos de recuperación, el acceso de emergencia restablece a la vez tu contraseña, tu TOTP y tus códigos de recuperación, pero solo funciona por SSH, directamente en el servidor y con acceso al archivo de la base de datos. No hay ninguna vía remota ni web para hacerlo."},
	KeyRecoverBreakGlassCommandNote: {Text: "Ejecuta: aff admin reset"},
}
