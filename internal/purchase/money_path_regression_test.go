package purchase

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// awsAccessKeyCredStore returns a credential store that resolves a static AWS
// access-key blob, so the per-account credential resolution in the fan-out path
// succeeds without real cloud calls.
func awsAccessKeyCredStore() *MockCredentialStore {
	return &MockCredentialStore{
		LoadRawFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte(`{"access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_access_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}`), nil
		},
	}
}

// captureIdempotencyTokens runs a single-account execution for exec and returns
// the IdempotencyToken each PurchaseCommitment call received, keyed by resource
// type. It wires a provider that records opts.IdempotencyToken on every call.
func captureIdempotencyTokens(t *testing.T, exec *config.PurchaseExecution) map[string]string {
	t.Helper()
	ctx := context.Background()

	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProviderInst := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	plan := &config.PurchasePlan{Name: "Direct purchase"}

	mockStore.SavePurchaseExecutionFn = func(_ context.Context, _ *config.PurchaseExecution) error { return nil }
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)

	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProviderInst, nil)
	mockProviderInst.On("GetServiceClient", mock.Anything, common.ServiceEC2, mock.Anything).Return(mockServiceClient, nil)

	var mu sync.Mutex
	tokens := map[string]string{}
	mockServiceClient.On("PurchaseCommitment", mock.Anything,
		mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions"),
	).Run(func(args mock.Arguments) {
		rec := args.Get(1).(common.Recommendation)
		opts := args.Get(2).(common.PurchaseOptions)
		mu.Lock()
		tokens[rec.ResourceType] = opts.IdempotencyToken
		mu.Unlock()
	}).Return(common.PurchaseResult{Success: true, CommitmentID: "ri-ok"}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	// provCfg is nil: the mock factory matches mock.Anything for it and the
	// real factory is never reached.
	_, _, errs, procErr := manager.processPurchaseRecommendations(ctx, exec, plan, "111111111111", nil)
	require.NoError(t, procErr, "processPurchaseRecommendations must not fail in the happy path")
	require.Empty(t, errs, "all recs must commit so the captured token reflects a real purchase")
	return tokens
}

// TestRetryReusesIdempotencyToken is the issue #1012 regression guard: a retry
// of a failed-but-landed execution must derive the SAME per-rec provider
// idempotency token as the original attempt, so the provider dedupes and the
// commitment is never bought twice.
//
// Pre-fix, the token derived purely from the execution_id, which the retry
// regenerates as a fresh UUID — so this test would observe DIFFERENT tokens and
// fail. Post-fix, the retry copies the stable IdempotencyKey verbatim, so the
// tokens match.
func TestRetryReusesIdempotencyToken(t *testing.T) {
	recs := []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 300, Selected: true},
	}

	original := &config.PurchaseExecution{
		ExecutionID:     "exec-original",
		IdempotencyKey:  "stable-lineage-key-abc",
		Source:          common.PurchaseSourceWeb,
		Recommendations: append([]config.RecommendationRecord(nil), recs...),
	}
	// The retry successor: a FRESH ExecutionID (as persistRetryExecution mints),
	// but the SAME IdempotencyKey copied verbatim from the predecessor.
	retry := &config.PurchaseExecution{
		ExecutionID:     "exec-retry-NEW-uuid",
		IdempotencyKey:  "stable-lineage-key-abc",
		Source:          common.PurchaseSourceWeb,
		Recommendations: append([]config.RecommendationRecord(nil), recs...),
	}

	origTokens := captureIdempotencyTokens(t, original)
	retryTokens := captureIdempotencyTokens(t, retry)

	require.NotEmpty(t, origTokens["m5.large"], "original must derive a non-empty token")
	assert.Equal(t, origTokens["m5.large"], retryTokens["m5.large"],
		"retry must derive the SAME idempotency token as the original (issue #1012) despite a fresh ExecutionID")
	// Sanity: the token is derived from the stable lineage key, not the exec ID.
	assert.Equal(t, common.DeriveIdempotencyToken("stable-lineage-key-abc", 0), origTokens["m5.large"])
}

