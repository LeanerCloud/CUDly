package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

// RouteHandler is a function that handles a matched route
type RouteHandler func(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error)

// AuthLevel controls how Router.Route() enforces authentication.
// The zero value is AuthAdmin — secure by default.
// Any Route without an explicit Auth field requires admin access.
type AuthLevel int

const (
	// AuthAdmin requires admin role (API key or admin bearer token).
	// Zero value — any Route without an explicit Auth field gets this.
	AuthAdmin AuthLevel = iota
	// AuthUser requires any authenticated user. Use for self-service
	// endpoints (logout, profile, API key management).
	AuthUser
	// AuthPublic requires no authentication. Must also be listed in
	// isPublicEndpoint() for middleware bypass.
	AuthPublic
)

// Route defines a routing rule
type Route struct {
	// Pattern matching fields
	ExactPath  string // Exact path match (e.g., "/api/health")
	PathPrefix string // Path must start with this (e.g., "/api/users/")
	PathSuffix string // Path must end with this (e.g., "/revoke")
	Method     string // HTTP method (e.g., "GET", "POST")

	// Handler function
	Handler RouteHandler

	// Auth controls authentication level. Defaults to AuthAdmin (0) — secure by default.
	// Explicitly set to AuthUser or AuthPublic to relax the requirement.
	Auth AuthLevel
}

// Router manages request routing
type Router struct {
	routes []Route
	h      *Handler
}

// NewRouter creates a new router with all routes configured
func NewRouter(h *Handler) *Router {
	r := &Router{h: h}
	r.registerRoutes()
	return r
}

