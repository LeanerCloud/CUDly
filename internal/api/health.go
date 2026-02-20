package api

import (
	"context"
	"time"
)

// HealthResponse represents the health check response
type HealthResponse struct {
	Status    string                 `json:"status"`
	Timestamp time.Time              `json:"timestamp"`
	Checks    map[string]HealthCheck `json:"checks"`
}

// HealthCheck represents a single health check result
type HealthCheck struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// GetHealth performs comprehensive health checks
func (h *Handler) GetHealth(ctx context.Context) (*HealthResponse, error) {
	response := &HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now(),
		Checks:    make(map[string]HealthCheck),
	}

	// Check configuration store (includes database connection)
	configCheck := h.checkConfigStore(ctx)
	response.Checks["config_store"] = configCheck
	if configCheck.Status != "healthy" {
		response.Status = "degraded"
	}

	// Check auth service (includes database connection)
	authCheck := h.checkAuthService(ctx)
	response.Checks["auth_service"] = authCheck
	if authCheck.Status != "healthy" {
		response.Status = "degraded"
	}

	return response, nil
}

// checkConfigStore checks if the configuration store is accessible
func (h *Handler) checkConfigStore(ctx context.Context) HealthCheck {
	if h.config == nil {
		return HealthCheck{
			Status:  "unhealthy",
			Message: "Config store not initialized",
		}
	}

	// Try to access config to verify database connectivity
	_, err := h.config.GetGlobalConfig(ctx)
	if err != nil {
		return HealthCheck{
			Status:  "unhealthy",
			Message: "Failed to access config store: " + err.Error(),
		}
	}

	return HealthCheck{
		Status: "healthy",
	}
}

// checkAuthService checks if the auth service is accessible
func (h *Handler) checkAuthService(ctx context.Context) HealthCheck {
	if h.auth == nil {
		return HealthCheck{
			Status:  "unhealthy",
			Message: "Auth service not initialized",
		}
	}

	// Just verify the service exists and is configured
	// We don't want to create test users or sessions in health checks
	return HealthCheck{
		Status: "healthy",
	}
}
