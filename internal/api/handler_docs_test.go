package apihttp

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// docsBodyForTest renders the served /docs HTML and returns its body.
func docsBodyForTest(t *testing.T) string {
	t.Helper()
	resp, err := (&Handler{}).serveDocsUI(context.Background(), nil, nil)
	require.NoError(t, err)
	raw, ok := resp.(*rawResponse)
	require.True(t, ok, "serveDocsUI should return *rawResponse")
	return raw.body
}

// swaggerUITagRe matches each swagger-ui-dist <link>/<script> tag in the docs
// HTML so the assertions below can inspect the version pin and SRI attributes
// per tag (issues #447, #541, #543). The float-tag guard relies on matching the
// version segment, so the regex captures everything up to the closing '>'.
var swaggerUITagRe = regexp.MustCompile(`<(?:link|script)[^>]*unpkg\.com/swagger-ui-dist@[^>]*>`)

// TestServeDocsUISwaggerUITagsKeepVersionPinAndSRI guards the served Go /docs
// page (internal/api/handler_docs.go) against silently dropping the version pin
// or the SRI integrity attributes that close #447/#541. A regression here means
// the public docs page is back to loading unpinned, unverified CDN scripts.
func TestServeDocsUISwaggerUITagsKeepVersionPinAndSRI(t *testing.T) {
	body := docsBodyForTest(t)

	tags := swaggerUITagRe.FindAllString(body, -1)
	require.GreaterOrEqual(t, len(tags), 3,
		"expected at least 3 swagger-ui-dist tags (css, bundle, standalone-preset), got %d in:\n%s", len(tags), body)

	exactVersion := regexp.MustCompile(`swagger-ui-dist@\d+\.\d+\.\d+/`)
	floatVersion := regexp.MustCompile(`swagger-ui-dist@\d+/`)
	for _, tag := range tags {
		require.Regexp(t, exactVersion, tag,
			"swagger-ui-dist tag must pin an exact x.y.z version, not a floating major tag: %s", tag)
		require.NotRegexp(t, floatVersion, tag,
			"swagger-ui-dist tag must not use a floating major tag like @5: %s", tag)
		require.Contains(t, tag, `integrity="sha384-`,
			"swagger-ui-dist tag must carry an SRI integrity attribute: %s", tag)
		require.Contains(t, tag, `crossorigin="anonymous"`,
			"swagger-ui-dist tag must carry crossorigin=\"anonymous\" for SRI to apply: %s", tag)
	}

	// The standalone-preset script is referenced by the bootstrap call and must
	// be loaded (it was previously missing from the Go handler).
	require.Contains(t, body, "swagger-ui-standalone-preset.js",
		"docs page must load swagger-ui-standalone-preset.js")
}

// TestServeDocsUIUsesSharedSRIConstants asserts the served page uses the exact
// SRI hashes shared with frontend/src/docs.html (PR #521) so the two surfaces
// cannot drift apart.
func TestServeDocsUIUsesSharedSRIConstants(t *testing.T) {
	body := docsBodyForTest(t)
	require.Contains(t, body, swaggerUICSSSRI)
	require.Contains(t, body, swaggerUIBundleSRI)
	require.Contains(t, body, swaggerUIPresetSRI)
	require.Contains(t, body, "swagger-ui-dist@"+swaggerUIVersion+"/")
}

// TestDocsPageCSPAllowsUnpkgHostForScripts documents and locks in the CSP
// decision from #447/#541: the unpkg host stays in script-src because
// element-level SRI does not grant CSP load permission. If this changes,
// re-verify the page still renders before removing the host.
func TestDocsPageCSPAllowsUnpkgHostForScripts(t *testing.T) {
	scriptSrc := ""
	for _, directive := range strings.Split(docsPageCSP, ";") {
		if strings.HasPrefix(strings.TrimSpace(directive), "script-src") {
			scriptSrc = strings.TrimSpace(directive)
		}
	}
	require.NotEmpty(t, scriptSrc, "docsPageCSP must define a script-src directive")
	require.Contains(t, scriptSrc, "https://unpkg.com",
		"script-src must keep the unpkg host: element-level SRI does not grant CSP load permission")
}