// registerRoutes sets up all application routes
func (r *Router) registerRoutes() {
	r.routes = []Route{
		// Dashboard endpoints
		{ExactPath: "/api/dashboard/summary", Method: "GET", Handler: r.dashboardSummaryHandler},
		{ExactPath: "/api/dashboard/upcoming", Method: "GET", Handler: r.upcomingPurchasesHandler},

		// Configuration endpoints
		{ExactPath: "/api/config", Method: "GET", Handler: r.getConfigHandler},
		{ExactPath: "/api/config", Method: "PUT", Handler: r.updateConfigHandler},
		{PathPrefix: "/api/config/service/", Method: "GET", Handler: r.getServiceConfigHandler},
		{PathPrefix: "/api/config/service/", Method: "PUT", Handler: r.updateServiceConfigHandler},

		// Credentials endpoints
		{ExactPath: "/api/credentials/azure", Method: "POST", Handler: r.saveAzureCredentialsHandler},
		{ExactPath: "/api/credentials/gcp", Method: "POST", Handler: r.saveGCPCredentialsHandler},

		// Recommendations endpoints
		{ExactPath: "/api/recommendations", Method: "GET", Handler: r.getRecommendationsHandler},
		{ExactPath: "/api/recommendations/refresh", Method: "POST", Handler: r.refreshRecommendationsHandler},

		// Purchase plans endpoints
		{ExactPath: "/api/plans", Method: "GET", Handler: r.listPlansHandler},
		{ExactPath: "/api/plans", Method: "POST", Handler: r.createPlanHandler},
		// Suffix routes must precede generic prefix routes so they are matched first.
		{PathPrefix: "/api/plans/", PathSuffix: "/purchases", Method: "POST", Handler: r.createPlannedPurchasesHandler},
		{PathPrefix: "/api/plans/", PathSuffix: "/accounts", Method: "GET", Handler: r.listPlanAccountsHandler},
		{PathPrefix: "/api/plans/", PathSuffix: "/accounts", Method: "PUT", Handler: r.setPlanAccountsHandler},
		{PathPrefix: "/api/plans/", Method: "GET", Handler: r.getPlanHandler},
		{PathPrefix: "/api/plans/", Method: "PUT", Handler: r.updatePlanHandler},
		{PathPrefix: "/api/plans/", Method: "PATCH", Handler: r.patchPlanHandler},
		{PathPrefix: "/api/plans/", Method: "DELETE", Handler: r.deletePlanHandler},

		// Purchase actions
		{ExactPath: "/api/purchases/execute", Method: "POST", Handler: r.executePurchaseHandler},
		{PathPrefix: "/api/purchases/approve/", Method: "POST", Handler: r.approvePurchaseHandler, Auth: AuthPublic},
		{PathPrefix: "/api/purchases/cancel/", Method: "POST", Handler: r.cancelPurchaseHandler, Auth: AuthPublic},

		// Planned purchases endpoints (must come before generic /api/purchases/{id})
		{ExactPath: "/api/purchases/planned", Method: "GET", Handler: r.getPlannedPurchasesHandler},
		{PathPrefix: "/api/purchases/planned/", PathSuffix: "/pause", Method: "POST", Handler: r.pausePlannedPurchaseHandler},
		{PathPrefix: "/api/purchases/planned/", PathSuffix: "/resume", Method: "POST", Handler: r.resumePlannedPurchaseHandler},
		{PathPrefix: "/api/purchases/planned/", PathSuffix: "/run", Method: "POST", Handler: r.runPlannedPurchaseHandler},
		{PathPrefix: "/api/purchases/planned/", Method: "DELETE", Handler: r.deletePlannedPurchaseHandler},

		// Generic purchase details (must come after more specific routes)
		{PathPrefix: "/api/purchases/", Method: "GET", Handler: r.getPurchaseDetailsHandler},

		// History endpoints
		{ExactPath: "/api/history", Method: "GET", Handler: r.getHistoryHandler},
		{ExactPath: "/api/history/analytics", Method: "GET", Handler: r.getHistoryAnalyticsHandler},
		{ExactPath: "/api/history/breakdown", Method: "GET", Handler: r.getHistoryBreakdownHandler},

		// Analytics collection endpoint
		{ExactPath: "/api/analytics/collect", Method: "POST", Handler: r.triggerAnalyticsCollectionHandler},

		// Auth endpoints
		{ExactPath: "/api/auth/login", Method: "POST", Handler: r.loginHandler, Auth: AuthPublic},
		{ExactPath: "/api/auth/logout", Method: "POST", Handler: r.logoutHandler, Auth: AuthUser},
		{ExactPath: "/api/auth/me", Method: "GET", Handler: r.getCurrentUserHandler, Auth: AuthUser},
		{ExactPath: "/api/auth/check-admin", Method: "GET", Handler: r.checkAdminExistsHandler, Auth: AuthPublic},
		{ExactPath: "/api/auth/setup-admin", Method: "POST", Handler: r.setupAdminHandler, Auth: AuthPublic},
		{ExactPath: "/api/auth/forgot-password", Method: "POST", Handler: r.forgotPasswordHandler, Auth: AuthPublic},
		{ExactPath: "/api/auth/reset-password", Method: "POST", Handler: r.resetPasswordHandler, Auth: AuthPublic},
		{ExactPath: "/api/auth/profile", Method: "PUT", Handler: r.updateProfileHandler, Auth: AuthUser},
		{ExactPath: "/api/auth/change-password", Method: "POST", Handler: r.changePasswordHandler, Auth: AuthUser},

		// API Key endpoints (self-service — any authenticated user)
		{ExactPath: "/api/api-keys", Method: "GET", Handler: r.listAPIKeysHandler, Auth: AuthUser},
		{ExactPath: "/api/api-keys", Method: "POST", Handler: r.createAPIKeyHandler, Auth: AuthUser},
		{PathPrefix: "/api/api-keys/", PathSuffix: "/revoke", Method: "POST", Handler: r.revokeAPIKeyHandler, Auth: AuthUser},
		{PathPrefix: "/api/api-keys/", Method: "DELETE", Handler: r.deleteAPIKeyHandler, Auth: AuthUser},

		// User management endpoints
		{ExactPath: "/api/users", Method: "GET", Handler: r.listUsersHandler},
		{ExactPath: "/api/users", Method: "POST", Handler: r.createUserHandler},
		{PathPrefix: "/api/users/", Method: "GET", Handler: r.getUserHandler},
		{PathPrefix: "/api/users/", Method: "PUT", Handler: r.updateUserHandler},
		{PathPrefix: "/api/users/", Method: "DELETE", Handler: r.deleteUserHandler},

		// Cloud Account endpoints (more-specific suffix routes must precede generic prefix routes)
		{ExactPath: "/api/accounts/discover-org", Method: "POST", Handler: r.discoverOrgAccountsHandler},
		{ExactPath: "/api/accounts", Method: "GET", Handler: r.listAccountsHandler},
		{ExactPath: "/api/accounts", Method: "POST", Handler: r.createAccountHandler},
		{PathPrefix: "/api/accounts/", PathSuffix: "/credentials", Method: "POST", Handler: r.saveAccountCredentialsHandler},
		{PathPrefix: "/api/accounts/", PathSuffix: "/test", Method: "POST", Handler: r.testAccountCredentialsHandler},
		{PathPrefix: "/api/accounts/", PathSuffix: "/service-overrides", Method: "GET", Handler: r.listAccountServiceOverridesHandler},
		{PathPrefix: "/api/accounts/", Method: "PUT", Handler: r.updateAccountOrServiceOverrideHandler},
		{PathPrefix: "/api/accounts/", Method: "DELETE", Handler: r.deleteAccountOrServiceOverrideHandler},
		{PathPrefix: "/api/accounts/", Method: "GET", Handler: r.getAccountHandler},

		// Group management endpoints
		{ExactPath: "/api/groups", Method: "GET", Handler: r.listGroupsHandler},
		{ExactPath: "/api/groups", Method: "POST", Handler: r.createGroupHandler},
		{PathPrefix: "/api/groups/", Method: "GET", Handler: r.getGroupHandler},
		{PathPrefix: "/api/groups/", Method: "PUT", Handler: r.updateGroupHandler},
		{PathPrefix: "/api/groups/", Method: "DELETE", Handler: r.deleteGroupHandler},

		// RI Exchange endpoints
		{ExactPath: "/api/ri-exchange/instances", Method: "GET", Handler: r.listConvertibleRIsHandler},
		{ExactPath: "/api/ri-exchange/utilization", Method: "GET", Handler: r.getRIUtilizationHandler},
		{ExactPath: "/api/ri-exchange/reshape-recommendations", Method: "GET", Handler: r.getReshapeRecommendationsHandler},
		{ExactPath: "/api/ri-exchange/quote", Method: "POST", Handler: r.getExchangeQuoteHandler},
		{ExactPath: "/api/ri-exchange/execute", Method: "POST", Handler: r.executeExchangeHandler},
		{ExactPath: "/api/ri-exchange/config", Method: "GET", Handler: r.getRIExchangeConfigHandler},
		{ExactPath: "/api/ri-exchange/config", Method: "PUT", Handler: r.updateRIExchangeConfigHandler},
		{ExactPath: "/api/ri-exchange/history", Method: "GET", Handler: r.getRIExchangeHistoryHandler},
		{PathPrefix: "/api/ri-exchange/approve/", Method: "POST", Handler: r.approveRIExchangeHandler, Auth: AuthPublic},
		{PathPrefix: "/api/ri-exchange/reject/", Method: "POST", Handler: r.rejectRIExchangeHandler, Auth: AuthPublic},

		// Account self-registration (public, called by Terraform during federation IaC apply)
		{ExactPath: "/api/register", Method: "POST", Handler: r.submitRegistrationHandler, Auth: AuthPublic},
		{PathPrefix: "/api/register/", Method: "GET", Handler: r.getRegistrationStatusHandler, Auth: AuthPublic},

		// Admin registration management (suffix routes before generic prefix)
		{ExactPath: "/api/registrations", Method: "GET", Handler: r.listRegistrationsHandler},
		{PathPrefix: "/api/registrations/", PathSuffix: "/approve", Method: "POST", Handler: r.approveRegistrationHandler},
		{PathPrefix: "/api/registrations/", PathSuffix: "/reject", Method: "POST", Handler: r.rejectRegistrationHandler},
		{PathPrefix: "/api/registrations/", Method: "DELETE", Handler: r.deleteRegistrationHandler},
		{PathPrefix: "/api/registrations/", Method: "GET", Handler: r.getRegistrationHandler},

		// Federation IaC download endpoint (public — generic templates, no secrets)
		{ExactPath: "/api/federation/iac", Method: "GET", Handler: r.getFederationIaCHandler, Auth: AuthPublic},

		// Note: /.well-known/openid-configuration and /.well-known/jwks.json
		// are served by the transport layer (lambda.go / http.go) which
		// calls api.Handler.HandleOIDC before dispatching to this router.
		// That keeps the OIDC endpoints out of the API surface entirely —
		// no auth middleware, no CSRF, no CORS layer — and avoids special
		// cases in isPublicEndpoint / isStaticPath.

		// Health check (both root and /api paths)
		{ExactPath: "/health", Handler: r.healthCheckHandler, Auth: AuthPublic},
		{ExactPath: "/api/health", Handler: r.healthCheckHandler, Auth: AuthPublic},

		// Public info endpoint
		{ExactPath: "/api/info", Method: "GET", Handler: r.getPublicInfoHandler, Auth: AuthPublic},

		// API documentation (Swagger UI + raw spec)
		{PathPrefix: "/api/docs", Method: "GET", Handler: r.docsHandler, Auth: AuthPublic},
		{PathPrefix: "/docs", Method: "GET", Handler: r.docsHandler, Auth: AuthPublic},
	}
}

