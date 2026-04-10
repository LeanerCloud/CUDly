package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestPostgresStore_SaveRIExchangeRecord_GeneratesID verifies that SaveRIExchangeRecord
// assigns a UUID to the record before hitting the DB.
func TestPostgresStore_SaveRIExchangeRecord_GeneratesID(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	record := &RIExchangeRecord{
		AccountID:          "acc-123",
		Region:             "us-east-1",
		SourceRIIDs:        []string{"ri-111"},
		SourceInstanceType: "m5.large",
		SourceCount:        1,
		TargetOfferingID:   "offering-abc",
		TargetInstanceType: "c5.large",
		TargetCount:        1,
		PaymentDue:         "0.00",
		Status:             "pending",
		Mode:               "manual",
	}

	panicked := callWithRecover(func() {
		_ = store.SaveRIExchangeRecord(ctx, record)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	assert.NotEmpty(t, record.ID, "ID should be generated before DB call")
}

// TestPostgresStore_SaveRIExchangeRecord_PreservesExistingID verifies that an
// existing ID is not overwritten.
func TestPostgresStore_SaveRIExchangeRecord_PreservesExistingID(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	record := &RIExchangeRecord{
		ID:        "existing-ri-id",
		AccountID: "acc-456",
		Status:    "pending",
		Mode:      "auto",
	}

	panicked := callWithRecover(func() {
		_ = store.SaveRIExchangeRecord(ctx, record)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	assert.Equal(t, "existing-ri-id", record.ID)
}

// TestPostgresStore_GetRIExchangeRecord_NilDB exercises GetRIExchangeRecord entry.
func TestPostgresStore_GetRIExchangeRecord_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetRIExchangeRecord(ctx, "ri-id-123")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_GetRIExchangeRecordByToken_NilDB exercises the method entry.
func TestPostgresStore_GetRIExchangeRecordByToken_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetRIExchangeRecordByToken(ctx, "some-token")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_GetRIExchangeHistory_NilDB exercises the method entry.
func TestPostgresStore_GetRIExchangeHistory_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetRIExchangeHistory(ctx, time.Now().Add(-24*time.Hour), 10)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_TransitionRIExchangeStatus_NilDB exercises the method entry.
func TestPostgresStore_TransitionRIExchangeStatus_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.TransitionRIExchangeStatus(ctx, "ri-id", "pending", "processing")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_CompleteRIExchange_NilDB exercises the method entry.
func TestPostgresStore_CompleteRIExchange_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_ = store.CompleteRIExchange(ctx, "ri-id", "exchange-id")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_FailRIExchange_NilDB exercises the method entry.
func TestPostgresStore_FailRIExchange_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_ = store.FailRIExchange(ctx, "ri-id", "some error message")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_GetRIExchangeDailySpend_NilDB exercises the method entry.
func TestPostgresStore_GetRIExchangeDailySpend_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetRIExchangeDailySpend(ctx, time.Now())
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_CancelAllPendingExchanges_NilDB exercises the method entry.
func TestPostgresStore_CancelAllPendingExchanges_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.CancelAllPendingExchanges(ctx)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_GetStaleProcessingExchanges_NilDB exercises the method entry.
func TestPostgresStore_GetStaleProcessingExchanges_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetStaleProcessingExchanges(ctx, 5*time.Minute)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_CleanupOldExecutions_NilDB exercises the method entry.
func TestPostgresStore_CleanupOldExecutions_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.CleanupOldExecutions(ctx, 30)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_GetCloudAccount_NilDB exercises GetCloudAccount entry.
func TestPostgresStore_GetCloudAccount_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetCloudAccount(ctx, "account-id-123")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_UpdateCloudAccount_AzureAccount verifies UpdatedAt is set
// for an Azure account (exercises a different code path from the existing test).
func TestPostgresStore_UpdateCloudAccount_AzureAccount(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	before := time.Now().Add(-time.Second)
	account := &CloudAccount{
		ID:                  "azure-acc-update",
		Name:                "Azure Prod",
		Provider:            "azure",
		AzureSubscriptionID: "sub-123",
		AzureTenantID:       "tenant-456",
	}

	callWithRecover(func() {
		_ = store.UpdateCloudAccount(ctx, account)
	})

	assert.True(t, account.UpdatedAt.After(before), "UpdatedAt should be refreshed")
}

// TestPostgresStore_DeleteCloudAccount_NilDB exercises DeleteCloudAccount entry.
func TestPostgresStore_DeleteCloudAccount_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_ = store.DeleteCloudAccount(ctx, "acc-to-delete")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_ListCloudAccounts_NoFilter exercises the filter-building code
// path with an empty filter before the DB call.
func TestPostgresStore_ListCloudAccounts_NoFilter(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.ListCloudAccounts(ctx, CloudAccountFilter{})
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_ListCloudAccounts_ProviderFilter exercises the Provider filter
// path in query construction before the DB call.
func TestPostgresStore_ListCloudAccounts_ProviderFilter(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()
	provider := "aws"

	panicked := callWithRecover(func() {
		_, _ = store.ListCloudAccounts(ctx, CloudAccountFilter{Provider: &provider})
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_ListCloudAccounts_EnabledFilter exercises the Enabled filter path.
func TestPostgresStore_ListCloudAccounts_EnabledFilter(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()
	enabled := true

	panicked := callWithRecover(func() {
		_, _ = store.ListCloudAccounts(ctx, CloudAccountFilter{Enabled: &enabled})
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_ListCloudAccounts_SearchFilter exercises the Search filter path.
func TestPostgresStore_ListCloudAccounts_SearchFilter(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.ListCloudAccounts(ctx, CloudAccountFilter{Search: "prod"})
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_ListCloudAccounts_BastionIDFilter exercises the BastionID filter path.
func TestPostgresStore_ListCloudAccounts_BastionIDFilter(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()
	bastionID := "bastion-acc-123"

	panicked := callWithRecover(func() {
		_, _ = store.ListCloudAccounts(ctx, CloudAccountFilter{BastionID: &bastionID})
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_ListCloudAccounts_AllFilters exercises all filter paths together.
func TestPostgresStore_ListCloudAccounts_AllFilters(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()
	provider := "aws"
	enabled := false
	bastionID := "bastion-123"

	panicked := callWithRecover(func() {
		_, _ = store.ListCloudAccounts(ctx, CloudAccountFilter{
			Provider:  &provider,
			Enabled:   &enabled,
			Search:    "staging",
			BastionID: &bastionID,
		})
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_SaveAccountCredential_NilDB exercises the method entry.
func TestPostgresStore_SaveAccountCredential_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_ = store.SaveAccountCredential(ctx, "acc-123", "aws_access_key", "encrypted-blob")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_GetAccountCredential_NilDB exercises the method entry.
func TestPostgresStore_GetAccountCredential_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetAccountCredential(ctx, "acc-123", "aws_access_key")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_DeleteAccountCredentials_NilDB exercises the method entry.
func TestPostgresStore_DeleteAccountCredentials_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_ = store.DeleteAccountCredentials(ctx, "acc-123")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_HasAccountCredentials_NilDB exercises the method entry.
func TestPostgresStore_HasAccountCredentials_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.HasAccountCredentials(ctx, "acc-123")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_GetAccountServiceOverride_NilDB exercises the method entry.
func TestPostgresStore_GetAccountServiceOverride_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetAccountServiceOverride(ctx, "acc-123", "aws", "rds")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_SaveAccountServiceOverride_WithGCPService verifies pre-DB
// init for a GCP override (different provider path).
func TestPostgresStore_SaveAccountServiceOverride_WithGCPService(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	enabled := false
	override := &AccountServiceOverride{
		AccountID: "gcp-acc-123",
		Provider:  "gcp",
		Service:   "compute",
		Enabled:   &enabled,
	}

	panicked := callWithRecover(func() {
		_ = store.SaveAccountServiceOverride(ctx, override)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	assert.NotEmpty(t, override.ID, "ID should be generated before DB call")
	assert.False(t, override.CreatedAt.IsZero(), "CreatedAt should be set")
	assert.False(t, override.UpdatedAt.IsZero(), "UpdatedAt should be set")
}

// TestPostgresStore_SaveAccountServiceOverride_PreservesCreatedAt verifies that
// an existing CreatedAt is not overwritten on update.
func TestPostgresStore_SaveAccountServiceOverride_PreservesCreatedAt(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	originalCreatedAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	override := &AccountServiceOverride{
		ID:        "existing-override-id",
		AccountID: "acc-456",
		Provider:  "azure",
		Service:   "vm",
		CreatedAt: originalCreatedAt,
	}

	panicked := callWithRecover(func() {
		_ = store.SaveAccountServiceOverride(ctx, override)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	assert.Equal(t, originalCreatedAt, override.CreatedAt, "existing CreatedAt should not be overwritten")
	assert.False(t, override.UpdatedAt.IsZero(), "UpdatedAt should be set")
}

// TestPostgresStore_DeleteAccountServiceOverride_NilDB exercises the method entry.
func TestPostgresStore_DeleteAccountServiceOverride_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_ = store.DeleteAccountServiceOverride(ctx, "acc-123", "aws", "rds")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_ListAccountServiceOverrides_NilDB exercises the method entry.
func TestPostgresStore_ListAccountServiceOverrides_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.ListAccountServiceOverrides(ctx, "acc-123")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_SetPlanAccounts_NilDB exercises the Begin-transaction path.
func TestPostgresStore_SetPlanAccounts_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_ = store.SetPlanAccounts(ctx, "plan-123", []string{"acc-1", "acc-2"})
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_GetPlanAccounts_NilDB exercises the method entry.
func TestPostgresStore_GetPlanAccounts_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetPlanAccounts(ctx, "plan-123")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// TestPostgresStore_CreateCloudAccount_GCPAccount verifies GCP account creation
// pre-DB behavior.
func TestPostgresStore_CreateCloudAccount_GCPAccount(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	before := time.Now().Add(-time.Second)
	account := &CloudAccount{
		Name:           "GCP Prod",
		Provider:       "gcp",
		GCPProjectID:   "my-project",
		GCPClientEmail: "sa@my-project.iam.gserviceaccount.com",
		GCPAuthMode:    "service_account",
	}

	panicked := callWithRecover(func() {
		_ = store.CreateCloudAccount(ctx, account)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	assert.NotEmpty(t, account.ID, "ID should be generated")
	assert.True(t, account.CreatedAt.After(before), "CreatedAt should be set")
	assert.True(t, account.UpdatedAt.After(before), "UpdatedAt should be set")
}

// TestPostgresStore_CreateCloudAccount_AzureAccount exercises the Azure account path.
func TestPostgresStore_CreateCloudAccount_AzureAccount(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	account := &CloudAccount{
		Name:                "Azure Prod",
		Provider:            "azure",
		AzureSubscriptionID: "sub-abc",
		AzureTenantID:       "tenant-xyz",
		AzureClientID:       "client-def",
		AzureAuthMode:       "service_principal",
	}

	panicked := callWithRecover(func() {
		_ = store.CreateCloudAccount(ctx, account)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	assert.NotEmpty(t, account.ID)
}

// TestGlobalConfig_SaveWithRIExchangeDefaults exercises the RI exchange default
// value substitution logic in SaveGlobalConfig (applied before DB call).
func TestGlobalConfig_SaveWithRIExchangeDefaults(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	// All RI exchange fields at zero-value to trigger default substitution
	cfg := &GlobalConfig{
		EnabledProviders:               []string{"aws"},
		DefaultTerm:                    3,
		DefaultPayment:                 "no-upfront",
		DefaultCoverage:                80.0,
		RIExchangeMode:                 "",  // empty → should become "manual"
		RIExchangeLookbackDays:         0,   // zero → should become 30
		RIExchangeUtilizationThreshold: 0.0, // zero → should become 95.0
	}

	panicked := callWithRecover(func() {
		_ = store.SaveGlobalConfig(ctx, cfg)
	})

	// The method sets local copies of defaults — cfg fields should be unchanged.
	assert.True(t, panicked, "expected panic with nil db connection")
	// Config struct itself is not modified for RI exchange defaults (local vars used)
	assert.Equal(t, "", cfg.RIExchangeMode)
	assert.Equal(t, 0, cfg.RIExchangeLookbackDays)
	assert.Equal(t, 0.0, cfg.RIExchangeUtilizationThreshold)
}

// TestPostgresStore_SaveRIExchangeRecord_WithCompletedAt verifies that optional
// fields like CompletedAt and ExpiresAt are passed through correctly.
func TestPostgresStore_SaveRIExchangeRecord_WithCompletedAt(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	now := time.Now()
	later := now.Add(24 * time.Hour)
	record := &RIExchangeRecord{
		AccountID:   "acc-789",
		Status:      "completed",
		Mode:        "auto",
		CompletedAt: &now,
		ExpiresAt:   &later,
	}

	panicked := callWithRecover(func() {
		_ = store.SaveRIExchangeRecord(ctx, record)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	assert.NotEmpty(t, record.ID)
}
