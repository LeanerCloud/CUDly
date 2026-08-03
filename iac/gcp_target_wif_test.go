package iac

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// These tests assert on the exact bytes of the Terraform module shipped to
// customers (internal/api/handler_federation.go serves federation/gcp-target as
// the default bundle for target=gcp), so a regression is caught in the artifact
// the customer applies rather than in a copy of it.

const gcpTargetMainTF = "federation/gcp-target/terraform/main.tf"

// awsRoleAttributeMapping is the CEL expression main.tf maps to
// attribute.aws_role. It normalises a session ARN
// (arn:aws:sts::<acct>:assumed-role/<role>/<session>) down to the role ARN.
//
// It is asserted byte-for-byte below so that normalizeAWSRoleAttr, the Go
// transcription the behavioral tests run, cannot silently drift from the
// expression Terraform actually applies. Editing the mapping fails the string
// assertion and forces the transcription to be revisited.
const awsRoleAttributeMapping = `assertion.arn.contains('assumed-role') ? assertion.arn.extract('{account_arn}assumed-role/') + 'assumed-role/' + assertion.arn.extract('assumed-role/{role_name}/') : assertion.arn`

const (
	testAccountID = "123456789012"
	testRoleName  = "CUDly-Execution"
)

// testPinnedRoleARN is what local.aws_role_arn renders to for the values above,
// and therefore both the right-hand side of the attribute condition and the
// attribute value named by the impersonation grant.
const testPinnedRoleARN = "arn:aws:sts::" + testAccountID + ":assumed-role/" + testRoleName

func readMainTF(t *testing.T) string {
	t.Helper()
	raw, err := Modules.ReadFile(gcpTargetMainTF)
	if err != nil {
		t.Fatalf("read %s: %v", gcpTargetMainTF, err)
	}
	return string(raw)
}

// TestGCPTargetPinsRoleARNByEquality asserts the provider normalises the
// assertion ARN and then compares it with ==. A substring test on the raw ARN is
// bypassable (see TestGCPTargetRejectsIAMUserPathBypass).
func TestGCPTargetPinsRoleARNByEquality(t *testing.T) {
	tf := readMainTF(t)

	wantMapping := `"attribute.aws_role" = "` + awsRoleAttributeMapping + `"`
	if !strings.Contains(tf, wantMapping) {
		t.Errorf("%s: attribute.aws_role must be mapped to the normalising expression, want line:\n%s", gcpTargetMainTF, wantMapping)
	}

	wantLocal := `aws_role_arn = "arn:aws:sts::${var.aws_account_id}:assumed-role/${var.aws_role_name}"`
	if !strings.Contains(tf, wantLocal) {
		t.Errorf("%s: expected local pinning the trusted role ARN:\n%s", gcpTargetMainTF, wantLocal)
	}

	wantCondition := `"attribute.aws_role == '${local.aws_role_arn}'"`
	if !strings.Contains(tf, wantCondition) {
		t.Errorf("%s: attribute_condition must compare attribute.aws_role for equality, want:\n%s", gcpTargetMainTF, wantCondition)
	}

	// Guard the two shapes this fix replaced: a raw mapping, and a substring
	// test. Either one on its own re-opens the bypass.
	if strings.Contains(tf, `"attribute.aws_role" = "assertion.arn"`) {
		t.Errorf("%s: attribute.aws_role is mapped raw again; the ARN must be normalised before it is compared", gcpTargetMainTF)
	}
	if strings.Contains(tf, "attribute.aws_role.contains(") || strings.Contains(tf, "attribute.aws_role.startsWith(") {
		t.Errorf("%s: attribute_condition uses a substring/prefix test on attribute.aws_role; only == pins a single role", gcpTargetMainTF)
	}
}

