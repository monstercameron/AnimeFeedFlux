package publish

// static.go serves the compiled GWC/WASM admin bundle that web/build.sh
// stages into a serve directory (web/dist by default: app.wasm, app.wasm.gz,
// wasm_exec.js, index.html — see that script's doc comment for the
// scratch-dir build and atomic-replace staging, TODOS.md D0-02/D0-03,
// PLAN.md §15).
//
// ADMIN PLANE ONLY. PLAN.md §2 draws the publish/control split at "what the
// internet can reach unauthenticated": the public publish plane (§6) is a
// fixed, read-only feed route set (/, /feeds/*, /items/*, /healthz,
// /robots.txt, /favicon.ico) with no writer and no directory listing — it is
// not, and must never become, a general static file server. The admin UI is
// the WASM binary that drives the authenticated, IP-allowlisted control
// plane (`admin.anime.earlcameron.com`, §2, §12). StaticHandler belongs on
// that listener/mux only, alongside wherever GoGRPCBridge mounts its
// WebSocket endpoint. Mounting it on the public listener would let any
// unauthenticated internet client download the entire admin application —
// see NewStaticHandler's doc comment for exactly what the composition root
// (cmd/animefeedflux, out of scope here) needs to call and where.
//
// This file lives in package publish (rather than a new package) so it can
// share allowMethod/wantsGzip/etagMatch with server.go without exporting
// them, and so it is unit-testable in isolation from server.go and from
// cmd/animefeedflux's composition root.

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// staticContentTypes maps the small, fixed set of extensions web/build.sh
// emits to the Content-Type they must be served with. This is intentionally
// not mime.TypeByExtension: that function consults the OS registry (on
// Windows in particular), which is exactly the kind of environment-dependent
// answer that must not decide whether a browser stream-compiles a wasm
// binary or refuses it with a vague instantiation error.
var staticContentTypes = map[string]string{
	".wasm": "application/wasm",
	".js":   "text/javascript; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".json": "application/json; charset=utf-8",
	// .png/.ico: web/build.sh stages the brand icons (docs/
	// design-direction.md's brand assets) alongside the wasm bundle.
	// Without an entry here, loadStaticAsset's fallback below serves them
	// as application/octet-stream — browsers refuse to render an
	// octet-stream response as the tab favicon (and would offer it as a
	// download instead of inlining it), so the icon would silently never
	// appear even though the file itself was served successfully.
	".png": "image/png",
	// image/x-icon rather than the IANA-registered image/vnd.microsoft.icon:
	// the registered form is correct on paper and the de facto one is what
	// every browser actually keys its icon handling off, the same
	// spec-versus-practice split PLAN.md §5.4 already records for
	// application/rss+xml.
	".ico": "image/x-icon",
	// .svg is kept even though no SVG ships today: an unknown extension
	// fails silently (octet-stream, browser refuses to render), so the
	// entry costs nothing and removes a trap for whoever adds one back.
	".svg": "image/svg+xml",
}

// staticRepr is one representation (plain or gzip) of a static asset: the
// exact bytes served and the strong ETag derived from them.
type staticRepr struct {
	body []byte
	etag string
}

func newStaticRepr(body []byte) staticRepr {
	sum := sha256.Sum256(body)
	// Hashing the plain and gzip byte streams separately, rather than
	// deriving one ETag and reusing it for both representations, is what
	// makes the two ETags differ (they are different byte streams) without
	// any extra bookkeeping — and is required: a shared cache sitting in
	// front of this handler must be able to tell the two representations
	// apart by validator alone, or it can serve a gzip body to a client
	// that told it "no gzip" off a stale/shared cache entry.
	return staticRepr{body: body, etag: `"` + hex.EncodeToString(sum[:]) + `"`}
}

// staticAsset is one servable file, with its optional precompressed sibling.
type staticAsset struct {
	contentType string
	modTime     time.Time
	plain       staticRepr
	gzip        *staticRepr // nil when no "<name>.gz" sibling was found
}

// StaticHandler serves a snapshot of the admin static assets loaded once
// from disk. Loading everything into memory at construction time — rather
// than opening files per request — matches the rest of this package's
// "precompute once, serve cheaply" design (see cache.go's Entry) and means
// the handler never touches the filesystem, and never gzips anything, on
// the request path. That matters here specifically because build.sh
// precompresses at build time exactly so this handler does not have to
// spend CPU gzipping a multi-megabyte binary on every cold request on a
// 2 GB droplet already hosting four services (TODOS.md D0-03).
type StaticHandler struct {
	assets map[string]staticAsset // keyed by URL path, e.g. "/app.wasm"
}

