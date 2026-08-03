// Package main's only non-test file, generate-federation-iac.go, carries a
// //go:build ignore tag so `go build ./...` skips it, which also means these
// tests cannot import its identifiers. They exercise the compiled script as a
// subprocess instead, which is how an operator actually runs it and is the only
// way to assert on its exit status and stderr.
package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// scriptFile is relative to this package directory.
	scriptFile = "generate-federation-iac.go"
	// repoRoot is the directory the script must run from: its --templates-dir
	// and --modules-dir defaults are repository-root-relative.
	repoRoot = ".."

	buildTimeout = 2 * time.Minute
	runTimeout   = 30 * time.Second
)

var (
	buildOnce   sync.Once
	buildDir    string
	builtBinary string
	buildErr    error
	buildOutput string
)

// TestMain removes the directory holding the compiled script. t.TempDir cannot
// own it: the binary is built once and shared by every test in this package, so
// its lifetime is the test binary's, not any single test's.
func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// generatorBinary compiles the script once per test binary and returns the path
// to the executable. Naming the file explicitly on the command line is what
// makes the //go:build ignore tag a no-op, exactly as the documented
// `go run scripts/generate-federation-iac.go` invocation does.
func generatorBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		if _, err := exec.LookPath("go"); err != nil {
			buildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "genfediac")
		if err != nil {
			buildErr = err
			return
		}
		buildDir = dir
		bin := filepath.Join(dir, "generate-federation-iac")
		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, scriptFile)
		out, err := cmd.CombinedOutput()
		buildOutput = string(out)
		if err != nil {
			buildErr = err
			return
		}
		builtBinary = bin
	})
	if buildErr != nil {
		t.Fatalf("build %s: %v\n%s", scriptFile, buildErr, buildOutput)
	}
	return builtBinary
}

type runResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// runGenerator executes the compiled script from the repository root.
func runGenerator(t *testing.T, args ...string) runResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, generatorBinary(t), args...)
	cmd.Dir = repoRoot
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// A non-zero exit is the expected outcome for most cases here, so only a
	// failure to start (or the context deadline) is fatal.
	var exitErr *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("run %v: %v (stderr: %s)", args, err, stderr.String())
	}
	return runResult{
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		exitCode: cmd.ProcessState.ExitCode(),
	}
}

// awsWIFArgs is a complete, otherwise-valid AWS-WIF invocation; each test
// varies only the subject claim and the output destination.
func awsWIFArgs(subjectClaim, output string) []string {
	args := []string{
		"--target", "aws",
		"--source", "gcp",
		"--account-name", "prod",
		"--account-id", "123456789012",
		"--output", output,
	}
	if subjectClaim != "" {
		args = append(args, "--oidc-subject-claim", subjectClaim)
	}
	return args
}

