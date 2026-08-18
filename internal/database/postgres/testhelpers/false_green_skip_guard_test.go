package testhelpers

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// This guard is a tripwire against the defect behind #1597, not a proof of its
// absence. It catches the direct shapes, which is what raises the cost of
// reintroducing the defect; it does not close the class.
//
// The rule is not "no skips". It is that a skip predicate must test the
// precondition it claims to. Docker being absent is probed directly by
// RequirePostgresContainer and is the only thing allowed to skip; whether a
// container came up and whether migrations applied are results, not
// preconditions, so an error from either has to fail.
//
// Parse-only, so it needs no build tag and still sees the files behind
// //go:build integration that a default `go test` never compiles. Having no
// type information is what buys that, and it is paid for in shapes this
// implementation does not see. They are deliberately not listed here in prose:
// TestFalseGreenSkipGuardKnownBypasses executes each one, so what this guard
// misses cannot quietly drift away from what the comment claims it misses.
//
// It finds skips, not every way to report a false green: `if err != nil {
// t.Logf(...); return }` reports PASS, which is worse, but it is
// indistinguishable from an ordinary early return without type information.

// guardedCalls are the calls whose error must never be answered with a skip,
// mapped to why.
var guardedCalls = map[string]string{
	"SetupPostgresContainer": "a container that will not start while Docker is healthy is a " +
		"real failure; use testhelpers.RequirePostgresContainer, which probes Docker itself " +
		"and skips only on that",
	"RunMigrations":      "migrating is the thing under test; a migration that will not apply is a broken schema and must fail",
	"MigrateToVersion":   "migrating is the thing under test; a migration that will not apply is a broken schema and must fail",
	"RollbackMigrations": "rolling back is the thing under test; a down migration that will not apply is a broken schema and must fail",
}

// skipFuncs are the *testing.T methods that end a test without a verdict.
var skipFuncs = map[string]bool{"Skip": true, "Skipf": true, "SkipNow": true}

type finding struct {
	pos       token.Position
	call      string
	skipFn    string
	rationale string
}

func (f finding) String() string {
	return fmt.Sprintf("%s: t.%s() answers an error from %s -- %s", f.pos, f.skipFn, f.call, f.rationale)
}

func TestNoSkipOnDatabaseSetupFailure(t *testing.T) {
	root := repoRootDir(t)

	fset := token.NewFileSet()
	var findings []finding
	parsed := 0
	calls := map[string]int{}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", ".terraform":
				return filepath.SkipDir
			}
			// Anchored, unlike the names above: it prunes the TypeScript tree
			// at the repo root, and must not silently swallow a Go package
			// that happens to be called frontend.
			if path == filepath.Join(root, "frontend") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isGuardedSource(path) {
			return nil
		}
		// A file that does not parse is the compiler's problem, not this
		// guard's: a package that does not compile has no passing tests to
		// falsely report green either. It is counted only once it has actually
		// been inspected, so the total below cannot overstate the coverage.
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}
		parsed++
		perName, fs := inspectFile(fset, f)
		for name, n := range perName {
			calls[name] += n
		}
		findings = append(findings, fs...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	// "Found nothing" and "looked at nothing" are the same color on a terminal,
	// and telling them apart is precisely what this guard is about. The floor is
	// per name, not a total: a total stays comfortably non-zero while an excluded
	// directory or one broken name silently drops that name's findings to zero.
	var missing []string
	for name := range guardedCalls {
		if calls[name] == 0 {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("guard saw no call to %s anywhere: its file discovery, parsing or name "+
			"matching has regressed and findings for it would silently be zero. If the call "+
			"is genuinely gone from the repo, drop it from guardedCalls deliberately.",
			strings.Join(missing, ", "))
	}
	t.Logf("found %v across %d parsed file(s)", calls, parsed)

	if len(findings) > 0 {
		msgs := make([]string, 0, len(findings))
		for _, f := range findings {
			msgs = append(msgs, f.String())
		}
		sort.Strings(msgs)
		t.Errorf("%d test setup site(s) turn a real failure into a skip:\n\n%s",
			len(findings), strings.Join(msgs, "\n\n"))
	}
}

// isGuardedSource reports whether a file can hold a skip decision: any test
// file, plus the non-test files of the shared test-helper packages. #1597 moved
// the decision into testhelpers/postgres.go, so that is now exactly where the
// next helper that skips would be written.
func isGuardedSource(path string) bool {
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	if !strings.HasSuffix(path, ".go") {
		return false
	}
	switch filepath.Base(filepath.Dir(path)) {
	case "testhelpers", "testutil":
		return true
	}
	return false
}

// inspectFile reports how many times the file calls each guarded function and
// which of those are answered with a skip. The repo-wide run and the self-test
// both go through it, so the self-test cannot pass against logic the real run
// does not have.
//
// A finding is an if statement that mentions a guarded call in its init or
// condition and skips in its body, which covers `if err := f(); err != nil` and
// the `x, err := f()` / `if err != nil` pair alike. Each statement list gets its
// own assignment scope, since reusing `err` across tests in one file is the norm.
func inspectFile(fset *token.FileSet, f *ast.File) (map[string]int, []finding) {
	calls := map[string]int{}
	var findings []finding

	scan := func(list []ast.Stmt) {
		assigned := map[string]string{}
		for _, stmt := range list {
			switch s := stmt.(type) {
			case *ast.AssignStmt:
				recordAssignment(s, assigned)
			case *ast.IfStmt:
				call := guardedCallIn(s, assigned)
				if call == "" {
					continue
				}
				if fn, pos, found := skipCallIn(fset, s.Body); found {
					findings = append(findings, finding{
						pos: pos, call: call, skipFn: fn, rationale: guardedCalls[call],
					})
				}
			}
		}
	}

	// Descending unconditionally means nested blocks, closures and subtests are
	// reached as statement lists in their own right, each with its own scope.
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BlockStmt:
			scan(v.List)
		case *ast.CaseClause:
			scan(v.Body)
		}
		if name := guardedCallName(n); name != "" {
			calls[name]++
		}
		return true
	})

	return calls, findings
}

