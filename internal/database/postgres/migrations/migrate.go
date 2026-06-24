package migrations

import (
	"context"
	"errors"
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
	// Create the migrator and run the pre-Up recovery hooks (operator force,
	// then default-on dirty auto-heal). Kept in a helper so RunMigrations stays
	// under the cyclomatic-complexity budget as recovery paths grow.
	m, err := newMigratorWithRecovery(pool, migrationsPath)
	if err != nil {
		return err
	}
	defer m.Close()

	// Run migrations
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Get current version
	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("failed to get migration version: %w", err)
	}

	if dirty {
		return fmt.Errorf("database is in dirty state at version %d", version)
	}

	log.Printf("Database migrations completed successfully (version: %d)", version)

	// Create admin user if email provided (after migrations complete).
	// Admin-bootstrap failure is non-fatal: the schema migration step
	// succeeded and the app must not be prevented from starting because
	// of a bootstrap-only error (e.g. a stale migrate.go referencing a
	// dropped column). The failure is logged clearly so operators can
	// distinguish it from a migration-step failure (issue #945).
	if adminEmail != "" {
		if err := ensureAdminUser(ctx, pool, adminEmail, adminPassword); err != nil {
			log.Printf("WARNING: admin bootstrap failed (schema migration completed successfully): %v", err)
		}
	}

	return nil
}

