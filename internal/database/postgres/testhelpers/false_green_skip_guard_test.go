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

// This guard closes the class of defect behind #1597 for tests not yet written,
// rather than only the call sites that exist today.
//
// t.Skip reports a test as neither passed nor failed. That is the right answer
// for "this environment cannot run the test at all" and the wrong answer for
// anything the test was written to detect. The DB-backed suites used it for
// both: a testcontainer that would not start AND a set of migrations that would
// not apply took the same t.Skipf branch, so breaking any SQL file under
// internal/database/postgres/migrations turned the whole integration suite
// green.
//
// The rule enforced here is not "no skips". It is that a skip predicate must
// test the precondition it claims to. Docker being absent is probed directly by
// RequirePostgresContainer; that is the only thing allowed to skip. Whether a
// container came up and whether migrations applied are results, not
// preconditions, so an error from either has to fail.
//
// The check is parse-only, so it costs milliseconds and needs no build tags,
// which matters because every file it guards is behind //go:build integration
// and would otherwise be invisible to a default `go test`.

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
	files, calls := 0, 0

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", ".terraform", "frontend":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files++
		// A file that does not parse is the compiler's problem, not this
		// guard's: a package that does not compile has no passing tests to
		// falsely report green either.
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}
		n, fs := inspectFile(fset, f)
		calls += n
		findings = append(findings, fs...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	// "Found nothing" and "looked at nothing" are the same color on a terminal,
	// and telling them apart is precisely what this guard is about. The tripwire
	// counts guarded CALLS rather than the if statements checking them: once the
	// suites are fixed they check with require.NoError and hardly any of the if
	// shape survives, so a count of those would sit near zero and the guard could
	// lose its file discovery, its parsing or its name matching without saying so.
	if calls == 0 {
		t.Fatal("guard found zero calls to any guarded function; its detection has broken " +
			"and it would report success on a repo that skips on every migration failure")
	}
	t.Logf("found %d guarded call(s) across %d test file(s)", calls, files)

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

// inspectFile reports how many guarded calls the file makes and which of them
// are answered with a skip. The repo-wide run and the self-test both go through
// it, so the self-test cannot pass against logic the real run does not have.
//
// A finding is an if statement that mentions a guarded call in its init or
// condition and skips in its body. That covers `if err := f(); err != nil` and
// the `x, err := f()` / `if err != nil` pair alike: for the latter the call is
// resolved through the preceding assignment in the same statement list. Each
// list gets its own assignment scope, because reusing `err` across tests in one
// file is the norm and a file-wide map would attribute one test's call to
// another's check.
func inspectFile(fset *token.FileSet, f *ast.File) (int, []finding) {
	calls := 0
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

	// Every statement list that can hold an assignment followed by its check is
	// scanned with its own scope. Descending unconditionally means nested
	// blocks, closures and subtests are reached as lists in their own right.
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BlockStmt:
			scan(v.List)
		case *ast.CaseClause:
			scan(v.Body)
		}
		if guardedCallName(n) != "" {
			calls++
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

// TestFalseGreenSkipGuardDetects exercises the detector on synthetic source
// covering each shape it must separate. Without it the guard could break into
// reporting nothing and the repo-wide run above would still look green, which is
// the failure mode #1597 is about, one level up.
func TestFalseGreenSkipGuardDetects(t *testing.T) {
	const src = `package p

func TestInlineSkip(t *testing.T) {
	if err := migrations.RunMigrations(ctx, pool, path, "", ""); err != nil {
		t.Skipf("migrations failed: %v", err) // want: finding
	}
}

func TestTwoStatementSkip(t *testing.T) {
	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	if err != nil {
		t.Skipf("no container: %v", err) // want: finding
	}
	_ = container
}

func TestNestedSkip(t *testing.T) {
	err := migrations.MigrateToVersion(ctx, pool, path, 42)
	if err != nil {
		if strings.Contains(err.Error(), "dirty") {
			t.Skip("dirty") // want: finding, a nested skip still ends without a verdict
		}
	}
}

func TestSubtestSkip(t *testing.T) {
	t.Run("sub", func(t *testing.T) {
		err := migrations.RollbackMigrations(ctx, pool, path, 1)
		if err != nil {
			t.Skipf("rollback failed: %v", err) // want: finding, closures are walked
		}
	})
}

func TestFails(t *testing.T) {
	if err := migrations.RunMigrations(ctx, pool, path, "", ""); err != nil {
		t.Fatalf("migrations failed: %v", err) // want: inspected, no finding
	}
}

func TestRebindClearsAssociation(t *testing.T) {
	_, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	err = os.Setenv("X", "1")
	if err != nil {
		t.Skip("unrelated") // want: not inspected, err no longer holds the guarded call
	}
}

func TestUnrelatedSkip(t *testing.T) {
	if os.Getenv("SKIP_DB_TESTS") != "" {
		t.Skip("opt-out") // want: not inspected, an explicit environment opt-out
	}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	calls, findings := inspectFile(fset, f)

	// One guarded call in each function except TestUnrelatedSkip, which makes
	// none. This is the same count the repo-wide tripwire relies on.
	if calls != 6 {
		t.Errorf("calls = %d guarded call(s), want 6", calls)
	}

	// Line numbers of the four t.Skip* calls that must be reported.
	wantLines := map[int]bool{5: true, 12: true, 21: true, 30: true}
	got := map[int]bool{}
	for _, f := range findings {
		got[f.pos.Line] = true
	}
	if len(findings) != len(wantLines) {
		msgs := make([]string, 0, len(findings))
		for _, f := range findings {
			msgs = append(msgs, f.String())
		}
		t.Fatalf("findings = %d, want %d:\n%s", len(findings), len(wantLines), strings.Join(msgs, "\n"))
	}
	for line := range wantLines {
		if !got[line] {
			t.Errorf("no finding on line %d", line)
		}
	}
}
