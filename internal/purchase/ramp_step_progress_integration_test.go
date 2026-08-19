//go:build integration
// +build integration

package purchase

// Regression coverage for issue #1669: retrying two failed accounts of one
// multi-account ramp step must advance the plan's CurrentStep exactly once.
//
// These tests run the real Manager against a real migrated Postgres (the plan
// row, the executions and the ramp advance all go through the production
// store), because the defect lives in the INTERACTION between the fan-out, the
// per-account retry successors and the store's step accounting. A unit test
// over the store helper alone cannot see it.

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// rampMigrationsPath resolves the migrations directory relative to this file.
func rampMigrationsPath() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "database", "postgres", "migrations")
}

// rampStepStore stands up a fresh Postgres container with all migrations
// applied and returns a production store bound to it, plus the pool so a test
// can drive its own transaction alongside the store's.
func rampStepStore(ctx context.Context, t *testing.T) (*config.PostgresStore, *pgxpool.Pool) {
	t.Helper()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	t.Cleanup(func() {
		if cleanupErr := container.Cleanup(ctx); cleanupErr != nil {
			t.Logf("container cleanup: %v", cleanupErr)
		}
	})

	require.NoError(t, migrations.RunMigrations(ctx, container.DB.Pool(), rampMigrationsPath(), "", ""))
	return config.NewPostgresStore(container.DB), container.DB.Pool()
}

// rampStepFixture is the world a ramp-step test runs against: a plan sitting at
// CurrentStep=2 of 4 with three cloud accounts attached, and a manager wired to
// the real store with a provider that always sells.
type rampStepFixture struct {
	store    *config.PostgresStore
	manager  *Manager
	plan     *config.PurchasePlan
	accounts []config.CloudAccount

	mu        sync.Mutex
	purchased []string // one entry per commitment that reached the fake cloud
}

// newRampStepFixture builds the fixture. accountA is credential-resolvable from
// the start; accountB and accountC are configured with an auth mode that has no
// STS client wired, so their credential resolution fails deterministically --
// modeling "two of three accounts failed this ramp step" without racing the
// provider mock. repairAccounts() flips them to a working auth mode, which is
// what an operator does before retrying.
//
// Enabled is false purely to keep the plan minimal: PurchasePlan.Validate
// requires a non-empty Services map only for enabled plans, and nothing on the
// execution path reads plan.Enabled.
func newRampStepFixture(ctx context.Context, t *testing.T) *rampStepFixture {
	t.Helper()

	store, _ := rampStepStore(ctx, t)

	plan := &config.PurchasePlan{
		Name:         "Ramp Step Plan",
		AutoPurchase: true,
		Services:     map[string]config.ServiceConfig{},
		RampSchedule: config.RampSchedule{
			Type:             "weekly",
			PercentPerStep:   25,
			StepIntervalDays: 7,
			CurrentStep:      2,
			TotalSteps:       4,
			StartDate:        time.Now().AddDate(0, 0, -14),
		},
	}
	require.NoError(t, store.CreatePurchasePlan(ctx, plan))

	authModes := []string{"access_keys", "role_arn", "role_arn"}
	accountIDs := make([]string, 0, len(authModes))
	for i, mode := range authModes {
		acct := &config.CloudAccount{
			Name:        fmt.Sprintf("acct-%c", 'a'+i),
			Provider:    "aws",
			ExternalID:  fmt.Sprintf("11111111111%d", i),
			Enabled:     true,
			AWSAuthMode: mode,
			AWSRoleARN:  "arn:aws:iam::111111111110:role/cudly",
		}
		require.NoError(t, store.CreateCloudAccount(ctx, acct))
		accountIDs = append(accountIDs, acct.ID)
	}
	require.NoError(t, store.SetPlanAccounts(ctx, plan.ID, accountIDs))

	accounts, err := store.GetPlanAccounts(ctx, plan.ID)
	require.NoError(t, err)
	require.Len(t, accounts, 3, "the fan-out must see all three accounts")
	// GetPlanAccounts orders by name, so index 0 is the account that resolves
	// and 1-2 are the ones that fail. Pin it: the whole scenario depends on
	// which accounts fail, and a change in ordering would quietly retarget the
	// retries at an account that never failed.
	require.Equal(t, "access_keys", accounts[0].AWSAuthMode, "accounts[0] must be the account that succeeds")
	require.Equal(t, "role_arn", accounts[1].AWSAuthMode, "accounts[1] must be a failing account")
	require.Equal(t, "role_arn", accounts[2].AWSAuthMode, "accounts[2] must be a failing account")

	f := &rampStepFixture{store: store, plan: plan, accounts: accounts}

	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProviderInst := new(MockProvider)
	mockServiceClient := new(MockServiceClient)
	t.Cleanup(func() { mockEmail.AssertExpectations(t) })
	t.Cleanup(func() { mockFactory.AssertExpectations(t) })
	t.Cleanup(func() { mockProviderInst.AssertExpectations(t) })
	t.Cleanup(func() { mockServiceClient.AssertExpectations(t) })

	mockEmail.On("SendPurchaseConfirmation", mock.Anything, mock.AnythingOfType("email.NotificationData")).Return(nil).Maybe()
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProviderInst, nil).Maybe()
	mockProviderInst.On("GetServiceClient", mock.Anything, common.ServiceEC2, mock.Anything).Return(mockServiceClient, nil).Maybe()
	mockServiceClient.On("PurchaseCommitment", mock.Anything, mock.Anything, mock.AnythingOfType("common.PurchaseOptions")).
		Run(func(args mock.Arguments) {
			opts := args.Get(2).(common.PurchaseOptions)
			f.mu.Lock()
			f.purchased = append(f.purchased, opts.IdempotencyToken)
			f.mu.Unlock()
		}).
		Return(common.PurchaseResult{Success: true, CommitmentID: "ri-ok"}, nil).Maybe()

	f.manager = &Manager{
		config:          store,
		email:           mockEmail,
		providerFactory: mockFactory,
		credStore:       awsAccessKeyCredStore(),
		dashboardURL:    "https://dashboard.example.com",
	}
	return f
}

