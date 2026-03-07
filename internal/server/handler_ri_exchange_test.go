package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/testutil"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
)

func TestHandleRIExchangeReshape_DisabledConfig(t *testing.T) {
	ctx := testutil.TestContext(t)

	store := &mockConfigStoreForExchange{
		globalConfig: &config.GlobalConfig{
			RIExchangeEnabled: false,
		},
	}

	app := &Application{
		Config: store,
	}

	result, err := app.handleRIExchangeReshape(ctx)

	testutil.AssertNoError(t, err)
	if result != nil {
		t.Error("expected nil result when exchange is disabled")
	}
}

func TestHandleRIExchangeReshape_ConfigLoadFailure(t *testing.T) {
	ctx := testutil.TestContext(t)

	store := &mockConfigStoreForExchange{
		globalConfigErr: errors.New("db connection failed"),
	}

	app := &Application{
		Config: store,
	}

	_, err := app.handleRIExchangeReshape(ctx)

	testutil.AssertError(t, err)
	if err.Error() != "failed to load config: db connection failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseScheduledEvent_RIExchangeReshape(t *testing.T) {
	taskType, err := ParseScheduledEvent([]byte(`{"action": "ri_exchange_reshape"}`))
	testutil.AssertNoError(t, err)
	testutil.AssertEqual(t, TaskRIExchangeReshape, taskType)
}

func TestBuildExchangeNotificationData(t *testing.T) {
	result := &exchange.AutoExchangeResult{
		Mode: "manual",
		Completed: []exchange.ExchangeOutcome{
			{
				RecordID:           "rec-1",
				SourceRIID:         "ri-completed",
				SourceInstanceType: "m5.large",
				TargetInstanceType: "m6i.large",
				TargetCount:        1,
				PaymentDue:         "5.00",
				ExchangeID:         "exch-1",
				UtilizationPct:     45.0,
			},
		},
		Pending: []exchange.ExchangeOutcome{
			{
				RecordID:           "rec-2",
				ApprovalToken:      "token-abc",
				SourceRIID:         "ri-pending",
				SourceInstanceType: "c5.xlarge",
				TargetInstanceType: "c6i.xlarge",
				TargetCount:        2,
				PaymentDue:         "10.50",
				UtilizationPct:     30.0,
			},
		},
		Skipped: []exchange.SkippedRecommendation{
			{
				SourceRIID:         "ri-skipped",
				SourceInstanceType: "t3.micro",
				Reason:             "no matching offering",
			},
		},
	}

	data := buildExchangeNotificationData(result, "https://dashboard.example.com")

	testutil.AssertEqual(t, "https://dashboard.example.com", data.DashboardURL)
	testutil.AssertEqual(t, "manual", data.Mode)
	testutil.AssertEqual(t, 2, len(data.Exchanges))
	testutil.AssertEqual(t, 1, len(data.Skipped))

	// Verify completed exchange is in the list
	testutil.AssertEqual(t, "ri-completed", data.Exchanges[0].SourceRIID)
	testutil.AssertEqual(t, "exch-1", data.Exchanges[0].ExchangeID)

	// Verify pending exchange is in the list
	testutil.AssertEqual(t, "ri-pending", data.Exchanges[1].SourceRIID)
	testutil.AssertEqual(t, "token-abc", data.Exchanges[1].ApprovalToken)

	// Verify skipped
	testutil.AssertEqual(t, "ri-skipped", data.Skipped[0].SourceRIID)
	testutil.AssertEqual(t, "no matching offering", data.Skipped[0].Reason)
}

func TestBuildExchangeNotificationData_Empty(t *testing.T) {
	result := &exchange.AutoExchangeResult{Mode: "auto"}
	data := buildExchangeNotificationData(result, "https://example.com")

	testutil.AssertEqual(t, "auto", data.Mode)
	testutil.AssertEqual(t, 0, len(data.Exchanges))
	testutil.AssertEqual(t, 0, len(data.Skipped))
}

