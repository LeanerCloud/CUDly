package iacfiles

// GCP WIF onboarding-script tests. Split out of templates_test.go, which the
// project's 500-line file limit had outgrown. runRenderedScript stays there and
// is shared with the AWS WIF tests.

import (
	"cmp"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gcloudStubScript returns a stand-in `gcloud` that records every invocation and
// answers describes from state files under $CUDLY_STUB_STATE. The state
// directory is the caller's, so a test can seed a pre-existing pool/provider
// before the run and can hand the same directory to two consecutive runs to
// exercise the re-run path a customer actually takes.
//
// Only the describe/create verbs the GCP WIF script branches on are modeled;
// everything else (config set, services enable, add-iam-policy-binding)
// succeeds, so a run that reaches them has proved the guards let it through.
func gcloudStubScript(logPath string) string {
	return `#!/usr/bin/env bash
state="${CUDLY_STUB_STATE:?stub state dir}"
printf '%s\n' "gcloud $*" >> '` + logPath + `'

# Flag values are read off the argument vector rather than parsed out of the
# joined string, so a value containing spaces or quotes is recorded verbatim.
issuer=""; condition=""; mapping=""; provider_id=""; prev=""
for a in "$@"; do
  case "$a" in
    --issuer-uri=*)          issuer="${a#--issuer-uri=}" ;;
    --attribute-condition=*) condition="${a#--attribute-condition=}" ;;
    --attribute-mapping=*)   mapping="${a#--attribute-mapping=}" ;;
  esac
  [[ "$prev" == "create-oidc" ]] && provider_id="$a"
  prev="$a"
done

pool_path="projects/000000000000/locations/global/workloadIdentityPools/cudly-target"

case "$*" in
  *"workload-identity-pools providers describe"*)
    [[ -f "${state}/provider" ]] || exit 1
    case "$*" in
      *"value(oidc.issuerUri)"*)     cat "${state}/provider.issuer" ;;
      *"value(attributeCondition)"*) cat "${state}/provider.condition" ;;
      *"value(attributeMapping)"*)   cat "${state}/provider.mapping" ;;
      *"value(oidc.allowedAudiences)"*) cat "${state}/provider.audiences" ;;
      *"value(state)"*)              cat "${state}/provider.state" ;;
      *"value(disabled)"*)           cat "${state}/provider.disabled" ;;
    esac
    ;;
  *"workload-identity-pools providers list"*)
    if [[ -n "${STUB_PROVIDER_LIST_ERROR:-}" ]]; then
      echo "ERROR: (gcloud.iam.workload-identity-pools.providers.list) ${STUB_PROVIDER_LIST_ERROR}" >&2
      exit 1
    fi
    # Soft-deleted providers are omitted here as they are by real gcloud
    # without --show-deleted; the seeded list holds only live ones.
    [[ -f "${state}/providers.list" ]] && cat "${state}/providers.list"
    ;;
  *"workload-identity-pools providers create-oidc"*)
    if [[ -n "${STUB_PROVIDER_CREATE_ERROR:-}" ]]; then
      echo "ERROR: (gcloud.iam.workload-identity-pools.providers.create-oidc) ${STUB_PROVIDER_CREATE_ERROR}" >&2
      exit 1
    fi
    printf '%s\n' "$issuer"    > "${state}/provider.issuer"
    printf '%s\n' "$condition" > "${state}/provider.condition"
    printf '%s\n' "$mapping"   > "${state}/provider.mapping"
    printf '\n'               > "${state}/provider.audiences"
    printf 'ACTIVE\n'          > "${state}/provider.state"
    printf 'False\n'           > "${state}/provider.disabled"
    printf '%s\n' "${pool_path}/providers/${provider_id}" >> "${state}/providers.list"
    touch "${state}/provider"
    ;;
  *"workload-identity-pools describe"*)
    [[ -f "${state}/pool" ]] || exit 1
    echo "projects/000000000000/locations/global/workloadIdentityPools/cudly-target"
    ;;
  *"workload-identity-pools create"*)
    if [[ -n "${STUB_POOL_CREATE_ERROR:-}" ]]; then
      echo "ERROR: (gcloud.iam.workload-identity-pools.create) ${STUB_POOL_CREATE_ERROR}" >&2
      exit 1
    fi
    touch "${state}/pool"
    ;;
esac
exit 0
`
}

const (
	// gcpStubIssuer is the issuer the rendered script is run with, i.e. the
	// value an existing provider must carry to be reusable.
	gcpStubIssuer = "https://cudly.example.com/oidc"
	// gcpExpectedCondition/gcpExpectedMapping are what the script writes for the
	// default subject, and therefore what it must demand of a provider it reuses.
	gcpExpectedCondition = "assertion.sub == 'cudly-controller'"
	gcpExpectedMapping   = "google.subject=assertion.sub"
	// gcpStubAudience is WIF_AUDIENCE for the stub's pool: the provider's own
	// resource name, which is what GCP defaults an unset audience list to and
	// what CUDly presents as `aud`.
	gcpStubAudience = "//iam.googleapis.com/projects/000000000000/locations/global/workloadIdentityPools/cudly-target/providers/cudly-oidc"
)

// gcpStubState is the pre-existing GCP state a run starts from. The zero value
// is an empty project.
type gcpStubState struct {
	pool     bool
	provider bool
	// Set only when provider is true.
	issuer    string
	condition string
	mapping   string
	// state defaults to ACTIVE and disabled to False when provider is true.
	state    string
	disabled string
	// audiences is the ';'-joined oidc.allowedAudiences list. Empty means the
	// field is unset, which is how a provider created without --allowed-audiences
	// reports, and which GCP treats as "the provider's own resource name".
	audiences string
	// otherProviders are further live providers in the same pool, given as
	// bare IDs.
	otherProviders []string
}

// seedGCPStubState materializes state in dir for the stub to read.
func seedGCPStubState(t *testing.T, dir string, s gcpStubState) {
	t.Helper()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	if s.pool {
		write("pool", "")
	}
	const poolPath = "projects/000000000000/locations/global/workloadIdentityPools/cudly-target"
	var live []string
	if s.provider {
		write("provider", "")
		write("provider.issuer", s.issuer)
		write("provider.condition", s.condition)
		write("provider.mapping", s.mapping)
		write("provider.audiences", s.audiences)
		write("provider.state", cmp.Or(s.state, "ACTIVE"))
		write("provider.disabled", cmp.Or(s.disabled, "False"))
		live = append(live, poolPath+"/providers/cudly-oidc")
	}
	for _, id := range s.otherProviders {
		live = append(live, poolPath+"/providers/"+id)
	}
	if len(live) > 0 {
		write("providers.list", strings.Join(live, "\n"))
	}
}

// runGCPWIFScript renders gcp-wif-cli.sh and runs it against the gcloud stub,
// with stateDir carrying the project state across runs.
func runGCPWIFScript(t *testing.T, stateDir string, env map[string]string) (exitCode int, stdout, stderr string, calls []string) {
	t.Helper()
	// CUDlyAPIURL is cleared so the auto-registration block (curl + jq) is not
	// rendered: these tests are about the pool/provider/grant sequence.
	data := baseData()
	data.CUDlyAPIURL = ""
	data.ProjectID = "cudly-demo-project"
	data.ServiceAccountEmail = "cudly@cudly-demo-project.iam.gserviceaccount.com"
	rendered := renderCLITemplate(t, "templates/gcp-wif-cli.sh.tmpl", data)

	full := map[string]string{
		"CUDLY_STUB_STATE": stateDir,
		// Rendered from an empty CUDlyAPIURL above, so it must be supplied here
		// for the issuer guard to pass.
		"CUDLY_ISSUER_URL": gcpStubIssuer,
	}
	for k, v := range env {
		full[k] = v
	}
	return runRenderedScript(t, "gcp-wif-cli.sh", rendered, func(logPath string) map[string]string {
		return map[string]string{"gcloud": gcloudStubScript(logPath)}
	}, full)
}

// callsContaining returns the recorded gcloud invocations containing needle.
func callsContaining(calls []string, needle string) []string {
	var out []string
	for _, c := range calls {
		if strings.Contains(c, needle) {
			out = append(out, c)
		}
	}
	return out
}

// TestGCPWIFCLI_ProviderReuseIsVerified is the regression test for #1661. The
// served script used to run both creates with `|| echo "(may already exist)"`,
// so under `set -euo pipefail` every failure of a security-critical create was
// swallowed: a provider that already existed with a weaker attribute condition
// (or none) was silently reused, the impersonation grant was made against it,
// and the script printed "=== Done ===" and exited 0.
//
// Each reject case below asserts the run aborts *before* the grant, and the
// accept cases assert a fresh project and a legitimate re-run both still
// succeed, so the fix cannot be a blanket refusal.
func TestGCPWIFCLI_ProviderReuseIsVerified(t *testing.T) {
	const grantCall = "add-iam-policy-binding"

	reject := []struct {
		name  string
		state gcpStubState
		env   map[string]string
		// wantText must appear in stderr.
		wantText string
		// absentCall, when set, must not appear among the recorded gcloud
		// invocations. It carries the cases where aborting at the right point
		// matters as much as aborting at all.
		absentCall string
	}{
		{
			// The actual vulnerability: "true" is non-empty and admits every
			// subject the issuer will sign.
			name:     "existing provider with an always-true condition",
			state:    gcpStubState{pool: true, provider: true, issuer: gcpStubIssuer, condition: "true", mapping: gcpExpectedMapping},
			wantText: "different attribute condition",
		},
		{
			name:     "existing provider with no condition at all",
			state:    gcpStubState{pool: true, provider: true, issuer: gcpStubIssuer, condition: "", mapping: gcpExpectedMapping},
			wantText: "different attribute condition",
		},
		{
			name:     "existing provider pinned to a different subject",
			state:    gcpStubState{pool: true, provider: true, issuer: gcpStubIssuer, condition: "assertion.sub == 'someone-else'", mapping: gcpExpectedMapping},
			wantText: "different attribute condition",
		},
		{
			// A matching condition on a provider trusting someone else's issuer
			// admits whoever that issuer signs a cudly-controller token for.
			name:     "existing provider bound to a foreign issuer",
			state:    gcpStubState{pool: true, provider: true, issuer: "https://evil.example.com/oidc", condition: gcpExpectedCondition, mapping: gcpExpectedMapping},
			wantText: "different issuer URI",
		},
		{
			name:     "existing provider with a different attribute mapping",
			state:    gcpStubState{pool: true, provider: true, issuer: gcpStubIssuer, condition: gcpExpectedCondition, mapping: "google.subject=assertion.email"},
			wantText: "different attribute mapping",
		},
		{
			// An audience list that omits this pool's audience rejects every
			// token CUDly mints, so the run would otherwise report success over
			// a provider that cannot accept it.
			name: "existing provider restricted to another audience",
			state: gcpStubState{
				pool: true, provider: true, issuer: gcpStubIssuer,
				condition: gcpExpectedCondition, mapping: gcpExpectedMapping,
				audiences: "//iam.googleapis.com/projects/000000000000/locations/global/workloadIdentityPools/other/providers/other",
			},
			wantText: "different allowed audience list",
		},
		{
			// Not an "already exists" case: any other create failure (quota,
			// permission, a concurrent run winning the race) must stop the run
			// rather than fall through to the grant.
			name:     "provider create fails",
			state:    gcpStubState{pool: true},
			env:      map[string]string{"STUB_PROVIDER_CREATE_ERROR": "PERMISSION_DENIED: caller lacks iam.workloadIdentityPoolProviders.create"},
			wantText: "PERMISSION_DENIED",
		},
		{
			// The pre-fix script swallowed this one too and went on to create
			// the provider; the assertion that no provider create is attempted
			// is what makes this case a regression witness rather than a
			// restatement of the propagation loop's eventual abort.
			name:       "pool create fails",
			env:        map[string]string{"STUB_POOL_CREATE_ERROR": "PERMISSION_DENIED: caller lacks iam.workloadIdentityPools.create"},
			wantText:   "PERMISSION_DENIED",
			absentCall: "providers create-oidc",
		},
		{
			// describe returns a soft-deleted provider with its configuration
			// intact, so every comparison matches and the run would otherwise
			// report success against a provider that cannot exchange tokens.
			name: "existing provider is soft-deleted",
			state: gcpStubState{
				pool: true, provider: true, issuer: gcpStubIssuer,
				condition: gcpExpectedCondition, mapping: gcpExpectedMapping, state: "DELETED",
			},
			wantText: "cannot exchange tokens",
		},
		{
			name: "existing provider is disabled",
			state: gcpStubState{
				pool: true, provider: true, issuer: gcpStubIssuer,
				condition: gcpExpectedCondition, mapping: gcpExpectedMapping, disabled: "True",
			},
			wantText: "cannot exchange tokens",
		},
		{
			// The guard must not depend on gcloud rendering the boolean as
			// Python's "True": a lowercase or numeric rendering still means
			// disabled, and testing for the one usable value keeps it that way.
			name: "existing provider is disabled, rendered lowercase",
			state: gcpStubState{
				pool: true, provider: true, issuer: gcpStubIssuer,
				condition: gcpExpectedCondition, mapping: gcpExpectedMapping, disabled: "true",
			},
			wantText: "cannot exchange tokens",
		},
		{
			// The grant names the pool, not the provider, so a second provider
			// in the same pool that maps a token to the same subject satisfies
			// it. Verifying only this run's provider would leave that open.
			name: "pool holds a provider this script did not configure",
			state: gcpStubState{
				pool: true, provider: true, issuer: gcpStubIssuer,
				condition: gcpExpectedCondition, mapping: gcpExpectedMapping,
				otherProviders: []string{"someone-elses-oidc"},
			},
			wantText: "did not configure",
		},
		{
			// The fail-closed case for the enumeration itself: a list this run
			// could not read says nothing about what the pool holds, so it must
			// stop rather than proceed on an empty result. This is what the
			// fetch-into-a-variable form buys over piping into a filter.
			name:       "listing the pool's providers fails",
			state:      gcpStubState{pool: true},
			env:        map[string]string{"STUB_PROVIDER_LIST_ERROR": "PERMISSION_DENIED: caller lacks iam.workloadIdentityPoolProviders.list"},
			wantText:   "PERMISSION_DENIED",
			absentCall: "providers create-oidc",
		},
		{
			// The shape a customer onboarded before the provider default
			// changed from cudly-<source> to cudly-oidc lands in: the pool
			// holds only the old provider. The refusal has to come before the
			// create, or every attempt leaves another provider behind.
			name: "pool holds only a legacy provider, ours not yet created",
			state: gcpStubState{
				pool:           true,
				otherProviders: []string{"cudly-aws"},
			},
			wantText:   "did not configure",
			absentCall: "providers create-oidc",
		},
	}

	for _, tc := range reject {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			seedGCPStubState(t, stateDir, tc.state)

			exitCode, stdout, stderr, calls := runGCPWIFScript(t, stateDir, tc.env)

			if exitCode == 0 {
				t.Errorf("the run must abort, but it exited 0; stdout:\n%s", stdout)
			}
			if grants := callsContaining(calls, grantCall); len(grants) != 0 {
				t.Errorf("the impersonation grant must not be made, but %s was invoked: %v", grantCall, grants)
			}
			if strings.Contains(stdout, "=== Done ===") {
				t.Errorf("an aborted run must not report success; stdout:\n%s", stdout)
			}
			if !strings.Contains(stderr, tc.wantText) {
				t.Errorf("stderr must explain the abort with %q, got:\n%s", tc.wantText, stderr)
			}
			if tc.absentCall != "" {
				if got := callsContaining(calls, tc.absentCall); len(got) != 0 {
					t.Errorf("the run must abort before %q, but it was invoked: %v", tc.absentCall, got)
				}
			}
		})
	}

	// Positive control: an empty project must still be configured end to end.
	// Without it, a fix that refused every run would satisfy every case above.
	t.Run("accept/empty project", func(t *testing.T) {
		stateDir := t.TempDir()
		exitCode, stdout, stderr, calls := runGCPWIFScript(t, stateDir, nil)

		if exitCode != 0 {
			t.Fatalf("a fresh project must be configured successfully, got exit %d; stderr:\n%s", exitCode, stderr)
		}
		if len(calls) == 0 {
			t.Fatal("the gcloud stub was never invoked, so this case proves nothing")
		}
		created := callsContaining(calls, "providers create-oidc")
		if len(created) != 1 {
			t.Fatalf("expected exactly one provider create, got %d: %v", len(created), calls)
		}
		if !strings.Contains(created[0], "--attribute-condition="+gcpExpectedCondition) {
			t.Errorf("the provider must be created with the pinned condition %q, got:\n%s", gcpExpectedCondition, created[0])
		}
		if grants := callsContaining(calls, grantCall); len(grants) != 1 {
			t.Errorf("expected exactly one impersonation grant, got %d: %v", len(grants), calls)
		}
		if !strings.Contains(stdout, "=== Done ===") {
			t.Errorf("a successful run must report the values CUDly needs; stdout:\n%s", stdout)
		}
	})

	// An audience list is a restriction only when it omits ours. Both of these
	// are legitimate reuse: unset (GCP defaults to the provider's own resource
	// name, which is what CUDly presents) and a list that includes it among
	// others. Without them the audience check could reject every provider and
	// the case above would still pass.
	for _, tc := range []struct {
		name      string
		audiences string
	}{
		{"unset audience list", ""},
		{"audience list containing ours", "https://other.example/aud;" + gcpStubAudience},
	} {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			seedGCPStubState(t, stateDir, gcpStubState{
				pool: true, provider: true, issuer: gcpStubIssuer,
				condition: gcpExpectedCondition, mapping: gcpExpectedMapping,
				audiences: tc.audiences,
			})

			exitCode, stdout, stderr, calls := runGCPWIFScript(t, stateDir, nil)
			if exitCode != 0 {
				t.Fatalf("reuse must be allowed, got exit %d; stderr:\n%s", exitCode, stderr)
			}
			if grants := callsContaining(calls, grantCall); len(grants) != 1 {
				t.Errorf("expected exactly one impersonation grant, got %d: %v", len(grants), calls)
			}
			if !strings.Contains(stdout, "=== Done ===") {
				t.Errorf("the run must complete; stdout:\n%s", stdout)
			}
		})
	}

	// The resumable case: a customer re-running the script over the state the
	// first run left behind. The second run must reuse the provider it created
	// rather than refuse it, so onboarding is not broken by the fix.
	t.Run("accept/re-run over the previous run's state", func(t *testing.T) {
		stateDir := t.TempDir()
		if exitCode, _, stderr, _ := runGCPWIFScript(t, stateDir, nil); exitCode != 0 {
			t.Fatalf("first run failed with exit %d; stderr:\n%s", exitCode, stderr)
		}

		exitCode, stdout, stderr, calls := runGCPWIFScript(t, stateDir, nil)
		if exitCode != 0 {
			t.Fatalf("re-running over an unchanged project must succeed, got exit %d; stderr:\n%s", exitCode, stderr)
		}
		if created := callsContaining(calls, "providers create-oidc"); len(created) != 0 {
			t.Errorf("the second run must not re-create the provider, got: %v", created)
		}
		if !strings.Contains(stdout, "Reusing existing provider") {
			t.Errorf("the second run must report the reuse; stdout:\n%s", stdout)
		}
		if grants := callsContaining(calls, grantCall); len(grants) != 1 {
			t.Errorf("the re-run must still (idempotently) apply the grant, got %d: %v", len(grants), calls)
		}
		if !strings.Contains(stdout, "=== Done ===") {
			t.Errorf("the re-run must complete; stdout:\n%s", stdout)
		}
	})
}

