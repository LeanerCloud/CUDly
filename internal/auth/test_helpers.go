package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockStore is a mock implementation of the auth store for testing
type MockStore struct {
	mock.Mock
}

func (m *MockStore) GetUserByID(ctx context.Context, userID string) (*User, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*User), args.Error(1)
}

func (m *MockStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*User), args.Error(1)
}

func (m *MockStore) CreateUser(ctx context.Context, user *User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockStore) UpdateUser(ctx context.Context, user *User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockStore) DeleteUser(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockStore) ListUsers(ctx context.Context) ([]User, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]User), args.Error(1)
}

func (m *MockStore) GetUserByResetToken(ctx context.Context, token string) (*User, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*User), args.Error(1)
}

func (m *MockStore) AdminExists(ctx context.Context) (bool, error) {
	args := m.Called(ctx)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) GetGroup(ctx context.Context, groupID string) (*Group, error) {
	args := m.Called(ctx, groupID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Group), args.Error(1)
}

func (m *MockStore) CreateGroup(ctx context.Context, group *Group) error {
	args := m.Called(ctx, group)
	return args.Error(0)
}

func (m *MockStore) UpdateGroup(ctx context.Context, group *Group) error {
	args := m.Called(ctx, group)
	return args.Error(0)
}

func (m *MockStore) DeleteGroup(ctx context.Context, groupID string) error {
	args := m.Called(ctx, groupID)
	return args.Error(0)
}

func (m *MockStore) ListGroups(ctx context.Context) ([]Group, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]Group), args.Error(1)
}

func (m *MockStore) CreateSession(ctx context.Context, session *Session) error {
	args := m.Called(ctx, session)
	return args.Error(0)
}

func (m *MockStore) GetSession(ctx context.Context, token string) (*Session, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Session), args.Error(1)
}

func (m *MockStore) DeleteSession(ctx context.Context, token string) error {
	args := m.Called(ctx, token)
	return args.Error(0)
}

func (m *MockStore) DeleteUserSessions(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockStore) CleanupExpiredSessions(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// API Key operations
func (m *MockStore) CreateAPIKey(ctx context.Context, key *UserAPIKey) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockStore) GetAPIKeyByID(ctx context.Context, keyID string) (*UserAPIKey, error) {
	args := m.Called(ctx, keyID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*UserAPIKey), args.Error(1)
}

func (m *MockStore) GetAPIKeyByHash(ctx context.Context, keyHash string) (*UserAPIKey, error) {
	args := m.Called(ctx, keyHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*UserAPIKey), args.Error(1)
}

func (m *MockStore) ListAPIKeysByUser(ctx context.Context, userID string) ([]*UserAPIKey, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*UserAPIKey), args.Error(1)
}

func (m *MockStore) UpdateAPIKey(ctx context.Context, key *UserAPIKey) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockStore) DeleteAPIKey(ctx context.Context, keyID string) error {
	args := m.Called(ctx, keyID)
	return args.Error(0)
}

func (m *MockStore) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// MockEmailSender is a mock implementation of the email sender for testing
type MockEmailSender struct {
	mock.Mock
}

func (m *MockEmailSender) SendPasswordResetEmail(ctx context.Context, email, resetURL string) error {
	args := m.Called(ctx, email, resetURL)
	return args.Error(0)
}

func (m *MockEmailSender) SendWelcomeEmail(ctx context.Context, email, dashboardURL, role string) error {
	args := m.Called(ctx, email, dashboardURL, role)
	return args.Error(0)
}

// Verify that MockStore implements StoreInterface
var _ StoreInterface = (*MockStore)(nil)

// Verify that MockEmailSender implements EmailSenderInterface
var _ EmailSenderInterface = (*MockEmailSender)(nil)

// createTestService creates a service with mocks for testing
func createTestService(mockStore *MockStore, mockEmail *MockEmailSender) *Service {
	return &Service{
		store:           mockStore,
		emailSender:     mockEmail,
		sessionDuration: 24 * time.Hour,
		dashboardURL:    "https://dashboard.example.com",
	}
}

// createTestUser creates a user with hashed password for testing
func createTestUser(t *testing.T, password string) *User {
	t.Helper()

	// We need a service instance to hash the password
	// Note: salt is no longer used - bcrypt handles salting internally
	s := &Service{}
	hash, err := s.hashPassword(password)
	require.NoError(t, err)

	return &User{
		ID:           "user-123",
		Email:        "test@example.com",
		PasswordHash: hash,
		Salt:         "", // Not used anymore, bcrypt handles salting
		Role:         RoleUser,
		Active:       true,
		CreatedAt:    time.Now(),
	}
}
