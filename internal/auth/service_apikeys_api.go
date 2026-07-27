package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// apiKeyUsageTopKeysLimit caps the "most active" list returned by
// GetAPIKeysUsageStatsAPI. Named constant per house style (no magic
// numbers) -- kept small since the summary card is a glance-level widget.
const apiKeyUsageTopKeysLimit = 3

// apiKeyActivity pairs a key with its effective (non-stale) window count so
// the totals, the sort and the top-N cut all work off the same number
// instead of each re-deriving it from the raw column.
type apiKeyActivity struct {
	key    *UserAPIKey
	window int64
}

// sortAPIKeysByActivity sorts in place by effective window count desc, with
// request_count_total desc as the tiebreaker. Extracted as a helper so
// it can be unit-tested without going through the full service.
func sortAPIKeysByActivity(activity []apiKeyActivity) {
	sort.SliceStable(activity, func(i, j int) bool {
		if activity[i].window != activity[j].window {
			return activity[i].window > activity[j].window
		}
		return activity[i].key.RequestCountTotal > activity[j].key.RequestCountTotal
	})
}

// effectiveWindowUsage returns the window counter and window start as they
// should be reported to API consumers.
//
// The stored counter is only rewritten when the key's NEXT request arrives
// (see PostgresStore.RecordAPIKeyUsage), so a key that stops being used
// keeps its final window count on the row indefinitely. Reporting that
// column verbatim would claim a key idle for months served N requests in
// the current window. An expired window has no current activity, so it
// reads as zero with no window start rather than a stale non-zero count.
func effectiveWindowUsage(key *UserAPIKey, now time.Time) (int64, *time.Time) {
	if key.RequestCountWindowStart == nil ||
		!now.Before(key.RequestCountWindowStart.Add(apiKeyUsageWindow)) {
		return 0, nil
	}
	return key.RequestCountWindow, key.RequestCountWindowStart
}

// effectiveLifetimeUsage returns the lifetime request count to report, or nil
// when the true count is not known.
//
// Migration 000094 added request_count_total with DEFAULT 0, so every key that
// already existed reads as zero no matter how much traffic it actually served.
// RecordAPIKeyUsage is the only writer of last_used_at on the request path and
// it always bumps the counter in the same statement, so "has been used at
// least once, yet carries a zero lifetime count" can only mean the usage
// predates the counter. Reporting that as 0 would assert a request volume we
// do not have; nil says "unknown" and lets the UI show it as such.
//
// This relies on last_used_at and request_count_total moving together. Any new
// caller of the legacy UpdateAPIKeyLastUsed (which bumps only the timestamp)
// would break the invariant and make a genuinely-unused key look unknown.
func effectiveLifetimeUsage(key *UserAPIKey) *int64 {
	if key.RequestCountTotal == 0 && key.LastUsedAt != nil {
		return nil
	}
	total := key.RequestCountTotal
	return &total
}

// API wrapper methods for API key operations
// These methods return API-friendly types and handle type conversions

// APICreateAPIKeyRequest represents the API request to create an API key.
type APICreateAPIKeyRequest struct {
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions,omitempty"`
}

// APIKeyInfo represents public API key information (without sensitive data).
type APIKeyInfo struct {
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// RequestCountWindowStart is when the current request_count_window
	// began; nil means there is no current window, either because the key
	// has never recorded a request or because its last window has closed.
	// Exposed so consumers know exactly which period RequestCountWindow
	// covers instead of assuming a true trailing "last 24h".
	RequestCountWindowStart *time.Time   `json:"request_count_window_start,omitempty"`
	ID                      string       `json:"id"`
	Name                    string       `json:"name"`
	KeyPrefix               string       `json:"key_prefix"`
	Permissions             []Permission `json:"permissions,omitempty"`
	IsActive                bool         `json:"is_active"`
	// Usage counters (issue #340/#344 deferred sub-task). RequestCountWindow
	// is a fixed/tumbling window count, not a true rolling 24h total, and is
	// zero once that window has closed -- see effectiveWindowUsage,
	// PostgresStore.RecordAPIKeyUsage and RequestCountWindowStart above.
	//
	// RequestCountTotal is a pointer because "unknown" and "zero" are
	// different answers: a key that was already in use before migration 000094
	// added the counter has no recoverable lifetime figure, and reporting 0
	// for it would be a fabricated number. nil means unknown; see
	// effectiveLifetimeUsage.
	RequestCountTotal  *int64 `json:"request_count_total"`
	RequestCountWindow int64  `json:"request_count_window"`
}