// TestLegacyRowFallsBackToExecutionID guards the migration-000066 legacy path:
// a row with no IdempotencyKey (NULL column) must derive its token from the
// ExecutionID, identical to the pre-fix behavior for a single un-retried
// execution, so old in-flight rows keep working.
func TestLegacyRowFallsBackToExecutionID(t *testing.T) {
	legacy := &config.PurchaseExecution{
		ExecutionID: "legacy-exec-id",
		// IdempotencyKey deliberately empty (legacy NULL).
		Source: common.PurchaseSourceWeb,
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 300, Selected: true},
		},
	}
	tokens := captureIdempotencyTokens(t, legacy)
	assert.Equal(t, common.DeriveIdempotencyToken("legacy-exec-id", 0), tokens["m5.large"],
		"a legacy row (no idempotency_key) must fall back to ExecutionID for the token")
}

// TestMultiAccountSeedsStablePerAccountKey is the issue #1012 / H1 guard for the
// multi-account fan-out: a re-drive of a multi-account plan must reproduce the
// same per-account idempotency key (root lineage key + account ID), NOT a fresh
// UUID. Pre-fix, each account row minted a random UUID at execution time, so a
// second run derived a different token and double-bought. This test asserts the
// per-account IdempotencyKey is deterministic across two runs of the same root.
func TestMultiAccountSeedsStablePerAccountKey(t *testing.T) {
	ctx := context.Background()
	accounts := []config.CloudAccount{
		{ID: "acct-A", Name: "A", Provider: "aws", ExternalID: "111111111111", AWSAuthMode: "access_keys"},
		{ID: "acct-B", Name: "B", Provider: "aws", ExternalID: "222222222222", AWSAuthMode: "access_keys"},
	}

	runOnce := func() map[string]string {
		mockStore := new(MockConfigStore)
		mockEmail := new(MockEmailSender)
		mockFactory := new(MockProviderFactory)
		mockProviderInst := new(MockProvider)
		mockServiceClient := new(MockServiceClient)

		baseExec := &config.PurchaseExecution{
			ExecutionID:    "root-exec",
			IdempotencyKey: "root-lineage",
			PlanID:         "plan-x",
			Source:         common.PurchaseSourceWeb,
			Recommendations: []config.RecommendationRecord{
				{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 300, Selected: true},
			},
		}
		plan := &config.PurchasePlan{ID: "plan-x", Name: "Plan X"}

		var mu sync.Mutex
		perAccountKey := map[string]string{}
		mockStore.SavePurchaseExecutionFn = func(_ context.Context, e *config.PurchaseExecution) error {
			mu.Lock()
			if e.CloudAccountID != nil {
				perAccountKey[*e.CloudAccountID] = e.IdempotencyKey
			}
			mu.Unlock()
			return nil
		}
		mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
		mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)

		mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProviderInst, nil)
		mockProviderInst.On("GetServiceClient", mock.Anything, common.ServiceEC2, mock.Anything).Return(mockServiceClient, nil)
		mockServiceClient.On("PurchaseCommitment", mock.Anything, mock.Anything, mock.Anything).
			Return(common.PurchaseResult{Success: true, CommitmentID: "ri-ok"}, nil)

		manager := &Manager{
			config:          mockStore,
			email:           mockEmail,
			providerFactory: mockFactory,
			credStore:       awsAccessKeyCredStore(),
			dashboardURL:    "https://dashboard.example.com",
		}
		require.NoError(t, manager.executeMultiAccount(ctx, baseExec, plan, accounts))
		return perAccountKey
	}

	run1 := runOnce()
	run2 := runOnce()

	require.Equal(t, "root-lineage:acct-A", run1["acct-A"], "per-account key must be lineage+accountID, not a UUID")
	require.Equal(t, "root-lineage:acct-B", run1["acct-B"])
	assert.Equal(t, run1, run2, "two runs of the same root must reproduce identical per-account idempotency keys (issue #1012/H1)")
}

