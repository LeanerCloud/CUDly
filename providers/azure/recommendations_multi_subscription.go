// Package azure provides the org-wide (multi-subscription) recommendations
// fan-out client.
package azure

import (
	"context"
	"fmt"
	"strings"

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

// SubscriptionFailure records one subscription that could not be queried
// during an org-wide fan-out.
type SubscriptionFailure struct {
	SubscriptionID string
	Err            error
}

// PartialSubscriptionFailureError reports that an org-wide fan-out completed
// with some subscriptions queried successfully and others not.
//
// It is returned ALONGSIDE the successful subscriptions' recommendations, so
// a caller can keep the partial data and still know the sweep was
// incomplete. Callers that want the data must inspect the error:
//
//	recs, err := client.GetAllRecommendations(ctx)
//	var partial *azure.PartialSubscriptionFailureError
//	if errors.As(err, &partial) {
//	    // recs holds partial.Succeeded subscriptions' recommendations;
//	    // partial.Failed says which subscriptions are missing and why.
//	} else if err != nil {
//	    return err
//	}
//
// This exists because the alternative -- returning the partial results with
// a nil error -- makes "these subscriptions have no savings available"
// indistinguishable from "these subscriptions were never successfully
// queried". On a collection path whose output is persisted and rendered as
// a savings opportunity, that reads as a shrinking opportunity rather than a
// failed sweep. A log line is not a programmatic signal; this is.
//
// Failing the whole sweep on one transient subscription error would be worse
// than a partial result, which is why the successful data is still returned.
type PartialSubscriptionFailureError struct {
	// Attempted is how many subscriptions the fan-out queried.
	Attempted int
	// Succeeded is how many returned a result. Always < Attempted and > 0:
	// an all-failed sweep is a plain error, not a partial one.
	Succeeded int
	// Failed carries every subscription that errored, with its cause.
	Failed []SubscriptionFailure
}

func (e *PartialSubscriptionFailureError) Error() string {
	ids := make([]string, 0, len(e.Failed))
	for _, f := range e.Failed {
		ids = append(ids, f.SubscriptionID)
	}
	return fmt.Sprintf(
		"azure recommendations incomplete: %d of %d subscriptions succeeded; %d failed (%s): %v",
		e.Succeeded, e.Attempted, len(e.Failed), strings.Join(ids, ", "), e.Failed[0].Err)
}

// Unwrap exposes the per-subscription causes so errors.Is/errors.As can match
// against any of them (e.g. checking whether a throttling error is in play).
func (e *PartialSubscriptionFailureError) Unwrap() []error {
	errs := make([]error, 0, len(e.Failed))
	for _, f := range e.Failed {
		errs = append(errs, f.Err)
	}
	return errs
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
//
// If SOME subscriptions fail, it returns the successful subscriptions'
// recommendations together with a *PartialSubscriptionFailureError. Callers
// that want the partial data must inspect the error with errors.As; a caller
// that treats any non-nil error as fatal gets a loud failure rather than a
// silently incomplete sweep. Either way the incompleteness is visible in the
// return values, not just in a log line.
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
	failed := make([]SubscriptionFailure, 0, len(subs))
	for i, err := range errs {
		if err != nil {
			failed = append(failed, SubscriptionFailure{SubscriptionID: subs[i].subscriptionID, Err: err})
			logging.Warnf("Azure subscription %s recommendations: %v", subs[i].subscriptionID, err)
			continue
		}
		out = append(out, results[i]...)
	}

	if len(failed) == 0 {
		return out, nil
	}
	if len(failed) == len(subs) {
		return nil, fmt.Errorf("all %d Azure subscriptions failed to return recommendations: %w", len(failed), failed[0].Err)
	}

	// Partial sweep: hand back what succeeded AND a typed error saying what
	// did not, so the caller can tell an incomplete sweep from a complete one
	// that happened to find nothing. See PartialSubscriptionFailureError.
	return out, &PartialSubscriptionFailureError{
		Attempted: len(subs),
		Succeeded: len(subs) - len(failed),
		Failed:    failed,
	}
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