// APIKeysUsageStatsTopKey is one entry in the top-keys list returned by
// GetAPIKeysUsageStatsAPI. Trimmed to identifier + window count so the UI
// doesn't have to re-derive anything from the full APIKeyInfo blob.
type APIKeysUsageStatsTopKey struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	KeyPrefix          string `json:"key_prefix"`
	RequestCountWindow int64  `json:"request_count_window"`
}

// APIKeysUsageStatsResponse is the summary payload for the API keys
// section header (issue #340/#344 deferred sub-task). Scoped to the
// calling user's own keys -- same scope as ListUserAPIKeysAPI.
//
// TotalRequestsWindow sums each key's fixed-window counter (see
// PostgresStore.RecordAPIKeyUsage) across the keys whose window is still
// open; it is NOT a true trailing-24h total, and the windows it sums are
// not aligned with one another.
//
// TotalActive counts the keys the authentication path would accept, so a
// key that is unrevoked but past its expiry is not counted.
//
// TotalRequestsLifetime sums only the keys whose lifetime count is known.
// LifetimePartial reports whether any key was left out because its usage
// predates migration 000094 (see effectiveLifetimeUsage), so the UI can show
// the total as a lower bound instead of presenting an undercount as exact.
type APIKeysUsageStatsResponse struct {
	TotalActive           int                       `json:"total_active"`
	TotalRequestsWindow   int64                     `json:"total_requests_window"`
	TotalRequestsLifetime int64                     `json:"total_requests_lifetime"`
	LifetimePartial       bool                      `json:"lifetime_partial"`
	TopKeys               []APIKeysUsageStatsTopKey `json:"top_keys"`
}

// APICreateAPIKeyResponse represents the API response for creating an API key.
type APICreateAPIKeyResponse struct {
	Info   *APIKeyInfo `json:"info"`
	APIKey string      `json:"api_key"` //nolint:gosec // G117: intentional one-time raw API-key response -- the key is returned to its owner exactly once on creation and is never re-stored or re-serialized
	KeyID  string      `json:"key_id"`
}

// APIListAPIKeysResponse represents the API response for listing API keys.
type APIListAPIKeysResponse struct {
	APIKeys []*APIKeyInfo `json:"api_keys"`
}

// CreateAPIKeyAPI creates a new API key and returns API-friendly response.
//
// req may be any struct whose JSON representation is compatible with
// APICreateAPIKeyRequest (name, expires_at, permissions). The handler lives
// in a different package (api) and passes api.CreateAPIKeyRequest, which
// carries the same JSON fields but is a distinct Go type. A direct type
// assertion would always fail and return a 500; JSON re-encoding handles any
// caller-package type transparently (issue #1440).
func (s *Service) CreateAPIKeyAPI(ctx context.Context, userID string, req any) (any, error) {
	var createReq APICreateAPIKeyRequest
	switch r := req.(type) {
	case APICreateAPIKeyRequest:
		// Fast path: caller already supplies our concrete type (service tests,
		// future callers that import this package directly).
		createReq = r
	default:
		// Cross-package path: re-encode to JSON and back so any struct with
		// compatible json tags (e.g. api.CreateAPIKeyRequest) is accepted.
		b, err := json.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("invalid request: %w", err)
		}
		if err := json.Unmarshal(b, &createReq); err != nil {
			return nil, fmt.Errorf("invalid request: %w", err)
		}
	}

	// Create the API key
	apiKey, keyInfo, err := s.CreateAPIKey(ctx, userID, createReq.Name, createReq.Permissions, createReq.ExpiresAt)
	if err != nil {
		return nil, err
	}

	// Convert to API response
	return &APICreateAPIKeyResponse{
		APIKey: apiKey,
		KeyID:  keyInfo.ID,
		Info: &APIKeyInfo{
			ID:                      keyInfo.ID,
			Name:                    keyInfo.Name,
			KeyPrefix:               keyInfo.KeyPrefix,
			Permissions:             keyInfo.Permissions,
			ExpiresAt:               keyInfo.ExpiresAt,
			CreatedAt:               keyInfo.CreatedAt,
			LastUsedAt:              keyInfo.LastUsedAt,
			IsActive:                keyInfo.IsActive,
			RequestCountTotal:       effectiveLifetimeUsage(keyInfo),
			RequestCountWindow:      keyInfo.RequestCountWindow,
			RequestCountWindowStart: keyInfo.RequestCountWindowStart,
		},
	}, nil
}

