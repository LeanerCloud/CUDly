// Guards the class of defect behind #1967 and #1968: an AWS action the
// application calls under the runtime role that no runtime IaC flavor grants.
// check-aws-iam-parity.sh cannot see this class because it only compares the
// three runtime flavors with each other, so a gap present in all three passes.
// This derives the called-action set straight from the SDK request structs
// under providers/aws and internal and asserts each flavor grants every one.
package aws_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// sdkServiceToIAMPrefix maps an aws-sdk-go-v2 service package name to the IAM
// action prefix it authorizes against. Two services rename: Cost Explorer's
// package is costexplorer but its actions are ce:*, and OpenSearch's package
// is opensearch but its actions (for historical reasons) are es:*. sts is
// deliberately absent: GetCallerIdentity requires no IAM permission at all,
// so a call to it must never enter the derived set.
var sdkServiceToIAMPrefix = map[string]string{
	"costexplorer":  "ce",
	"ec2":           "ec2",
	"rds":           "rds",
	"elasticache":   "elasticache",
	"opensearch":    "es",
	"redshift":      "redshift",
	"memorydb":      "memorydb",
	"savingsplans":  "savingsplans",
	"organizations": "organizations",
}

// requiredDerivedActions is the floor from #1967/#1968: actions the code is
// known to call that no runtime flavor grants today. Asserting the scanner
// finds these (independent of whether any file grants them) catches a broken
// walker directly, rather than letting it pass by inspecting nothing.
var requiredDerivedActions = []string{
	"ce:GetCostAndUsage",
	"ec2:CreateReservedInstancesListing",
	"ec2:DescribeReservedInstancesListings",
	"ec2:CancelReservedInstancesListing",
	"ec2:DescribeInstanceTypes",
	"ec2:CreateTags",
	"redshift:DescribeTags",
	"redshift:CreateTags",
	"es:AddTags",
}

// scanRoots are walked, relative to the repo root, for SDK call sites. Not
// cmd/: the CLI runs under operator credentials, a different identity than
// the runtime role this test guards (organizations:DescribeAccount is
// CLI-only for exactly this reason, per #1322).
var scanRoots = []string{
	filepath.Join("providers", "aws"),
	"internal",
}

// runtimeIAMFiles are the IaC files that grant the runtime role's IAM
// actions, relative to this package's directory (the three flavors compared
// by check-aws-iam-parity.sh comparison 1).
var runtimeIAMFiles = []string{
	filepath.Join("lambda", "main.tf"),
	filepath.Join("fargate", "main.tf"),
	filepath.Join("..", "..", "..", "..", "cloudformation", "stacks", "CUDly", "template.yaml"),
}

// calledAction records where a derived action was first seen, so a failure
// message names a place the reader can open.
type calledAction struct {
	file string
	line int
}

func TestRuntimeGrantsEveryCalledAction(t *testing.T) {
	called := deriveCalledActions(t)

	for _, action := range requiredDerivedActions {
		if _, ok := called[action]; !ok {
			t.Errorf("scanner did not derive %s from %v; it is a known SDK call the runtime role must be granted (#1967/#1968) and its absence here means the walker is broken, not that the call went away", action, scanRoots)
		}
	}

	for _, rel := range runtimeIAMFiles {
		t.Run(rel, func(t *testing.T) {
			granted := grantedActions(t, rel)
			for action, site := range called {
				if !granted[action] {
					t.Errorf("%s does not grant %s, called at %s:%d", rel, action, site.file, site.line)
				}
			}
		})
	}
}

