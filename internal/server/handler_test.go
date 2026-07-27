package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/mocks"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/LeanerCloud/CUDly/internal/testutil"
	"github.com/stretchr/testify/mock"
)

// mockTaskLocker implements TaskLocker for testing.
type mockTaskLocker struct {
	err         error
	lockCalls   int
	unlockCalls int
	acquired    bool
}

func (m *mockTaskLocker) TryAdvisoryLock(_ context.Context, _ int64) (bool, error) {
	m.lockCalls++
	return m.acquired, m.err
}

func (m *mockTaskLocker) ReleaseAdvisoryLock(_ context.Context, _ int64) {
	m.unlockCalls++
}

func TestHandleScheduledTask(t *testing.T) {
	tests := []struct {
		setupMocks  func(*testutil.MockScheduler, *testutil.MockPurchaseManager)
		name        string
		taskType    ScheduledTaskType
		expectError bool
	}{
		{
			name:     "collect_recommendations success",
			taskType: TaskCollectRecommendations,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				s.CollectRecommendationsFunc = func(ctx context.Context, ownerToken string) (*scheduler.CollectResult, error) {
					return &scheduler.CollectResult{}, nil
				}
			},
			expectError: false,
		},
		{
			name:     "collect_recommendations failure",
			taskType: TaskCollectRecommendations,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				s.CollectRecommendationsFunc = func(ctx context.Context, ownerToken string) (*scheduler.CollectResult, error) {
					return nil, errors.New("collection failed")
				}
			},
			expectError: true,
		},
		{
			name:     "process_scheduled_purchases success",
			taskType: TaskProcessScheduledPurchases,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				p.ProcessScheduledPurchasesFunc = func(ctx context.Context) (*purchase.ProcessResult, error) {
					return &purchase.ProcessResult{}, nil
				}
			},
			expectError: false,
		},
		{
			name:     "send_notifications success",
			taskType: TaskSendNotifications,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				p.SendUpcomingPurchaseNotificationsFunc = func(ctx context.Context) (*purchase.NotificationResult, error) {
					return &purchase.NotificationResult{}, nil
				}
			},
			expectError: false,
		},
		{
			name:        "cleanup success",
			taskType:    TaskCleanupExpiredRecords,
			setupMocks:  func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {},
			expectError: false,
		},
		{
			name:        "analytics_refresh success",
			taskType:    TaskRefreshAnalytics,
			setupMocks:  func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {},
			expectError: false,
		},
		{
			name:     "reap_stuck_purchases success",
			taskType: TaskReapStuckPurchases,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				p.ReapStuckExecutionsFunc = func(ctx context.Context, reapAfter time.Duration) (*purchase.ReapResult, error) {
					// The wiring uses ParseReapAfterFromEnv; default is
					// 10 min when env is unset (which it is in tests).
					if reapAfter != 10*time.Minute {
						return nil, errors.New("expected default 10m threshold when env unset")
					}
					return &purchase.ReapResult{Found: 2, Reaped: 2}, nil
				}
			},
			expectError: false,
		},
		{
			name:     "reap_stuck_purchases propagates store error",
			taskType: TaskReapStuckPurchases,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				p.ReapStuckExecutionsFunc = func(ctx context.Context, reapAfter time.Duration) (*purchase.ReapResult, error) {
					return nil, errors.New("db down")
				}
			},
			expectError: true,
		},
		{
			name:     "fire_scheduled_purchases success",
			taskType: TaskFireScheduledPurchases,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				p.FireScheduledDelayedPurchasesFunc = func(ctx context.Context) (*purchase.FireResult, error) {
					return &purchase.FireResult{Found: 1, Fired: 1}, nil
				}
			},
			expectError: false,
		},
		{
			name:     "fire_scheduled_purchases propagates error",
			taskType: TaskFireScheduledPurchases,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				p.FireScheduledDelayedPurchasesFunc = func(ctx context.Context) (*purchase.FireResult, error) {
					return nil, errors.New("db down")
				}
			},
			expectError: true,
		},
		{
			name:     "finalize_revocations success",
			taskType: TaskFinalizeRevocations,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				p.FinalizeInFlightRevocationsFunc = func(ctx context.Context) (*purchase.FinalizeResult, error) {
					return &purchase.FinalizeResult{Found: 1, Finalized: 1}, nil
				}
			},
			expectError: false,
		},
		{
			name:     "finalize_revocations propagates error",
			taskType: TaskFinalizeRevocations,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				p.FinalizeInFlightRevocationsFunc = func(ctx context.Context) (*purchase.FinalizeResult, error) {
					return nil, errors.New("db down")
				}
			},
			expectError: true,
		},
		{
			name:        "unknown task type",
			taskType:    ScheduledTaskType("unknown"),
			setupMocks:  func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear PURCHASE_APPROVED_REAP_AFTER so the reap_stuck_purchases
			// cases below see the deterministic default (10m) regardless of
			// ambient env in CI/dev. The reap subtests assert reapAfter ==
			// 10*time.Minute in their setupMocks; without this, an
			// inherited env value would silently make them flaky (A5 CR).
			// t.Setenv automatically restores the prior value at cleanup.
			t.Setenv("PURCHASE_APPROVED_REAP_AFTER", "")

			ctx := testutil.TestContext(t)

			mockScheduler := &testutil.MockScheduler{}
			mockPurchase := &testutil.MockPurchaseManager{}
			tt.setupMocks(mockScheduler, mockPurchase)

			app := &Application{
				Scheduler: mockScheduler,
				Purchase:  mockPurchase,
			}

			_, err := app.HandleScheduledTask(ctx, tt.taskType, ScheduledTaskParams{})

			if tt.expectError {
				testutil.AssertError(t, err)
			} else {
				testutil.AssertNoError(t, err)
			}
		})
	}
}

