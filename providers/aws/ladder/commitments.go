package ladder

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/ladder"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
)

// ListCommitments returns all active commitments for the given scope by merging:
//   - Active convertible RIs (riLister.ListConvertibleReservedInstances)
//   - Active Savings Plans of type EC2Instance and Compute (spLister.ListActiveSPs)
//
// The scope's AccountID must match Config.AccountID; this implementation is
// single-account and returns an error when the scope targets a different account.
func (a *AWSLadder) ListCommitments(ctx context.Context, scope ladder.Scope) ([]common.Commitment, error) {
	if err := a.validateScope(scope); err != nil {
		return nil, err
	}

	riCommitments, err := a.listRICommitments(ctx)
	if err != nil {
		return nil, fmt.Errorf("ListCommitments: RI listing failed: %w", err)
	}

	spCommitments, err := a.listSPCommitments(ctx)
	if err != nil {
		return nil, fmt.Errorf("ListCommitments: SP listing failed: %w", err)
	}

	result := make([]common.Commitment, 0, len(riCommitments)+len(spCommitments))
	result = append(result, riCommitments...)
	result = append(result, spCommitments...)
	return result, nil
}

// validateScope returns an error when scope targets a provider or account that
// does not match this AWSLadder instance. Fails loud rather than silently
// returning data for the wrong account.
func (a *AWSLadder) validateScope(scope ladder.Scope) error {
	if scope.Provider != common.ProviderAWS {
		return fmt.Errorf("AWSLadder: expected provider %s, got %s", common.ProviderAWS, scope.Provider)
	}
	if scope.AccountID != a.cfg.AccountID {
		return fmt.Errorf("AWSLadder: scope account %s does not match configured account %s",
			scope.AccountID, a.cfg.AccountID)
	}
	return nil
}

// listRICommitments fetches active convertible RIs and maps them to
// common.Commitment values. Only RIs in "active" state are included;
// payment-pending RIs are excluded because their hourly costs are not
// yet finalized.
func (a *AWSLadder) listRICommitments(ctx context.Context) ([]common.Commitment, error) {
	ris, err := a.ris.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]common.Commitment, 0, len(ris))
	for i := range ris {
		out = append(out, riToCommitment(&ris[i], a.cfg.AccountID, a.cfg.Region))
	}
	return out, nil
}

// riToCommitment converts a ConvertibleRI to a common.Commitment.
// ri is taken by pointer to avoid copying the large ConvertibleRI struct (hugeParam).
//
// Cost is the RESERVATION-TOTAL hourly amortized cost, computed by riHourlyCost:
// DescribeReservedInstances pricing fields (RecurringHourlyAmount, UsagePrice,
// FixedPrice) are per-instance, so the per-instance hourly rate is multiplied
// by InstanceCount. See riHourlyCost for the formula and semantics (matches the
// repo's canonical monthlyCostFromConvertibleRI in internal/api/handler_ri_exchange.go).
func riToCommitment(ri *ec2svc.ConvertibleRI, accountID, region string) common.Commitment {
	totalHourlyCost := riHourlyCost(ri)

	return common.Commitment{
		Provider:       common.ProviderAWS,
		Account:        accountID,
		CommitmentID:   ri.ReservedInstanceID,
		CommitmentType: common.CommitmentReservedInstance,
		Service:        common.ServiceEC2,
		Region:         region,
		ResourceType:   ri.InstanceType,
		Count:          int(ri.InstanceCount),
		State:          ri.State,
		StartDate:      ri.Start,
		EndDate:        ri.End,
		Cost:           totalHourlyCost,
	}
}

// listSPCommitments fetches active EC2Instance and Compute Savings Plans and
// maps them to common.Commitment values. Other plan types (SageMaker, Database)
// are filtered out because they do not belong to any of the three ladder layers.
func (a *AWSLadder) listSPCommitments(ctx context.Context) ([]common.Commitment, error) {
	sps, err := a.sps.ListActiveSPs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]common.Commitment, 0, len(sps))
	for i := range sps {
		if !isLadderSPType(sps[i].PlanType) {
			continue
		}
		out = append(out, spToCommitment(&sps[i], a.cfg.AccountID))
	}
	return out, nil
}

// isLadderSPType returns true for the two plan types that map to ladder layers.
func isLadderSPType(planType string) bool {
	return planType == "EC2Instance" || planType == "Compute"
}

// spToCommitment converts an ActiveSP to a common.Commitment.
// sp is taken by pointer to avoid copying the large ActiveSP struct (hugeParam).
// Cost is set to HourlyCommitmentUSD, which is the $/hr committed spend.
// The end date carries a zero value when AWS returns an empty End string
// (e.g. for queued plans); callers treat the zero time as "no expiry signal".
func spToCommitment(sp *ActiveSP, accountID string) common.Commitment {
	service := common.ServiceSavingsPlansEC2Instance
	if sp.PlanType == "Compute" {
		service = common.ServiceSavingsPlansCompute
	}

	return common.Commitment{
		Provider:       common.ProviderAWS,
		Account:        accountID,
		CommitmentID:   sp.PlanID,
		CommitmentType: common.CommitmentSavingsPlan,
		Service:        service,
		Region:         sp.Region,
		ResourceType:   sp.PlanType,
		Count:          1, // Savings Plans are single commitment units
		State:          sp.State,
		StartDate:      sp.StartDate,
		EndDate:        sp.EndDate,
		Cost:           sp.HourlyCommitmentUSD,
	}
}
