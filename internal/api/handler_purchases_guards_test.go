package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	tests := []struct {
		name      string
		rec       config.RecommendationRecord
		wantError bool
		// wantPayment is set ONLY on rows whose input token gets rewritten: it
		// is asserted against rec.Payment after a successful call. "" means the
		// token was already canonical for the provider (case-only differences
		// included).
		wantPayment string
		// wantAdjustment marks the rows whose rewrite changes the BILLING
		// SCHEDULE rather than just the spelling, and so must surface a
		// caller-visible PaymentAdjustment (#1503 follow-up). A rewrite between
		// two spellings of the same schedule (Azure "all-upfront" -> "upfront")
		// costs the customer nothing and must NOT be disclosed. Field-level
		// assertions live in
		// TestValidatePurchaseRecommendation_SurfacesPaymentAdjustment and
		// TestValidatePurchaseRecommendation_NoAdjustmentForScheduleEquivalentRename.
		wantAdjustment bool
	}{
		// --- AWS canonical set ---
		{"valid aws all-upfront 3y", validRec(), false, "", false},
		{"valid aws no-upfront 1y", mutate(func(r *config.RecommendationRecord) { r.Payment = "no-upfront"; r.Term = 1 }), false, "", false},
		{"valid aws partial-upfront", mutate(func(r *config.RecommendationRecord) { r.Payment = "partial-upfront" }), false, "", false},
		{"aws rejects azure-only monthly", mutate(func(r *config.RecommendationRecord) { r.Payment = "monthly" }), true, "", false},
		{"aws rejects azure-only upfront", mutate(func(r *config.RecommendationRecord) { r.Payment = "upfront" }), true, "", false},
		// --- Azure canonical set ---
		{"valid azure upfront", mutate(func(r *config.RecommendationRecord) { r.Provider = "azure"; r.Payment = "upfront" }), false, "", false},
		{"valid azure monthly", mutate(func(r *config.RecommendationRecord) { r.Provider = "azure"; r.Payment = "monthly" }), false, "", false},
		// Legacy AWS-style tokens on Azure are normalized to Azure-canonical
		// before validation. Both of these are pure respellings -- Azure's
		// "upfront" IS all-upfront and its "monthly" IS no-upfront -- so the
		// customer's cash flow is untouched and no adjustment is surfaced.
		{"azure accepts legacy all-upfront (respelled upfront, no adjustment)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "azure"
			r.Payment = "all-upfront"
		}), false, "upfront", false},
		{"azure accepts legacy no-upfront (respelled monthly, no adjustment)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "azure"
			r.Payment = "no-upfront"
		}), false, "monthly", false},
		// partial-upfront has no Azure equivalent; it coerces to monthly (the
		// no-upfront default), never to upfront, so the caller never gets
		// silently billed an all-upfront schedule it did not choose (#1503).
		// This one genuinely changes the schedule, so it must be disclosed.
		{"azure accepts legacy partial-upfront (coerced to monthly, not upfront)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "azure"
			r.Payment = "partial-upfront"
		}), false, "monthly", true},
		{"azure rejects unknown token", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "azure"
			r.Payment = "foo"
		}), true, "", false},
		// --- GCP canonical set (monthly-only) ---
		{"valid gcp monthly", mutate(func(r *config.RecommendationRecord) { r.Provider = "gcp"; r.Payment = "monthly" }), false, "", false},
		// Legacy tokens on GCP are all normalized to monthly. GCP has no
		// upfront tier at all, so every upfront-shaped token really does move
		// the customer onto a different schedule; only no-upfront is a
		// respelling of the schedule they already asked for.
		{"gcp accepts legacy upfront (coerced to monthly)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "gcp"
			r.Payment = "upfront"
		}), false, "monthly", true},
		{"gcp accepts legacy all-upfront (coerced to monthly)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "gcp"
			r.Payment = "all-upfront"
		}), false, "monthly", true},
		{"gcp accepts legacy no-upfront (respelled monthly, no adjustment)", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "gcp"
			r.Payment = "no-upfront"
		}), false, "monthly", false},
		{"gcp rejects unknown token", mutate(func(r *config.RecommendationRecord) {
			r.Provider = "gcp"
			r.Payment = "foo"
		}), true, "", false},
		// --- General ---
		{"payment case-insensitive", mutate(func(r *config.RecommendationRecord) { r.Payment = "All-Upfront" }), false, "", false},
		{"invalid term 7", mutate(func(r *config.RecommendationRecord) { r.Term = 7 }), true, "", false},
		{"invalid term 0", mutate(func(r *config.RecommendationRecord) { r.Term = 0 }), true, "", false},
		{"invalid payment foo", mutate(func(r *config.RecommendationRecord) { r.Payment = "foo" }), true, "", false},
		{"negative count", mutate(func(r *config.RecommendationRecord) { r.Count = -1 }), true, "", false},
		{"negative monthly cost rejected", mutate(func(r *config.RecommendationRecord) {
			m := -1.0
			r.MonthlyCost = &m
		}), true, "", false},
		{"nil monthly cost accepted", mutate(func(r *config.RecommendationRecord) { r.MonthlyCost = nil }), false, "", false},
		{"zero monthly cost accepted", mutate(func(r *config.RecommendationRecord) {
			m := 0.0
			r.MonthlyCost = &m
		}), false, "", false},
		{"zero count", mutate(func(r *config.RecommendationRecord) { r.Count = 0 }), true, "", false},
		{"empty service", mutate(func(r *config.RecommendationRecord) { r.Service = "" }), true, "", false},
		{"empty provider rejected", mutate(func(r *config.RecommendationRecord) { r.Provider = "" }), true, "", false},
		{"all provider rejected", mutate(func(r *config.RecommendationRecord) { r.Provider = "all" }), true, "", false},
		{"unknown provider rejected", mutate(func(r *config.RecommendationRecord) { r.Provider = "ibm" }), true, "", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := tt.rec
			adjustment, err := validatePurchaseRecommendation(&rec, 0)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.wantPayment != "" {
				assert.Equal(t, tt.wantPayment, rec.Payment)
			}
			if tt.wantAdjustment {
				require.NotNil(t, adjustment, "a billing-schedule change must surface a PaymentAdjustment")
				assert.Equal(t, tt.wantPayment, adjustment.AppliedPaymentOption)
			} else {
				assert.Nil(t, adjustment, "the billing schedule did not change, so nothing should be disclosed")
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
	_, err := validatePurchaseRecommendation(&rec, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "azure")
	assert.Contains(t, err.Error(), "upfront")
	assert.Contains(t, err.Error(), "monthly")
}

// TestValidatePurchaseRecommendation_NormalizationWarning is a regression test
// for #1504: the doc comment on config.NormalizePaymentOption
// (internal/config/validation.go) states that the caller is expected to WARN
// when a raw payment-option token differs from its normalized canonical form,
// so an operator can audit a money-affecting coercion. validatePurchaseRecommendation
// is that caller; it must emit a WARN log when normalization actually changes
// the value (raw != canonical). This test captures the default logger's
// output via captureDefaultLog (defined in handler_accounts_test.go, package
// api) and asserts the log line is emitted with the provider, service, raw
// and canonical values, and rec index.
//
// Not run with t.Parallel(): captureDefaultLog mutates the shared default
// logger's output, which would race against other SetOutput-using tests if
// parallelized (see TestValidatePlanAccountProviders_GetAccountDBError_NoPIILeak).
func TestValidatePurchaseRecommendation_NormalizationWarning(t *testing.T) {
	logBuf := captureDefaultLog(t)

	// Azure partial-upfront has no direct Azure equivalent; NormalizePaymentOption
	// coerces it to "monthly" (#1503). This is a real raw != canonical
	// transition and must be logged.
	rec := validRec()
	rec.Provider = "azure"
	rec.Payment = "partial-upfront"
	_, err := validatePurchaseRecommendation(&rec, 7)
	require.NoError(t, err)
	// The money-affecting coercion itself must be unchanged by adding the log.
	assert.Equal(t, "monthly", rec.Payment, "coercion behavior must stay partial-upfront -> monthly, not upfront")

	logged := logBuf.String()
	assert.Contains(t, logged, "[WARN]")
	assert.Contains(t, logged, "rec 7 (azure/ec2)", "warning must identify the rec index, provider and service for audit")
	assert.Contains(t, logged, `raw="partial-upfront"`)
	assert.Contains(t, logged, `canonical="monthly"`)
}

// TestValidatePurchaseRecommendation_NoWarningWhenAlreadyCanonical guards that
// an already-canonical payment option (no real normalization) does not emit a
// WARN log: only an actual raw->canonical transition should be observable, so
// operators aren't flooded with noise on the common case.
//
// Not run with t.Parallel(); see TestValidatePurchaseRecommendation_NormalizationWarning.
func TestValidatePurchaseRecommendation_NoWarningWhenAlreadyCanonical(t *testing.T) {
	logBuf := captureDefaultLog(t)

	rec := validRec()
	rec.Provider = "azure"
	rec.Payment = "monthly" // already canonical for azure; no normalization occurs
	adjustment, err := validatePurchaseRecommendation(&rec, 0)
	require.NoError(t, err)
	assert.Nil(t, adjustment, "already-canonical payment option must not surface an adjustment")
	assert.Equal(t, "monthly", rec.Payment)

	assert.Empty(t, logBuf.String(), "no normalization occurred, so nothing should be logged")
}

// TestValidatePurchaseRecommendation_SurfacesPaymentAdjustment pins the
// caller-facing coercion notice (#1503 follow-up): when the web execute path
// normalizes a payment option onto a DIFFERENT provider-canonical token, the
// returned PaymentAdjustment must identify the rec and carry the requested
// token, the applied token, and a reason naming both, so the API response can
// tell the caller what actually happened to this money-affecting field instead
// of only WARN-logging it for operators.
func TestValidatePurchaseRecommendation_SurfacesPaymentAdjustment(t *testing.T) {
	t.Parallel()
	rec := validRec()
	rec.Provider = "azure"
	rec.Payment = "partial-upfront"
	adjustment, err := validatePurchaseRecommendation(&rec, 3)
	require.NoError(t, err)
	require.NotNil(t, adjustment, "azure partial-upfront is coerced and must surface an adjustment")
	assert.Equal(t, 3, adjustment.RecIndex)
	assert.Equal(t, "azure", adjustment.Provider)
	assert.Equal(t, rec.Service, adjustment.Service)
	assert.Equal(t, "partial-upfront", adjustment.RequestedPaymentOption)
	assert.Equal(t, "monthly", adjustment.AppliedPaymentOption)
	// The reason must be self-explanatory to the caller: it names the rejected
	// token, the applied token, and the provider whose billing model forced it.
	assert.Contains(t, adjustment.Reason, `"partial-upfront"`)
	assert.Contains(t, adjustment.Reason, `"monthly"`)
	assert.Contains(t, adjustment.Reason, "azure")
	// The adjustment must agree with the actual mutation applied to the rec:
	// applied means applied, not merely advertised.
	assert.Equal(t, adjustment.AppliedPaymentOption, rec.Payment)
}

// TestValidatePurchaseRecommendation_NoAdjustmentForScheduleEquivalentRename
// guards the other half of the #1503 disclosure contract: a coercion that only
// respells a token in the target provider's vocabulary must NOT be reported as
// an adjustment, because nothing about the customer's cash flow changed.
//
// This is not hypothetical noise-avoidance. The fan-out purchase modal's
// per-bucket Payment dropdown is populated from paymentOptionsFor
// (frontend/src/lib/purchase-compatibility.ts), whose candidate list has no
// "upfront" entry — so an Azure bucket the user pays upfront ALWAYS submits
// the AWS-style "all-upfront". Reporting that rename would put a sticky
// "Billing schedule adjusted" warning on every ordinary Azure upfront
// purchase, claiming a change that did not happen and drowning out the
// partial-upfront case that did.
//
// The operator WARN is deliberately still emitted for these: a non-canonical
// token on the wire is an upstream input bug worth auditing even when it costs
// the customer nothing.
//
// Not run with t.Parallel(): captureDefaultLog mutates the shared default
// logger's output (see TestValidatePurchaseRecommendation_NormalizationWarning).
func TestValidatePurchaseRecommendation_NoAdjustmentForScheduleEquivalentRename(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		payment     string
		wantPayment string
	}{
		// Azure spells AWS's all-upfront "upfront": same single charge at
		// purchase, different word.
		{"azure all-upfront is upfront respelled", "azure", "all-upfront", "upfront"},
		// Azure spells AWS's no-upfront "monthly": same per-period billing.
		{"azure no-upfront is monthly respelled", "azure", "no-upfront", "monthly"},
		// GCP likewise has only the recurring schedule spelled "monthly".
		{"gcp no-upfront is monthly respelled", "gcp", "no-upfront", "monthly"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logBuf := captureDefaultLog(t)

			rec := validRec()
			rec.Provider = tc.provider
			rec.Payment = tc.payment
			adjustment, err := validatePurchaseRecommendation(&rec, 0)
			require.NoError(t, err)
			assert.Nil(t, adjustment,
				"%s bills identically to %s; a rename must not be disclosed as a billing-schedule change",
				tc.payment, tc.wantPayment)
			// The canonicalization itself must still happen.
			assert.Equal(t, tc.wantPayment, rec.Payment)
			// ...and the operator-facing audit log must still fire.
			assert.Contains(t, logBuf.String(), "[WARN]",
				"a non-canonical token on the wire is still an upstream input bug worth logging")
		})
	}
}

