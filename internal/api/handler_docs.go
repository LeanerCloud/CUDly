package api

import (
	"context"
	_ "embed"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

//go:embed openapi.yaml
var openapiSpec []byte

// swaggerUIVersion is the exact swagger-ui-dist version pinned in the docs
// page. Kept in sync with frontend/src/docs.html (PR #521); both surfaces must
// reference the same version so the SRI hashes below stay valid for both.
const swaggerUIVersion = "5.32.6"

// Subresource Integrity (SRI) hashes for the pinned swagger-ui-dist@5.32.6
// assets, identical to the values verified in frontend/src/docs.html (PR #521).
// They let the browser reject any CDN asset whose content does not match,
// guarding against CDN poisoning / route hijack on the public /docs page
// (issues #447, #541). Do not regenerate these independently: the two doc
// surfaces must stay byte-for-byte consistent.
const (
	swaggerUICSSSRI      = "sha384-9Q2fpS+xeS4ffJy6CagnwoUl+4ldAYhOs9pgZuEKxypVModhmZFzeMlvVsAjf7uT"
	swaggerUIBundleSRI   = "sha384-EYdOaiRwn44zNjrw+Tfs06qYz9BGQVo2f4/pLY5i7VorbjnZNhdplAbTBk8FXHUJ"
	swaggerUIPresetSRI   = "sha384-49fpFaVrAWI/qdgl9Vv5E/4NXxRUiJX5vGuLws1NUpTWGtEqzWEx8gHTw2UTehFK"
	swaggerUICDNBaseHref = "https://unpkg.com/swagger-ui-dist@" + swaggerUIVersion
)

// docsPageCSP is the Content-Security-Policy emitted for the /docs and
// /api/docs HTML pages. The default restrictive CSP set by
// setSecurityHeaders (default-src 'none') blocks unpkg-hosted swagger-ui
// assets and the inline bootstrap script, leaving the page blank
// (issue #329). This relaxed policy whitelists exactly what Swagger UI
// needs to render and nothing more:
//
//   - script-src: 'self' for any future same-origin script + unpkg for
//     swagger-ui-bundle.js / swagger-ui-standalone-preset.js + 'unsafe-inline'
//     for the bootstrap call below.
//   - style-src: 'self' + unpkg for swagger-ui.css + 'unsafe-inline'
//     because the bundle injects styles at runtime.
//   - img-src: 'self' + data: for the bundle's data-URI icons.
//   - font-src: 'self' + unpkg for any web fonts swagger-ui references.
//   - connect-src: 'self' for the same-origin openapi.yaml fetch.
//   - frame-ancestors: 'none' (unchanged from the default — still
//     clickjacking-safe).
//
// The unpkg host stays in script-src deliberately. The CDN tags now carry
// per-element Subresource Integrity attributes (see swaggerUI*SRI above),
// which is the tamper anchor that closes #447 / #541. Element-level SRI does
// NOT grant CSP load permission: CSP decides whether a resource may load (by
// host or by a CSP-level hash-source), and SRI only verifies the bytes once
// loading is permitted. Dropping the unpkg host while keeping only element
// SRI would block the external bundle and blank the page. CSP-level
// 'sha384-...' sources could in principle replace the host, but matching an
// external script's SRI against a CSP hash requires 'strict-dynamic' and has
// inconsistent browser support, so we keep the host and rely on the SRI
// attributes for integrity. This matches frontend/src/docs.html, which uses
// SRI with an implicit host allowance and no CSP at all.
//
// Every non-docs response keeps the original restrictive CSP.
const docsPageCSP = "default-src 'none'; " +
	"script-src 'self' 'unsafe-inline' https://unpkg.com; " +
	"style-src 'self' 'unsafe-inline' https://unpkg.com; " +
	"img-src 'self' data:; " +
	"font-src 'self' https://unpkg.com; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'"

// serveDocsUI returns a self-contained HTML page with Swagger UI loaded from CDN.
// The response carries a relaxed Content-Security-Policy (docsPageCSP) so the
// CDN assets and bootstrap script actually run; without it the page is blank.
func (h *Handler) serveDocsUI(_ context.Context, _ *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>CUDly API Documentation</title>
  <link rel="stylesheet" href="` + swaggerUICDNBaseHref + `/swagger-ui.css" integrity="` + swaggerUICSSSRI + `" crossorigin="anonymous">
  <style>body{margin:0}</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="` + swaggerUICDNBaseHref + `/swagger-ui-bundle.js" integrity="` + swaggerUIBundleSRI + `" crossorigin="anonymous"></script>
  <script src="` + swaggerUICDNBaseHref + `/swagger-ui-standalone-preset.js" integrity="` + swaggerUIPresetSRI + `" crossorigin="anonymous"></script>
  <script>
    const specURL = window.location.pathname.replace(/\/+$/, "") + "/openapi.yaml";
    SwaggerUIBundle({url:specURL,dom_id:"#swagger-ui",deepLinking:true,presets:[SwaggerUIBundle.presets.apis,SwaggerUIBundle.SwaggerUIStandalonePreset],layout:"BaseLayout"});
  </script>
</body>
</html>`
	return &rawResponse{
		contentType: "text/html; charset=utf-8",
		body:        html,
		csp:         docsPageCSP,
	}, nil
}

// serveOpenAPISpec returns the raw OpenAPI YAML specification.
func (h *Handler) serveOpenAPISpec(_ context.Context, _ *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	return &rawResponse{
		contentType: "application/yaml; charset=utf-8",
		body:        string(openapiSpec),
	}, nil
}

// docsHandler dispatches /docs and /api/docs requests.
// Requests ending in /openapi.yaml serve the raw spec; everything else serves the UI.
func (h *Handler) docsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	path := req.RequestContext.HTTP.Path
	if strings.HasSuffix(path, "/openapi.yaml") {
		return h.serveOpenAPISpec(ctx, req, params)
	}
	return h.serveDocsUI(ctx, req, params)
}
