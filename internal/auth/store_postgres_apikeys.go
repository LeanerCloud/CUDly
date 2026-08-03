// PostgresStore's API-key surface, split out of store_postgres.go so that
// file stops growing past the project's 500-line ceiling (CodeRabbit finding
// on PR #1523). Pure move: the queries and scanning behavior are unchanged.

package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ==========================================
// API KEY OPERATIONS
// ==========================================

// CreateAPIKey creates a new API key.
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

// GetAPIKeyByID retrieves an API key by ID.
func (s *PostgresStore) GetAPIKeyByID(ctx context.Context, keyID string) (*UserAPIKey, error) {
	query := `
		SELECT id, user_id, name, key_prefix, key_hash, permissions,
		       is_active, expires_at, created_at, last_used_at,
		       request_count_total, request_count_window, request_count_window_start
		FROM api_keys
		WHERE id = $1
	`

	return s.scanAPIKey(s.db.QueryRow(ctx, query, keyID))
}

// GetAPIKeyByHash retrieves an API key by hash.
func (s *PostgresStore) GetAPIKeyByHash(ctx context.Context, keyHash string) (*UserAPIKey, error) {
	query := `
		SELECT id, user_id, name, key_prefix, key_hash, permissions,
		       is_active, expires_at, created_at, last_used_at,
		       request_count_total, request_count_window, request_count_window_start
		FROM api_keys
		WHERE key_hash = $1 AND is_active = true
		  AND (expires_at IS NULL OR expires_at > NOW())
	`

	return s.scanAPIKey(s.db.QueryRow(ctx, query, keyHash))
}

// ListAPIKeysByUser lists all API keys for a user.
func (s *PostgresStore) ListAPIKeysByUser(ctx context.Context, userID string) ([]*UserAPIKey, error) {
	query := `
		SELECT id, user_id, name, key_prefix, key_hash, permissions,
		       is_active, expires_at, created_at, last_used_at,
		       request_count_total, request_count_window, request_count_window_start
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
		if key == nil {
			continue
		}
		keys = append(keys, key)
	}

	return keys, rows.Err()
}

// UpdateAPIKey updates an API key.
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

// UpdateAPIKeyLastUsed atomically updates the last_used_at timestamp for an
// API key. Retained for backwards compatibility with callers that only need
// the timestamp update -- production code routes through RecordAPIKeyUsage,
// which additionally maintains the usage counters from migration 000094.
func (s *PostgresStore) UpdateAPIKeyLastUsed(ctx context.Context, keyID string) error {
	query := `UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`
	result, err := s.db.Exec(ctx, query, keyID)
	if err != nil {
		return fmt.Errorf("failed to update API key last used: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("API key not found: %s", keyID)
	}
	return nil
}

// RecordAPIKeyUsage atomically updates last_used_at, adds delta to
// request_count_total, and adds-or-resets request_count_window based on
// whether the current window is still within apiKeyUsageWindow of its start.
//
// delta is the number of requests being recorded in this flush. The auth hot
// path coalesces concurrent requests for the same key into a single write
// (see Service.RecordUsageAsync), so delta is frequently greater than 1 and
// must be added rather than treated as a single increment.
//
// request_count_window is a FIXED/TUMBLING window counter, not a true
// trailing-24h rolling count: the single UPDATE does the window decision
// inline --
//   - if window_start IS NULL OR (NOW() - window_start) >= apiKeyUsageWindow,
//     start a fresh window (window_start = NOW(), count = delta) -- this
//     DISCARDS whatever count the previous window had, even if some of those
//     requests happened within the preceding window-length of NOW().
//   - otherwise add delta to the existing count.
//
// The window length is passed in from apiKeyUsageWindow rather than written
// as a SQL literal so the write path and the API read path (which zeroes an
// expired window, see effectiveWindowUsage) cannot drift apart.
//
// An idle key's stale count lingers on the row unchanged until its next
// request; the read path is responsible for not reporting it. Callers that
// need the actual period covered should read request_count_window_start
// rather than assuming "requests in the last 24h".
//
// Doing it in one statement keeps the update atomic per row, so two
// concurrent calls can't both see "stale window" and reset in parallel --
// pgx serializes updates to the same row.
func (s *PostgresStore) RecordAPIKeyUsage(ctx context.Context, keyID string, delta int64) error {
	if delta <= 0 {
		return fmt.Errorf("invalid API key usage delta %d: must be positive", delta)
	}
	query := `
		UPDATE api_keys
		SET last_used_at = NOW(),
		    request_count_total = request_count_total + $2,
		    request_count_window = CASE
		        WHEN request_count_window_start IS NULL
		             OR NOW() - request_count_window_start >= make_interval(secs => $3)
		        THEN $2
		        ELSE request_count_window + $2
		    END,
		    request_count_window_start = CASE
		        WHEN request_count_window_start IS NULL
		             OR NOW() - request_count_window_start >= make_interval(secs => $3)
		        THEN NOW()
		        ELSE request_count_window_start
		    END
		WHERE id = $1
	`
	result, err := s.db.Exec(ctx, query, keyID, delta, apiKeyUsageWindow.Seconds())
	if err != nil {
		return fmt.Errorf("failed to record API key usage: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("API key not found: %s", keyID)
	}
	return nil
}

// DeleteAPIKey deletes an API key.
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

// scanAPIKey scans an API key from a database row.
func (s *PostgresStore) scanAPIKey(scanner Scanner) (*UserAPIKey, error) {
	var key UserAPIKey
	var permissionsJSON []byte
	var expiresAt, lastUsedAt, windowStart sql.NullTime

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
		&key.RequestCountTotal,
		&key.RequestCountWindow,
		&windowStart,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
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
	if windowStart.Valid {
		key.RequestCountWindowStart = &windowStart.Time
	}

	return &key, nil
}