// repairAccounts flips the two broken accounts to a resolvable auth mode, the
// operator-side precondition of a successful retry.
func (f *rampStepFixture) repairAccounts(ctx context.Context, t *testing.T) {
	t.Helper()
	for i := 1; i < len(f.accounts); i++ {
		acct := f.accounts[i]
		acct.AWSAuthMode = "access_keys"
		require.NoError(t, f.store.UpdateCloudAccount(ctx, &acct))
	}
}

// currentStep re-reads the plan from Postgres and returns its ramp position.
func (f *rampStepFixture) currentStep(ctx context.Context, t *testing.T) int {
	t.Helper()
	plan, err := f.store.GetPurchasePlan(ctx, f.plan.ID)
	require.NoError(t, err)
	return plan.RampSchedule.CurrentStep
}

// purchaseCount reports how many commitments reached the fake cloud so far.
func (f *rampStepFixture) purchaseCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.purchased)
}

// rampStepRecommendation is the single rec every execution in these tests buys.
func rampStepRecommendation() []config.RecommendationRecord {
	return []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 300, Selected: true},
	}
}

// saveRootExecution persists the plan-scheduled root row for ramp step
// stepNumber: no cloud_account_id, so the executor fans it out.
func (f *rampStepFixture) saveRootExecution(ctx context.Context, t *testing.T, stepNumber int) *config.PurchaseExecution {
	t.Helper()
	exec := &config.PurchaseExecution{
		ExecutionID:     uuid.New().String(),
		IdempotencyKey:  fmt.Sprintf("lineage-step-%d", stepNumber),
		PlanID:          f.plan.ID,
		Status:          "pending",
		StepNumber:      stepNumber,
		ScheduledDate:   time.Now(),
		Recommendations: rampStepRecommendation(),
	}
	require.NoError(t, f.store.SavePurchaseExecution(ctx, exec))
	return exec
}

// saveRetryExecution persists the successor row an operator's Retry produces
// for one failed per-account row, mirroring api.persistRetryExecution: the
// PlanID, StepNumber, CloudAccountID and idempotency lineage all propagate from
// the predecessor, and the row arrives already approved by the human who
// clicked Retry.
func (f *rampStepFixture) saveRetryExecution(ctx context.Context, t *testing.T, stepNumber int, accountID string) *config.PurchaseExecution {
	t.Helper()
	acctID := accountID
	exec := &config.PurchaseExecution{
		ExecutionID:     uuid.New().String(),
		IdempotencyKey:  fmt.Sprintf("lineage-step-%d:%s", stepNumber, accountID),
		PlanID:          f.plan.ID,
		CloudAccountID:  &acctID,
		Status:          "approved",
		StepNumber:      stepNumber,
		ScheduledDate:   time.Now(),
		Recommendations: rampStepRecommendation(),
		Source:          common.PurchaseSourceWeb,
		RetryAttemptN:   1,
	}
	require.NoError(t, f.store.SavePurchaseExecution(ctx, exec))
	return exec
}

