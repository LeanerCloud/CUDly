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
// across the Azure subscriptions accessible to the authenticated principal --
// every one of them by default, or the subset named by
// RecommendationParams.AccountFilter (see selectSubscriptions).
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

// GetRecommendations fans params out concurrently (errgroup) to the
// subscriptions selected by selectSubscriptions -- every accessible
// subscription unless params.AccountFilter narrows it -- and merges the
// results.
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

	targets, err := m.selectSubscriptions(params.AccountFilter)
	if err != nil {
		return nil, err
	}

	results := make([][]common.Recommendation, len(targets))
	errs := make([]error, len(targets))

	g, gctx := errgroup.WithContext(ctx)
	for i, sub := range targets {
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

	return mergeSubscriptionResults(targets, results, errs)
}

// selectSubscriptions narrows the fan-out to params.AccountFilter.
//
// AccountFilter is a scoping control, not a display convenience: the AWS
// provider applies it to every recommendation it returns (filterByAccounts in
// providers/aws/service_client.go), so a caller that scopes a request to a
// subset of accounts must not be handed another account's data by the Azure
// path either. Before org-wide fan-out existed this was moot -- a
// subscription-scoped client could only ever return its own subscription --
// but a client covering every visible subscription has to honour the filter
// or it silently widens the caller's scope.
//
// Filtering BEFORE the fan-out (rather than discarding rows afterwards, as
// AWS does) also avoids issuing ARM calls against subscriptions the caller
// never asked about.
//
// An empty filter means "every visible subscription" -- the org-wide default
// this client exists to provide. A non-empty filter that matches nothing is
// an error rather than an empty result: returning zero recommendations would
// be indistinguishable from "these subscriptions have no savings available".
func (m *MultiSubscriptionRecommendationsClient) selectSubscriptions(filter []string) ([]subscriptionClient, error) {
	if len(filter) == 0 {
		return m.subscriptions, nil
	}

	wanted := make(map[string]struct{}, len(filter))
	for _, id := range filter {
		wanted[id] = struct{}{}
	}

	selected := make([]subscriptionClient, 0, len(m.subscriptions))
	for _, sub := range m.subscriptions {
		if _, ok := wanted[sub.subscriptionID]; ok {
			selected = append(selected, sub)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf(
			"azure multi-subscription recommendations: account filter %v matches none of the %d accessible subscriptions",
			filter, len(m.subscriptions))
	}
	return selected, nil
}

// mergeSubscriptionResults concatenates successful per-subscription results,
// logging a warning for each subscription that failed, and applies the
// all-attempted-failed guard described in GetRecommendations' doc comment.
// subs, results and errs are index-aligned.
func mergeSubscriptionResults(subs []subscriptionClient, results [][]common.Recommendation, errs []error) ([]common.Recommendation, error) {
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
			logging.Warnf("Azure subscription %s recommendations: %v", subs[i].subscriptionID, err)
			continue
		}
		out = append(out, results[i]...)
	}

	if failures > 0 && failures == len(subs) {
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
