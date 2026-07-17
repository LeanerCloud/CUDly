package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/LeanerCloud/CUDly/ci_cd_sanity_tests/pkg/sanity/report"
)

type Options struct {
	SubscriptionID   string
	ExpectedTenantID string // optional
	ExpectedSubID    string // optional
	Timeout          time.Duration
}

// azureSubscriptionInfo holds the subscription/tenant fields extracted from the
// armsubscriptions API response. This mirrors the fields previously parsed from
// "az account show -o json" so that validateAccountExpectations is unchanged.
type azureSubscriptionInfo struct {
	ID       string
	TenantID string
	Name     string
	State    string
}

// azAccountShow is the JSON shape produced by "az account show -o json". It is
// retained only to support the existing validateAccountExpectations function
// which the unit tests exercise via its JSON parsing path.
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
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

// encodeAccountJSON serializes azureSubscriptionInfo into the same JSON shape
// that "az account show -o json" produced so that validateAccountExpectations
// can be reused without modification. The struct is composed of plain strings
// so json.Marshal cannot realistically fail; a nil return on the impossible
// error path lets the caller skip the expected-checks step rather than feed
// validateAccountExpectations a partially-encoded payload.
func encodeAccountJSON(info azureSubscriptionInfo) []byte {
	a := azAccountShow{
		ID:       info.ID,
		TenantID: info.TenantID,
		Name:     info.Name,
		State:    info.State,
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil
	}
	return b
}

// newCheckResult returns a CheckResult with name and timing already set.
func newCheckResult(name string, start time.Time) report.CheckResult {
	return report.CheckResult{
		Name:      name,
		StartedAt: start,
		Details:   map[string]string{},
	}
}

// checkPass records a passing check with an optional detail message and
// returns it ready to be added to the report.
func checkPass(cr *report.CheckResult, detail string) report.CheckResult {
	cr.EndedAt = time.Now().UTC()
	cr.Status = report.StatusPass
	if detail != "" {
		cr.Details["result"] = detail
	}
	return *cr
}

// checkFail records a failing check and returns it.
func checkFail(cr *report.CheckResult, msg string) report.CheckResult {
	cr.EndedAt = time.Now().UTC()
	cr.Status = report.StatusFail
	cr.Message = msg
	return *cr
}

// runGroupListCheck lists up to 10 resource groups in the subscription.
func runGroupListCheck(ctx context.Context, subscriptionID string, cred azcore.TokenCredential) report.CheckResult {
	cr := newCheckResult("azure:group:list(sample)", time.Now().UTC())
	cr.Details["subscriptionID"] = subscriptionID

	rgClient, err := armresources.NewResourceGroupsClient(subscriptionID, cred, nil)
	if err != nil {
		return checkFail(&cr, fmt.Sprintf("failed to create resource-groups client: %v", err))
	}

	pager := rgClient.NewListPager(nil)
	var names []string
	for pager.More() && len(names) < 10 {
		page, pageErr := pager.NextPage(ctx)
		if pageErr != nil {
			return checkFail(&cr, pageErr.Error())
		}
		for _, rg := range page.Value {
			if rg.Name != nil && rg.Location != nil {
				names = append(names, fmt.Sprintf("%s (%s)", *rg.Name, *rg.Location))
			}
			if len(names) >= 10 {
				break
			}
		}
	}
	cr.Details["result"] = truncate(strings.Join(names, ", "), 2048)
	return checkPass(&cr, "")
}