// TestGenerator_RejectsHostileOIDCSubjectClaim is the regression test for the
// #1691 review finding: the generator used to assign --oidc-subject-claim
// straight onto the template data with no validation and no escaping, and the
// value is interpolated verbatim into three grammars this script renders:
// Bash (aws-cfn-deploy.sh.tmpl builds a "OIDCSubjectClaim=<value>" word for
// `aws cloudformation deploy`), JSON (aws-wif-cf-params.json.tmpl) and HCL
// (aws-wif.tfvars.tmpl). None of those artifacts validates the value when the
// operator runs them, and the analogous guard in the server-rendered
// aws-wif-cli.sh could not help even if it were here: it runs after the
// OIDC_SUBJECT_CLAIM="${OIDC_SUBJECT_CLAIM:-<value>}" assignment that embeds
// the value, so a command substitution in the default has already executed by
// the time it is inspected. Generation time is the only point that runs first.
//
// Each case asserts a non-zero exit, that stderr names the flag, and that no
// output file was produced.
func TestGenerator_RejectsHostileOIDCSubjectClaim(t *testing.T) {
	cases := []struct {
		name  string
		claim string
	}{
		{"command substitution", "$(id)"},
		{"backtick substitution", "`id`"},
		{"shell variable", "${accounts.google.com:sub}"},
		{"closing brace ends the parameter expansion", "x}; touch /tmp/pwned"},
		{"double quote breaks out of the shell word", `a" injected="1`},
		{"single quote", "a' injected='1"},
		{"backslash", `a\b`},
		{"semicolon", "abc;id"},
		{"ampersand", "abc&id"},
		{"pipe", "abc|id"},
		{"redirect", "abc>out"},
		{"whitespace", "abc def"},
		{"newline", "abc\nid"},
		{"wildcard", "*"},
		{"leading dash reads as a flag", "--output"},
		{"over the length cap", strings.Repeat("1", 256)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "generated.tfvars")
			res := runGenerator(t, awsWIFArgs(tc.claim, out)...)

			if res.exitCode == 0 {
				t.Errorf("claim %q was accepted (exit 0); stdout:\n%s", tc.claim, res.stdout)
			}
			if !strings.Contains(res.stderr, "--oidc-subject-claim") {
				t.Errorf("claim %q: stderr must name the rejected flag, got:\n%s", tc.claim, res.stderr)
			}
			// Rendering happens strictly after validation, so the run must not
			// have reached renderTmpl's error prefixes.
			for _, renderMarker := range []string{"render ", "parse ", "read internal/iacfiles"} {
				if strings.Contains(res.stderr, renderMarker) {
					t.Errorf("claim %q: run reached template rendering (stderr contains %q):\n%s",
						tc.claim, renderMarker, res.stderr)
				}
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("claim %q: output file %s must not be created (stat err: %v)", tc.claim, out, err)
			}
			if res.stdout != "" {
				t.Errorf("claim %q: nothing must be written to stdout, got:\n%s", tc.claim, res.stdout)
			}
		})
	}
}

// TestGenerator_RejectsMissingOIDCSubjectClaim covers the case this PR exists
// for: an AWS-WIF bundle with no subject claim at all would pin the trust
// policy to nothing and accept every identity the issuer can mint.
func TestGenerator_RejectsMissingOIDCSubjectClaim(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated.tfvars")
	res := runGenerator(t, awsWIFArgs("", out)...)

	if res.exitCode == 0 {
		t.Fatalf("an absent --oidc-subject-claim was accepted (exit 0); stdout:\n%s", res.stdout)
	}
	if !strings.Contains(res.stderr, "--oidc-subject-claim is required") {
		t.Errorf("stderr must say the flag is required, got:\n%s", res.stderr)
	}
	if !strings.Contains(res.stderr, ":sub condition") {
		t.Errorf("stderr must explain the missing :sub condition, got:\n%s", res.stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output file %s must not be created (stat err: %v)", out, err)
	}
}

// TestGenerator_RejectsHostileClaimInBundleMode covers --format bundle
// separately from the single-file cases above. Bundle mode is the path that
// renders aws-cfn-deploy.sh.tmpl, i.e. the one artifact that turns the claim
// into Bash source, and it reaches the templates through runBundle rather than
// runSingleFile. Without this case, moving the validator into runSingleFile
// would leave the shell sink unguarded and every other test would still pass.
func TestGenerator_RejectsHostileClaimInBundleMode(t *testing.T) {
	for _, claim := range []string{"", "$(id)", `a" injected="1`} {
		t.Run("claim="+claim, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "bundle.zip")
			res := runGenerator(t,
				"--target", "aws",
				"--source", "gcp",
				"--format", "bundle",
				"--account-name", "prod",
				"--account-id", "123456789012",
				"--oidc-subject-claim", claim,
				"--output", out,
			)
			if res.exitCode == 0 {
				t.Errorf("claim %q accepted in bundle mode (exit 0)", claim)
			}
			if !strings.Contains(res.stderr, "--oidc-subject-claim") {
				t.Errorf("claim %q: stderr must name the rejected flag, got:\n%s", claim, res.stderr)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("claim %q: bundle %s must not be written (stat err: %v)", claim, out, err)
			}
		})
	}
}

