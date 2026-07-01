package api

// TestOpenAPI403OnPermissionGatedRoutes is a regression guard that enforces
// the invariant documented in issue #476: every route whose handler calls
// h.requirePermission(...) must declare a '403' response in openapi.yaml.
//
// Design rationale:
//   - The test reads the live openapi.yaml from the package directory (same
//     directory as this file) via the standard "testdata adjacent" convention.
//   - It reads router.go to obtain the handler wrapper function name for
//     each route, then reads the wrapper body to extract which h.* function
//     it delegates to.
//   - It then reads handler_*.go source files and checks whether the
//     h.* function body calls requirePermission.
//   - For every such route it asserts that the spec operation declares '403'.
//
// The test is intentionally source-reading so it catches new routes the
// moment a developer wires them in router.go with a handler that calls
// requirePermission, without requiring a separate test-doubles update.
//
// False-positive risk: functions that call requirePermission only through a
// helper (not directly) will not be detected by the simple grep. Given the
// current codebase convention (every permission check goes through
// requirePermission at the outer handler boundary), this is acceptable.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// routeKey canonicalises a path+method pair for map lookups.
func routeKey(path, method string) string {
	return strings.ToUpper(method) + " " + path
}

// parseSpecOperations reads openapi.yaml and returns a map of
// routeKey -> bool indicating whether '403' is declared in responses.
//
// The spec is parsed as a generic tree (map[string]interface{}) to avoid
// the strict struct-unmarshal errors that arise from path-item-level
// `parameters` keys (which are YAML sequences and would fail to unmarshal
// into a typed struct that has only `Security` and `Responses` fields).
func parseSpecOperations(specPath string) (map[string]bool, error) {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}

	var doc map[string]interface{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	pathsRaw, ok := doc["paths"]
	if !ok {
		return nil, fmt.Errorf("openapi.yaml has no 'paths' key")
	}
	paths, ok := pathsRaw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("'paths' is not a map")
	}

	httpMethods := map[string]bool{
		"get": true, "post": true, "put": true, "patch": true,
		"delete": true, "head": true, "options": true,
	}

	result := make(map[string]bool)
	for path, pathItemRaw := range paths {
		pathItem, ok := pathItemRaw.(map[string]interface{})
		if !ok {
			continue
		}
		for method, opRaw := range pathItem {
			if !httpMethods[method] {
				continue
			}
			op, ok := opRaw.(map[string]interface{})
			if !ok {
				continue
			}
			has403 := false
			if responsesRaw, ok := op["responses"]; ok {
				if responses, ok := responsesRaw.(map[string]interface{}); ok {
					_, has403 = responses["403"]
				}
			}
			result[routeKey(path, method)] = has403
		}
	}
	return result, nil
}

// routeHandlerEntry is the relevant columns from a Route literal in router.go.
type routeHandlerEntry struct {
	exactPath      string
	pathPrefix     string
	pathSuffix     string
	method         string
	wrapperName    string // e.g. "dashboardSummaryHandler"
	delegateFnName string // e.g. "getDashboardSummary" (the h.* call inside the wrapper)
}