// TestGCPTargetGrantsImpersonationToOneIdentity asserts the
// roles/iam.workloadIdentityUser grant names an exact identity rather than the
// whole pool. Tightening the attribute condition alone leaves the blast radius
// of the next mapping bug unchanged.
func TestGCPTargetGrantsImpersonationToOneIdentity(t *testing.T) {
	tf := readMainTF(t)

	wantAWSMember := `"principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.cudly.name}/attribute.aws_role/${local.aws_role_arn}"`
	if !strings.Contains(tf, wantAWSMember) {
		t.Errorf("%s: AWS impersonation grant must name the exact normalised role ARN, want:\n%s", gcpTargetMainTF, wantAWSMember)
	}

	wantOIDCMember := `"principal://iam.googleapis.com/${google_iam_workload_identity_pool.cudly.name}/subject/${var.oidc_subject}"`
	if !strings.Contains(tf, wantOIDCMember) {
		t.Errorf("%s: OIDC impersonation grant must name the exact subject, want:\n%s", gcpTargetMainTF, wantOIDCMember)
	}

	// Any wildcard in a principal identifier matches every identity in the pool,
	// whichever role or attribute it is attached to.
	wildcard := regexp.MustCompile(`principal(Set)?://[^"]*\*`)
	if m := wildcard.FindString(tf); m != "" {
		t.Errorf("%s: wildcard principal identifier %q grants impersonation pool-wide", gcpTargetMainTF, m)
	}
}

// TestGCPTargetUpgradeOrderingIsPinned asserts the two lifecycle directives that
// make the member swap safe on deployments applied before this fix. Losing
// either one turns a security fix into an outage: without depends_on the narrow
// member can be created while attribute.aws_role still holds the raw session ARN
// (matching nothing), and without create_before_destroy the old pool-wide member
// is removed before the narrow one exists.
func TestGCPTargetUpgradeOrderingIsPinned(t *testing.T) {
	tf := readMainTF(t)

	_, grant, found := strings.Cut(tf, `resource "google_service_account_iam_member" "cudly_wif"`)
	if !found {
		t.Fatalf("%s: google_service_account_iam_member.cudly_wif not found", gcpTargetMainTF)
	}
	if !strings.Contains(grant, "depends_on = [google_iam_workload_identity_pool_provider.cudly]") {
		t.Errorf("%s: cudly_wif must depend on the provider so the attribute mapping is updated before the narrow member is created", gcpTargetMainTF)
	}
	if !strings.Contains(grant, "create_before_destroy = true") {
		t.Errorf("%s: cudly_wif must set create_before_destroy so the narrow member exists before the pool-wide one is removed", gcpTargetMainTF)
	}
}

// setupScript is the shell onboarding path. It is the second way a customer can
// configure the same pool and provider; both paths default to pool 'cudly-pool'
// and provider 'cudly-provider', so a customer who uses one and then the other
// lands on the same provider rather than on two independent ones.
const setupScript = "../arm/CUDly-CrossSubscription/setup-gcp-wif.sh"

// awsMappingBlock captures the body of the AWS branch of attribute_mapping in
// main.tf, i.e. everything between the ternary's opening brace and the OIDC
// alternative.
var awsMappingBlock = regexp.MustCompile(`(?s)attribute_mapping = var\.provider_type == "aws" \? \{(.*?)\n\s*\} : var\.oidc_attribute_mapping`)

// hclMappingPair matches one `"key" = "value"` entry inside that block.
var hclMappingPair = regexp.MustCompile(`(?m)^\s*"([^"]+)"\s*=\s*"(.*)"\s*$`)

// scriptMappingLiteral captures the quoted, comma-delimited mapping dict the
// script hands to gcloud's --attribute-mapping. It is anchored on the
// attribute.aws_role key so it selects the AWS dict and not the OIDC one.
var scriptMappingLiteral = regexp.MustCompile(`"([^"]*attribute\.aws_role=[^"]*)"`)

// tfAWSMapping returns main.tf's AWS attribute_mapping as sorted "key=value"
// entries, the form both sides are compared in.
func tfAWSMapping(t *testing.T) []string {
	t.Helper()

	block := awsMappingBlock.FindStringSubmatch(readMainTF(t))
	if block == nil {
		t.Fatalf("%s: AWS branch of attribute_mapping not found", gcpTargetMainTF)
	}
	pairs := hclMappingPair.FindAllStringSubmatch(block[1], -1)
	if len(pairs) == 0 {
		t.Fatalf("%s: AWS attribute_mapping has no entries", gcpTargetMainTF)
	}

	got := make([]string, 0, len(pairs))
	for _, p := range pairs {
		got = append(got, p[1]+"="+p[2])
	}
	slices.Sort(got)
	return got
}

