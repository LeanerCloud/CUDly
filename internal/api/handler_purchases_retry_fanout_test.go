package api

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Issue #1537 regression harness ------------------------------------
//
// A multi-account plan fans out into one purchase_executions row per cloud
// account (purchase.executeForAccount), each stamped with CloudAccountID and a
// per-account idempotency key "<root-key>:<accountID>". Those per-account rows
// reach status="failed" on their own and the History UI offers Retry on them.
//
// The tests below drive the REAL chain end to end — the retry HTTP handler
// (Handler.retryPurchase, which builds the successor row) followed by the REAL
// purchase.Manager executing that successor — and count the cloud purchases
// that actually fire. A narrower unit test on either half alone would stay
// green while the double-spend lived in the seam between them.

const (
	// Valid UUIDs: retryPurchase runs validateUUID on the execution ID.
	fanoutFailedAcctCExecID = "11111111-2222-3333-4444-555555555501"
	fanoutFailedAcctAExecID = "11111111-2222-3333-4444-555555555502"
	fanoutFailedRootExecID  = "11111111-2222-3333-4444-555555555503"

	fanoutPlanID  = "77777777-7777-7777-7777-777777777777"
	fanoutRootKey = "root-lineage-1537"

	fanoutAcctA = "acct-A"
	fanoutAcctB = "acct-B"
	fanoutAcctC = "acct-C"
)

// fanoutPlanAccounts is the plan's account set: the fan-out unit whose
// re-entry on retry is what issue #1537 is about.
func fanoutPlanAccounts() []config.CloudAccount {
	return []config.CloudAccount{
		{ID: fanoutAcctA, Name: "A", Provider: "aws", ExternalID: "111111111111", AWSAuthMode: "access_keys"},
		{ID: fanoutAcctB, Name: "B", Provider: "aws", ExternalID: "222222222222", AWSAuthMode: "access_keys"},
		{ID: fanoutAcctC, Name: "C", Provider: "aws", ExternalID: "333333333333", AWSAuthMode: "access_keys"},
	}
}

func fanoutRecs() []config.RecommendationRecord {
	return []config.RecommendationRecord{
		{
			Provider: "aws", Service: "ec2", ResourceType: "m5.large",
			Region: "us-east-1", Count: 1, Term: 1,
			UpfrontCost: 300, Selected: true,
		},
	}
}

// fanoutCredStore resolves a static AWS access-key blob so per-account
// credential resolution succeeds without touching the cloud.
type fanoutCredStore struct {
	MockCredentialStore
}

func (*fanoutCredStore) LoadRaw(_ context.Context, _, _ string) ([]byte, error) {
	return []byte(`{"access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_access_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}`), nil
}

// fanoutServiceClient records the idempotency token of every commitment
// purchase that reaches the "cloud". One recorded token == one real purchase.
type fanoutServiceClient struct {
	mu     sync.Mutex
	tokens []string
}

func (c *fanoutServiceClient) PurchaseCommitment(_ context.Context, _ common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	c.mu.Lock()
	c.tokens = append(c.tokens, opts.IdempotencyToken)
	c.mu.Unlock()
	return common.PurchaseResult{Success: true, CommitmentID: "ri-" + opts.IdempotencyToken}, nil
}

// purchasedTokens returns a sorted copy of the recorded tokens. Sorted so
// assertions are stable under the parallel per-account fan-out.
func (c *fanoutServiceClient) purchasedTokens() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]string(nil), c.tokens...)
	sort.Strings(out)
	return out
}

func (c *fanoutServiceClient) GetServiceType() common.ServiceType { return common.ServiceEC2 }
func (c *fanoutServiceClient) GetRegion() string                  { return "us-east-1" }
func (c *fanoutServiceClient) GetRecommendations(_ context.Context, _ *common.RecommendationParams) ([]common.Recommendation, error) {
	return nil, nil
}
func (c *fanoutServiceClient) GetExistingCommitments(_ context.Context) ([]common.Commitment, error) {
	return nil, nil
}
func (c *fanoutServiceClient) ValidateOffering(_ context.Context, _ common.Recommendation) error {
	return nil
}
func (c *fanoutServiceClient) GetOfferingDetails(_ context.Context, _ common.Recommendation) (*common.OfferingDetails, error) {
	return &common.OfferingDetails{}, nil
}
func (c *fanoutServiceClient) GetValidResourceTypes(_ context.Context) ([]string, error) {
	return []string{"m5.large"}, nil
}

// fanoutProvider is a provider.Provider whose service client is the shared
// recorder above, so every account's purchases land in one tally.
type fanoutProvider struct{ svc *fanoutServiceClient }

