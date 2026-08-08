package mocks

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

// This guard closes the class of defect behind #1595 and #1740 for code not yet
// written, rather than only the instances that exist today.
//
// testify's AssertCalled / AssertNotCalled diff the matchers they are given
// against the arguments a mock recorded. An empty matcher list is diffed too,
// and each real argument counts as a difference, so a name-only
// AssertNotCalled(t, "Method") never matches a method that passes arguments to
// m.Called: it reports success no matter what the code under test did. The same
// goes for any matcher count that differs from that method's real arity.
//
// A mock may opt out by defining its own AssertCalled / AssertNotCalled -- as
// MockConfigStore does in assertions.go, where the shadowed versions read a
// callLog and name-only deliberately means "not called at all". That opt-out is
// detected, not hardcoded, so a future mock that adopts the same pattern is
// covered automatically.
//
// The check is syntactic and package-scoped: a mock and the tests that use it
// live in the same package throughout this repo. It parses rather than builds,
// so it costs milliseconds.

// mockType is one testify mock in one package.
type mockType struct {
	// calledArity maps method name -> number of arguments the method hands to
	// m.Called. This is what testify diffs against, and it is NOT always the
	// signature arity: MockSESClient.SendEmail takes three parameters but calls
	// m.Called(ctx, input).
	calledArity map[string]int
	// shadows is true when the type defines its own assertion helpers.
	shadows bool
}

type assertSite struct {
	pos      token.Position
	recv     string
	assertFn string
	method   string
	matchers int
}

func TestNoUnfailableMockAssertions(t *testing.T) {
	root := repoRoot(t)

	pkgFiles := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", ".terraform":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			dir := filepath.Dir(path)
			pkgFiles[dir] = append(pkgFiles[dir], path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	var findings []string
	var skipped []string
	checked := 0
	for _, dir := range sortedKeys(pkgFiles) {
		fset := token.NewFileSet()
		var files []*ast.File
		for _, p := range pkgFiles[dir] {
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				// A file that does not parse is the compiler's problem, not this
				// guard's; skipping it cannot hide a vacuous assertion, because a
				// package that does not compile has no passing tests either.
				continue
			}
			files = append(files, f)
		}
		mocks := collectMocks(files)
		if len(mocks) == 0 {
			continue
		}
		for _, f := range files {
			for _, site := range collectAssertSites(fset, f) {
				recvType, resolved := resolveRecvType(f, site)
				m, isMock := mocks[recvType]
				if resolved && !isMock {
					// Resolved to a concrete type that is not a testify mock.
					// Nothing to say about it, and no blind spot either.
					continue
				}
				if !resolved {
					// The receiver was not assigned from a mock literal in this
					// file. Only report it when the method name belongs to some
					// mock in the package and would be unfailable if it were one,
					// so an unreviewed blind spot cannot hide behind a pass.
					if unresolvedLooksRisky(site, mocks) {
						skipped = append(skipped, fmt.Sprintf(
							"%s: %s.%s(t, %q) -- receiver type could not be resolved; "+
								"review this site by hand", site.pos, site.recv, site.assertFn, site.method))
					}
					continue
				}
				arity, known := m.calledArity[site.method]
				if !known || m.shadows {
					continue
				}
				checked++
				if arity == 0 {
					continue
				}
				switch {
				case site.matchers == 0:
					findings = append(findings, fmt.Sprintf(
						"%s: %s.%s(t, %q) passes no matchers, but %s hands %d argument(s) to m.Called. "+
							"testify diffs the empty matcher list against those arguments and counts each as a "+
							"difference, so this assertion can never fail. Pass %d matcher(s) (mock.Anything is fine).",
						site.pos, site.recv, site.assertFn, site.method, site.method, arity, arity))
				case site.matchers != arity:
					findings = append(findings, fmt.Sprintf(
						"%s: %s.%s(t, %q) passes %d matcher(s), but %s hands %d argument(s) to m.Called. "+
							"A count that differs from the real arity can never match, so this assertion can "+
							"never fail. Pass %d matcher(s).",
						site.pos, site.recv, site.assertFn, site.method, site.matchers, site.method, arity, arity))
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("guard checked no assertion sites; its mock or receiver detection has broken " +
			"and it would report success on a repo full of unfailable assertions")
	}
	t.Logf("checked %d mock assertion site(s)", checked)

	// An unresolved receiver is not proof of safety. Surface it rather than
	// letting the count quietly shrink.
	if len(skipped) > 0 {
		sort.Strings(skipped)
		t.Errorf("%d assertion site(s) could not be checked:\n\n%s",
			len(skipped), strings.Join(skipped, "\n\n"))
	}

	if len(findings) > 0 {
		sort.Strings(findings)
		t.Errorf("%d unfailable mock assertion(s):\n\n%s", len(findings), strings.Join(findings, "\n\n"))
	}
}

// collectMocks finds every type in the package that has at least one method
// dispatching through m.Called, which is what makes it a testify mock for the
// purposes of this check.
func collectMocks(files []*ast.File) map[string]*mockType {
	mocks := map[string]*mockType{}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 || fd.Body == nil {
				continue
			}
			typeName := recvTypeIdent(fd.Recv.List[0].Type)
			if typeName == "" {
				continue
			}
			m := mocks[typeName]
			if m == nil {
				m = &mockType{calledArity: map[string]int{}}
				mocks[typeName] = m
			}
			if fd.Name.Name == "AssertCalled" || fd.Name.Name == "AssertNotCalled" {
				m.shadows = true
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Called":
					m.calledArity[fd.Name.Name] = len(call.Args)
				case "MethodCalled":
					if len(call.Args) >= 1 {
						m.calledArity[fd.Name.Name] = len(call.Args) - 1
					}
				}
				return true
			})
		}
	}
	// A type with no m.Called anywhere is not a mock.
	for name, m := range mocks {
		if len(m.calledArity) == 0 {
			delete(mocks, name)
		}
	}
	return mocks
}

