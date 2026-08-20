// Guard for issue #1636: the hub's cross-account sts:AssumeRole grant must be
// pinned to account IDs the operator declared, not to a role-name prefix alone.
//
// arn:aws:iam::*:role/CUDly* narrows WHICH role but never WHOSE account, and
// the customer-side templates default the role name to literally "CUDly", so
// the pattern matched every onboarded account by construction and every
// un-onboarded one as well. A mis-selected CloudAccount row therefore produced
// a successful AssumeRole rather than an AccessDenied, and the first sign of
// the defect was an irreversible purchase in the wrong account. The
// sts:ExternalId StringLike "*" condition cannot cover for this: the role ARN
// and the external ID are read off the same record, so a wrong selection
// supplies both consistently.
//
// The grant lives in three independently maintained copies (two Terraform
// modules and the shipped CloudFormation hub), which is why this guard
// DISCOVERS them by walking the tree instead of opening a list of three paths.
// A list of paths is the same defect one level up: a fourth copy added later is
// invisible to a sweep that only opens the files someone remembered to name.
//
// The walking and the trust/identity classification live in
// cross_account_sts_discovery_test.go; this file holds what is asserted about
// what they find.
package aws_test

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// knownGrantSites must all be found by the walk. Not the guard's input (the
// walk is), but its floor: if a refactor moves or renames a file, the walk
// silently inspects two sites instead of three and still reports "no
// violations", which is how a sweep passes by looking at nothing.
var knownGrantSites = []string{
	filepath.Join("terraform", "modules", "compute", "aws", "lambda", "main.tf"),
	filepath.Join("terraform", "modules", "compute", "aws", "fargate", "main.tf"),
	filepath.Join("cloudformation", "stacks", "CUDly", "template.yaml"),
}

// accountConditionKey pins the grant to declared accounts; externalIDCondition
// requires sts:ExternalId to be present at all. Both must survive: they close
// different gaps and neither substitutes for the other.
const (
	accountConditionKey = "aws:ResourceAccount"
	externalIDCondition = "sts:ExternalId"
)

// accountSourceRefs are the config inputs an aws:ResourceAccount value must
// come from. A literal list of account IDs in the policy would be equally safe
// at IAM; it is rejected because it drifts, and the drift is silent in the
// direction that matters -- an account removed from the deployment's config
// stays reachable by a policy nobody re-read.
var accountSourceRefs = []string{"cross_account_target_account_ids", "CrossAccountTargetAccountIds"}

// Every check below matches the condition where it is ASSIGNED a value, not
// merely where the key appears. Both halves of that are load-bearing, and every
// one of them was established by mutating this tree rather than reasoned about:
//
//   - Prose satisfies a bare containment check. The block comments in
//     lambda/main.tf explain what aws:ResourceAccount and sts:ExternalId do, so
//     deleting the actual conditions left the words behind and the guard stayed
//     green. Hence stripCommentLines, in the discovery file.
//   - Comment stripping alone is not enough. The precondition's error_message
//     names cross_account_target_account_ids in running text, and an
//     error_message is code, not a comment, so hardcoding the account list
//     still read as "sourced from config". Hence the assignment anchors.
//   - The KEY being pinned to the right value says nothing about the OPERATOR
//     it is pinned under. Swapping StringEquals for StringLike passed an
//     earlier version of this guard, and under StringLike a value like "1111*"
//     matches most of AWS. Hence accountConditionOperator.
var (
	// Requires StringEquals, or ForAnyValue:StringEquals, to be the operator
	// immediately enclosing aws:ResourceAccount. Nothing may intervene but
	// whitespace and the opening brace.
	//
	// The leading [^:\w] is what rejects ForAllValues:StringEquals, which looks
	// like a harmless spelling and is not: for a single-valued key such as
	// aws:ResourceAccount, ForAllValues evaluates TRUE when the key is absent
	// from the request, so it turns the pin into a no-op. RE2 has no lookbehind,
	// so "not preceded by a colon" is written as a character class.
	accountConditionOperator = regexp.MustCompile(
		`(?:^|[^:\w])(?:ForAnyValue:)?StringEquals"?\s*[:=]\s*\{?\s*"?aws:ResourceAccount`)
	externalIDAssigned       = regexp.MustCompile(`sts:ExternalId"?\s*[:=]`)
	accountSourcedFromConfig = regexp.MustCompile(
		`aws:ResourceAccount"?\s*[:=]\s*(?:Ref:\s*)?(?:var\.)?(?:` +
			strings.Join(accountSourceRefs, "|") + `)`)
)

