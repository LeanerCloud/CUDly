package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/testutil"
)

// makeStaticDir creates a temporary directory with the given files and
// returns its path. The caller is responsible for cleanup via t.Cleanup.
func makeStaticDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return dir
}

// ----- cacheControlForExt -----

func TestCacheControlForExt(t *testing.T) {
	tests := []struct {
		ext      string
		contains string
	}{
		{".html", "no-cache"},
		{".js", "immutable"},
		{".css", "immutable"},
		{".png", "immutable"},
		{".woff", "immutable"},
		{".woff2", "immutable"},
		{".ttf", "immutable"},
		{".svg", "immutable"},
		{".ico", "immutable"},
		{".webp", "immutable"},
		{".json", "max-age=3600"},
		{"", "max-age=3600"},
	}
	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			got := cacheControlForExt(tt.ext)
			testutil.AssertContains(t, got, tt.contains)
		})
	}
}

// ----- setCacheHeaders -----

func TestSetCacheHeaders(t *testing.T) {
	tests := []struct {
		path     string
		contains string
	}{
		{"/index.html", "no-cache"},
		{"/bundle.js", "immutable"},
		{"/style.css", "immutable"},
		{"/logo.png", "immutable"},
		{"/data.json", "max-age=3600"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			setCacheHeaders(w, tt.path)
			got := w.Header().Get("Cache-Control")
			testutil.AssertContains(t, got, tt.contains)
		})
	}
}

// ----- isStaticPath -----

func TestIsStaticPath(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/api/accounts", false},
		{"/api/", false},
		{"/api", false},
		{"/health", false},
		{"/", true},
		{"/index.html", true},
		{"/app/dashboard", true},
		{"//health", false}, // double-slash normalised to /health
		{"//api/test", false},
		{"/version", false},  // public build-metadata endpoint must reach API, not SPA
		{"//version", false}, // double-slash normalised to /version
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			testutil.AssertEqual(t, tt.expected, isStaticPath(tt.path))
		})
	}
}

// ----- staticDirFromEnv -----

func TestStaticDirFromEnv_Unset(t *testing.T) {
	testutil.SetEnv(t, "STATIC_DIR", "")
	testutil.AssertEqual(t, "", staticDirFromEnv())
}

func TestStaticDirFromEnv_ValidDir(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html/>"})
	testutil.SetEnv(t, "STATIC_DIR", dir)
	testutil.AssertEqual(t, dir, staticDirFromEnv())
}

func TestStaticDirFromEnv_MissingIndexHTML(t *testing.T) {
	dir := t.TempDir() // no index.html
	testutil.SetEnv(t, "STATIC_DIR", dir)
	testutil.AssertEqual(t, "", staticDirFromEnv())
}

func TestStaticDirFromEnv_NonExistentDir(t *testing.T) {
	testutil.SetEnv(t, "STATIC_DIR", "/nonexistent/path/no/index")
	testutil.AssertEqual(t, "", staticDirFromEnv())
}

// ----- resolveStaticFilePath -----

func TestResolveStaticFilePath_ExistingFile(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{
		"index.html":    "<html/>",
		"bundle.js":     "var x=1;",
		"css/style.css": "body{}",
	})

	filePath, cleanPath, ok := resolveStaticFilePath(dir, "/bundle.js")
	testutil.AssertEqual(t, true, ok)
	testutil.AssertEqual(t, "/bundle.js", cleanPath)
	testutil.AssertTrue(t, filepath.IsAbs(filePath), "filePath should be absolute")
}

func TestResolveStaticFilePath_RootFallsBackToIndex(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html/>"})

	_, cleanPath, ok := resolveStaticFilePath(dir, "/")
	testutil.AssertEqual(t, true, ok)
	testutil.AssertEqual(t, "/index.html", cleanPath)
}

func TestResolveStaticFilePath_ExtensionlessFallsBackToIndex(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html/>"})

	_, cleanPath, ok := resolveStaticFilePath(dir, "/some/spa/route")
	testutil.AssertEqual(t, true, ok)
	testutil.AssertEqual(t, "/index.html", cleanPath)
}

