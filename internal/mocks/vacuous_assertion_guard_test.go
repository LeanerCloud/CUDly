package mocks

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
// The check is syntactic. Mocks are resolved within the using package first and
// then, for mocks declared elsewhere and aliased in (MockConfigStore is the
// repo's one such case), through a repo-wide index by type name. A receiver it
// still cannot place is reported rather than skipped: a skip that says nothing
// is the same shape of defect the guard exists to catch. It parses rather than
// builds, so it costs milliseconds.
//
// Receiver resolution walks enclosing function bodies innermost-outward, which
// approximates Go's lexical scoping rather than implementing it.

// mockType is one testify mock in one package.
type mockType struct {
	// calledArity maps method name -> number of arguments the method hands to
	// m.Called. This is what testify diffs against, and it is NOT always the
	// signature arity: MockSESClient.SendEmail takes three parameters but calls
	// m.Called(ctx, input).
	calledArity map[string]int
	// shadowed records which assertion helpers the type defines itself, keyed by
	// helper name. Per-helper rather than per-type: a mock that shadows only
	// AssertNotCalled leaves AssertCalled going through testify, where the
	// name-only form is still unfailable, so exempting the whole type would
	// hand back the very hole this guard closes.
	shadowed map[string]bool
}

type assertSite struct {
	pos      token.Position
	recv     string
	assertFn string
	method   string
	matchers int
	// unsupported is non-empty when the site was recognized as a mock
	// assertion but has a shape this check cannot analyze. Such sites are
	// reported, never dropped: silently ignoring a shape is the same defect
	// the guard exists to catch.
	unsupported string
	// scopes is the chain of function bodies enclosing the site, innermost
	// first. Resolution walks it outward like Go's own lexical scoping: a
	// t.Cleanup closure that references a mock declared in the enclosing test
	// still resolves, while two tests in one file that both declare mockStore
	// no longer bleed into each other.
	scopes []ast.Node
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

	// Mocks are usually declared in the package that uses them, but not always:
	// MockConfigStore lives in internal/mocks and is aliased into internal/api,
	// internal/purchase and internal/scheduler. A per-package map alone does not
	// recognize it there, so those sites would be skipped -- and a skip that
	// reports nothing is the very defect this guard exists to catch. Index every
	// mock in the repo by type name as a fallback for exactly that case.
	global := map[string][]*mockType{}
	parsed := map[string][]*ast.File{}
	fsets := map[string]*token.FileSet{}
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
		parsed[dir] = files
		fsets[dir] = fset
		for name, m := range collectMocks(files) {
			global[name] = append(global[name], m)
		}
	}

	var findings []string
	var skipped []string
	checked := 0
	for _, dir := range sortedKeys(pkgFiles) {
		fset := fsets[dir]
		files := parsed[dir]
		mocks := collectMocks(files)
		if len(mocks) == 0 {
			continue
		}
		for _, f := range files {
			for _, site := range collectAssertSites(fset, f) {
				verdict, msg := classifySite(site, mocks, global)
				switch verdict {
				case verdictSkipped:
					skipped = append(skipped, msg)
				case verdictFinding:
					checked++
					findings = append(findings, msg)
				case verdictChecked:
					checked++
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
				m = &mockType{calledArity: map[string]int{}, shadowed: map[string]bool{}}
				mocks[typeName] = m
			}
			if fd.Name.Name == "AssertCalled" || fd.Name.Name == "AssertNotCalled" {
				m.shadowed[fd.Name.Name] = true
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
	// Innermost enclosing function body for each site, so receiver resolution
	// is scoped rather than file-wide.
	var stack []ast.Node
	var walk func(n ast.Node) bool
	walk = func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncDecl:
			if v.Body != nil {
				stack = append(stack, v.Body)
				defer func() { stack = stack[:len(stack)-1] }()
				ast.Inspect(v.Body, walk)
				return false
			}
		case *ast.FuncLit:
			if v.Body != nil {
				stack = append(stack, v.Body)
				defer func() { stack = stack[:len(stack)-1] }()
				ast.Inspect(v.Body, walk)
				return false
			}
		case *ast.CallExpr:
			sel, ok := v.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "AssertCalled" && sel.Sel.Name != "AssertNotCalled") {
				return true
			}
			// Copy innermost-first; stack is mutated as the walk unwinds.
			chain := make([]ast.Node, 0, len(stack))
			for i := len(stack) - 1; i >= 0; i-- {
				chain = append(chain, stack[i])
			}
			site := assertSite{pos: fset.Position(v.Pos()), assertFn: sel.Sel.Name, scopes: chain}

			recv, ok := sel.X.(*ast.Ident)
			if !ok {
				site.recv = exprString(sel.X)
				site.unsupported = "receiver is not a plain identifier (e.g. a field or nested selector)"
				out = append(out, site)
				return true
			}
			site.recv = recv.Name

			if len(v.Args) < 2 {
				// Defensive only: testify's signature requires a method name, so
				// this shape cannot appear in code that compiles. It is exercised
				// from the parse-only synthetic fixture in the self-test.
				site.unsupported = "fewer than two arguments; not a well-formed mock assertion"
				out = append(out, site)
				return true
			}
			lit, ok := v.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				site.unsupported = "method name is not a string literal"
				out = append(out, site)
				return true
			}
			// strconv.Unquote rather than strings.Trim: Trim mishandles escapes
			// and silently accepts a raw-string literal, which is also
			// token.STRING but has different contents.
			name, err := strconv.Unquote(lit.Value)
			if err != nil {
				site.unsupported = "method name literal could not be unquoted: " + err.Error()
				out = append(out, site)
				return true
			}
			site.method = name
			if v.Ellipsis.IsValid() {
				// AssertNotCalled(t, "M", args...) spreads a slice whose length
				// is not knowable here. Counting the spread expression as one
				// matcher would invent a mismatch.
				site.unsupported = "matchers are passed as a variadic spread; the count is not statically known"
				out = append(out, site)
				return true
			}
			site.matchers = len(v.Args) - 2
			out = append(out, site)
			return true
		}
		return true
	}
	ast.Inspect(f, walk)
	return out
}