// TestValidatePurchaseRecommendation_AdjustmentWhenScheduleChanges covers the
// non-Azure half of the same contract: GCP commitments are monthly-only, so an
// upfront-shaped token really does move the customer onto a different billing
// schedule and MUST be disclosed.
func TestValidatePurchaseRecommendation_AdjustmentWhenScheduleChanges(t *testing.T) {
	t.Parallel()
	for _, payment := range []string{"all-upfront", "upfront", "partial-upfront"} {
		t.Run(payment, func(t *testing.T) {
			t.Parallel()
			rec := validRec()
			rec.Provider = "gcp"
			rec.Payment = payment
			adjustment, err := validatePurchaseRecommendation(&rec, 0)
			require.NoError(t, err)
			require.NotNil(t, adjustment,
				"gcp has no upfront billing tier, so %q lands on a different schedule and must be disclosed", payment)
			assert.Equal(t, payment, adjustment.RequestedPaymentOption)
			assert.Equal(t, "monthly", adjustment.AppliedPaymentOption)
			assert.Equal(t, "monthly", rec.Payment)
		})
	}
}

// TestHandler_executePurchase_SurfacesPaymentAdjustments is the response-level
// regression test for the #1503 follow-up: an Azure partial-upfront purchase
// submitted through the real executePurchase handler must return a
// payment_adjustments entry telling the caller the request was applied as
// monthly; a green validator-level test alone could not prove the notice
// survives to the response body the client actually sees.
func TestHandler_executePurchase_SurfacesPaymentAdjustments(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdminPurchaser()
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		// rec 0 is already canonical (azure/monthly); rec 1 carries the #1503
		// partial-upfront token and must be the only rec surfaced.
		Body: `{"recommendations": [` +
			`{"id": "rec-1", "provider": "azure", "service": "vm", "count": 1, "term": 1, "payment": "monthly", "upfront_cost": 100.0, "savings": 50.0},` +
			`{"id": "rec-2", "provider": "azure", "service": "vm", "count": 1, "term": 1, "payment": "partial-upfront", "upfront_cost": 200.0, "savings": 25.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	require.NoError(t, err)

	resultMap := result.(map[string]any)
	adjustments, ok := resultMap["payment_adjustments"].([]PaymentAdjustment)
	require.True(t, ok, "response must carry payment_adjustments when a coercion occurred")
	require.Len(t, adjustments, 1, "only the coerced rec must be surfaced")
	adj := adjustments[0]
	assert.Equal(t, 1, adj.RecIndex, "adjustment must point at the coerced rec's position")
	assert.Equal(t, "azure", adj.Provider)
	assert.Equal(t, "vm", adj.Service)
	assert.Equal(t, "partial-upfront", adj.RequestedPaymentOption)
	assert.Equal(t, "monthly", adj.AppliedPaymentOption)
	assert.NotEmpty(t, adj.Reason)
}

// TestHandler_executePurchase_NoAdjustmentsWhenCanonical guards the
// backward-compat contract of the #1503 follow-up: when every payment option
// is already provider-canonical, the payment_adjustments key must be ABSENT
// (not an empty array), so existing clients see a byte-identical response on
// the common case.
func TestHandler_executePurchase_NoAdjustmentsWhenCanonical(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdminPurchaser()
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"recommendations": [{"id": "rec-1", "provider": "azure", "service": "vm", "count": 1, "term": 1, "payment": "monthly", "upfront_cost": 100.0, "savings": 50.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	require.NoError(t, err)

	resultMap := result.(map[string]any)
	assert.NotContains(t, resultMap, "payment_adjustments",
		"canonical-only request must not carry the payment_adjustments key at all")
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
