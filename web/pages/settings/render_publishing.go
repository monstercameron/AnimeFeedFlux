//go:build js && wasm

package settings

import (
	"strconv"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// renderPublishing is the Publishing section (D4-09): base URL, author,
// copyright, TTL, default og:image — all validated on save (absolute
// URL, correct scheme) because these land in every channel element of
// every feed (PLAN.md §12.5).
func renderPublishing() ui.Node {
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

	ui.UseEffect(func() func() {
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
	}, "publishing-mount")

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

	state := ComputeScreenState(ScreenInputs{Loading: loading.Get(), Err: errState.Get(), ItemCount: 1})
	errs := fieldErr.Get()

	body := h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.publishing.title"))),

		h.Label(h.Text(t("settings.publishing.baseUrl")),
			h.Input(h.Value(baseURL.Get()), h.OnInput(func(e ui.InputEvent) { baseURL.Set(e.GetValue()) }))),
		h.Show(errs["baseURL"] != nil, h.P(h.ClassStr("af-error"), h.Text(t("settings.publishing.baseUrl.invalid")))),

		h.Label(h.Text(t("settings.publishing.author")),
			h.Input(h.Value(author.Get()), h.OnInput(func(e ui.InputEvent) { author.Set(e.GetValue()) }))),
		h.Label(h.Text(t("settings.publishing.contact")),
			h.Input(h.Value(contact.Get()), h.OnInput(func(e ui.InputEvent) { contact.Set(e.GetValue()) }))),
		h.Label(h.Text(t("settings.publishing.copyright")),
			h.Input(h.Value(copyright.Get()), h.OnInput(func(e ui.InputEvent) { copyright.Set(e.GetValue()) }))),
		h.Label(h.Text(t("settings.publishing.ttlMinutes")),
			h.Input(h.Type("number"), h.Value(strconv.Itoa(int(ttlMinutes.Get()))),
				h.OnInput(func(e ui.InputEvent) { ttlMinutes.Set(int32(parseInt64(e.GetValue()))) }))),
		h.Label(h.Text(t("settings.publishing.cacheControl")),
			h.Input(h.Value(cacheControl.Get()), h.OnInput(func(e ui.InputEvent) { cacheControl.Set(e.GetValue()) }))),
		h.Label(h.Text(t("settings.publishing.ogImage")),
			h.Input(h.Value(ogImage.Get()), h.OnInput(func(e ui.InputEvent) { ogImage.Set(e.GetValue()) }))),
		h.Show(errs["ogImage"] != nil, h.P(h.ClassStr("af-error"), h.Text(t("settings.publishing.ogImage.invalid")))),

		h.Show(errState.Get() != nil, h.P(h.ClassStr("af-error"), h.Text(t("settings.publishing.saveError")))),
		h.Show(saved.Get(), h.P(h.ClassStr("af-success"), h.Text(t("settings.publishing.saved")))),
		h.Button(h.Type("button"), h.DisabledIf(saving.Get()), h.OnClick(doSave), h.Text(t("settings.publishing.save"))),
	)

	return screenWrapper(state, errState.Get(), body)
}