// newMigratorWithRecovery builds the golang-migrate migrator for migrationsPath
// and runs the pre-Up recovery hooks in order: the one-shot operator force
// (CUDLY_FORCE_MIGRATION_VERSION) first, then the default-on dirty auto-heal.
// On success it returns a migrator the caller must Close(); on any error it
// closes the migrator itself and returns the error so the caller never sees a
// half-initialized migrator.
//
// Ordering note: maybeAutoHealDirty runs AFTER maybeForceMigrationVersion so an
// explicit force always takes precedence (it pins+cleans first, leaving nothing
// dirty for auto-heal to act on). The auto-heal error propagates so a heal
// failure is recorded (and the app fail-opens in ensureDB) rather than being
// masked by the later dirty check.
func newMigratorWithRecovery(pool *pgxpool.Pool, migrationsPath string) (*migrate.Migrate, error) {
	// Get database connection string from pool config (without admin email parameter - RDS Proxy doesn't support options)
	dsn := buildMigrateDSN(pool.Config(), "")

	m, err := migrate.New(
		fmt.Sprintf("file://%s", migrationsPath),
		dsn,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create migrator: %w", err)
	}

	// One-shot operator recovery: if CUDLY_FORCE_MIGRATION_VERSION is set,
	// call Force(N) before Up(). Clears the dirty flag and pins state to
	// the given version. Used to recover from a partially-applied
	// migration without direct DB access. Remove the env var after the
	// next successful deploy.
	if err := maybeForceMigrationVersion(m); err != nil {
		m.Close()
		return nil, err
	}

	// Default-on dirty auto-heal: when the schema_migrations row is dirty,
	// clear the dirty flag at the CURRENT recorded version so the subsequent
	// Up() re-applies any pending migrations, letting a cold start self-recover
	// instead of staying broken until a manual force.
	if err := maybeAutoHealDirty(m); err != nil {
		m.Close()
		return nil, err
	}

	return m, nil
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
//
// Note: the `role` column was dropped by migration 000057; this INSERT
// intentionally omits it (issue #945).
func ensureAdminUser(ctx context.Context, pool *pgxpool.Pool, email string, password string) error {
	if password != "" {
		return ensureAdminUserWithPassword(ctx, pool, email, password)
	}

	log.Printf("Ensuring admin user exists: %s (user will need to reset password to login)", email)

	// Create admin user with no password - account is inactive until password is set via reset flow.
	// Use ON CONFLICT to prevent race conditions when multiple instances run migrations.
	// group_ids is seeded with the Administrators group so a fresh bootstrap admin
	// has group-based permissions from the start (issue #351).
	// The `role` column was removed by migration 000057 (issue #945).
	result, err := pool.Exec(ctx, `
		INSERT INTO users (
			id, email, password_hash, salt, active, group_ids, created_at, updated_at
		) VALUES (
			gen_random_uuid(), $1, '', '', false, ARRAY[$2]::UUID[], NOW(), NOW()
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

	// Idempotent backfill + invariant check on the bootstrap admin row
	// only. Scoping by email prevents the backfill from touching
	// non-admin users in pre-057/rollback states (issue #945).
	if err := assignAdminGroupAndWarn(ctx, pool, defaultAdminGroupID, email); err != nil {
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
//
// Note: the `role` column was dropped by migration 000057; this INSERT
// intentionally omits it (issue #945).
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
	// The `role` column was removed by migration 000057 (issue #945).
	result, err := pool.Exec(ctx, `
		INSERT INTO users (
			id, email, password_hash, salt, active, group_ids, created_at, updated_at
		) VALUES (
			gen_random_uuid(), $1, $2, '', true, ARRAY[$3]::UUID[], NOW(), NOW()
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

	// Idempotent backfill + invariant check on the bootstrap admin row
	// only. Scoping by email prevents the backfill from touching
	// non-admin users in pre-057/rollback states (issue #945).
	if err := assignAdminGroupAndWarn(ctx, pool, defaultAdminGroupID, email); err != nil {
		return fmt.Errorf("failed to assign admin group: %w", err)
	}
	return nil
}

// assignAdminGroupAndWarn runs an idempotent backfill that appends
// groupID to the bootstrap admin row (identified by adminEmail) when
// its group_ids is empty (NULL or zero-length). Scoping by email
// prevents the backfill from touching non-admin users in pre-057 or
// rollback states. The DISTINCT(unnest(...)) dedupe makes the UPDATE
// safe to run repeatedly. After the backfill, a defensive SELECT
// checks whether the admin row still has empty group_ids and logs a
// WARN so operators see drift in container logs.
//
// Post-migration-000057: the `users_min_one_group` CHECK constraint
// prevents group_ids from being NULL or zero-length, so this backfill
// is a no-op in normal operation. It remains as defence-in-depth for
// pre-057 schemas (rollback scenarios) and any future drift. The `role`
// column was removed by migration 000057 (issue #945) and must not be
// referenced here.
//
// The EXISTS guard on the groups table makes the backfill a no-op
// when migration 000024 hasn't yet seeded the Administrators group -
// defence-in-depth, since in practice this function is invoked
// after RunMigrations -> m.Up() completes.
func assignAdminGroupAndWarn(ctx context.Context, pool *pgxpool.Pool, groupID string, adminEmail string) error {
	res, err := pool.Exec(ctx, `
		UPDATE users
		SET group_ids = ARRAY(
			SELECT DISTINCT unnest(
				COALESCE(group_ids, '{}') || ARRAY[$1]::UUID[]
			)
		),
			updated_at = NOW()
		WHERE email = $2
		  AND (group_ids IS NULL OR cardinality(group_ids) = 0)
		  AND EXISTS (SELECT 1 FROM groups WHERE id = $1::UUID)
	`, groupID, adminEmail)
	if err != nil {
		return fmt.Errorf("failed to backfill admin group_ids: %w", err)
	}
	if n := res.RowsAffected(); n > 0 {
		// Route to the stdlib logger (stderr) like every other admin-activity
		// message in this file. fmt.Printf would echo this to stdout, which
		// issue #440 explicitly forbids for admin-account operations.
		log.Printf("Backfilled group_ids for %d user(s) to include Administrators group", n)
	}

	// Invariant check: if the bootstrap admin row still has empty
	// group_ids after the backfill (e.g. EXISTS guard failed because
	// the Administrators group is missing), log loudly so operators notice.
	// Post-057, the users_min_one_group CHECK means this count is
	// always 0 unless a rollback is in progress.
	var remaining int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM users
		WHERE email = $1
		  AND (group_ids IS NULL OR cardinality(group_ids) = 0)
	`, adminEmail).Scan(&remaining); err != nil {
		return fmt.Errorf("failed to check admin group_ids invariant: %w", err)
	}
	if remaining > 0 {
		log.Printf("WARN: bootstrap admin %s has empty group_ids and the Administrators group could not be assigned. Permissions may not work as expected. Check that migration 000024_seed_default_groups has run successfully.", adminEmail)
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

// maybeAutoHealDirty self-recovers a dirty schema_migrations row: when the DB
// is dirty it calls m.Force(currentRecordedVersion) to clear the dirty flag at
// the version golang-migrate last recorded, and the caller's subsequent m.Up()
// re-applies only the still-pending migrations. This runs ONCE per boot, so a
// cold start that finds a dirty DB self-heals instead of staying broken until
// an operator manually forces a version (the multi-hour outage this addresses).
//
// DEFAULT-ON (not opt-in). The whole outage was "the app started but stayed
// broken for hours needing a manual force", and TestMigrations_FullStackIdempotent
// proves re-running migrations is safe, so auto-heal runs by default. Set the
// escape hatch CUDLY_MIGRATION_AUTOHEAL=false (any strconv.ParseBool falsey
// value) to disable it in an environment whose migrations are not idempotent.
// When disabled, a dirty DB is left untouched here and surfaces as the usual
// "database is in dirty state" error after Up(); the caller (app.go ensureDB)
// still FAIL-OPENS on that error so the app always starts -- a broken schema
// surfaces via /health + the CloudWatch alarm, never via a crash-loop.
//
// CRITICAL -- Force to the CURRENT recorded version, NEVER a lower one. Up()
// then applies only the pending tail. Forcing BELOW already-applied migrations
// would re-run seed/data migrations, and some RAISE on a second run (e.g.
// 000059 errors with "a group named Purchaser already exists with a different
// id" because 000064 already relocated it). Force(current)+Up() is the only
// safe shape; Force(lower) would turn a recoverable dirty state into a hard
// failure. (Proven live during the incident.)
//
// IDEMPOTENCY INVARIANT: Force(version) clears the dirty marker WITHOUT rolling
// back the partial effects of the interrupted migration, so when Up() re-runs
// the pending tail those migrations must tolerate already-applied state
// (CREATE ... IF NOT EXISTS, DROP ... IF EXISTS, DO-blocks that no-op when the
// target already exists). The full-stack idempotency test guards this for the
// whole directory as migrations are added.
//
// CUDLY_FORCE_MIGRATION_VERSION still takes precedence: it runs earlier and
// leaves the row clean, so this is a no-op when both are set. If auto-heal
// itself fails (e.g. Force errors, or the re-applied Up() still fails), the
// error propagates to ensureDB, which fail-opens AND records the failure so
// the migration-failed alarm fires -- the app still starts.
//
// The ParseBool gate is duplicated from internal/server.getEnvBool rather than
// imported because the migrations package must not depend on internal/server,
// which would invert the dependency direction.
func maybeAutoHealDirty(m *migrate.Migrate) error {
	if !autoHealEnabled() {
		return nil
	}

	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		// No migrations recorded yet -> nothing to heal.
		return nil
	}
	if err != nil {
		return fmt.Errorf("auto-heal: failed to read migration version: %w", err)
	}
	if !dirty {
		return nil
	}

	// Force the CURRENT recorded version (never lower -- see the doc comment),
	// then let the caller's Up() re-apply only the pending tail.
	log.Printf("Database is DIRTY at version %d: auto-heal forcing the current version %d to clear the dirty flag, then re-applying pending migrations (set CUDLY_MIGRATION_AUTOHEAL=false to disable)", version, version)
	if err := m.Force(int(version)); err != nil {
		return fmt.Errorf("auto-heal: failed to force version %d to clear dirty flag: %w", version, err)
	}
	log.Printf("Auto-heal cleared dirty flag at version %d; proceeding to re-apply pending migrations", version)
	return nil
}

// autoHealEnabled reports whether dirty auto-heal should run. DEFAULT-ON: it
// returns true unless CUDLY_MIGRATION_AUTOHEAL is explicitly set to a
// strconv.ParseBool falsey value (0/f/false/...). Unset, empty, or unparseable
// values keep auto-heal enabled. Kept local to the migrations package to avoid
// an inverted dependency on internal/server (see maybeAutoHealDirty).
func autoHealEnabled() bool {
	v := os.Getenv("CUDLY_MIGRATION_AUTOHEAL")
	if v == "" {
		return true
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		// Unparseable value -> keep the safe default (enabled) rather than
		// silently disabling self-recovery on a typo.
		return true
	}
	return b
}

// RollbackMigrations rolls back N migrations
// logMigrateVersion reads the current migration version and logs it before a rollback.
// ErrNilVersion (no migrations applied yet) is silently ignored.
func logMigrateVersion(m *migrate.Migrate, steps int) {
	currentVersion, _, verErr := m.Version()
	if verErr != nil && !errors.Is(verErr, migrate.ErrNilVersion) {
		log.Printf("Warning: failed to read current migration version: %v", verErr)
	}
	log.Printf("Rolling back %d migration(s) from version %d...", steps, currentVersion)
}

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

	logMigrateVersion(m, steps)

	// Rollback steps
	if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to rollback migrations: %w", err)
	}

	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("failed to get migration version: %w", err)
	}

	if dirty {
		return fmt.Errorf("database is in dirty state at version %d", version)
	}

	log.Printf("Rolled back %d migration(s) (current version: %d)", steps, version)
	return nil
}

// MigrateToVersion migrates the schema up or down to exactly the given
// version. Unlike RunMigrations it applies no post-migration Go logic
// (admin seeding etc.), and unlike RollbackMigrations it targets a version
// rather than a step count. Migration tests use it to pin the database at
// the version just below the migration under test; fixed step counts from
// head silently drift every time a newer migration lands.
func MigrateToVersion(ctx context.Context, pool *pgxpool.Pool, migrationsPath string, version uint) error {
	dsn := buildMigrateDSN(pool.Config(), "")

	m, err := migrate.New(
		fmt.Sprintf("file://%s", migrationsPath),
		dsn,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}
	defer m.Close()

	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to migrate to version %d: %w", version, err)
	}

	current, dirty, err := m.Version()
	if err != nil {
		return fmt.Errorf("failed to get migration version: %w", err)
	}
	if dirty {
		return fmt.Errorf("database is in dirty state at version %d", current)
	}
	if current != version {
		return fmt.Errorf("expected migration version %d, got %d", version, current)
	}

	log.Printf("Migrated to version %d", current)
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
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
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