// siteVerdict is what the guard has to say about a single assertion site.
type siteVerdict int

const (
	verdictIgnore  siteVerdict = iota // nothing to say: not a mock, shadowed, or zero-arity
	verdictChecked                    // analyzed and sound
	verdictFinding                    // an assertion that cannot fail
	verdictSkipped                    // could not be analyzed, and must be reported as such
)

// classifySite is the ONE dispatch for an assertion site. The repo-wide run and
// the self-test both call it, so the self-test cannot pass against a code path
// that exists only inside the test.
//
// That is not hypothetical: the unsupported-shape reporting below was once
// written in five places and read in none that actually ran, because the
// self-test re-implemented this dispatch with the branch the real loop lacked.
// A test asserting on its own private copy of the logic is the same defect this
// whole guard exists to catch, one level up.
func classifySite(site assertSite, mocks map[string]*mockType, global map[string][]*mockType) (siteVerdict, string) {
	if site.unsupported != "" {
		return verdictSkipped, fmt.Sprintf(
			"%s: %s.%s(t, %q) -- %s. Review by hand.",
			site.pos, site.recv, site.assertFn, site.method, site.unsupported)
	}

	recvType, resolved := resolveRecvType(site.scopes, site)
	m, isMock := mocks[recvType]
	if resolved && !isMock {
		var reason string
		m, isMock, reason = resolveCrossPackage(global, recvType, site.method)
		if !isMock {
			return verdictSkipped, fmt.Sprintf(
				"%s: %s.%s(t, %q) -- receiver resolves to %s, which this check "+
					"could not analyze (%s). Review by hand.",
				site.pos, site.recv, site.assertFn, site.method, recvType, reason)
		}
	}
	if !resolved {
		// Only report an unresolved receiver when the method name belongs to
		// some mock and would be unfailable if it were one, so an unreviewed
		// blind spot cannot hide behind a pass without drowning the report.
		if unresolvedLooksRisky(site, mocks, global) {
			return verdictSkipped, fmt.Sprintf(
				"%s: %s.%s(t, %q) -- receiver type could not be resolved; "+
					"review this site by hand", site.pos, site.recv, site.assertFn, site.method)
		}
		return verdictIgnore, ""
	}
	if m.shadowed[site.assertFn] {
		// This type implements the helper being used, so it defines what the
		// matchers mean. See MockConfigStore in assertions.go.
		return verdictIgnore, ""
	}
	arity, known := m.calledArity[site.method]
	if !known {
		// The method exists on a mock but never calls m.Called itself (it
		// delegates to a sibling), so there is no arity to check against.
		// "Checked nothing" must never be silent -- the same reason checked == 0
		// is fatal.
		return verdictSkipped, fmt.Sprintf(
			"%s: %s.%s(t, %q) -- %s does not call m.Called directly, so its "+
				"argument count cannot be derived. Review by hand.",
			site.pos, site.recv, site.assertFn, site.method, site.method)
	}
	if arity == 0 {
		return verdictChecked, ""
	}
	switch {
	case site.matchers == 0:
		return verdictFinding, fmt.Sprintf(
			"%s: %s.%s(t, %q) passes no matchers, but %s hands %d argument(s) to m.Called. "+
				"testify diffs the empty matcher list against those arguments and counts each as a "+
				"difference, so this assertion can never fail. Pass %d matcher(s) (mock.Anything is fine).",
			site.pos, site.recv, site.assertFn, site.method, site.method, arity, arity)
	case site.matchers != arity:
		return verdictFinding, fmt.Sprintf(
			"%s: %s.%s(t, %q) passes %d matcher(s), but %s hands %d argument(s) to m.Called. "+
				"A count that differs from the real arity can never match, so this assertion can "+
				"never fail. Pass %d matcher(s).",
			site.pos, site.recv, site.assertFn, site.method, site.matchers, site.method, arity, arity)
	}
	return verdictChecked, ""
}