// execute drives the real async executor entry point for one row.
func (f *rampStepFixture) execute(ctx context.Context, exec *config.PurchaseExecution) error {
	msg := AsyncMessage{Type: MessageTypeExecutePurchase, ExecutionID: exec.ExecutionID}
	return f.manager.handleExecutePurchase(ctx, &msg)
}

// TestRampStepAdvancesOncePerStepAcrossPerAccountRetries is the issue #1669
// regression guard.
//
// A multi-account plan fans ramp step 3 out over accounts A, B and C. A buys; B
// and C fail on credentials. The operator repairs both and retries each failed
// row, and both retries succeed. The ramp must end on step 3, not step 4: with
// the pre-fix blind CurrentStep++ each successful retry advanced the ramp
// again, so one whole ramp step's worth of commitment silently dropped out of
// the plan's accounting.
func TestRampStepAdvancesOncePerStepAcrossPerAccountRetries(t *testing.T) {
	ctx := context.Background()
	f := newRampStepFixture(ctx, t)

	require.Equal(t, 2, f.currentStep(ctx, t), "fixture must start on step 2 of 4")

	// Step 3 fans out: A commits, B and C fail credential resolution.
	root := f.saveRootExecution(ctx, t, 3)
	require.NoError(t, f.execute(ctx, root),
		"a partial multi-account run must be acked, not surfaced as a flat failure (#1014)")

	require.Equal(t, 1, f.purchaseCount(), "exactly one account (A) may commit on the first pass")
	assert.Equal(t, 2, f.currentStep(ctx, t),
		"a partially-failed step must not advance the ramp at all")

	// The operator fixes the two broken accounts and retries each failed row.
	f.repairAccounts(ctx, t)

	retryB := f.saveRetryExecution(ctx, t, 3, f.accounts[1].ID)
	require.NoError(t, f.execute(ctx, retryB))
	require.Equal(t, 2, f.purchaseCount(), "B's retry must commit")
	assert.Equal(t, 3, f.currentStep(ctx, t),
		"the first successful completion of step 3 advances the ramp to 3")

	retryC := f.saveRetryExecution(ctx, t, 3, f.accounts[2].ID)
	require.NoError(t, f.execute(ctx, retryC))
	require.Equal(t, 3, f.purchaseCount(), "C's retry must commit")

	assert.Equal(t, 3, f.currentStep(ctx, t),
		"completing step 3 a second time must NOT advance the ramp again (issue #1669): "+
			"the plan has bought 3 of 4 ramp steps and must still say so")

	// Completing step 3 once more against this exact post-scenario state must
	// take the no-op branch. Nothing else observable distinguishes it from the
	// refusal branch (both return before the write and both leave CurrentStep
	// at 3), and the caller only logs the error, so assert on the return value
	// directly against real DB state rather than against pgxmock.
	require.NoError(t, f.store.CompletePlanStep(ctx, f.plan.ID, 3),
		"a counted step must no-op, not be refused as a skipped predecessor")

	// The ramp is not complete, so the plan still points at a next execution.
	plan, err := f.store.GetPurchasePlan(ctx, f.plan.ID)
	require.NoError(t, err)
	assert.False(t, plan.RampSchedule.IsComplete(), "3 of 4 steps bought is not a complete ramp")
	require.NotNil(t, plan.NextExecutionDate, "an incomplete ramp must keep a next execution date")

	// And the 4th step still counts. Pre-fix the ramp was already sitting at 4
	// here, so this step's completion changed nothing and the plan finished
	// having bought three steps while reporting four.
	step4 := f.saveRootExecution(ctx, t, 4)
	require.NoError(t, f.execute(ctx, step4))
	assert.Equal(t, 4, f.currentStep(ctx, t), "step 4 completes the ramp")
	assert.Equal(t, 6, f.purchaseCount(), "step 4 commits once per account")
}

// TestCompletePlanStepIsIdempotentUnderConcurrency answers the question the
// row lock is supposed to settle: can two concurrent completions of the SAME
// ramp step both pass the guard and both advance?
//
// The goroutines are released together from a shared start gate and contend on
// the plan row for real (SELECT ... FOR UPDATE inside the store's transaction),
// so this is a measurement, not an argument from isolation levels.
func TestCompletePlanStepIsIdempotentUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	store, _ := rampStepStore(ctx, t)
	plan := saveConcurrencyRampPlan(ctx, t, store, "Concurrent Ramp Plan")

	const racers = 8
	start := make(chan struct{})
	errCh := make(chan error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errCh <- store.CompletePlanStep(ctx, plan.ID, 3)
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)

	seen := 0
	for err := range errCh {
		seen++
		require.NoError(t, err, "every concurrent completion of step 3 must succeed or no-op, never error")
	}
	require.Equal(t, racers, seen, "all racers must have reported")

	reloaded, err := store.GetPurchasePlan(ctx, plan.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, reloaded.RampSchedule.CurrentStep,
		"%d concurrent completions of step 3 must leave the ramp on step 3", racers)
}

