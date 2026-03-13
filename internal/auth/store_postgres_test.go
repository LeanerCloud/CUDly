package auth

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockDBConnection mocks database.Connection
type MockDBConnection struct {
	mock.Mock
}

func (m *MockDBConnection) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	mockArgs := m.Called(ctx, sql, args)
	return mockArgs.Get(0).(pgx.Row)
}

func (m *MockDBConnection) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	mockArgs := m.Called(ctx, sql, args)
	if mockArgs.Get(0) == nil {
		return nil, mockArgs.Error(1)
	}
	return mockArgs.Get(0).(pgx.Rows), mockArgs.Error(1)
}

func (m *MockDBConnection) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	mockArgs := m.Called(ctx, sql, args)
	return mockArgs.Get(0).(pgconn.CommandTag), mockArgs.Error(1)
}

func (m *MockDBConnection) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// MockRow mocks pgx.Row
type MockRow struct {
	mock.Mock
	scanFunc func(dest ...interface{}) error
}

func (m *MockRow) Scan(dest ...interface{}) error {
	if m.scanFunc != nil {
		return m.scanFunc(dest...)
	}
	args := m.Called(dest)
	return args.Error(0)
}

// Helper function to create a mock row that returns a user
// The scan order matches scanUser() in store_postgres.go:
// id, email, password_hash, salt, role, group_ids, active,
// mfa_enabled, mfa_secret (NullString), reset_token (NullString), reset_expiry (NullTime),
// failed_login_attempts, locked_until (NullTime), password_history,
// created_at, updated_at, last_login_at (NullTime)
func createMockRowWithUser(user *User) *MockRow {
	return &MockRow{
		scanFunc: func(dest ...interface{}) error {
			// Populate destination pointers with user data
			if len(dest) >= 17 {
				*dest[0].(*string) = user.ID
				*dest[1].(*string) = user.Email
				*dest[2].(*string) = user.PasswordHash
				*dest[3].(*string) = user.Salt
				*dest[4].(*string) = user.Role
				*dest[5].(*[]string) = user.GroupIDs
				*dest[6].(*bool) = user.Active
				*dest[7].(*bool) = user.MFAEnabled
				// dest[8] is sql.NullString for MFASecret
				if user.MFASecret != "" {
					*dest[8].(*sql.NullString) = sql.NullString{String: user.MFASecret, Valid: true}
				} else {
					*dest[8].(*sql.NullString) = sql.NullString{Valid: false}
				}
				// dest[9] is sql.NullString for PasswordResetToken
				if user.PasswordResetToken != "" {
					*dest[9].(*sql.NullString) = sql.NullString{String: user.PasswordResetToken, Valid: true}
				} else {
					*dest[9].(*sql.NullString) = sql.NullString{Valid: false}
				}
				// dest[10] is sql.NullTime for PasswordResetExpiry
				if user.PasswordResetExpiry != nil {
					*dest[10].(*sql.NullTime) = sql.NullTime{Time: *user.PasswordResetExpiry, Valid: true}
				} else {
					*dest[10].(*sql.NullTime) = sql.NullTime{Valid: false}
				}
				*dest[11].(*int) = user.FailedLoginAttempts
				// dest[12] is sql.NullTime for LockedUntil
				if user.LockedUntil != nil {
					*dest[12].(*sql.NullTime) = sql.NullTime{Time: *user.LockedUntil, Valid: true}
				} else {
					*dest[12].(*sql.NullTime) = sql.NullTime{Valid: false}
				}
				*dest[13].(*[]string) = user.PasswordHistory
				*dest[14].(*time.Time) = user.CreatedAt
				*dest[15].(*time.Time) = user.UpdatedAt
				// dest[16] is sql.NullTime for LastLoginAt
				if user.LastLoginAt != nil {
					*dest[16].(*sql.NullTime) = sql.NullTime{Time: *user.LastLoginAt, Valid: true}
				} else {
					*dest[16].(*sql.NullTime) = sql.NullTime{Valid: false}
				}
			}
			return nil
		},
	}
}

