package api

import (
	"context"
	"sync"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/mocks"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/stretchr/testify/mock"
)

var _ credentials.CredentialStore = (*MockCredentialStore)(nil) // compile-time interface check

// MockConfigStore is the shared testify mock for config.StoreInterface.
// All Fn-override fields and default behaviors live in internal/mocks.
type MockConfigStore = mocks.MockConfigStore

// MockCredentialStore is a simple stub implementing credentials.CredentialStore.
// SaveCredential always returns nil; other methods are no-ops.
type MockCredentialStore struct{}

func (m *MockCredentialStore) SaveCredential(_ context.Context, _, _ string, _ []byte) error {
	return nil
}
func (m *MockCredentialStore) LoadRaw(_ context.Context, _, _ string) ([]byte, error) {
	return nil, nil
}
func (m *MockCredentialStore) DeleteCredential(_ context.Context, _, _ string) error {
	return nil
}
func (m *MockCredentialStore) HasCredential(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

func (m *MockCredentialStore) EncryptPayload(plaintext []byte) (string, error) {
	return string(plaintext), nil // no-op: return plaintext as "encrypted" for tests
}

func (m *MockCredentialStore) DecryptPayload(ciphertext string) ([]byte, error) {
	return []byte(ciphertext), nil // no-op: return ciphertext as "decrypted" for tests
}

// MockPurchaseManager is a mock implementation of purchase.Manager.
type MockPurchaseManager struct {
	mock.Mock
}

func (m *MockPurchaseManager) ApproveExecution(ctx context.Context, execID, token, actor string) error {
	args := m.Called(ctx, execID, token, actor)
	return args.Error(0)
}

func (m *MockPurchaseManager) ApproveAndExecute(ctx context.Context, execID, actor string, transitionedBy *string) error {
	args := m.Called(ctx, execID, actor, transitionedBy)
	return args.Error(0)
}

func (m *MockPurchaseManager) CancelExecution(ctx context.Context, execID, token, actor string) error {
	args := m.Called(ctx, execID, token, actor)
	return args.Error(0)
}

// MockScheduler is a mock implementation of scheduler.Scheduler.
type MockScheduler struct {
	mock.Mock
}

func (m *MockScheduler) CollectRecommendations(ctx context.Context, ownerToken string) (*scheduler.CollectResult, error) {
	args := m.Called(ctx, ownerToken)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*scheduler.CollectResult), args.Error(1)
}

func (m *MockScheduler) ListRecommendations(ctx context.Context, filter config.RecommendationFilter) ([]config.RecommendationRecord, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.RecommendationRecord), args.Error(1)
}

func (m *MockScheduler) GetRecommendationByID(ctx context.Context, id string) (*config.RecommendationRecord, []string, error) {
	args := m.Called(ctx, id)
	var rec *config.RecommendationRecord
	if args.Get(0) != nil {
		rec = args.Get(0).(*config.RecommendationRecord)
	}
	var hiddenBy []string
	if args.Get(1) != nil {
		hiddenBy = args.Get(1).([]string)
	}
	return rec, hiddenBy, args.Error(2)
}

// MockAuthService is a mock implementation of the auth service.
type MockAuthService struct {
	mock.Mock
	// usageBookingsMu guards usageBookings.
	usageBookingsMu sync.Mutex
	// usageBookings records every key ID passed to RecordAPIKeyUsageAsync,
	// in call order. See that method for why it does not use mock.Mock.
	usageBookings []string
}

// RecordAPIKeyUsageAsync records the booking instead of routing through
// mock.Called. The handler books usage on every API-key-authenticated
// request, so going through mock.Called would panic in each of the dozens of
// existing tests that drive such a request without registering an
// expectation. Recording into a slice keeps the calls observable --
// UsageBookings() is what the exactly-once tests assert on -- without that
// churn, and without the .Maybe() registration that would make the
// assertions vacuous.
func (m *MockAuthService) RecordAPIKeyUsageAsync(keyID string) {
	m.usageBookingsMu.Lock()
	defer m.usageBookingsMu.Unlock()
	m.usageBookings = append(m.usageBookings, keyID)
}