func (p *fanoutProvider) Name() string                                  { return "aws" }
func (p *fanoutProvider) DisplayName() string                           { return "Amazon Web Services" }
func (p *fanoutProvider) IsConfigured() bool                            { return true }
func (p *fanoutProvider) GetCredentials() (provider.Credentials, error) { return nil, nil }
func (p *fanoutProvider) ValidateCredentials(_ context.Context) error   { return nil }
func (p *fanoutProvider) GetAccounts(_ context.Context) ([]common.Account, error) {
	return nil, nil
}
func (p *fanoutProvider) GetRegions(_ context.Context) ([]common.Region, error) { return nil, nil }
func (p *fanoutProvider) GetDefaultRegion() string                              { return "us-east-1" }
func (p *fanoutProvider) GetSupportedServices() []common.ServiceType {
	return []common.ServiceType{common.ServiceEC2}
}
func (p *fanoutProvider) GetServiceClient(_ context.Context, _ common.ServiceType, _ string) (provider.ServiceClient, error) {
	return p.svc, nil
}
func (p *fanoutProvider) GetRecommendationsClient(_ context.Context) (provider.RecommendationsClient, error) {
	return nil, nil
}

type fanoutProviderFactory struct{ prov *fanoutProvider }

func (f *fanoutProviderFactory) CreateAndValidateProvider(_ context.Context, _ string, _ *provider.ProviderConfig) (provider.Provider, error) {
	return f.prov, nil
}

// executeSuccessorForReal runs the retry successor through the REAL
// purchase.Manager the way production does: the approved row is picked up from
// the execute_purchase queue message, claimed, and executed. It returns the
// sorted idempotency tokens of every commitment purchase that reached the
// cloud — one entry per real purchase.
func executeSuccessorForReal(t *testing.T, successor *config.PurchaseExecution) []string {
	t.Helper()
	ctx := context.Background()

	// The row as the DB holds it once the operator has clicked the approval
	// link: same fields the retry handler persisted, status "approved".
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
	// Plan progress advances only on a fully clean run; .Maybe() so a run that
	// ends partial/failed does not turn into a mock-expectation failure that
	// would mask the purchase-count assertion the caller actually makes.
	store.On("CompletePlanStep", mock.Anything, fanoutPlanID, mock.Anything).Return(nil).Maybe()

	store.GetPurchasePlanFn = func(_ context.Context, planID string) (*config.PurchasePlan, error) {
		return &config.PurchasePlan{ID: planID, Name: "Plan 1537"}, nil
	}
	// Fn overrides rather than testify expectations: whether the fan-out
	// consults the plan's accounts at all is exactly what is under test, so
	// these must not double as assertions.
	store.GetPlanAccountsFn = func(_ context.Context, _ string) ([]config.CloudAccount, error) {
		return fanoutPlanAccounts(), nil
	}
	store.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		for _, a := range fanoutPlanAccounts() {
			if a.ID == id {
				acct := a
				return &acct, nil
			}
		}
		return nil, nil
	}
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
	require.NoError(t, mgr.ProcessMessage(ctx, string(body)),
		"the successor must execute cleanly; a non-nil error would make the purchase tally meaningless")

	return svc.purchasedTokens()
}

// retryFailedRow drives the real retry handler for a failed row owned by the
// calling session and returns the successor execution it persisted.
func retryFailedRow(t *testing.T, failed *config.PurchaseExecution) *config.PurchaseExecution {
	t.Helper()
	session := &Session{UserID: retryCallerID, Email: "operator@example.com"}
	// retry-own authorizes the row's creator (issue #907).
	successor, _ := runSessionRetryAllowed(t, failed, session, false, true, sessionRetryReq())
	return successor
}

// failedPerAccountRow builds the purchase_executions row that
// purchase.executeForAccount persists when one account of a fan-out fails:
// scoped to that account and keyed "<root-key>:<accountID>".
func failedPerAccountRow(execID, accountID string) *config.PurchaseExecution {
	creator := retryCallerID
	return &config.PurchaseExecution{
		ExecutionID:     execID,
		Status:          "failed",
		PlanID:          fanoutPlanID,
		StepNumber:      1,
		CloudAccountID:  strPtr(accountID),
		IdempotencyKey:  fanoutRootKey + ":" + accountID,
		Error:           "credential resolution failed for account " + accountID + ": RequestLimitExceeded",
		CreatedByUserID: &creator,
		CapacityPercent: 100,
		Source:          common.PurchaseSourceWeb,
		Recommendations: fanoutRecs(),
	}
}