// exprString renders a receiver expression for a report message.
func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	case *ast.CallExpr:
		return exprString(v.Fun) + "()"
	case *ast.IndexExpr:
		return exprString(v.X) + "[...]"
	}
	return "<expr>"
}

// resolveRecvType maps the assertion's receiver identifier to the type it was
// assigned from, searching the enclosing function bodies from the innermost
// outward. This approximates Go's lexical scoping closely enough for the shapes
// mock assertions are written in; it is not a full implementation of it. Only
// function bodies are walked, so a package-level mock var is not resolved (see
// the limitation noted in the PR description).
// A file-wide search would keep the last matching assignment, so two tests in
// one file that both name a variable mockStore would resolve every site to
// whichever type happened to appear last -- comparing matcher counts against the
// wrong arity and either inventing a finding or accepting a real one. This repo
// reuses names like mockStore and mockFactory across many tests in a file, so
// that is a live hazard, not a theoretical one.
//
// Conflicting bindings inside a single scope return unresolved rather than a
// guess: the site then reaches the skipped report, which is the direction this
// check degrades in everywhere else.
func resolveRecvType(scopes []ast.Node, site assertSite) (string, bool) {
	for _, scope := range scopes {
		name, binds, conflict := bindingInScope(scope, site.recv)
		if !binds {
			continue // not bound here; look outward
		}
		// The innermost scope that binds the name wins, exactly as it shadows
		// an outer binding at run time. If its type cannot be determined, or it
		// binds two types, report unresolved rather than adopting an outer type
		// the code would never actually use.
		if conflict || name == "" {
			return "", false
		}
		return name, true
	}
	return "", false
}

// bindingInScope looks for assignments of name directly within one function
// body, without descending into nested function literals (those are their own
// scopes and are searched separately). It reports the bound mock type, whether
// the scope binds the name AT ALL, and whether it binds it to two different
// types.
//
// "Binds it at all" is deliberately separate from "we could type it": a scope
// that does `mockStore := newStore()` shadows any outer mockStore, so the walk
// must stop there rather than continue outward and adopt an unrelated type.
func bindingInScope(scope ast.Node, recv string) (string, bool, bool) {
	var found string
	binds := false
	conflict := false
	ast.Inspect(scope, func(n ast.Node) bool {
		if fl, ok := n.(*ast.FuncLit); ok && fl.Body != scope {
			return false
		}
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
			if !ok || id.Name != recv {
				continue
			}
			binds = true
			name := mockTypeOfExpr(rhs[i])
			if name == "" {
				continue
			}
			if found != "" && found != name {
				conflict = true
			}
			found = name
		}
		return true
	})
	return found, binds, conflict
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
func unresolvedLooksRisky(site assertSite, mocks map[string]*mockType, global map[string][]*mockType) bool {
	// Any matcher count can be wrong, not just zero: a non-zero count that does
	// not match the real arity is the third vacuity mode from #1735. Both are
	// reported, since neither can be judged without knowing the type.
	looks := func(m *mockType) bool {
		if m.shadowed[site.assertFn] {
			return false
		}
		arity, ok := m.calledArity[site.method]
		return ok && arity > 0
	}
	for _, m := range mocks {
		if looks(m) {
			return true
		}
	}
	for _, defs := range global {
		for _, m := range defs {
			if looks(m) {
				return true
			}
		}
	}
	return false
}

