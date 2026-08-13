package config

// store_postgres_ri_exchange_idempotency_test.go — pgxmock coverage for the
// RI exchange submit-time claim (issue #1642). The claim decides whether an
// irreversible exchange commits, so both of its answers and its failure mode
// are pinned here; the real concurrency and window semantics are exercised
// against a live Postgres in store_postgres_db_test.go.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPGXMock_ClaimRIExchangeIdempotencyKey_WonWhenRowWritten pins the winning
// answer AND the shape of the statement that produces it: the whole decision
// is one Exec, and the window travels as seconds into make_interval so the
// expiry is compared against the DATABASE clock rather than the caller's.
func TestPGXMock_ClaimRIExchangeIdempotencyKey_WonWhenRowWritten(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)

	mock.ExpectExec("INSERT INTO ri_exchange_idempotency").
		WithArgs("fingerprint-1", 900.0).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	claimed, err := store.ClaimRIExchangeIdempotencyKey(context.Background(), "fingerprint-1", 15*time.Minute)
	require.NoError(t, err)
	assert.True(t, claimed, "an inserted row means this caller owns the submit")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_ClaimRIExchangeIdempotencyKey_LostWhenConflictHeld is the branch
// that stops the double spend: the ON CONFLICT predicate finds the existing
// claim still inside its window, updates nothing, and the caller must read
// that zero-row result as "someone else already submitted this".
func TestPGXMock_ClaimRIExchangeIdempotencyKey_LostWhenConflictHeld(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)

	mock.ExpectExec("INSERT INTO ri_exchange_idempotency").
		WithArgs("fingerprint-1", 900.0).
		WillReturnResult(pgxmock.NewResult("INSERT", 0))

	claimed, err := store.ClaimRIExchangeIdempotencyKey(context.Background(), "fingerprint-1", 15*time.Minute)
	require.NoError(t, err)
	assert.False(t, claimed, "a live claim held by an earlier submit must not be taken over")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_ClaimRIExchangeIdempotencyKey_ErrorIsNotAWonClaim: a failed Exec
// must surface as an error, never as a claim the caller can act on.
func TestPGXMock_ClaimRIExchangeIdempotencyKey_ErrorIsNotAWonClaim(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)

	mock.ExpectExec("INSERT INTO ri_exchange_idempotency").
		WithArgs("fingerprint-1", 900.0).
		WillReturnError(errors.New("connection refused"))

	claimed, err := store.ClaimRIExchangeIdempotencyKey(context.Background(), "fingerprint-1", 15*time.Minute)
	require.Error(t, err)
	assert.False(t, claimed)
	assert.Contains(t, err.Error(), "connection refused")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_ClaimRIExchangeIdempotencyKey_RejectsUnusableArguments pins the
// fail-loud guards. An empty key would collapse every distinct exchange onto
// one row, and a non-positive window would make make_interval(secs => 0) treat
// every existing claim as expired -- both turn the guard into a no-op that
// still reports success, which is worse than no guard at all.
func TestPGXMock_ClaimRIExchangeIdempotencyKey_RejectsUnusableArguments(t *testing.T) {
	cases := map[string]struct {
		key    string
		window time.Duration
		want   string
	}{
		"empty key":       {"", 15 * time.Minute, "empty RI exchange idempotency key"},
		"zero window":     {"fingerprint-1", 0, "window must be positive"},
		"negative window": {"fingerprint-1", -time.Second, "window must be positive"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mock := newMock(t)
			store := storeWith(mock)
			// No ExpectExec: the guard must refuse before touching the database.

			claimed, err := store.ClaimRIExchangeIdempotencyKey(context.Background(), tc.key, tc.window)
			require.Error(t, err)
			assert.False(t, claimed)
			assert.Contains(t, err.Error(), tc.want)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
