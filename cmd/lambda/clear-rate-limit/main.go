package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/LeanerCloud/CUDly/internal/secrets"
	"github.com/aws/aws-lambda-go/lambda"
)

const defaultDomain = "leanercloud.com"

func getDomain() string {
	if domain := os.Getenv("RATE_LIMIT_DOMAIN"); domain != "" {
		return domain
	}
	return defaultDomain
}

type Response struct {
	Message        string `json:"message"`
	DeletedCount   int    `json:"deleted_count"`
	RemainingCount int    `json:"remaining_count"`
}

func clearRateLimit(ctx context.Context) (Response, error) {
	// Initialize database connection with secret resolution
	dbConfig, err := database.LoadFromEnv()
	if err != nil {
		return Response{}, fmt.Errorf("failed to load database config: %w", err)
	}

	// Create secret resolver if password secret is specified
	var secretResolver database.SecretResolver
	if dbConfig.PasswordSecret != "" {
		secretConfig := secrets.LoadConfigFromEnv()
		resolver, err := secrets.NewResolver(ctx, secretConfig)
		if err != nil {
			return Response{}, fmt.Errorf("failed to create secret resolver: %w", err)
		}
		defer resolver.Close()
		secretResolver = resolver
	}

	db, err := database.NewConnection(ctx, dbConfig, secretResolver)
	if err != nil {
		return Response{}, fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	// Clear rate limits for forgot_password endpoint
	domain := getDomain()
	tag, err := db.Exec(ctx,
		"DELETE FROM rate_limits WHERE id LIKE $1",
		"EMAIL#%@"+domain+"#ENDPOINT#forgot_password")
	if err != nil {
		return Response{}, fmt.Errorf("failed to delete rate limits: %w", err)
	}

	deletedCount := tag.RowsAffected()

	// Get remaining count
	var remainingCount int64
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM rate_limits").Scan(&remainingCount)
	if err != nil {
		return Response{}, fmt.Errorf("failed to count remaining rate limits: %w", err)
	}

	log.Printf("Successfully cleared %d rate limit(s), %d remaining", deletedCount, remainingCount)

	return Response{
		Message:        fmt.Sprintf("Successfully cleared %d rate limit(s)", deletedCount),
		DeletedCount:   int(deletedCount),
		RemainingCount: int(remainingCount),
	}, nil
}

func main() {
	lambda.Start(clearRateLimit)
}