// Helper function to create a mock row that returns an error
func createMockRowWithError(err error) *MockRow {
	return &MockRow{
		scanFunc: func(dest ...interface{}) error {
			return err
		},
	}
}

func TestPostgresStore_GetUserByID(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully get user by ID", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		expectedUser := &User{
			ID:       "user-123",
			Email:    "test@example.com",
			Role:     RoleUser,
			Active:   true,
			GroupIDs: []string{},
		}

		mockRow := createMockRowWithUser(expectedUser)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		user, err := store.GetUserByID(ctx, "user-123")
		require.NoError(t, err)
		assert.Equal(t, "user-123", user.ID)
		assert.Equal(t, "test@example.com", user.Email)

		mockDB.AssertExpectations(t)
	})

	t.Run("return ErrNoRows when user not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockRow := createMockRowWithError(pgx.ErrNoRows)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		user, err := store.GetUserByID(ctx, "nonexistent")
		assert.ErrorIs(t, err, pgx.ErrNoRows)
		assert.Nil(t, user)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_GetUserByEmail(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully get user by email", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		expectedUser := &User{
			ID:       "user-123",
			Email:    "test@example.com",
			Role:     RoleUser,
			Active:   true,
			GroupIDs: []string{},
		}

		mockRow := createMockRowWithUser(expectedUser)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		user, err := store.GetUserByEmail(ctx, "test@example.com")
		require.NoError(t, err)
		assert.Equal(t, "test@example.com", user.Email)

		mockDB.AssertExpectations(t)
	})

	t.Run("return ErrNoRows when user not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockRow := createMockRowWithError(pgx.ErrNoRows)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		user, err := store.GetUserByEmail(ctx, "nonexistent@example.com")
		assert.ErrorIs(t, err, pgx.ErrNoRows)
		assert.Nil(t, user)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_CreateUser(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully create user", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		user := &User{
			Email:        "new@example.com",
			PasswordHash: "hash123",
			Role:         RoleUser,
			Active:       true,
			GroupIDs:     []string{},
		}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, nil)

		err := store.CreateUser(ctx, user)
		require.NoError(t, err)
		assert.NotEmpty(t, user.ID)
		assert.False(t, user.CreatedAt.IsZero())

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when insert fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		user := &User{
			Email:    "new@example.com",
			Role:     RoleUser,
			GroupIDs: []string{},
		}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("insert failed"))

		err := store.CreateUser(ctx, user)
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_UpdateUser(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully update user", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		user := &User{
			ID:       "user-123",
			Email:    "updated@example.com",
			Role:     RoleAdmin,
			GroupIDs: []string{},
		}

		// Create a CommandTag that indicates 1 row was affected
		tag := pgconn.NewCommandTag("UPDATE 1")
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

		err := store.UpdateUser(ctx, user)
		require.NoError(t, err)
		assert.False(t, user.UpdatedAt.IsZero())

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when update fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{},
		}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("update failed"))

		err := store.UpdateUser(ctx, user)
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_DeleteUser(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully delete user", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		// Create a CommandTag that indicates 1 row was affected
		tag := pgconn.NewCommandTag("DELETE 1")
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

		err := store.DeleteUser(ctx, "user-123")
		require.NoError(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when delete fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("delete failed"))

		err := store.DeleteUser(ctx, "user-123")
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when user not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		// Return 0 rows affected
		tag := pgconn.NewCommandTag("DELETE 0")
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

		err := store.DeleteUser(ctx, "nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user not found")

		mockDB.AssertExpectations(t)
	})
}

func TestNewPostgresStore(t *testing.T) {
	mockDB := new(MockDBConnection)
	store := NewPostgresStore(mockDB)

	assert.NotNil(t, store)
	assert.Equal(t, mockDB, store.db)
}

