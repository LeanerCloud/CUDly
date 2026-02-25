package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HealthStatus represents the overall health of the application
type HealthStatus struct {
	Status    string                 `json:"status"`
	Version   string                 `json:"version"`
	Timestamp time.Time              `json:"timestamp"`
	Checks    map[string]CheckResult `json:"checks"`
}

// CheckResult represents the result of a health check
type CheckResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// handleHealthCheck returns the health status of the application
func (app *Application) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	health := HealthStatus{
		Status:    "healthy",
		Version:   app.Version,
		Timestamp: time.Now(),
		Checks:    make(map[string]CheckResult),
	}

	// Check configuration store
	health.Checks["config_store"] = app.checkConfigStore(ctx)
	if health.Checks["config_store"].Status != "healthy" {
		health.Status = "degraded"
	}

	// Check auth store
	health.Checks["auth_store"] = app.checkAuthStore(ctx)
	if health.Checks["auth_store"].Status != "healthy" {
		health.Status = "degraded"
	}

	// Always return 200 for the health endpoint so startup/liveness probes pass.
	// The actual health status is in the JSON body. "degraded" means the app is
	// running but some dependencies (like DB) aren't connected yet - this is
	// expected during cold starts with lazy DB initialization.
	statusCode := http.StatusOK

	// Write response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(health)
}

// checkConfigStore checks the health of the configuration store
func (app *Application) checkConfigStore(ctx context.Context) CheckResult {
	// Check if config store exists
	if app.Config == nil {
		// If using PostgreSQL with lazy initialization, DB might not be connected yet
		if app.dbConfig != nil {
			return CheckResult{
				Status:  "pending",
				Message: "Database connection pending (lazy initialization)",
			}
		}
		return CheckResult{
			Status:  "unhealthy",
			Message: "Config store not initialized",
		}
	}

	// If using PostgreSQL, check database connection health
	if app.DB != nil {
		if err := app.DB.HealthCheck(ctx); err != nil {
			return CheckResult{
				Status:  "unhealthy",
				Message: fmt.Sprintf("Database health check failed: %v", err),
			}
		}
	}

	return CheckResult{
		Status: "healthy",
	}
}

// checkAuthStore checks the health of the auth store
func (app *Application) checkAuthStore(ctx context.Context) CheckResult {
	if app.Auth == nil {
		return CheckResult{
			Status:  "unhealthy",
			Message: "Auth service not initialized",
		}
	}

	// Ping the database to verify connection is healthy
	if err := app.Auth.Ping(ctx); err != nil {
		return CheckResult{
			Status:  "unhealthy",
			Message: fmt.Sprintf("Auth store ping failed: %v", err),
		}
	}

	return CheckResult{
		Status: "healthy",
	}
}
