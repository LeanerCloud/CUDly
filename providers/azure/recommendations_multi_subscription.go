// Package azure provides the org-wide (multi-subscription) recommendations
// fan-out client.
package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"golang.org/x/sync/errgroup"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// newSubscriptionRecommendationsClientFn builds the per-subscription
// recommendations client. Declared as a package-level var (default:
// NewRecommendationsClientAdapter) so tests can substitute a fake
// per-subscription client and exercise fan-out/merge behavior without
// issuing real ARM calls. Mirrors the newComputeClientFn-style injection
// used by RecommendationsClientAdapter in recommendations.go.
var newSubscriptionRecommendationsClientFn = func(cred azcore.TokenCredential, subscriptionID string) (provider.RecommendationsClient, error) {
	return NewRecommendationsClientAdapter(cred, subscriptionID)
}

// subscriptionClient pairs a subscription ID with its recommendations
// client so fan-out logs and error messages can identify which subscription
// a failure came from.
type subscriptionClient struct {
	subscriptionID string
	client         provider.RecommendationsClient
}

// MultiSubscriptionRecommendationsClient fans recommendation collection out
// across every Azure subscription accessible to the authenticated
// principal.
//
// Azure has no organization-wide equivalent of AWS Cost Explorer's
// AccountScope=Linked: the Consumption Reservation Recommendations and
// Advisor APIs are subscription-scoped. Achieving AWS-parity org-wide
// coverage therefore requires calling the per-subscription
// RecommendationsClientAdapter once per subscription and aggregating the
// results client-side, which is what this type does.
type MultiSubscriptionRecommendationsClient struct {
	subscriptions []subscriptionClient
}

// NewMultiSubscriptionRecommendationsClient builds a fan-out client covering
// every account in accounts. Returns an error when accounts is empty (there
// is nothing to fan out to) or when building the per-subscription client
// fails for any account -- fail loud rather than silently dropping a
// subscription that should have been covered.
func NewMultiSubscriptionRecommendationsClient(cred azcore.TokenCredential, accounts []common.Account) (*MultiSubscriptionRecommendationsClient, error) {
	if len(accounts) == 0 {
		return nil, fmt.Errorf("azure multi-subscription recommendations: at least one subscription is required")
	}

	subscriptions := make([]subscriptionClient, 0, len(accounts))
	for _, account := range accounts {
		client, err := newSubscriptionRecommendationsClientFn(cred, account.ID)
		if err != nil {
			return nil, fmt.Errorf("azure multi-subscription recommendations: failed to build client for subscription %s: %w", account.ID, err)
		}
		subscriptions = append(subscriptions, subscriptionClient{subscriptionID: account.ID, client: client})
	}

	return &MultiSubscriptionRecommendationsClient{subscriptions: subscriptions}, nil
}

// GetRecommendations fans params out to every subscription concurrently
// (errgroup) and merges the results.
//
// Error isolation mirrors RecommendationsClientAdapter.GetRecommendations:
// each per-subscription goroutine captures its own error and returns nil to
// the group, so one subscription failing (e.g. the principal lost Reader
// access mid-run, or a subscription-specific throttle) never cancels
// sibling subscriptions. The semaphore that bounds aggregate concurrent ARM
// calls is acquired inside each per-subscription client's own
// GetRecommendations (around the outbound API calls, not around this
// fan-out), so no additional semaphore is needed at this layer.
//
// After g.Wait(), ctx.Err() is checked explicitly: g.Wait() only reports
// errors returned to the group, and every goroutine here returns nil, so a
// parent-context cancellation would otherwise go unnoticed.
//
// If every subscription fails, GetRecommendations returns a wrapped error
// instead of a silently empty, nil-error result -- the same
// all-attempted-failed guard used by mergeServiceResults, ported here so a
// total credential/throttle failure isn't indistinguishable from "no
// savings available across the whole tenant".
func (m *MultiSubscriptionRecommendationsClient) GetRecommendations(ctx context.Context, params *common.RecommendationParams) ([]common.Recommendation, error) {
	if params == nil {
		return nil, fmt.Errorf("params cannot be nil")
	}

	results := make([][]common.Recommendation, len(m.subscriptions))
	errs := make([]error, len(m.subscriptions))

	g, gctx := errgroup.WithContext(ctx)
	for i, sub := range m.subscriptions {
		i, sub := i, sub
		g.Go(func() error {
			recs, err := sub.client.GetRecommendations(gctx, params)
			results[i] = recs
			errs[i] = err
			return nil // error isolation: never propagate to errgroup
		})
	}
	if err := g.Wait(); err != nil {
		// Unreachable in practice -- every goroutine above returns nil -- but
		// handled explicitly (rather than discarded) so a future change that
		// starts propagating a goroutine error isn't silently swallowed.
		return nil, err
	}

	// Propagate parent-context cancellation explicitly -- see doc comment.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return m.mergeResults(results, errs)
}

// mergeResults concatenates successful per-subscription results, logging a
// warning for each subscription that failed, and applies the
// all-attempted-failed guard described in GetRecommendations' doc comment.
func (m *MultiSubscriptionRecommendationsClient) mergeResults(results [][]common.Recommendation, errs []error) ([]common.Recommendation, error) {
	total := 0
	for _, r := range results {
		total += len(r)
	}

	out := make([]common.Recommendation, 0, total)
	failures := 0
	var lastErr error
	for i, err := range errs {
		if err != nil {
			failures++
			lastErr = err
			logging.Warnf("Azure subscription %s recommendations: %v", m.subscriptions[i].subscriptionID, err)
			continue
		}
		out = append(out, results[i]...)
	}

	if failures > 0 && failures == len(m.subscriptions) {
		return nil, fmt.Errorf("all %d Azure subscriptions failed to return recommendations: %w", failures, lastErr)
	}
	return out, nil
}

// GetRecommendationsForService retrieves recommendations for a single
// service across every subscription.
func (m *MultiSubscriptionRecommendationsClient) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	return m.GetRecommendations(ctx, &common.RecommendationParams{Service: service})
}

// GetAllRecommendations retrieves recommendations for every supported
// service across every subscription.
func (m *MultiSubscriptionRecommendationsClient) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	return m.GetRecommendations(ctx, &common.RecommendationParams{})
}

// Compile-time interface compliance check.
var _ provider.RecommendationsClient = (*MultiSubscriptionRecommendationsClient)(nil)
