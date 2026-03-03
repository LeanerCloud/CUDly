package api

import (
	"context"
	_ "embed"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

//go:embed openapi.yaml
var openapiSpec []byte

// serveDocsUI returns a self-contained HTML page with Swagger UI loaded from CDN.
func (h *Handler) serveDocsUI(_ context.Context, _ *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>CUDly API Documentation</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>body{margin:0}</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({url:"/api/docs/openapi.yaml",dom_id:"#swagger-ui",deepLinking:true,presets:[SwaggerUIBundle.presets.apis,SwaggerUIBundle.SwaggerUIStandalonePreset],layout:"BaseLayout"});
  </script>
</body>
</html>`
	return &rawResponse{
		contentType: "text/html; charset=utf-8",
		body:        html,
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
