package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/api"
	"github.com/LeanerCloud/CUDly/internal/server"
	"github.com/LeanerCloud/CUDly/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestApp creates a minimal Application for testing with no DB dependency
func createTestApp() *server.Application {
	apiHandler := api.NewHandler(api.HandlerConfig{})
	return &server.Application{
		API:       apiHandler,
		Scheduler: &testutil.MockScheduler{},
		Purchase:  &testutil.MockPurchaseManager{},
	}
}

func TestInitApp_Cached(t *testing.T) {
	// Save and restore the global app
	origApp := app
	defer func() { app = origApp }()

	testApp := createTestApp()
	app = testApp

	result, err := initApp(context.Background())
	require.NoError(t, err)
	assert.Equal(t, testApp, result, "should return cached app")
}

func TestInitApp_SetsVersion(t *testing.T) {
	origApp := app
	origVersion := Version
	origDBHost := os.Getenv("DB_HOST")
	defer func() {
		app = origApp
		Version = origVersion
		os.Setenv("DB_HOST", origDBHost)
	}()

	// Ensure app is nil so initApp tries to initialize
	app = nil
	Version = "test-v1.2.3"
	os.Unsetenv("DB_HOST")

	_, err := initApp(context.Background())
	// It will fail because DB_HOST is not set, but Version should have been set
	require.Error(t, err)
	assert.Equal(t, "test-v1.2.3", os.Getenv("VERSION"))
}

func TestInitApp_EmptyVersion(t *testing.T) {
	origApp := app
	origVersion := Version
	origDBHost := os.Getenv("DB_HOST")
	origEnvVersion := os.Getenv("VERSION")
	defer func() {
		app = origApp
		Version = origVersion
		if origDBHost != "" {
			os.Setenv("DB_HOST", origDBHost)
		} else {
			os.Unsetenv("DB_HOST")
		}
		if origEnvVersion != "" {
			os.Setenv("VERSION", origEnvVersion)
		} else {
			os.Unsetenv("VERSION")
		}
	}()

	app = nil
	Version = ""
	os.Unsetenv("DB_HOST")
	os.Unsetenv("VERSION")

	_, err := initApp(context.Background())
	require.Error(t, err)
	// When Version is empty, os.Setenv("VERSION", Version) should NOT be called
	// so VERSION env should remain unset or whatever it was before
}

func TestInitApp_FailsWithoutDB(t *testing.T) {
	origApp := app
	origDBHost := os.Getenv("DB_HOST")
	defer func() {
		app = origApp
		if origDBHost != "" {
			os.Setenv("DB_HOST", origDBHost)
		} else {
			os.Unsetenv("DB_HOST")
		}
	}()

	app = nil
	os.Unsetenv("DB_HOST")

	_, err := initApp(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize application")
}

func TestHandler_InitFailure(t *testing.T) {
	origApp := app
	origDBHost := os.Getenv("DB_HOST")
	defer func() {
		app = origApp
		if origDBHost != "" {
			os.Setenv("DB_HOST", origDBHost)
		} else {
			os.Unsetenv("DB_HOST")
		}
	}()

	app = nil
	os.Unsetenv("DB_HOST")

	rawEvent := json.RawMessage(`{"action":"collect_recommendations"}`)
	_, err := Handler(context.Background(), rawEvent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initialization failed")
}

func TestHandler_ScheduledEvent(t *testing.T) {
	origApp := app
	defer func() { app = origApp }()

	app = createTestApp()

	// Scheduled event - will be processed by the app
	rawEvent := json.RawMessage(`{"source":"aws.events","detail-type":"Scheduled Event","action":"collect_recommendations"}`)
	result, err := Handler(context.Background(), rawEvent)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestHandler_SQSEvent(t *testing.T) {
	origApp := app
	defer func() { app = origApp }()

	app = createTestApp()

	rawEvent := json.RawMessage(`{"Records":[{"eventSource":"aws:sqs","messageId":"msg-1","body":"{}"}]}`)
	result, err := Handler(context.Background(), rawEvent)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestHandler_HTTPEvent(t *testing.T) {
	origApp := app
	defer func() { app = origApp }()

	app = createTestApp()

	rawEvent := json.RawMessage(`{"requestContext":{"http":{"method":"GET","path":"/api/health"}},"rawPath":"/api/health","headers":{}}`)
	result, err := Handler(context.Background(), rawEvent)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestHandler_ReusesApp(t *testing.T) {
	origApp := app
	defer func() { app = origApp }()

	app = createTestApp()

	rawEvent := json.RawMessage(`{"source":"aws.events","action":"collect_recommendations"}`)

	// Call twice - should reuse the same app
	result1, err1 := Handler(context.Background(), rawEvent)
	require.NoError(t, err1)

	result2, err2 := Handler(context.Background(), rawEvent)
	require.NoError(t, err2)

	assert.NotNil(t, result1)
	assert.NotNil(t, result2)
}
