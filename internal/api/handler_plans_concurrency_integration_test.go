//go:build integration
// +build integration

package api

// Real-Postgres concurrency tests for the create-planned-purchases path (issue
// #1861). The guard that keeps a create off a ramp step some account already
// bought is only a guard if it is decided and acted on under the same lock the
// completion path takes. Read outside that lock it is advisory: two creates both
// see the step free, each mints a root row for it, and approving both
// re-fans-out over the accounts that already bought under a fresh idempotency
// lineage, which is the duplicate commitment the whole guard exists to prevent.
//
// A mock cannot hold a row lock, so this measures against a real database.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// createConcurrencyFixture is a plan sitting on ramp step 2 of 4, wired to a
// handler backed by a real store.
type createConcurrencyFixture struct {
	store   *config.PostgresStore
	pool    *pgxpool.Pool
	handler *Handler
	planID  string
}

func newCreateConcurrencyFixture(ctx context.Context, t *testing.T) *createConcurrencyFixture {
	t.Helper()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Cleanup(context.Background()) })
	require.NoError(t, migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", ""))

	store := config.NewPostgresStore(container.DB)

	plan := &config.PurchasePlan{
		Name:     "Concurrent Create Plan",
		Services: map[string]config.ServiceConfig{},
		RampSchedule: config.RampSchedule{
			Type: "weekly", PercentPerStep: 25, StepIntervalDays: 7,
			CurrentStep: 2, TotalSteps: 4, StartDate: time.Now().AddDate(0, 0, -14),
		},
	}
	require.NoError(t, store.CreatePurchasePlan(ctx, plan))

	acct := &config.CloudAccount{
		Name: "acct-a", Provider: "aws", ExternalID: "333333333333", Enabled: true,
	}
	require.NoError(t, store.CreateCloudAccount(ctx, acct))
	require.NoError(t, store.SetPlanAccounts(ctx, plan.ID, []string{acct.ID}))

	// A real users row: createPurchaseExecutionsTx stamps the session user onto
	// created_by_user_id, which is a foreign key, so a synthetic UUID would make
	// every insert fail for a reason unrelated to what these tests measure.
	userID := uuid.New().String()
	_, err = container.DB.Pool().Exec(ctx, `
		INSERT INTO users (id, email, password_hash, salt, active, group_ids, created_at, updated_at)
		SELECT $1, 'creator@example.com', 'x', 'y', true, ARRAY[g.id], now(), now()
		  FROM groups g ORDER BY g.name LIMIT 1`, userID)
	require.NoError(t, err)

	mockAuth := new(MockAuthService)
	session := &Session{UserID: userID, Email: "creator@example.com"}
	mockAuth.On("ValidateSession", mock.Anything, "admin-token").Return(session, nil).Maybe()
	mockAuth.grantAdmin()

	return &createConcurrencyFixture{
		store:   store,
		pool:    container.DB.Pool(),
		handler: &Handler{config: store, auth: mockAuth},
		planID:  plan.ID,
	}
}

// create issues one create-planned-purchases request for `count` steps.
func (f *createConcurrencyFixture) create(ctx context.Context, count int) (*CreatePlannedPurchasesResponse, error) {
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    fmt.Sprintf(`{"count":%d,"start_date":"2026-09-01"}`, count),
	}
	return f.handler.createPlannedPurchases(ctx, req, f.planID)
}

