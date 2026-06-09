package config

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPGXMock_CreateSuppression_RoundTrip(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO purchase_suppressions`).
		WithArgs(pgxmock.AnyArg(), "exec-1", "acct-1", "aws", "ec2", "us-east-1",
			"t4g.nano", "", 3, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	expires := time.Now().Add(7 * 24 * time.Hour)
	err := store.CreateSuppression(ctx, &PurchaseSuppression{
		ExecutionID: "exec-1", AccountID: "acct-1", Provider: "aws",
		Service: "ec2", Region: "us-east-1", ResourceType: "t4g.nano",
		SuppressedCount: 3, ExpiresAt: expires,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_CreateSuppression_Validation(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	t.Run("nil suppression rejected", func(t *testing.T) {
		err := store.CreateSuppression(ctx, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil")
	})

	t.Run("missing execution_id rejected", func(t *testing.T) {
		err := store.CreateSuppression(ctx, &PurchaseSuppression{
			SuppressedCount: 1, ExpiresAt: time.Now().Add(time.Hour),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ExecutionID")
	})

	t.Run("non-positive count rejected", func(t *testing.T) {
		err := store.CreateSuppression(ctx, &PurchaseSuppression{
			ExecutionID: "e", SuppressedCount: 0, ExpiresAt: time.Now().Add(time.Hour),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SuppressedCount")
	})

	t.Run("zero expiry rejected", func(t *testing.T) {
		err := store.CreateSuppression(ctx, &PurchaseSuppression{
			ExecutionID: "e", SuppressedCount: 1,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ExpiresAt")
	})
}

func TestPGXMock_DeleteSuppressionsByExecution(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM purchase_suppressions WHERE execution_id = \$1`).
		WithArgs("exec-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 2))
	mock.ExpectCommit()

	require.NoError(t, store.DeleteSuppressionsByExecution(ctx, "exec-1"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_ListActiveSuppressions(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	future := now.Add(24 * time.Hour)
	cols := []string{
		"id", "execution_id", "account_id", "provider", "service", "region",
		"resource_type", "engine", "suppressed_count", "expires_at", "created_at",
	}
	rows := pgxmock.NewRows(cols).
		AddRow("sup-1", "exec-1", "acct-1", "aws", "ec2", "us-east-1",
			"t4g.nano", "", 3, future, now).
		AddRow("sup-2", "exec-2", "acct-1", "aws", "ec2", "us-east-1",
			"t4g.nano", "", 2, future, now)
	mock.ExpectQuery(`SELECT .* FROM purchase_suppressions WHERE expires_at > NOW\(\)`).
		WillReturnRows(rows)

	sups, err := store.ListActiveSuppressions(ctx)
	require.NoError(t, err)
	assert.Len(t, sups, 2)
	assert.Equal(t, "sup-1", sups[0].ID)
	assert.Equal(t, 3, sups[0].SuppressedCount)
	assert.Equal(t, "sup-2", sups[1].ID)
	assert.Equal(t, 2, sups[1].SuppressedCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_WithTx_Commits(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT`).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO foo VALUES (1)")
		return err
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_WithTx_RollsBackOnError(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectRollback()

	err := store.WithTx(ctx, func(_ pgx.Tx) error {
		return assert.AnError
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
	assert.NoError(t, mock.ExpectationsWereMet())
}
