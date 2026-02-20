package migrations

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // postgres driver
	_ "github.com/golang-migrate/migrate/v4/source/file"       // file source
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunMigrations runs database migrations using golang-migrate
// adminEmail is optional - if provided, admin user will be created after migrations complete
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsPath string, adminEmail string) error {
	// Get database connection string from pool config (without admin email parameter - RDS Proxy doesn't support options)
	dsn := buildMigrateDSN(pool.Config(), "")

	// Create migrator
	m, err := migrate.New(
		fmt.Sprintf("file://%s", migrationsPath),
		dsn,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}
	defer m.Close()

	// Run migrations
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Get current version
	version, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("failed to get migration version: %w", err)
	}

	if dirty {
		return fmt.Errorf("database is in dirty state at version %d", version)
	}

	fmt.Printf("Database migrations completed successfully (version: %d)\n", version)

	// Create admin user if email provided (after migrations complete)
	if adminEmail != "" {
		if err := ensureAdminUser(ctx, pool, adminEmail); err != nil {
			return fmt.Errorf("failed to create admin user: %w", err)
		}
	}

	return nil
}

// ensureAdminUser creates the admin user if it doesn't exist
// The admin user is created with an empty password and must use password reset to set initial password
func ensureAdminUser(ctx context.Context, pool *pgxpool.Pool, email string) error {
	fmt.Printf("Ensuring admin user exists: %s (user will need to reset password to login)\n", email)

	// Check if user already exists
	var exists bool
	err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)", email).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if user exists: %w", err)
	}

	if exists {
		fmt.Printf("Admin user already exists: %s\n", email)
		return nil
	}

	// Create admin user with empty password
	_, err = pool.Exec(ctx, `
		INSERT INTO users (
			id, email, password_hash, salt, role, active, created_at, updated_at
		) VALUES (
			gen_random_uuid(), $1, '', '', 'admin', true, NOW(), NOW()
		)
	`, email)

	if err != nil {
		return fmt.Errorf("failed to insert admin user: %w", err)
	}

	fmt.Printf("✅ Admin user created: %s (password not set - user must reset)\n", email)
	return nil
}

// RollbackMigrations rolls back N migrations
func RollbackMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsPath string, steps int) error {
	dsn := buildMigrateDSN(pool.Config(), "")

	m, err := migrate.New(
		fmt.Sprintf("file://%s", migrationsPath),
		dsn,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}
	defer m.Close()

	// Rollback steps
	if err := m.Steps(-steps); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to rollback migrations: %w", err)
	}

	version, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("failed to get migration version: %w", err)
	}

	if dirty {
		return fmt.Errorf("database is in dirty state at version %d", version)
	}

	fmt.Printf("Rolled back %d migration(s) (current version: %d)\n", steps, version)
	return nil
}

// GetMigrationVersion returns the current migration version
func GetMigrationVersion(ctx context.Context, pool *pgxpool.Pool, migrationsPath string) (uint, bool, error) {
	dsn := buildMigrateDSN(pool.Config(), "")

	m, err := migrate.New(
		fmt.Sprintf("file://%s", migrationsPath),
		dsn,
	)
	if err != nil {
		return 0, false, fmt.Errorf("failed to create migrator: %w", err)
	}
	defer m.Close()

	version, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return 0, false, fmt.Errorf("failed to get migration version: %w", err)
	}

	return version, dirty, nil
}

// buildMigrateDSN builds a connection string for golang-migrate from pgx config
// Note: adminEmail parameter is kept for backward compatibility but ignored (RDS Proxy doesn't support options)
func buildMigrateDSN(config *pgxpool.Config, adminEmail string) string {
	// Extract connection details from pgx config
	host := config.ConnConfig.Host
	port := config.ConnConfig.Port
	user := config.ConnConfig.User
	password := config.ConnConfig.Password
	database := config.ConnConfig.Database

	// URL encode the username and password to handle special characters
	encodedUser := url.QueryEscape(user)
	encodedPassword := url.QueryEscape(password)

	// Build DSN (golang-migrate uses postgres:// format)
	// Don't add connection options - RDS Proxy doesn't support them
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=require",
		encodedUser,
		encodedPassword,
		host,
		port,
		database,
	)
}

// ValidateMigrationsPath checks if migrations directory exists
func ValidateMigrationsPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("migrations directory does not exist: %s", path)
		}
		return fmt.Errorf("failed to check migrations directory: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("migrations path is not a directory: %s", path)
	}

	return nil
}
