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
package aws_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// grantRoots are walked recursively for policy sources. Relative to this
// package directory, terraform/modules/compute/aws — so these are all of
// terraform/ and all of cloudformation/stacks/.
//
// Deliberately the whole of terraform/ rather than this module: an
// unconditioned copy of the grant added to, say, modules/database/aws is
// exactly as dangerous as one added here, and a walk rooted at the module
// would call the tree clean without opening it. That is the same "only the
// files someone remembered" defect this guard exists to catch.
//
// iac/federation/** is outside these roots: it defines the TARGET account's
// trust policy, where the hub is the principal being trusted rather than the
// caller. An aws:ResourceAccount condition is meaningless there. Trust
// policies that DO fall inside the roots (cloudformation/stacks/
// CUDly-CrossAccount, the service-principal trusts in modules/networking and
// modules/database) are excluded by classification, not by path.
// Repo-relative, resolved against repoRoot, so a failure message names a path
// the reader can open rather than a chain of "..".
var grantRoots = []string{
	"terraform",
	filepath.Join("cloudformation", "stacks"),
}

// repoRoot is four levels up from this package (aws -> compute -> modules ->
// terraform -> root).
func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	return root
}

// skippedGrantFiles hold an sts:AssumeRole that is not a grant. policy_boundary.tf
// defines the permissions BOUNDARY: a ceiling capping what a role may be granted
// rather than granting anything, so pinning it to account IDs would cap the
// modules below their own grant and 403 at runtime. It has its own suite
// (ci-cd-permissions/policy_guard_test.go, TestBoundaryMatchesCrossAccountRolePrefix).
//
// By file rather than by directory: skipping all of ci-cd-permissions would
// exempt every other policy in it, and an unconditioned grant dropped beside the
// boundary is exactly as dangerous as one dropped anywhere else.
// Repo-relative PATHS, not basenames. A basename match exempts every file with
// that name anywhere in the walk -- an earlier version matched the directory
// name `ci-cd-permissions` and so skipped all three of
// environments/{aws,azure,gcp}, plus any future directory that happened to
// share the name. The sibling suite would not have caught an unconditioned
// grant dropped in there either: policy_guard_test.go asserts on the
// CrossAccountAssumeRoleCeiling Sid, not on how many statements exist.
var skippedGrantFiles = []string{
	filepath.Join("terraform", "environments", "aws", "ci-cd-permissions", "policy_boundary.tf"),
}

var grantExtensions = map[string]bool{".tf": true, ".yaml": true, ".yml": true}

// knownGrantSites must all be found by the walk. Not the guard's input (the
// walk is), but its floor: if a refactor moves or renames a file, the walk
// silently inspects two sites instead of three and still reports "no
// violations", which is how a sweep passes by looking at nothing.
var knownGrantSites = []string{
	filepath.Join("terraform", "modules", "compute", "aws", "lambda", "main.tf"),
	filepath.Join("terraform", "modules", "compute", "aws", "fargate", "main.tf"),
	filepath.Join("cloudformation", "stacks", "CUDly", "template.yaml"),
}

// assumeRoleActionPattern matches sts:AssumeRole where it is an ACTION VALUE
// rather than prose. Every variable description in these modules names the
// action in running text, and a guard that fires on those constrains what may
// be written about the grant instead of what the grant does. The three
// alternatives are the only three forms the sources use: a quoted HCL string,
// a YAML list item, and a YAML scalar after Action:.
var assumeRoleActionPattern = regexp.MustCompile(`(?m)"sts:AssumeRole"|^\s*-\s+sts:AssumeRole\s*$|Action:\s+sts:AssumeRole\s*$`)

// trustMarkers and identityMarkers classify an sts:AssumeRole action by
// whichever marker most recently precedes it. A trust policy names the
// principal allowed to assume the role it is attached to; an identity policy
// names the roles its holder may assume. Only the second kind can carry an
// aws:ResourceAccount condition, because only there is the role the resource.
//
// Regexes rather than substrings, and this is not decoration: "PolicyDocument"
// is a SUBSTRING of "AssumeRolePolicyDocument", so a substring rule reads every
// YAML trust policy as an identity policy and demands a condition that cannot
// exist there. TestGrantClassifierSeparatesTrustFromIdentityPolicies caught
// exactly that. The identity form therefore requires a non-letter before the
// key; RE2 has no lookbehind, so it is written as an alternation.
var (
	trustMarkers = []*regexp.Regexp{
		regexp.MustCompile(`assume_role_policy\s*=`),
		regexp.MustCompile(`AssumeRolePolicyDocument\s*:`),
	}
	identityMarkers = []*regexp.Regexp{
		regexp.MustCompile(`resource "aws_iam_role_policy"`),
		regexp.MustCompile(`resource "aws_iam_policy"`),
		// An identity policy does not have to be its own resource.
		// inline_policy was invisible to an earlier version of this list: a
		// grant written that way classified as trust, because the nearest
		// preceding marker was the assume_role_policy above it in the same
		// aws_iam_role block.
		regexp.MustCompile(`inline_policy\s*\{`),
		regexp.MustCompile(`(?:^|[^A-Za-z])PolicyDocument\s*:`),
		regexp.MustCompile(`AWS::IAM::ManagedPolicy`),
	}
)

