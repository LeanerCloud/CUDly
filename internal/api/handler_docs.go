package api

import (
	"context"
	_ "embed"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

//go:embed openapi.yaml
var openapiSpec []byte

// docsPageCSP is the Content-Security-Policy emitted for the /docs and
// /api/docs HTML pages. The default restrictive CSP set by
// setSecurityHeaders (default-src 'none') blocks unpkg-hosted swagger-ui
// assets and the inline bootstrap script, leaving the page blank
// (issue #329). This relaxed policy whitelists exactly what Swagger UI
// needs to render and nothing more:
//
//   - script-src: 'self' for any future same-origin script + unpkg for
//     swagger-ui-bundle.js + 'unsafe-inline' for the bootstrap call below.
//   - style-src: 'self' + unpkg for swagger-ui.css + 'unsafe-inline'
//     because the bundle injects styles at runtime.
//   - img-src: 'self' + data: for the bundle's data-URI icons.
//   - font-src: 'self' + unpkg for any web fonts swagger-ui references.
//   - connect-src: 'self' for the same-origin openapi.yaml fetch.
//   - frame-ancestors: 'none' (unchanged from the default — still
//     clickjacking-safe).
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
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.32.5/swagger-ui.css">
  <style>body{margin:0}</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.32.5/swagger-ui-bundle.js"></script>
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
	var (
		response any
		err      error
	)
	if strings.HasSuffix(path, "/openapi.yaml") {
		response, err = h.serveOpenAPISpec(ctx, req, params)
	} else {
		response, err = h.serveDocsUI(ctx, req, params)
	}
	if err != nil || req.RequestContext.HTTP.Method != "HEAD" {
		return response, err
	}
	if raw, ok := response.(*rawResponse); ok {
		// rawResponse currently contains only strings, so a shallow copy is safe.
		head := *raw
		head.body = ""
		return &head, nil
	}
	return response, nil
}