// TestSQSRedeliveryDoesNotDoubleExecute is the issue #1013 (C2) regression
// guard: a redelivered execute_purchase SQS message must NOT execute the row a
// second time. The first delivery wins the CAS to "running"; the redelivery
// loses the CAS (the row is no longer in an executable state) and is acked
// without touching the cloud.
//
// Pre-fix, handleExecutePurchase only checked status in (approved,pending) with
// no atomic transition, so both deliveries would execute. This test asserts the
// purchase runs exactly once and the second delivery is a benign no-op.
func TestSQSRedeliveryDoesNotDoubleExecute(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProviderInst := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	newPending := func() *config.PurchaseExecution {
		return &config.PurchaseExecution{
			ExecutionID:    "exec-dup",
			IdempotencyKey: "lineage-dup",
			Status:         "pending",
			Recommendations: []config.RecommendationRecord{
				{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 300, Selected: true},
			},
		}
	}

	// Both deliveries read the row as "pending" from the DB (at-least-once SQS:
	// pre-fix nothing CASes it to running before the cloud call, so a redelivery
	// re-reads a still-claimable row). Each GetExecutionByID returns a FRESH
	// pending copy, faithfully modeling the real double-delivery scenario.
	mockStore.On("GetExecutionByID", ctx, "exec-dup").Return(newPending(), nil).Once()
	mockStore.On("GetExecutionByID", ctx, "exec-dup").Return(newPending(), nil).Once()

	// First claim wins: pending -> running, returns the running row. The second
	// claim (redelivery) loses with ErrExecutionNotInExpectedStatus, because the
	// row already left the executable set. testify replays Once() expectations in
	// declaration order, so the first call gets the win and the second the loss.
	running := newPending()
	running.Status = "running"
	mockStore.On("TransitionExecutionStatus", ctx, "exec-dup",
		[]string{"approved", "pending", "notified"}, "running", (*string)(nil)).Return(running, nil).Once()
	mockStore.On("TransitionExecutionStatus", ctx, "exec-dup",
		[]string{"approved", "pending", "notified"}, "running", (*string)(nil)).
		Return(nil, fmt.Errorf("%w: row already running", config.ErrExecutionNotInExpectedStatus)).Once()

	mockStore.SavePurchaseExecutionFn = func(_ context.Context, _ *config.PurchaseExecution) error { return nil }
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockStore.On("GetPurchasePlan", ctx, mock.Anything).Return(&config.PurchasePlan{Name: "p", AutoPurchase: true}, nil).Maybe()

	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProviderInst, nil)
	mockProviderInst.On("GetServiceClient", mock.Anything, common.ServiceEC2, mock.Anything).Return(mockServiceClient, nil)

	var purchaseCalls int
	var mu sync.Mutex
	mockServiceClient.On("PurchaseCommitment", mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { mu.Lock(); purchaseCalls++; mu.Unlock() }).
		Return(common.PurchaseResult{Success: true, CommitmentID: "ri-ok"}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	msg := AsyncMessage{Type: MessageTypeExecutePurchase, ExecutionID: "exec-dup"}
	require.NoError(t, manager.handleExecutePurchase(ctx, &msg), "first delivery executes cleanly")
	require.NoError(t, manager.handleExecutePurchase(ctx, &msg), "redelivery is a benign no-op (acked)")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, purchaseCalls, "the cloud purchase must run EXACTLY once across both deliveries (issue #1013)")
}