func TestResolveStaticFilePath_MissingFileWithExtension(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html/>"})

	_, _, ok := resolveStaticFilePath(dir, "/missing.png")
	testutil.AssertEqual(t, false, ok)
}

func TestResolveStaticFilePath_DirectoryTraversal(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html/>"})

	_, _, ok := resolveStaticFilePath(dir, "/../../../etc/passwd")
	// traversal should be blocked (ok=false) or resolve safely inside dir
	if ok {
		// if ok, the resolved path must still be inside dir
		filePath, _, _ := resolveStaticFilePath(dir, "/../../../etc/passwd")
		absDir, _ := filepath.Abs(dir)
		absFile, _ := filepath.Abs(filePath)
		testutil.AssertTrue(t, len(absFile) >= len(absDir), "traversal attempt must stay inside dir")
	}
}

// TestResolveStaticFilePath_SiblingDirBlocked is a regression test for 04-M6:
// the previous HasPrefix check lacked a separator, so a sibling directory
// named "<dir>-evil" would have passed the containment check because
// "/srv/static" is a string-prefix of "/srv/static-evil". The fix appends
// os.PathSeparator to the prefix so only paths genuinely inside the dir pass.
func TestResolveStaticFilePath_SiblingDirBlocked(t *testing.T) {
	// Create a parent and the real static dir inside it.
	parent := t.TempDir()
	realDir := filepath.Join(parent, "static")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir static: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "index.html"), []byte("<html/>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	// Create a sibling whose name starts with "static" (the prefix-confusion case).
	siblingDir := filepath.Join(parent, "static-evil")
	if err := os.MkdirAll(siblingDir, 0o755); err != nil {
		t.Fatalf("mkdir static-evil: %v", err)
	}
	secretFile := filepath.Join(siblingDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("should-not-serve"), 0o644); err != nil {
		t.Fatalf("write secret.txt: %v", err)
	}

	// Attempting to resolve a path that lands in the sibling must return ok=false.
	_, _, ok := resolveStaticFilePath(realDir, "/../static-evil/secret.txt")
	testutil.AssertEqual(t, false, ok)
}

func TestResolveStaticFilePath_IndexMissingForSPARoute(t *testing.T) {
	dir := t.TempDir() // no index.html

	_, _, ok := resolveStaticFilePath(dir, "/some/route")
	testutil.AssertEqual(t, false, ok)
}

// ----- serveStaticForLambda -----

func TestServeStaticForLambda_ExistingHTMLFile(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html>hello</html>"})

	content, ct, cc, found := serveStaticForLambda(dir, "/index.html")
	testutil.AssertEqual(t, true, found)
	testutil.AssertEqual(t, "<html>hello</html>", string(content))
	testutil.AssertContains(t, ct, "html")
	testutil.AssertContains(t, cc, "no-cache")
}

func TestServeStaticForLambda_ExistingJSFile(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{
		"index.html": "<html/>",
		"app.js":     "var x=1;",
	})

	content, ct, cc, found := serveStaticForLambda(dir, "/app.js")
	testutil.AssertEqual(t, true, found)
	testutil.AssertEqual(t, "var x=1;", string(content))
	_ = ct
	testutil.AssertContains(t, cc, "immutable")
}

func TestServeStaticForLambda_NotFound(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html/>"})

	_, _, _, found := serveStaticForLambda(dir, "/missing.png")
	testutil.AssertEqual(t, false, found)
}

func TestServeStaticForLambda_SPAFallback(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html>spa</html>"})

	content, _, _, found := serveStaticForLambda(dir, "/dashboard")
	testutil.AssertEqual(t, true, found)
	testutil.AssertEqual(t, "<html>spa</html>", string(content))
}

func TestServeStaticForLambda_UnknownExtension(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{
		"index.html": "<html/>",
		"data.bin":   "\x00\x01\x02",
	})

	content, ct, _, found := serveStaticForLambda(dir, "/data.bin")
	testutil.AssertEqual(t, true, found)
	testutil.AssertEqual(t, "\x00\x01\x02", string(content))
	testutil.AssertEqual(t, "application/octet-stream", ct)
}

