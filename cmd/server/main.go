// Package main provides the unified entry point for CUDly server.
// It supports both AWS Lambda and standard HTTP server modes.
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/LeanerCloud/CUDly/internal/server"
)

// Version and BuildTime are set at build time via ldflags
var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	// Parse command line flags
	mode := flag.String("mode", "auto", "Runtime mode: auto, lambda, http")
	port := flag.Int("port", 8080, "HTTP server port (ignored in lambda mode)")
	flag.Parse()

	// Print version info
	log.Printf("CUDly Server v%s (built: %s)", Version, BuildTime)

	// Set version in environment for the application
	if Version != "" {
		os.Setenv("VERSION", Version)
	}

	ctx := context.Background()

	// Initialize application
	app, err := server.NewApplication(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}
	defer app.Close()

	// Determine runtime mode
	runtimeMode := determineRuntimeMode(*mode)
	log.Printf("Starting CUDly server in %s mode", runtimeMode)

	// Start appropriate server
	switch runtimeMode {
	case "lambda":
		server.StartLambdaHandler(app)
	case "http":
		if err := server.StartHTTPServer(app, *port); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	default:
		log.Fatalf("Unknown runtime mode: %s", runtimeMode)
	}
}

// determineRuntimeMode determines the runtime mode based on flags and environment
func determineRuntimeMode(modeFlag string) string {
	// If mode is explicitly set, use it
	if modeFlag != "auto" {
		return modeFlag
	}

	// Auto-detect based on environment
	// Lambda sets AWS_LAMBDA_RUNTIME_API when running
	if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
		return "lambda"
	}

	// Check for explicit RUNTIME_MODE environment variable
	if runtimeMode := os.Getenv("RUNTIME_MODE"); runtimeMode != "" {
		switch runtimeMode {
		case "lambda", "http":
			return runtimeMode
		}
	}

	// Default to HTTP mode for containers (Fargate, Cloud Run, Container Apps)
	return "http"
}
