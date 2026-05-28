package migrations

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // postgres driver
	_ "github.com/golang-migrate/migrate/v4/source/file"       // file source
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// bcryptCost matches the cost used in internal/auth/service_password.go
const bcryptCost = 12

// defaultAdminGroupID is the fixed UUID of the Administrators group
// seeded by migration 000024_seed_default_groups.up.sql. Duplicated
// here as a literal (rather than imported from internal/auth) because
// the migrations package must not depend on the auth package -
// auth depends on the DB, not the reverse. Keep in sync with
// auth.DefaultAdminGroupID (internal/auth/types.go) and the literal
// inside the 000024 migration SQL.
const defaultAdminGroupID = "00000000-0000-5000-8000-000000000001"

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

	// One-shot operator recovery: if CUDLY_FORCE_MIGRATION_VERSION is set,
	// call Force(N) before Up(). Clears the dirty flag and pins state to
	// the given version. Used to recover from a partially-applied
	// migration without direct DB access. Remove the env var after the
	// next successful deploy.
	if err := maybeForceMigrationVersion(m); err != nil {
		return err
	}

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

	log.Printf("Database migrations completed successfully (version: %d)", version)

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
//
// The INSERT seeds group_ids with the Administrators group so the user
// has full group-based permissions from the first boot. After the
// insert, assignAdminGroupAndWarn runs an idempotent backfill on any
// admin row whose group_ids drifted to empty (e.g. from an out-of-band
// manual DB seed), and warns operators if the drift cannot be repaired
// (e.g. groups table not yet populated). See issue #351.
func ensureAdminUser(ctx context.Context, pool *pgxpool.Pool, email string, password string) error {
	if password != "" {
		return ensureAdminUserWithPassword(ctx, pool, email, password)
	}

	log.Printf("Ensuring admin user exists: %s (user will need to reset password to login)", email)

	// Create admin user with no password - account is inactive until password is set via reset flow.
	// Use ON CONFLICT to prevent race conditions when multiple instances run migrations.
	// group_ids is seeded with the Administrators group so a fresh bootstrap admin
	// has group-based permissions from the start (issue #351).
	result, err := pool.Exec(ctx, `
		INSERT INTO users (
			id, email, password_hash, salt, role, active, group_ids, created_at, updated_at
		) VALUES (
			gen_random_uuid(), $1, '', '', 'admin', false, ARRAY[$2]::UUID[], NOW(), NOW()
		)
		ON CONFLICT (email) DO NOTHING
	`, email, defaultAdminGroupID)

	if err != nil {
		return fmt.Errorf("failed to insert admin user: %w", err)
	}

	if result.RowsAffected() > 0 {
		log.Printf("Admin user created: %s (password not set - user must reset)", email)
	} else {
		log.Printf("Admin user already exists: %s", email)
	}

	// Idempotent backfill + invariant check on any pre-existing admin
	// row whose group_ids drifted to empty after migration 000024's
	// one-shot backfill already ran.
	if err := assignAdminGroupAndWarn(ctx, pool, defaultAdminGroupID); err != nil {
		return fmt.Errorf("failed to assign admin group: %w", err)
	}
	return nil
}

// ensureAdminUserWithPassword creates or updates the admin with a hashed password and active=true.
// If the admin already exists with an empty password_hash, the password and active flag are updated.
//
// The INSERT seeds group_ids with the Administrators group so the user
// has full group-based permissions from the first boot. The DO UPDATE
// clause is deliberately NOT extended to touch group_ids - the
// post-insert assignAdminGroupAndWarn handles drift uniformly without
// coupling that semantics to the password-empty WHERE clause. See
// issue #351.
func ensureAdminUserWithPassword(ctx context.Context, pool *pgxpool.Pool, email string, password string) error {
	log.Printf("Ensuring admin user exists with password: %s", email)

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("failed to hash admin password: %w", err)
	}

	// Insert new admin or update existing one that has no password set yet.
	// Only overwrite password_hash/active when the existing hash is empty,
	// so we never clobber a password that was already set via the UI.
	// group_ids is seeded with the Administrators group on insert (issue #351).
	result, err := pool.Exec(ctx, `
		INSERT INTO users (
			id, email, password_hash, salt, role, active, group_ids, created_at, updated_at
		) VALUES (
			gen_random_uuid(), $1, $2, '', 'admin', true, ARRAY[$3]::UUID[], NOW(), NOW()
		)
		ON CONFLICT (email) DO UPDATE
			SET password_hash = $2, active = true, updated_at = NOW()
			WHERE users.password_hash = ''
	`, email, string(hashedPassword), defaultAdminGroupID)

	if err != nil {
		return fmt.Errorf("failed to upsert admin user: %w", err)
	}

	if result.RowsAffected() > 0 {
		log.Printf("Admin user created/activated: %s", email)
	} else {
		log.Printf("Admin user already has a password set: %s (skipping)", email)
	}

	// Idempotent backfill + invariant check on any pre-existing admin
	// row whose group_ids drifted to empty after migration 000024's
	// one-shot backfill already ran.
	if err := assignAdminGroupAndWarn(ctx, pool, defaultAdminGroupID); err != nil {
		return fmt.Errorf("failed to assign admin group: %w", err)
	}
	return nil
}