// saveConcurrencyRampPlan persists a plan sitting on step 2 of 4.
func saveConcurrencyRampPlan(ctx context.Context, t *testing.T, store *config.PostgresStore, name string) *config.PurchasePlan {
	t.Helper()
	plan := &config.PurchasePlan{
		Name:     name,
		Services: map[string]config.ServiceConfig{},
		RampSchedule: config.RampSchedule{
			Type: "weekly", PercentPerStep: 25, StepIntervalDays: 7,
			CurrentStep: 2, TotalSteps: 4, StartDate: time.Now().AddDate(0, 0, -14),
		},
	}
	require.NoError(t, store.CreatePurchasePlan(ctx, plan))
	return plan
}

// TestCompletePlanStepBlocksOnTheRowLockAndSeesTheWinner forces the interleaving
// the previous test can only hope for, so the idempotency guard's dependence on
// the row lock is measured rather than argued.
//
// A transaction outside the store takes the plan row's FOR UPDATE lock and
// advances the ramp to step 3, exactly as the first per-account retry's
// transaction would. While that transaction is open, a concurrent
// CompletePlanStep(plan, 3) -- the second retry -- must block rather than read
// the pre-advance value: if it could proceed, both would see CurrentStep=2 and
// both would advance, which is the lost update the lock exists to prevent.
// Once the holder commits, the blocked call must observe the committed step 3
// and no-op.
func TestCompletePlanStepBlocksOnTheRowLockAndSeesTheWinner(t *testing.T) {
	ctx := context.Background()
	store, pool := rampStepStore(ctx, t)
	plan := saveConcurrencyRampPlan(ctx, t, store, "Locked Ramp Plan")

	holder, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = holder.Rollback(ctx) }()

	var lockedID string
	require.NoError(t,
		holder.QueryRow(ctx, `SELECT id FROM purchase_plans WHERE id = $1 FOR UPDATE`, plan.ID).Scan(&lockedID))
	require.Equal(t, plan.ID, lockedID)

	// The winning retry's advance, still uncommitted.
	_, err = holder.Exec(ctx,
		`UPDATE purchase_plans SET ramp_schedule = jsonb_set(ramp_schedule, '{current_step}', '3') WHERE id = $1`,
		plan.ID)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- store.CompletePlanStep(ctx, plan.ID, 3) }()

	// Wait until Postgres reports the completion's own SELECT ... FOR UPDATE
	// blocked on a lock, so the "still running" check below is not a race
	// against goroutine start-up. Matching the query text matters: if the store
	// stopped taking the row lock on the READ, the blocked statement would be
	// the later UPDATE instead and this poll would never match.
	require.Eventually(t, func() bool {
		var blocked int
		if qErr := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE wait_event_type = 'Lock'
			   AND state = 'active'
			   AND pid <> pg_backend_pid()
			   AND query ILIKE '%purchase_plans%FOR UPDATE%'`,
		).Scan(&blocked); qErr != nil {
			return false
		}
		return blocked > 0
	}, 15*time.Second, 25*time.Millisecond,
		"the concurrent completion's locking read must wait on the plan row lock")

	select {
	case earlyErr := <-done:
		t.Fatalf("CompletePlanStep returned (%v) while another transaction held the plan row lock: it did not serialize", earlyErr)
	default:
	}

	require.NoError(t, holder.Commit(ctx))

	select {
	case loserErr := <-done:
		require.NoError(t, loserErr, "the loser of the race must no-op cleanly, not error")
	case <-time.After(15 * time.Second):
		t.Fatal("CompletePlanStep never returned after the row lock was released")
	}

	reloaded, err := store.GetPurchasePlan(ctx, plan.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, reloaded.RampSchedule.CurrentStep,
		"the blocked completion must observe the committed step 3 and leave it alone")
	// CurrentStep alone cannot tell the two outcomes apart: a stale read of
	// step 2 followed by an advance also lands on 3. last_execution_date can.
	// The holder's UPDATE never touched it, and only the advancing path in
	// CompletePlanStep writes it, so its absence proves the blocked call took
	// the no-op branch rather than re-advancing off a pre-commit read.
	assert.Nil(t, reloaded.LastExecutionDate,
		"the blocked completion must no-op, not re-advance the ramp from a stale read")
}