// ListUserAPIKeysAPI lists all API keys for a user and returns API-friendly response.
func (s *Service) ListUserAPIKeysAPI(ctx context.Context, userID string) (any, error) {
	keys, err := s.ListUserAPIKeys(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Convert to API response. The window counter goes through
	// effectiveWindowUsage so a key whose window has expired reports zero
	// rather than the stale count still sitting on its row.
	now := time.Now()
	apiKeys := make([]*APIKeyInfo, 0, len(keys))
	for _, key := range keys {
		windowCount, windowStart := effectiveWindowUsage(key, now)
		apiKeys = append(apiKeys, &APIKeyInfo{
			ID:                      key.ID,
			Name:                    key.Name,
			KeyPrefix:               key.KeyPrefix,
			Permissions:             key.Permissions,
			ExpiresAt:               key.ExpiresAt,
			CreatedAt:               key.CreatedAt,
			LastUsedAt:              key.LastUsedAt,
			IsActive:                key.IsActive,
			RequestCountTotal:       effectiveLifetimeUsage(key),
			RequestCountWindow:      windowCount,
			RequestCountWindowStart: windowStart,
		})
	}

	return &APIListAPIKeysResponse{
		APIKeys: apiKeys,
	}, nil
}

// GetAPIKeysUsageStatsAPI computes section-level usage stats for the
// calling user's own API keys. Aggregated in-process so we don't need a
// separate DB round-trip -- ListUserAPIKeys already returns the per-key
// counters from migration 000094.
//
// The "top keys" list is sorted by effective window count descending, with
// total-lifetime as the tiebreaker so a long-running idle key doesn't
// outrank an active one with the same window count. Up to
// apiKeyUsageTopKeysLimit entries are surfaced; keys with zero window
// activity are omitted rather than shown as an uninformative "top key".
//
// Window counts run through effectiveWindowUsage first: the raw column
// keeps its last value until the key is used again, so summing it verbatim
// would report requests from windows that closed arbitrarily long ago as
// current activity.
//
// TotalActive uses validateAPIKeyStatus, the same predicate the
// authentication path applies, so a key that is unrevoked but expired is
// not counted as active here while the keys table below the summary card
// shows it as "Expired".
func (s *Service) GetAPIKeysUsageStatsAPI(ctx context.Context, userID string) (any, error) {
	keys, err := s.ListUserAPIKeys(ctx, userID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	resp := &APIKeysUsageStatsResponse{
		TopKeys: []APIKeysUsageStatsTopKey{},
	}
	activity := make([]apiKeyActivity, 0, len(keys))
	for _, k := range keys {
		windowCount, _ := effectiveWindowUsage(k, now)
		if validateAPIKeyStatus(k) == nil {
			resp.TotalActive++
		}
		resp.TotalRequestsWindow += windowCount
		if lifetime := effectiveLifetimeUsage(k); lifetime != nil {
			resp.TotalRequestsLifetime += *lifetime
		} else {
			// Pre-000094 key: its real lifetime count is unrecoverable, so
			// the total is a lower bound rather than an exact figure.
			resp.LifetimePartial = true
		}
		activity = append(activity, apiKeyActivity{key: k, window: windowCount})
	}

	sortAPIKeysByActivity(activity)

	limit := min(apiKeyUsageTopKeysLimit, len(activity))
	for i := range limit {
		a := activity[i]
		if a.window == 0 {
			break
		}
		resp.TopKeys = append(resp.TopKeys, APIKeysUsageStatsTopKey{
			ID:                 a.key.ID,
			Name:               a.key.Name,
			KeyPrefix:          a.key.KeyPrefix,
			RequestCountWindow: a.window,
		})
	}

	return resp, nil
}

// DeleteAPIKeyAPI deletes an API key.
func (s *Service) DeleteAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	return s.DeleteAPIKey(ctx, userID, keyID)
}

// RevokeAPIKeyAPI revokes an API key.
func (s *Service) RevokeAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	return s.RevokeAPIKey(ctx, userID, keyID)
}

