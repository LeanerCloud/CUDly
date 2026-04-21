package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/api"
	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/LeanerCloud/CUDly/internal/testutil"
	"github.com/aws/aws-lambda-go/events"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIsLambdaRuntime(t *testing.T) {
	// Save and restore
	orig := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	defer func() {
		if orig != "" {
			os.Setenv("AWS_LAMBDA_RUNTIME_API", orig)
		} else {
			os.Unsetenv("AWS_LAMBDA_RUNTIME_API")
		}
	}()

	os.Unsetenv("AWS_LAMBDA_RUNTIME_API")
	testutil.AssertEqual(t, false, isLambdaRuntime())

	os.Setenv("AWS_LAMBDA_RUNTIME_API", "localhost:9001")
	testutil.AssertEqual(t, true, isLambdaRuntime())
}

func TestClose(t *testing.T) {
	t.Run("nil DB", func(t *testing.T) {
		app := &Application{DB: nil}
		err := app.Close()
		testutil.AssertNoError(t, err)
	})
}

func TestEnsureDB_NilDBConfig(t *testing.T) {
	app := &Application{dbConfig: nil}
	err := app.ensureDB(context.Background())
	testutil.AssertNoError(t, err)
}

func TestGetEnvInt(t *testing.T) {
	key := "TEST_GET_ENV_INT_CUDLY"
	defer os.Unsetenv(key)

	// Default value
	testutil.AssertEqual(t, 42, getEnvInt(key, 42))

	// Valid int
	os.Setenv(key, "100")
	testutil.AssertEqual(t, 100, getEnvInt(key, 42))

	// Invalid int - returns default
	os.Setenv(key, "not-a-number")
	testutil.AssertEqual(t, 42, getEnvInt(key, 42))
}

func TestGetEnvFloat(t *testing.T) {
	key := "TEST_GET_ENV_FLOAT_CUDLY"
	defer os.Unsetenv(key)

	// Default value
	testutil.AssertEqual(t, 80.0, getEnvFloat(key, 80.0))

	// Valid float
	os.Setenv(key, "95.5")
	testutil.AssertEqual(t, 95.5, getEnvFloat(key, 80.0))

	// Invalid float - returns default
	os.Setenv(key, "not-a-float")
	testutil.AssertEqual(t, 80.0, getEnvFloat(key, 80.0))
}

func TestHttpToLambdaRequest_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	req.Header.Set("User-Agent", "TestAgent/1.0")

	lambdaReq := httpToLambdaRequest(req)

	testutil.AssertEqual(t, "5.6.7.8", lambdaReq.RequestContext.HTTP.SourceIP)
	testutil.AssertEqual(t, "TestAgent/1.0", lambdaReq.RequestContext.HTTP.UserAgent)
}

func TestHttpToLambdaRequest_NilBody(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Body = nil

	lambdaReq := httpToLambdaRequest(req)

	testutil.AssertEqual(t, "", lambdaReq.Body)
}

func TestLambdaResponseToHTTP_Base64Body(t *testing.T) {
	content := "Hello, World!"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	resp := &events.LambdaFunctionURLResponse{
		StatusCode:      200,
		Body:            encoded,
		IsBase64Encoded: true,
		Headers: map[string]string{
			"Content-Type": "application/octet-stream",
		},
	}

	w := httptest.NewRecorder()
	lambdaResponseToHTTP(w, resp)

	testutil.AssertEqual(t, 200, w.Code)
	testutil.AssertEqual(t, content, w.Body.String())
}

func TestLambdaResponseToHTTP_InvalidBase64(t *testing.T) {
	resp := &events.LambdaFunctionURLResponse{
		StatusCode:      200,
		Body:            "not-valid-base64!!!",
		IsBase64Encoded: true,
	}

	w := httptest.NewRecorder()
	lambdaResponseToHTTP(w, resp)
	// Should attempt to write error
	testutil.AssertTrue(t, w.Body.Len() > 0, "Should have written something")
}

func TestHandleHTTPRequest(t *testing.T) {
	app := &Application{
		API: api.NewHandler(api.HandlerConfig{}),
	}

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()

	app.handleHTTPRequest(w, req)

	// Should get some response (possibly 200 from health or 404)
	testutil.AssertTrue(t, w.Code > 0, "Should have a status code")
}

