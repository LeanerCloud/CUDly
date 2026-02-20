package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBConnection defines the interface for database operations needed by PostgresStore
type DBConnection interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Ping(ctx context.Context) error
}

// PostgresStore implements StoreInterface using PostgreSQL
type PostgresStore struct {
	db DBConnection
}

// NewPostgresStore creates a new PostgreSQL-backed auth store
func NewPostgresStore(db DBConnection) *PostgresStore {
	return &PostgresStore{db: db}
}

// Verify PostgresStore implements StoreInterface
var _ StoreInterface = (*PostgresStore)(nil)

// ==========================================
// USER OPERATIONS
// ==========================================

// GetUserByID retrieves a user by ID
func (s *PostgresStore) GetUserByID(ctx context.Context, userID string) (*User, error) {
	query := `
		SELECT id, email, password_hash, salt, role, group_ids, active,
		       mfa_enabled, mfa_secret, password_reset_token, password_reset_expiry,
		       failed_login_attempts, locked_until, password_history,
		       created_at, updated_at, last_login_at
		FROM users
		WHERE id = $1
	`

	return s.scanUser(s.db.QueryRow(ctx, query, userID))
}

// GetUserByEmail retrieves a user by email
func (s *PostgresStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	query := `
		SELECT id, email, password_hash, salt, role, group_ids, active,
		       mfa_enabled, mfa_secret, password_reset_token, password_reset_expiry,
		       failed_login_attempts, locked_until, password_history,
		       created_at, updated_at, last_login_at
		FROM users
		WHERE email = $1
	`

	user, err := s.scanUser(s.db.QueryRow(ctx, query, email))
	if err != nil {
		return nil, err
	}
	return user, nil
}

// CreateUser creates a new user
func (s *PostgresStore) CreateUser(ctx context.Context, user *User) error {
	// Generate UUID if not provided
	if user.ID == "" {
		user.ID = uuid.New().String()
	}

	// Set timestamps
	now := time.Now()
	user.CreatedAt = now
	user.UpdatedAt = now

	query := `
		INSERT INTO users (
			id, email, password_hash, salt, role, group_ids, active,
			mfa_enabled, mfa_secret, password_reset_token, password_reset_expiry,
			failed_login_attempts, locked_until, password_history,
			created_at, updated_at, last_login_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`

	_, err := s.db.Exec(ctx, query,
		user.ID,
		user.Email,
		user.PasswordHash,
		user.Salt,
		user.Role,
		user.GroupIDs,
		user.Active,
		user.MFAEnabled,
		user.MFASecret,
		user.PasswordResetToken,
		user.PasswordResetExpiry,
		user.FailedLoginAttempts,
		user.LockedUntil,
		user.PasswordHistory,
		user.CreatedAt,
		user.UpdatedAt,
		user.LastLoginAt,
	)

	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("email already in use")
		}
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

// isDuplicateKeyError checks if the error is a PostgreSQL unique constraint violation (code 23505)
func isDuplicateKeyError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// UpdateUser updates an existing user
func (s *PostgresStore) UpdateUser(ctx context.Context, user *User) error {
	user.UpdatedAt = time.Now()

	query := `
		UPDATE users SET
			email = $2,
			password_hash = $3,
			salt = $4,
			role = $5,
			group_ids = $6,
			active = $7,
			mfa_enabled = $8,
			mfa_secret = $9,
			password_reset_token = $10,
			password_reset_expiry = $11,
			failed_login_attempts = $12,
			locked_until = $13,
			password_history = $14,
			updated_at = $15,
			last_login_at = $16
		WHERE id = $1
	`

	result, err := s.db.Exec(ctx, query,
		user.ID,
		user.Email,
		user.PasswordHash,
		user.Salt,
		user.Role,
		user.GroupIDs,
		user.Active,
		user.MFAEnabled,
		user.MFASecret,
		user.PasswordResetToken,
		user.PasswordResetExpiry,
		user.FailedLoginAttempts,
		user.LockedUntil,
		user.PasswordHistory,
		user.UpdatedAt,
		user.LastLoginAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("user not found: %s", user.ID)
	}

	return nil
}

// DeleteUser deletes a user
func (s *PostgresStore) DeleteUser(ctx context.Context, userID string) error {
	query := `DELETE FROM users WHERE id = $1`

	result, err := s.db.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("user not found: %s", userID)
	}

	return nil
}

// ListUsers lists all users
func (s *PostgresStore) ListUsers(ctx context.Context) ([]User, error) {
	query := `
		SELECT id, email, password_hash, salt, role, group_ids, active,
		       mfa_enabled, mfa_secret, password_reset_token, password_reset_expiry,
		       failed_login_attempts, locked_until, password_history,
		       created_at, updated_at, last_login_at
		FROM users
		ORDER BY created_at DESC
	`

	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	users := make([]User, 0)
	for rows.Next() {
		user, err := s.scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *user)
	}

	return users, rows.Err()
}