// ValidateUserAPIKeyAPI validates a user API key and returns the key info and user
// This is the API-facing wrapper for ValidateUserAPIKey.
func (s *Service) ValidateUserAPIKeyAPI(ctx context.Context, apiKey string) (*UserAPIKey, *User, error) {
	return s.ValidateUserAPIKey(ctx, apiKey)
}

// HasAPIKeyPermissionAPI validates a user API key and checks the requested
// action/resource against the key's effective permissions: the intersection
// of the key's scoped permissions with the owning user's group-derived
// permissions (ComputeEffectivePermissions). A key created without explicit
// permissions inherits the owner's full permission set. Returns the owning
// user's ID, the key's database ID, and whether the permission is held.
// The key ID is threaded to callers so they can pass it to
// HasAPIKeyPermissionForConstraintsAPI without a redundant DB lookup.
//
// Fail closed: any validation failure (unknown, revoked, or expired key,
// inactive owner) or permission-lookup error returns a non-nil error and
// callers must deny access.
func (s *Service) HasAPIKeyPermissionAPI(ctx context.Context, apiKey, action, resource string) (userID, keyID string, allowed bool, err error) {
	key, user, err := s.ValidateUserAPIKey(ctx, apiKey)
	if err != nil {
		return "", "", false, err
	}

	perms, err := s.ComputeEffectivePermissions(ctx, key, user)
	if err != nil {
		return "", "", false, fmt.Errorf("computing effective API key permissions: %w", err)
	}

	// AuthContext.HasPermission applies the same matching semantics as
	// session-based checks, including the admin:* wildcard with its
	// money-spending carve-outs (issue #923).
	effective := &AuthContext{User: user, Permissions: perms}
	return user.ID, key.ID, effective.HasPermission(action, resource), nil
}

// HasAPIKeyPermissionForConstraintsAPI checks request-derived permission
// constraint sets against a user API key's effective permissions (the
// intersection of the key's own permissions with the owning user's
// group-derived permissions). Callers must have already confirmed the key
// grants action/resource via HasAPIKeyPermissionAPI; this enforces the
// Constraints dimension (MaxPurchaseAmount, Providers, Services, Regions,
// AccountIDs) at execution time for user-API-key sessions (adversarial-review
// F2, issue #1141 extension).
//
// Fail closed: an empty constraintSets slice is a caller bug; any DB lookup
// failure returns an error and callers must deny.
func (s *Service) HasAPIKeyPermissionForConstraintsAPI(ctx context.Context, keyID, userID, action, resource string, constraintSets []PermissionConstraints) (bool, error) {
	if len(constraintSets) == 0 {
		return false, fmt.Errorf("no permission constraint sets provided for %s on %s", action, resource)
	}
	key, err := s.store.GetAPIKeyByID(ctx, keyID)
	if err != nil {
		return false, fmt.Errorf("failed to load API key for constraint check: %w", err)
	}
	if key == nil {
		return false, fmt.Errorf("API key not found")
	}
	// lookupAPIKeyUser also verifies that the user is still active.
	user, err := s.lookupAPIKeyUser(ctx, userID)
	if err != nil {
		return false, err
	}
	// Fetch the owner's auth context once; it is used for two purposes:
	//   1. Computing the key's effective permissions (key ∩ owner at action/resource level)
	//   2. Independently enforcing the owner's group constraint limits.
	// This prevents a key whose MaxPurchaseAmount (or other constraint) exceeds the owner's
	// group limit from authorizing more than the owner's group allows (CR finding).
	ownerAuthCtx, err := s.GetAuthContext(ctx, user.ID)
	if err != nil {
		return false, fmt.Errorf("failed to get owner auth context for constraint check: %w", err)
	}
	perms := computeEffectivePermissionsFromAuthCtx(key, ownerAuthCtx)
	// Each constraint set must independently pass both gates:
	//   - The key's effective permissions (key's constraint limits, e.g. MaxPurchaseAmount).
	//   - The owner's group permissions (owner's constraint limits).
	for i := range constraintSets {
		if !s.permissionsAllow(perms, action, resource, &constraintSets[i]) {
			return false, nil
		}
		if !s.permissionsAllow(ownerAuthCtx.Permissions, action, resource, &constraintSets[i]) {
			return false, nil
		}
	}
	return true, nil
}