// UsageBookings returns the key IDs booked so far, in call order.
func (m *MockAuthService) UsageBookings() []string {
	m.usageBookingsMu.Lock()
	defer m.usageBookingsMu.Unlock()
	return append([]string(nil), m.usageBookings...)
}

func (m *MockAuthService) Login(ctx context.Context, req LoginRequest) (*LoginResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*LoginResponse), args.Error(1)
}

func (m *MockAuthService) Logout(ctx context.Context, token string) error {
	args := m.Called(ctx, token)
	return args.Error(0)
}

func (m *MockAuthService) ValidateSession(ctx context.Context, token string) (*Session, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Session), args.Error(1)
}

func (m *MockAuthService) ValidateCSRFToken(ctx context.Context, sessionToken, csrfToken string) error {
	args := m.Called(ctx, sessionToken, csrfToken)
	return args.Error(0)
}

func (m *MockAuthService) SetupAdmin(ctx context.Context, req SetupAdminRequest) (*LoginResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*LoginResponse), args.Error(1)
}

func (m *MockAuthService) CheckAdminExists(ctx context.Context) (bool, error) {
	args := m.Called(ctx)
	return args.Bool(0), args.Error(1)
}

func (m *MockAuthService) RequestPasswordReset(ctx context.Context, email string) error {
	args := m.Called(ctx, email)
	return args.Error(0)
}

func (m *MockAuthService) ConfirmPasswordReset(ctx context.Context, req PasswordResetConfirm) error {
	args := m.Called(ctx, req)
	return args.Error(0)
}

func (m *MockAuthService) ResetTokenStatus(ctx context.Context, token string) (string, string, error) {
	args := m.Called(ctx, token)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *MockAuthService) GetUser(ctx context.Context, userID string) (*User, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*User), args.Error(1)
}

func (m *MockAuthService) UpdateUserProfile(ctx context.Context, userID string, email string, currentPassword string, newPassword string) error {
	args := m.Called(ctx, userID, email, currentPassword, newPassword)
	return args.Error(0)
}

// User management mock methods.
func (m *MockAuthService) CreateUserAPI(ctx context.Context, req interface{}) (interface{}, error) {
	args := m.Called(ctx, req)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) UpdateUserAPI(ctx context.Context, actorUserID, userID string, req interface{}) (interface{}, error) {
	args := m.Called(ctx, actorUserID, userID, req)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) DeleteUser(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockAuthService) ListUsersAPI(ctx context.Context) (interface{}, error) {
	args := m.Called(ctx)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) ChangePasswordAPI(ctx context.Context, userID, currentPassword, newPassword string) error {
	args := m.Called(ctx, userID, currentPassword, newPassword)
	return args.Error(0)
}

// MFA lifecycle mock methods (issue #497).
func (m *MockAuthService) MFASetupAPI(ctx context.Context, userID, password string) (string, string, error) {
	args := m.Called(ctx, userID, password)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *MockAuthService) MFAEnableAPI(ctx context.Context, userID, code string) ([]string, error) {
	args := m.Called(ctx, userID, code)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockAuthService) MFADisableAPI(ctx context.Context, userID, password, codeOrRecovery string) error {
	args := m.Called(ctx, userID, password, codeOrRecovery)
	return args.Error(0)
}

