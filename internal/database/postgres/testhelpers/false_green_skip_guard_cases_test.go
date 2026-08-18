package testhelpers

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

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

	// Per-name counts, the same measurement the repo-wide floor relies on.
	wantCalls := map[string]int{
		"RunMigrations":          2, // TestInlineSkip, TestFails
		"SetupPostgresContainer": 2, // TestTwoStatementSkip, TestRebindClearsAssociation
		"MigrateToVersion":       1,
		"RollbackMigrations":     1,
	}
	for name, want := range wantCalls {
		if calls[name] != want {
			t.Errorf("calls[%q] = %d, want %d", name, calls[name], want)
		}
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

// TestFalseGreenSkipGuardKnownBypasses records what the guard does not catch,
// so the header's boundary is executed rather than asserted. Each case is a
// real false green that would slip past.
//
// This is documentation, not a requirement: widening the guard to catch one of
// these is an improvement, and the failure it produces here is the prompt to
// move that case into TestFalseGreenSkipGuardDetects and update the header.
//
// Every case still leaves the guarded call countable, which is the part that
// matters operationally: a bypass hides a finding, but it cannot also silence
// the per-name floor into reporting a clean repo it never looked at.
func TestFalseGreenSkipGuardKnownBypasses(t *testing.T) {
	cases := []struct {
		name     string
		why      string
		wantCall string
		src      string
	}{
		{
			name:     "skip behind a helper",
			why:      "only a t.Skip* selector counts as a skip, so any wrapper hides it",
			wantCall: "RunMigrations",
			src: `package p
func TestX(t *testing.T) {
	if err := migrations.RunMigrations(ctx, pool, path, "", ""); err != nil {
		skipDB(t, err)
	}
}`,
		},
		{
			name:     "switch case rather than an if",
			why:      "a finding is an IfStmt; a CaseClause guard is never considered",
			wantCall: "RunMigrations",
			src: `package p
func TestX(t *testing.T) {
	err := migrations.RunMigrations(ctx, pool, path, "", "")
	switch {
	case err != nil:
		t.Skipf("nope: %v", err)
	}
}`,
		},
		{
			name:     "assignment in the parent, check inside a t.Run closure",
			why:      "each statement list carries its own scope, so err is unresolved in the closure",
			wantCall: "SetupPostgresContainer",
			src: `package p
func TestX(t *testing.T) {
	_, err := testhelpers.SetupPostgresContainer(ctx, t)
	t.Run("sub", func(t *testing.T) {
		if err != nil {
			t.Skip("no container")
		}
	})
}`,
		},
		{
			name:     "wrapper returning the guarded call's error",
			why:      "the if statement names only the wrapper, and there is no type information to follow it",
			wantCall: "RunMigrations",
			src: `package p
func setup(t *testing.T) error {
	return migrations.RunMigrations(ctx, pool, path, "", "")
}
func TestX(t *testing.T) {
	if err := setup(t); err != nil {
		t.Skip("setup failed")
	}
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "synthetic.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			calls, findings := inspectFile(fset, f)

			if len(findings) > 0 {
				t.Errorf("the guard now catches this shape (%s). That is an improvement: "+
					"move the case into TestFalseGreenSkipGuardDetects and drop it from the "+
					"header's boundary list.", tc.why)
			}
			if calls[tc.wantCall] != 1 {
				t.Errorf("calls[%q] = %d, want 1: the bypass must still leave the call "+
					"countable, or it would disarm the per-name floor as well",
					tc.wantCall, calls[tc.wantCall])
			}
		})
	}
}