// TestGenerator_RejectsInapplicableSubjectClaim covers the combinations that
// render no AWS trust policy. Accepting the flag there and dropping it would be
// the same fail-quiet shape this PR removes: the operator asks for the trust to
// be pinned and gets an artifact that pins nothing, with no diagnostic.
func TestGenerator_RejectsInapplicableSubjectClaim(t *testing.T) {
	cases := []struct{ name, target, source string }{
		{"aws cross-account", "aws", "aws"},
		{"azure target", "azure", "aws"},
		{"gcp target", "gcp", "aws"},
		{"gcp same-cloud", "gcp", "gcp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runGenerator(t,
				"--target", tc.target,
				"--source", tc.source,
				"--account-name", "prod",
				"--account-id", "123456789012",
				"--oidc-subject-claim", "123456789012345678901",
				"--output", filepath.Join(t.TempDir(), "generated.tfvars"),
			)
			if res.exitCode == 0 {
				t.Errorf("--oidc-subject-claim was silently accepted for target=%s source=%s",
					tc.target, tc.source)
			}
			if !strings.Contains(res.stderr, "is not applicable") {
				t.Errorf("stderr must say the flag is not applicable, got:\n%s", res.stderr)
			}
		})
	}
}

// TestGenerator_InvalidTargetReportedAsTarget guards the diagnostic ordering.
// The claim check runs outside the target switch, so without an explicit
// --target check first, a typo'd target combined with a perfectly good
// --oidc-subject-claim reported the claim as "not applicable" and sent the
// operator off to drop a flag that was never the problem.
func TestGenerator_InvalidTargetReportedAsTarget(t *testing.T) {
	for _, withClaim := range []bool{false, true} {
		name := "without claim"
		args := []string{
			"--target", "Aws", // capitalised: a plausible typo
			"--source", "gcp",
			"--account-name", "prod",
			"--account-id", "123456789012",
			"--output", "-",
		}
		if withClaim {
			name = "with claim"
			args = append(args, "--oidc-subject-claim", "123456789012345678901")
		}
		t.Run(name, func(t *testing.T) {
			res := runGenerator(t, args...)
			if res.exitCode == 0 {
				t.Fatalf("--target Aws was accepted (exit 0)")
			}
			if !strings.Contains(res.stderr, "--target must be aws, azure, or gcp") {
				t.Errorf("a bad --target must be reported as a --target problem, got:\n%s", res.stderr)
			}
		})
	}
}

// TestGenerator_OverlongClaimIsNotEchoed checks that no rejection path echoes a
// multi-kilobyte argument back at the operator, whichever diagnosis it reports.
// Every branch that formats the claim goes through displayClaim, so this holds
// independently of the order the checks run in.
func TestGenerator_OverlongClaimIsNotEchoed(t *testing.T) {
	const claimLen = 50_000
	claim := strings.Repeat("A", claimLen)

	for _, tc := range []struct{ name, target, source, wantText string }{
		// Required path: the length cap is what rejects it.
		{"required path", "aws", "gcp", "over the 255-byte limit"},
		// Not-applicable path: the flag does not belong here at all, and saying
		// so is more useful than complaining about the length.
		{"not applicable path", "gcp", "gcp", "is not applicable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runGenerator(t,
				"--target", tc.target,
				"--source", tc.source,
				"--account-name", "prod",
				"--account-id", "123456789012",
				"--oidc-subject-claim", claim,
				"--output", filepath.Join(t.TempDir(), "generated.tfvars"),
			)
			if res.exitCode == 0 {
				t.Fatalf("a %d-character claim was accepted (exit 0)", claimLen)
			}
			if !strings.Contains(res.stderr, tc.wantText) {
				t.Errorf("expected %q in the rejection, got:\n%s", tc.wantText, res.stderr)
			}
			// Generous bound: any branch that echoed the claim in full would
			// blow past this by three orders of magnitude.
			if len(res.stderr) > 2000 {
				t.Errorf("the rejected claim must not be echoed back, but stderr is %d bytes", len(res.stderr))
			}
		})
	}
}