// GetUserByResetToken retrieves a user by password reset token
func (s *PostgresStore) GetUserByResetToken(ctx context.Context, token string) (*User, error) {
	query := `
		SELECT id, email, password_hash, salt, role, group_ids, active,
		       mfa_enabled, mfa_secret, password_reset_token, password_reset_expiry,
		       failed_login_attempts, locked_until, password_history,
		       created_at, updated_at, last_login_at
		FROM users
		WHERE password_reset_token = $1
		  AND password_reset_expiry > NOW()
	`

	return s.scanUser(s.db.QueryRow(ctx, query, token))
}

// AdminExists checks if any admin user exists
func (s *PostgresStore) AdminExists(ctx context.Context) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM users WHERE role = 'admin' AND active = true)`

	var exists bool
	err := s.db.QueryRow(ctx, query).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check admin existence: %w", err)
	}

	return exists, nil
}

// ==========================================
// GROUP OPERATIONS
// ==========================================

// GetGroup retrieves a group by ID
func (s *PostgresStore) GetGroup(ctx context.Context, groupID string) (*Group, error) {
	query := `
		SELECT id, name, description, permissions, allowed_accounts,
		       created_at, updated_at, created_by
		FROM groups
		WHERE id = $1
	`

	return s.scanGroup(s.db.QueryRow(ctx, query, groupID))
}

// CreateGroup creates a new group
func (s *PostgresStore) CreateGroup(ctx context.Context, group *Group) error {
	// Generate UUID if not provided
	if group.ID == "" {
		group.ID = uuid.New().String()
	}

	// Set timestamps
	now := time.Now()
	group.CreatedAt = now
	group.UpdatedAt = now

	// Marshal permissions to JSONB
	permissionsJSON, err := json.Marshal(group.Permissions)
	if err != nil {
		return fmt.Errorf("failed to marshal permissions: %w", err)
	}

	query := `
		INSERT INTO groups (
			id, name, description, permissions, allowed_accounts,
			created_at, updated_at, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err = s.db.Exec(ctx, query,
		group.ID,
		group.Name,
		group.Description,
		permissionsJSON,
		group.AllowedAccounts,
		group.CreatedAt,
		group.UpdatedAt,
		group.CreatedBy,
	)

	if err != nil {
		return fmt.Errorf("failed to create group: %w", err)
	}

	return nil
}

// UpdateGroup updates an existing group
func (s *PostgresStore) UpdateGroup(ctx context.Context, group *Group) error {
	group.UpdatedAt = time.Now()

	// Marshal permissions to JSONB
	permissionsJSON, err := json.Marshal(group.Permissions)
	if err != nil {
		return fmt.Errorf("failed to marshal permissions: %w", err)
	}

	query := `
		UPDATE groups SET
			name = $2,
			description = $3,
			permissions = $4,
			allowed_accounts = $5,
			updated_at = $6
		WHERE id = $1
	`

	result, err := s.db.Exec(ctx, query,
		group.ID,
		group.Name,
		group.Description,
		permissionsJSON,
		group.AllowedAccounts,
		group.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update group: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("group not found: %s", group.ID)
	}

	return nil
}

// DeleteGroup deletes a group
func (s *PostgresStore) DeleteGroup(ctx context.Context, groupID string) error {
	query := `DELETE FROM groups WHERE id = $1`

	result, err := s.db.Exec(ctx, query, groupID)
	if err != nil {
		return fmt.Errorf("failed to delete group: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("group not found: %s", groupID)
	}

	return nil
}

// ListGroups lists all groups
func (s *PostgresStore) ListGroups(ctx context.Context) ([]Group, error) {
	query := `
		SELECT id, name, description, permissions, allowed_accounts,
		       created_at, updated_at, created_by
		FROM groups
		ORDER BY created_at DESC
	`

	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list groups: %w", err)
	}
	defer rows.Close()

	groups := make([]Group, 0)
	for rows.Next() {
		group, err := s.scanGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, *group)
	}

	return groups, rows.Err()
}

// ==========================================
// SESSION OPERATIONS
// ==========================================

