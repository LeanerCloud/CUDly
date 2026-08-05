package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Issue #1668 regression harness ------------------------------------
//
// A purchase_executions row reaching status="failed" does NOT prove the
// commitment never landed: a timeout, a lost response or a failed
// post-purchase write all produce a "failed" row behind an order the provider
// actually accepted. Every retry is therefore potentially a re-drive, and only
// a provider-side duplicate guard makes it safe.
//
// purchase.RedriveRefusalReason encodes which provider/service combinations
// have such a guard. Before this fix it gated only the reaper's automatic
// re-drive; the user-facing Retry button did not consult it at all, so an
// operator retrying a landed Azure savings-plans row bought a SECOND savings
// plan: a multi-year commitment that cannot be canceled.
//
// The tests below drive the REAL chain the operator drives: the retry HTTP
// handler (Handler.retryPurchase) and then, when it allows the retry, the REAL
// purchase.Manager executing the successor it persisted. They assert on the
// number of purchases that reach the cloud, not on statuses. A narrower unit
// test on purchase.RedriveRefusalReason alone stays green either way, because
// the predicate was already correct and simply unconsulted.

const (
	redriveAzureSPExecID    = "22222222-3333-4444-5555-666666666601"
	redriveAzureSPAltExecID = "22222222-3333-4444-5555-666666666602"
	redriveAzureRIExecID    = "22222222-3333-4444-5555-666666666603"
	redriveUnknownExecID    = "22222222-3333-4444-5555-666666666604"
	redriveForceExecID      = "22222222-3333-4444-5555-666666666605"
	redriveMixedExecID      = "22222222-3333-4444-5555-666666666606"
	redriveNoRecsExecID     = "22222222-3333-4444-5555-666666666607"
	redriveNoRecsPlanID     = "33333333-4444-5555-6666-777777777701"

	redriveLineageKey = "lineage-1668"
)

// redriveFailedRow builds the failed row an operator sees in History for a
// purchase whose order may well have landed at the provider: the execution
// timed out waiting for the response, so nothing local records the commitment
// even though the provider may hold it.
func redriveFailedRow(execID, provider, service string) *config.PurchaseExecution {
	creator := retryCallerID
	return &config.PurchaseExecution{
		ExecutionID:     execID,
		Status:          "failed",
		IdempotencyKey:  redriveLineageKey + ":" + execID,
		Error:           "context deadline exceeded while awaiting the order response",
		CreatedByUserID: &creator,
		CapacityPercent: 100,
		Source:          common.PurchaseSourceWeb,
		Recommendations: []config.RecommendationRecord{{
			Provider:     provider,
			Service:      service,
			ResourceType: "Standard_D2s_v3",
			Region:       "westeurope",
			Count:        1,
			Term:         3,
			UpfrontCost:  30000,
			Selected:     true,
		}},
	}
}

// purchasesFiredByRetry drives the REAL retry handler for failed and, when the
// handler allows the retry, runs the successor it persisted through the REAL
// purchase.Manager exactly as production does. It returns the idempotency
// token of every commitment purchase that reached the "cloud" (one entry per
// real purchase) alongside the handler's error (nil when the retry was
// allowed).
func purchasesFiredByRetry(t *testing.T, failed *config.PurchaseExecution, req *events.LambdaFunctionURLRequest) ([]string, error) {
	t.Helper()

	// retry-own authorizes the row's creator (issue #907), so RBAC is out of
	// the way and the re-drive-safety gate is what the assertions see.
	session := &Session{UserID: retryCallerID, Email: "operator@example.com"}
	handler, mockConfig, _ := buildSessionRetryHandler(failed, session, false, true)

	var saved []*config.PurchaseExecution
	mockConfig.On("SavePurchaseExecution", mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) {
			// Copy so later in-place mutations by the handler don't
			// retroactively rewrite the captured successor.
			snap := *args.Get(1).(*config.PurchaseExecution)
			saved = append(saved, &snap)
		}).
		Return(nil).Maybe()

	if _, err := handler.retryPurchase(context.Background(), req, failed.ExecutionID); err != nil {
		assert.Empty(t, saved,
			"a refused retry must not persist a successor row; a persisted successor is one approval click away from reaching the provider")
		return nil, err
	}

	// First save is the successor; the second is the original row stamped with
	// the linkage pointer (the retry tx orders them that way for the FK).
	require.NotEmpty(t, saved, "an allowed retry must have persisted a successor execution")
	return executeRedriveSuccessor(t, saved[0]), nil
}

