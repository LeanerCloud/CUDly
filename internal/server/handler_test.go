package server

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/LeanerCloud/CUDly/internal/testutil"
)

// mockTaskLocker implements TaskLocker for testing
type mockTaskLocker struct {
	acquired    bool
	err         error
	lockCalls   int
	unlockCalls int
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
		name        string
		taskType    ScheduledTaskType
		setupMocks  func(*testutil.MockScheduler, *testutil.MockPurchaseManager)
		expectError bool
	}{
		{
			name:     "collect_recommendations success",
			taskType: TaskCollectRecommendations,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				s.CollectRecommendationsFunc = func(ctx context.Context) (*scheduler.CollectResult, error) {
					return &scheduler.CollectResult{}, nil
				}
			},
			expectError: false,
		},
		{
			name:     "collect_recommendations failure",
			taskType: TaskCollectRecommendations,
			setupMocks: func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {
				s.CollectRecommendationsFunc = func(ctx context.Context) (*scheduler.CollectResult, error) {
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
			name:        "unknown task type",
			taskType:    ScheduledTaskType("unknown"),
			setupMocks:  func(s *testutil.MockScheduler, p *testutil.MockPurchaseManager) {},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testutil.TestContext(t)

			mockScheduler := &testutil.MockScheduler{}
			mockPurchase := &testutil.MockPurchaseManager{}
			tt.setupMocks(mockScheduler, mockPurchase)

			app := &Application{
				Scheduler: mockScheduler,
				Purchase:  mockPurchase,
			}

			_, err := app.HandleScheduledTask(ctx, tt.taskType)

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
	mockScheduler.CollectRecommendationsFunc = func(ctx context.Context) (*scheduler.CollectResult, error) {
		return &scheduler.CollectResult{Recommendations: 5}, nil
	}

	app := &Application{
		Scheduler: mockScheduler,
		Purchase:  &testutil.MockPurchaseManager{},
		DB:        nil, // No DB — lock path skipped
	}

	result, err := app.HandleScheduledTask(ctx, TaskCollectRecommendations)
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

		_, err := app.HandleScheduledTask(ctx, TaskCleanupExpiredRecords)
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

		result, err := app.HandleScheduledTask(ctx, TaskCollectRecommendations)
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

		_, err := app.HandleScheduledTask(ctx, TaskCollectRecommendations)
		testutil.AssertError(t, err)
		testutil.AssertContains(t, err.Error(), "failed to check task lock")
	})
}

func TestHandleSQSMessage(t *testing.T) {
	tests := []struct {
		name        string
		messageBody string
		setupMocks  func(*testutil.MockPurchaseManager)
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
		name         string
		rawEvent     string
		expectedTask ScheduledTaskType
		expectError  bool
	}{
		{
			name:         "collect_recommendations event",
			rawEvent:     `{"action": "collect_recommendations"}`,
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
			taskType, err := ParseScheduledEvent([]byte(tt.rawEvent))
			if tt.expectError {
				testutil.AssertError(t, err)
			} else {
				testutil.AssertNoError(t, err)
				testutil.AssertEqual(t, tt.expectedTask, taskType)
			}
		})
	}
}