// TestRetryOfFailedPerAccountRowDoesNotRefanOut is the issue #1537 regression
// guard, direction (a): two attempts at the SAME logical purchase must
// converge on ONE.
//
// Scenario, exactly as it happens in production: plan P covers accounts A, B
// and C. The root execution fans out; A and B commit real reserved instances,
// C fails on credential resolution and its per-account row is saved "failed".
// The operator clicks Retry on that C row — the only row History shows as
// failed.
//
// Pre-fix, persistRetryExecution dropped CloudAccountID, so the successor was
// born account-scopeless and purchase.executePurchase re-entered the
// multi-account fan-out: A and B were purchased a SECOND time, under freshly
// suffixed keys ("<root-key>:C:A") whose derived provider tokens miss the AWS
// ClientToken / EC2 RI tag-guard entirely, so nothing deduped them. At $300
// upfront per RI that is $600 of unintended spend from one Retry click, and it
// scales with the plan's account count.
//
// Post-fix the successor keeps account C's scope, so exactly one purchase
// fires and it carries the SAME provider token the original C attempt used —
// so even if C's first attempt had actually landed at the provider, the retry
// dedupes instead of buying twice.
func TestRetryOfFailedPerAccountRowDoesNotRefanOut(t *testing.T) {
	failedC := failedPerAccountRow(fanoutFailedAcctCExecID, fanoutAcctC)

	successor := retryFailedRow(t, failedC)
	tokens := executeSuccessorForReal(t, successor)

	// The money assertion comes first and deliberately uses assert, not
	// require: on the pre-fix code the follow-up checks below name the cause
	// (a dropped account scope) in the same failing run.
	assert.Equal(t, []string{common.DeriveIdempotencyToken(failedC.IdempotencyKey, 0)}, tokens,
		"retrying account C's failed row must fire exactly ONE purchase, carrying the ORIGINAL C attempt's provider token. "+
			"Extra tokens are accounts that already committed being bought AGAIN (issue #1537); a different token would miss the provider's dedupe guard")

	if assert.NotNil(t, successor.CloudAccountID,
		"the retry successor of a per-account row must inherit that account's scope; a nil scope re-enters the multi-account fan-out (issue #1537)") {
		assert.Equal(t, fanoutAcctC, *successor.CloudAccountID,
			"the successor must stay scoped to the account whose row was retried")
	}
	assert.Equal(t, failedC.IdempotencyKey, successor.IdempotencyKey,
		"the lineage key must carry over verbatim so the provider token is reproduced (issue #1012)")
}

// TestRetryOfPerAccountRowsKeepsAccountsDistinct is the issue #1537 guard for
// direction (b): two GENUINELY DIFFERENT purchases must not collapse onto one
// token. Scoping the successor must not over-converge — retrying account A's
// failed row and account C's failed row are separate money events and must
// keep separate provider tokens, or one of the two legitimate purchases would
// be silently deduped away and never happen.
func TestRetryOfPerAccountRowsKeepsAccountsDistinct(t *testing.T) {
	failedA := failedPerAccountRow(fanoutFailedAcctAExecID, fanoutAcctA)
	failedC := failedPerAccountRow(fanoutFailedAcctCExecID, fanoutAcctC)

	tokensA := executeSuccessorForReal(t, retryFailedRow(t, failedA))
	tokensC := executeSuccessorForReal(t, retryFailedRow(t, failedC))

	require.Len(t, tokensA, 1, "account A's retry must purchase exactly once")
	require.Len(t, tokensC, 1, "account C's retry must purchase exactly once")
	assert.NotEqual(t, tokensA[0], tokensC[0],
		"retries of DIFFERENT accounts' rows must derive DIFFERENT provider tokens; collapsing them would make one legitimate purchase silently never happen")
	assert.Equal(t, common.DeriveIdempotencyToken(fanoutRootKey+":"+fanoutAcctA, 0), tokensA[0])
	assert.Equal(t, common.DeriveIdempotencyToken(fanoutRootKey+":"+fanoutAcctC, 0), tokensC[0])
}

// TestRetryOfFailedRootRowStillFansOutToEveryAccount is the other half of
// direction (b): the fix must not suppress a fan-out that SHOULD happen.
//
// A root execution that failed before any account committed (e.g. the plan's
// account list could not be loaded) carries no CloudAccountID. Retrying it
// must still fan out across every plan account, each under the per-account
// lineage key the first attempt would have used — so nothing is skipped and no
// two accounts share a token.
func TestRetryOfFailedRootRowStillFansOutToEveryAccount(t *testing.T) {
	creator := retryCallerID
	failedRoot := &config.PurchaseExecution{
		ExecutionID: fanoutFailedRootExecID,
		Status:      "failed",
		PlanID:      fanoutPlanID,
		StepNumber:  1,
		// CloudAccountID deliberately nil: the root row never reached the fan-out.
		IdempotencyKey:  fanoutRootKey,
		Error:           "failed to load plan accounts for plan " + fanoutPlanID + ": RequestLimitExceeded",
		CreatedByUserID: &creator,
		CapacityPercent: 100,
		Source:          common.PurchaseSourceWeb,
		Recommendations: fanoutRecs(),
	}

	successor := retryFailedRow(t, failedRoot)
	assert.Nil(t, successor.CloudAccountID,
		"a root row's successor must stay account-scopeless so the fan-out still runs")

	tokens := executeSuccessorForReal(t, successor)

	want := []string{
		common.DeriveIdempotencyToken(fanoutRootKey+":"+fanoutAcctA, 0),
		common.DeriveIdempotencyToken(fanoutRootKey+":"+fanoutAcctB, 0),
		common.DeriveIdempotencyToken(fanoutRootKey+":"+fanoutAcctC, 0),
	}
	sort.Strings(want)
	assert.Equal(t, want, tokens,
		"retrying a root row must still purchase once per plan account, each under its own per-account token")
}