func collectAssertSites(fset *token.FileSet, f *ast.File) []assertSite {
	var out []assertSite
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "AssertCalled" && sel.Sel.Name != "AssertNotCalled" {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || len(call.Args) < 2 {
			return true
		}
		lit, ok := call.Args[1].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if call.Ellipsis.IsValid() {
			// AssertNotCalled(t, "M", args...) spreads a slice whose length is
			// not knowable here. Counting the spread expression as one matcher
			// would invent a mismatch, so the site is left alone.
			return true
		}
		out = append(out, assertSite{
			pos:      fset.Position(call.Pos()),
			recv:     recv.Name,
			assertFn: sel.Sel.Name,
			method:   strings.Trim(lit.Value, `"`),
			matchers: len(call.Args) - 2,
		})
		return true
	})
	return out
}

// resolveRecvType maps the assertion's receiver identifier back to the type it
// was assigned from within the file: mockStore := &MockX{}, new(MockX), or
// MockX{}. It reports the type name whether or not that type is a mock, so the
// caller can tell "this is not a mock" (nothing to check) apart from "we could
// not tell what this is" (an unreviewed blind spot).
func resolveRecvType(f *ast.File, site assertSite) (string, bool) {
	var found string
	ast.Inspect(f, func(n ast.Node) bool {
		var lhs, rhs []ast.Expr
		switch v := n.(type) {
		case *ast.AssignStmt:
			lhs, rhs = v.Lhs, v.Rhs
		case *ast.ValueSpec:
			for _, name := range v.Names {
				lhs = append(lhs, name)
			}
			rhs = v.Values
		default:
			return true
		}
		if len(lhs) != len(rhs) {
			return true
		}
		for i, l := range lhs {
			id, ok := l.(*ast.Ident)
			if !ok || id.Name != site.recv {
				continue
			}
			if name := mockTypeOfExpr(rhs[i]); name != "" {
				found = name
			}
		}
		return true
	})
	return found, found != ""
}

// mockTypeOfExpr extracts T from &T{...}, T{...} and new(T).
func mockTypeOfExpr(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.UnaryExpr:
		if v.Op == token.AND {
			return mockTypeOfExpr(v.X)
		}
	case *ast.CompositeLit:
		if id, ok := v.Type.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.CallExpr:
		if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "new" && len(v.Args) == 1 {
			if t, ok := v.Args[0].(*ast.Ident); ok {
				return t.Name
			}
		}
	}
	return ""
}

