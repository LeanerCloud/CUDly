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

// serveStaticForLambda checks if the request path matches a static file in dir.
// Returns the file content, content type, cache header, and whether a file was found.
func serveStaticForLambda(dir, urlPath string) (content []byte, contentType string, cacheControl string, found bool) {
	cleanPath := path.Clean(urlPath)
	if cleanPath == "/" || cleanPath == "." {
		cleanPath = "/index.html"
	}

	filePath := filepath.Join(dir, filepath.FromSlash(cleanPath))

	// Prevent directory traversal
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", "", false
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return nil, "", "", false
	}
	if !strings.HasPrefix(absFile, absDir) {
		return nil, "", "", false
	}

	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		// File not found: if path has extension, truly not found
		if path.Ext(cleanPath) != "" {
			return nil, "", "", false
		}
		// SPA fallback: serve index.html
		filePath = filepath.Join(dir, "index.html")
		cleanPath = "/index.html"
		if _, err := os.Stat(filePath); err != nil {
			return nil, "", "", false
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, "", "", false
	}

	// Determine content type from extension
	ext := strings.ToLower(path.Ext(cleanPath))
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}

	// Determine cache control
	cc := "public, max-age=3600"
	switch ext {
	case ".html":
		cc = "no-cache, no-store, must-revalidate"
	case ".js", ".css", ".woff", ".woff2", ".ttf", ".eot",
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp":
		cc = "public, max-age=31536000, immutable"
	}

	return data, ct, cc, true
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
func isStaticPath(urlPath string) bool {
	if strings.HasPrefix(urlPath, "/api/") || urlPath == "/api" {
		return false
	}
	if urlPath == "/health" {
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
