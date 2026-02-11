package azure

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/ci_cd_sanity_tests/pkg/sanity/report"
)

type Options struct {
	SubscriptionID   string
	ExpectedTenantID string // optional
	ExpectedSubID    string // optional
	Timeout          time.Duration
}

func Run(ctx context.Context, opts Options) (*report.Report, error) {
	if opts.SubscriptionID == "" {
		opts.SubscriptionID = os.Getenv("AZURE_SUBSCRIPTION_ID")
	}
	if opts.SubscriptionID == "" {
		return nil, fmt.Errorf("missing Azure subscription id: set AZURE_SUBSCRIPTION_ID or pass --subscription-id")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}

	rctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	rep := &report.Report{
		RunID:     fmt.Sprintf("azure-%d", time.Now().Unix()),
		Cloud:     "azure",
		Mode:      "dry-run",
		StartedAt: time.Now().UTC(),
	}

	runCmd := func(name string, args ...string) report.CheckResult {
		start := time.Now().UTC()
		cmd := exec.CommandContext(rctx, "az", args...)
		out, err := cmd.CombinedOutput()
		end := time.Now().UTC()

		cr := report.CheckResult{
			Name:      name,
			StartedAt: start,
			EndedAt:   end,
			Details: map[string]string{
				"cmd":    "az " + strings.Join(args, " "),
				"output": string(out),
			},
		}
		if err != nil {
			cr.Status = report.StatusFail
			cr.Message = err.Error()
		} else {
			cr.Status = report.StatusPass
		}
		return cr
	}

	// Ensure subscription context (read-only)
	rep.Add(runCmd("azure:account:set", "account", "set", "--subscription", opts.SubscriptionID))

	// Read-only identity/subscription info
	rep.Add(runCmd("azure:account:show", "account", "show", "-o", "json"))

	// Optional tenant/subscription checks (portable): read from az account show output
	// We do it by re-running az account show and parsing minimally (string contains checks).
	// (Keeps dependencies minimal.)
	if opts.ExpectedSubID != "" || opts.ExpectedTenantID != "" {
		start := time.Now().UTC()
		cmd := exec.CommandContext(rctx, "az", "account", "show", "-o", "json")
		out, err := cmd.CombinedOutput()
		end := time.Now().UTC()

		cr := report.CheckResult{
			Name:      "azure:account:expected_checks",
			StartedAt: start,
			EndedAt:   end,
			Details: map[string]string{
				"output": string(out),
			},
		}
		if err != nil {
			cr.Status = report.StatusFail
			cr.Message = err.Error()
		} else {
			// very lightweight checks (no JSON lib needed)
			ok := true
			if opts.ExpectedSubID != "" && !strings.Contains(string(out), fmt.Sprintf(`"id": "%s"`, opts.ExpectedSubID)) {
				ok = false
				cr.Message = fmt.Sprintf("expected subscription %s not found in az account show output", opts.ExpectedSubID)
			}
			if opts.ExpectedTenantID != "" && !strings.Contains(string(out), fmt.Sprintf(`"tenantId": "%s"`, opts.ExpectedTenantID)) {
				ok = false
				if cr.Message == "" {
					cr.Message = fmt.Sprintf("expected tenant %s not found in az account show output", opts.ExpectedTenantID)
				} else {
					cr.Message += "; " + fmt.Sprintf("expected tenant %s not found", opts.ExpectedTenantID)
				}
			}
			if ok {
				cr.Status = report.StatusPass
			} else {
				cr.Status = report.StatusFail
			}
		}
		rep.Add(cr)
	}

	// Read-only lists (sample)
	rep.Add(runCmd("azure:group:list(sample)", "group", "list", "--query", "[0:10].{name:name, location:location}", "-o", "json"))
	rep.Add(runCmd("azure:vm:list(sample)", "vm", "list", "--query", "[0:10].{name:name, resourceGroup:resourceGroup, location:location}", "-o", "json"))

	rep.EndedAt = time.Now().UTC()
	return rep, nil
}
