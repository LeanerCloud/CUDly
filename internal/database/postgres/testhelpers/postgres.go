package testhelpers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresContainer wraps a testcontainers PostgreSQL instance
type PostgresContainer struct {
	Container testcontainers.Container
	Config    *database.Config
	DB        *database.Connection
}

// SetupPostgresContainer creates and starts a PostgreSQL test container
func SetupPostgresContainer(ctx context.Context, t *testing.T) (*PostgresContainer, error) {
	t.Helper()

	// Create PostgreSQL container
	postgresContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("cudly_test"),
		postgres.WithUsername("cudly_test"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start postgres container: %w", err)
	}

	// Get connection details
	host, err := postgresContainer.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	port, err := postgresContainer.MappedPort(ctx, "5432")
	if err != nil {
		return nil, fmt.Errorf("failed to get container port: %w", err)
	}

	// Build database config
	config := &database.Config{
		Host:              host,
		Port:              port.Int(),
		Database:          "cudly_test",
		User:              "cudly_test",
		Password:          "test_password",
		SSLMode:           "disable",
		MaxConnections:    10,
		MinConnections:    1,
		MaxConnLifetime:   time.Hour,
		MaxConnIdleTime:   30 * time.Minute,
		HealthCheckPeriod: time.Minute,
		ConnectTimeout:    10 * time.Second,
		AutoMigrate:       false,
		MigrationsPath:    "../migrations",
		LogLevel:          "error",
	}

	// Create database connection
	db, err := database.NewConnection(ctx, config, nil)
	if err != nil {
		postgresContainer.Terminate(ctx)
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return &PostgresContainer{
		Container: postgresContainer,
		Config:    config,
		DB:        db,
	}, nil
}

// Cleanup terminates the test container and closes database connection
func (c *PostgresContainer) Cleanup(ctx context.Context) error {
	if c.DB != nil {
		c.DB.Close()
	}
	if c.Container != nil {
		return c.Container.Terminate(ctx)
	}
	return nil
}

// TruncateTables removes all data from tables (useful between tests)
func (c *PostgresContainer) TruncateTables(ctx context.Context, tables ...string) error {
	for _, table := range tables {
		// Use pgx.Identifier to safely quote table names and prevent SQL injection
		ident := pgx.Identifier{table}
		query := fmt.Sprintf("TRUNCATE TABLE %s CASCADE", ident.Sanitize())
		if _, err := c.DB.Exec(ctx, query); err != nil {
			return fmt.Errorf("failed to truncate table %s: %w", table, err)
		}
	}
	return nil
}

// ResetDatabase drops and recreates all tables (useful for clean state)
func (c *PostgresContainer) ResetDatabase(ctx context.Context) error {
	// Drop all tables
	query := `
		DO $$
		DECLARE
			r RECORD;
		BEGIN
			FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public') LOOP
				EXECUTE 'DROP TABLE IF EXISTS ' || quote_ident(r.tablename) || ' CASCADE';
			END LOOP;
		END $$;
	`
	if _, err := c.DB.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to drop tables: %w", err)
	}

	return nil
}