func TestHandleHTTPRequest_WithBody(t *testing.T) {
	app := &Application{
		API: api.NewHandler(api.HandlerConfig{}),
	}

	body := bytes.NewReader([]byte(`{"test":"data"}`))
	req := httptest.NewRequest("POST", "/api/test", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	app.handleHTTPRequest(w, req)

	testutil.AssertTrue(t, w.Code > 0, "Should have a status code")
}

func TestHandleScheduledHTTP_TaskError(t *testing.T) {
	app := &Application{
		Scheduler: &testutil.MockScheduler{},
		Purchase:  &testutil.MockPurchaseManager{},
	}

	// Unknown task type causes error
	req := httptest.NewRequest("POST", "/api/scheduled/invalid_task_type", nil)
	w := httptest.NewRecorder()

	app.handleScheduledHTTP(w, req)

	testutil.AssertEqual(t, 500, w.Code)
}

func TestHandleScheduledHTTP_ProcessPurchases(t *testing.T) {
	app := &Application{
		Purchase: &testutil.MockPurchaseManager{
			ProcessScheduledPurchasesFunc: func(ctx context.Context) (*purchase.ProcessResult, error) {
				return &purchase.ProcessResult{Processed: 2, Executed: 1}, nil
			},
		},
	}

	req := httptest.NewRequest("POST", "/api/scheduled/process_scheduled_purchases", nil)
	w := httptest.NewRecorder()

	app.handleScheduledHTTP(w, req)

	testutil.AssertEqual(t, 200, w.Code)
}

func TestHandleScheduledHTTP_SendNotifications(t *testing.T) {
	app := &Application{
		Purchase: &testutil.MockPurchaseManager{
			SendUpcomingPurchaseNotificationsFunc: func(ctx context.Context) (*purchase.NotificationResult, error) {
				return &purchase.NotificationResult{Notified: 3}, nil
			},
		},
	}

	req := httptest.NewRequest("POST", "/api/scheduled/send_notifications", nil)
	w := httptest.NewRecorder()

	app.handleScheduledHTTP(w, req)

	testutil.AssertEqual(t, 200, w.Code)
}

func TestHandleProcessScheduledPurchases_Error(t *testing.T) {
	ctx := testutil.TestContext(t)
	app := &Application{
		Purchase: &testutil.MockPurchaseManager{
			ProcessScheduledPurchasesFunc: func(ctx context.Context) (*purchase.ProcessResult, error) {
				return nil, errors.New("purchase processing failed")
			},
		},
	}

	_, err := app.HandleScheduledTask(ctx, TaskProcessScheduledPurchases)
	testutil.AssertError(t, err)
}

func TestHandleSendNotifications_Error(t *testing.T) {
	ctx := testutil.TestContext(t)
	app := &Application{
		Purchase: &testutil.MockPurchaseManager{
			SendUpcomingPurchaseNotificationsFunc: func(ctx context.Context) (*purchase.NotificationResult, error) {
				return nil, errors.New("notification failed")
			},
		},
	}

	_, err := app.HandleScheduledTask(ctx, TaskSendNotifications)
	testutil.AssertError(t, err)
}

func TestHandleCollectRecommendations_WithResults(t *testing.T) {
	ctx := testutil.TestContext(t)
	app := &Application{
		Scheduler: &testutil.MockScheduler{
			CollectRecommendationsFunc: func(ctx context.Context) (*scheduler.CollectResult, error) {
				return &scheduler.CollectResult{
					Recommendations: 15,
					TotalSavings:    2500.50,
				}, nil
			},
		},
	}

	result, err := app.HandleScheduledTask(ctx, TaskCollectRecommendations)
	testutil.AssertNoError(t, err)
	testutil.AssertTrue(t, result != nil, "Result should not be nil")
}

// noopEmailSender is a minimal email.SenderInterface for unit tests
var _ email.SenderInterface = (*noopEmailSender)(nil)

type noopEmailSender struct{}

func (n *noopEmailSender) SendNotification(ctx context.Context, subject, message string) error {
	return nil
}
func (n *noopEmailSender) SendToEmail(ctx context.Context, toEmail, subject, body string) error {
	return nil
}
func (n *noopEmailSender) SendNewRecommendationsNotification(ctx context.Context, data email.NotificationData) error {
	return nil
}
func (n *noopEmailSender) SendScheduledPurchaseNotification(ctx context.Context, data email.NotificationData) error {
	return nil
}
func (n *noopEmailSender) SendPurchaseConfirmation(ctx context.Context, data email.NotificationData) error {
	return nil
}
func (n *noopEmailSender) SendPurchaseFailedNotification(ctx context.Context, data email.NotificationData) error {
	return nil
}
func (n *noopEmailSender) SendPasswordResetEmail(ctx context.Context, emailAddr, resetURL string) error {
	return nil
}
func (n *noopEmailSender) SendWelcomeEmail(ctx context.Context, emailAddr, dashboardURL, role string) error {
	return nil
}
func (n *noopEmailSender) SendRIExchangePendingApproval(ctx context.Context, data email.RIExchangeNotificationData) error {
	return nil
}
func (n *noopEmailSender) SendRIExchangeCompleted(ctx context.Context, data email.RIExchangeNotificationData) error {
	return nil
}
func (n *noopEmailSender) SendPurchaseApprovalRequest(ctx context.Context, data email.NotificationData) error {
	return nil
}
func (n *noopEmailSender) SendRegistrationReceivedNotification(_ context.Context, _ email.RegistrationNotificationData) error {
	return nil
}
func (n *noopEmailSender) SendRegistrationDecisionNotification(_ context.Context, _ string, _ email.RegistrationDecisionData) error {
	return nil
}

func TestLoadApplicationConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		// Clear all env vars that LoadApplicationConfig reads
		envVars := []string{
			"VERSION", "NOTIFICATION_DAYS_BEFORE", "DEFAULT_TERM",
			"DEFAULT_PAYMENT_OPTION", "DEFAULT_COVERAGE", "DEFAULT_RAMP_SCHEDULE",
			"API_KEY_SECRET_ARN", "ENABLE_DASHBOARD", "DASHBOARD_BUCKET",
			"DASHBOARD_URL", "CORS_ALLOWED_ORIGIN", "AWS_LAMBDA_RUNTIME_API",
		}
		for _, key := range envVars {
			testutil.SetEnv(t, key, "")
		}

		cfg := LoadApplicationConfig()

		testutil.AssertEqual(t, "dev", cfg.Version)
		testutil.AssertEqual(t, 3, cfg.NotificationDaysBefore)
		testutil.AssertEqual(t, 3, cfg.DefaultTerm)
		testutil.AssertEqual(t, "", cfg.DefaultPaymentOption)
		testutil.AssertEqual(t, 80.0, cfg.DefaultCoverage)
		testutil.AssertEqual(t, "", cfg.DefaultRampSchedule)
		testutil.AssertEqual(t, false, cfg.EnableDashboard)
		testutil.AssertEqual(t, false, cfg.IsLambda)
	})

	t.Run("custom values", func(t *testing.T) {
		testutil.SetEnv(t, "VERSION", "1.2.3")
		testutil.SetEnv(t, "NOTIFICATION_DAYS_BEFORE", "7")
		testutil.SetEnv(t, "DEFAULT_TERM", "1")
		testutil.SetEnv(t, "DEFAULT_PAYMENT_OPTION", "AllUpfront")
		testutil.SetEnv(t, "DEFAULT_COVERAGE", "95.5")
		testutil.SetEnv(t, "DEFAULT_RAMP_SCHEDULE", "linear")
		testutil.SetEnv(t, "API_KEY_SECRET_ARN", "arn:api:key")
		testutil.SetEnv(t, "ENABLE_DASHBOARD", "true")
		testutil.SetEnv(t, "DASHBOARD_BUCKET", "my-bucket")
		testutil.SetEnv(t, "DASHBOARD_URL", "https://dash.example.com")
		testutil.SetEnv(t, "CORS_ALLOWED_ORIGIN", "https://example.com")
		testutil.SetEnv(t, "AWS_LAMBDA_RUNTIME_API", "localhost:9001")

		cfg := LoadApplicationConfig()

		testutil.AssertEqual(t, "1.2.3", cfg.Version)
		testutil.AssertEqual(t, 7, cfg.NotificationDaysBefore)
		testutil.AssertEqual(t, 1, cfg.DefaultTerm)
		testutil.AssertEqual(t, "AllUpfront", cfg.DefaultPaymentOption)
		testutil.AssertEqual(t, 95.5, cfg.DefaultCoverage)
		testutil.AssertEqual(t, "linear", cfg.DefaultRampSchedule)
		testutil.AssertEqual(t, "arn:api:key", cfg.APIKeySecretARN)
		testutil.AssertEqual(t, true, cfg.EnableDashboard)
		testutil.AssertEqual(t, "my-bucket", cfg.DashboardBucket)
		testutil.AssertEqual(t, "https://dash.example.com", cfg.DashboardURL)
		testutil.AssertEqual(t, "https://example.com", cfg.CORSAllowedOrigin)
		testutil.AssertEqual(t, true, cfg.IsLambda)
	})
}