// Route finds and executes the matching route handler.
// Routes with Auth == AuthAdmin (the default) require admin access before the handler is called.
func (r *Router) Route(ctx context.Context, method, path string, req *events.LambdaFunctionURLRequest) (any, error) {
	for _, route := range r.routes {
		if r.matches(route, method, path) {
			if route.Auth == AuthAdmin {
				if _, err := r.h.requireAdmin(ctx, req); err != nil {
					return nil, err
				}
			}
			params := r.extractParams(route, path)
			return route.Handler(ctx, req, params)
		}
	}
	return nil, errNotFound
}

// matches checks if a route matches the given method and path
func (r *Router) matches(route Route, method, path string) bool {
	// Check method (if specified)
	if route.Method != "" && route.Method != method {
		return false
	}

	// Check exact path match
	if route.ExactPath != "" {
		return route.ExactPath == path
	}

	// Check prefix and suffix
	if route.PathPrefix != "" && !strings.HasPrefix(path, route.PathPrefix) {
		return false
	}
	if route.PathSuffix != "" && !strings.HasSuffix(path, route.PathSuffix) {
		return false
	}

	// If we have a prefix or suffix, we matched
	return route.PathPrefix != "" || route.PathSuffix != ""
}

// extractParams extracts path parameters from the route
func (r *Router) extractParams(route Route, path string) map[string]string {
	params := make(map[string]string)

	// Extract ID from prefix-based routes
	if route.PathPrefix != "" {
		remaining := strings.TrimPrefix(path, route.PathPrefix)
		if route.PathSuffix != "" {
			remaining = strings.TrimSuffix(remaining, route.PathSuffix)
		}
		if remaining != "" {
			params["id"] = remaining
		}
	}

	return params
}