// ----- spaFileServer / ServeHTTP -----

func TestSpaFileServer_ServesExistingFile(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{
		"index.html": "<html>index</html>",
		"app.js":     "var x=1;",
	})

	handler := spaFileServer(dir)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/app.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	testutil.AssertEqual(t, http.StatusOK, w.Code)
}

func TestSpaFileServer_ServesIndexForRoot(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html>root</html>"})

	handler := spaFileServer(dir)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	testutil.AssertEqual(t, http.StatusOK, w.Code)
}

func TestSpaFileServer_SPAFallbackForUnknownPath(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html>spa</html>"})

	handler := spaFileServer(dir)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/some/route", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	testutil.AssertEqual(t, http.StatusOK, w.Code)
}

func TestSpaFileServer_404ForMissingExtensionFile(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html/>"})

	handler := spaFileServer(dir)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/missing.png", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	testutil.AssertEqual(t, http.StatusNotFound, w.Code)
}

func TestSpaFileServer_404WhenIndexMissing(t *testing.T) {
	dir := t.TempDir() // no index.html

	handler := spaFileServer(dir)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/any/route", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	testutil.AssertEqual(t, http.StatusNotFound, w.Code)
}

// ----- SPA catch-all: every frontend route (issue #1775) -----

// appRoutes lists every path the SPA can be deep-linked to, taken from the
// tab table and the legacy-redirect table in frontend/src/navigation.ts. A new
// nav entry (or a new root-level mux registration that shadows one) must not
// silently start serving something other than the SPA shell.
var appRoutes = []string{
	"/",
	"/home",
	"/opportunities",
	"/plans",
	"/purchases",
	"/inventory",
	"/inventory/active-commitments",
	"/inventory/coverage",
	"/inventory/ri-exchange",
	"/admin",
	"/admin/general",
	"/admin/purchasing",
	"/admin/accounts",
	"/admin/users",
	// Legacy paths kept alive by LEGACY_PATH_REDIRECTS for old bookmarks.
	"/dashboard",
	"/recommendations",
	"/history",
	"/settings",
	"/ri-exchange",
	// Non-tab SPA landing paths.
	"/reset-password",
	"/archera-insurance",
	"/purchases/approve/abc-123",
	"/purchases/cancel/abc-123",
}

func TestSPAFallbackServesIndexForEveryAppRoute(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{
		"index.html":      "<html>spa</html>",
		"docs/index.html": "<html>docs</html>",
	})

	for _, route := range appRoutes {
		t.Run(route, func(t *testing.T) {
			if !isStaticPath(route) {
				t.Fatalf("route %s is not classified as a static path; the API router would swallow it", route)
			}

			content, _, _, found := serveStaticForLambda(dir, route)
			testutil.AssertEqual(t, true, found)
			testutil.AssertEqual(t, "<html>spa</html>", string(content))

			handler := spaFileServer(dir)
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, route, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			testutil.AssertEqual(t, http.StatusOK, w.Code)
			testutil.AssertEqual(t, "<html>spa</html>", w.Body.String())
		})
	}
}

// The API docs page is a real directory in the build output. Before #1775 a
// directory stat fell straight through to the SPA fallback, so the "API Docs"
// header link (href="/docs/") rendered the dashboard instead.
func TestResolveStaticFilePath_DirectoryIndex(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{
		"index.html":      "<html>spa</html>",
		"docs/index.html": "<html>docs</html>",
	})

	for _, route := range []string{"/docs", "/docs/"} {
		t.Run(route, func(t *testing.T) {
			content, _, _, found := serveStaticForLambda(dir, route)
			testutil.AssertEqual(t, true, found)
			testutil.AssertEqual(t, "<html>docs</html>", string(content))
		})
	}
}

// A directory without its own index.html still falls back to the SPA shell, so
// asset directories (dist/js, dist/css) do not 404 into a broken page.
func TestResolveStaticFilePath_DirectoryWithoutIndexFallsBackToSPA(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{
		"index.html": "<html>spa</html>",
		"js/app.js":  "var x=1;",
	})

	content, _, _, found := serveStaticForLambda(dir, "/js")
	testutil.AssertEqual(t, true, found)
	testutil.AssertEqual(t, "<html>spa</html>", string(content))
}

