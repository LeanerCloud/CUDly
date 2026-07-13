package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
)

// strptr is a tiny local helper to take the address of a string literal.
func strptr(s string) *string { return &s }

func validRec() config.RecommendationRecord {
	return config.RecommendationRecord{
		ID:           "rec-1",
		Provider:     "aws",
		Service:      "ec2",
		Region:       "us-east-1",
		ResourceType: "t3.medium",
		Count:        2,
		Term:         3,
		Payment:      "all-upfront",
	}
}

// --- #643: per-rec Term/Payment/Count/Provider/Service validation ---

func TestValidatePurchaseRecommendation(t *testing.T) {
	t.Parallel()
	mutate := func(f func(r *config.RecommendationRecord)) config.RecommendationRecord {
		r := validRec()
		f(&r)
		return r
	}
	tests := []struct { //nolint:govet // fieldalignment: reorder would break API/readability
		name      string
		rec       config.RecommendationRecord
		wantError bool
	}{
		// --- AWS canonical set ---
		{"valid aws all-upfront 3y", validRec(), false},
		{"valid aws no-upfront 1y", mutate(func(r *config.RecommendationRecord) { r.Payment = "no-upfront"; r.Term = 1 }), false},
		{"valid aws partial-upfront", mutate(func(r *config.RecommendationRecord) { r.Payment = "partial-upfront" }), false},
		{"aws rejects azure-only monthly", mutate(func(r *config.RecommendationRecord) { r.Payment = "monthly" }), true},
		{"aws rejects azure-only upfront", mutate(func(r *config.RecommendationRecord) { r.Payment = "upfront" }), true},
		// --- Azure canonical set ---
		{"valid azure upfront", mutate(func(r *config.RecommendationRecord) { r.Provider = "azure"; r.Payment = "upfront" }), false},
		{"valid azure monthly", mutate(func(r *config.RecommendationRecord) { r.Provider = "azure"; r.Payment = "monthly" }), false},
		// Legacy AWS-style tokens on Azure are normalized to Azure-canonical before validation.
		{"azure accepts legacy all-upfront (coerced to upfront)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "azure"
			r.Payment = "all-upfront"
		}), false},
		{"azure accepts legacy no-upfront (coerced to monthly)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "azure"
			r.Payment = "no-upfront"
		}), false},
		{"azure accepts legacy partial-upfront (coerced to upfront)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "azure"
			r.Payment = "partial-upfront"
		}), false},
		{"azure rejects unknown token", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "azure"
			r.Payment = "foo"
		}), true},
		// --- GCP canonical set (monthly-only) ---
		{"valid gcp monthly", mutate(func(r *config.RecommendationRecord) { r.Provider = "gcp"; r.Payment = "monthly" }), false},
		// Legacy tokens on GCP are all normalized to monthly.
		{"gcp accepts legacy upfront (coerced to monthly)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "gcp"
			r.Payment = "upfront"
		}), false},
		{"gcp accepts legacy all-upfront (coerced to monthly)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "gcp"
			r.Payment = "all-upfront"
		}), false},
		{"gcp accepts legacy no-upfront (coerced to monthly)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "gcp"
			r.Payment = "no-upfront"
		}), false},
		{"gcp rejects unknown token", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "gcp"
			r.Payment = "foo"
		}), true},
		// --- General ---
		{"payment case-insensitive", mutate(func(r *config.RecommendationRecord) { r.Payment = "All-Upfront" }), false},
		{"invalid term 7", mutate(func(r *config.RecommendationRecord) { r.Term = 7 }), true},
		{"invalid term 0", mutate(func(r *config.RecommendationRecord) { r.Term = 0 }), true},
		{"invalid payment foo", mutate(func(r *config.RecommendationRecord) { r.Payment = "foo" }), true},
		{"negative count", mutate(func(r *config.RecommendationRecord) { r.Count = -1 }), true},
		{"zero count", mutate(func(r *config.RecommendationRecord) { r.Count = 0 }), true},
		{"empty service", mutate(func(r *config.RecommendationRecord) { r.Service = "" }), true},
		{"empty provider rejected", mutate(func(r *config.RecommendationRecord) { r.Provider = "" }), true},
		{"all provider rejected", mutate(func(r *config.RecommendationRecord) { r.Provider = "all" }), true},
		{"unknown provider rejected", mutate(func(r *config.RecommendationRecord) { r.Provider = "ibm" }), true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := tt.rec
			err := validatePurchaseRecommendation(&rec, 0)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestValidatePurchaseRecommendation_ErrorMessage verifies that a mismatched
// (provider, payment-option) pair produces a 400 error whose message names the
// provider and lists the valid options, matching the plan-validator shape
// required by issue #717.
func TestValidatePurchaseRecommendation_ErrorMessage(t *testing.T) {
	t.Parallel()
	rec := validRec()
	rec.Provider = "azure"
	rec.Payment = "foo" // not in azure canonical set and has no normalization alias
	err := validatePurchaseRecommendation(&rec, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "azure")
	assert.Contains(t, err.Error(), "upfront")
	assert.Contains(t, err.Error(), "monthly")
}

// The per-rec #643 validation is wired into the web execute boundary
// (validateExecutePurchaseRequest), NOT the shared validateAndTotalRecommendations
// which the retry path also calls with replayed recs. This test pins that
// separation: validateAndTotalRecommendations must still accept a zero-count rec
// so the retry path (which replays already-validated recs that may carry Count:0
// shorthand) is not re-gated by the submit-time rules.
func TestValidateAndTotalRecommendations_DoesNotGateCount(t *testing.T) {
	t.Parallel()
	zero := validRec()
	zero.Count = 0
	_, _, err := validateAndTotalRecommendations([]config.RecommendationRecord{zero})
	require.NoError(t, err)
}

// --- #644: submit-time idempotency key + duplicate lookup ---

func TestPurchaseIdempotencyKey_StableAndDiscriminating(t *testing.T) {
	t.Parallel()
	recsA := []config.RecommendationRecord{validRec()}
	// Same content, different slice order must hash the same.
	r2 := validRec()
	r2.ID = "rec-2"
	r2.Region = "eu-west-1"
	ordered := []config.RecommendationRecord{validRec(), r2}
	reordered := []config.RecommendationRecord{r2, validRec()}

	assert.Equal(t,
		purchaseIdempotencyKey("user-1", recsA, 100),
		purchaseIdempotencyKey("user-1", recsA, 100),
		"identical input must hash identically")
	assert.Equal(t,
		purchaseIdempotencyKey("user-1", ordered, 100),
		purchaseIdempotencyKey("user-1", reordered, 100),
		"slice order must not change the key")

	// Discriminating dimensions.
	assert.NotEqual(t, purchaseIdempotencyKey("user-1", recsA, 100), purchaseIdempotencyKey("user-2", recsA, 100), "creator")
	assert.NotEqual(t, purchaseIdempotencyKey("user-1", recsA, 100), purchaseIdempotencyKey("user-1", recsA, 50), "capacity")

	scaled := []config.RecommendationRecord{validRec()}
	scaled[0].Count = 1
	assert.NotEqual(t, purchaseIdempotencyKey("user-1", recsA, 100), purchaseIdempotencyKey("user-1", scaled, 100), "count")

	acctA := []config.RecommendationRecord{validRec()}
	acctA[0].CloudAccountID = strptr("acct-A")
	acctB := []config.RecommendationRecord{validRec()}
	acctB[0].CloudAccountID = strptr("acct-B")
	assert.NotEqual(t, purchaseIdempotencyKey("user-1", acctA, 100), purchaseIdempotencyKey("user-1", acctB, 100), "account")
}

func TestFindDuplicatePendingExecution(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	creator := "11111111-1111-1111-1111-111111111111"
	recs := []config.RecommendationRecord{validRec()}
	key := purchaseIdempotencyKey(creator, recs, 100)

	makeExec := func(id string, age time.Duration, src string, c *string, capacity int) config.PurchaseExecution {
		// Copy recs so a subtest that mutates exec.Recommendations does not
		// corrupt the shared slice used to compute `key` above.
		recsCopy := append([]config.RecommendationRecord(nil), recs...)
		return config.PurchaseExecution{
			ExecutionID:     id,
			Status:          "pending",
			Source:          src,
			ScheduledDate:   now.Add(-age),
			Recommendations: recsCopy,
			CreatedByUserID: c,
			CapacityPercent: capacity,
		}
	}

	t.Run("matching recent web execution is a duplicate", func(t *testing.T) {
		store := new(MockConfigStore)
		store.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{
			makeExec("exec-dup", 30*time.Second, common.PurchaseSourceWeb, &creator, 100),
		}, nil)
		h := &Handler{config: store}
		dup, err := h.findDuplicatePendingExecution(ctx, creator, key, now)
		require.NoError(t, err)
		require.NotNil(t, dup)
		assert.Equal(t, "exec-dup", dup.ExecutionID)
	})

	t.Run("outside the window is not a duplicate", func(t *testing.T) {
		store := new(MockConfigStore)
		store.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{
			makeExec("exec-old", purchaseIdempotencyWindow+time.Minute, common.PurchaseSourceWeb, &creator, 100),
		}, nil)
		h := &Handler{config: store}
		dup, err := h.findDuplicatePendingExecution(ctx, creator, key, now)
		require.NoError(t, err)
		assert.Nil(t, dup)
	})

	t.Run("different creator is not a duplicate", func(t *testing.T) {
		other := "22222222-2222-2222-2222-222222222222"
		store := new(MockConfigStore)
		store.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{
			makeExec("exec-other", 10*time.Second, common.PurchaseSourceWeb, &other, 100),
		}, nil)
		h := &Handler{config: store}
		dup, err := h.findDuplicatePendingExecution(ctx, creator, key, now)
		require.NoError(t, err)
		assert.Nil(t, dup)
	})

	t.Run("non-web source is skipped", func(t *testing.T) {
		store := new(MockConfigStore)
		store.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{
			makeExec("exec-cli", 10*time.Second, "cudly-cli", &creator, 100),
		}, nil)
		h := &Handler{config: store}
		dup, err := h.findDuplicatePendingExecution(ctx, creator, key, now)
		require.NoError(t, err)
		assert.Nil(t, dup)
	})

	t.Run("distinct rec set is not a duplicate", func(t *testing.T) {
		differing := makeExec("exec-diff", 10*time.Second, common.PurchaseSourceWeb, &creator, 100)
		differing.Recommendations[0].Count = 99
		store := new(MockConfigStore)
		store.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{differing}, nil)
		h := &Handler{config: store}
		dup, err := h.findDuplicatePendingExecution(ctx, creator, key, now)
		require.NoError(t, err)
		assert.Nil(t, dup)
	})

	t.Run("lookup error is surfaced, not swallowed", func(t *testing.T) {
		store := new(MockConfigStore)
		store.On("GetPendingExecutions", ctx).Return(nil, errors.New("db down"))
		h := &Handler{config: store}
		dup, err := h.findDuplicatePendingExecution(ctx, creator, key, now)
		require.Error(t, err)
		assert.Nil(t, dup)
	})
}

