package server

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/LeanerCloud/CUDly/internal/testutil"
)

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
