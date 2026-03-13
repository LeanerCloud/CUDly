// Package main provides the Lambda entry point for CUDly.
// This handler uses the unified server package with PostgreSQL backend.
// It processes multiple event types:
// - Scheduled events for recommendation collection
// - HTTP requests for the dashboard API
// - Purchase approval workflow events
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/LeanerCloud/CUDly/internal/server"
	"github.com/aws/aws-lambda-go/lambda"
)

// Version is set at build time
var Version = "dev"

var (
	app   *server.Application
	appMu sync.Mutex
)

// initApp initializes the application using the unified server package.
// Uses a mutex to protect against concurrent initialization.
func initApp(ctx context.Context) (*server.Application, error) {
	appMu.Lock()
	defer appMu.Unlock()

	if app != nil {
		return app, nil
	}

	// Set version for the application
	os.Setenv("VERSION", Version)

	log.Printf("CUDly Lambda Handler starting, version: %s", Version)

	// Initialize using the unified server package (PostgreSQL-based)
	var err error
	app, err = server.NewApplication(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize application: %w", err)
	}

	log.Println("Lambda handler initialized successfully")
	return app, nil
}

// Handler is the main Lambda handler function
// This delegates to Application.HandleLambdaEvent which handles all event types
func Handler(ctx context.Context, rawEvent json.RawMessage) (interface{}, error) {
	// Initialize app on first request (lazy initialization)
	application, err := initApp(ctx)
	if err != nil {
		log.Printf("Failed to initialize application: %v", err)
		return nil, fmt.Errorf("initialization failed: %w", err)
	}

	// Delegate to the unified server package
	return application.HandleLambdaEvent(ctx, rawEvent)
}

func main() {
	lambda.Start(Handler)
}
