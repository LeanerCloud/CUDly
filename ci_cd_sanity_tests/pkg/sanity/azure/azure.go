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
				"output": string(out),
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

	// Robust expected checks via JSON parsing (no fragile string matching)
	if opts.ExpectedSubID != "" || opts.ExpectedTenantID != "" {
		start := time.Now().UTC()
		check := report.CheckResult{
			Name:      "azure:account:expected_checks",
			StartedAt: start,
			Details:   map[string]string{},
		}

		var a azAccountShow
		err := json.Unmarshal(accountOut, &a)
		end := time.Now().UTC()
		check.EndedAt = end

		if err != nil {
			check.Status = report.StatusFail
			check.Message = fmt.Sprintf("failed to parse az account show JSON: %v", err)
			check.Details["raw"] = string(accountOut)
			rep.Add(check)
		} else {
			check.Details["id"] = a.ID
			check.Details["tenantId"] = a.TenantID
			check.Details["name"] = a.Name
			check.Details["state"] = a.State
			check.Details["user"] = a.User.Name

			ok := true
			msg := ""

			if opts.ExpectedSubID != "" && a.ID != opts.ExpectedSubID {
				ok = false
				msg += fmt.Sprintf("unexpected subscription: got %s want %s; ", a.ID, opts.ExpectedSubID)
			}
			if opts.ExpectedTenantID != "" && a.TenantID != opts.ExpectedTenantID {
				ok = false
				msg += fmt.Sprintf("unexpected tenant: got %s want %s; ", a.TenantID, opts.ExpectedTenantID)
			}

			if ok {
				check.Status = report.StatusPass
			} else {
				check.Status = report.StatusFail
				check.Message = strings.TrimSpace(msg)
			}
			rep.Add(check)
		}
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