func TestNewApplicationFromDeps(t *testing.T) {
	ctx := testutil.TestContext(t)

	validDBConfig := &database.Config{
		Host:     "localhost",
		Port:     5432,
		Database: "cudly_test",
		User:     "test",
		Password: "test",
		SSLMode:  "disable",
	}

	baseCfg := ApplicationConfig{
		Version:                "test-v1",
		NotificationDaysBefore: 3,
		DefaultTerm:            3,
		DefaultCoverage:        80,
		IsLambda:               false,
	}

	t.Run("non-Lambda path with in-memory rate limiter", func(t *testing.T) {
		deps := ExternalDeps{
			EmailSender: &noopEmailSender{},
			DBConfig:    validDBConfig,
		}

		app, err := NewApplicationFromDeps(ctx, baseCfg, deps)
		testutil.AssertNoError(t, err)
		testutil.AssertTrue(t, app != nil, "App should not be nil")
		testutil.AssertEqual(t, "test-v1", app.Version)
		testutil.AssertTrue(t, app.API != nil, "API handler should be created")
		testutil.AssertTrue(t, app.Scheduler != nil, "Scheduler should be created")
		testutil.AssertTrue(t, app.Purchase != nil, "Purchase manager should be created")
		testutil.AssertTrue(t, app.Auth != nil, "Auth service should be created")
		testutil.AssertTrue(t, app.RateLimiter != nil, "Rate limiter should be in-memory for non-Lambda")
		testutil.AssertTrue(t, app.DB == nil, "DB should be nil (lazy init)")
	})

	t.Run("Lambda path with nil rate limiter", func(t *testing.T) {
		lambdaCfg := baseCfg
		lambdaCfg.IsLambda = true

		deps := ExternalDeps{
			EmailSender: &noopEmailSender{},
			DBConfig:    validDBConfig,
		}

		app, err := NewApplicationFromDeps(ctx, lambdaCfg, deps)
		testutil.AssertNoError(t, err)
		testutil.AssertTrue(t, app.RateLimiter == nil, "Rate limiter should be nil for Lambda (initialized after DB connect)")
	})

	t.Run("nil dbConfig returns error", func(t *testing.T) {
		deps := ExternalDeps{
			EmailSender: &noopEmailSender{},
			DBConfig:    nil,
		}

		app, err := NewApplicationFromDeps(ctx, baseCfg, deps)
		testutil.AssertError(t, err)
		testutil.AssertTrue(t, app == nil, "App should be nil on error")
		testutil.AssertContains(t, err.Error(), "database configuration required")
	})

	t.Run("nil email sender is accepted", func(t *testing.T) {
		deps := ExternalDeps{
			EmailSender: nil,
			DBConfig:    validDBConfig,
		}

		app, err := NewApplicationFromDeps(ctx, baseCfg, deps)
		testutil.AssertNoError(t, err)
		testutil.AssertTrue(t, app != nil, "App should be created even with nil email sender")
	})

	t.Run("config values are wired correctly", func(t *testing.T) {
		cfg := ApplicationConfig{
			Version:                "v2.0",
			NotificationDaysBefore: 7,
			DefaultTerm:            1,
			DefaultPaymentOption:   "AllUpfront",
			DefaultCoverage:        95.5,
			APIKeySecretARN:        "arn:aws:key",
			EnableDashboard:        true,
			DashboardBucket:        "bucket",
			DashboardURL:           "https://dash.test",
			CORSAllowedOrigin:      "https://test.com",
			IsLambda:               false,
		}

		deps := ExternalDeps{
			EmailSender: &noopEmailSender{},
			DBConfig:    validDBConfig,
		}

		app, err := NewApplicationFromDeps(ctx, cfg, deps)
		testutil.AssertNoError(t, err)
		testutil.AssertEqual(t, "v2.0", app.Version)
		testutil.AssertEqual(t, cfg.DashboardURL, app.appConfig.DashboardURL)
		testutil.AssertEqual(t, cfg.CORSAllowedOrigin, app.appConfig.CORSAllowedOrigin)
	})
}