// Handler wrappers that adapt the old handlers to the new RouteHandler signature

func (r *Router) dashboardSummaryHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getDashboardSummary(ctx, req.QueryStringParameters)
}

func (r *Router) upcomingPurchasesHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getUpcomingPurchases(ctx)
}

func (r *Router) getConfigHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getConfig(ctx)
}

func (r *Router) updateConfigHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.updateConfig(ctx, req)
}

func (r *Router) getServiceConfigHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getServiceConfig(ctx, params["id"])
}

func (r *Router) updateServiceConfigHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.updateServiceConfig(ctx, req, params["id"])
}

func (r *Router) saveAzureCredentialsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.saveAzureCredentials(ctx, req)
}

func (r *Router) saveGCPCredentialsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.saveGCPCredentials(ctx, req)
}

func (r *Router) getRecommendationsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getRecommendations(ctx, req.QueryStringParameters)
}

func (r *Router) refreshRecommendationsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.scheduler.CollectRecommendations(ctx)
}

func (r *Router) listPlansHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.listPlans(ctx, req)
}

func (r *Router) createPlanHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.createPlan(ctx, req)
}

func (r *Router) getPlanHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getPlan(ctx, req, params["id"])
}

func (r *Router) updatePlanHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.updatePlan(ctx, req, params["id"])
}