// recordAssignment notes `x, err := SetupPostgresContainer(...)` so a following
// `if err != nil` is recognized as guarding that call, and clears the note when
// the same name is rebound to something else.
func recordAssignment(s *ast.AssignStmt, assigned map[string]string) {
	if len(s.Rhs) != 1 {
		return
	}
	call := guardedCallName(s.Rhs[0])
	for _, lhs := range s.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || id.Name == "_" {
			continue
		}
		if call == "" {
			delete(assigned, id.Name)
			continue
		}
		assigned[id.Name] = call
	}
}

// guardedCallIn returns the guarded call whose error the if statement checks,
// either inline in its init/condition or through a preceding assignment.
func guardedCallIn(s *ast.IfStmt, assigned map[string]string) string {
	for _, n := range []ast.Node{s.Init, s.Cond} {
		if n == nil {
			continue
		}
		var found string
		ast.Inspect(n, func(x ast.Node) bool {
			if name := guardedCallName(x); name != "" {
				found = name
				return false
			}
			return true
		})
		if found != "" {
			return found
		}
	}

	// `if err != nil` on its own: resolve err through the preceding assignment.
	var found string
	ast.Inspect(s.Cond, func(x ast.Node) bool {
		id, ok := x.(*ast.Ident)
		if !ok {
			return true
		}
		if call, ok := assigned[id.Name]; ok {
			found = call
			return false
		}
		return true
	})
	return found
}

// guardedCallName returns the guarded function name a node calls, if any. It
// matches on the function name alone: qualified (migrations.RunMigrations) and
// unqualified in-package calls are the same defect, and these names are
// distinctive enough that matching the selector too would only buy false
// negatives from an unexpected package alias.
func guardedCallName(n ast.Node) string {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return ""
	}
	var name string
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		name = fun.Sel.Name
	case *ast.Ident:
		name = fun.Name
	}
	if _, guarded := guardedCalls[name]; guarded {
		return name
	}
	return ""
}

// skipCallIn reports the first t.Skip / t.Skipf / t.SkipNow inside a block,
// including nested blocks, since a skip behind a further condition still ends
// the test without a verdict.
func skipCallIn(fset *token.FileSet, body *ast.BlockStmt) (string, token.Position, bool) {
	var name string
	var pos token.Position
	ast.Inspect(body, func(n ast.Node) bool {
		if name != "" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !skipFuncs[sel.Sel.Name] {
			return true
		}
		name = sel.Sel.Name
		pos = fset.Position(call.Pos())
		return false
	})
	return name, pos, name != ""
}

func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}