// NewStaticHandler builds a StaticHandler from every regular file directly
// inside dir (web/build.sh's SERVE_DIR — web/dist by default), non-
// recursively. A file "<name>.gz" is treated as the precompressed sibling of
// "<name>" and is not exposed as its own route. "index.html", if present, is
// additionally served at "/".
//
// Wiring (cmd/animefeedflux, not done here): build (or accept as config) the
// SERVE_DIR path web/build.sh staged into, call NewStaticHandler(dir) once
// at startup, and mount the result on the ADMIN listener's mux — e.g.
// adminMux.Handle("/", staticHandler) — never on the mux passed to the
// public publish plane's server (server.go's NewServer/routes). If the
// admin mux also serves the GoGRPCBridge WebSocket endpoint at a specific
// path (e.g. "/rpc" or "/ws"), register that route on the same mux before
// or after this one; StaticHandler only claims the asset paths it found on
// disk plus "/", so it will not shadow a differently-named RPC route. A
// missing serve directory (build.sh has not run yet) is returned as an
// error here, not deferred to a panic on first request — the composition
// root should fail startup loudly rather than serve a 404-only admin UI.
func NewStaticHandler(dir string) (*StaticHandler, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	h := &StaticHandler{assets: make(map[string]staticAsset)}

	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".gz") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return nil, err
		}

		asset, err := loadStaticAsset(dir, entry.Name(), info.ModTime())
		if err != nil {
			return nil, err
		}

		h.assets["/"+entry.Name()] = asset
		if entry.Name() == "index.html" {
			h.assets["/"] = asset
		}
	}

	return h, nil
}

func loadStaticAsset(dir, name string, modTime time.Time) (staticAsset, error) {
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return staticAsset{}, err
	}

	contentType, ok := staticContentTypes[filepath.Ext(name)]
	if !ok {
		contentType = "application/octet-stream"
	}

	asset := staticAsset{
		contentType: contentType,
		modTime:     modTime,
		plain:       newStaticRepr(body),
	}

	gzPath := filepath.Join(dir, name+".gz")
	if gzBody, err := os.ReadFile(gzPath); err == nil {
		repr := newStaticRepr(gzBody)
		asset.gzip = &repr
	} else if !os.IsNotExist(err) {
		return staticAsset{}, err
	}

	return asset, nil
}

// ServeHTTP serves the asset at r.URL.Path, negotiating gzip against
// Accept-Encoding and honoring conditional GET, following the same
// discipline server.go's writeEntry uses for the publish plane (§5.4): pick
// the representation first, then validate the conditional request against
// that representation's own ETag — never the other one's — since the two
// representations are different documents from an HTTP-caching point of
// view (Vary: Accept-Encoding, RFC 9110 §13.1.1) even though they render to
// the same bytes in the browser.
// Has reports whether an asset is served at this exact URL path.
//
// It exists so the admin listener's mux can decide between "this is a real
// asset" and "this is a client-side route that needs the SPA shell" WITHOUT
// speculatively serving and then intercepting a 404 — the bundle is ~31 MB,
// and buffering a response to discover its status is not a reasonable price
// for a routing decision. Routing belongs to the mux; this type stays a pure
// asset server that knows nothing about the router's paths.
func (h *StaticHandler) Has(urlPath string) bool {
	_, ok := h.assets[urlPath]
	return ok
}

func (h *StaticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r) {
		return
	}

	asset, ok := h.assets[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}

	repr := asset.plain
	useGzip := false
	if asset.gzip != nil && wantsGzip(r) {
		repr = *asset.gzip
		useGzip = true
	}

	hdr := w.Header()
	hdr.Set("Content-Type", asset.contentType)
	hdr.Set("ETag", repr.etag)
	hdr.Set("Last-Modified", asset.modTime.UTC().Format(http.TimeFormat))
	hdr.Set("Cache-Control", "no-cache")
	// Vary: Accept-Encoding unconditionally, even when this asset has no
	// gzip sibling — a shared cache cannot know that in advance, and the
	// header costs nothing to send on every response (mirrors writeEntry).
	hdr.Set("Vary", "Accept-Encoding")

	if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatch(inm, repr.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if useGzip {
		hdr.Set("Content-Encoding", "gzip")
	}
	hdr.Set("Content-Length", strconv.Itoa(len(repr.body)))

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Write(repr.body)
}
