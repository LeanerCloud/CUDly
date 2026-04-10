package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			testutil.AssertEqual(t, tt.expected, isStaticPath(tt.path))
		})
	}
}

// ----- hasFileContent -----

func TestHasFileContent_NonEmptyDir(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html/>"})
	testutil.AssertEqual(t, true, hasFileContent(dir))
}

func TestHasFileContent_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	testutil.AssertEqual(t, false, hasFileContent(dir))
}

func TestHasFileContent_NonExistentDir(t *testing.T) {
	testutil.AssertEqual(t, false, hasFileContent("/nonexistent/path/that/does/not/exist"))
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

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	testutil.AssertEqual(t, http.StatusOK, w.Code)
}

func TestSpaFileServer_ServesIndexForRoot(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html>root</html>"})

	handler := spaFileServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	testutil.AssertEqual(t, http.StatusOK, w.Code)
}

func TestSpaFileServer_SPAFallbackForUnknownPath(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html>spa</html>"})

	handler := spaFileServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/some/route", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	testutil.AssertEqual(t, http.StatusOK, w.Code)
}

func TestSpaFileServer_404ForMissingExtensionFile(t *testing.T) {
	dir := makeStaticDir(t, map[string]string{"index.html": "<html/>"})

	handler := spaFileServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/missing.png", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	testutil.AssertEqual(t, http.StatusNotFound, w.Code)
}

func TestSpaFileServer_404WhenIndexMissing(t *testing.T) {
	dir := t.TempDir() // no index.html

	handler := spaFileServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/any/route", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	testutil.AssertEqual(t, http.StatusNotFound, w.Code)
}