func TestInitConfigStore(t *testing.T) {
	t.Run("missing DB_HOST returns error", func(t *testing.T) {
		testutil.SetEnv(t, "DB_HOST", "")

		_, _, _, err := initConfigStore(context.Background())
		testutil.AssertError(t, err)
		testutil.AssertContains(t, err.Error(), "DB_HOST must be set")
	})

	t.Run("valid env returns nil config store and valid dbConfig", func(t *testing.T) {
		testutil.SetEnv(t, "DB_HOST", "localhost")
		testutil.SetEnv(t, "DB_PASSWORD", "testpass")
		testutil.SetEnv(t, "DB_SSL_MODE", "disable")
		testutil.SetEnv(t, "SECRET_PROVIDER", "env")
		testutil.SetEnv(t, "AWS_REGION_CONFIG", "us-east-1")

		configStore, dbConfig, resolver, err := initConfigStore(context.Background())
		testutil.AssertNoError(t, err)
		testutil.AssertTrue(t, configStore == nil, "Config store should be nil (lazy init)")
		testutil.AssertTrue(t, dbConfig != nil, "DB config should not be nil")
		testutil.AssertTrue(t, resolver != nil, "Secret resolver should not be nil")
		testutil.AssertEqual(t, "localhost", dbConfig.Host)
	})
}