func TestPostgresStore_GetUserByResetToken(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully get user by reset token", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		resetExpiry := time.Now().Add(time.Hour)
		expectedUser := &User{
			ID:                  "user-123",
			Email:               "test@example.com",
			Role:                RoleUser,
			Active:              true,
			GroupIDs:            []string{},
			PasswordResetToken:  "reset-token-123",
			PasswordResetExpiry: &resetExpiry,
		}

		mockRow := createMockRowWithUser(expectedUser)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		user, err := store.GetUserByResetToken(ctx, "reset-token-123")
		require.NoError(t, err)
		assert.Equal(t, "user-123", user.ID)
		assert.Equal(t, "reset-token-123", user.PasswordResetToken)

		mockDB.AssertExpectations(t)
	})

	t.Run("return ErrNoRows when token not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockRow := createMockRowWithError(pgx.ErrNoRows)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		user, err := store.GetUserByResetToken(ctx, "invalid-token")
		assert.ErrorIs(t, err, pgx.ErrNoRows)
		assert.Nil(t, user)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_AdminExists(t *testing.T) {
	ctx := context.Background()

	t.Run("return true when admin exists", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockRow := &MockRow{
			scanFunc: func(dest ...interface{}) error {
				*dest[0].(*bool) = true
				return nil
			},
		}
		// AdminExists calls QueryRow without extra args, so args slice is empty
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), []interface{}(nil)).Return(mockRow)

		exists, err := store.AdminExists(ctx)
		require.NoError(t, err)
		assert.True(t, exists)

		mockDB.AssertExpectations(t)
	})

	t.Run("return false when no admin exists", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockRow := &MockRow{
			scanFunc: func(dest ...interface{}) error {
				*dest[0].(*bool) = false
				return nil
			},
		}
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), []interface{}(nil)).Return(mockRow)

		exists, err := store.AdminExists(ctx)
		require.NoError(t, err)
		assert.False(t, exists)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error on database failure", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockRow := createMockRowWithError(fmt.Errorf("database error"))
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), []interface{}(nil)).Return(mockRow)

		exists, err := store.AdminExists(ctx)
		assert.Error(t, err)
		assert.False(t, exists)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_UpdateUser_NotFound(t *testing.T) {
	ctx := context.Background()

	mockDB := new(MockDBConnection)
	store := &PostgresStore{db: mockDB}

	user := &User{
		ID:       "nonexistent",
		Email:    "test@example.com",
		GroupIDs: []string{},
	}

	// Return 0 rows affected
	tag := pgconn.NewCommandTag("UPDATE 0")
	mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

	err := store.UpdateUser(ctx, user)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "user not found")

	mockDB.AssertExpectations(t)
}

// ==========================================
// SESSION TESTS
// ==========================================