// TestMultiAccountPartialSuccessIsAcked is the issue #1014 regression guard: a
// multi-account run where at least one account committed must NOT surface as a
// flat failure to the SQS handler (which would trigger redelivery and a
// double-buy). The handler must return nil (ack) when committed >= 1.
func TestMultiAccountPartialSuccessIsAcked(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProviderInst := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	accounts := []config.CloudAccount{
		{ID: "acct-ok", Name: "OK", Provider: "aws", ExternalID: "111111111111", AWSAuthMode: "access_keys"},
		// acct-bad uses a non-access_keys auth mode with no STS client wired, so
		// its credential resolution fails deterministically (committed=false) —
		// modeling "one account in the fan-out fails" without racing the mock.
		{ID: "acct-bad", Name: "BAD", Provider: "aws", ExternalID: "222222222222", AWSAuthMode: "role_arn"},
	}

	exec := &config.PurchaseExecution{
		ExecutionID:    "root-partial",
		IdempotencyKey: "lineage-partial",
		Status:         "pending",
		PlanID:         "plan-x",
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 300, Selected: true},
		},
	}
	plan := &config.PurchasePlan{ID: "plan-x", Name: "Plan X", AutoPurchase: true}

	mockStore.On("GetExecutionByID", ctx, "root-partial").Return(exec, nil)
	running := *exec
	running.Status = "running"
	mockStore.On("TransitionExecutionStatus", ctx, "root-partial",
		[]string{"approved", "pending", "notified"}, "running", (*string)(nil)).Return(&running, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-x").Return(plan, nil).Maybe()
	// GetPlanAccounts is served by the Fn hook, not a testify expectation.
	mockStore.GetPlanAccountsFn = func(_ context.Context, _ string) ([]config.CloudAccount, error) {
		return accounts, nil
	}
	// updatePlanProgress only runs on a fully-clean run (execErr == nil); a
	// partial multi-account run skips it. Marked Maybe so a (non-deterministic)
	// all-success ordering of the two account goroutines doesn't fail the mock.
	mockStore.On("CompletePlanStep", ctx, "plan-x", mock.Anything).Return(nil).Maybe()

	var savedRoot *config.PurchaseExecution
	mockStore.SavePurchaseExecutionFn = func(_ context.Context, e *config.PurchaseExecution) error {
		if e.CloudAccountID == nil {
			c := *e
			savedRoot = &c
		}
		return nil
	}
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)

	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProviderInst, nil)
	mockProviderInst.On("GetServiceClient", mock.Anything, common.ServiceEC2, mock.Anything).Return(mockServiceClient, nil)
	// Only acct-ok reaches a purchase (acct-bad fails at credential resolution),
	// so exactly one PurchaseCommitment call commits — the run is a partial
	// success: 1 account committed, 1 account failed.
	mockServiceClient.On("PurchaseCommitment", mock.Anything, mock.Anything, mock.Anything).
		Return(common.PurchaseResult{Success: true, CommitmentID: "ri-ok"}, nil).Once()

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		credStore:       awsAccessKeyCredStore(),
		dashboardURL:    "https://dashboard.example.com",
	}

	msg := AsyncMessage{Type: MessageTypeExecutePurchase, ExecutionID: "root-partial"}
	err := manager.handleExecutePurchase(ctx, &msg)
	require.NoError(t, err,
		"a multi-account run with >=1 committed account must be ACKed (return nil), not redelivered (issue #1014)")

	require.NotNil(t, savedRoot, "the root row must be saved with its aggregate status (H2)")
	assert.Equal(t, "partially_completed", savedRoot.Status,
		"root must reflect partial success, never 'failed' (double-spend mislabel #642/#1014)")
}

// scopelessRunResult is what runScopelessRow observed: the tokens of every
// commitment purchase that reached the fake cloud (one entry == one real
// purchase), and the error executePurchase returned.
type scopelessRunResult struct {
	err    error
	tokens []string
}

// runScopelessRow drives executePurchase for a plan execution that carries NO
// cloud_account_id, against a plan whose accounts are `accounts`, and reports
// what actually got bought.
//
// The whole provider chain is wired and WOULD happily sell, so a missing or
// fail-open guard shows up as extra purchases rather than as an
// unexpected-mock-call panic that a require.Error would misread as "refused".
func runScopelessRow(t *testing.T, exec *config.PurchaseExecution, accounts []config.CloudAccount) scopelessRunResult {
	t.Helper()
	ctx := context.Background()

	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProviderInst := new(MockProvider)
	mockServiceClient := new(MockServiceClient)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockEmail.AssertExpectations(t) })
	t.Cleanup(func() { mockFactory.AssertExpectations(t) })
	t.Cleanup(func() { mockProviderInst.AssertExpectations(t) })
	t.Cleanup(func() { mockServiceClient.AssertExpectations(t) })

	mockStore.GetPurchasePlanFn = func(_ context.Context, id string) (*config.PurchasePlan, error) {
		return &config.PurchasePlan{ID: id, Name: "Plan X"}, nil
	}
	// Fn overrides, not testify expectations: whether the plan's accounts are
	// consulted at all is part of what is under test.
	mockStore.GetPlanAccountsFn = func(_ context.Context, _ string) ([]config.CloudAccount, error) {
		return accounts, nil
	}
	mockStore.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		for i := range accounts {
			if accounts[i].ID == id {
				acct := accounts[i]
				return &acct, nil
			}
		}
		return nil, nil
	}
	mockStore.SavePurchaseExecutionFn = func(_ context.Context, _ *config.PurchaseExecution) error { return nil }

	var mu sync.Mutex
	var tokens []string
	mockStore.On("SavePurchaseHistory", mock.Anything, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil).Maybe()
	mockEmail.On("SendPurchaseConfirmation", mock.Anything, mock.AnythingOfType("email.NotificationData")).Return(nil).Maybe()
	mockStore.On("CompletePlanStep", mock.Anything, "plan-x", mock.Anything).Return(nil).Maybe()
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProviderInst, nil).Maybe()
	mockProviderInst.On("GetServiceClient", mock.Anything, common.ServiceEC2, mock.Anything).Return(mockServiceClient, nil).Maybe()
	mockServiceClient.On("PurchaseCommitment", mock.Anything, mock.Anything, mock.AnythingOfType("common.PurchaseOptions")).
		Run(func(args mock.Arguments) {
			opts := args.Get(2).(common.PurchaseOptions)
			mu.Lock()
			tokens = append(tokens, opts.IdempotencyToken)
			mu.Unlock()
		}).
		Return(common.PurchaseResult{Success: true, CommitmentID: "ri-ok"}, nil).Maybe()

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		credStore:       awsAccessKeyCredStore(),
		dashboardURL:    "https://dashboard.example.com",
	}

	err := manager.executePurchase(ctx, exec)

	mu.Lock()
	defer mu.Unlock()
	out := append([]string(nil), tokens...)
	sort.Strings(out)
	return scopelessRunResult{err: err, tokens: out}
}