// executionsForStep counts the plan's execution rows stamped with step.
func (f *createConcurrencyFixture) executionsForStep(ctx context.Context, t *testing.T, step int) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT count(*) FROM purchase_executions WHERE plan_id = $1 AND step_number = $2`,
		f.planID, step).Scan(&n))
	return n
}

// TestCreatePlannedPurchases_ConcurrentCreatesMintOneStepEach is the issue
// #1861 / F-A regression guard.
//
// Two creates enter with the same CurrentStep and race. Exactly one may win:
// the loser must see the winner's row and be refused, not mint a second root
// row for the same step. Serialized by hand this proves nothing, because the
// loser would read the winner's committed row anyway; the goroutines are
// released together from a shared gate so they contend for real.
func TestCreatePlannedPurchases_ConcurrentCreatesMintOneStepEach(t *testing.T) {
	ctx := context.Background()
	f := newCreateConcurrencyFixture(ctx, t)

	const racers = 6
	start := make(chan struct{})
	errs := make(chan error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := f.create(ctx, 1)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	won, refused, seen := 0, 0, 0
	for err := range errs {
		seen++
		if err == nil {
			won++
			continue
		}
		ce, ok := IsClientError(err)
		require.True(t, ok, "a losing create must be refused cleanly, got %v", err)
		require.Equal(t, 409, ce.code, "the loser must be refused as a conflict, got %d: %v", ce.code, err)
		refused++
	}
	require.Equal(t, racers, seen, "every racer must report")
	assert.Equal(t, 1, won, "exactly one create may mint ramp step 3")
	assert.Equal(t, racers-1, refused)

	assert.Equal(t, 1, f.executionsForStep(ctx, t, 3),
		"ramp step 3 must end with exactly one execution row, not one per racer")
}

// TestCreatePlannedPurchases_BlocksOnTheRampLockAndSeesTheWinner forces the
// interleaving the race test can only hope for, so the guard's dependence on the
// plan row lock is measured rather than argued.
//
// A transaction outside the handler takes the plan row's FOR UPDATE lock and
// inserts a pending root row for step 3, exactly as a winning create would.
// While that transaction is open, a concurrent create must BLOCK rather than
// read the pre-insert state: if it could proceed, it would find step 3 free and
// mint a second root row for it. Once the holder commits, the blocked create
// must observe the committed row and refuse.
func TestCreatePlannedPurchases_BlocksOnTheRampLockAndSeesTheWinner(t *testing.T) {
	ctx := context.Background()
	f := newCreateConcurrencyFixture(ctx, t)

	holder, err := f.pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = holder.Rollback(ctx) }()

	var lockedID string
	require.NoError(t,
		holder.QueryRow(ctx, `SELECT id FROM purchase_plans WHERE id = $1 FOR UPDATE`, f.planID).Scan(&lockedID))
	require.Equal(t, f.planID, lockedID)

	// The winning create's insert, still uncommitted.
	_, err = holder.Exec(ctx, `
		INSERT INTO purchase_executions (plan_id, execution_id, status, step_number, scheduled_date)
		VALUES ($1, $2, 'pending', 3, now())`, f.planID, uuid.New().String())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, createErr := f.create(ctx, 1)
		done <- createErr
	}()

	// Wait until Postgres reports the create's own locking read blocked. Match
	// the query text: if the handler stopped taking the row lock on the READ,
	// the blocked statement would be something else and this poll would never
	// match, failing the test rather than silently passing it.
	require.Eventually(t, func() bool {
		var blocked int
		if qErr := f.pool.QueryRow(ctx, `
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
		"the concurrent create's locking read must wait on the plan row lock")

	select {
	case earlyErr := <-done:
		t.Fatalf("createPlannedPurchases returned (%v) while another transaction held the plan row lock: it did not serialize", earlyErr)
	default:
	}

	require.NoError(t, holder.Commit(ctx))

	select {
	case loserErr := <-done:
		require.Error(t, loserErr, "the blocked create must see the committed row and refuse")
		ce, ok := IsClientError(loserErr)
		require.True(t, ok)
		assert.Equal(t, 409, ce.code)
	case <-time.After(15 * time.Second):
		t.Fatal("createPlannedPurchases never returned after the plan row lock was released")
	}

	assert.Equal(t, 1, f.executionsForStep(ctx, t, 3),
		"the blocked create must not have added a second row for step 3")
}

// TestCreatePlannedPurchases_CanceledStepIsReschedulable is the positive
// control for the widened predicate. A step whose executions were all canceled
// bought nothing and has nothing in flight, so the operator must be able to
// schedule it again; a guard that refused it would turn a cancel into a
// dead end.
func TestCreatePlannedPurchases_CanceledStepIsReschedulable(t *testing.T) {
	ctx := context.Background()
	f := newCreateConcurrencyFixture(ctx, t)

	require.NoError(t, f.store.SavePurchaseExecution(ctx, &config.PurchaseExecution{
		ExecutionID:   uuid.New().String(),
		PlanID:        f.planID,
		Status:        config.StatusCanceled,
		StepNumber:    3,
		ScheduledDate: time.Now(),
	}))

	resp, err := f.create(ctx, 1)
	require.NoError(t, err, "a step whose only rows were canceled must be reschedulable")
	assert.Equal(t, 1, resp.Created)
}