// parseRouterEntries reads router.go and returns a slice of route entries,
// extracting ExactPath, PathPrefix, PathSuffix, Method, and Handler fields
// from the Route literal syntax, then for each handler wrapper function
// extracts the name of the h.* method it delegates to.
func parseRouterEntries(routerPath string) ([]routeHandlerEntry, error) {
	data, err := os.ReadFile(routerPath)
	if err != nil {
		return nil, fmt.Errorf("read router: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	// Patterns to extract fields from Route literals.
	reExact := regexp.MustCompile(`ExactPath:\s*"([^"]+)"`)
	rePrefix := regexp.MustCompile(`PathPrefix:\s*"([^"]+)"`)
	reSuffix := regexp.MustCompile(`PathSuffix:\s*"([^"]+)"`)
	reMethod := regexp.MustCompile(`Method:\s*"([^"]+)"`)
	reHandler := regexp.MustCompile(`Handler:\s*r\.(\w+)`)

	// Pattern to detect a Router wrapper function declaration, e.g.:
	//   func (r *Router) dashboardSummaryHandler(ctx ...
	reWrapperDecl := regexp.MustCompile(`^func \(r \*Router\) (\w+)\(`)
	// Pattern to extract the h.* delegated function name from the wrapper body, e.g.:
	//   return r.h.getDashboardSummary(ctx, ...
	reDelegateCall := regexp.MustCompile(`r\.h\.(\w+)\(`)

	// Phase 1: build route entries with wrapperName set.
	var entries []routeHandlerEntry
	for _, line := range lines {
		if !strings.Contains(line, "Handler:") {
			continue
		}
		var entry routeHandlerEntry
		if m := reExact.FindStringSubmatch(line); m != nil {
			entry.exactPath = m[1]
		}
		if m := rePrefix.FindStringSubmatch(line); m != nil {
			entry.pathPrefix = m[1]
		}
		if m := reSuffix.FindStringSubmatch(line); m != nil {
			entry.pathSuffix = m[1]
		}
		if m := reMethod.FindStringSubmatch(line); m != nil {
			entry.method = m[1]
		}
		if m := reHandler.FindStringSubmatch(line); m != nil {
			entry.wrapperName = m[1]
		}
		if entry.wrapperName != "" {
			entries = append(entries, entry)
		}
	}

	// Phase 2: for each wrapper function body, find the h.* delegate call.
	// Build a map: wrapperName -> delegateFnName.
	wrapperToDelegate := make(map[string]string)
	inWrapper := ""
	for _, line := range lines {
		if m := reWrapperDecl.FindStringSubmatch(line); m != nil {
			inWrapper = m[1]
			continue
		}
		if inWrapper != "" {
			if m := reDelegateCall.FindStringSubmatch(line); m != nil {
				// First h.* call in the wrapper body is the delegate.
				if _, seen := wrapperToDelegate[inWrapper]; !seen {
					wrapperToDelegate[inWrapper] = m[1]
				}
			}
			// A blank line or another func declaration ends the wrapper scan.
			if strings.HasPrefix(strings.TrimSpace(line), "func ") {
				inWrapper = ""
			}
		}
	}

	// Phase 3: annotate entries with delegateFnName.
	for i := range entries {
		if fn, ok := wrapperToDelegate[entries[i].wrapperName]; ok {
			entries[i].delegateFnName = fn
		}
	}

	return entries, nil
}

// handlerFuncCallsRequirePermission returns true if any handler_*.go file in
// srcDir contains a function whose name is fnName and whose body calls
// requirePermission.
func handlerFuncCallsRequirePermission(srcDir, fnName string) (bool, error) {
	pattern := filepath.Join(srcDir, "handler_*.go")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return false, err
	}

	// Match the exact function name as declared on *Handler.
	reFunc := regexp.MustCompile(`^func \(h \*Handler\) (\w+)\(`)

	for _, fpath := range files {
		f, err := os.Open(fpath)
		if err != nil {
			return false, err
		}

		scanner := bufio.NewScanner(f)
		inTarget := false
		braceDepth := 0

		for scanner.Scan() {
			line := scanner.Text()

			if m := reFunc.FindStringSubmatch(line); m != nil {
				inTarget = m[1] == fnName
				braceDepth = 0
			}

			if inTarget {
				braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
				if strings.Contains(line, "requirePermission") {
					f.Close()
					return true, nil
				}
				if braceDepth < 0 {
					inTarget = false
				}
			}
		}
		if err := scanner.Err(); err != nil {
			f.Close()
			return false, err
		}
		f.Close()
	}
	return false, nil
}

// specPathsForRoute returns the set of spec routeKeys that the given router
// entry could correspond to.
func specPathsForRoute(entry routeHandlerEntry, specOps map[string]bool) []string {
	var matches []string

	for key := range specOps {
		parts := strings.SplitN(key, " ", 2)
		if len(parts) != 2 {
			continue
		}
		method, path := parts[0], parts[1]

		if entry.method != "" && method != strings.ToUpper(entry.method) {
			continue
		}

		if entry.exactPath != "" {
			if path == entry.exactPath {
				matches = append(matches, key)
			}
			continue
		}

		matchesPrefix := entry.pathPrefix == "" || strings.HasPrefix(path, entry.pathPrefix)
		matchesSuffix := entry.pathSuffix == "" || strings.HasSuffix(path, entry.pathSuffix)
		if matchesPrefix && matchesSuffix {
			matches = append(matches, key)
		}
	}
	return matches
}

func TestOpenAPI403OnPermissionGatedRoutes(t *testing.T) {
	// Locate the package directory using the file path of this test.
	_, testFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	pkgDir := filepath.Dir(testFile)

	specPath := filepath.Join(pkgDir, "openapi.yaml")
	routerPath := filepath.Join(pkgDir, "router.go")

	// Parse the spec.
	specOps, err := parseSpecOperations(specPath)
	require.NoError(t, err, "failed to parse openapi.yaml")
	require.NotEmpty(t, specOps, "parsed zero operations from openapi.yaml")

	// Parse the router.
	routerEntries, err := parseRouterEntries(routerPath)
	require.NoError(t, err, "failed to parse router.go")
	require.NotEmpty(t, routerEntries, "parsed zero route entries from router.go")

	var failures []string

	for _, entry := range routerEntries {
		if entry.delegateFnName == "" {
			// Could not resolve the delegate; skip rather than false-negative.
			continue
		}

		// Check if the delegate handler calls requirePermission.
		callsPerm, err := handlerFuncCallsRequirePermission(pkgDir, entry.delegateFnName)
		if err != nil {
			t.Logf("warning: could not check handler %s: %v", entry.delegateFnName, err)
			continue
		}
		if !callsPerm {
			continue
		}

		// Find the spec operations that match this route.
		matched := specPathsForRoute(entry, specOps)
		if len(matched) == 0 {
			// Route not in spec yet — out of scope for this test.
			continue
		}

		for _, key := range matched {
			has403, exists := specOps[key]
			if !exists {
				continue
			}
			if !has403 {
				failures = append(failures, fmt.Sprintf(
					"wrapper=%s delegate=%s (exact=%q prefix=%q suffix=%q method=%q) -> spec op %q calls requirePermission but '403' is missing from responses",
					entry.wrapperName, entry.delegateFnName,
					entry.exactPath, entry.pathPrefix, entry.pathSuffix, entry.method,
					key,
				))
			}
		}
	}

	assert.Empty(t, failures,
		"The following spec operations are missing '403' even though their handlers call requirePermission:\n%s",
		strings.Join(failures, "\n"),
	)
}
