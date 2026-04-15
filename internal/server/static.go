package server

import (
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// spaFileServer returns an http.Handler that serves static files from dir
// with SPA fallback: if the requested file doesn't exist and the path has
// no file extension, it serves index.html for client-side routing.
func spaFileServer(dir string) http.Handler {
	return &spaHandler{dir: dir}
}

type spaHandler struct {
	dir string
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Clean the path to prevent directory traversal
	urlPath := path.Clean(r.URL.Path)
	if urlPath == "/" {
		urlPath = "/index.html"
	}

	// Try to serve the requested file
	filePath := filepath.Join(h.dir, filepath.FromSlash(urlPath))

	info, err := os.Stat(filePath)
	if err == nil && !info.IsDir() {
		setCacheHeaders(w, urlPath)
		http.ServeFile(w, r, filePath)
		return
	}

	// File not found: if path has an extension, return 404
	if path.Ext(urlPath) != "" {
		http.NotFound(w, r)
		return
	}

	// SPA fallback: serve index.html for extensionless paths
	indexPath := filepath.Join(h.dir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		http.NotFound(w, r)
		return
	}
	setCacheHeaders(w, "/index.html")
	http.ServeFile(w, r, indexPath)
}

// setCacheHeaders sets Cache-Control based on file type.
// HTML files get no-cache; hashed assets (JS/CSS/images) get immutable long cache.
func setCacheHeaders(w http.ResponseWriter, urlPath string) {
	ext := strings.ToLower(path.Ext(urlPath))
	switch ext {
	case ".html":
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	case ".js", ".css", ".woff", ".woff2", ".ttf", ".eot",
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp":
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
}

// resolveStaticFilePath validates the URL path against directory traversal and
// resolves the actual file path. Falls back to index.html for extensionless
// paths (SPA routing). Returns the file path, the clean path used for content
// type detection, and whether a valid file was found.
func resolveStaticFilePath(dir, urlPath string) (filePath, cleanPath string, ok bool) {
	cleanPath = path.Clean(urlPath)
	if cleanPath == "/" || cleanPath == "." {
		cleanPath = "/index.html"
	}

	filePath = filepath.Join(dir, filepath.FromSlash(cleanPath))

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", false
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return "", "", false
	}
	if !strings.HasPrefix(absFile, absDir) {
		return "", "", false
	}

	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		if path.Ext(cleanPath) != "" {
			return "", "", false
		}
		// SPA fallback
		filePath = filepath.Join(dir, "index.html")
		cleanPath = "/index.html"
		if _, err := os.Stat(filePath); err != nil {
			return "", "", false
		}
	}

	return filePath, cleanPath, true
}

// cacheControlForExt returns the Cache-Control header value for a file extension.
func cacheControlForExt(ext string) string {
	switch ext {
	case ".html":
		return "no-cache, no-store, must-revalidate"
	case ".js", ".css", ".woff", ".woff2", ".ttf", ".eot",
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp":
		return "public, max-age=31536000, immutable"
	default:
		return "public, max-age=3600"
	}
}

// serveStaticForLambda checks if the request path matches a static file in dir.
// Returns the file content, content type, cache header, and whether a file was found.
func serveStaticForLambda(dir, urlPath string) (content []byte, contentType string, cacheControl string, found bool) {
	filePath, cleanPath, ok := resolveStaticFilePath(dir, urlPath)
	if !ok {
		return nil, "", "", false
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, "", "", false
	}

	ext := strings.ToLower(path.Ext(cleanPath))
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}

	return data, ct, cacheControlForExt(ext), true
}

// staticDirFromEnv returns the STATIC_DIR env var if set and the directory exists.
func staticDirFromEnv() string {
	dir := os.Getenv("STATIC_DIR")
	if dir == "" {
		return ""
	}
	// Verify the directory and index.html exist
	indexPath := filepath.Join(dir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		if !os.IsNotExist(err) {
			log.Printf("STATIC_DIR set to %s but index.html not accessible: %v", dir, err)
		}
		return ""
	}
	// Verify it's actually a directory
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		log.Printf("STATIC_DIR %s is not a directory", dir)
		return ""
	}
	log.Printf("Static file serving enabled from %s", dir)
	return dir
}

// isStaticPath returns true if the path should be handled by the static file
// server rather than the API. API paths start with /api/ or are /health.
// The OIDC issuer discovery endpoints (/.well-known/openid-configuration
// and /.well-known/jwks.json) are also routed to the API handler — they
// must be served as JSON, not as the SPA index fallback.
func isStaticPath(urlPath string) bool {
	// Normalize double slashes (e.g. //health -> /health) that can arise
	// when a trailing-slash base URL is concatenated with a path.
	clean := path.Clean(urlPath)
	if strings.HasPrefix(clean, "/api/") || clean == "/api" {
		return false
	}
	if clean == "/health" {
		return false
	}
	if strings.HasPrefix(clean, "/.well-known/") {
		return false
	}
	return true
}

// hasFileContent checks if the static dir contains at least one file,
// used during startup to validate the STATIC_DIR configuration.
func hasFileContent(dir string) bool {
	entries, err := fs.ReadDir(os.DirFS(dir), ".")
	if err != nil {
		return false
	}
	return len(entries) > 0
}