func TestTaskLockID(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		id1 := taskLockID(TaskCollectRecommendations)
		id2 := taskLockID(TaskCollectRecommendations)
		testutil.AssertEqual(t, id1, id2)
	})

	t.Run("different tasks produce different IDs", func(t *testing.T) {
		id1 := taskLockID(TaskCollectRecommendations)
		id2 := taskLockID(TaskRIExchangeReshape)
		testutil.AssertNotEqual(t, id1, id2)
	})

	t.Run("all task types unique", func(t *testing.T) {
		tasks := []ScheduledTaskType{
			TaskCollectRecommendations,
			TaskProcessScheduledPurchases,
			TaskSendNotifications,
			TaskCleanupExpiredRecords,
			TaskRefreshAnalytics,
			TaskRIExchangeReshape,
			TaskReapStuckPurchases,
		}
		seen := make(map[int64]ScheduledTaskType)
		for _, task := range tasks {
			id := taskLockID(task)
			if prev, exists := seen[id]; exists {
				t.Fatalf("lock ID collision: %s and %s both produce %d", prev, task, id)
			}
			seen[id] = task
		}
	})
}

func TestHandleScheduledTaskSkipsWhenDBNil(t *testing.T) {
	ctx := testutil.TestContext(t)

	mockScheduler := &testutil.MockScheduler{}
	mockScheduler.CollectRecommendationsFunc = func(ctx context.Context, ownerToken string) (*scheduler.CollectResult, error) {
		return &scheduler.CollectResult{Recommendations: 5}, nil
	}

	app := &Application{
		Scheduler: mockScheduler,
		Purchase:  &testutil.MockPurchaseManager{},
		DB:        nil, // No DB — lock path skipped
	}

	result, err := app.HandleScheduledTask(ctx, TaskCollectRecommendations, ScheduledTaskParams{})
	testutil.AssertNoError(t, err)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestHandleScheduledTaskAdvisoryLock(t *testing.T) {
	t.Run("lock acquired - task executes", func(t *testing.T) {
		ctx := testutil.TestContext(t)
		locker := &mockTaskLocker{acquired: true}

		app := &Application{
			Scheduler:  &testutil.MockScheduler{},
			Purchase:   &testutil.MockPurchaseManager{},
			TaskLocker: locker,
		}

		_, err := app.HandleScheduledTask(ctx, TaskCleanupExpiredRecords, ScheduledTaskParams{})
		testutil.AssertNoError(t, err)
		testutil.AssertEqual(t, 1, locker.lockCalls)
		testutil.AssertEqual(t, 1, locker.unlockCalls)
	})

	t.Run("lock not acquired - task skipped", func(t *testing.T) {
		ctx := testutil.TestContext(t)
		locker := &mockTaskLocker{acquired: false}

		app := &Application{
			Scheduler:  &testutil.MockScheduler{},
			Purchase:   &testutil.MockPurchaseManager{},
			TaskLocker: locker,
		}

		result, err := app.HandleScheduledTask(ctx, TaskCollectRecommendations, ScheduledTaskParams{})
		testutil.AssertNoError(t, err)
		testutil.AssertEqual(t, 1, locker.lockCalls)
		testutil.AssertEqual(t, 0, locker.unlockCalls)

		m, ok := result.(map[string]string)
		if !ok {
			t.Fatalf("expected map[string]string, got %T", result)
		}
		testutil.AssertEqual(t, "skipped", m["status"])
		testutil.AssertEqual(t, "already_running", m["reason"])
	})

	t.Run("lock error - returns error", func(t *testing.T) {
		ctx := testutil.TestContext(t)
		locker := &mockTaskLocker{err: errors.New("db connection lost")}

		app := &Application{
			Scheduler:  &testutil.MockScheduler{},
			Purchase:   &testutil.MockPurchaseManager{},
			TaskLocker: locker,
		}

		_, err := app.HandleScheduledTask(ctx, TaskCollectRecommendations, ScheduledTaskParams{})
		testutil.AssertError(t, err)
		testutil.AssertContains(t, err.Error(), "failed to check task lock")
	})
}

// TestHandleScheduledTaskReleasesMarkerWhenSkipped pins the abandoned-marker
// leak in the issue #261 compare-and-clear guard: a collect run that WON
// MarkCollectionStarted (so it owns the in-flight marker) but is then skipped
// by the advisory lock never reaches CollectRecommendations, so the deferred
// token-scoped clear never fires. Before this release the marker sat stranded
// for the full 5-minute auto-recovery window, rejecting every refresh the user
// attempted with 409 while nothing backed by that marker was running.
func TestHandleScheduledTaskReleasesMarkerWhenSkipped(t *testing.T) {
	t.Run("owner token released when the run is skipped", func(t *testing.T) {
		ctx := testutil.TestContext(t)
		store := new(mocks.MockConfigStore)
		t.Cleanup(func() { store.AssertExpectations(t) })
		store.On("ClearCollectionStarted", mock.Anything, "tok-owner").Return(nil)

		app := &Application{
			Config:     store,
			Scheduler:  &testutil.MockScheduler{},
			Purchase:   &testutil.MockPurchaseManager{},
			TaskLocker: &mockTaskLocker{acquired: false},
		}

		result, err := app.HandleScheduledTask(ctx, TaskCollectRecommendations,
			ScheduledTaskParams{OwnerToken: "tok-owner"})
		testutil.AssertNoError(t, err)

		m, ok := result.(map[string]string)
		if !ok {
			t.Fatalf("expected map[string]string, got %T", result)
		}
		testutil.AssertEqual(t, "skipped", m["status"])
		store.AssertCalled(t, "ClearCollectionStarted", mock.Anything, "tok-owner")
	})

	// A tokenless run (EventBridge cron, the /api/scheduled/ HTTP path, the
	// --task CLI) owns no marker, so a skip must not clear anything: that
	// would be exactly the cross-run wipe issue #261 closes. The expectation
	// is registered (as Maybe) so an unwanted call is still recorded rather
	// than falling through the mock's no-expectation default and letting
	// AssertNotCalled pass vacuously.
	t.Run("tokenless skipped run clears nothing", func(t *testing.T) {
		ctx := testutil.TestContext(t)
		store := new(mocks.MockConfigStore)
		t.Cleanup(func() { store.AssertExpectations(t) })
		store.On("ClearCollectionStarted", mock.Anything, mock.Anything).Return(nil).Maybe()

		app := &Application{
			Config:     store,
			Scheduler:  &testutil.MockScheduler{},
			Purchase:   &testutil.MockPurchaseManager{},
			TaskLocker: &mockTaskLocker{acquired: false},
		}

		_, err := app.HandleScheduledTask(ctx, TaskCollectRecommendations, ScheduledTaskParams{})
		testutil.AssertNoError(t, err)
		store.AssertNotCalled(t, "ClearCollectionStarted", mock.Anything, mock.Anything)
	})

	// A lock-check error is returned to the caller, so the Lambda async invoke
	// retries the same event with the same owner token and the collect can
	// still run. Releasing the marker there would strand the retry.
	t.Run("lock error keeps the marker for the retry", func(t *testing.T) {
		ctx := testutil.TestContext(t)
		store := new(mocks.MockConfigStore)
		t.Cleanup(func() { store.AssertExpectations(t) })
		store.On("ClearCollectionStarted", mock.Anything, mock.Anything).Return(nil).Maybe()

		app := &Application{
			Config:     store,
			Scheduler:  &testutil.MockScheduler{},
			Purchase:   &testutil.MockPurchaseManager{},
			TaskLocker: &mockTaskLocker{err: errors.New("db connection lost")},
		}

		_, err := app.HandleScheduledTask(ctx, TaskCollectRecommendations,
			ScheduledTaskParams{OwnerToken: "tok-owner"})
		testutil.AssertError(t, err)
		store.AssertNotCalled(t, "ClearCollectionStarted", mock.Anything, mock.Anything)
	})

	// Only collect_recommendations carries an owner token. A stray token on
	// another task type must never reach the collection marker.
	t.Run("other task types never touch the marker", func(t *testing.T) {
		ctx := testutil.TestContext(t)
		store := new(mocks.MockConfigStore)
		t.Cleanup(func() { store.AssertExpectations(t) })
		store.On("ClearCollectionStarted", mock.Anything, mock.Anything).Return(nil).Maybe()

		app := &Application{
			Config:     store,
			Scheduler:  &testutil.MockScheduler{},
			Purchase:   &testutil.MockPurchaseManager{},
			TaskLocker: &mockTaskLocker{acquired: false},
		}

		_, err := app.HandleScheduledTask(ctx, TaskCleanupExpiredRecords,
			ScheduledTaskParams{OwnerToken: "tok-owner"})
		testutil.AssertNoError(t, err)
		store.AssertNotCalled(t, "ClearCollectionStarted", mock.Anything, mock.Anything)
	})
}

func TestHandleSQSMessage(t *testing.T) {
	tests := []struct {
		setupMocks  func(*testutil.MockPurchaseManager)
		name        string
		messageBody string
		expectError bool
	}{
		{
			name:        "valid message",
			messageBody: `{"purchase_id": "123"}`,
			setupMocks: func(p *testutil.MockPurchaseManager) {
				p.ProcessMessageFunc = func(ctx context.Context, body string) error {
					return nil
				}
			},
			expectError: false,
		},
		{
			name:        "invalid message",
			messageBody: `{"invalid": "data"}`,
			setupMocks: func(p *testutil.MockPurchaseManager) {
				p.ProcessMessageFunc = func(ctx context.Context, body string) error {
					return errors.New("invalid message format")
				}
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testutil.TestContext(t)

			mockPurchase := &testutil.MockPurchaseManager{}
			tt.setupMocks(mockPurchase)

			app := &Application{
				Purchase: mockPurchase,
			}

			err := app.HandleSQSMessage(ctx, tt.messageBody)

			if tt.expectError {
				testutil.AssertError(t, err)
			} else {
				testutil.AssertNoError(t, err)
			}
		})
	}
}

func TestParseScheduledEvent(t *testing.T) {
	tests := []struct {
		name          string
		rawEvent      string
		expectedTask  ScheduledTaskType
		expectedToken string
		expectError   bool
	}{
		{
			name:         "collect_recommendations event",
			rawEvent:     `{"action": "collect_recommendations"}`,
			expectedTask: TaskCollectRecommendations,
		},
		{
			// Async self-invoke payload carries the owner token so the
			// scheduler can scope ClearCollectionStarted to this run
			// (issue #261 compare-and-clear guard).
			name:          "collect_recommendations event with owner_token",
			rawEvent:      `{"source": "aws.events", "action": "collect_recommendations", "owner_token": "` + testOwnerToken + `"}`,
			expectedTask:  TaskCollectRecommendations,
			expectedToken: testOwnerToken,
		},
		{
			// A non-empty owner_token is validated at the boundary: the only
			// legitimate producer is asyncInvokeSelf, which always sends a
			// uuid.New(), so a non-UUID value means a corrupt payload. It
			// could never match a marker owner, and letting it through would
			// strand that marker for the full 5-minute recovery window with
			// only a buried error log to show for it.
			name:        "collect_recommendations event with malformed owner_token",
			rawEvent:    `{"source": "aws.events", "action": "collect_recommendations", "owner_token": "tok-1"}`,
			expectError: true,
		},
		{
			// An absent owner_token stays legitimate: cron, the
			// /api/scheduled/ HTTP path and the --task CLI never win
			// MarkCollectionStarted and own no marker to clear.
			name:         "collect_recommendations event with empty owner_token",
			rawEvent:     `{"source": "aws.events", "action": "collect_recommendations", "owner_token": ""}`,
			expectedTask: TaskCollectRecommendations,
		},
		{
			name:         "process_scheduled_purchases event",
			rawEvent:     `{"action": "process_scheduled_purchases"}`,
			expectedTask: TaskProcessScheduledPurchases,
		},
		{
			name:         "send_notifications event",
			rawEvent:     `{"action": "send_notifications"}`,
			expectedTask: TaskSendNotifications,
		},
		{
			name:         "cleanup event",
			rawEvent:     `{"action": "cleanup"}`,
			expectedTask: TaskCleanupExpiredRecords,
		},
		{
			name:         "analytics_refresh event",
			rawEvent:     `{"action": "analytics_refresh"}`,
			expectedTask: TaskRefreshAnalytics,
		},
		{
			name:         "reap_stuck_purchases event",
			rawEvent:     `{"action": "reap_stuck_purchases"}`,
			expectedTask: TaskReapStuckPurchases,
		},
		{
			name:         "fire_scheduled_purchases event",
			rawEvent:     `{"action": "fire_scheduled_purchases"}`,
			expectedTask: TaskFireScheduledPurchases,
		},
		{
			name:         "finalize_revocations event",
			rawEvent:     `{"action": "finalize_revocations"}`,
			expectedTask: TaskFinalizeRevocations,
		},
		{
			name:        "unknown action returns error",
			rawEvent:    `{"action": "unknown"}`,
			expectError: true,
		},
		{
			name:        "invalid JSON returns error",
			rawEvent:    `{invalid json}`,
			expectError: true,
		},
		{
			name:         "EventBridge format",
			rawEvent:     `{"source": "aws.events", "action": "send_notifications"}`,
			expectedTask: TaskSendNotifications,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskType, params, err := ParseScheduledEvent([]byte(tt.rawEvent))
			if tt.expectError {
				testutil.AssertError(t, err)
			} else {
				testutil.AssertNoError(t, err)
				testutil.AssertEqual(t, tt.expectedTask, taskType)
				testutil.AssertEqual(t, tt.expectedToken, params.OwnerToken)
			}
		})
	}
}