// A directory index reached via /docs/ must not serve a file the direct
// request path (/docs/index.html) would reject. os.Stat follows symlinks, so
// the containment check has to run on the candidate, not just on the directory.
func TestResolveStaticFilePath_DirectoryIndexRejectsSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	staticDir := filepath.Join(parent, "static", "docs")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	root := filepath.Join(parent, "static")
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>spa</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	outside := filepath.Join(parent, "secret.html")
	if err := os.WriteFile(outside, []byte("should-not-serve"), 0o644); err != nil {
		t.Fatalf("write secret.html: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(staticDir, "index.html")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Both spellings must agree, and neither may serve the out-of-tree file.
	direct, _, _, directFound := serveStaticForLambda(root, "/docs/index.html")
	testutil.AssertEqual(t, false, directFound)
	testutil.AssertEqual(t, "", string(direct))

	viaDir, _, _, viaDirFound := serveStaticForLambda(root, "/docs/")
	testutil.AssertEqual(t, true, viaDirFound)
	testutil.AssertEqual(t, "<html>spa</html>", string(viaDir))
}

// The SPA shell is served for every client-side route, so it needs the same
// containment check as the direct request path. Without it a symlinked
// index.html pointing out of the static dir is refused at /index.html and
// served at /plans, which is the same asymmetry directoryIndex avoids.
func TestSPAFallbackRejectsSymlinkedShell(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "static")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir static: %v", err)
	}
	outside := filepath.Join(parent, "secret.html")
	if err := os.WriteFile(outside, []byte("should-not-serve"), 0o644); err != nil {
		t.Fatalf("write secret.html: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "index.html")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Direct spelling: already rejected before this change.
	_, _, _, directFound := serveStaticForLambda(root, "/index.html")
	testutil.AssertEqual(t, false, directFound)

	// Client-side route: must reach the same verdict, not serve the target.
	for _, route := range []string{"/plans", "/inventory/coverage", "/"} {
		t.Run(route, func(t *testing.T) {
			content, _, _, found := serveStaticForLambda(root, route)
			testutil.AssertEqual(t, false, found)
			testutil.AssertEqual(t, "", string(content))
		})
	}
}

// hostileRoot builds a static root with a sibling tree outside it holding a
// file that must never be served. Returns (root, outsideFile, symlinked), where
// symlinked reports whether the symlink cases could be created.
//
// The lexical fixture is built unconditionally and the symlink cases are
// additive, so a platform without symlink support loses the symlink cases only.
// t.Skip here would take the traversal, encoded-path, absolute-path and NUL
// cases with it and report the whole suite as skipped, which reads as a pass.
func hostileRoot(t *testing.T) (string, string, bool) {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "static")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, body := range map[string]string{
		"index.html":      "<html>spa</html>",
		"docs/index.html": "<html>docs</html>",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	outside := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(outside, []byte("TOP-SECRET"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	// Symlinks that escape the root, in the same fixture as the lexical cases.
	// Keeping them together means one `-run Hostile` covers both classes; when
	// they lived only in separately-named tests, a name-filtered mutation run
	// silently skipped the only cases exercising symlinkSafeContainedIn, which
	// is the check path.Clean cannot stand in for.
	symlinked := true
	for _, link := range []string{
		filepath.Join(root, "escape.txt"),
		filepath.Join(root, "docs", "leak.html"),
	} {
		if err := os.Symlink(outside, link); err != nil {
			t.Logf("symlinks unavailable, symlink cases skipped (lexical cases still run): %v", err)
			symlinked = false
			break
		}
	}
	return root, outside, symlinked
}

// symlinkCases returns the escaping-symlink paths when the fixture could create
// them, and nothing otherwise, so callers append rather than branch.
func symlinkCases(symlinked bool) []string {
	if !symlinked {
		return nil
	}
	return []string{"/escape.txt", "/docs/leak.html"}
}

// Asserting on the resolved path, not on a status code: a handler that serves
// the wrong file with 200 passes a status-only assertion. The invariant is that
// whatever resolveStaticFilePath hands back is inside the served root, with
// symlinks resolved, or it hands back nothing.
func TestResolveStaticFilePath_HostilePathsNeverEscapeRoot(t *testing.T) {
	root, outside, symlinked := hostileRoot(t)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("evalsymlinks root: %v", err)
	}

	hostile := append([]string{
		"/../secret.txt",
		"/../../secret.txt",
		"/..",
		"/../",
		"/docs/../../secret.txt",
		"/./../secret.txt",
		"/....//secret.txt",
		"/..%2fsecret.txt",           // encoded, as the Lambda RawPath transport delivers it
		"/%2e%2e%2fsecret.txt",       // fully encoded
		"/%252e%252e%252fsecret.txt", // double encoded
		"/..\\secret.txt",            // backslash separator
		"/" + outside,                // absolute path pasted into the URL
		outside,                      // absolute path, no leading join
		"//secret.txt",
		"/docs/./../../secret.txt",
		"/\x00/secret.txt", // NUL byte
		"/plans\x00.html",
	}, symlinkCases(symlinked)...)

	for _, p := range hostile {
		t.Run(strconv.Quote(p), func(t *testing.T) {
			filePath, _, ok := resolveStaticFilePath(root, p)
			if !ok {
				return // refused outright, which is a valid answer
			}
			abs, absErr := filepath.Abs(filePath)
			if absErr != nil {
				t.Fatalf("abs(%q): %v", filePath, absErr)
			}
			// Resolve symlinks before comparing: a lexically-contained path
			// whose target is outside is exactly the bypass being tested for.
			if resolved, symErr := filepath.EvalSymlinks(abs); symErr == nil {
				abs = resolved
			}
			if !isPathContainedIn(abs, realRoot) {
				t.Fatalf("path %q escaped the root: resolved to %q, outside %q", p, abs, realRoot)
			}
			// Belt and braces: never the secret, whatever the path.
			if data, readErr := os.ReadFile(abs); readErr == nil && strings.Contains(string(data), "TOP-SECRET") {
				t.Fatalf("path %q served the out-of-root secret from %q", p, abs)
			}
		})
	}
}

// Same matrix through the real HTTP handler, so Go's own URL decoding is in the
// loop rather than assumed. Asserts on the served bytes, not the status.
func TestSpaFileServer_HostilePathsNeverServeOutOfRoot(t *testing.T) {
	root, _, symlinked := hostileRoot(t)
	handler := spaFileServer(root)

	targets := append([]string{
		"/../secret.txt",
		"/../../secret.txt",
		"/docs/../../secret.txt",
		"/..%2fsecret.txt",
		"/%2e%2e%2fsecret.txt",
		"/%252e%252e%252fsecret.txt",
		"/..\\secret.txt",
		"//secret.txt",
		"/....//secret.txt",
	}, symlinkCases(symlinked)...)

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if strings.Contains(w.Body.String(), "TOP-SECRET") {
				t.Fatalf("target %q served out-of-root content (status %d)", target, w.Code)
			}
		})
	}
}

// The Lambda transport passes RawPath, which is not decoded the way
// http.Request.URL.Path is, so the two transports must be checked separately.
func TestServeStaticForLambda_HostilePathsNeverServeOutOfRoot(t *testing.T) {
	root, outside, symlinked := hostileRoot(t)

	paths := append([]string{
		"/../secret.txt",
		"/../../secret.txt",
		"/docs/../../secret.txt",
		"/..%2fsecret.txt",
		"/%2e%2e%2fsecret.txt",
		"/%252e%252e%252fsecret.txt",
		"/..\\secret.txt",
		"//secret.txt",
		outside,
	}, symlinkCases(symlinked)...)

	for _, p := range paths {
		t.Run(strconv.Quote(p), func(t *testing.T) {
			content, _, _, _ := serveStaticForLambda(root, p)
			if strings.Contains(string(content), "TOP-SECRET") {
				t.Fatalf("path %q served out-of-root content", p)
			}
		})
	}
}