func (m *MockAuthService) MFARegenerateRecoveryCodesAPI(ctx context.Context, userID, code string) ([]string, error) {
	args := m.Called(ctx, userID, code)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

// Group management mock methods.
func (m *MockAuthService) CreateGroupAPI(ctx context.Context, actorUserID string, req interface{}) (interface{}, error) {
	args := m.Called(ctx, actorUserID, req)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) UpdateGroupAPI(ctx context.Context, actorUserID, groupID string, req interface{}) (interface{}, error) {
	args := m.Called(ctx, actorUserID, groupID, req)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) DeleteGroup(ctx context.Context, groupID string) error {
	args := m.Called(ctx, groupID)
	return args.Error(0)
}

func (m *MockAuthService) GetGroupAPI(ctx context.Context, groupID string) (interface{}, error) {
	args := m.Called(ctx, groupID)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) ListGroupsAPI(ctx context.Context) (interface{}, error) {
	args := m.Called(ctx)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) HasPermissionAPI(ctx context.Context, userID, action, resource string) (bool, error) {
	args := m.Called(ctx, userID, action, resource)
	return permissionDecision(args, action, resource), args.Error(1)
}

// permissionDecision reads a mock return that is either a constant bool or a
// decision function (registered by grantPermissions, which answers through the
// real matcher). It is called AFTER m.Called so the invocation is always
// recorded first: short-circuiting ahead of m.Called is what made 20
// assertions vacuous in issue #1595, and this must not reintroduce it.
func permissionDecision(args mock.Arguments, action, resource string) bool {
	if decide, ok := args.Get(0).(func(action, resource string) bool); ok {
		return decide(action, resource)
	}
	return args.Bool(0)
}

func (m *MockAuthService) HasPermissionForConstraintsAPI(ctx context.Context, userID, action, resource string, constraintSets []auth.PermissionConstraints) (bool, error) {
	args := m.Called(ctx, userID, action, resource, constraintSets)
	// The decision-function path (registered by grantPermissionsScoped) reads
	// the constraint arguments, so it answers from auth's real matchers rather
	// than from a re-statement of their contract. Explicit
	// mock.On(...).Return(bool, err) expectations for constrained-permission
	// tests are untouched -- those already encode the intended outcome for
	// whatever constraintSets the test passes, via permissionDecision below.
	if decide, ok := args.Get(0).(constraintDecisionFunc); ok {
		if err := args.Error(1); err != nil {
			return false, err
		}
		return decide(action, resource, constraintSets)
	}
	// permissionDecision's func(action, resource) bool answers from the verb
	// alone. On THIS method that IS the #1762 defect: constraintSets is
	// discarded, so every request outside the permission's constraints reads
	// as allowed. Reject it here rather than letting a future registration
	// reintroduce the divergence silently. Note that this aborts the test
	// binary, so a wholesale revert of grantPermissionsScoped's wiring dies
	// here instead of reporting the per-dimension failures in
	// TestGrantPermissionsScoped_ConstraintDimensionsAreEnforced; the reach
	// this adds is over a one-off inline registration, which no test asserts.
	if _, blind := args.Get(0).(func(action, resource string) bool); blind {
		panic("MockAuthService.HasPermissionForConstraintsAPI: a func(action, resource) bool return " +
			"discards the constraint arguments (issue #1762); register a constraintDecisionFunc")
	}
	return permissionDecision(args, action, resource), args.Error(1)
}

// constraintDecisionFunc is the constraint-AWARE mock return registered by
// grantPermissionsScoped, for HasPermissionForConstraintsAPI only. A named
// type rather than a bare signature so the registration site says which of
// the two decision shapes it means, and so the blind-signature check above
// has something unambiguous to reject.
type constraintDecisionFunc func(action, resource string, constraintSets []auth.PermissionConstraints) (bool, error)

func (m *MockAuthService) GetUserPermissionsAPI(ctx context.Context, userID string) (any, error) {
	args := m.Called(ctx, userID)
	return args.Get(0), args.Error(1)
}

// allowConstraintChecks stubs the SEC-01 execution-time permission
// constraint check (HasPermissionForConstraintsAPI and the user-API-key
// variant HasAPIKeyPermissionForConstraintsAPI) to succeed for any request,
// modeling a granting permission with no Constraints configured. Tests that
// target constraint behavior register explicit expectations instead.
func (m *MockAuthService) allowConstraintChecks() {
	m.On("HasPermissionForConstraintsAPI", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(true, nil).Maybe()
	m.On("HasAPIKeyPermissionForConstraintsAPI", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(true, nil).Maybe()
}

// grantAdmin models an Administrators-group member: a principal holding
// exactly {admin, *}, with unrestricted account access (the seeded group
// carries allowed_accounts ARRAY['*'], surfaced as nil).
//
// It does NOT stub the authorization decision. Every question is answered by
// the REAL matcher, auth.AuthContext.HasPermission, so the mock models the
// principal's STATE and lets production logic work out the answer -- including
// the adminCarvedOuts money verbs, which admin:* does NOT grant.
//
// Before issue #1596 this returned a constant true for every (action,
// resource) triple. That modeled a principal production cannot have: an admin
// who may spend money. The practical effect was that adminCarvedOuts could
// have been deleted outright without a single test in this package failing,
// so the #923 separation-of-duties control had no handler-level coverage at
// all. See TestGrantAdmin_CarveOutIsEnforcedAtHandler.
//
// Register any test-specific mock.On(...) expectation for a method this
// grants (e.g. a HasPermissionAPI denial) BEFORE calling grantAdmin, or
// express it via grantPermissions([...]) instead: testify serves the first
// registered matching expectation, so one added after this call is silently
// shadowed by the generic one grantAdmin registers and never actually runs.
func (m *MockAuthService) grantAdmin() {
	m.grantPermissions([]auth.Permission{{Action: auth.ActionAdmin, Resource: auth.ResourceAll}})
}

// grantAdminPurchaser models the principal a purchase actually requires: an
// Administrators member who is ALSO in the Purchaser group.
//
// admin:* alone cannot execute, approve-any or retry-any a purchase -- those
// three verbs are carved out of the wildcard for separation of duties (#923),
// so they require explicit membership in a group that grants them. Migrations
// 000059/000064 backfill exactly this pairing onto every existing admin, so
// this is the DEFAULT real-world operator on the money paths, not an exotic
// one.
//
// Tests on execute/approve/retry handlers must use this rather than
// grantAdmin. Before #1596 they used grantAdmin and passed anyway, because the
// stub answered true for verbs production denies.
func (m *MockAuthService) grantAdminPurchaser() {
	m.grantPermissions([]auth.Permission{
		{Action: auth.ActionAdmin, Resource: auth.ResourceAll},
		{Action: auth.ActionExecute, Resource: auth.ResourcePurchases},
		{Action: auth.ActionApproveAny, Resource: auth.ResourcePurchases},
		{Action: auth.ActionRetryAny, Resource: auth.ResourcePurchases},
	})
}

// grantScoped models an admin whose account access is RESTRICTED to the given
// accounts, so handlers that claim to honor allowed_accounts can be exercised
// on the restricted path. grantAdmin's nil allow-list means unrestricted, so
// without this no test could ever exercise account scoping (issue #1596; the
// #950/#956 regression class this made untestable).
func (m *MockAuthService) grantScoped(accounts ...string) {
	m.grantPermissionsScoped(
		[]auth.Permission{{Action: auth.ActionAdmin, Resource: auth.ResourceAll}},
		accounts,
	)
}

// grantPermissions models a principal holding exactly perms, with
// unrestricted account access.
func (m *MockAuthService) grantPermissions(perms []auth.Permission) {
	m.grantPermissionsScoped(perms, nil)
}

// grantPermissionsScoped is the shared implementation. Both halves of the
// authorization decision are answered by the real auth code rather than by a
// constant: the verb check by auth.AuthContext.HasPermission, the constraint
// check by auth.PermissionsAllowForConstraintSets. A handler asking for a verb
// this principal does not hold, or for a request outside the Constraints
// configured on the permission that grants it, is DENIED exactly as it would
// be in production.
//
// .Maybe() is retained: many handlers legitimately return before reaching a
// permission check (bad body, bad UUID). Asserting the check happened is a
// per-handler contract that grantAdmin cannot state for all 235 call sites;
// tests that need it should assert the call explicitly.
func (m *MockAuthService) grantPermissionsScoped(perms []auth.Permission, accounts []string) {
	authCtx := &auth.AuthContext{Permissions: perms}
	decide := func(action, resource string) bool {
		return authCtx.HasPermission(action, resource)
	}
	m.On("HasPermissionAPI", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(decide, nil).Maybe()
	// The SEC-01 execution-time constraint check. This previously reused
	// `decide`, which takes only (action, resource) and therefore discarded
	// the constraint arguments outright, so the mock answered "allowed" for
	// all five constraint dimensions when the request fell OUTSIDE the
	// permission's constraints (issue #1762).
	//
	// The shortcut was justified on the grounds that a principal holding only
	// admin:* satisfies any constraint set for the verbs it holds. That is
	// true, and stayed true through #1758: both halves short-circuit on the
	// admin:* wildcard before reaching any constraint comparison, so for such
	// a principal the constraint arguments are ignored whatever Constraints
	// the admin permission itself carries, and #1758's carve-out of
	// execute:ri-exchange only changed which verbs both DENY in lockstep.
	// What the justification never covered is the helper it was attached to.
	// grantPermissionsScoped takes an ARBITRARY permission set, and a
	// permission carrying Constraints is exactly what the check exists to
	// bound.
	decideConstrained := constraintDecisionFunc(
		func(action, resource string, constraintSets []auth.PermissionConstraints) (bool, error) {
			return auth.PermissionsAllowForConstraintSets(perms, action, resource, constraintSets)
		})
	m.On("HasPermissionForConstraintsAPI", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(decideConstrained, nil).Maybe()
	m.On("GetAllowedAccountsAPI", mock.Anything, mock.Anything).
		Return(accounts, nil).Maybe()
}

func (m *MockAuthService) GetAllowedAccountsAPI(ctx context.Context, userID string) ([]string, error) {
	args := m.Called(ctx, userID)
	if v := args.Get(0); v != nil {
		return v.([]string), args.Error(1)
	}
	return nil, args.Error(1)
}

// API Key management mock methods.
func (m *MockAuthService) CreateAPIKeyAPI(ctx context.Context, userID string, req interface{}) (interface{}, error) {
	args := m.Called(ctx, userID, req)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) ListUserAPIKeysAPI(ctx context.Context, userID string) (interface{}, error) {
	args := m.Called(ctx, userID)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) GetAPIKeysUsageStatsAPI(ctx context.Context, userID string) (interface{}, error) {
	args := m.Called(ctx, userID)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) DeleteAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	args := m.Called(ctx, userID, keyID)
	return args.Error(0)
}

func (m *MockAuthService) RevokeAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	args := m.Called(ctx, userID, keyID)
	return args.Error(0)
}

func (m *MockAuthService) ValidateUserAPIKeyAPI(ctx context.Context, apiKey string) (interface{}, interface{}, error) {
	args := m.Called(ctx, apiKey)
	return args.Get(0), args.Get(1), args.Error(2)
}

func (m *MockAuthService) HasAPIKeyPermissionAPI(ctx context.Context, apiKey, action, resource string) (string, string, bool, error) {
	args := m.Called(ctx, apiKey, action, resource)
	return args.String(0), args.String(1), args.Bool(2), args.Error(3)
}

func (m *MockAuthService) HasAPIKeyPermissionForConstraintsAPI(ctx context.Context, keyID, userID, action, resource string, constraintSets []auth.PermissionConstraints) (bool, error) {
	args := m.Called(ctx, keyID, userID, action, resource, constraintSets)
	return args.Bool(0), args.Error(1)
}