// conditionProximityWindow bounds how far after the sts:AssumeRole action the
// conditions are looked for. Every site puts Condition within ~250 bytes of
// Action, so the window is generous.
//
// It exists because the assertions used to run against the whole file, and a
// condition written ANYWHERE in it counted: moving the StringEquals block into
// a decoy `locals` block left the real grant unconditioned with the guard
// green. This is a heuristic, not a parse. The honest alternative is an HCL
// parser and a YAML parser for a check whose whole value is being cheap enough
// to live in the module's own test package. It assumes Condition follows
// Action, which is true at all three sites and is the conventional ordering.
const conditionProximityWindow = 800

// identityGrantRegion returns the slice of code running from the file's first
// identity-policy sts:AssumeRole action to conditionProximityWindow bytes later.
// Callers have already asserted there is exactly one such action.
func identityGrantRegion(code string) string {
	for _, loc := range assumeRoleActionPattern.FindAllStringIndex(code, -1) {
		if isTrustGrant(code, loc[0]) {
			continue
		}
		end := min(loc[0]+conditionProximityWindow, len(code))
		return code[loc[0]:end]
	}
	return ""
}

func TestCrossAccountAssumeRoleIsPinnedToDeclaredAccounts(t *testing.T) {
	sites := identityGrantSites(t)

	for _, want := range knownGrantSites {
		if _, ok := sites[want]; !ok {
			t.Fatalf("%s grants no identity-policy sts:AssumeRole, but it is one of the three sites this guard exists to cover. Either the grant moved (point knownGrantSites at its new home) or it was deleted; a walk that no longer reaches it reports a clean result for a file it never opened. Found: %v", want, sortedKeys(sites))
		}
	}

	for path, code := range sites {
		t.Run(path, func(t *testing.T) {
			assertGrantPinned(t, path, code)
		})
	}
}

// assertGrantPinned takes code with comments already stripped by
// identityGrantSites.
func assertGrantPinned(t *testing.T, path, code string) {
	t.Helper()

	// Exactly one, because identityGrantRegion below inspects the FIRST
	// identity grant in the file and nothing else. This bounds the number of
	// sts:AssumeRole actions; it does not by itself prove the conditions found
	// belong to that statement -- that is what the region scoping does.
	if n := countIdentityGrants(code); n != 1 {
		t.Fatalf("%s holds %d identity-policy sts:AssumeRole grants; only the first is inspected, so the others would go unchecked. Split the file or teach the guard to read statements", path, n)
	}
	// Scoped to the grant's own region, not the whole file: a condition written
	// elsewhere in the document must not stand in for the one on this statement.
	region := identityGrantRegion(code)

	if !accountConditionOperator.MatchString(region) {
		t.Fatalf("%s grants sts:AssumeRole with no StringEquals %s condition on the statement itself. The Resource pattern is not a restriction on the account (see TestAssumeRoleResourcePatternDoesNotRestrictTheAccount), so without this condition the grant reaches a CUDly* role in ANY AWS account and a mis-selected account produces a purchase instead of an AccessDenied (#1636). The operator has to be StringEquals: StringLike would let \"1111*\" match most of AWS, and ForAllValues:StringEquals is TRUE when the key is absent", path, accountConditionKey)
	}
	if !accountSourcedFromConfig.MatchString(region) {
		t.Errorf("%s sets %s but not from %v. A hardcoded list drifts from the accounts the deployment actually declares, and the drift is silent in the direction that matters: an account added to the app but not to the policy fails, an account removed from the app stays reachable", path, accountConditionKey, accountSourceRefs)
	}
	if !externalIDAssigned.MatchString(region) {
		t.Errorf("%s dropped the %s condition. It closes a different gap from %s -- an app-layer bug that omits the external ID entirely -- and the two do not substitute for each other", path, externalIDCondition, accountConditionKey)
	}
}

// There is deliberately no "reject a wildcard account ID" assertion here.
// Under StringEquals a "*" is the literal string "*", which no account ID is,
// so a wildcard denies everything rather than allowing everything -- it fails
// closed, which is not the direction this guard defends. Format is already
// enforced where an operator can actually get it wrong: the variable
// validation blocks in {lambda,fargate}/variables.tf and the AllowedPattern on
// the CloudFormation parameter. Asserting it a third time here bought a
// fragile regex protecting an unreachable bypass.