func recvTypeIdent(e ast.Expr) string {
	if star, ok := e.(*ast.StarExpr); ok {
		e = star.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func repoRoot(t *testing.T) string {
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

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// unresolvedLooksRisky reports whether an assertion whose receiver could not be
// resolved names a method that some mock in the package implements with a
// non-zero m.Called arity and without shadowing the helpers. Those are the
// unresolved sites that could be hiding the defect.
func unresolvedLooksRisky(site assertSite, mocks map[string]*mockType) bool {
	if site.matchers > 0 {
		// A site carrying matchers is only wrong if the count is off, which
		// cannot be judged without knowing which mock it is. Ambiguous rather
		// than risky, and reporting every one would drown the real findings.
		return false
	}
	for _, m := range mocks {
		if m.shadows {
			continue
		}
		if arity, ok := m.calledArity[site.method]; ok && arity > 0 {
			return true
		}
	}
	return false
}

// TestVacuousAssertionGuardDetects exercises the guard's analysis on synthetic
// source covering each case it must separate. Without this, the guard could
// break into reporting nothing and the repo-wide run would still look green --
// the exact failure mode #1595 was about.
func TestVacuousAssertionGuardDetects(t *testing.T) {
	const src = `package p

type MockThing struct{ mock.Mock }

func (m *MockThing) Two(a, b int) error  { return m.Called(a, b).Error(0) }
func (m *MockThing) None() error         { return m.Called().Error(0) }

type MockShadowed struct{ mock.Mock }

func (m *MockShadowed) Two(a, b int) error                  { return m.Called(a, b).Error(0) }
func (m *MockShadowed) AssertNotCalled(t T, s string, a ...interface{}) bool { return true }

type NotAMock struct{}

func (n *NotAMock) Two(a, b int) error { return nil }

func Test(t *testing.T) {
	thing := &MockThing{}
	thing.AssertNotCalled(t, "Two")                               // want: vacuous
	thing.AssertNotCalled(t, "Two", mock.Anything)                // want: wrong count
	thing.AssertNotCalled(t, "Two", mock.Anything, mock.Anything) // want: ok
	thing.AssertCalled(t, "Two")                                  // want: vacuous
	thing.AssertNotCalled(t, "None")                              // want: ok, arity 0

	shadowed := new(MockShadowed)
	shadowed.AssertNotCalled(t, "Two") // want: ok, type shadows the helpers

	plain := &NotAMock{}
	plain.AssertNotCalled(t, "Two") // want: ignored, not a mock

	unknown.AssertNotCalled(t, "Two") // want: reported as unresolved

	spread := &MockThing{}
	spread.AssertNotCalled(t, "Two", anyArgs...) // want: ignored, count unknowable
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	files := []*ast.File{f}
	mocks := collectMocks(files)

	if _, ok := mocks["NotAMock"]; ok {
		t.Error("NotAMock has no m.Called and must not be treated as a mock")
	}
	if m, ok := mocks["MockThing"]; !ok {
		t.Fatal("MockThing not detected as a mock")
	} else {
		if got := m.calledArity["Two"]; got != 2 {
			t.Errorf("MockThing.Two arity = %d, want 2", got)
		}
		if got, ok := m.calledArity["None"]; !ok || got != 0 {
			t.Errorf("MockThing.None arity = %d (present=%v), want 0", got, ok)
		}
		if m.shadows {
			t.Error("MockThing does not define its own helpers and must not count as shadowing")
		}
	}
	if m, ok := mocks["MockShadowed"]; !ok || !m.shadows {
		t.Error("MockShadowed defines AssertNotCalled and must count as shadowing")
	}

	type verdict struct {
		method   string
		matchers int
		vacuous  bool
	}
	var got []verdict
	unresolved := 0
	for _, site := range collectAssertSites(fset, f) {
		recvType, resolved := resolveRecvType(f, site)
		m, isMock := mocks[recvType]
		if resolved && !isMock {
			continue
		}
		if !resolved {
			if unresolvedLooksRisky(site, mocks) {
				unresolved++
			}
			continue
		}
		arity, known := m.calledArity[site.method]
		if !known || m.shadows || arity == 0 {
			continue
		}
		got = append(got, verdict{site.method, site.matchers, site.matchers != arity})
	}

	want := []verdict{
		{"Two", 0, true},  // name-only
		{"Two", 1, true},  // wrong count
		{"Two", 2, false}, // correct
		{"Two", 0, true},  // AssertCalled, name-only
	}
	if len(got) != len(want) {
		t.Fatalf("checked %d site(s), want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("site %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if unresolved != 1 {
		t.Errorf("unresolved risky sites = %d, want 1 (the `unknown` receiver)", unresolved)
	}
}