// executeRedriveSuccessor runs a retry successor through the real
// purchase.Manager the way production does: the row is picked up from the
// execute_purchase queue message once the operator has approved it, claimed,
// and executed. Returns the idempotency token of every purchase that reached
// the cloud.
func executeRedriveSuccessor(t *testing.T, successor *config.PurchaseExecution) []string {
	t.Helper()

	approved := *successor
	approved.Status = "approved"
	running := approved
	running.Status = "running"

	store := new(MockConfigStore)
	t.Cleanup(func() { store.AssertExpectations(t) })

	store.On("GetExecutionByID", mock.Anything, approved.ExecutionID).Return(&approved, nil).Once()
	store.On("TransitionExecutionStatus", mock.Anything, approved.ExecutionID,
		[]string{"approved", "pending", "notified"}, "running", (*string)(nil)).Return(&running, nil).Once()
	store.On("SavePurchaseHistory", mock.Anything, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	store.SavePurchaseExecutionFn = func(_ context.Context, _ *config.PurchaseExecution) error { return nil }

	svc := &fanoutServiceClient{}
	mgr := purchase.NewManager(purchase.ManagerConfig{
		ConfigStore:     store,
		EmailSender:     &stubEmailNotifier{},
		CredentialStore: &fanoutCredStore{},
		ProviderFactory: &fanoutProviderFactory{prov: &fanoutProvider{svc: svc}},
		DashboardURL:    "https://dashboard.example.com",
	})

	body, err := json.Marshal(purchase.AsyncMessage{
		Type:        purchase.MessageTypeExecutePurchase,
		ExecutionID: approved.ExecutionID,
	})
	require.NoError(t, err)
	require.NoError(t, mgr.ProcessMessage(context.Background(), string(body)),
		"the successor must execute cleanly; a non-nil error would make the purchase tally meaningless")

	return svc.purchasedTokens()
}

// assertRedriveRefused asserts the shape of the refusal the retry endpoint
// must return: a 409 (the same status the ops-hint and threshold gates use, so
// the History UI already renders it in place of the Retry button) carrying a
// specific reason rather than a generic client error.
func assertRedriveRefused(t *testing.T, err error, wantReasonFragment string) {
	t.Helper()
	require.Error(t, err, "the retry must be refused")
	ce, ok := IsClientError(err)
	require.True(t, ok, "the refusal must be a structured client error, got: %v", err)
	assert.Equal(t, 409, ce.code)
	require.NotNil(t, ce.Details(), "the refusal must carry structured details the UI can render")
	assert.Equal(t, true, ce.Details()["redrive_unsafe"],
		"redrive_unsafe marks this as a permanent refusal, unlike the operator-fixable ops hints")
	hint, ok := ce.Details()["ops_hint"].(string)
	require.True(t, ok, "ops_hint must be a string so the existing History renderer can show it")
	assert.Contains(t, hint, wantReasonFragment,
		"the refusal must name why this purchase cannot be re-driven, not fail generically")
}

// TestRetryOfLandedAzureSavingsPlanPurchasesNothing is the issue #1668
// regression guard.
//
// Scenario, exactly as it happens in production: an Azure savings-plans
// purchase is submitted, Azure accepts the order alias, and the execution then
// times out waiting for the response. The row lands in History as "failed"
// with a Retry button on it. The operator, seeing a failure, clicks Retry.
//
// Pre-fix the retry path gated only on status, RBAC, already-retried, the
// ops-hint map and the attempt threshold, none of which knows anything about
// provider re-drive safety, so a successor row was created and, once
// approved, purchased a SECOND savings plan. Azure savings plans carry no
// server-side idempotency key and name their order alias from
// time.Now().UnixNano(), so nothing on the provider side collapsed the second
// order onto the first, and a savings plan cannot be canceled: at $30k
// upfront that is $30k of unrecoverable spend from one Retry click.
//
// Post-fix the handler consults purchase.RedriveRefusalReason, the same
// predicate the reaper's automatic re-drive already gated on, and refuses,
// so no successor row exists and nothing can reach Azure.
func TestRetryOfLandedAzureSavingsPlanPurchasesNothing(t *testing.T) {
	// Both spellings the recommendation records use in the wild; the reaper's
	// exclusion covers both and the retry path must not diverge.
	cases := []struct {
		name    string
		execID  string
		service string
	}{
		{name: "savingsplans", execID: redriveAzureSPExecID, service: "savingsplans"},
		{name: "savings-plans", execID: redriveAzureSPAltExecID, service: "savings-plans"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failed := redriveFailedRow(tc.execID, "azure", tc.service)

			tokens, err := purchasesFiredByRetry(t, failed, sessionRetryReq())

			// The money assertion comes first and deliberately uses assert,
			// not require, so the refusal-shape checks below still name the
			// cause in the same failing run on the pre-fix code.
			assert.Empty(t, tokens,
				"retrying a possibly-landed Azure savings-plans row must fire ZERO purchases; "+
					"any token here is a second, non-cancellable savings plan bought by one Retry click (issue #1668)")
			assertRedriveRefused(t, err, "second savings plan")
		})
	}
}