// TestGCPWIFCLI_SubjectValidated covers #1661 item 2: CUDLY_FEDERATED_SUBJECT is
// environment-overridable and lands inside the CEL string literal of
// --attribute-condition, so a value carrying a quote closes the literal and the
// remainder rewrites the condition: the value "x' || true || '" renders a
// condition whose second disjunct is a bare "true", admitting every subject the
// issuer will sign. It also lands in the principal:// identifier of the grant,
// where a '*' widens the member.
func TestGCPWIFCLI_SubjectValidated(t *testing.T) {
	reject := []struct {
		name    string
		subject string
	}{
		{"CEL break-out", `x' || true || '`},
		{"double quote", `x" || true || "`},
		{"backslash", `x\'`},
		{"backtick", "x`id`"},
		{"wildcard", "*"},
		{"embedded wildcard", "cudly-*"},
		{"unexpanded placeholder", "${CUDLY_SUBJECT}"},
		{"embedded space", "cudly controller"},
		{"leading punctuation", "-cudly-controller"},
		{"over the 127-character google.subject limit", strings.Repeat("a", 128)},
	}

	for _, tc := range reject {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			exitCode, _, stderr, calls := runGCPWIFScript(t, stateDir,
				map[string]string{"CUDLY_FEDERATED_SUBJECT": tc.subject})

			if exitCode == 0 {
				t.Errorf("CUDLY_FEDERATED_SUBJECT %q was accepted (exit 0)", tc.subject)
			}
			// The guard must run before the first gcloud call, so a rejected run
			// leaves no pool, provider or grant behind.
			if len(calls) != 0 {
				t.Errorf("no gcloud call may be made before the subject is validated, got %d: %v", len(calls), calls)
			}
			if !strings.Contains(stderr, "CUDLY_FEDERATED_SUBJECT") {
				t.Errorf("stderr must name the rejected variable, got:\n%s", stderr)
			}
		})
	}

	// Positive controls: the default, and a subject in the Auth0/Okta shape the
	// charset deliberately admits. Without these, a guard rejecting everything
	// would pass every case above.
	accept := []struct {
		name    string
		subject string
		// wantPinned is the subject the provider must end up pinned to, which
		// differs from subject only where the script substitutes its default.
		wantPinned string
	}{
		// The only subject CUDly can actually present: gcpFederatedSubject in
		// internal/credentials/gcp_federated.go is a compile-time constant.
		{"default subject", "cudly-controller", "cudly-controller"},
		// The cases below assert the charset admits these values and that they
		// reach gcloud unmangled. They do NOT assert a usable configuration:
		// pinning the provider to any subject other than the default binds it
		// to one CUDly will never sign.
		{"pipe-bearing subject reaches gcloud unmangled", "google-oauth2|1234567890", "google-oauth2|1234567890"},
		{"exactly 127 characters", strings.Repeat("a", 127), strings.Repeat("a", 127)},
		// An empty override is not a hole: ${VAR:-default} substitutes the
		// default for an empty value, so the provider is still pinned to
		// cudly-controller rather than to the empty subject.
		{"empty falls back to the default", "", "cudly-controller"},
	}
	for _, tc := range accept {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			exitCode, stdout, stderr, calls := runGCPWIFScript(t, stateDir,
				map[string]string{"CUDLY_FEDERATED_SUBJECT": tc.subject})

			if exitCode != 0 {
				t.Fatalf("CUDLY_FEDERATED_SUBJECT %q was rejected (exit %d); stderr:\n%s", tc.subject, exitCode, stderr)
			}
			created := callsContaining(calls, "providers create-oidc")
			if len(created) != 1 {
				t.Fatalf("expected exactly one provider create, got %d: %v", len(created), calls)
			}
			if want := "--attribute-condition=assertion.sub == '" + tc.wantPinned + "'"; !strings.Contains(created[0], want) {
				t.Errorf("the provider must be pinned to the supplied subject with %q, got:\n%s", want, created[0])
			}
			// The run completes rather than aborting on the charset guard.
			// Whether the resulting provider is usable is a separate question,
			// and for every subject but the default the answer is no.
			if !strings.Contains(stdout, "=== Done ===") {
				t.Errorf("an accepted subject must not abort the run; stdout:\n%s", stdout)
			}
		})
	}
}
