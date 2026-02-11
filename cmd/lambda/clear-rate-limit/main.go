package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	_ "github.com/lib/pq"
)

type Response struct {
	Message         string `json:"message"`
	DeletedCount    int    `json:"deleted_count"`
	RemainingCount  int    `json:"remaining_count"`
}

func clearRateLimit(ctx context.Context) (Response, error) {
	// Get database connection details from environment
	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbName := os.Getenv("DB_NAME")
	dbUser := os.Getenv("DB_USER")
	dbPassword := os.Getenv("DB_PASSWORD")

	if dbHost == "" || dbName == "" || dbUser == "" || dbPassword == "" {
		return Response{}, fmt.Errorf("missing required environment variables")
	}

	if dbPort == "" {
		dbPort = "5432"
	}

	// Connect to database
	connStr := fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s sslmode=require",
		dbHost, dbPort, dbName, dbUser, dbPassword)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return Response{}, fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		return Response{}, fmt.Errorf("failed to ping database: %w", err)
	}

	// Clear rate limits for forgot_password endpoint
	result, err := db.ExecContext(ctx,
		"DELETE FROM rate_limits WHERE id LIKE 'EMAIL#%@leanercloud.com#ENDPOINT#forgot_password'")
	if err != nil {
		return Response{}, fmt.Errorf("failed to delete rate limits: %w", err)
	}

	deletedCount, _ := result.RowsAffected()

	// Get remaining count
	var remainingCount int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM rate_limits").Scan(&remainingCount)
	if err != nil {
		return Response{}, fmt.Errorf("failed to count remaining rate limits: %w", err)
	}

	return Response{
		Message:        fmt.Sprintf("Successfully cleared %d rate limit(s)", deletedCount),
		DeletedCount:   int(deletedCount),
		RemainingCount: remainingCount,
	}, nil
}

func main() {
	lambda.Start(clearRateLimit)
}