// TestRetryOfLandedAzureSavingsPlanIsNotForceOverridable pins the explicit
// decision that ?force=true, which does override the retry-attempt threshold,
// must NOT override the re-drive-safety refusal. A savings plan cannot be
// canceled, so there is no recovery from a wrong override, and a duplicate is
// not what the operator clicking Retry is asking for; buying a second one on
// purpose is a fresh purchase, not a retry.
func TestRetryOfLandedAzureSavingsPlanIsNotForceOverridable(t *testing.T) {
	failed := redriveFailedRow(redriveForceExecID, "azure", "savingsplans")

	tokens, err := purchasesFiredByRetry(t, failed, sessionRetryReqWithForce())

	assert.Empty(t, tokens,
		"?force=true must not buy a second Azure savings plan; force overrides the attempt threshold, not provider safety")
	assertRedriveRefused(t, err, "second savings plan")
}

// TestRetryOfFailedRecommendationlessExecutionIsAllowed is the guard for the
// far side of the gate: it must fire only where the duplicate hazard is real,
// because a refusal here is permanent.
//
// createPurchaseExecutionsTx (handler_plans.go) and getOrCreateExecution
// (purchase/notifications.go) both create executions with NO recommendations,
// and a failed approval email marks those "failed". They buy nothing, so they
// cannot double-buy, and retrying is the only recovery an operator has. An
// earlier revision of this gate refused them along with the genuinely unsafe
// rows, which would have stranded the whole class permanently.
//
// Empty here always means empty as created, never "we could not load them":
// GetExecutionByID propagates a recommendations unmarshal failure as an error,
// which the handler turns into a 500 long before this gate runs.
func TestRetryOfFailedRecommendationlessExecutionIsAllowed(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID: redriveNoRecsExecID,
		PlanID:      redriveNoRecsPlanID,
		StepNumber:  1,
		Status:      "failed",
		// A transient send failure, deliberately not one of the
		// persistent-failure hints, so the ops-hint gate stays out of the way.
		Error:           "failed to send approval email: SES throttle exceeded",
		CreatedByUserID: &creator,
		Source:          common.PurchaseSourceWeb,
		// Recommendations deliberately nil: this is how both creation paths
		// above persist the row.
	}
	session := &Session{UserID: retryCallerID, Email: "operator@example.com"}

	successor, updated := runSessionRetryAllowed(t, failed, session, false, true, sessionRetryReq())

	assert.Empty(t, successor.Recommendations,
		"the successor carries the predecessor's (empty) recommendations, so it buys nothing; that is precisely why refusing it bought no safety")
	assert.Equal(t, 1, successor.RetryAttemptN, "the retry chain still advances")
	require.NotNil(t, updated.RetryExecutionID, "the original must be linked to its successor")
	assert.Equal(t, successor.ExecutionID, *updated.RetryExecutionID)
}

