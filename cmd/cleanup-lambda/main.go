package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/LeanerCloud/CUDly/internal/secrets"
	"github.com/aws/aws-lambda-go/lambda"
)

// CleanupEvent represents the input to the cleanup function
type CleanupEvent struct {
	DryRun bool `json:"dryRun,omitempty"`
}

// CleanupResult represents the cleanup operation results
type CleanupResult struct {
	SessionsDeleted   int64 `json:"sessionsDeleted"`
	ExecutionsDeleted int64 `json:"executionsDeleted"`
	DryRun            bool  `json:"dryRun"`
	Timestamp         int64 `json:"timestamp"`
}

func cleanupExpiredRecords(ctx context.Context, event CleanupEvent) (*CleanupResult, error) {
	log.Printf("Starting cleanup job (dryRun=%v)", event.DryRun)

	// Initialize database connection with secret resolution
	dbConfig, err := database.LoadFromEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to load database config: %w", err)
	}

	// Create secret resolver if password secret is specified
	var secretResolver database.SecretResolver
	if dbConfig.PasswordSecret != "" {
		secretConfig := secrets.LoadConfigFromEnv()
		resolver, err := secrets.NewResolver(ctx, secretConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create secret resolver: %w", err)
		}
		defer resolver.Close()
		secretResolver = resolver
	}

	db, err := database.NewConnection(ctx, dbConfig, secretResolver)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	now := time.Now()
	result := &CleanupResult{
		DryRun:    event.DryRun,
		Timestamp: now.Unix(),
	}

	if event.DryRun {
		// Count what would be deleted
		err = db.QueryRow(ctx, "SELECT COUNT(*) FROM sessions WHERE expires_at < $1", now).Scan(&result.SessionsDeleted)
		if err != nil {
			return nil, fmt.Errorf("failed to count expired sessions: %w", err)
		}

		err = db.QueryRow(ctx, "SELECT COUNT(*) FROM purchase_executions WHERE expires_at < $1", now).Scan(&result.ExecutionsDeleted)
		if err != nil {
			return nil, fmt.Errorf("failed to count expired executions: %w", err)
		}

		log.Printf("DRY RUN: Would delete %d sessions and %d executions", result.SessionsDeleted, result.ExecutionsDeleted)
	} else {
		// Delete expired sessions
		tag, err := db.Exec(ctx, "DELETE FROM sessions WHERE expires_at < $1", now)
		if err != nil {
			return nil, fmt.Errorf("failed to cleanup sessions: %w", err)
		}
		result.SessionsDeleted = tag.RowsAffected()
		log.Printf("Deleted %d expired sessions", result.SessionsDeleted)

		// Delete expired executions
		tag, err = db.Exec(ctx, "DELETE FROM purchase_executions WHERE expires_at < $1", now)
		if err != nil {
			return nil, fmt.Errorf("failed to cleanup executions: %w", err)
		}
		result.ExecutionsDeleted = tag.RowsAffected()
		log.Printf("Deleted %d expired executions", result.ExecutionsDeleted)
	}

	log.Printf("Cleanup job completed: %+v", result)
	return result, nil
}

func main() {
	lambda.Start(cleanupExpiredRecords)
}
