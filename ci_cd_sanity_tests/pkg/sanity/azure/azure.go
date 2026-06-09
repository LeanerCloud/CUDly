package azure

import (
	"context"
	"encoding/json"
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

type azAccountShow struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
	Name     string `json:"name"`
	State    string `json:"state"`
	User     struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"user"`
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// validateAccountExpectations parses "az account show" JSON output and checks
// that the subscription and tenant IDs match expectations.
func validateAccountExpectations(opts Options, accountOut []byte) report.CheckResult {
	start := time.Now().UTC()
	check := report.CheckResult{
		Name:      "azure:account:expected_checks",
		StartedAt: start,
		Details:   map[string]string{},
	}

	var a azAccountShow
	if err := json.Unmarshal(accountOut, &a); err != nil {
		check.EndedAt = time.Now().UTC()
		check.Status = report.StatusFail
		check.Message = fmt.Sprintf("failed to parse az account show JSON: %v", err)
		check.Details["raw"] = string(accountOut)
		return check
	}

	check.EndedAt = time.Now().UTC()
	check.Details["id"] = a.ID
	check.Details["tenantId"] = a.TenantID
	check.Details["name"] = a.Name
	check.Details["state"] = a.State
	check.Details["user"] = a.User.Name

	var msgs []string
	if opts.ExpectedSubID != "" && a.ID != opts.ExpectedSubID {
		msgs = append(msgs, fmt.Sprintf("unexpected subscription: got %s want %s", a.ID, opts.ExpectedSubID))
	}
	if opts.ExpectedTenantID != "" && a.TenantID != opts.ExpectedTenantID {
		msgs = append(msgs, fmt.Sprintf("unexpected tenant: got %s want %s", a.TenantID, opts.ExpectedTenantID))
	}

	if len(msgs) == 0 {
		check.Status = report.StatusPass
	} else {
		check.Status = report.StatusFail
		check.Message = strings.Join(msgs, "; ")
	}
	return check
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

	runCmd := func(name string, args ...string) ([]byte, report.CheckResult) {
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
				"output": truncate(string(out), 2048),
			},
		}
		if err != nil {
			cr.Status = report.StatusFail
			cr.Message = err.Error()
		} else {
			cr.Status = report.StatusPass
		}
		return out, cr
	}

	// Ensure subscription context (read-only)
	_, cr := runCmd("azure:account:set", "account", "set", "--subscription", opts.SubscriptionID)
	rep.Add(cr)

	// Read-only identity/subscription info (only call once; reuse output)
	accountOut, cr := runCmd("azure:account:show", "account", "show", "-o", "json")
	rep.Add(cr)

	if opts.ExpectedSubID != "" || opts.ExpectedTenantID != "" {
		rep.Add(validateAccountExpectations(opts, accountOut))
	}

	// Read-only lists (sample)
	_, cr = runCmd("azure:group:list(sample)", "group", "list",
		"--query", "[0:10].{name:name, location:location}", "-o", "json")
	rep.Add(cr)

	_, cr = runCmd("azure:vm:list(sample)", "vm", "list",
		"--query", "[0:10].{name:name, resourceGroup:resourceGroup, location:location}", "-o", "json")
	rep.Add(cr)

	rep.EndedAt = time.Now().UTC()
	return rep, nil
}