// sameShadowing reports whether two same-named mocks define the same set of
// assertion helpers, so the cross-package fallback does not exempt a site on one
// mock's opt-out while the real type has none.
func sameShadowing(a, b *mockType) bool {
	if len(a.shadowed) != len(b.shadowed) {
		return false
	}
	for k := range a.shadowed {
		if !b.shadowed[k] {
			return false
		}
	}
	return true
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
	spread.AssertNotCalled(t, "Two", anyArgs...) // want: unsupported, count unknowable

	h.mockThing.AssertNotCalled(t, "Two") // want: unsupported, receiver is a selector
	thing.AssertNotCalled(t, methodName)  // want: unsupported, method not a literal
	thing.AssertNotCalled(t)              // want: unsupported, fewer than two arguments

	// A second test in the same file binding the same name to another type: the
	// site above must not resolve to this one.
	other := &MockShadowed{}
	other.AssertNotCalled(t, "Two")
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
		if len(m.shadowed) != 0 {
			t.Error("MockThing does not define its own helpers and must not count as shadowing")
		}
	}
	if m, ok := mocks["MockShadowed"]; !ok || !m.shadowed["AssertNotCalled"] {
		t.Error("MockShadowed defines AssertNotCalled and must be recorded as shadowing it")
	} else if m.shadowed["AssertCalled"] {
		t.Error("MockShadowed does not define AssertCalled; shadowing must be per-helper, " +
			"or its name-only AssertCalled sites would be exempted while still unfailable")
	}

	// The synthetic source is a single package, so the repo-wide index the real
	// run consults is just this package's mocks.
	global := map[string][]*mockType{}
	for name, m := range mocks {
		global[name] = append(global[name], m)
	}

	// Drive the REAL dispatch, not a copy of it. An earlier revision of this
	// test re-implemented the main loop's branching, so it asserted on an
	// unsupported-shape branch the production path did not have.
	counts := map[siteVerdict]int{}
	var findings, skips []string
	for _, site := range collectAssertSites(fset, f) {
		v, msg := classifySite(site, mocks, global)
		counts[v]++
		switch v {
		case verdictFinding:
			findings = append(findings, msg)
		case verdictSkipped:
			skips = append(skips, msg)
		}
	}

	// Three unfailable assertions: name-only Two, wrong-count Two, name-only
	// AssertCalled on Two. The correct-count and zero-arity sites are sound.
	if counts[verdictFinding] != 3 {
		t.Errorf("findings = %d, want 3:\n%s", counts[verdictFinding], strings.Join(findings, "\n"))
	}

	// Every shape the check cannot analyze must be REPORTED, never dropped:
	// four unsupported syntactic shapes plus a receiver typed to something that
	// is not a mock and a receiver that cannot be resolved at all.
	if counts[verdictSkipped] != 6 {
		t.Errorf("skipped = %d, want 6:\n%s", counts[verdictSkipped], strings.Join(skips, "\n"))
	}
	for _, want := range []string{
		"variadic spread",
		"not a plain identifier",
		"not a string literal",
		"fewer than two arguments",
		"no mock of that name is declared",
		"could not be resolved",
	} {
		found := false
		for _, got := range skips {
			if strings.Contains(got, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no skipped entry mentions %q; got:\n%s", want, strings.Join(skips, "\n"))
		}
	}
}

// resolveCrossPackage finds a mock declared outside the package that uses it,
// keyed by type name. Type names are not unique across the repo (MockEmailSender
// and MockHTTPClient each have several unrelated definitions), so a single
// candidate is used directly and multiple candidates are accepted only when they
// agree about the method in question. Anything else is reported rather than
// assumed, because guessing here would silently attribute one mock's arity to
// another.
func resolveCrossPackage(global map[string][]*mockType, typeName, method string) (*mockType, bool, string) {
	candidates := global[typeName]
	switch len(candidates) {
	case 0:
		return nil, false, "no mock of that name is declared anywhere in the repo"
	case 1:
		return candidates[0], true, ""
	}
	first := candidates[0]
	wantArity, wantKnown := first.calledArity[method]
	for _, c := range candidates[1:] {
		arity, known := c.calledArity[method]
		if known != wantKnown || arity != wantArity || !sameShadowing(c, first) {
			return nil, false, fmt.Sprintf(
				"%d mocks share that name and disagree about %s", len(candidates), method)
		}
	}
	return first, true, ""
}

