package api

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateAWSExternalID covers the issue #128 backend validation
// invariants for the AWS sts:ExternalId field on the role_arn auth mode.
// The frontend always populates this field (issue #18 / PR #36) but the
// backend is the source of truth — defence-in-depth requires that empty
// / out-of-range / disallowed-charset values are rejected with 400s on
// both create and update.
func TestValidateAWSExternalID(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantError   bool
		errContains string
	}{
		// --- Valid values ---
		{"uuid", "550e8400-e29b-41d4-a716-446655440000", false, ""},
		{"32 hex chars", "0123456789abcdef0123456789abcdef", false, ""},
		{"with all allowed punctuation", "abcd_+=,.@:/-1234", false, ""},
		{"max length boundary - 1224 chars", strings.Repeat("a", 1224), false, ""},
		{"min length boundary - 16 chars", "0123456789abcdef", false, ""},

		// --- Invalid: empty / whitespace ---
		{"empty", "", true, "required"},
		{"whitespace only", "   \t\n", true, "required"},

		// --- Invalid: length ---
		{"too short - 1 char", "x", true, "16-1224"},
		{"too short - 15 chars", "0123456789abcde", true, "16-1224"},
		{"too long - 1225 chars", strings.Repeat("a", 1225), true, "16-1224"},

		// --- Invalid: charset ---
		{"contains space", "abcd1234abcd1234 invalid", true, "characters"},
		{"contains question mark", "abcd1234abcd1234?bad", true, "characters"},
		{"contains parens", "abcd1234abcd1234(x)", true, "characters"},
		{"contains semicolon", "abcd1234abcd1234;rm", true, "characters"},
		{"contains unicode", "abcd1234abcd1234é", true, "characters"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAWSExternalID(tc.input)
			if tc.wantError {
				require.Error(t, err)
				ce, ok := IsClientError(err)
				require.True(t, ok, "expected ClientError, got %T", err)
				assert.Equal(t, 400, ce.code)
				assert.Contains(t, ce.message, tc.errContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestCreateAccount_AWSExternalID_RoleArnRequired locks down that the
// validation runs at the createAccount handler level — not just on the
// pure helper — for the role_arn auth mode (issue #128).
func TestCreateAccount_AWSExternalID_RoleArnRequired(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)
	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"name":"Acme","provider":"aws","external_id":"123456789012",` +
		`"aws_auth_mode":"role_arn","aws_role_arn":"arn:aws:iam::123456789012:role/CUDly"}`
	result, err := handler.createAccount(ctx, adminRequest(body))
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "aws_external_id")
}

// TestCreateAccount_AWSExternalID_RoleArnTooShort rejects 15-char IDs.
func TestCreateAccount_AWSExternalID_RoleArnTooShort(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)
	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"name":"Acme","provider":"aws","external_id":"123456789012",` +
		`"aws_auth_mode":"role_arn","aws_role_arn":"arn:aws:iam::123456789012:role/CUDly",` +
		`"aws_external_id":"too-short"}`
	result, err := handler.createAccount(ctx, adminRequest(body))
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "16-1224")
}

// TestCreateAccount_AWSExternalID_RoleArnInvalidChars rejects values
// with characters outside AWS's documented sts:ExternalId charset.
func TestCreateAccount_AWSExternalID_RoleArnInvalidChars(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)
	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"name":"Acme","provider":"aws","external_id":"123456789012",` +
		`"aws_auth_mode":"role_arn","aws_role_arn":"arn:aws:iam::123456789012:role/CUDly",` +
		`"aws_external_id":"abcd1234abcd1234 has space"}`
	result, err := handler.createAccount(ctx, adminRequest(body))
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "characters")
}

// TestCreateAccount_AWSExternalID_RoleArnHappyPath confirms a valid
// External ID under role_arn passes validation end-to-end.
func TestCreateAccount_AWSExternalID_RoleArnHappyPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)
	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"name":"Acme","provider":"aws","external_id":"123456789012",` +
		`"aws_auth_mode":"role_arn","aws_role_arn":"arn:aws:iam::123456789012:role/CUDly",` +
		`"aws_external_id":"550e8400-e29b-41d4-a716-446655440000"}`
	_, err := handler.createAccount(ctx, adminRequest(body))
	require.NoError(t, err)
}

// TestCreateAccount_AWSExternalID_RoleArnNoRoleArnSelfAccount asserts
// that the validation does NOT fire when role_arn mode is paired with
// an empty role ARN — that combination is the self-account onboarding
// path (uses ambient Lambda credentials, no AssumeRole, no ExternalId
// needed). See awsAmbientCredResult in handler_accounts.go.
func TestCreateAccount_AWSExternalID_RoleArnNoRoleArnSelfAccount(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	setupAdminAuth(ctx, mockAuth)
	store := setupAdminMock(ctx)
	handler := &Handler{auth: mockAuth, config: store}

	body := `{"name":"CUDly host","provider":"aws","external_id":"123456789012",` +
		`"aws_auth_mode":"role_arn"}` // no aws_role_arn, no aws_external_id
	_, err := handler.createAccount(ctx, adminRequest(body))
	require.NoError(t, err)
}

// TestCreateAccount_AWSExternalID_NotRequiredForOtherAuthModes asserts
// that the new validation only fires for role_arn — bastion and WIF
// auth modes have their own assume-role mechanics where ExternalId is
// not the primary guard, and access_keys doesn't assume a role at all.
func TestCreateAccount_AWSExternalID_NotRequiredForOtherAuthModes(t *testing.T) {
	for _, mode := range []string{"access_keys", "bastion", "workload_identity_federation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			mockAuth := new(MockAuthService)
			setupAdminAuth(ctx, mockAuth)
			store := setupAdminMock(ctx)
			handler := &Handler{auth: mockAuth, config: store}

			body := `{"name":"Acme","provider":"aws","external_id":"123456789012",` +
				`"aws_auth_mode":"` + mode + `"}`
			_, err := handler.createAccount(ctx, adminRequest(body))
			require.NoError(t, err)
		})
	}
}

// TestParseArnPartition covers the helper that extracts the partition
// segment from an STS GetCallerIdentity ARN (issue #130c). The result
// is interpolated into the IAM trust-policy snippet, so any failure to
// recognise a known partition must fall back to "" (which the frontend
// then defaults to "aws").
func TestParseArnPartition(t *testing.T) {
	cases := []struct {
		name string
		arn  string
		want string
	}{
		{"standard aws", "arn:aws:iam::123456789012:role/CUDly", "aws"},
		{"china", "arn:aws-cn:iam::123456789012:role/CUDly", "aws-cn"},
		{"govcloud", "arn:aws-us-gov:iam::123456789012:role/CUDly", "aws-us-gov"},
		{"aws sts", "arn:aws:sts::123456789012:assumed-role/CUDly/session", "aws"},
		{"unknown partition", "arn:evil:iam::123456789012:role/X", ""},
		{"missing prefix", "iam::123456789012:role/CUDly", ""},
		{"empty", "", ""},
		{"only arn:", "arn:", ""},
		{"arn with no colons after prefix", "arn:nopartition", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseArnPartition(tc.arn)
			assert.Equal(t, tc.want, got)
		})
	}
}