func TestConvertForAutoExchange(t *testing.T) {
	instances := []ec2svc.ConvertibleRI{
		{
			ReservedInstanceID:  "ri-123",
			InstanceType:        "m5.large",
			InstanceCount:       2,
			NormalizationFactor: 4.0,
			ProductDescription:  "Linux/UNIX",
			InstanceTenancy:     "default",
			Scope:               "Region",
			Duration:            31536000,
		},
		{
			ReservedInstanceID:  "ri-456",
			InstanceType:        "c5.xlarge",
			InstanceCount:       1,
			NormalizationFactor: 8.0,
			ProductDescription:  "Windows",
			InstanceTenancy:     "dedicated",
			Scope:               "Availability Zone",
			Duration:            94608000,
		},
	}

	utilData := []recommendations.RIUtilization{
		{
			ReservedInstanceID: "ri-123",
			UtilizationPercent: 45.0,
		},
		{
			ReservedInstanceID: "ri-456",
			UtilizationPercent: 80.0,
		},
	}

	riInfos, utilInfos, riMetadata := convertForAutoExchange(instances, utilData)

	// Check RI infos
	testutil.AssertEqual(t, 2, len(riInfos))
	testutil.AssertEqual(t, "ri-123", riInfos[0].ID)
	testutil.AssertEqual(t, "m5.large", riInfos[0].InstanceType)
	testutil.AssertEqual(t, int32(2), riInfos[0].InstanceCount)
	testutil.AssertEqual(t, "convertible", riInfos[0].OfferingClass)

	// Check utilization infos
	testutil.AssertEqual(t, 2, len(utilInfos))
	testutil.AssertEqual(t, "ri-123", utilInfos[0].RIID)
	testutil.AssertEqual(t, 45.0, utilInfos[0].UtilizationPercent)

	// Check RI metadata
	testutil.AssertEqual(t, 2, len(riMetadata))
	meta := riMetadata["ri-123"]
	testutil.AssertEqual(t, "Linux/UNIX", meta.ProductDescription)
	testutil.AssertEqual(t, "default", meta.InstanceTenancy)
	testutil.AssertEqual(t, "Region", meta.Scope)
	testutil.AssertEqual(t, int64(31536000), meta.Duration)

	meta2 := riMetadata["ri-456"]
	testutil.AssertEqual(t, "Windows", meta2.ProductDescription)
	testutil.AssertEqual(t, "dedicated", meta2.InstanceTenancy)
}

func TestConfigExchangeStoreAdapter(t *testing.T) {
	savedRecord := (*config.RIExchangeRecord)(nil)
	mockStore := &mockConfigStoreForExchange{
		saveRIExchangeRecordFunc: func(ctx context.Context, record *config.RIExchangeRecord) error {
			savedRecord = record
			return nil
		},
		cancelAllPendingFunc: func(ctx context.Context) (int64, error) {
			return 3, nil
		},
		getDailySpendFunc: func(ctx context.Context, date time.Time) (string, error) {
			return "42.50", nil
		},
	}

	adapter := newConfigExchangeStoreAdapter(mockStore)

	t.Run("SaveRIExchangeRecord", func(t *testing.T) {
		ctx := testutil.TestContext(t)
		record := &exchange.ExchangeRecord{
			AccountID:          "123456789",
			Region:             "us-east-1",
			SourceRIIDs:        []string{"ri-123"},
			SourceInstanceType: "m5.large",
			TargetOfferingID:   "off-456",
			TargetInstanceType: "m6i.large",
			TargetCount:        1,
			PaymentDue:         "5.00",
			Status:             "pending",
			Mode:               "manual",
		}

		err := adapter.SaveRIExchangeRecord(ctx, record)
		testutil.AssertNoError(t, err)

		if savedRecord == nil {
			t.Fatal("expected record to be saved")
		}
		testutil.AssertEqual(t, "123456789", savedRecord.AccountID)
		testutil.AssertEqual(t, "m5.large", savedRecord.SourceInstanceType)
		testutil.AssertEqual(t, "m6i.large", savedRecord.TargetInstanceType)
	})

	t.Run("CancelAllPendingExchanges", func(t *testing.T) {
		ctx := testutil.TestContext(t)
		count, err := adapter.CancelAllPendingExchanges(ctx)
		testutil.AssertNoError(t, err)
		testutil.AssertEqual(t, int64(3), count)
	})

	t.Run("GetRIExchangeDailySpend", func(t *testing.T) {
		ctx := testutil.TestContext(t)
		spend, err := adapter.GetRIExchangeDailySpend(ctx, time.Now())
		testutil.AssertNoError(t, err)
		testutil.AssertEqual(t, "42.50", spend)
	})
}

func TestSendExchangeNotification_NoEmailSender(t *testing.T) {
	app := &Application{Email: nil}
	result := &exchange.AutoExchangeResult{Mode: "auto"}
	// Should not panic when Email is nil
	app.sendExchangeNotification(context.Background(), result)
}

func TestSendExchangeNotification_NoResults(t *testing.T) {
	emailSent := false
	app := &Application{
		Email: &mockEmailSender{
			sendCompletedFunc: func(ctx context.Context, data email.RIExchangeNotificationData) error {
				emailSent = true
				return nil
			},
		},
	}

	result := &exchange.AutoExchangeResult{Mode: "auto"}
	app.sendExchangeNotification(context.Background(), result)

	if emailSent {
		t.Error("expected no email to be sent for empty results")
	}
}