func TestPostgresStore_CreateSession(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully create session", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		session := &Session{
			Token:     "session-token-123",
			UserID:    "user-123",
			Email:     "test@example.com",
			Role:      RoleUser,
			ExpiresAt: time.Now().Add(24 * time.Hour),
			CreatedAt: time.Now(),
			UserAgent: "Mozilla/5.0",
			IPAddress: "192.168.1.1",
			CSRFToken: "csrf-token-123",
		}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, nil)

		err := store.CreateSession(ctx, session)
		require.NoError(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when insert fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		session := &Session{
			Token:  "session-token-123",
			UserID: "user-123",
		}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("insert failed"))

		err := store.CreateSession(ctx, session)
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_GetSession(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully get session", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		expectedSession := &Session{
			Token:     "session-token-123",
			UserID:    "user-123",
			Email:     "test@example.com",
			Role:      RoleUser,
			ExpiresAt: time.Now().Add(24 * time.Hour),
			CreatedAt: time.Now(),
			UserAgent: "Mozilla/5.0",
			IPAddress: "192.168.1.1",
			CSRFToken: "csrf-token-123",
		}

		mockRow := &MockRow{
			scanFunc: func(dest ...interface{}) error {
				*dest[0].(*string) = expectedSession.Token
				*dest[1].(*string) = expectedSession.UserID
				*dest[2].(*string) = expectedSession.Email
				*dest[3].(*string) = expectedSession.Role
				*dest[4].(*time.Time) = expectedSession.ExpiresAt
				*dest[5].(*time.Time) = expectedSession.CreatedAt
				*dest[6].(*string) = expectedSession.UserAgent
				*dest[7].(*string) = expectedSession.IPAddress
				*dest[8].(*string) = expectedSession.CSRFToken
				return nil
			},
		}
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		session, err := store.GetSession(ctx, "session-token-123")
		require.NoError(t, err)
		assert.Equal(t, "session-token-123", session.Token)
		assert.Equal(t, "user-123", session.UserID)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when session not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockRow := createMockRowWithError(pgx.ErrNoRows)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		session, err := store.GetSession(ctx, "nonexistent")
		assert.Error(t, err)
		assert.Nil(t, session)
		assert.Contains(t, err.Error(), "session not found or expired")

		mockDB.AssertExpectations(t)
	})

	t.Run("return error on database failure", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockRow := createMockRowWithError(fmt.Errorf("database error"))
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		session, err := store.GetSession(ctx, "token")
		assert.Error(t, err)
		assert.Nil(t, session)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_DeleteSession(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully delete session", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, nil)

		err := store.DeleteSession(ctx, "session-token-123")
		require.NoError(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when delete fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("delete failed"))

		err := store.DeleteSession(ctx, "session-token-123")
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_DeleteUserSessions(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully delete user sessions", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, nil)

		err := store.DeleteUserSessions(ctx, "user-123")
		require.NoError(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when delete fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("delete failed"))

		err := store.DeleteUserSessions(ctx, "user-123")
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_CleanupExpiredSessions(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully cleanup expired sessions", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		tag := pgconn.NewCommandTag("DELETE 5")
		// CleanupExpiredSessions calls Exec without extra args
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), []interface{}(nil)).Return(tag, nil)

		err := store.CleanupExpiredSessions(ctx)
		require.NoError(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when cleanup fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), []interface{}(nil)).Return(pgconn.CommandTag{}, fmt.Errorf("cleanup failed"))

		err := store.CleanupExpiredSessions(ctx)
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})
}

// ==========================================
// GROUP TESTS
// ==========================================

// Helper function to create a mock row that returns a group
func createMockRowWithGroup(group *Group) *MockRow {
	return &MockRow{
		scanFunc: func(dest ...interface{}) error {
			if len(dest) >= 8 {
				*dest[0].(*string) = group.ID
				*dest[1].(*string) = group.Name
				*dest[2].(*string) = group.Description
				// dest[3] is permissions JSON
				*dest[3].(*[]byte) = []byte(`[]`)
				*dest[4].(*[]string) = group.AllowedAccounts
				*dest[5].(*time.Time) = group.CreatedAt
				*dest[6].(*time.Time) = group.UpdatedAt
				// dest[7] is sql.NullString for CreatedBy
				if group.CreatedBy != "" {
					*dest[7].(*sql.NullString) = sql.NullString{String: group.CreatedBy, Valid: true}
				} else {
					*dest[7].(*sql.NullString) = sql.NullString{Valid: false}
				}
			}
			return nil
		},
	}
}

