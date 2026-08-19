package purchase

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Issue #1718: armed pre-#1668 retry successors ----------------------
//
// #1668 gates the user-facing Retry at CREATION time, so no new Azure
// savings-plans successor can be built. It cannot reach successors a PRE-#1668
// retry already created: those rows sit in pending / notified / approved /
// scheduled with RetryAttemptN > 0 and buy a second, non-cancelable savings
// plan the moment they are approved.
//
// Every executor funnels through executeAndFinalize, so the guard lives there.
// These tests drive each executor entry point that can reach an armed row and
// assert on the PROVIDER CALL COUNT, not on statuses: the only thing that
// matters is whether money moved.
//
// The negative controls are as load-bearing as the refusals. Without them a
// gate that blocked every Azure savings-plan purchase, including legitimate
// first buys, would pass the refusal tests just as well.

const (
	armedExecID  = "44444444-5555-6666-7777-888888888801"
	armedPlanID  = "44444444-5555-6666-7777-888888888899"
	armedLineage = "lineage-1718"
)

// armedPurchaseRecorder counts every commitment purchase that reaches the
// "cloud". One recorded call is one real, irreversible purchase.
type armedPurchaseRecorder struct {
	mu    sync.Mutex
	calls []common.Recommendation
}

func (r *armedPurchaseRecorder) record(rec common.Recommendation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, rec)
}