// resourceGroupFromID extracts the resource group name from an Azure resource ID.
// The ID format is: .../resourceGroups/<name>/...
func resourceGroupFromID(id string) string {
	parts := strings.Split(id, "/")
	for i, p := range parts {
		if strings.EqualFold(p, "resourceGroups") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// vmSummary returns a short display string for a virtual machine.
func vmSummary(vm *armcompute.VirtualMachine) string {
	name := ""
	rg := ""
	loc := ""
	if vm.Name != nil {
		name = *vm.Name
	}
	if vm.Location != nil {
		loc = *vm.Location
	}
	if vm.ID != nil {
		rg = resourceGroupFromID(*vm.ID)
	}
	return fmt.Sprintf("%s (rg:%s loc:%s)", name, rg, loc)
}

// runVMListCheck lists up to 10 virtual machines in the subscription.
func runVMListCheck(ctx context.Context, subscriptionID string, cred azcore.TokenCredential) report.CheckResult {
	cr := newCheckResult("azure:vm:list(sample)", time.Now().UTC())
	cr.Details["subscriptionID"] = subscriptionID

	vmClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, cred, nil)
	if err != nil {
		return checkFail(&cr, fmt.Sprintf("failed to create virtual-machines client: %v", err))
	}

	pager := vmClient.NewListAllPager(nil)
	var items []string
	for pager.More() && len(items) < 10 {
		page, pageErr := pager.NextPage(ctx)
		if pageErr != nil {
			return checkFail(&cr, pageErr.Error())
		}
		for _, vm := range page.Value {
			items = append(items, vmSummary(vm))
			if len(items) >= 10 {
				break
			}
		}
	}
	cr.Details["result"] = truncate(strings.Join(items, ", "), 2048)
	return checkPass(&cr, "")
}

// runAccountSetCheck verifies that the given subscription ID is reachable.
func runAccountSetCheck(ctx context.Context, subscriptionID string, cred azcore.TokenCredential) report.CheckResult {
	cr := newCheckResult("azure:account:set", time.Now().UTC())
	cr.Details["subscriptionID"] = subscriptionID

	subClient, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return checkFail(&cr, fmt.Sprintf("failed to create subscriptions client: %v", err))
	}

	if _, err := subClient.Get(ctx, subscriptionID, nil); err != nil {
		return checkFail(&cr, err.Error())
	}
	return checkPass(&cr, "subscription reachable")
}

// runAccountShowCheck retrieves subscription identity information.
// It returns the check result and the JSON-encoded account info (for use by
// validateAccountExpectations). The JSON is empty on failure.
func runAccountShowCheck(ctx context.Context, subscriptionID string, cred azcore.TokenCredential) (result report.CheckResult, accountJSON []byte) {
	cr := newCheckResult("azure:account:show", time.Now().UTC())
	cr.Details["subscriptionID"] = subscriptionID

	subClient, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return checkFail(&cr, fmt.Sprintf("failed to create subscriptions client: %v", err)), nil
	}

	resp, err := subClient.Get(ctx, subscriptionID, nil)
	if err != nil {
		return checkFail(&cr, err.Error()), nil
	}

	sub := resp.Subscription
	info := azureSubscriptionInfo{}
	if sub.State != nil {
		info.State = string(*sub.State)
	}
	if sub.SubscriptionID != nil {
		info.ID = *sub.SubscriptionID
	}
	if sub.TenantID != nil {
		info.TenantID = *sub.TenantID
	}
	if sub.DisplayName != nil {
		info.Name = *sub.DisplayName
	}

	cr.Details["id"] = info.ID
	cr.Details["tenantId"] = info.TenantID
	cr.Details["name"] = info.Name
	cr.Details["state"] = info.State
	return checkPass(&cr, "account info retrieved"), encodeAccountJSON(info)
}

// Run performs read-only Azure sanity checks using native SDK calls.
//
// Auth: DefaultAzureCredential is used throughout. In CI this resolves via the
// AZURE_CLIENT_ID / AZURE_TENANT_ID / AZURE_CLIENT_SECRET environment
// variables (service-principal flow). On an operator workstation it falls back
// to AzureCLICredential (i.e. the session established by "az login"), so the
// behavior is identical to the previous CLI-based implementation.
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

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		rep.EndedAt = time.Now().UTC()
		return nil, fmt.Errorf("azure: failed to build DefaultAzureCredential: %w", err)
	}

	rep.Add(runAccountSetCheck(rctx, opts.SubscriptionID, cred))

	accountShowResult, accountOut := runAccountShowCheck(rctx, opts.SubscriptionID, cred)
	rep.Add(accountShowResult)

	// --- azure:account:expected_checks ---
	if (opts.ExpectedSubID != "" || opts.ExpectedTenantID != "") && len(accountOut) > 0 {
		rep.Add(validateAccountExpectations(opts, accountOut))
	}

	// --- azure:group:list(sample) ---
	rep.Add(runGroupListCheck(rctx, opts.SubscriptionID, cred))

	// --- azure:vm:list(sample) ---
	rep.Add(runVMListCheck(rctx, opts.SubscriptionID, cred))

	rep.EndedAt = time.Now().UTC()
	return rep, nil
}
