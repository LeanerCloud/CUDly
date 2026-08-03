package api

import (
	"context"
	"fmt"

	"github.com/aws/aws-lambda-go/events"
)

// API Key usage-stats handler.
//
// Split out of handler_apikeys.go / router.go (CodeRabbit finding on PR
// #1523) so the new per-API-key usage-stats surface has its own bounded-
// context file rather than growing the already-oversized router.go and
// handler_apikeys.go further.

// listAPIKeysUsageStats handles GET /api/api-keys/usage-stats.
//
// Returns a section-level summary scoped to the calling user's own keys:
// total active, total requests (fixed-window count + lifetime), and the
// top-3 keys by window activity. Scope is identical to listAPIKeys -- both
// gate on the same view/api-keys permission so the section header and the
// row list can never disagree about visibility.
func (h *Handler) listAPIKeysUsageStats(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "api-keys")
	if err != nil {
		return nil, err
	}

	stats, err := h.auth.GetAPIKeysUsageStatsAPI(ctx, session.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to load API key usage stats: %w", err)
	}

	return stats, nil
}

// listAPIKeysUsageStatsHandler is the router-level wrapper for
// listAPIKeysUsageStats, registered against GET /api/api-keys/usage-stats.
func (r *Router) listAPIKeysUsageStatsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.listAPIKeysUsageStats(ctx, req)
}