func TestBuildDuplicatePurchaseResponse(t *testing.T) {
	t.Parallel()
	sent := time.Now()
	ex := &config.PurchaseExecution{
		ExecutionID:      "exec-1",
		Status:           "pending",
		Recommendations:  []config.RecommendationRecord{validRec()},
		TotalUpfrontCost: 123.45,
		EstimatedSavings: 67.89,
		NotificationSent: &sent,
	}
	resp := buildDuplicatePurchaseResponse(ex)
	assert.Equal(t, "exec-1", resp["execution_id"])
	assert.Equal(t, "pending", resp["status"])
	assert.Equal(t, 1, resp["recommendation_count"])
	assert.Equal(t, true, resp["duplicate"])
	assert.Equal(t, true, resp["email_sent"])

	ex.NotificationSent = nil
	assert.Equal(t, false, buildDuplicatePurchaseResponse(ex)["email_sent"])
}

// --- #647: capacity_percent consistency with scaled rec counts ---

func TestValidateCapacityConsistency(t *testing.T) {
	t.Parallel()
	// recWith builds a rec carrying both the scaled count and the pre-scaling
	// recommended count so the cross-check has something to verify.
	recWith := func(count, recommended int) config.RecommendationRecord {
		r := validRec()
		r.Count = count
		r.RecommendedCount = recommended
		return r
	}
	tests := []struct {
		name      string
		recs      []config.RecommendationRecord
		capacity  int
		wantError bool
	}{
		{"full capacity matches", []config.RecommendationRecord{recWith(10, 10)}, 100, false},
		{"50 percent floors to match", []config.RecommendationRecord{recWith(5, 10)}, 50, false},
		{"50 percent of odd floors down", []config.RecommendationRecord{recWith(5, 11)}, 50, false}, // floor(11*50/100)=5
		{"mismatch claims 50 but sent full", []config.RecommendationRecord{recWith(10, 10)}, 50, true},
		{"mismatch claims full but scaled", []config.RecommendationRecord{recWith(5, 10)}, 100, true},
		{"absent recommended_count is skipped", []config.RecommendationRecord{recWith(5, 0)}, 50, false},
		{"one consistent one inconsistent rejects", []config.RecommendationRecord{recWith(5, 10), recWith(10, 10)}, 50, true},
		{"empty recs ok", nil, 100, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateCapacityConsistency(tt.recs, tt.capacity)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