// TestRetryOfMixedExecutionWithOneAzureSavingsPlanIsRefused guards the whole
// recommendation list, not just its head. An execution can carry several recs,
// and re-driving it re-drives every one of them: a single unsafe rec anywhere
// in the list makes the whole retry unsafe, however many safe recs sit in
// front of it. The AWS rec here is deliberately first, so a gate that checked
// only the leading rec would pass this row straight through to Azure.
func TestRetryOfMixedExecutionWithOneAzureSavingsPlanIsRefused(t *testing.T) {
	failed := redriveFailedRow(redriveMixedExecID, "aws", "ec2")
	failed.Recommendations = append(failed.Recommendations, config.RecommendationRecord{
		Provider:     "azure",
		Service:      "savingsplans",
		ResourceType: "Standard_D2s_v3",
		Region:       "westeurope",
		Count:        1,
		Term:         3,
		UpfrontCost:  30000,
		Selected:     true,
	})

	tokens, err := purchasesFiredByRetry(t, failed, sessionRetryReq())

	assert.Empty(t, tokens,
		"one unsafe rec must block the whole retry; purchasing the safe recs alone would still re-drive the Azure savings plan alongside them")
	assertRedriveRefused(t, err, "second savings plan")
}

// TestRetryOfUnknownProviderRowIsRefused covers the fail-closed half of the
// same gate: purchase.RedriveRefusalReason refuses a provider it does not
// recognize rather than assuming a duplicate guard exists. Before this fix an
// unknown provider was un-redrivable by the reaper yet freely retryable from
// the API. The two paths now agree.
func TestRetryOfUnknownProviderRowIsRefused(t *testing.T) {
	failed := redriveFailedRow(redriveUnknownExecID, "oraclecloud", "compute")

	tokens, err := purchasesFiredByRetry(t, failed, sessionRetryReq())

	assert.Empty(t, tokens,
		"a provider with no known duplicate guard must not be re-driven from the retry endpoint")
	assertRedriveRefused(t, err, `provider "oraclecloud" is not known to reject a duplicate purchase`)
}

// TestRetryOfFailedAzureReservationStillPurchasesOnce is the over-blocking
// guard. The refusal must be as narrow as the underlying provider gap: Azure
// reservations go through DoIdempotentPurchaseTwoStep (#729), which looks the
// order up before purchasing, so retrying one is safe and must keep working.
// A gate that blocked every Azure row would strand legitimate retries.
//
// The single purchase must also carry the token derived from the PREDECESSOR's
// lineage key, which is what makes the provider-side dedupe engage if the
// first attempt had in fact landed.
func TestRetryOfFailedAzureReservationStillPurchasesOnce(t *testing.T) {
	failed := redriveFailedRow(redriveAzureRIExecID, "azure", "compute")

	tokens, err := purchasesFiredByRetry(t, failed, sessionRetryReq())

	require.NoError(t, err, "an Azure reservation retry is safe and must still be allowed")
	assert.Equal(t, []string{common.DeriveIdempotencyToken(failed.IdempotencyKey, 0)}, tokens,
		"the retry must fire exactly one purchase, under the predecessor's token so Azure's two-step lookup dedupes it")
}
