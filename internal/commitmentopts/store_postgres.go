package commitmentopts

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// dbConn is the minimal pgx-style surface PostgresStore uses. Both
// *database.Connection and the pgxmock mocks satisfy it. Mirrors the
// interface shape in internal/config/store_postgres.go so the two stores
// can share test-container scaffolding.
type dbConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// PostgresStore is the Postgres-backed commitmentopts.Store implementation.
type PostgresStore struct {
	db dbConn
}

// NewPostgresStore returns a store backed by the given connection. The
// connection must already have the 000039 migration applied.
func NewPostgresStore(db *database.Connection) *PostgresStore {
	return &PostgresStore{db: db}
}

// Verify PostgresStore implements Store.
var _ Store = (*PostgresStore)(nil)

// Get returns every persisted combo grouped by provider and service,
// plus a boolean that is true iff a probe run row exists. The combos map
// may be empty even when the boolean is true — that legitimately means
// "we probed and nothing matched our normalizer".
func (s *PostgresStore) Get(ctx context.Context) (Options, bool, error) {
	hasData, err := s.HasData(ctx)
	if err != nil {
		return nil, false, err
	}
	if !hasData {
		return nil, false, nil
	}

	rows, err := s.db.Query(ctx,
		`SELECT provider, service, term_years, payment_option FROM commitment_options_combos`,
	)
	if err != nil {
		return nil, false, fmt.Errorf("query commitment_options_combos: %w", err)
	}
	defer rows.Close()

	opts := make(Options)
	for rows.Next() {
		var c Combo
		if err := rows.Scan(&c.Provider, &c.Service, &c.TermYears, &c.Payment); err != nil {
			return nil, false, fmt.Errorf("scan commitment_options_combos row: %w", err)
		}
		byService := opts[c.Provider]
		if byService == nil {
			byService = make(map[string][]Combo)
			opts[c.Provider] = byService
		}
		byService[c.Service] = append(byService[c.Service], c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate commitment_options_combos: %w", err)
	}
	return opts, true, nil
}

// HasData reports whether a probe run row exists.
func (s *PostgresStore) HasData(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM commitment_options_probe_runs)`,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check commitment_options_probe_runs: %w", err)
	}
	return exists, nil
}

// Save persists the probe run row and every combo transactionally. If a
// run row already exists (a second process raced us to persist), Save is
// a no-op — the singleton PK means only the first writer wins. Combo
// inserts use ON CONFLICT DO NOTHING so re-running the probe after a
// manual row clear and partial repopulation doesn't fail.
func (s *PostgresStore) Save(ctx context.Context, combos []Combo, sourceAccountID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`INSERT INTO commitment_options_probe_runs (singleton, probed_at, source_account_id)
		 VALUES (TRUE, NOW(), $1)
		 ON CONFLICT (singleton) DO NOTHING`,
		sourceAccountID,
	); err != nil {
		return fmt.Errorf("insert commitment_options_probe_runs: %w", err)
	}

	for _, c := range combos {
		if _, err := tx.Exec(ctx,
			`INSERT INTO commitment_options_combos (provider, service, term_years, payment_option)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT DO NOTHING`,
			c.Provider, c.Service, c.TermYears, c.Payment,
		); err != nil {
			return fmt.Errorf("insert commitment_options_combos: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
