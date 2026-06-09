package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// CREATE CLOUD ACCOUNT
// ==========================================

func TestPostgresStore_CreateCloudAccount_GeneratesIDAndTimestamps(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	account := &CloudAccount{
		Name:       "Prod-US",
		Provider:   "aws",
		ExternalID: "123456789012",
		Enabled:    true,
	}

	// The method generates ID and timestamps before the DB call.
	panicked := callWithRecover(func() {
		_ = store.CreateCloudAccount(ctx, account)
	})

	assert.True(t, panicked, "expected panic with nil db")
	assert.NotEmpty(t, account.ID, "ID should be generated before the DB call")
	assert.False(t, account.CreatedAt.IsZero(), "CreatedAt should be set before the DB call")
	assert.False(t, account.UpdatedAt.IsZero(), "UpdatedAt should be set before the DB call")
}

func TestPostgresStore_CreateCloudAccount_PreservesExistingID(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	existingID := "aaaabbbb-cccc-dddd-eeee-ffffffffffff"
	account := &CloudAccount{
		ID:         existingID,
		Name:       "Prod-US",
		Provider:   "aws",
		ExternalID: "123456789012",
		Enabled:    true,
	}

	callWithRecover(func() {
		_ = store.CreateCloudAccount(ctx, account)
	})

	assert.Equal(t, existingID, account.ID, "existing ID should be preserved")
}

// ==========================================
// UPDATE CLOUD ACCOUNT
// ==========================================

func TestPostgresStore_UpdateCloudAccount_SetsUpdatedAt(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	before := time.Now().Add(-time.Second)
	account := &CloudAccount{
		ID:       "test-id",
		Name:     "Prod-US",
		Provider: "aws",
	}

	callWithRecover(func() {
		_ = store.UpdateCloudAccount(ctx, account)
	})

	assert.True(t, account.UpdatedAt.After(before), "UpdatedAt should be refreshed")
}

// ==========================================
// SAVE ACCOUNT SERVICE OVERRIDE
// ==========================================

func TestPostgresStore_SaveAccountServiceOverride_GeneratesIDAndTimestamps(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	override := &AccountServiceOverride{
		AccountID: "acct-id",
		Provider:  "aws",
		Service:   "ec2",
	}

	callWithRecover(func() {
		_ = store.SaveAccountServiceOverride(ctx, override)
	})

	assert.NotEmpty(t, override.ID, "ID should be generated")
	assert.False(t, override.CreatedAt.IsZero(), "CreatedAt should be set")
	assert.False(t, override.UpdatedAt.IsZero(), "UpdatedAt should be set")
}

func TestPostgresStore_SaveAccountServiceOverride_PreservesExistingID(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	existingID := "override-existing-id"
	override := &AccountServiceOverride{
		ID:        existingID,
		AccountID: "acct-id",
		Provider:  "aws",
		Service:   "ec2",
	}

	callWithRecover(func() {
		_ = store.SaveAccountServiceOverride(ctx, override)
	})

	assert.Equal(t, existingID, override.ID, "existing ID should be preserved")
}

// ==========================================
// CloudAccountFilter validation
// ==========================================

func TestCloudAccountFilter_ZeroValue(t *testing.T) {
	filter := CloudAccountFilter{}
	assert.Nil(t, filter.Provider)
	assert.Nil(t, filter.Enabled)
	assert.Empty(t, filter.Search)
	assert.Nil(t, filter.BastionID)
}

func TestCloudAccountFilter_WithAllFields(t *testing.T) {
	provider := "aws"
	enabled := true
	bastionID := "bastion-uuid"

	filter := CloudAccountFilter{
		Provider:  &provider,
		Enabled:   &enabled,
		Search:    "prod",
		BastionID: &bastionID,
	}

	require.NotNil(t, filter.Provider)
	assert.Equal(t, "aws", *filter.Provider)
	require.NotNil(t, filter.Enabled)
	assert.True(t, *filter.Enabled)
	assert.Equal(t, "prod", filter.Search)
	require.NotNil(t, filter.BastionID)
	assert.Equal(t, "bastion-uuid", *filter.BastionID)
}

// ==========================================
// CloudAccount derived field
// ==========================================

func TestCloudAccount_CredentialsConfiguredDefaultsFalse(t *testing.T) {
	a := CloudAccount{
		ID:         "id",
		Name:       "Test",
		Provider:   "aws",
		ExternalID: "123456789012",
		Enabled:    true,
	}
	assert.False(t, a.CredentialsConfigured)
}

func TestCloudAccount_AWSFieldsOptional(t *testing.T) {
	a := CloudAccount{
		ID:           "id",
		Name:         "Test",
		Provider:     "aws",
		ExternalID:   "123456789012",
		Enabled:      true,
		AWSAuthMode:  "role_arn",
		AWSRoleARN:   "arn:aws:iam::123456789012:role/CUDly",
		AWSIsOrgRoot: false,
	}
	assert.Equal(t, "role_arn", a.AWSAuthMode)
	assert.Equal(t, "arn:aws:iam::123456789012:role/CUDly", a.AWSRoleARN)
}
