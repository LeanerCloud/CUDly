package iac

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests execute arm/CUDly-CrossSubscription/setup-gcp-wif.sh against a
// stub `gcloud` rather than reading it, because the defect they cover (#1661
// item 3) is an invocation gcloud rejects: reading the script cannot tell a
// command gcloud accepts from one it refuses, and the refusal landed after every
// mutation had been made.

// gcloudCredConfigStub is a stand-in `gcloud` that records every invocation and
// reproduces the one argparse rule under test: create-cred-config requires
// exactly one credential source. Every other verb succeeds, so a run that
// reaches them has proved the script got that far.
const gcloudCredConfigStub = `#!/usr/bin/env bash
printf '%s\n' "gcloud $*" >> "${STUB_LOG}"
case "$*" in
  *"projects describe"*)
    echo "000000000000"
    ;;
  *create-cred-config*)
    sources=0
    for a in "$@"; do
      case "$a" in
        --aws|--azure|--credential-source-file=*|--credential-source-url=*|--executable-command=*)
          sources=$((sources + 1)) ;;
      esac
    done
    if [[ $sources -ne 1 ]]; then
      echo "ERROR: (gcloud.iam.workload-identity-pools.create-cred-config) Exactly one of (--aws | --azure | --credential-source-file | --credential-source-url | --executable-command) must be specified." >&2
      exit 1
    fi
    echo '{"type":"external_account","audience":"stub"}'
    ;;
  *"workload-identity-pools providers describe"*|*"workload-identity-pools describe"*)
    # Nothing exists yet, so the script takes its create paths.
    exit 1
    ;;
esac
exit 0
`

// runSetupScript runs setup-gcp-wif.sh with the stub `gcloud` first on PATH and
// returns its exit code, stdout, stderr and the recorded gcloud invocations.
//
// PATH is set on the child process only, so a run here cannot leak a stub
// `gcloud` into any other test.
func runSetupScript(t *testing.T, args ...string) (exitCode int, stdout, stderr string, calls []string) {
	t.Helper()

	// Fatal, not Skip: a green run that never executed the script would hide
	// exactly the regression this test exists to catch.
	bashPath, lookErr := exec.LookPath("bash")
	if lookErr != nil {
		t.Fatalf("bash is required to execute %s: %v", setupScript, lookErr)
	}

	dir := t.TempDir()
	stubDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(stubDir, 0o700); err != nil {
		t.Fatalf("create stub dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stubDir, "gcloud"), []byte(gcloudCredConfigStub), 0o755); err != nil {
		t.Fatalf("write gcloud stub: %v", err)
	}
	logPath := filepath.Join(dir, "gcloud-invocations.log")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bashPath, append([]string{setupScript}, args...)...)
	cmd.Env = []string{
		"PATH=" + stubDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"STUB_LOG=" + logPath,
	}
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	var exitErr *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("run %s: %v (stderr: %s)", setupScript, err, errBuf.String())
	}

	logBytes, err := os.ReadFile(logPath)
	switch {
	case os.IsNotExist(err):
		// The stub was never invoked, which is what the reject case wants.
	case err != nil:
		t.Fatalf("read gcloud invocation log: %v", err)
	default:
		for _, line := range strings.Split(strings.TrimSpace(string(logBytes)), "\n") {
			if line != "" {
				calls = append(calls, line)
			}
		}
	}
	return cmd.ProcessState.ExitCode(), outBuf.String(), errBuf.String(), calls
}

// setupScriptCommonArgs are the flags every case below shares.
func setupScriptCommonArgs() []string {
	return []string{
		"--project", "my-gcp-project",
		"--sa-email", "cudly@my-gcp-project.iam.gserviceaccount.com",
	}
}

func setupScriptOIDCArgs(extra ...string) []string {
	return append(append(setupScriptCommonArgs(),
		"--provider-type", "oidc",
		"--issuer-uri", "https://cudly.example.com/oidc",
		"--oidc-subject", "cudly-controller",
	), extra...)
}

// credConfigCalls returns the recorded create-cred-config invocations.
func credConfigCalls(calls []string) []string {
	var out []string
	for _, c := range calls {
		if strings.Contains(c, "create-cred-config") {
			out = append(out, c)
		}
	}
	return out
}

// TestSetupScriptOIDCCompletesWithoutACredentialSource is the regression test
// for #1661 item 3. The OIDC branch called create-cred-config with no credential
// source; gcloud requires exactly one, so the command was rejected and `set -e`
// aborted the run at its last step, after the pool, the provider and the
// impersonation grant had all been created. The customer was left with a
// configured project, a non-zero exit and nothing to register.
func TestSetupScriptOIDCCompletesWithoutACredentialSource(t *testing.T) {
	exitCode, stdout, stderr, calls := runSetupScript(t, setupScriptOIDCArgs()...)

	if exitCode != 0 {
		t.Fatalf("an OIDC run without a credential source must complete, got exit %d; stderr:\n%s", exitCode, stderr)
	}
	if len(calls) == 0 {
		t.Fatal("the gcloud stub was never invoked, so this case proves nothing")
	}
	if got := credConfigCalls(calls); len(got) != 0 {
		t.Errorf("create-cred-config must not be called without a credential source, got: %v", got)
	}
	// The audience is the whole point of the run once the credential config is
	// not produced: without it the operator has nothing to paste into CUDly.
	if !strings.Contains(stdout, "gcp_wif_audience : //iam.googleapis.com/projects/000000000000/locations/global/workloadIdentityPools/cudly-pool/providers/cudly-provider") {
		t.Errorf("the run must print the WIF audience to register; stdout:\n%s", stdout)
	}
}

