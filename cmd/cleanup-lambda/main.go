package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/aws/aws-lambda-go/lambda"
)

// CleanupEvent represents the input to the cleanup function.
type CleanupEvent struct {
	DryRun bool `json:"dryRun,omitempty"`
}

// CleanupResult represents the cleanup operation results.
type CleanupResult struct {
	SessionsDeleted   int64 `json:"sessionsDeleted"`
	ExecutionsDeleted int64 `json:"executionsDeleted"`
	DryRun            bool  `json:"dryRun"`
	Timestamp         int64 `json:"timestamp"`
}

func cleanupExpiredRecords(ctx context.Context, event CleanupEvent) (*CleanupResult, error) {
	log.Printf("Starting cleanup job (dryRun=%v)", event.DryRun)

	// A new DB connection is opened per invocation (no connection reuse across warm starts).
	// This is intentional: the cleanup Lambda runs infrequently and the simpler, stateless
	// design is preferred over the shared-connection pattern used in cmd/lambda/main.go.
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	now := time.Now()
	result := &CleanupResult{
		DryRun:    event.DryRun,
		Timestamp: now.Unix(),
	}

	if event.DryRun {
		if err := dryRunCount(ctx, db, now, result); err != nil {
			return nil, err
		}
	} else {
		if err := deleteExpired(ctx, db, now, result); err != nil {
			return nil, err
		}
	}

	log.Printf("Cleanup job completed: %+v", result)
	return result, nil
}

// dryRunCount counts records that would be deleted without actually deleting them.
func dryRunCount(ctx context.Context, db *database.Connection, now time.Time, result *CleanupResult) error {
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM sessions WHERE expires_at < $1", now).Scan(&result.SessionsDeleted); err != nil {
		return fmt.Errorf("failed to count expired sessions: %w", err)
	}
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM purchase_executions WHERE expires_at < $1", now).Scan(&result.ExecutionsDeleted); err != nil {
		return fmt.Errorf("failed to count expired executions: %w", err)
	}
	log.Printf("DRY RUN: Would delete %d sessions and %d executions", result.SessionsDeleted, result.ExecutionsDeleted)
	return nil
}

// deleteExpired deletes expired sessions and executions in a single transaction.
func deleteExpired(ctx context.Context, db *database.Connection, now time.Time, result *CleanupResult) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	tag, err := tx.Exec(ctx, "DELETE FROM sessions WHERE expires_at < $1", now)
	if err != nil {
		return fmt.Errorf("failed to cleanup sessions: %w", err)
	}
	result.SessionsDeleted = tag.RowsAffected()
	log.Printf("Deleted %d expired sessions", result.SessionsDeleted)

	tag, err = tx.Exec(ctx, "DELETE FROM purchase_executions WHERE expires_at < $1", now)
	if err != nil {
		return fmt.Errorf("failed to cleanup executions: %w", err)
	}
	result.ExecutionsDeleted = tag.RowsAffected()
	log.Printf("Deleted %d expired executions", result.ExecutionsDeleted)

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit cleanup transaction: %w", err)
	}
	return nil
}

func main() {
	lambda.Start(cleanupExpiredRecords)
}