// scriptAWSMapping returns the script's AWS attribute mapping as sorted
// "key=value" entries. Every literal carrying the mapping must agree, so the
// script cannot create a provider with one mapping and reconcile against
// another.
func scriptAWSMapping(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(setupScript)
	if err != nil {
		t.Fatalf("read %s: %v", setupScript, err)
	}
	literals := scriptMappingLiteral.FindAllStringSubmatch(string(raw), -1)
	if len(literals) == 0 {
		t.Fatalf("%s: no --attribute-mapping dict containing attribute.aws_role found", setupScript)
	}
	for _, l := range literals[1:] {
		if l[1] != literals[0][1] {
			t.Fatalf("%s: AWS attribute mapping is spelled two different ways:\n%s\n%s", setupScript, literals[0][1], l[1])
		}
	}

	got := strings.Split(literals[0][1], ",")
	slices.Sort(got)
	return got
}

// TestGCPTargetMappingMatchesSetupScript pins the two onboarding paths to the
// same attribute mapping.
//
// The script refuses to reuse a provider whose configuration differs from the
// one it would have written, so a mapping this module writes but the script
// does not expect is not a cosmetic divergence: a customer onboarded via the
// Terraform bundle who later runs the script against the same project is told
// to delete the provider and start over, which detaches every live federated
// session and which the next terraform apply then fights. An extra key does
// that even when nothing reads its value, so the whole set is compared here,
// not just the keys the attribute_condition and the impersonation grant consume.
func TestGCPTargetMappingMatchesSetupScript(t *testing.T) {
	tfMapping := tfAWSMapping(t)
	scriptMapping := scriptAWSMapping(t)

	if !slices.Equal(tfMapping, scriptMapping) {
		t.Errorf("AWS attribute mapping differs between the two onboarding paths; a provider created by one is unusable by the other.\n%s:\n  %s\n%s:\n  %s",
			gcpTargetMainTF, strings.Join(tfMapping, "\n  "),
			setupScript, strings.Join(scriptMapping, "\n  "))
	}
}

