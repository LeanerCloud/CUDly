// Package main provides the unified entry point for CUDly server.
// It supports both AWS Lambda and standard HTTP server modes.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/LeanerCloud/CUDly/internal/server"
)

// Version, BuildTime, and GitSHA are set at build time via ldflags.
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitSHA    = "unknown"
)

func main() {
	// Parse command line flags
	mode := flag.String("mode", "auto", "Runtime mode: auto, lambda, http")
	port := flag.Int("port", 8080, "HTTP server port (ignored in lambda mode)")
	task := flag.String("task", "", "Run a scheduled task and exit (e.g., collect_recommendations, cleanup)")
	flag.Parse()

	// Print version info
	log.Printf("CUDly Server v%s (git: %s, built: %s)", Version, GitSHA, BuildTime)

	// Export build metadata to the environment so the api package can read it
	// without importing main (which would create an import cycle).
	os.Setenv("VERSION", Version)
	os.Setenv("BUILD_TIME", BuildTime)
	os.Setenv("GIT_SHA", GitSHA)

	ctx := context.Background()

	// Initialize application
	app, err := server.NewApplication(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}
	defer app.Close()

	// If --task is provided, run the task with a timeout and exit
	if *task != "" {
		timeout := getTaskTimeout()
		taskCtx, cancel := context.WithTimeout(ctx, timeout)

		log.Printf("Running scheduled task: %s (timeout: %v)", *task, timeout)
		taskType := server.ScheduledTaskType(*task)
		result, err := app.HandleScheduledTask(taskCtx, taskType)
		cancel()
		if err != nil {
			log.Fatalf("Scheduled task %q failed: %v", *task, err)
		}
		log.Printf("Scheduled task %q completed successfully: %v", *task, result)
		return
	}

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

// getTaskTimeout returns the task timeout from TASK_TIMEOUT env var or the default of 15 minutes.
// Logs a warning when TASK_TIMEOUT is set but cannot be parsed or is non-positive,
// so the operator knows the value was not applied.
func getTaskTimeout() time.Duration {
	const defaultTimeout = 15 * time.Minute
	if v := os.Getenv("TASK_TIMEOUT"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil {
			log.Printf("WARNING: TASK_TIMEOUT=%q is not a valid integer; using default %v", v, defaultTimeout)
			return defaultTimeout
		}
		if secs <= 0 {
			log.Printf("WARNING: TASK_TIMEOUT=%q must be a positive number; using default %v", v, defaultTimeout)
			return defaultTimeout
		}
		return time.Duration(secs) * time.Second
	}
	return defaultTimeout
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
		default:
			log.Printf("Warning: unrecognized RUNTIME_MODE %q, falling back to auto-detection", runtimeMode)
		}
	}

	// Default to HTTP mode for containers (Fargate, Cloud Run, Container Apps)
	return "http"
}
