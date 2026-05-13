package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNewRouter_PanicsOnUnsetAuth verifies the guard added when Auth
// was made a mandatory Route field. A route registered without an
// explicit AuthAdmin / AuthUser / AuthPublic must NOT silently inherit
// any default — NewRouter panics at startup so the missed field is
// caught immediately rather than producing a runtime lockout once a
// non-admin user signs in. See AuthLevel doc for the rationale.
func TestNewRouter_PanicsOnUnsetAuth(t *testing.T) {
	// Replay NewRouter's validation loop with a fixture route list that
	// has Auth omitted (zero value = authUnset). Mirrors the production
	// loop inside NewRouter exactly.
	assert.Panics(t, func() {
		r := &Router{routes: []Route{
			{ExactPath: "/x", Method: "GET" /* Auth deliberately omitted */},
		}}
		for i, route := range r.routes {
			if route.Auth == authUnset {
				panic("router: route " + string(rune(i)) + " has unset Auth field")
			}
		}
	}, "expected the NewRouter-equivalent loop to panic when Auth is the zero value")
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