// CreateSession creates a new session
func (s *PostgresStore) CreateSession(ctx context.Context, session *Session) error {
	query := `
		INSERT INTO sessions (
			token, user_id, email, role, expires_at, created_at,
			user_agent, ip_address, csrf_token
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := s.db.Exec(ctx, query,
		session.Token,
		session.UserID,
		session.Email,
		session.Role,
		session.ExpiresAt,
		session.CreatedAt,
		session.UserAgent,
		session.IPAddress,
		session.CSRFToken,
	)

	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	return nil
}

// GetSession retrieves a session by token
func (s *PostgresStore) GetSession(ctx context.Context, token string) (*Session, error) {
	query := `
		SELECT token, user_id, email, role, expires_at, created_at,
		       user_agent, ip_address, csrf_token
		FROM sessions
		WHERE token = $1 AND expires_at > NOW()
	`

	var session Session
	err := s.db.QueryRow(ctx, query, token).Scan(
		&session.Token,
		&session.UserID,
		&session.Email,
		&session.Role,
		&session.ExpiresAt,
		&session.CreatedAt,
		&session.UserAgent,
		&session.IPAddress,
		&session.CSRFToken,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("session not found or expired")
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	return &session, nil
}

// DeleteSession deletes a session
func (s *PostgresStore) DeleteSession(ctx context.Context, token string) error {
	query := `DELETE FROM sessions WHERE token = $1`

	_, err := s.db.Exec(ctx, query, token)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	return nil
}

// DeleteUserSessions deletes all sessions for a user
func (s *PostgresStore) DeleteUserSessions(ctx context.Context, userID string) error {
	query := `DELETE FROM sessions WHERE user_id = $1`

	_, err := s.db.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to delete user sessions: %w", err)
	}

	return nil
}

// CleanupExpiredSessions deletes expired sessions
func (s *PostgresStore) CleanupExpiredSessions(ctx context.Context) error {
	query := `DELETE FROM sessions WHERE expires_at <= NOW()`

	result, err := s.db.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to cleanup expired sessions: %w", err)
	}

	logging.Debugf("Cleaned up %d expired sessions", result.RowsAffected())
	return nil
}

// ==========================================
// API KEY OPERATIONS
// ==========================================

// CreateAPIKey creates a new API key
func (s *PostgresStore) CreateAPIKey(ctx context.Context, key *UserAPIKey) error {
	// Generate UUID if not provided
	if key.ID == "" {
		key.ID = uuid.New().String()
	}

	// Set timestamps
	key.CreatedAt = time.Now()

	// Marshal permissions to JSONB
	permissionsJSON, err := json.Marshal(key.Permissions)
	if err != nil {
		return fmt.Errorf("failed to marshal permissions: %w", err)
	}

	query := `
		INSERT INTO api_keys (
			id, user_id, name, key_prefix, key_hash, permissions,
			is_active, expires_at, created_at, last_used_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err = s.db.Exec(ctx, query,
		key.ID,
		key.UserID,
		key.Name,
		key.KeyPrefix,
		key.KeyHash,
		permissionsJSON,
		key.IsActive,
		key.ExpiresAt,
		key.CreatedAt,
		key.LastUsedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create API key: %w", err)
	}

	return nil
}

// GetAPIKeyByID retrieves an API key by ID
func (s *PostgresStore) GetAPIKeyByID(ctx context.Context, keyID string) (*UserAPIKey, error) {
	query := `
		SELECT id, user_id, name, key_prefix, key_hash, permissions,
		       is_active, expires_at, created_at, last_used_at
		FROM api_keys
		WHERE id = $1
	`

	return s.scanAPIKey(s.db.QueryRow(ctx, query, keyID))
}

// GetAPIKeyByHash retrieves an API key by hash
func (s *PostgresStore) GetAPIKeyByHash(ctx context.Context, keyHash string) (*UserAPIKey, error) {
	query := `
		SELECT id, user_id, name, key_prefix, key_hash, permissions,
		       is_active, expires_at, created_at, last_used_at
		FROM api_keys
		WHERE key_hash = $1 AND is_active = true
		  AND (expires_at IS NULL OR expires_at > NOW())
	`

	return s.scanAPIKey(s.db.QueryRow(ctx, query, keyHash))
}

// ListAPIKeysByUser lists all API keys for a user
func (s *PostgresStore) ListAPIKeysByUser(ctx context.Context, userID string) ([]*UserAPIKey, error) {
	query := `
		SELECT id, user_id, name, key_prefix, key_hash, permissions,
		       is_active, expires_at, created_at, last_used_at
		FROM api_keys
		WHERE user_id = $1
		ORDER BY created_at DESC
	`

	rows, err := s.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list API keys: %w", err)
	}
	defer rows.Close()

	keys := make([]*UserAPIKey, 0)
	for rows.Next() {
		key, err := s.scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}

	return keys, rows.Err()
}

