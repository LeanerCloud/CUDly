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
	"golang.org/x/crypto/bcrypt"
)

// bcryptCost matches the cost used in internal/auth/service_password.go
const bcryptCost = 12

// RunMigrations runs database migrations using golang-migrate
// adminEmail is optional - if provided, admin user will be created after migrations complete
// adminPassword is optional - if provided, admin is created with hashed password and active=true
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsPath string, adminEmail string, adminPassword string) error {
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
		if err := ensureAdminUser(ctx, pool, adminEmail, adminPassword); err != nil {
			return fmt.Errorf("failed to create admin user: %w", err)
		}
	}

	return nil
}

// ensureAdminUser creates the admin user if it doesn't exist.
// When password is provided, the admin is created with a bcrypt-hashed password and active=true.
// When password is empty, the admin is created inactive and must use password reset to log in.
func ensureAdminUser(ctx context.Context, pool *pgxpool.Pool, email string, password string) error {
	if password != "" {
		return ensureAdminUserWithPassword(ctx, pool, email, password)
	}

	fmt.Printf("Ensuring admin user exists: %s (user will need to reset password to login)\n", email)

	// Create admin user with no password - account is inactive until password is set via reset flow
	// Use ON CONFLICT to prevent race conditions when multiple instances run migrations
	result, err := pool.Exec(ctx, `
		INSERT INTO users (
			id, email, password_hash, salt, role, active, created_at, updated_at
		) VALUES (
			gen_random_uuid(), $1, '', '', 'admin', false, NOW(), NOW()
		)
		ON CONFLICT (email) DO NOTHING
	`, email)

	if err != nil {
		return fmt.Errorf("failed to insert admin user: %w", err)
	}

	if result.RowsAffected() > 0 {
		fmt.Printf("Admin user created: %s (password not set - user must reset)\n", email)
	} else {
		fmt.Printf("Admin user already exists: %s\n", email)
	}
	return nil
}

// ensureAdminUserWithPassword creates or updates the admin with a hashed password and active=true.
// If the admin already exists with an empty password_hash, the password and active flag are updated.
func ensureAdminUserWithPassword(ctx context.Context, pool *pgxpool.Pool, email string, password string) error {
	fmt.Printf("Ensuring admin user exists with password: %s\n", email)

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("failed to hash admin password: %w", err)
	}

	// Insert new admin or update existing one that has no password set yet.
	// Only overwrite password_hash/active when the existing hash is empty,
	// so we never clobber a password that was already set via the UI.
	result, err := pool.Exec(ctx, `
		INSERT INTO users (
			id, email, password_hash, salt, role, active, created_at, updated_at
		) VALUES (
			gen_random_uuid(), $1, $2, '', 'admin', true, NOW(), NOW()
		)
		ON CONFLICT (email) DO UPDATE
			SET password_hash = $2, active = true, updated_at = NOW()
			WHERE users.password_hash = ''
	`, email, string(hashedPassword))

	if err != nil {
		return fmt.Errorf("failed to upsert admin user: %w", err)
	}

	if result.RowsAffected() > 0 {
		fmt.Printf("Admin user created/activated with password: %s\n", email)
	} else {
		fmt.Printf("Admin user already has a password set: %s (skipping)\n", email)
	}
	return nil
}

// RollbackMigrations rolls back N migrations
func RollbackMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsPath string, steps int) error {
	if steps <= 0 {
		return fmt.Errorf("rollback steps must be positive, got %d", steps)
	}
	const maxRollbackSteps = 10
	if steps > maxRollbackSteps {
		return fmt.Errorf("refusing to rollback more than %d migrations at once (requested %d); use multiple calls for safety", maxRollbackSteps, steps)
	}

	dsn := buildMigrateDSN(pool.Config(), "")

	m, err := migrate.New(
		fmt.Sprintf("file://%s", migrationsPath),
		dsn,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}
	defer m.Close()

	// Log current version before rollback
	currentVersion, _, _ := m.Version()
	fmt.Printf("Rolling back %d migration(s) from version %d...\n", steps, currentVersion)

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

// buildMigrateDSN builds a connection string for golang-migrate from pgx config.
// sslModeOverride, if non-empty, is used instead of inferring from TLSConfig.
func buildMigrateDSN(config *pgxpool.Config, sslModeOverride string) string {
	// Extract connection details from pgx config
	host := config.ConnConfig.Host
	port := config.ConnConfig.Port
	user := config.ConnConfig.User
	password := config.ConnConfig.Password
	database := config.ConnConfig.Database

	// URL encode the username and password to handle special characters
	encodedUser := url.QueryEscape(user)
	encodedPassword := url.QueryEscape(password)

	// Use explicit sslmode if provided, otherwise infer from TLS config
	sslMode := sslModeOverride
	if sslMode == "" {
		sslMode = "require"
		if config.ConnConfig.TLSConfig == nil {
			sslMode = "disable"
		}
	}

	// Build DSN (golang-migrate uses postgres:// format)
	// Don't add connection options - RDS Proxy doesn't support them
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		encodedUser,
		encodedPassword,
		host,
		port,
		database,
		sslMode,
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
