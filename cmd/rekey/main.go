// Command rekey is a one-shot migration that re-encrypts every row in
// account_credentials whose ciphertext was produced under the all-zero dev key
// (because the cipher.go silent-fallback bug was active at write time on
// Azure Container Apps and GCP Cloud Run before the security fix landed).
//
// Operator runbook: cmd/rekey/README.md
//
// Refuses to run unless CUDLY_REKEY_FROM_ZERO_KEY=1 is set. Idempotent —
// running it a second time finds nothing to re-key (zero-key Decrypt fails
// on real-key rows, so they fall into the "skipped" bucket).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/LeanerCloud/CUDly/internal/secrets"
)

const safetyEnv = "CUDLY_REKEY_FROM_ZERO_KEY"

type counters struct {
	scanned, reKeyed, skippedAlreadyReal, errored int
}

func main() {
	timeout := flag.Duration("timeout", 10*time.Minute, "overall timeout for the migration")
	flag.Parse()

	if os.Getenv(safetyEnv) != "1" {
		log.Fatalf("rekey: refusing to run without %s=1 (see docs/runbooks/rekey-from-zero-key.md)", safetyEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := run(ctx); err != nil {
		log.Fatalf("rekey: %v", err)
	}
}

func run(ctx context.Context) error {
	resolver, err := buildResolver(ctx)
	if err != nil {
		return fmt.Errorf("build resolver: %w", err)
	}
	defer func() {
		if cerr := resolver.Close(); cerr != nil {
			log.Printf("rekey: warning: resolver close: %v", cerr)
		}
	}()

	realKey, source, err := credentials.LoadKey(ctx, resolver)
	if err != nil {
		return fmt.Errorf("load real key: %w", err)
	}
	log.Printf("rekey: loaded real key via %s", source)

	zeroKey := credentials.DevKey()
	if isEqual(realKey, zeroKey) {
		return fmt.Errorf("real key is the all-zero dev key — refusing to rekey (would be a no-op)")
	}

	db, err := initDB(ctx, resolver)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer db.Close()

	cs, err := rekeyAccountCredentials(ctx, db, zeroKey, realKey)
	if err != nil {
		return fmt.Errorf("rekey: %w", err)
	}
	log.Printf("rekey: scanned=%d re_keyed=%d skipped_already_real=%d errored=%d",
		cs.scanned, cs.reKeyed, cs.skippedAlreadyReal, cs.errored)

	if cs.errored > 0 {
		return fmt.Errorf("rekey: %d rows errored — see logs for IDs", cs.errored)
	}
	return nil
}

// buildResolver wires the same secrets.Resolver the production server uses.
func buildResolver(ctx context.Context) (secrets.Resolver, error) {
	return secrets.NewResolver(ctx, secrets.LoadConfigFromEnv())
}

// initDB connects using the same env-driven path the production server uses,
// so the rekey job runs against the same DB as the live service.
func initDB(ctx context.Context, resolver secrets.Resolver) (*database.Connection, error) {
	dbConfig, err := database.LoadFromEnv()
	if err != nil {
		return nil, fmt.Errorf("load db config: %w", err)
	}
	var sr database.SecretResolver
	if dbConfig.PasswordSecret != "" {
		sr = resolver
	}
	return database.NewConnection(ctx, dbConfig, sr)
}

// rekeyAccountCredentials walks account_credentials and re-encrypts every row
// whose ciphertext decrypts under the zero key. Real-key rows are detected by
// decrypt failure (AES-GCM authentication tag mismatch) and skipped. Each
// re-key is committed in its own transaction so a partial run leaves the
// database in a consistent state.
func rekeyAccountCredentials(ctx context.Context, db *database.Connection, zeroKey, realKey []byte) (counters, error) {
	var cs counters

	rows, err := db.Query(ctx, `SELECT id, encrypted_blob FROM account_credentials`)
	if err != nil {
		return cs, fmt.Errorf("query: %w", err)
	}

	type row struct {
		id   string
		blob string
	}
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.blob); err != nil {
			rows.Close()
			return cs, fmt.Errorf("scan: %w", err)
		}
		pending = append(pending, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return cs, fmt.Errorf("iter: %w", err)
	}

	for _, r := range pending {
		cs.scanned++
		outcome := rekeyOne(ctx, db, r.id, r.blob, zeroKey, realKey)
		switch outcome {
		case outcomeReKeyed:
			cs.reKeyed++
		case outcomeSkipped:
			cs.skippedAlreadyReal++
		case outcomeErrored:
			cs.errored++
		}
	}
	return cs, nil
}

type rekeyOutcome int

const (
	outcomeReKeyed rekeyOutcome = iota
	outcomeSkipped
	outcomeErrored
)

// rekeyOne handles a single row inside its own transaction. Returns the outcome.
// Plaintext is held in memory only for the duration of this call and is never
// logged.
func rekeyOne(ctx context.Context, db *database.Connection, id, blob string, zeroKey, realKey []byte) rekeyOutcome {
	plaintext, err := credentials.Decrypt(zeroKey, blob)
	if err != nil {
		// Decrypt with zero key failed — assume already real-key encrypted.
		return outcomeSkipped
	}
	newBlob, err := credentials.Encrypt(realKey, plaintext)
	if err != nil {
		log.Printf("rekey: encrypt id=%s: %v", id, err)
		return outcomeErrored
	}
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		log.Printf("rekey: begin tx id=%s: %v", id, err)
		return outcomeErrored
	}
	if _, err := tx.Exec(ctx, `UPDATE account_credentials SET encrypted_blob = $1 WHERE id = $2`, newBlob, id); err != nil {
		_ = tx.Rollback(ctx)
		log.Printf("rekey: update id=%s: %v", id, err)
		return outcomeErrored
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("rekey: commit id=%s: %v", id, err)
		return outcomeErrored
	}
	return outcomeReKeyed
}

// isEqual is a small constant-time-like equality check on key bytes.
// Used only for sanity-check that real != zero; not security-sensitive.
func isEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