func (r *Router) patchPlanHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.patchPlan(ctx, req, params["id"])
}

func (r *Router) deletePlanHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.deletePlan(ctx, req, params["id"])
}

func (r *Router) createPlannedPurchasesHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.createPlannedPurchases(ctx, req, params["id"])
}

func (r *Router) executePurchaseHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.executePurchase(ctx, req)
}

func (r *Router) getPurchaseDetailsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getPurchaseDetails(ctx, req, params["id"])
}

func (r *Router) approvePurchaseHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	token := req.QueryStringParameters["token"]
	return r.h.approvePurchase(ctx, params["id"], token)
}

func (r *Router) cancelPurchaseHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	token := req.QueryStringParameters["token"]
	return r.h.cancelPurchase(ctx, params["id"], token)
}

func (r *Router) getPlannedPurchasesHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getPlannedPurchases(ctx, req)
}

func (r *Router) pausePlannedPurchaseHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.pausePlannedPurchase(ctx, req, params["id"])
}

func (r *Router) resumePlannedPurchaseHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.resumePlannedPurchase(ctx, req, params["id"])
}

func (r *Router) runPlannedPurchaseHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.runPlannedPurchase(ctx, req, params["id"])
}

func (r *Router) deletePlannedPurchaseHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.deletePlannedPurchase(ctx, req, params["id"])
}

func (r *Router) getHistoryHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getHistory(ctx, req.QueryStringParameters)
}

func (r *Router) getHistoryAnalyticsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getHistoryAnalytics(ctx, req.QueryStringParameters)
}

func (r *Router) getHistoryBreakdownHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getHistoryBreakdown(ctx, req.QueryStringParameters)
}

func (r *Router) triggerAnalyticsCollectionHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.triggerAnalyticsCollection(ctx, req.QueryStringParameters)
}

func (r *Router) loginHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.login(ctx, req)
}

func (r *Router) logoutHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.logout(ctx, req)
}

func (r *Router) getCurrentUserHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getCurrentUser(ctx, req)
}

func (r *Router) checkAdminExistsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.checkAdminExists(ctx, req)
}

func (r *Router) setupAdminHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.setupAdmin(ctx, req)
}

func (r *Router) forgotPasswordHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.forgotPassword(ctx, req)
}

func (r *Router) resetPasswordHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.resetPassword(ctx, req)
}

func (r *Router) updateProfileHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.updateProfile(ctx, req)
}

func (r *Router) changePasswordHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.changePassword(ctx, req)
}

func (r *Router) listAPIKeysHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.listAPIKeys(ctx, req)
}

func (r *Router) createAPIKeyHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.createAPIKey(ctx, req)
}

func (r *Router) revokeAPIKeyHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.revokeAPIKey(ctx, req)
}

func (r *Router) deleteAPIKeyHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.deleteAPIKey(ctx, req)
}

func (r *Router) listUsersHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.listUsers(ctx, req)
}

func (r *Router) createUserHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.createUser(ctx, req)
}

func (r *Router) getUserHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getUser(ctx, req, params["id"])
}

func (r *Router) updateUserHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.updateUser(ctx, req, params["id"])
}

func (r *Router) deleteUserHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.deleteUser(ctx, req, params["id"])
}

func (r *Router) listGroupsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.listGroups(ctx, req)
}

func (r *Router) createGroupHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.createGroup(ctx, req)
}

func (r *Router) getGroupHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getGroup(ctx, req, params["id"])
}

func (r *Router) updateGroupHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.updateGroup(ctx, req, params["id"])
}

func (r *Router) deleteGroupHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.deleteGroup(ctx, req, params["id"])
}

func (r *Router) healthCheckHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.GetHealth(ctx)
}

func (r *Router) getPublicInfoHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getPublicInfo(ctx, req)
}