// UpdateAPIKey updates an API key
func (s *PostgresStore) UpdateAPIKey(ctx context.Context, key *UserAPIKey) error {
	// Marshal permissions to JSONB
	permissionsJSON, err := json.Marshal(key.Permissions)
	if err != nil {
		return fmt.Errorf("failed to marshal permissions: %w", err)
	}

	query := `
		UPDATE api_keys SET
			name = $2,
			permissions = $3,
			is_active = $4,
			expires_at = $5,
			last_used_at = $6
		WHERE id = $1
	`

	result, err := s.db.Exec(ctx, query,
		key.ID,
		key.Name,
		permissionsJSON,
		key.IsActive,
		key.ExpiresAt,
		key.LastUsedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update API key: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("API key not found: %s", key.ID)
	}

	return nil
}

// UpdateAPIKeyLastUsed atomically updates the last_used_at timestamp for an API key
func (s *PostgresStore) UpdateAPIKeyLastUsed(ctx context.Context, keyID string) error {
	query := `UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, query, keyID)
	if err != nil {
		return fmt.Errorf("failed to update API key last used: %w", err)
	}
	return nil
}

// DeleteAPIKey deletes an API key
func (s *PostgresStore) DeleteAPIKey(ctx context.Context, keyID string) error {
	query := `DELETE FROM api_keys WHERE id = $1`

	result, err := s.db.Exec(ctx, query, keyID)
	if err != nil {
		return fmt.Errorf("failed to delete API key: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("API key not found: %s", keyID)
	}

	return nil
}

// ==========================================
// HELPER FUNCTIONS
// ==========================================

// Scanner interface for both Row and Rows
type Scanner interface {
	Scan(dest ...any) error
}

// scanUser scans a user from a database row
func (s *PostgresStore) scanUser(scanner Scanner) (*User, error) {
	var user User
	var groupIDs []string
	var passwordHistory []string
	var resetExpiry, lockedUntil, lastLoginAt sql.NullTime
	var mfaSecret, resetToken sql.NullString

	err := scanner.Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.Salt,
		&user.Role,
		&groupIDs,
		&user.Active,
		&user.MFAEnabled,
		&mfaSecret,
		&resetToken,
		&resetExpiry,
		&user.FailedLoginAttempts,
		&lockedUntil,
		&passwordHistory,
		&user.CreatedAt,
		&user.UpdatedAt,
		&lastLoginAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan user: %w", err)
	}

	user.GroupIDs = groupIDs
	user.PasswordHistory = passwordHistory

	// Handle nullable strings
	if mfaSecret.Valid {
		user.MFASecret = mfaSecret.String
	}
	if resetToken.Valid {
		user.PasswordResetToken = resetToken.String
	}

	// Handle nullable timestamps
	if resetExpiry.Valid {
		user.PasswordResetExpiry = &resetExpiry.Time
	}
	if lockedUntil.Valid {
		user.LockedUntil = &lockedUntil.Time
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}

	return &user, nil
}

// scanGroup scans a group from a database row
func (s *PostgresStore) scanGroup(scanner Scanner) (*Group, error) {
	var group Group
	var permissionsJSON []byte
	var allowedAccounts []string
	var createdBy sql.NullString

	err := scanner.Scan(
		&group.ID,
		&group.Name,
		&group.Description,
		&permissionsJSON,
		&allowedAccounts,
		&group.CreatedAt,
		&group.UpdatedAt,
		&createdBy,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan group: %w", err)
	}

	// Unmarshal permissions
	if err := json.Unmarshal(permissionsJSON, &group.Permissions); err != nil {
		return nil, fmt.Errorf("failed to unmarshal permissions: %w", err)
	}

	group.AllowedAccounts = allowedAccounts

	if createdBy.Valid {
		group.CreatedBy = createdBy.String
	}

	return &group, nil
}

// scanAPIKey scans an API key from a database row
func (s *PostgresStore) scanAPIKey(scanner Scanner) (*UserAPIKey, error) {
	var key UserAPIKey
	var permissionsJSON []byte
	var expiresAt, lastUsedAt sql.NullTime

	err := scanner.Scan(
		&key.ID,
		&key.UserID,
		&key.Name,
		&key.KeyPrefix,
		&key.KeyHash,
		&permissionsJSON,
		&key.IsActive,
		&expiresAt,
		&key.CreatedAt,
		&lastUsedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan API key: %w", err)
	}

	// Unmarshal permissions
	if len(permissionsJSON) > 0 {
		if err := json.Unmarshal(permissionsJSON, &key.Permissions); err != nil {
			return nil, fmt.Errorf("failed to unmarshal permissions: %w", err)
		}
	}

	// Handle nullable timestamps
	if expiresAt.Valid {
		key.ExpiresAt = &expiresAt.Time
	}
	if lastUsedAt.Valid {
		key.LastUsedAt = &lastUsedAt.Time
	}

	return &key, nil
}

// Ping checks the database connection health
func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.db.Ping(ctx)
}
