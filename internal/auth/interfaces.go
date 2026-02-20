package auth

import (
	"context"
)

// StoreInterface defines the methods required for auth storage
type StoreInterface interface {
	// User operations
	GetUserByID(ctx context.Context, userID string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	CreateUser(ctx context.Context, user *User) error
	UpdateUser(ctx context.Context, user *User) error
	DeleteUser(ctx context.Context, userID string) error
	ListUsers(ctx context.Context) ([]User, error)
	GetUserByResetToken(ctx context.Context, token string) (*User, error)
	AdminExists(ctx context.Context) (bool, error)

	// Group operations
	GetGroup(ctx context.Context, groupID string) (*Group, error)
	CreateGroup(ctx context.Context, group *Group) error
	UpdateGroup(ctx context.Context, group *Group) error
	DeleteGroup(ctx context.Context, groupID string) error
	ListGroups(ctx context.Context) ([]Group, error)

	// Session operations
	CreateSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, token string) (*Session, error)
	DeleteSession(ctx context.Context, token string) error
	DeleteUserSessions(ctx context.Context, userID string) error
	CleanupExpiredSessions(ctx context.Context) error

	// API Key operations
	CreateAPIKey(ctx context.Context, key *UserAPIKey) error
	GetAPIKeyByID(ctx context.Context, keyID string) (*UserAPIKey, error)
	GetAPIKeyByHash(ctx context.Context, keyHash string) (*UserAPIKey, error)
	ListAPIKeysByUser(ctx context.Context, userID string) ([]*UserAPIKey, error)
	UpdateAPIKey(ctx context.Context, key *UserAPIKey) error
	DeleteAPIKey(ctx context.Context, keyID string) error
}

// EmailSenderInterface defines the methods required for sending emails
type EmailSenderInterface interface {
	SendPasswordResetEmail(ctx context.Context, email, resetURL string) error
	SendWelcomeEmail(ctx context.Context, email, dashboardURL, role string) error
}
