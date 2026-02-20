//go:build integration
// +build integration

package testutil

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresContainer holds the testcontainer for PostgreSQL
type PostgresContainer struct {
	Container testcontainers.Container
	Host      string
	Port      string
	Database  string
	Username  string
	Password  string
}

// SetupPostgresContainer creates and starts a PostgreSQL testcontainer
func SetupPostgresContainer(ctx context.Context, t *testing.T) (*PostgresContainer, error) {
	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "cudly_test",
			"POSTGRES_USER":     "cudly_test",
			"POSTGRES_PASSWORD": "test_password",
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort("5432/tcp"),
		).WithDeadline(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start postgres container: %w", err)
	}

	// Clean up container when test ends
	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Errorf("failed to terminate container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	mappedPort, err := container.MappedPort(ctx, "5432")
	if err != nil {
		return nil, fmt.Errorf("failed to get container port: %w", err)
	}

	return &PostgresContainer{
		Container: container,
		Host:      host,
		Port:      mappedPort.Port(),
		Database:  "cudly_test",
		Username:  "cudly_test",
		Password:  "test_password",
	}, nil
}

// ConnectionString returns a PostgreSQL connection string
func (pc *PostgresContainer) ConnectionString() string {
	return fmt.Sprintf("postgresql://%s:%s@%s:%s/%s?sslmode=disable",
		pc.Username, pc.Password, pc.Host, pc.Port, pc.Database)
}

// Config returns a database configuration for the test container
func (pc *PostgresContainer) Config() map[string]string {
	return map[string]string{
		"DB_HOST":     pc.Host,
		"DB_PORT":     pc.Port,
		"DB_NAME":     pc.Database,
		"DB_USER":     pc.Username,
		"DB_PASSWORD": pc.Password,
		"DB_SSL_MODE": "disable",
	}
}