// TestSetupScriptEmitsCredentialConfigWhenSourced covers the paths that do
// produce a credential config, so the fix above cannot be "stop emitting one".
func TestSetupScriptEmitsCredentialConfigWhenSourced(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantFlags []string
	}{
		{
			name: "aws provider",
			args: append(setupScriptCommonArgs(),
				"--provider-type", "aws",
				"--aws-account-id", "123456789012",
				"--aws-role-name", "CUDly-Execution"),
			wantFlags: []string{"--aws"},
		},
		{
			name:      "oidc with a file source",
			args:      setupScriptOIDCArgs("--oidc-credential-source", "/var/run/secrets/cudly/token"),
			wantFlags: []string{"--credential-source-file=/var/run/secrets/cudly/token"},
		},
		{
			// A JSON response needs the format and the field holding the token;
			// with neither, gcloud treats the whole body as the token.
			name: "oidc with a JSON url source",
			args: setupScriptOIDCArgs(
				"--oidc-credential-source", "https://cudly.example.com/oidc/token",
				"--oidc-credential-source-field", "value"),
			wantFlags: []string{
				"--credential-source-url=https://cudly.example.com/oidc/token",
				"--credential-source-type=json",
				"--credential-source-field-name=value",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exitCode, _, stderr, calls := runSetupScript(t, tc.args...)

			if exitCode != 0 {
				t.Fatalf("run failed with exit %d; stderr:\n%s", exitCode, stderr)
			}
			got := credConfigCalls(calls)
			if len(got) != 1 {
				t.Fatalf("expected exactly one create-cred-config invocation, got %d: %v", len(got), calls)
			}
			for _, flag := range tc.wantFlags {
				if !strings.Contains(got[0], flag) {
					t.Errorf("create-cred-config must carry %q, got:\n%s", flag, got[0])
				}
			}
		})
	}
}

// TestSetupScriptRejectsCredentialSourceInAWSMode pins the credential source
// flags to the provider type they configure. An AWS provider's source is AWS
// IMDS, so accepting these and ignoring them would emit an IMDS-sourced config
// while the operator believes they pinned a token file.
func TestSetupScriptRejectsCredentialSourceInAWSMode(t *testing.T) {
	args := append(setupScriptCommonArgs(),
		"--provider-type", "aws",
		"--aws-account-id", "123456789012",
		"--aws-role-name", "CUDly-Execution",
		"--oidc-credential-source", "/var/run/secrets/cudly/token")

	exitCode, _, stderr, calls := runSetupScript(t, args...)

	if exitCode == 0 {
		t.Error("--oidc-credential-source was accepted under --provider-type aws (exit 0)")
	}
	if len(calls) != 0 {
		t.Errorf("the rejection must happen before the first gcloud call, got %d: %v", len(calls), calls)
	}
	if !strings.Contains(stderr, "do not apply to --provider-type aws") {
		t.Errorf("stderr must explain why the flag is refused, got:\n%s", stderr)
	}
}

// TestSetupScriptRejectsCredentialFieldWithoutSource covers the other half of
// the credential-source validation: the field name says how to read a token out
// of a JSON response, so on its own it configures nothing. Accepting it silently
// would emit a credential config with no credential source, which is the
// invocation gcloud rejects in the first place.
func TestSetupScriptRejectsCredentialFieldWithoutSource(t *testing.T) {
	exitCode, _, stderr, calls := runSetupScript(t,
		setupScriptOIDCArgs("--oidc-credential-source-field", "value")...)

	if exitCode == 0 {
		t.Error("--oidc-credential-source-field was accepted without a source (exit 0)")
	}
	if len(calls) != 0 {
		t.Errorf("the rejection must happen before the first gcloud call, got %d: %v", len(calls), calls)
	}
	if !strings.Contains(stderr, "--oidc-credential-source-field") {
		t.Errorf("stderr must name the rejected flag, got:\n%s", stderr)
	}
}

// TestSetupScriptRejectsInsecureCredentialSource pins the two shapes a
// credential source may take. A plain http:// endpoint would carry the subject
// token in cleartext, and anything else is neither a path gcloud can read nor a
// URL it can fetch.
func TestSetupScriptRejectsInsecureCredentialSource(t *testing.T) {
	for _, source := range []string{
		"http://insecure.example.com/token",
		"relative/path/token",
		"token",
	} {
		t.Run(source, func(t *testing.T) {
			exitCode, _, stderr, calls := runSetupScript(t,
				setupScriptOIDCArgs("--oidc-credential-source", source)...)

			if exitCode == 0 {
				t.Errorf("credential source %q was accepted (exit 0)", source)
			}
			// Rejected before the first gcloud call, so a bad invocation leaves
			// no pool, provider or grant behind.
			if len(calls) != 0 {
				t.Errorf("no gcloud call may be made before the credential source is validated, got %d: %v", len(calls), calls)
			}
			if !strings.Contains(stderr, "--oidc-credential-source") {
				t.Errorf("stderr must name the rejected flag, got:\n%s", stderr)
			}
		})
	}
}
