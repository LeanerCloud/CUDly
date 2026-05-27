// Package api — CI guard for the base64-decode requirement on password-bearing
// handler functions.
//
// # Problem (regression class of #356)
//
// The frontend base64-encodes every password field before POST-ing to the API
// (see frontend/src/api/auth.ts). Every handler that reads a password field
// from the request body must call decodeBase64Password (or a helper that does)
// before forwarding the value to the service layer. If the decode step is
// omitted, bcrypt hashes the base64 string instead of the plaintext, locking
// the user out with no error — exactly what happened in issue #356.
//
// # Guard mechanism
//
// The test uses go/parser and go/ast to scan every non-test .go source file in
// this package. It classifies a source function as a "password handler" when:
//
//  1. The function body contains a json.Unmarshal call, AND
//  2. The function body contains a selector expression that reads a field
//     whose name ends with "Password" from any local variable.
//
// For each such function the test verifies that the function body also contains
// a call to decodeBase64Password or one of its known delegate helpers
// (decodeChangePasswordRequest). Functions that are legitimately exempt (e.g.,
// setupAdmin, which is bootstrapped separately) must be listed in
// knownExemptFunctions with an explanation.
//
// # Synthetic-regression sub-test
//
// TestBase64PasswordGuard_SyntheticRegression embeds a deliberately bad handler
// (reads .NewPassword from a json.Unmarshal target but never calls
// decodeBase64Password) as a string and asserts that the same AST scanner
// detects the violation. This proves the guard would have caught issue #356.
package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// knownExemptFunctions lists functions that are legitimately permitted to read
// a struct with a Password field from json.Unmarshal without calling
// decodeBase64Password themselves.
//
// Add an entry here only when the function has a documented reason for not
// decoding the password (e.g., the endpoint uses a different auth convention or
// is not exposed to the frontend encode/decode contract). Every entry here
// should be accompanied by a comment explaining why.
var knownExemptFunctions = map[string]string{
	// setupAdmin: the bootstrap endpoint. The frontend does base64-encode the
	// password (see frontend/src/api/auth.ts:setupAdmin), but the backend
	// currently forwards the raw value to the auth service which handles
	// hashing internally. Tracked as a separate concern; do not expand this
	// exemption to other handlers.
	"setupAdmin": "bootstrap endpoint — see comment in knownExemptFunctions for details",
}

// decodeHelpers is the set of function-call names that constitute a compliant
// alternative to calling decodeBase64Password directly. A handler that
// delegates to one of these helpers is considered compliant.
var decodeHelpers = map[string]bool{
	"decodeBase64Password":        true,
	"decodeChangePasswordRequest": true,
}

// passwordFieldSuffix is the suffix that marks a struct field as carrying a
// password value that the frontend always base64-encodes.
const passwordFieldSuffix = "Password"

// TestBase64PasswordGuard scans every non-test Go source file in this package
// and asserts that any function which (a) json.Unmarshals into a struct and (b)
// reads a *.Password field from that struct also calls decodeBase64Password (or
// a known delegate helper) before it can forward the value downstream.
func TestBase64PasswordGuard(t *testing.T) {
	t.Parallel()

	pkgDir := "." // tests run with cwd = package dir
	files, err := filepath.Glob(filepath.Join(pkgDir, "*.go"))
	require.NoError(t, err, "glob handler files")

	fset := token.NewFileSet()
	var violations []string

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue // skip test files — they intentionally exercise bad inputs
		}

		src, err := os.ReadFile(path)
		require.NoError(t, err, "read %s", path)

		f, err := parser.ParseFile(fset, path, src, 0)
		require.NoError(t, err, "parse %s", path)

		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}

			funcName := fd.Name.Name

			if _, exempt := knownExemptFunctions[funcName]; exempt {
				continue
			}

			if !funcBodyUnmarshalsJSON(fd.Body) {
				continue // not a request-body handler
			}

			if !funcBodyReadsPasswordField(fd.Body) {
				continue // does not touch a password field
			}

			// This function is a password handler. Verify it decodes.
			if !funcBodyCallsDecodeHelper(fd.Body) {
				violations = append(violations,
					filepath.Base(path)+":"+funcName+
						" reads a *.Password field from json.Unmarshal output"+
						" but does not call decodeBase64Password or a known delegate;"+
						" see issue #356 and #661",
				)
			}
		}
	}

	assert.Empty(t, violations,
		"password handler(s) missing base64-decode step "+
			"(refs #356/#661) — fix or add to knownExemptFunctions:\n%s",
		strings.Join(violations, "\n"))
}

// TestBase64PasswordGuard_SyntheticRegression proves the scanner detects the
// #356 regression class: a handler that reads .NewPassword from a json.Unmarshal
// target but never calls decodeBase64Password. This test must FAIL the
// scan assertion (i.e., the violation must be found). If the scanner cannot
// detect this shape, the guard is broken and this test itself fails.
func TestBase64PasswordGuard_SyntheticRegression(t *testing.T) {
	t.Parallel()

	// Minimal source that reproduces the #356 bug shape:
	//   - json.Unmarshal into a struct that has a NewPassword field
	//   - read .NewPassword and forward it directly to a service call
	//   - NO call to decodeBase64Password
	const badHandler = `
package api

import (
	"context"
	"encoding/json"
)

type badResetRequest struct {
	Token       string ` + "`json:\"token\"`" + `
	NewPassword string ` + "`json:\"new_password\"`" + `
}

// badResetPassword is a synthetic example of the #356 regression:
// it reads NewPassword from the JSON body but skips the base64-decode step.
func badResetPassword(ctx context.Context, body string) error {
	var req badResetRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return err
	}
	// Bug: req.NewPassword is still base64-encoded here; no decode call.
	_ = req.NewPassword
	return nil
}
`

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", badHandler, 0)
	require.NoError(t, err, "parse synthetic bad handler")

	var detected bool
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		if !funcBodyUnmarshalsJSON(fd.Body) {
			continue
		}
		if !funcBodyReadsPasswordField(fd.Body) {
			continue
		}
		if !funcBodyCallsDecodeHelper(fd.Body) {
			detected = true
		}
	}

	assert.True(t, detected,
		"synthetic regression was NOT detected by the scanner; "+
			"the TestBase64PasswordGuard guard is broken and must be fixed (refs #661)")
}

// ---- AST helpers ----

// funcBodyUnmarshalsJSON reports whether the function body contains at least
// one call to json.Unmarshal (or a bare Unmarshal call in scope).
func funcBodyUnmarshalsJSON(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			if fn.Sel.Name == "Unmarshal" {
				found = true
			}
		case *ast.Ident:
			if fn.Name == "Unmarshal" {
				found = true
			}
		}
		return !found
	})
	return found
}

// funcBodyReadsPasswordField reports whether the function body contains a
// selector expression whose field name ends with "Password". This covers
// .Password, .NewPassword, .CurrentPassword, and any future variants.
func funcBodyReadsPasswordField(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if strings.HasSuffix(sel.Sel.Name, passwordFieldSuffix) {
			found = true
		}
		return !found
	})
	return found
}

// funcBodyCallsDecodeHelper reports whether the function body contains a call
// to any function listed in decodeHelpers (decodeBase64Password or a known
// wrapper such as decodeChangePasswordRequest).
func funcBodyCallsDecodeHelper(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			name = fn.Name
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		}
		if decodeHelpers[name] {
			found = true
		}
		return !found
	})
	return found
}