func TestPostgresStore_GetGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully get group", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		expectedGroup := &Group{
			ID:              "group-123",
			Name:            "Test Group",
			Description:     "A test group",
			AllowedAccounts: []string{"account-1"},
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
			CreatedBy:       "admin-user",
		}

		mockRow := createMockRowWithGroup(expectedGroup)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		group, err := store.GetGroup(ctx, "group-123")
		require.NoError(t, err)
		assert.Equal(t, "group-123", group.ID)
		assert.Equal(t, "Test Group", group.Name)

		mockDB.AssertExpectations(t)
	})

	t.Run("return ErrNoRows when group not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockRow := createMockRowWithError(pgx.ErrNoRows)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		group, err := store.GetGroup(ctx, "nonexistent")
		assert.ErrorIs(t, err, pgx.ErrNoRows)
		assert.Nil(t, group)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_CreateGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully create group", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		group := &Group{
			Name:            "New Group",
			Description:     "A new group",
			Permissions:     []Permission{},
			AllowedAccounts: []string{},
		}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, nil)

		err := store.CreateGroup(ctx, group)
		require.NoError(t, err)
		assert.NotEmpty(t, group.ID)
		assert.False(t, group.CreatedAt.IsZero())

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when insert fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		group := &Group{
			Name:        "New Group",
			Permissions: []Permission{},
		}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("insert failed"))

		err := store.CreateGroup(ctx, group)
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_UpdateGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully update group", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		group := &Group{
			ID:          "group-123",
			Name:        "Updated Group",
			Permissions: []Permission{},
		}

		tag := pgconn.NewCommandTag("UPDATE 1")
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

		err := store.UpdateGroup(ctx, group)
		require.NoError(t, err)
		assert.False(t, group.UpdatedAt.IsZero())

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when update fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		group := &Group{
			ID:          "group-123",
			Permissions: []Permission{},
		}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("update failed"))

		err := store.UpdateGroup(ctx, group)
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when group not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		group := &Group{
			ID:          "nonexistent",
			Permissions: []Permission{},
		}

		tag := pgconn.NewCommandTag("UPDATE 0")
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

		err := store.UpdateGroup(ctx, group)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "group not found")

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_DeleteGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully delete group", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		tag := pgconn.NewCommandTag("DELETE 1")
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

		err := store.DeleteGroup(ctx, "group-123")
		require.NoError(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when delete fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("delete failed"))

		err := store.DeleteGroup(ctx, "group-123")
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when group not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		tag := pgconn.NewCommandTag("DELETE 0")
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

		err := store.DeleteGroup(ctx, "nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "group not found")

		mockDB.AssertExpectations(t)
	})
}

// ==========================================
// API KEY TESTS
// ==========================================

// Helper function to create a mock row that returns an API key
func createMockRowWithAPIKey(key *UserAPIKey) *MockRow {
	return &MockRow{
		scanFunc: func(dest ...interface{}) error {
			if len(dest) >= 10 {
				*dest[0].(*string) = key.ID
				*dest[1].(*string) = key.UserID
				*dest[2].(*string) = key.Name
				*dest[3].(*string) = key.KeyPrefix
				*dest[4].(*string) = key.KeyHash
				// dest[5] is permissions JSON
				*dest[5].(*[]byte) = []byte(`[]`)
				*dest[6].(*bool) = key.IsActive
				// dest[7] is sql.NullTime for ExpiresAt
				if key.ExpiresAt != nil {
					*dest[7].(*sql.NullTime) = sql.NullTime{Time: *key.ExpiresAt, Valid: true}
				} else {
					*dest[7].(*sql.NullTime) = sql.NullTime{Valid: false}
				}
				*dest[8].(*time.Time) = key.CreatedAt
				// dest[9] is sql.NullTime for LastUsedAt
				if key.LastUsedAt != nil {
					*dest[9].(*sql.NullTime) = sql.NullTime{Time: *key.LastUsedAt, Valid: true}
				} else {
					*dest[9].(*sql.NullTime) = sql.NullTime{Valid: false}
				}
			}
			return nil
		},
	}
}