// deriveCalledActions walks scanRoots and returns every IAM action implied by
// an SDK *Input request literal, keyed by action.
func deriveCalledActions(t *testing.T) map[string]calledAction {
	t.Helper()

	root := repoRoot(t)
	fset := token.NewFileSet()
	actions := map[string]calledAction{}
	filesScanned := 0

	for _, rel := range scanRoots {
		dir := filepath.Join(root, rel)
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			filesScanned++
			return scanFileForActions(t, fset, root, path, actions)
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	if filesScanned == 0 {
		t.Fatalf("walked %v and parsed zero Go files; every assertion below would pass by inspecting nothing", scanRoots)
	}
	if len(actions) == 0 {
		t.Fatalf("derived zero SDK actions from %v; a broken import or literal match would pass every assertion below by inspecting nothing", scanRoots)
	}
	return actions
}

// scanFileForActions parses one Go file and records every action its SDK
// *Input literals imply into actions, keyed by action so the first call site
// wins.
func scanFileForActions(t *testing.T, fset *token.FileSet, root, path string, actions map[string]calledAction) error {
	t.Helper()

	file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if parseErr != nil {
		t.Fatalf("parsing %s: %v", path, parseErr)
	}

	aliasToPrefix := sdkImportAliases(file)
	if len(aliasToPrefix) == 0 {
		return nil
	}

	relPath, relErr := filepath.Rel(root, path)
	if relErr != nil {
		return relErr
	}

	ast.Inspect(file, func(n ast.Node) bool {
		action, ok := actionFromCompositeLit(n, aliasToPrefix)
		if !ok {
			return true
		}
		if _, exists := actions[action]; exists {
			return true
		}
		actions[action] = calledAction{file: relPath, line: fset.Position(n.Pos()).Line}
		return true
	})
	return nil
}

// actionFromCompositeLit reports the IAM action implied by n, if n is a
// composite literal of an SDK *Input request type whose package alias
// resolves through aliasToPrefix (e.g. &ec2.CreateTagsInput{...} -> "ec2:CreateTags").
func actionFromCompositeLit(n ast.Node, aliasToPrefix map[string]string) (string, bool) {
	cl, ok := n.(*ast.CompositeLit)
	if !ok {
		return "", false
	}
	sel, ok := cl.Type.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	prefix, ok := aliasToPrefix[ident.Name]
	if !ok {
		return "", false
	}
	typeName := sel.Sel.Name
	op := strings.TrimSuffix(typeName, "Input")
	if op == "" || op == typeName {
		return "", false
	}
	return prefix + ":" + op, true
}

// sdkImportAliases returns, for one file, the map from the local identifier
// an aws-sdk-go-v2 service package is used under to the IAM prefix it
// authorizes against. A subpackage import such as .../service/ec2/types is
// deliberately excluded: it defines the enum and shape types the *Input
// structs embed, not the *Input structs themselves, so its alias must never
// stand in for the service package's.
func sdkImportAliases(file *ast.File) map[string]string {
	const svcPrefix = "github.com/aws/aws-sdk-go-v2/service/"

	aliases := map[string]string{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		rest := strings.TrimPrefix(path, svcPrefix)
		if rest == path || strings.Contains(rest, "/") {
			continue
		}
		prefix, ok := sdkServiceToIAMPrefix[rest]
		if !ok {
			continue
		}
		alias := rest
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				continue
			}
			alias = imp.Name.Name
		}
		aliases[alias] = prefix
	}
	return aliases
}

// grantedActionPattern is check-aws-iam-parity.sh's extraction regex,
// rewritten with a capture group so the match includes only the action
// itself, not the boundary byte before it.
var grantedActionPattern = regexp.MustCompile(`(?:^|[^A-Za-z])((?:ce|ec2|rds|elasticache|es|redshift|memorydb|savingsplans|organizations):[A-Z][A-Za-z]+)`)

// hashCommentLinePattern matches a whole line whose first non-blank byte is
// #, the comment style both Terraform and CloudFormation YAML use here.
var hashCommentLinePattern = regexp.MustCompile(`(?m)^[ \t]*#.*$`)

// grantedActions reads path (relative to this package's directory, matching
// how go test sets its working directory) and returns the set of IAM actions
// it grants. Comment lines are stripped first, so a comment naming an action
// cannot satisfy the guard.
func grantedActions(t *testing.T, path string) map[string]bool {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	content := hashCommentLinePattern.ReplaceAllString(string(data), "")

	granted := map[string]bool{}
	for _, m := range grantedActionPattern.FindAllStringSubmatch(content, -1) {
		granted[m[1]] = true
	}
	return granted
}
