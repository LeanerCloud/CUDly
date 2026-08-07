package main

// generate-federation-iac_test.go drives scripts/generate-federation-iac.go
// end to end via `go run`, the same way an operator would invoke it. The
// script carries a `//go:build ignore` tag, so it is never compiled as part
// of this package (or any other) — the only way to exercise its actual
// --format=tfvars rendering path, and therefore the only way to catch drift
// between iacData and the tfvars templates it renders, is to run it as a
// subprocess. See #1709: iacData was missing three fields (ContactEmail,
// CUDlyAPIURL, SourceAccountID) referenced by every tfvars template, and
// nothing exercised this path so the break went unnoticed.

import (
	"os/exec"
	"strings"
	"testing"
)

// runViaGoRun invokes `go run generate-federation-iac.go <args>` from the
// repository root (the test binary's working directory is this package's
// directory, scripts/, so ".." is the repo root — matching how the script's
// own doc comment says to invoke it). Named distinctly from the
// generate_federation_iac_test.go file's own runGenerator (same package,
// different signature: that one runs a pre-built binary) to avoid a symbol
// collision between the two test files.
func runViaGoRun(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "scripts/generate-federation-iac.go"}, args...)...)
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestGenerateFederationIaC_TfvarsCombinations runs every --target/--source
// combination routed by singleFileTmpl through the real --format=tfvars
// path (the default format) and asserts a successful render plus the
// presence of the fields that data. Failing to add a field the templates
// reference is exactly the bug in #1709: text/template treats a missing
// struct field as a hard execution error, not an empty string.
func TestGenerateFederationIaC_TfvarsCombinations(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantContain []string
	}{
		{
			name: "aws target, azure source",
			args: []string{
				"--target", "aws", "--source", "azure",
				"--account-name", "Acme", "--account-id", "123456789012",
				"--tenant-id", "11111111-2222-3333-4444-555555555555",
				"--oidc-subject-claim", "11111111-2222-3333-4444-555555555555",
				"--contact-email", "ops@example.com", "--cudly-api-url", "https://cudly.example.com",
				"--output", "-",
			},
			wantContain: []string{
				`cudly_api_url = "https://cudly.example.com"`,
				`contact_email = "ops@example.com"`,
			},
		},
		{
			name: "aws target, gcp source",
			args: []string{
				"--target", "aws", "--source", "gcp",
				"--account-name", "Acme", "--account-id", "123456789012",
				"--oidc-subject-claim", "123456789012345678901",
				"--contact-email", "ops@example.com", "--cudly-api-url", "https://cudly.example.com",
				"--output", "-",
			},
			wantContain: []string{
				`cudly_api_url = "https://cudly.example.com"`,
				`contact_email = "ops@example.com"`,
			},
		},
		{
			name: "aws target, aws source (cross-account)",
			args: []string{
				"--target", "aws", "--source", "aws",
				"--account-name", "Acme", "--account-id", "999888777666",
				"--source-account-id", "111122223333",
				"--contact-email", "ops@example.com", "--cudly-api-url", "https://cudly.example.com",
				"--output", "-",
			},
			wantContain: []string{
				`source_account_id = "111122223333"`,
				`cudly_api_url = "https://cudly.example.com"`,
				`contact_email = "ops@example.com"`,
			},
		},
		{
			name: "azure target, aws source",
			args: []string{
				"--target", "azure", "--source", "aws",
				"--account-name", "Acme", "--account-id", "sub-1234",
				"--tenant-id", "11111111-2222-3333-4444-555555555555",
				"--contact-email", "ops@example.com", "--cudly-api-url", "https://cudly.example.com",
				"--output", "-",
			},
			wantContain: []string{
				`cudly_api_url = "https://cudly.example.com"`,
				`contact_email = "ops@example.com"`,
			},
		},
		{
			name: "azure target, gcp source",
			args: []string{
				"--target", "azure", "--source", "gcp",
				"--account-name", "Acme", "--account-id", "sub-1234",
				"--tenant-id", "11111111-2222-3333-4444-555555555555",
				"--contact-email", "ops@example.com", "--cudly-api-url", "https://cudly.example.com",
				"--output", "-",
			},
			wantContain: []string{
				`cudly_api_url = "https://cudly.example.com"`,
				`contact_email = "ops@example.com"`,
			},
		},
		{
			name: "gcp target, gcp source (sa impersonation)",
			args: []string{
				"--target", "gcp", "--source", "gcp",
				"--account-name", "Acme", "--account-id", "my-project",
				"--contact-email", "ops@example.com", "--cudly-api-url", "https://cudly.example.com",
				"--output", "-",
			},
			wantContain: []string{
				`cudly_api_url = "https://cudly.example.com"`,
				`contact_email = "ops@example.com"`,
			},
		},
		{
			name: "gcp target, aws source (WIF pool)",
			args: []string{
				"--target", "gcp", "--source", "aws",
				"--account-name", "Acme", "--account-id", "my-project",
				"--source-account-id", "111122223333",
				"--contact-email", "ops@example.com", "--cudly-api-url", "https://cudly.example.com",
				"--output", "-",
			},
			wantContain: []string{
				`aws_account_id = "111122223333"`,
				`cudly_api_url = "https://cudly.example.com"`,
				`contact_email = "ops@example.com"`,
			},
		},
		{
			name: "gcp target, azure source (WIF pool)",
			args: []string{
				"--target", "gcp", "--source", "azure",
				"--account-name", "Acme", "--account-id", "my-project",
				"--tenant-id", "11111111-2222-3333-4444-555555555555",
				"--contact-email", "ops@example.com", "--cudly-api-url", "https://cudly.example.com",
				"--output", "-",
			},
			wantContain: []string{
				`cudly_api_url = "https://cudly.example.com"`,
				`contact_email = "ops@example.com"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runViaGoRun(t, tt.args...)
			if err != nil {
				t.Fatalf("generator failed: %v\noutput:\n%s", err, out)
			}
			for _, want := range tt.wantContain {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\noutput:\n%s", want, out)
				}
			}
		})
	}
}

// TestGenerateFederationIaC_RequiresSourceAccountID guards the fail-loud
// behavior added alongside the #1709 fix: --source-account-id has no
// sensible default in the standalone script (unlike the server, which
// resolves it via STS), so target/source combinations that need it must
// error explicitly instead of silently rendering an empty or wrong account
// ID into the trust policy.
func TestGenerateFederationIaC_RequiresSourceAccountID(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "aws target, aws source, no source-account-id",
			args: []string{
				"--target", "aws", "--source", "aws",
				"--account-name", "Acme", "--account-id", "999888777666",
				"--output", "-",
			},
		},
		{
			name: "gcp target, aws source, no source-account-id",
			args: []string{
				"--target", "gcp", "--source", "aws",
				"--account-name", "Acme", "--account-id", "my-project",
				"--output", "-",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runViaGoRun(t, tt.args...)
			if err == nil {
				t.Fatalf("expected failure without --source-account-id, got success:\n%s", out)
			}
			if !strings.Contains(out, "--source-account-id is required") {
				t.Errorf("expected error naming --source-account-id, got:\n%s", out)
			}
		})
	}
}

// TestGenerateFederationIaC_RejectsMalformedSourceAccountID guards the CR
// finding on #1710: a non-empty --source-account-id is not necessarily a
// valid one. Before this check, a malformed value flowed straight into
// data.SourceAccountID and out into the rendered tfvars, where the problem
// would only surface later as a confusing Terraform or AWS error far from
// its actual cause. Covers both consumers of SourceAccountID: --target aws
// --source aws (aws-cross-account) and --target gcp --source aws (WIF pool).
func TestGenerateFederationIaC_RejectsMalformedSourceAccountID(t *testing.T) {
	tests := []struct {
		name            string
		target          string
		sourceAccountID string
	}{
		{name: "aws target, too short", target: "aws", sourceAccountID: "12345"},
		{name: "aws target, non-digit characters", target: "aws", sourceAccountID: "1111222233aa"},
		{name: "aws target, leading plus sign", target: "aws", sourceAccountID: "+11122223333"},
		{name: "aws target, whitespace padded", target: "aws", sourceAccountID: " 111122223333"},
		{name: "gcp target, too short", target: "gcp", sourceAccountID: "12345"},
		{name: "gcp target, non-digit characters", target: "gcp", sourceAccountID: "1111222233aa"},
		{name: "gcp target, leading plus sign", target: "gcp", sourceAccountID: "+11122223333"},
		{name: "gcp target, whitespace padded", target: "gcp", sourceAccountID: " 111122223333"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accountID := "999888777666"
			if tt.target == "gcp" {
				accountID = "my-project"
			}
			out, err := runViaGoRun(t,
				"--target", tt.target, "--source", "aws",
				"--account-name", "Acme", "--account-id", accountID,
				"--source-account-id", tt.sourceAccountID,
				"--output", "-",
			)
			if err == nil {
				t.Fatalf("expected failure for malformed --source-account-id %q, got success:\n%s", tt.sourceAccountID, out)
			}
			if !strings.Contains(out, "is not a valid AWS account ID") {
				t.Errorf("expected error naming the invalid --source-account-id, got:\n%s", out)
			}
			if !strings.Contains(out, tt.sourceAccountID) {
				t.Errorf("expected error to name the rejected value %q, got:\n%s", tt.sourceAccountID, out)
			}
		})
	}
}