func TestPostgresStore_CreateAPIKey(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully create API key", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		key := &UserAPIKey{
			UserID:      "user-123",
			Name:        "Test Key",
			KeyPrefix:   "cudly_ab",
			KeyHash:     "hash123",
			Permissions: []Permission{},
			IsActive:    true,
		}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, nil)

		err := store.CreateAPIKey(ctx, key)
		require.NoError(t, err)
		assert.NotEmpty(t, key.ID)
		assert.False(t, key.CreatedAt.IsZero())

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when insert fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		key := &UserAPIKey{
			UserID:      "user-123",
			Permissions: []Permission{},
		}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("insert failed"))

		err := store.CreateAPIKey(ctx, key)
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_GetAPIKeyByID(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully get API key by ID", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		expectedKey := &UserAPIKey{
			ID:        "key-123",
			UserID:    "user-123",
			Name:      "Test Key",
			KeyPrefix: "cudly_ab",
			KeyHash:   "hash123",
			IsActive:  true,
			CreatedAt: time.Now(),
		}

		mockRow := createMockRowWithAPIKey(expectedKey)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		key, err := store.GetAPIKeyByID(ctx, "key-123")
		require.NoError(t, err)
		assert.Equal(t, "key-123", key.ID)
		assert.Equal(t, "user-123", key.UserID)

		mockDB.AssertExpectations(t)
	})

	t.Run("return ErrNoRows when key not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockRow := createMockRowWithError(pgx.ErrNoRows)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		key, err := store.GetAPIKeyByID(ctx, "nonexistent")
		assert.ErrorIs(t, err, pgx.ErrNoRows)
		assert.Nil(t, key)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_GetAPIKeyByHash(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully get API key by hash", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		expectedKey := &UserAPIKey{
			ID:        "key-123",
			UserID:    "user-123",
			Name:      "Test Key",
			KeyPrefix: "cudly_ab",
			KeyHash:   "hash123",
			IsActive:  true,
			CreatedAt: time.Now(),
		}

		mockRow := createMockRowWithAPIKey(expectedKey)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		key, err := store.GetAPIKeyByHash(ctx, "hash123")
		require.NoError(t, err)
		assert.Equal(t, "key-123", key.ID)
		assert.Equal(t, "hash123", key.KeyHash)

		mockDB.AssertExpectations(t)
	})

	t.Run("return ErrNoRows when key not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockRow := createMockRowWithError(pgx.ErrNoRows)
		mockDB.On("QueryRow", ctx, mock.AnythingOfType("string"), mock.Anything).Return(mockRow)

		key, err := store.GetAPIKeyByHash(ctx, "nonexistent")
		assert.ErrorIs(t, err, pgx.ErrNoRows)
		assert.Nil(t, key)

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_UpdateAPIKey(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully update API key", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		key := &UserAPIKey{
			ID:          "key-123",
			Name:        "Updated Key",
			Permissions: []Permission{},
			IsActive:    true,
		}

		tag := pgconn.NewCommandTag("UPDATE 1")
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

		err := store.UpdateAPIKey(ctx, key)
		require.NoError(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when update fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		key := &UserAPIKey{
			ID:          "key-123",
			Permissions: []Permission{},
		}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("update failed"))

		err := store.UpdateAPIKey(ctx, key)
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when key not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		key := &UserAPIKey{
			ID:          "nonexistent",
			Permissions: []Permission{},
		}

		tag := pgconn.NewCommandTag("UPDATE 0")
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

		err := store.UpdateAPIKey(ctx, key)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "API key not found")

		mockDB.AssertExpectations(t)
	})
}

func TestPostgresStore_DeleteAPIKey(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully delete API key", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		tag := pgconn.NewCommandTag("DELETE 1")
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

		err := store.DeleteAPIKey(ctx, "key-123")
		require.NoError(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when delete fails", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(pgconn.CommandTag{}, fmt.Errorf("delete failed"))

		err := store.DeleteAPIKey(ctx, "key-123")
		assert.Error(t, err)

		mockDB.AssertExpectations(t)
	})

	t.Run("return error when key not found", func(t *testing.T) {
		mockDB := new(MockDBConnection)
		store := &PostgresStore{db: mockDB}

		tag := pgconn.NewCommandTag("DELETE 0")
		mockDB.On("Exec", ctx, mock.AnythingOfType("string"), mock.Anything).Return(tag, nil)

		err := store.DeleteAPIKey(ctx, "nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "API key not found")

		mockDB.AssertExpectations(t)
	})
}