func (r *Router) docsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.docsHandler(ctx, req, params)
}

func (r *Router) listConvertibleRIsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.listConvertibleRIs(ctx, req)
}

func (r *Router) getRIUtilizationHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getRIUtilization(ctx, req)
}

func (r *Router) getReshapeRecommendationsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getReshapeRecommendations(ctx, req)
}

func (r *Router) getExchangeQuoteHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getExchangeQuote(ctx, req)
}

func (r *Router) executeExchangeHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.executeExchange(ctx, req)
}

func (r *Router) getRIExchangeConfigHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getRIExchangeConfig(ctx, req)
}

func (r *Router) updateRIExchangeConfigHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.updateRIExchangeConfig(ctx, req)
}

func (r *Router) getRIExchangeHistoryHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getRIExchangeHistory(ctx, req)
}

func (r *Router) approveRIExchangeHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.approveRIExchange(ctx, params["id"], req.QueryStringParameters["token"])
}

func (r *Router) rejectRIExchangeHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.rejectRIExchange(ctx, params["id"], req.QueryStringParameters["token"])
}

// formatNotFoundError creates a detailed not found error message
func formatNotFoundError(method, path string) error {
	return fmt.Errorf("%w: %s %s", errNotFound, method, path)
}

// Cloud Account route wrappers.

func (r *Router) listAccountsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	return r.h.listAccounts(ctx, req)
}

func (r *Router) createAccountHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	return r.h.createAccount(ctx, req)
}

func (r *Router) discoverOrgAccountsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	return r.h.discoverOrgAccounts(ctx, req)
}

func (r *Router) getAccountHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getAccount(ctx, req, params["id"])
}

// updateAccountOrServiceOverrideHandler handles PUT /api/accounts/:id and
// PUT /api/accounts/:id/service-overrides/:provider/:service.
// The router puts everything after /api/accounts/ into params["id"].
func (r *Router) updateAccountOrServiceOverrideHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	if strings.Contains(params["id"], "/service-overrides/") {
		return r.h.saveAccountServiceOverride(ctx, req, params["id"])
	}
	return r.h.updateAccount(ctx, req, params["id"])
}

// deleteAccountOrServiceOverrideHandler handles DELETE /api/accounts/:id and
// DELETE /api/accounts/:id/service-overrides/:provider/:service.
func (r *Router) deleteAccountOrServiceOverrideHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	if strings.Contains(params["id"], "/service-overrides/") {
		return r.h.deleteAccountServiceOverride(ctx, req, params["id"])
	}
	return r.h.deleteAccount(ctx, req, params["id"])
}

func (r *Router) saveAccountCredentialsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.saveAccountCredentials(ctx, req, params["id"])
}

func (r *Router) testAccountCredentialsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.testAccountCredentials(ctx, req, params["id"])
}

func (r *Router) listAccountServiceOverridesHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.listAccountServiceOverrides(ctx, req, params["id"])
}

// Plan ↔ Account association wrappers.

func (r *Router) getFederationIaCHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	return r.h.getFederationIaC(ctx, req)
}

// Account self-registration wrappers.

func (r *Router) submitRegistrationHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	return r.h.submitRegistration(ctx, req)
}

func (r *Router) getRegistrationStatusHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getRegistrationStatus(ctx, params["id"])
}

func (r *Router) listRegistrationsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	return r.h.listRegistrations(ctx, req)
}

func (r *Router) getRegistrationHandler(ctx context.Context, _ *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.getRegistration(ctx, params["id"])
}

func (r *Router) approveRegistrationHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.approveRegistration(ctx, req, params["id"])
}

func (r *Router) rejectRegistrationHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.rejectRegistration(ctx, req, params["id"])
}

func (r *Router) deleteRegistrationHandler(ctx context.Context, _ *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.deleteRegistration(ctx, params["id"])
}

func (r *Router) listPlanAccountsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.listPlanAccounts(ctx, req, params["id"])
}

func (r *Router) setPlanAccountsHandler(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	return r.h.setPlanAccounts(ctx, req, params["id"])
}