// TestGenerator_ValidationPrecedesTemplateRead proves the ordering directly
// rather than by inference: pointing --templates-dir at an empty directory
// makes any rendering attempt fail with a "read ...: no such file" error, so
// getting the validation error instead is proof the flag was rejected before
// the generator ever touched a template.
func TestGenerator_ValidationPrecedesTemplateRead(t *testing.T) {
	emptyTemplates := t.TempDir()
	args := append(awsWIFArgs("$(id)", filepath.Join(t.TempDir(), "generated.tfvars")),
		"--templates-dir", emptyTemplates)
	res := runGenerator(t, args...)

	if res.exitCode == 0 {
		t.Fatalf("hostile claim accepted (exit 0); stdout:\n%s", res.stdout)
	}
	if !strings.Contains(res.stderr, "--oidc-subject-claim") {
		t.Errorf("expected the validation error, got:\n%s", res.stderr)
	}
	if strings.Contains(res.stderr, "no such file") {
		t.Errorf("the generator read a template before validating the flag:\n%s", res.stderr)
	}
}

// TestGenerator_AcceptsValidOIDCSubjectClaim is the positive control: without
// it, a validator that rejected everything would pass every test above.
//
// It uses --format cf-params rather than the default tfvars output because the
// script's iacData is missing the CUDlyAPIURL / SourceAccountID / ContactEmail
// fields the tfvars templates reference, so every tfvars path fails to render
// on main today. That drift predates this change and is tracked in #1690; the
// cf-params path renders end to end.
func TestGenerator_AcceptsValidOIDCSubjectClaim(t *testing.T) {
	cases := []struct {
		name  string
		claim string
	}{
		{"gcp service account numeric unique id", "123456789012345678901"},
		{"azure managed identity object id", "11111111-2222-3333-4444-555555555555"},
		{"kubernetes style subject", "system:serviceaccount:cudly:reader"},
		{"github actions style subject", "repo:LeanerCloud/CUDly:ref:refs/heads/main"},
		{"email style subject", "cudly@example-project.iam.gserviceaccount.com"},
		{"at the length cap", strings.Repeat("1", 255)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runGenerator(t,
				"--target", "aws",
				"--source", "azure",
				"--tenant-id", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				"--format", "cf-params",
				"--account-name", "prod",
				"--account-id", "123456789012",
				"--oidc-subject-claim", tc.claim,
				"--output", "-",
			)
			if res.exitCode != 0 {
				t.Fatalf("valid claim %q rejected (exit %d); stderr:\n%s", tc.claim, res.exitCode, res.stderr)
			}
			want := `"ParameterKey": "OIDCSubjectClaim", "ParameterValue": "` + tc.claim + `"`
			if !strings.Contains(res.stdout, want) {
				t.Errorf("rendered cf-params must carry the claim verbatim.\nwant substring: %s\ngot:\n%s",
					want, res.stdout)
			}
		})
	}
}

// TestGenerator_CrossAccountDoesNotRequireSubjectClaim guards the scoping of
// the new requirement. target=aws with source=aws is the cross-account
// (external ID) path: it renders aws-cross-account.tfvars.tmpl, which has no
// OIDC trust policy at all, so demanding a subject claim there would break a
// working combination.
//
// It asserts only on stderr, not on the exit code, because that invocation
// currently fails for an unrelated pre-existing reason: iacData is missing the
// SourceAccountID / CUDlyAPIURL / ContactEmail / OIDCIssuerHost fields the
// tfvars and deploy-script templates reference, so no tfvars path of this
// script renders on main today. That drift is tracked in #1690; once it is
// fixed this can also assert exitCode == 0.
func TestGenerator_CrossAccountDoesNotRequireSubjectClaim(t *testing.T) {
	res := runGenerator(t,
		"--target", "aws",
		"--source", "aws",
		"--account-name", "tgt",
		"--account-id", "999888777666",
		"--output", "-",
	)
	if strings.Contains(res.stderr, "--oidc-subject-claim") {
		t.Errorf("the aws->aws cross-account path must not require --oidc-subject-claim, got:\n%s", res.stderr)
	}
}