// scopelessPerAccountRow is the row shape both tests below start from: a
// successor minted by the pre-#1537 retry handler that is still pending or
// approved when the fix deploys. Its cloud_account_id is nil, but its lineage
// key is still the per-account "<root-key>:<accountID>" that
// executeForAccount stamped.
func scopelessPerAccountRow(accountID string) *config.PurchaseExecution {
	return &config.PurchaseExecution{
		ExecutionID:    "successor-of-" + accountID + "-row",
		CloudAccountID: nil,
		IdempotencyKey: "root-lineage:" + accountID,
		PlanID:         "plan-x",
		Source:         common.PurchaseSourceWeb,
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 300, Selected: true},
		},
	}
}

// TestScopelessPerAccountRowSelfHealsToItsAccount is the issue #1537
// defense-in-depth guard at the purchase layer, recovery half.
//
// The primary fix keeps cloud_account_id on a per-account row's retry successor
// (api.persistRetryExecution). This is the second gate, for a row that reaches
// the executor scopeless while its lineage is already per-account. Fanning such
// a row out would re-suffix its key ("root-lineage:acct-A" ->
// "root-lineage:acct-A:acct-B"), derive tokens matching nothing the first
// attempt used, and re-buy every account that already committed.
//
// When the scoped account is still on the plan the lineage key is durable proof
// of intent, so the executor re-attaches the scope and runs the single-account
// re-drive the row was always meant to be: exactly ONE purchase, under the
// IDENTICAL token the original attempt used, so a commitment that actually
// landed dedupes. Refusing here instead would be a dead end — the successor of
// a refused row carries the same nil scope and the same key, so it would be
// refused forever, and its predecessor already 409s as "already retried".
func TestScopelessPerAccountRowSelfHealsToItsAccount(t *testing.T) {
	accounts := []config.CloudAccount{
		{ID: "acct-A", Name: "A", Provider: "aws", ExternalID: "111111111111", AWSAuthMode: "access_keys"},
		{ID: "acct-B", Name: "B", Provider: "aws", ExternalID: "222222222222", AWSAuthMode: "access_keys"},
	}
	orphan := scopelessPerAccountRow("acct-A")

	got := runScopelessRow(t, orphan, accounts)

	require.NoError(t, got.err,
		"a scopeless row whose account is still on the plan must complete as a single-account re-drive, not dead-end on a refusal it can never recover from")
	assert.Equal(t, []string{common.DeriveIdempotencyToken("root-lineage:acct-A", 0)}, got.tokens,
		"exactly ONE purchase, under the token the ORIGINAL acct-A attempt derived. A second token is acct-B being bought again; a different single token would miss the provider's dedupe guard (issue #1537)")

	require.NotNil(t, orphan.CloudAccountID,
		"the recovered scope must be stamped on the row so the repair persists (executeAndFinalize saves it, and cloud_account_id is in SavePurchaseExecution's ON CONFLICT SET)")
	assert.Equal(t, "acct-A", *orphan.CloudAccountID)
}

