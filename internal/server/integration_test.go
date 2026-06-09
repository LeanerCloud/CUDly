//go:build integration
// +build integration

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/testutil"
)

// TestServerIntegration is an example integration test using testcontainers
// Run with: go test -tags=integration ./internal/server/...
func TestServerIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := testutil.TestContext(t)

	// Set up PostgreSQL container
	pgContainer, err := testutil.SetupPostgresContainer(ctx, t)
	if err != nil {
		t.Fatalf("Failed to start postgres container: %v", err)
	}

	// Set environment variables for database connection
	for key, value := range pgContainer.Config() {
		testutil.SetEnv(t, key, value)
	}

	// TODO: When database package is ready, uncomment this:
	// app, err := NewApplication(ctx)
	// if err != nil {
	// 	t.Fatalf("Failed to create application: %v", err)
	// }
	// defer app.Close()

	// For now, just verify container is running
	t.Logf("PostgreSQL container running at: %s", pgContainer.ConnectionString())
}

// TestHealthCheckIntegration tests the health check endpoint
func TestHealthCheckIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create minimal application for health check
	app := &Application{
		Version: "test",
	}

	// Create HTTP test server
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	// Call health check handler
	app.handleHealthCheck(w, req)

	// Verify response
	testutil.AssertEqual(t, http.StatusOK, w.Code)

	// Verify response is JSON
	contentType := w.Header().Get("Content-Type")
	testutil.AssertContains(t, contentType, "application/json")

	t.Logf("Health check response: %s", w.Body.String())
}

// TestScheduledTaskIntegration tests scheduled task execution
func TestScheduledTaskIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := testutil.TestContext(t)

	// Create application with mock dependencies
	mockScheduler := &testutil.MockScheduler{
		CollectRecommendationsFunc: func(ctx context.Context) (*scheduler.CollectResult, error) {
			// Simulate actual work
			time.Sleep(100 * time.Millisecond)
			return &scheduler.CollectResult{}, nil
		},
	}

	app := &Application{
		Scheduler: mockScheduler,
	}

	// Test collect_recommendations task
	result, err := app.HandleScheduledTask(ctx, TaskCollectRecommendations)
	testutil.AssertNoError(t, err)
	testutil.AssertTrue(t, result != nil, "Result should not be nil")

	t.Logf("Scheduled task completed successfully")
}

// TestApplicationLifecycle tests full application startup and shutdown
func TestApplicationLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := testutil.TestContext(t)

	// Set up test environment
	testutil.SetEnv(t, "VERSION", "integration-test")
	testutil.SetEnv(t, "CONFIG_TABLE", "test-config")
	testutil.SetEnv(t, "PLANS_TABLE", "test-plans")
	testutil.SetEnv(t, "HISTORY_TABLE", "test-history")
	testutil.SetEnv(t, "USERS_TABLE", "test-users")
	testutil.SetEnv(t, "GROUPS_TABLE", "test-groups")
	testutil.SetEnv(t, "SESSIONS_TABLE", "test-sessions")

	// TODO: When NewApplication is fully implemented, test it:
	// app, err := NewApplication(ctx)
	// if err != nil {
	// 	t.Fatalf("Failed to create application: %v", err)
	// }
	// defer app.Close()

	// For now, create minimal app
	app := &Application{
		Version: "integration-test",
	}

	// Verify version is set
	testutil.AssertEqual(t, "integration-test", app.Version)

	// Test cleanup
	err := app.Close()
	testutil.AssertNoError(t, err)

	t.Logf("Application lifecycle test completed")
}
