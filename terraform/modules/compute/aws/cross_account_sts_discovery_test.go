// Discovery and classification for the #1636 cross-account sts:AssumeRole
// guard: how the policy sites are found, and how a grant is told apart from a
// trust policy. The assertions these feed live in
// cross_account_sts_guard_test.go, which is also where the guard's purpose is
// written down.
package aws_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// grantRoots are walked recursively for policy sources. Repo-relative, resolved
// against repoRoot, so a failure message names a path the reader can open
// rather than a chain of "..".
//
// Deliberately the whole of terraform/ rather than just this module: an
// unconditioned copy of the grant added to, say, modules/database/aws is
// exactly as dangerous as one added here, and a walk rooted at the module would
// call the tree clean without opening it. That is the same "only the files
// someone remembered" defect this guard exists to catch.
//
// iac/federation/** is outside these roots: it defines the TARGET account's
// trust policy, where the hub is the principal being trusted rather than the
// caller. An aws:ResourceAccount condition is meaningless there. Trust policies
// that DO fall inside the roots (cloudformation/stacks/CUDly-CrossAccount, the
// service-principal trusts in modules/networking and modules/database) are
// excluded by classification, not by path.
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

// skippedGrantFiles hold an sts:AssumeRole that is not a grant.
// policy_boundary.tf defines the permissions BOUNDARY: a ceiling capping what a
// role may be granted rather than granting anything, so pinning it to account
// IDs would cap the modules below their own grant and 403 at runtime. It has
// its own suite (ci-cd-permissions/policy_guard_test.go,
// TestBoundaryMatchesCrossAccountRolePrefix).
//
// Repo-relative PATHS, not basenames. A basename match exempts every file with
// that name anywhere in the walk, and an earlier version matched the DIRECTORY
// name `ci-cd-permissions`, so it skipped all three of
// environments/{aws,azure,gcp} plus any future directory sharing the name. The
// sibling suite would not have caught an unconditioned grant dropped in there
// either: policy_guard_test.go asserts on the CrossAccountAssumeRoleCeiling
// Sid, not on how many statements exist.
var skippedGrantFiles = []string{
	filepath.Join("terraform", "environments", "aws", "ci-cd-permissions", "policy_boundary.tf"),
}

var grantExtensions = map[string]bool{".tf": true, ".yaml": true, ".yml": true}

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

// `#` for both languages, `//` because HCL has it too: an earlier version
// stripped only `#`, so deleting both real conditions and leaving them behind
// as `//` comments passed.
//
// Whole-line only, and there is deliberately NO /* */ pass. One was added and
// removed: `(?s)/\*.*?\*/` reads the `/*` in an ARN glob such as
// "arn:aws:s3:::bucket/*" as an opening delimiter and eats everything up to the
// next `*/` in an unrelated string. In an IAM policy file that silently
// swallowed the real conditions and left the guard reporting clean. A comment
// stripper that can blind the guard is worse than the HCL block comments it was
// meant to catch, of which the policy sources have none.
var commentLinePattern = regexp.MustCompile(`(?m)^[ \t]*(?:#|//).*$`)

// stripCommentLines blanks whole-line `#` and `//` comments. Whole-line only: a
// `#` or `//` inside a quoted string is content in both HCL and YAML, and
// removing it would corrupt the very values these assertions read.
func stripCommentLines(content string) string {
	return commentLinePattern.ReplaceAllString(content, "")
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
// a grant written in a form the identity markers do not recognize, which is the
// gap stated above.
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