// TestAssumeRoleResourcePatternDoesNotRestrictTheAccount pins WHY the
// condition above is load-bearing, by evaluating the Resource pattern the
// grant still uses against an account that was never declared. If a later
// change pins the account in the ARN itself this test fails, which is the
// signal to revisit the condition rather than to keep both.
func TestAssumeRoleResourcePatternDoesNotRestrictTheAccount(t *testing.T) {
	const (
		pattern      = "arn:aws:iam::*:role/CUDly*"
		undeclaredID = "999999999999"
	)
	undeclared := fmt.Sprintf("arn:aws:iam::%s:role/CUDly", undeclaredID)

	matcher := arnPatternMatcher(t, pattern)
	if !matcher.MatchString(undeclared) {
		t.Fatalf("%q no longer matches %q. The Resource now constrains the account on its own; re-derive what the %s condition is still buying before leaving both in place", pattern, undeclared, accountConditionKey)
	}

	// The same pattern is what both Terraform modules render, so the reader is
	// not taking the constant above on trust.
	// Selected by extension rather than by slice position: the Terraform sites
	// are the ones that render this exact interpolated string, and a [:2] slice
	// would turn a reordering of knownGrantSites into a false failure.
	const rendered = `"arn:aws:iam::*:role/${var.cross_account_role_name_prefix}*"`
	root := repoRoot(t)
	checked := 0
	for _, rel := range knownGrantSites {
		if filepath.Ext(rel) != ".tf" {
			continue
		}
		checked++
		if !strings.Contains(readFile(t, filepath.Join(root, rel)), rendered) {
			t.Errorf("%s no longer renders the account-wildcard Resource this test evaluates; update the pattern constant so the demonstration keeps matching the deployed policy", rel)
		}
	}
	if checked == 0 {
		t.Fatalf("no .tf entry in knownGrantSites (%v), so the demonstration above was cross-checked against nothing", knownGrantSites)
	}
}

// arnPatternMatcher compiles an IAM resource pattern into the regexp IAM
// evaluates it as: `*` matches any sequence, `?` any single character, and
// neither stops at a `/`, which is why a path segment can carry a name prefix.
func arnPatternMatcher(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()

	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")

	re, err := regexp.Compile(b.String())
	if err != nil {
		t.Fatalf("compiling ARN pattern %q: %v", pattern, err)
	}
	return re
}

// TestGrantClassifierSeparatesTrustFromIdentityPolicies fixtures the rule the
// walk depends on. Without it, a classifier that called everything a trust
// policy would make every assertion above vacuous while still reporting green.
func TestGrantClassifierSeparatesTrustFromIdentityPolicies(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		wantCount int
	}{
		{"hcl trust policy", "resource \"aws_iam_role\" \"x\" {\n  assume_role_policy = jsonencode({\n    Action = \"sts:AssumeRole\"\n  })\n}\n", 0},
		{"hcl identity policy", "resource \"aws_iam_role_policy\" \"x\" {\n  policy = jsonencode({\n    Action = [\"sts:AssumeRole\"]\n  })\n}\n", 1},
		{"yaml trust policy", "  AssumeRolePolicyDocument:\n    Statement:\n      - Action: sts:AssumeRole\n", 0},
		{"yaml identity policy", "  Policies:\n    - PolicyDocument:\n        Statement:\n          - Action:\n              - sts:AssumeRole\n", 1},
		{"trust then identity in one file", "resource \"aws_iam_role\" \"x\" {\n  assume_role_policy = \"sts:AssumeRole\"\n}\nresource \"aws_iam_role_policy\" \"y\" {\n  policy = \"sts:AssumeRole\"\n}\n", 1},
		// The canonical service-principal trust policy, in a file of its own so
		// no marker of either class precedes the action. Must classify as trust:
		// an aws:ResourceAccount condition is meaningless on it, and demanding
		// one is a false failure on correct code.
		{"standalone policy document with no marker", "data \"aws_iam_policy_document\" \"assume_role\" {\n  statement {\n    actions = [\"sts:AssumeRole\"]\n    principals {\n      type        = \"Service\"\n      identifiers = [\"lambda.amazonaws.com\"]\n    }\n  }\n}\n", 0},
		{"identity policy still wins over a preceding trust one", "resource \"aws_iam_role\" \"x\" {\n  assume_role_policy = \"y\"\n}\nresource \"aws_iam_role_policy\" \"z\" {\n  policy = jsonencode({ Action = [\"sts:AssumeRole\"] })\n}\n", 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countIdentityGrants(tc.content); got != tc.wantCount {
				t.Errorf("countIdentityGrants() = %d, want %d", got, tc.wantCount)
			}
		})
	}
}
