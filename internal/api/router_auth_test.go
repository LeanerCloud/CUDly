package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidateRoutes_PanicsOnUnsetAuth exercises the live validator
// NewRouter calls. Asserts the production validateRoutes — not a
// replay — so if a refactor removes the call from NewRouter the test
// stops passing (caught by CR review of #364 — the previous version
// replayed the panic loop inline and would have kept passing even if
// NewRouter stopped enforcing).
func TestValidateRoutes_PanicsOnUnsetAuth(t *testing.T) {
	assert.Panics(t, func() {
		validateRoutes([]Route{
			{ExactPath: "/x", Method: "GET" /* Auth deliberately omitted */},
		})
	}, "expected validateRoutes to panic when Auth is the zero value")
}

// TestValidateRoutes_AcceptsAllExplicitLevels covers the happy path
// for each of the three valid Auth values. A route table where every
// entry declares a level must NOT panic.
func TestValidateRoutes_AcceptsAllExplicitLevels(t *testing.T) {
	assert.NotPanics(t, func() {
		validateRoutes([]Route{
			{ExactPath: "/a", Method: "GET", Auth: AuthAdmin},
			{ExactPath: "/u", Method: "GET", Auth: AuthUser},
			{ExactPath: "/p", Method: "GET", Auth: AuthPublic},
		})
	}, "validateRoutes must accept any explicit Auth level")
}

// TestRoute_AllRegisteredHaveExplicitAuth is a structural assertion on the
// live route table built by registerRoutes(). Every entry must declare an
// explicit Auth level — if a future PR adds a route and forgets the field,
// this test fails *and* NewRouter would panic at startup. Two independent
// safety nets so a missed field can't reach a deployed Lambda.
func TestRoute_AllRegisteredHaveExplicitAuth(t *testing.T) {
	r := &Router{h: &Handler{}}
	r.registerRoutes()
	for i, route := range r.routes {
		assert.NotEqual(t, authUnset, route.Auth,
			"route %d (path=%q%q%q method=%q) is missing an explicit Auth field",
			i, route.PathPrefix, route.ExactPath, route.PathSuffix, route.Method)
	}
}