// TestScopelessPerAccountRowRefusesWhenAccountLeftThePlan is the fail-closed
// half, and the case a plan-account-set matcher gets wrong.
//
// Detecting per-account-ness by matching the plan's CURRENT accounts fails OPEN
// exactly when the scoped account has since been removed from the plan (or
// deleted and re-added under a new ID): nothing matches, no refusal fires, and
// the row re-fans-out and re-buys every remaining account under freshly
// suffixed keys. Detecting it by the colon that only executeForAccount ever
// writes into a lineage key does not have that hole.
//
// There is nothing to re-attach to here, so the only safe outcome is to refuse
// and buy NOTHING.
func TestScopelessPerAccountRowRefusesWhenAccountLeftThePlan(t *testing.T) {
	// acct-C has been removed from the plan since its row failed.
	accounts := []config.CloudAccount{
		{ID: "acct-A", Name: "A", Provider: "aws", ExternalID: "111111111111", AWSAuthMode: "access_keys"},
		{ID: "acct-B", Name: "B", Provider: "aws", ExternalID: "222222222222", AWSAuthMode: "access_keys"},
	}
	orphan := scopelessPerAccountRow("acct-C")

	got := runScopelessRow(t, orphan, accounts)

	assert.Empty(t, got.tokens,
		"a scopeless per-account row whose account left the plan must buy NOTHING; every purchase here is an account that already committed being bought again under a fresh, undeduplicated token (issue #1537)")
	assert.Nil(t, orphan.CloudAccountID,
		"no plan account matches the lineage key, so nothing may be stamped as the recovered scope")

	require.Error(t, got.err,
		"the refusal must surface as an error, not a silent skip, so the row lands 'failed' and an operator sees it")
	assert.Contains(t, got.err.Error(), "refusing to fan out",
		"the error must be the #1537 refusal itself, not an incidental downstream failure")
	assert.Contains(t, got.err.Error(), "no longer on plan",
		"the error must say WHY the scope could not be recovered, so the operator knows to re-add the account or cancel")
	assert.Contains(t, got.err.Error(), "acct-C",
		"the error must name the departed account, since re-adding THAT account is the operator's way out")
}

// TestRootKeyStillFansOutThroughExecutePurchase is the counterpart to the guard
// above: the refusal must be narrow. A genuine root row (root lineage key, no
// account scope) must still fan out across every plan account, each under its
// own per-account key — a legitimate multi-account purchase silently not
// happening is the same defect with the opposite sign.
func TestRootKeyStillFansOutThroughExecutePurchase(t *testing.T) {
	ctx := context.Background()
	accounts := []config.CloudAccount{
		{ID: "acct-A", Name: "A", Provider: "aws", ExternalID: "111111111111", AWSAuthMode: "access_keys"},
		{ID: "acct-B", Name: "B", Provider: "aws", ExternalID: "222222222222", AWSAuthMode: "access_keys"},
	}

	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProviderInst := new(MockProvider)
	mockServiceClient := new(MockServiceClient)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockEmail.AssertExpectations(t) })
	t.Cleanup(func() { mockFactory.AssertExpectations(t) })
	t.Cleanup(func() { mockProviderInst.AssertExpectations(t) })
	t.Cleanup(func() { mockServiceClient.AssertExpectations(t) })

	mockStore.GetPurchasePlanFn = func(_ context.Context, id string) (*config.PurchasePlan, error) {
		return &config.PurchasePlan{ID: id, Name: "Plan X"}, nil
	}
	mockStore.GetPlanAccountsFn = func(_ context.Context, _ string) ([]config.CloudAccount, error) {
		return accounts, nil
	}

	var mu sync.Mutex
	perAccountKey := map[string]string{}
	mockStore.SavePurchaseExecutionFn = func(_ context.Context, e *config.PurchaseExecution) error {
		mu.Lock()
		if e.CloudAccountID != nil {
			perAccountKey[*e.CloudAccountID] = e.IdempotencyKey
		}
		mu.Unlock()
		return nil
	}
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProviderInst, nil)
	mockProviderInst.On("GetServiceClient", mock.Anything, common.ServiceEC2, mock.Anything).Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", mock.Anything, mock.Anything, mock.Anything).
		Return(common.PurchaseResult{Success: true, CommitmentID: "ri-ok"}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		credStore:       awsAccessKeyCredStore(),
		dashboardURL:    "https://dashboard.example.com",
	}

	root := &config.PurchaseExecution{
		ExecutionID:    "root-exec",
		IdempotencyKey: "root-lineage",
		PlanID:         "plan-x",
		Source:         common.PurchaseSourceWeb,
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 300, Selected: true},
		},
	}
	require.NoError(t, manager.executePurchase(ctx, root),
		"a genuine root row must still fan out; the #1537 refusal must not catch it")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, map[string]string{
		"acct-A": "root-lineage:acct-A",
		"acct-B": "root-lineage:acct-B",
	}, perAccountKey, "every plan account must still be purchased under its own per-account lineage key")
}
