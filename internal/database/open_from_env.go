package database

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/secrets"
)

// OpenFromEnv creates a database connection using environment-variable configuration.
//
// It loads the DB config from the environment, optionally builds a secrets.Resolver
// when DB_PASSWORD_SECRET is set (resolving the password synchronously before
// returning), then returns an open connection pool. The resolver is closed as
// soon as the password has been resolved; callers do not need to manage it.
//
// This is the canonical bootstrap helper for cmd/* entrypoints that only need
// a DB connection and have no other use for the secrets resolver. If you also
// need the resolver for a different purpose (e.g. cmd/rekey loads a key via
// credentials.LoadKey before opening the DB), wire the resolver yourself and
// call NewConnection directly.
func OpenFromEnv(ctx context.Context) (*Connection, error) {
	dbConfig, err := LoadFromEnv()
	if err != nil {
		return nil, fmt.Errorf("database: load config from env: %w", err)
	}

	var sr SecretResolver
	if dbConfig.PasswordSecret != "" {
		secretConfig := secrets.LoadConfigFromEnv()
		resolver, rerr := secrets.NewResolver(ctx, secretConfig)
		if rerr != nil {
			return nil, fmt.Errorf("database: create secret resolver: %w", rerr)
		}
		// NewConnection resolves the password synchronously before returning, so
		// the resolver is safe to close immediately after the connection is open.
		defer func() {
			if cerr := resolver.Close(); cerr != nil {
				// Non-fatal: the connection is already established.
				_ = cerr
			}
		}()
		sr = resolver
	}

	conn, err := NewConnection(ctx, dbConfig, sr)
	if err != nil {
		return nil, fmt.Errorf("database: connect: %w", err)
	}
	return conn, nil
}