// assignAdminGroupAndWarn runs an idempotent backfill that appends
// groupID to any admin row whose group_ids is empty (NULL or
// zero-length). The DISTINCT(unnest(...)) dedupe makes the UPDATE
// safe to run repeatedly. After the backfill, a defensive SELECT
// counts any admin rows still showing empty group_ids and logs a
// WARN so operators see drift in container logs rather than only via
// a broken UI. This is the "defence-in-depth invariant" described
// in issue #351.
//
// The EXISTS guard on the groups table makes the backfill a no-op
// when migration 000024 hasn't yet seeded the Administrators group -
// defence-in-depth, since in practice this function is invoked
// after RunMigrations -> m.Up() completes.
func assignAdminGroupAndWarn(ctx context.Context, pool *pgxpool.Pool, groupID string) error {
	res, err := pool.Exec(ctx, `
		UPDATE users
		SET group_ids = ARRAY(
			SELECT DISTINCT unnest(
				COALESCE(group_ids, '{}') || ARRAY[$1]::UUID[]
			)
		),
			updated_at = NOW()
		WHERE role = 'admin'
		  AND (group_ids IS NULL OR cardinality(group_ids) = 0)
		  AND EXISTS (SELECT 1 FROM groups WHERE id = $1::UUID)
	`, groupID)
	if err != nil {
		return fmt.Errorf("failed to backfill admin group_ids: %w", err)
	}
	if n := res.RowsAffected(); n > 0 {
		// Route to the stdlib logger (stderr) like every other admin-activity
		// message in this file. fmt.Printf would echo this to stdout, which
		// issue #440 explicitly forbids for admin-account operations.
		log.Printf("Backfilled group_ids for %d admin user(s) to include Administrators group", n)
	}

	// Invariant check: any admin still missing group_ids after the
	// backfill (e.g. EXISTS guard failed because the Administrators
	// group is missing) is logged loudly so operators notice.
	var remaining int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM users
		WHERE role = 'admin'
		  AND (group_ids IS NULL OR cardinality(group_ids) = 0)
	`).Scan(&remaining); err != nil {
		return fmt.Errorf("failed to check admin group_ids invariant: %w", err)
	}
	if remaining > 0 {
		log.Printf("WARN: %d admin user(s) have empty group_ids and the Administrators group could not be assigned. Permissions may not work as expected. Check that migration 000024_seed_default_groups has run successfully.", remaining)
	}
	return nil
}

// maybeForceMigrationVersion inspects CUDLY_FORCE_MIGRATION_VERSION and,
// when set to a non-negative integer, calls m.Force(N) which clears the
// dirty flag and pins the migration version. Used as a one-shot recovery
// path after a partially-applied migration leaves schema_migrations in a
// dirty state.
//
// Operator flow:
//  1. Check the current DB state (e.g. by inspecting actual schema via
//     logs or a one-off job).
//  2. If the dirty migration's SQL effects landed (e.g. the PK exists),
//     set CUDLY_FORCE_MIGRATION_VERSION=N where N is the dirty version —
//     this marks it clean and subsequent Up() picks up from N+1.
//  3. If the dirty migration's effects did NOT land, set
//     CUDLY_FORCE_MIGRATION_VERSION=N-1 — subsequent Up() re-runs N.
//  4. Redeploy / restart so the env var is visible.
//  5. After the first successful run, remove the env var.
//
// A non-numeric value is rejected with a loud error rather than silently
// ignored — forcing a wrong version is destructive, so typos should
// surface immediately.
func maybeForceMigrationVersion(m *migrate.Migrate) error {
	v := os.Getenv("CUDLY_FORCE_MIGRATION_VERSION")
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fmt.Errorf("CUDLY_FORCE_MIGRATION_VERSION must be a non-negative integer, got %q", v)
	}
	log.Printf("CUDLY_FORCE_MIGRATION_VERSION=%d set: forcing migration state (clears dirty flag)", n)
	if err := m.Force(n); err != nil {
		return fmt.Errorf("failed to force migration version to %d: %w", n, err)
	}
	log.Printf("Forced migration state to version %d", n)
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
	log.Printf("Rolling back %d migration(s) from version %d...", steps, currentVersion)

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

	log.Printf("Rolled back %d migration(s) (current version: %d)", steps, version)
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
