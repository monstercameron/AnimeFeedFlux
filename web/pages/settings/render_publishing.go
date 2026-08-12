//go:build js && wasm

package settings

import (
	"strconv"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	affui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// renderPublishing is the Publishing section (D4-09): base URL, author,
// copyright, TTL, default og:image — all validated on save (absolute
// URL, correct scheme) because these land in every channel element of
// every feed (PLAN.md §12.5).
func renderPublishing() ui.Node {
	disconnected := isDisconnected()

	loading := ui.UseState(true)
	errState := ui.UseState(error(nil))
	saving := ui.UseState(false)
	saved := ui.UseState(false)
	fieldErr := ui.UseState(map[string]error(nil))

	baseURL := ui.UseState("")
	author := ui.UseState("")
	contact := ui.UseState("")
	copyright := ui.UseState("")
	ttlMinutes := ui.UseState(int32(0))
	cacheControl := ui.UseState("")
	ogImage := ui.UseState("")

	// reloadTick drives the error view's Retry control: the load effect is
	// keyed on it rather than on a constant, so bumping it re-runs the load.
	// §12.6 — an error with no way out is a dead end.
	reloadTick := ui.UseState(0)
	ui.UseEffectOf(func() func() {
		go func() {
			loading.Set(true)
			resp, err := deps.System.GetSettings(bgContext(), &affv1.SystemServiceGetSettingsRequest{})
			loading.Set(false)
			if err != nil {
				errState.Set(err)
				return
			}
			p := resp.GetSettings().GetPublishing()
			baseURL.Set(p.GetPublicBaseUrl())
			author.Set(p.GetDefaultAuthor())
			contact.Set(p.GetDefaultContact())
			copyright.Set(p.GetDefaultCopyright())
			ttlMinutes.Set(p.GetDefaultTtlMinutes())
			cacheControl.Set(p.GetDefaultCacheControl())
			ogImage.Set(p.GetDefaultOgImage())
		}()
		return nil
	}, reloadTick.Get())

	validate := func() map[string]error {
		errs := map[string]error{}
		if err := ValidateAbsoluteURL(baseURL.Get(), PublishingURLSchemes...); err != nil {
			errs["baseURL"] = err
		}
		if ogImage.Get() != "" {
			if err := ValidateAbsoluteURL(ogImage.Get(), PublishingURLSchemes...); err != nil {
				errs["ogImage"] = err
			}
		}
		return errs
	}

	doSave := func() {
		if disconnected {
			return
		}
		errs := validate()
		fieldErr.Set(errs)
		if len(errs) > 0 {
			return
		}
		saving.Set(true)
		saved.Set(false)
		errState.Set(nil)
		go func() {
			_, err := deps.System.UpdateSettings(bgContext(), &affv1.SystemServiceUpdateSettingsRequest{
				Settings: &affv1.Settings{
					Publishing: &affv1.Settings_Publishing{
						PublicBaseUrl:       baseURL.Get(),
						DefaultAuthor:       author.Get(),
						DefaultContact:      contact.Get(),
						DefaultCopyright:    copyright.Get(),
						DefaultTtlMinutes:   ttlMinutes.Get(),
						DefaultCacheControl: cacheControl.Get(),
						DefaultOgImage:      ogImage.Get(),
					},
				},
			})
			saving.Set(false)
			if err != nil {
				errState.Set(err)
				return
			}
			saved.Set(true)
		}()
	}

	state := ComputeScreenState(ScreenInputs{Loading: loading.Get(), Err: errState.Get(), Disconnected: disconnected, ItemCount: 1})
	errs := fieldErr.Get()

	baseURLErrKey, baseURLArgs := "", []any(nil)
	if errs["baseURL"] != nil {
		baseURLErrKey = "settings.publishing.baseUrl.invalid"
	}
	ogImageErrKey := ""
	if errs["ogImage"] != nil {
		ogImageErrKey = "settings.publishing.ogImage.invalid"
	}

	body := h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.publishing.title"))),
		h.Show(disconnected, h.P(h.Role("status"), h.ClassStr("af-warning"), h.Text(t("settings.common.disconnectedReason")))),

		affui.Input(affui.InputProps{
			T: t, ID: "settings-pub-baseurl", LabelKey: "settings.publishing.baseUrl",
			Value: baseURL.Get(), ErrorKey: baseURLErrKey, ErrorArgs: baseURLArgs,
			OnChange: func(v string) { baseURL.Set(v) },
		}),
		affui.Input(affui.InputProps{
			T: t, ID: "settings-pub-author", LabelKey: "settings.publishing.author",
			Value: author.Get(), OnChange: func(v string) { author.Set(v) },
		}),
		affui.Input(affui.InputProps{
			T: t, ID: "settings-pub-contact", LabelKey: "settings.publishing.contact",
			Value: contact.Get(), OnChange: func(v string) { contact.Set(v) },
		}),
		affui.Input(affui.InputProps{
			T: t, ID: "settings-pub-copyright", LabelKey: "settings.publishing.copyright",
			Value: copyright.Get(), OnChange: func(v string) { copyright.Set(v) },
		}),
		affui.Input(affui.InputProps{
			T: t, ID: "settings-pub-ttl", LabelKey: "settings.publishing.ttlMinutes",
			Type: "number", Mono: true, Value: strconv.Itoa(int(ttlMinutes.Get())),
			OnChange: func(v string) { ttlMinutes.Set(int32(parseInt64(v))) },
		}),
		affui.Input(affui.InputProps{
			T: t, ID: "settings-pub-cache", LabelKey: "settings.publishing.cacheControl",
			Mono: true, Value: cacheControl.Get(), OnChange: func(v string) { cacheControl.Set(v) },
		}),
		affui.Input(affui.InputProps{
			T: t, ID: "settings-pub-ogimage", LabelKey: "settings.publishing.ogImage",
			Value: ogImage.Get(), ErrorKey: ogImageErrKey,
			OnChange: func(v string) { ogImage.Set(v) },
		}),

		h.Show(errState.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.publishing.saveError")))),
		h.Show(saved.Get(), h.P(h.Role("status"), h.Aria("live", "polite"), h.ClassStr("af-success"), h.Text(t("settings.publishing.saved")))),
		affui.Button(affui.ButtonProps{
			T: t, LabelKey: "settings.publishing.save", Variant: affui.ButtonPrimary,
			Disabled: disconnected, Busy: saving.Get(), OnClick: doSave,
		}),
	)

	return screenWrapperRetry(state, errState.Get(), func() { reloadTick.Update(func(n int) int { return n + 1 }) }, body)
}
