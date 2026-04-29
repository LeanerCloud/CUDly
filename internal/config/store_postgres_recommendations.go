package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// batchSize caps rows per multi-row INSERT so we don't build an
// unbounded-sized query. 500 keeps placeholder count well under pgx's
// 65535 parameter limit (each row has 9 columns → max ≈7280 rows; 500
// gives 4500 params which stays conservative and leaves headroom).
const recommendationsBatchSize = 500

// ReplaceRecommendations wipes the recommendations table and reinserts the
// full snapshot inside a single transaction. Used for a force-full-resync
// path; the steady-state write path (see commit 6) is UpsertRecommendations.
// Atomic replace means concurrent readers either see the full old snapshot
// or the full new one — never a partial mid-replace state.
func (s *PostgresStore) ReplaceRecommendations(ctx context.Context, collectedAt time.Time, recs []RecommendationRecord) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `DELETE FROM recommendations`); err != nil {
		return fmt.Errorf("failed to wipe recommendations: %w", err)
	}

	if err := insertRecommendationsBatched(ctx, tx, collectedAt, recs, false); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE recommendations_state
		   SET last_collected_at     = $1,
		       last_collection_error = NULL
		 WHERE id = 1
	`, collectedAt); err != nil {
		return fmt.Errorf("failed to update state: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit replace: %w", err)
	}
	return nil
}

// UpsertRecommendations is the incremental write path: it upserts each row
// by natural key (cloud_account_id, provider, service, region,
// resource_type, term, payment_option) and then evicts stale rows for the
// set of (provider, account) pairs that successfully collected in this run.
// Pairs whose collection failed are NOT in successfulCollects and their
// stale rows stay — callers see older data with a banner rather than a
// blank section.
//
// Migration 000032 broadened the natural key to include term + payment_option,
// so per-rec ON CONFLICT no longer collides on SQLSTATE 21000.
//
// The eviction predicate uses (provider, account_key) IN (unnest($2, $3))
// where account_key matches the generated column on the table — nil
// CloudAccountID maps to uuid.Nil at the Go boundary, matching the
// COALESCE(cloud_account_id, '00000000-...') rule the table applies on
// insert. This collapses ambient and registered identities consistently
// on both sides of the join.
func (s *PostgresStore) UpsertRecommendations(ctx context.Context, collectedAt time.Time, recs []RecommendationRecord, successfulCollects []SuccessfulCollect) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := insertRecommendationsBatched(ctx, tx, collectedAt, recs, true); err != nil {
		return err
	}

	if len(successfulCollects) > 0 {
		providers, accountKeys, err := successfulCollectArrays(successfulCollects)
		if err != nil {
			return fmt.Errorf("failed to materialise successful-collect arrays: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM recommendations
			 WHERE collected_at < $1
			   AND (provider, account_key) IN (
			       SELECT p, a
			         FROM unnest($2::text[], $3::uuid[]) AS t(p, a)
			   )
		`, collectedAt, providers, accountKeys); err != nil {
			return fmt.Errorf("failed to evict stale rows: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE recommendations_state
		   SET last_collected_at     = $1,
		       last_collection_error = NULL
		 WHERE id = 1
	`, collectedAt); err != nil {
		return fmt.Errorf("failed to update state: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit upsert: %w", err)
	}
	return nil
}

// successfulCollectArrays converts the SuccessfulCollect slice into the two
// parallel arrays the eviction unnest() expects. nil CloudAccountID becomes
// uuid.Nil so the join matches the generated account_key column for
// ambient-credential rows; non-nil pointers go through uuid.Parse with
// errors surfaced.
func successfulCollectArrays(scs []SuccessfulCollect) (providers []string, accountKeys []uuid.UUID, err error) {
	providers = make([]string, len(scs))
	accountKeys = make([]uuid.UUID, len(scs))
	for i, sc := range scs {
		providers[i] = sc.Provider
		if sc.CloudAccountID == nil {
			accountKeys[i] = uuid.Nil
			continue
		}
		parsed, perr := uuid.Parse(*sc.CloudAccountID)
		if perr != nil {
			return nil, nil, fmt.Errorf("invalid cloud_account_id %q for provider %q: %w", *sc.CloudAccountID, sc.Provider, perr)
		}
		accountKeys[i] = parsed
	}
	return providers, accountKeys, nil
}

// insertRecommendationsBatched performs a batched multi-row INSERT of recs
// inside the given transaction. If onConflict is true, it appends an
// ON CONFLICT DO UPDATE clause keyed by the natural key; if false, the
// insert assumes the table was pre-wiped and conflicts can't occur (used
// by ReplaceRecommendations).
func insertRecommendationsBatched(ctx context.Context, tx pgx.Tx, collectedAt time.Time, recs []RecommendationRecord, onConflict bool) error {
	for start := 0; start < len(recs); start += recommendationsBatchSize {
		end := start + recommendationsBatchSize
		if end > len(recs) {
			end = len(recs)
		}
		if err := insertRecommendationsBatch(ctx, tx, collectedAt, recs[start:end], onConflict); err != nil {
			return err
		}
	}
	return nil
}

// insertRecommendationsBatch inserts a single batch of up to
// recommendationsBatchSize rows. Splits the VALUES placeholder list into
// (collected_at, cloud_account_id, provider, service, region,
// resource_type, engine, payload, upfront_cost, monthly_savings, term,
// payment_option) — 12 columns per row (id defaulted via
// gen_random_uuid() so we send 12 args, no $n for id).
//
// term + payment_option were added as part of the natural-key
// broadening (migration 000032) so per-rec ON CONFLICT can store
// every Azure term × payment variant per SKU instead of collapsing
// onto the highest-savings one.
// engine was added to distinguish MySQL vs Postgres RDS at the same SKU.
func insertRecommendationsBatch(ctx context.Context, tx pgx.Tx, collectedAt time.Time, recs []RecommendationRecord, onConflict bool) error {
	if len(recs) == 0 {
		return nil
	}

	const colsPerRow = 12
	args := make([]any, 0, len(recs)*colsPerRow)
	placeholders := make([]string, 0, len(recs))

	for i, rec := range recs {
		payload, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("failed to marshal recommendation %d: %w", i, err)
		}
		base := i * colsPerRow
		placeholders = append(placeholders, fmt.Sprintf(
			"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11, base+12,
		))
		args = append(args,
			collectedAt,        // collected_at
			rec.CloudAccountID, // cloud_account_id (nullable)
			rec.Provider,       // provider
			rec.Service,        // service
			rec.Region,         // region
			rec.ResourceType,   // resource_type
			rec.Engine,         // engine
			payload,            // payload (JSONB)
			rec.UpfrontCost,    // upfront_cost
			rec.Savings,        // monthly_savings
			rec.Term,           // term
			rec.Payment,        // payment_option
		)
	}

	query := fmt.Sprintf(`
		INSERT INTO recommendations
		    (collected_at, cloud_account_id, provider, service, region,
		     resource_type, engine, payload, upfront_cost, monthly_savings,
		     term, payment_option)
		VALUES %s
	`, strings.Join(placeholders, ","))

	if onConflict {
		query += `
			ON CONFLICT (account_key, provider, service, region, resource_type, engine, term, payment_option)
			DO UPDATE SET
			    payload         = EXCLUDED.payload,
			    upfront_cost    = EXCLUDED.upfront_cost,
			    monthly_savings = EXCLUDED.monthly_savings,
			    collected_at    = EXCLUDED.collected_at,
			    cloud_account_id = EXCLUDED.cloud_account_id
		`
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to insert recommendations batch: %w", err)
	}
	return nil
}

// buildRecommendationFilter constructs the WHERE clause + parameter list
// for ListStoredRecommendations. Extracted to keep the caller below the
// gocyclo threshold; also makes the SQL builder testable in isolation if
// needed.
func buildRecommendationFilter(filter RecommendationFilter) (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, val any) {
		conds = append(conds, fmt.Sprintf(cond, len(args)+1))
		args = append(args, val)
	}
	if filter.Provider != "" {
		add("provider = $%d", filter.Provider)
	}
	if filter.Service != "" {
		add("service = $%d", filter.Service)
	}
	if filter.Region != "" {
		add("region = $%d", filter.Region)
	}
	if len(filter.AccountIDs) > 0 {
		add("cloud_account_id = ANY($%d)", filter.AccountIDs)
	}
	if filter.MinSavings > 0 {
		add("monthly_savings >= $%d", filter.MinSavings)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ListStoredRecommendations reads recommendations matching the filter.
// All pushed-down filter conditions are applied in SQL so Postgres (not
// Go) prunes the rows. The payload JSONB is decoded back into the original
// RecommendationRecord. Ordering is left to callers.
func (s *PostgresStore) ListStoredRecommendations(ctx context.Context, filter RecommendationFilter) ([]RecommendationRecord, error) {
	whereClause, args := buildRecommendationFilter(filter)
	rows, err := s.db.Query(ctx, `SELECT payload FROM recommendations`+whereClause, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query recommendations: %w", err)
	}
	defer rows.Close()

	var out []RecommendationRecord
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("failed to scan payload: %w", err)
		}
		var rec RecommendationRecord
		if err := json.Unmarshal(payload, &rec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}
	return out, nil
}

// GetRecommendationsFreshness returns the singleton freshness row. The
// table is seeded with id=1 by the migration so a row always exists;
// LastCollectedAt and LastCollectionError can be NULL.
func (s *PostgresStore) GetRecommendationsFreshness(ctx context.Context) (*RecommendationsFreshness, error) {
	var (
		lastCollectedAt     *time.Time
		lastCollectionError *string
	)
	err := s.db.QueryRow(ctx, `
		SELECT last_collected_at, last_collection_error
		  FROM recommendations_state
		 WHERE id = 1
	`).Scan(&lastCollectedAt, &lastCollectionError)
	if err != nil {
		return nil, fmt.Errorf("failed to read recommendations_state: %w", err)
	}
	return &RecommendationsFreshness{
		LastCollectedAt:     lastCollectedAt,
		LastCollectionError: lastCollectionError,
	}, nil
}

// SetRecommendationsCollectionError records the most recent collection's
// error message without touching last_collected_at. Used by the scheduler
// when a collect fails partially or fully so the frontend banner surfaces
// the issue while existing cached rows stay visible.
func (s *PostgresStore) SetRecommendationsCollectionError(ctx context.Context, errMsg string) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE recommendations_state
		   SET last_collection_error = $1
		 WHERE id = 1
	`, errMsg); err != nil {
		return fmt.Errorf("failed to set collection error: %w", err)
	}
	return nil
}