func TestSendExchangeNotification_ManualPending(t *testing.T) {
	approvalSent := false
	app := &Application{
		Email: &mockEmailSender{
			sendApprovalFunc: func(ctx context.Context, data email.RIExchangeNotificationData) error {
				approvalSent = true
				return nil
			},
		},
	}

	result := &exchange.AutoExchangeResult{
		Mode: "manual",
		Pending: []exchange.ExchangeOutcome{
			{SourceRIID: "ri-1"},
		},
	}
	app.sendExchangeNotification(context.Background(), result)

	if !approvalSent {
		t.Error("expected approval email to be sent")
	}
}

func TestSendExchangeNotification_AutoCompleted(t *testing.T) {
	completedSent := false
	app := &Application{
		Email: &mockEmailSender{
			sendCompletedFunc: func(ctx context.Context, data email.RIExchangeNotificationData) error {
				completedSent = true
				return nil
			},
		},
	}

	result := &exchange.AutoExchangeResult{
		Mode: "auto",
		Completed: []exchange.ExchangeOutcome{
			{SourceRIID: "ri-1", ExchangeID: "exch-1"},
		},
	}
	app.sendExchangeNotification(context.Background(), result)

	if !completedSent {
		t.Error("expected completed email to be sent")
	}
}

func TestSendExchangeNotification_EmailFailure(t *testing.T) {
	app := &Application{
		Email: &mockEmailSender{
			sendCompletedFunc: func(ctx context.Context, data email.RIExchangeNotificationData) error {
				return errors.New("SES rate limit exceeded")
			},
		},
	}

	result := &exchange.AutoExchangeResult{
		Mode: "auto",
		Completed: []exchange.ExchangeOutcome{
			{SourceRIID: "ri-1"},
		},
	}
	// Should not panic on email failure — just logs
	app.sendExchangeNotification(context.Background(), result)
}

// --- Mock types ---

type mockConfigStoreForExchange struct {
	mockConfigStoreForHealth // embed base mock for unused methods
	globalConfig             *config.GlobalConfig
	globalConfigErr          error
	saveRIExchangeRecordFunc func(ctx context.Context, record *config.RIExchangeRecord) error
	cancelAllPendingFunc     func(ctx context.Context) (int64, error)
	getDailySpendFunc        func(ctx context.Context, date time.Time) (string, error)
}

func (m *mockConfigStoreForExchange) GetGlobalConfig(ctx context.Context) (*config.GlobalConfig, error) {
	if m.globalConfigErr != nil {
		return nil, m.globalConfigErr
	}
	if m.globalConfig != nil {
		return m.globalConfig, nil
	}
	return &config.GlobalConfig{}, nil
}

func (m *mockConfigStoreForExchange) SaveRIExchangeRecord(ctx context.Context, record *config.RIExchangeRecord) error {
	if m.saveRIExchangeRecordFunc != nil {
		return m.saveRIExchangeRecordFunc(ctx, record)
	}
	return nil
}

func (m *mockConfigStoreForExchange) CancelAllPendingExchanges(ctx context.Context) (int64, error) {
	if m.cancelAllPendingFunc != nil {
		return m.cancelAllPendingFunc(ctx)
	}
	return 0, nil
}

func (m *mockConfigStoreForExchange) GetRIExchangeDailySpend(ctx context.Context, date time.Time) (string, error) {
	if m.getDailySpendFunc != nil {
		return m.getDailySpendFunc(ctx, date)
	}
	return "0", nil
}

type mockEmailSender struct {
	sendApprovalFunc  func(ctx context.Context, data email.RIExchangeNotificationData) error
	sendCompletedFunc func(ctx context.Context, data email.RIExchangeNotificationData) error
}

func (m *mockEmailSender) SendNotification(context.Context, string, string) error { return nil }
func (m *mockEmailSender) SendToEmail(context.Context, string, string, string) error {
	return nil
}
func (m *mockEmailSender) SendNewRecommendationsNotification(context.Context, email.NotificationData) error {
	return nil
}
func (m *mockEmailSender) SendScheduledPurchaseNotification(context.Context, email.NotificationData) error {
	return nil
}
func (m *mockEmailSender) SendPurchaseConfirmation(context.Context, email.NotificationData) error {
	return nil
}
func (m *mockEmailSender) SendPurchaseFailedNotification(context.Context, email.NotificationData) error {
	return nil
}
func (m *mockEmailSender) SendPasswordResetEmail(context.Context, string, string) error {
	return nil
}
func (m *mockEmailSender) SendWelcomeEmail(context.Context, string, string, string) error {
	return nil
}

func (m *mockEmailSender) SendRIExchangePendingApproval(ctx context.Context, data email.RIExchangeNotificationData) error {
	if m.sendApprovalFunc != nil {
		return m.sendApprovalFunc(ctx, data)
	}
	return nil
}

func (m *mockEmailSender) SendRIExchangeCompleted(ctx context.Context, data email.RIExchangeNotificationData) error {
	if m.sendCompletedFunc != nil {
		return m.sendCompletedFunc(ctx, data)
	}
	return nil
}