// extractTemplate models the extract() function available in GCP's workload
// identity attribute-mapping CEL. The template is a literal containing exactly
// one {placeholder}; the result is the text between the literal prefix and
// suffix, or "" when the template does not match.
//
// Google's own documented example is the expression under test:
// "arn:aws:sts::123456789012:assumed-role/Admin/85" extracts
// "arn:aws:sts::123456789012:" via '{account_arn}assumed-role/' and "Admin" via
// 'assumed-role/{role_name}/'.
func extractTemplate(s, tmpl string) string {
	open := strings.Index(tmpl, "{")
	closeIdx := strings.Index(tmpl, "}")
	if open < 0 || closeIdx < open {
		return ""
	}
	prefix, suffix := tmpl[:open], tmpl[closeIdx+1:]

	rest := s
	if prefix != "" {
		i := strings.Index(rest, prefix)
		if i < 0 {
			return ""
		}
		rest = rest[i+len(prefix):]
	}
	if suffix == "" {
		return rest
	}
	j := strings.Index(rest, suffix)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// normalizeAWSRoleAttr is the Go transcription of awsRoleAttributeMapping. It is
// the value attribute.aws_role carries at token-exchange time, i.e. the value
// the attribute condition and the impersonation grant both compare against.
func normalizeAWSRoleAttr(arn string) string {
	if !strings.Contains(arn, "assumed-role") {
		return arn
	}
	return extractTemplate(arn, "{account_arn}assumed-role/") +
		"assumed-role/" +
		extractTemplate(arn, "assumed-role/{role_name}/")
}

// oldConditionAdmits reproduces the condition this fix replaced:
// attribute.aws_role.contains('assumed-role/<role>/') applied to the raw ARN.
// Used to prove the exploit case below genuinely passed the previous gate.
func oldConditionAdmits(arn, roleName string) bool {
	return strings.Contains(arn, "assumed-role/"+roleName+"/")
}

func TestGCPTargetRejectsIAMUserPathBypass(t *testing.T) {
	// arn:aws:iam::<acct>:user/assumed-role/<pinned role>/<name> is reachable by
	// anyone holding iam:CreateUser in the trusted account: AWS's IAM path
	// grammar is (/)|(/[!-~]+/), so /assumed-role/CUDly-Execution/ is a legal
	// path and the resulting user ARN carries it. STS strips the path from
	// assumed-role ARNs, so the same trick does not work through an IAM role.
	exploit := "arn:aws:iam::" + testAccountID + ":user/assumed-role/" + testRoleName + "/evil"

	if !oldConditionAdmits(exploit, testRoleName) {
		t.Fatalf("test fixture is wrong: %q must satisfy the substring condition this fix replaced, or it does not reproduce the reported bypass", exploit)
	}
	if got := normalizeAWSRoleAttr(exploit); got == testPinnedRoleARN {
		t.Errorf("IAM user path bypass still admitted: normalised to %q, which equals the pinned %q", got, testPinnedRoleARN)
	}
}

func TestNormalizedAWSRoleAttrAdmitsOnlyThePinnedRole(t *testing.T) {
	tests := []struct {
		name  string
		arn   string
		admit bool
	}{
		{
			name:  "pinned role session",
			arn:   "arn:aws:sts::" + testAccountID + ":assumed-role/" + testRoleName + "/cudly-session",
			admit: true,
		},
		{
			// The session name varies per exchange; normalisation exists so the
			// same grant matches every session of the pinned role.
			name:  "pinned role, different session name",
			arn:   "arn:aws:sts::" + testAccountID + ":assumed-role/" + testRoleName + "/i-0abc123",
			admit: true,
		},
		{
			// Equality, not a prefix test: a longer sibling role must not inherit
			// the pinned role's trust.
			name:  "longer sibling role name",
			arn:   "arn:aws:sts::" + testAccountID + ":assumed-role/" + testRoleName + "-Admin/s",
			admit: false,
		},
		{
			name:  "IAM user crafted at path /assumed-role/<pinned role>/",
			arn:   "arn:aws:iam::" + testAccountID + ":user/assumed-role/" + testRoleName + "/evil",
			admit: false,
		},
		{
			// Two occurrences: whichever occurrence extract() binds to, the value
			// keeps the arn:aws:iam::<acct>:user/ prefix and cannot equal an
			// arn:aws:sts:: role ARN.
			name:  "IAM user path repeating the marker",
			arn:   "arn:aws:iam::" + testAccountID + ":user/assumed-role/" + testRoleName + "/assumed-role/" + testRoleName + "/evil",
			admit: false,
		},
		{
			name:  "plain IAM user",
			arn:   "arn:aws:iam::" + testAccountID + ":user/alice",
			admit: false,
		},
		{
			// Session name carrying the pinned role name.
			name:  "unrelated role with a crafted session name",
			arn:   "arn:aws:sts::" + testAccountID + ":assumed-role/Attacker/assumed-role-" + testRoleName,
			admit: false,
		},
		{
			name:  "pinned role in a different AWS account",
			arn:   "arn:aws:sts::210987654321:assumed-role/" + testRoleName + "/s",
			admit: false,
		},
		{
			// The pinned ARN is standard-partition; a GovCloud caller is refused
			// rather than admitted on a partition mismatch.
			name:  "pinned role in the GovCloud partition",
			arn:   "arn:aws-us-gov:sts::" + testAccountID + ":assumed-role/" + testRoleName + "/s",
			admit: false,
		},
		{
			name:  "root account principal",
			arn:   "arn:aws:iam::" + testAccountID + ":root",
			admit: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeAWSRoleAttr(tc.arn)
			if admitted := got == testPinnedRoleARN; admitted != tc.admit {
				t.Errorf("attribute.aws_role for %q normalised to %q; admitted=%v, want %v (pinned: %q)",
					tc.arn, got, admitted, tc.admit, testPinnedRoleARN)
			}
		})
	}
}