func (r *armedPurchaseRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// armedExecution builds a single-recommendation execution for the given
// provider/service and retry-chain position. retryAttemptN > 0 marks it as a
// successor created by a retry, which is what #1718 is about.
func armedExecution(provider, service string, retryAttemptN int, status string) *config.PurchaseExecution {
	return &config.PurchaseExecution{
		ExecutionID:    armedExecID,
		PlanID:         armedPlanID,
		Status:         status,
		IdempotencyKey: armedLineage,
		RetryAttemptN:  retryAttemptN,
		Source:         common.PurchaseSourceWeb,
		ScheduledDate:  time.Now(),
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

// armedHarness wires a Manager whose provider records every purchase, plus the
// store mocks each executor path needs. serviceType must match what
// mapServiceType produces for the execution's service slug, since that is what
// the provider's GetServiceClient is asked for.
func armedHarness(t *testing.T, serviceType common.ServiceType) (*Manager, *MockConfigStore, *armedPurchaseRecorder) {
	t.Helper()

	rec := &armedPurchaseRecorder{}
	store := new(MockConfigStore)
	email := new(MockEmailSender)
	factory := new(MockProviderFactory)
	prov := new(MockProvider)
	svc := new(MockServiceClient)

	// Fn overrides rather than testify expectations: these must not double as
	// assertions, because whether the execution path reaches them at all is
	// precisely what is under test.
	store.SavePurchaseExecutionFn = func(_ context.Context, _ *config.PurchaseExecution) error { return nil }
	store.GetPurchasePlanFn = func(_ context.Context, id string) (*config.PurchasePlan, error) {
		return &config.PurchasePlan{ID: id, Name: "Plan 1718"}, nil
	}
	store.GetPlanAccountsFn = func(_ context.Context, _ string) ([]config.CloudAccount, error) { return nil, nil }

	store.On("SavePurchaseHistory", mock.Anything, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil).Maybe()
	store.On("CompletePlanStep", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	store.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil).Maybe()
	email.On("SendPurchaseConfirmation", mock.Anything, mock.AnythingOfType("email.NotificationData")).Return(nil).Maybe()

	factory.On("CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything).Return(prov, nil).Maybe()
	prov.On("GetServiceClient", mock.Anything, serviceType, mock.Anything).Return(svc, nil).Maybe()
	svc.On("PurchaseCommitment", mock.Anything, mock.Anything, mock.AnythingOfType("common.PurchaseOptions")).
		Run(func(args mock.Arguments) { rec.record(args.Get(1).(common.Recommendation)) }).
		Return(common.PurchaseResult{Success: true, CommitmentID: "commitment-1"}, nil).Maybe()

	mgr := &Manager{
		config:          store,
		email:           email,
		providerFactory: factory,
		credStore:       awsAccessKeyCredStore(),
		dashboardURL:    "https://dashboard.example.com",
	}
	return mgr, store, rec
}

// expectClaim wires the CAS the executor paths perform before executing, so the
// execution reaches executeAndFinalize the way production does.
func expectClaim(store *MockConfigStore, exec *config.PurchaseExecution, from []string, to string) {
	claimed := *exec
	claimed.Status = to
	store.On("TransitionExecutionStatus", mock.Anything, exec.ExecutionID, from, to, (*string)(nil)).
		Return(&claimed, nil).Once()
}

// TestApproveAndExecuteRefusesArmedAzureSavingsPlanRetry is the issue #1718
// regression guard on the approval path: the operator opens the approval link
// (or clicks Approve) on a successor a pre-#1668 retry created, and no second
// savings plan may be bought.
func TestApproveAndExecuteRefusesArmedAzureSavingsPlanRetry(t *testing.T) {
	exec := armedExecution("azure", "savingsplans", 1, "pending")
	mgr, store, rec := armedHarness(t, common.ServiceSavingsPlansAll)
	store.On("GetExecutionByID", mock.Anything, exec.ExecutionID).Return(exec, nil).Maybe()
	expectClaim(store, exec, []string{"pending", "notified"}, "approved")

	err := mgr.ApproveAndExecute(context.Background(), exec.ExecutionID, "operator@example.com", nil)

	assert.Equal(t, 0, rec.count(),
		"approving an armed Azure savings-plans retry successor must buy NOTHING; one call here is a second, non-cancelable savings plan (issue #1718)")
	require.Error(t, err, "the refusal must surface to the approver rather than passing silently")
	assert.Contains(t, err.Error(), "refusing to execute retry attempt 1")
	assert.Contains(t, err.Error(), "second savings plan")
}

// TestClaimAndExecuteRefusesArmedAzureSavingsPlanRetry covers the SQS
// execute_purchase and cron sweep path into the same armed row.
func TestClaimAndExecuteRefusesArmedAzureSavingsPlanRetry(t *testing.T) {
	exec := armedExecution("azure", "savingsplans", 2, "approved")
	mgr, store, rec := armedHarness(t, common.ServiceSavingsPlansAll)
	store.On("GetExecutionByID", mock.Anything, exec.ExecutionID).Return(exec, nil).Maybe()
	expectClaim(store, exec, []string{"approved", "pending", "notified"}, "running")

	body, err := json.Marshal(AsyncMessage{Type: MessageTypeExecutePurchase, ExecutionID: exec.ExecutionID})
	require.NoError(t, err)
	_ = mgr.ProcessMessage(context.Background(), string(body))

	assert.Equal(t, 0, rec.count(),
		"the SQS/cron executor must not buy a second savings plan for an armed retry successor (issue #1718)")
}

// TestFireScheduledDelayedPurchasesRefusesArmedAzureSavingsPlanRetry covers the
// path issue #1718 did not name, and which an approved successor is MOST likely
// to take in practice.
//
// approveWithDelay (internal/api/handler_purchases.go) transitions an approved
// row pending -> scheduled and returns; the row then fires later from the
// scheduler sweep via fireOneDue, never passing through ApproveAndExecute at
// all. Gating only the two executors the issue named would have left this
// entire path open for exactly the rows the issue is about.
func TestFireScheduledDelayedPurchasesRefusesArmedAzureSavingsPlanRetry(t *testing.T) {
	exec := armedExecution("azure", "savings-plans", 1, "scheduled")
	mgr, store, rec := armedHarness(t, common.ServiceSavingsPlansAll)
	store.On("GetScheduledExecutionsDue", mock.Anything).
		Return([]config.PurchaseExecution{*exec}, nil).Once()
	expectClaim(store, exec, []string{"scheduled"}, "approved")

	result, err := mgr.FireScheduledDelayedPurchases(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 0, rec.count(),
		"the delayed-approval scheduler must not buy a second savings plan for an armed retry successor (issue #1718)")
	assert.Equal(t, 0, result.Fired, "the armed row must not count as fired")
	assert.Equal(t, 1, result.Errored, "the refusal must be surfaced as an error, not silently swallowed")
}

// TestFirstAzureSavingsPlanPurchaseStillExecutes is the negative control that
// makes the refusals meaningful. A FRESH Azure savings-plans execution has no
// retry lineage, nothing to duplicate, and must still buy exactly once. A gate
// keyed on re-drive safety alone, without the RetryAttemptN > 0 term, would
// block every legitimate first savings-plan purchase and still pass every test
// above.
func TestFirstAzureSavingsPlanPurchaseStillExecutes(t *testing.T) {
	exec := armedExecution("azure", "savingsplans", 0, "approved")
	mgr, store, rec := armedHarness(t, common.ServiceSavingsPlansAll)
	store.On("GetExecutionByID", mock.Anything, exec.ExecutionID).Return(exec, nil).Maybe()
	expectClaim(store, exec, []string{"approved", "pending", "notified"}, "running")

	body, err := json.Marshal(AsyncMessage{Type: MessageTypeExecutePurchase, ExecutionID: exec.ExecutionID})
	require.NoError(t, err)
	require.NoError(t, mgr.ProcessMessage(context.Background(), string(body)))

	assert.Equal(t, 1, rec.count(),
		"a first Azure savings-plans purchase (retry_attempt_n = 0) must still go through; blocking it would be a worse bug than the one being fixed")
}

// TestSafeProviderRetrySuccessorStillExecutes is the second negative control:
// the guard must be as narrow as the provider gap. An AWS retry successor
// reproduces its ClientToken, so the provider collapses a re-drive onto the
// original and the retry must still execute.
func TestSafeProviderRetrySuccessorStillExecutes(t *testing.T) {
	exec := armedExecution("aws", "ec2", 3, "approved")
	mgr, store, rec := armedHarness(t, common.ServiceEC2)
	store.On("GetExecutionByID", mock.Anything, exec.ExecutionID).Return(exec, nil).Maybe()
	expectClaim(store, exec, []string{"approved", "pending", "notified"}, "running")

	body, err := json.Marshal(AsyncMessage{Type: MessageTypeExecutePurchase, ExecutionID: exec.ExecutionID})
	require.NoError(t, err)
	require.NoError(t, mgr.ProcessMessage(context.Background(), string(body)))

	assert.Equal(t, 1, rec.count(),
		"an AWS retry successor dedupes at the provider via ClientToken and must still execute; the guard must not widen beyond the providers that lack a duplicate guard")
}

// TestArmedRowIsDefusedNotLeftArmed pins the disposition of a refused row. A
// refusal that left the row in an executable status would simply be retried by
// the next sweep, so the guard has to be terminal: finalizeExecution stamps
// "failed" with the reason and executeAndFinalize persists it. Once failed, the
// #1668 creation-time gate refuses to retry it, so it cannot be re-armed.
func TestArmedRowIsDefusedNotLeftArmed(t *testing.T) {
	exec := armedExecution("azure", "savingsplans", 1, "approved")
	mgr, store, rec := armedHarness(t, common.ServiceSavingsPlansAll)

	var saved []config.PurchaseExecution
	store.SavePurchaseExecutionFn = func(_ context.Context, e *config.PurchaseExecution) error {
		saved = append(saved, *e)
		return nil
	}
	store.On("GetExecutionByID", mock.Anything, exec.ExecutionID).Return(exec, nil).Maybe()
	expectClaim(store, exec, []string{"approved", "pending", "notified"}, "running")

	body, err := json.Marshal(AsyncMessage{Type: MessageTypeExecutePurchase, ExecutionID: exec.ExecutionID})
	require.NoError(t, err)
	_ = mgr.ProcessMessage(context.Background(), string(body))

	require.Equal(t, 0, rec.count())
	require.NotEmpty(t, saved, "the refused row must be persisted, not left in its claimed status")
	final := saved[len(saved)-1]
	assert.Equal(t, "failed", final.Status,
		"a refused armed row must reach a terminal status so the next sweep does not pick it up again")
	assert.Contains(t, final.Error, "second savings plan",
		"the stored failure reason must say why, so an operator is not left guessing")
}