// KNOWN GAP, stated rather than papered over: `data "aws_iam_policy_document"`
// is not an identity marker, so an identity grant written that way is skipped
// unless some other identity marker happens to precede it in the same file.
// The marker was briefly on the list above and had to come off, because the
// same block is ALSO the canonical way to write a trust policy:
//
//	data "aws_iam_policy_document" "assume_role" {
//	  statement {
//	    actions    = ["sts:AssumeRole"]
//	    principals { type = "Service" ... }
//	  }
//	}
//
// Telling the two apart means finding whether a principals block exists inside
// the same statement, which most-recent-marker-wins cannot do: the principals
// block follows the actions line rather than preceding it. Doing it properly
// needs a real HCL parse, and a guard that demanded aws:ResourceAccount on
// every service-principal trust policy in the repo would be removed within a
// week. Leaving it off costs a missed grant written that way; leaving it on
// cost false failures on correct files.

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
//     green. Hence stripCommentLines below.
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
	// `#` for both languages, `//` because HCL has it too: an earlier version
	// stripped only `#`, so deleting both real conditions and leaving them
	// behind as `//` comments passed.
	//
	// Whole-line only, and there is deliberately NO /* */ pass. One was added
	// and removed: `(?s)/\*.*?\*/` reads the `/*` in an ARN glob such as
	// "arn:aws:s3:::bucket/*" as an opening delimiter and eats everything up to
	// the next `*/` in an unrelated string. In an IAM policy file that silently
	// swallowed the real conditions and left the guard reporting clean. A
	// comment stripper that can blind the guard is worse than the HCL block
	// comments it was meant to catch, of which the policy sources have none.
	commentLinePattern = regexp.MustCompile(`(?m)^[ \t]*(?:#|//).*$`)
)

// stripCommentLines blanks whole-line `#` and `//` comments. Whole-line only: a
// `#` or `//` inside a quoted string is content in both HCL and YAML, and
// removing it would corrupt the very values these assertions read.
func stripCommentLines(content string) string {
	return commentLinePattern.ReplaceAllString(content, "")
}

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

// identityGrantSites walks grantRoots and returns every file holding at least
// one identity-policy sts:AssumeRole grant, keyed by path. The stored value is
// the file with whole-line comments removed, so no assertion downstream can be
// satisfied by a comment that merely describes the grant.
func identityGrantSites(t *testing.T) map[string]string {
	t.Helper()

	root := repoRoot(t)
	sites := map[string]string{}
	inspected := 0
	for _, rel := range grantRoots {
		walkPolicyFiles(t, root, filepath.Join(root, rel), func(path, content string) {
			inspected++
			code := stripCommentLines(content)
			if countIdentityGrants(code) > 0 {
				sites[path] = code
			}
		})
	}
	if inspected == 0 {
		t.Fatalf("walked %v and opened zero policy files; every assertion below would pass by inspecting nothing", grantRoots)
	}
	return sites
}

// walkPolicyFiles calls visit for every file under dir with a policy-source
// extension, passing the path relative to root. Discovered via filepath.WalkDir
// rather than a glob so a grant added at any depth is reached.
func walkPolicyFiles(t *testing.T, root, dir string, visit func(path, content string)) {
	t.Helper()

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !grantExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		for _, skip := range skippedGrantFiles {
			if rel == skip {
				return nil
			}
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		visit(rel, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
}

// countIdentityGrants counts sts:AssumeRole actions that classify as
// identity-policy grants.
func countIdentityGrants(content string) int {
	count := 0
	for _, loc := range assumeRoleActionPattern.FindAllStringIndex(content, -1) {
		if !isTrustGrant(content, loc[0]) {
			count++
		}
	}
	return count
}

// isTrustGrant reports whether the action at idx sits in a trust policy,
// decided by which marker class most recently precedes it.
//
// With NO identity marker preceding it, the answer is trust. That default is
// the safe direction and it is load-bearing: a standalone file holding only a
// service-principal trust policy has no marker of either class, and the
// opposite default made the guard demand aws:ResourceAccount on a correct file.
// A guard that fires on correct code gets deleted; the cost of this default is
// a grant written in a form the identity markers do not recognise, which is the
// gap already stated above.
func isTrustGrant(content string, idx int) bool {
	before := content[:idx]
	identity := lastMarkerIndex(before, identityMarkers)
	return identity == -1 || lastMarkerIndex(before, trustMarkers) > identity
}

func lastMarkerIndex(content string, markers []*regexp.Regexp) int {
	last := -1
	for _, marker := range markers {
		all := marker.FindAllStringIndex(content, -1)
		if len(all) == 0 {
			continue
		}
		if i := all[len(all)-1][0]; i > last {
			last = i
		}
	}
	return last
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

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