func TestHandleHTTPRequest_EnsureDBError(t *testing.T) {
	app := &Application{
		API:      api.NewHandler(api.HandlerConfig{}),
		dbConfig: &database.Config{Host: "unreachable"},
		dbErr:    fmt.Errorf("connection failed"),
	}

	req := httptest.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()

	app.handleHTTPRequest(w, req)

	testutil.AssertEqual(t, 503, w.Code)
}

func TestHandleLambdaEvent_EnsureDBError(t *testing.T) {
	app := &Application{
		API:      api.NewHandler(api.HandlerConfig{}),
		dbConfig: &database.Config{Host: "unreachable"},
		dbErr:    fmt.Errorf("connection failed"),
	}

	rawEvent := json.RawMessage(`{"requestContext":{"http":{"method":"GET"}}}`)
	_, err := app.HandleLambdaEvent(context.Background(), rawEvent)
	testutil.AssertError(t, err)
	testutil.AssertContains(t, err.Error(), "connection failed")
}

func TestHandleScheduledHTTP_EnsureDBError(t *testing.T) {
	app := &Application{
		dbConfig: &database.Config{Host: "unreachable"},
		dbErr:    fmt.Errorf("db error"),
	}

	req := httptest.NewRequest("POST", "/api/scheduled/collect_recommendations", nil)
	w := httptest.NewRecorder()

	app.handleScheduledHTTP(w, req)

	testutil.AssertEqual(t, 503, w.Code)
}

// ---------- runMigrationsBounded tests ----------
//
// These tests exercise the goroutine+timeout+recover logic in isolation by
// swapping the package-level runMigrations hook. They do NOT hit a real DB,
// so the *pgxpool.Pool passed in is a typed-nil — the fake runner never
// dereferences it. MUST NOT run in parallel since the hook is global.

func withFakeMigrations(t *testing.T, fake func(ctx context.Context, pool *pgxpool.Pool, path, email, pw string) error) {
	t.Helper()
	orig := runMigrations
	runMigrations = fake
	t.Cleanup(func() { runMigrations = orig })
}

func TestRunMigrationsBounded_Success(t *testing.T) {
	withFakeMigrations(t, func(ctx context.Context, _ *pgxpool.Pool, _, _, _ string) error {
		return nil
	})
	err := runMigrationsBounded(nil, "", "", "", 1*time.Second)
	testutil.AssertNoError(t, err)
}

func TestRunMigrationsBounded_FailureReturnsError(t *testing.T) {
	sentinel := errors.New("dirty at 27")
	withFakeMigrations(t, func(ctx context.Context, _ *pgxpool.Pool, _, _, _ string) error {
		return sentinel
	})
	err := runMigrationsBounded(nil, "", "", "", 1*time.Second)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected error to wrap sentinel, got: %v", err)
	}
}

func TestRunMigrationsBounded_Timeout(t *testing.T) {
	withFakeMigrations(t, func(ctx context.Context, _ *pgxpool.Pool, _, _, _ string) error {
		<-ctx.Done() // block until runMigrationsBounded cancels the ctx
		return ctx.Err()
	})

	start := time.Now()
	err := runMigrationsBounded(nil, "", "", "", 50*time.Millisecond)
	elapsed := time.Since(start)

	testutil.AssertError(t, err)
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected 'timed out' in error; got %q", err.Error())
	}
	// runMigrationsBounded must return shortly after the timeout elapses.
	// If it took significantly longer than 2× the budget, the <-done
	// rendezvous hung — meaning the goroutine wasn't properly cleaned up.
	if elapsed > 200*time.Millisecond {
		t.Fatalf("runMigrationsBounded took too long (%s); goroutine may have leaked", elapsed)
	}
}

func TestRunMigrationsBounded_PanicRecovered(t *testing.T) {
	withFakeMigrations(t, func(ctx context.Context, _ *pgxpool.Pool, _, _, _ string) error {
		panic("boom")
	})
	err := runMigrationsBounded(nil, "", "", "", 1*time.Second)
	testutil.AssertError(t, err)
	if !strings.Contains(err.Error(), "panic") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected panic error to mention both 'panic' and 'boom'; got %q", err.Error())
	}
}