// TestResolveCrossPackage pins the fallback used for mocks declared outside the
// package that asserts on them. Before it existed, MockConfigStore was
// unrecognized in internal/api, internal/purchase and internal/scheduler, so
// every site there was silently skipped: absent from the checked count, absent
// from the report. Removing MockConfigStore's shadowed helpers took the guard
// from 6 findings to 56 once this landed, the 50 difference being exactly those
// cross-package sites.
//
// Type names are not unique across this repo (MockHTTPClient has five unrelated
// definitions), so the fallback must refuse to guess rather than attribute one
// mock's arity to another.
func TestResolveCrossPackage(t *testing.T) {
	twoArgs := &mockType{calledArity: map[string]int{"Do": 2}, shadowed: map[string]bool{}}
	twoArgsAgain := &mockType{calledArity: map[string]int{"Do": 2}, shadowed: map[string]bool{}}
	threeArgs := &mockType{calledArity: map[string]int{"Do": 3}, shadowed: map[string]bool{}}
	shadowed := &mockType{calledArity: map[string]int{"Do": 2}, shadowed: map[string]bool{"AssertNotCalled": true}}

	tests := []struct {
		name       string
		candidates []*mockType
		wantOK     bool
	}{
		{"single definition is used", []*mockType{twoArgs}, true},
		{"no definition anywhere", nil, false},
		{"duplicates that agree", []*mockType{twoArgs, twoArgsAgain}, true},
		{"duplicates disagreeing on arity", []*mockType{twoArgs, threeArgs}, false},
		{"duplicates disagreeing on shadowing", []*mockType{twoArgs, shadowed}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			global := map[string][]*mockType{"MockThing": tt.candidates}
			got, ok, reason := resolveCrossPackage(global, "MockThing", "Do")
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (reason %q)", ok, tt.wantOK, reason)
			}
			if ok && got == nil {
				t.Error("resolved but returned no mock")
			}
			if !ok && reason == "" {
				t.Error("refused to resolve but gave no reason; the skip would be silent")
			}
		})
	}
}

// TestResolveCrossPackage_UnknownMethodStillDisagrees covers the case where one
// candidate implements the method and another does not: that is a disagreement,
// not a match, and must not resolve.
func TestResolveCrossPackage_UnknownMethodStillDisagrees(t *testing.T) {
	has := &mockType{calledArity: map[string]int{"Do": 2}, shadowed: map[string]bool{}}
	hasNot := &mockType{calledArity: map[string]int{"Other": 1}, shadowed: map[string]bool{}}
	global := map[string][]*mockType{"MockThing": {has, hasNot}}
	if _, ok, reason := resolveCrossPackage(global, "MockThing", "Do"); ok {
		t.Errorf("resolved across candidates that disagree about Do (reason %q)", reason)
	}
}

// TestResolveRecvTypeScoping pins the scoping rules for receiver resolution.
// Before this, the search covered the whole file and kept the LAST matching
// assignment, so two tests in one file both naming a variable mockStore
// resolved every site to whichever type appeared last — comparing the matcher
// count against the wrong arity, which either invents a finding or accepts a
// genuinely unfailable assertion. Wrong attribution is the failure direction
// that gets a guard deleted rather than fixed.
func TestResolveRecvTypeScoping(t *testing.T) {
	const src = `package p

func TestA(t *testing.T) {
	mockStore := &MockAlpha{}
	mockStore.AssertNotCalled(t, "Do")
}

func TestB(t *testing.T) {
	mockStore := &MockBeta{}
	mockStore.AssertNotCalled(t, "Do")
}

func TestClosure(t *testing.T) {
	ses := &MockGamma{}
	t.Cleanup(func() {
		ses.AssertNotCalled(t, "Do")
	})
}

func TestConflict(t *testing.T) {
	dup := &MockAlpha{}
	dup = &MockBeta{}
	dup.AssertNotCalled(t, "Do")
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "scoping.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := map[string]string{}
	for _, site := range collectAssertSites(fset, f) {
		typ, ok := resolveRecvType(site.scopes, site)
		if !ok {
			typ = "<unresolved>"
		}
		got[fmt.Sprintf("%s@%d", site.recv, site.pos.Line)] = typ
	}

	want := map[string]string{
		"mockStore@5":  "MockAlpha",    // TestA's own binding, not TestB's
		"mockStore@10": "MockBeta",     // TestB's own binding, not TestA's
		"ses@16":       "MockGamma",    // closure resolves through the enclosing scope
		"dup@23":       "<unresolved>", // one scope, two types: refuse to guess
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s resolved to %q, want %q", k, got[k], w)
		}
	}
}
