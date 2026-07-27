package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// TestSearchRecommendationsFansOutAllCombosWhenTermAndPaymentOmitted is the
// regression guard for the defect where "omitted means search all" was false
// for reservations. The tool sent Cost Explorer an empty
// TermInYears/PaymentOption, AWS silently applied its own 1yr/all-upfront
// default, and the caller got ONE of six purchasable offers while the tool's
// own schema claimed it had searched them all -- on a real account that hid
// the best offer, a 3yr/all-upfront saving 63% where the returned
// 1yr/all-upfront saved 40% on the identical instance.
func TestSearchRecommendationsFansOutAllCombosWhenTermAndPaymentOmitted(t *testing.T) {
	t.Parallel()
	recs := []common.Recommendation{{Provider: common.ProviderAWS, ResourceType: "t4g.nano", Count: 1}}
	client := &fakeRecommendationsClient{recs: recs}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	_, result, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "ec2",
		Region:   "eu-west-1",
	})

	require.NoError(t, err)
	assert.ElementsMatch(t, []searchCombo{
		{term: "1yr", payment: "all-upfront"},
		{term: "1yr", payment: "partial-upfront"},
		{term: "1yr", payment: "no-upfront"},
		{term: "3yr", payment: "all-upfront"},
		{term: "3yr", payment: "partial-upfront"},
		{term: "3yr", payment: "no-upfront"},
	}, client.combos(), "every purchasable term/payment offer must be searched")
	// Every combo's recommendations reach the caller, so the full menu is
	// returned rather than one arbitrary cell of it.
	assert.Equal(t, 6, result.Count)
	assert.Len(t, result.Recommendations, 6)
	// Shared fields still ride along on each fanned-out call.
	for i, p := range client.allParams {
		assert.Equalf(t, "eu-west-1", p.Region, "call %d lost the region filter", i)
	}
}

// TestSearchRecommendationsFansOutOverPaymentOptionsOnly proves the fan-out
// expands only the OMITTED dimension: a caller who pinned term_years=3 must
// get the three payment variants of a 3yr commitment, not all six combos.
func TestSearchRecommendationsFansOutOverPaymentOptionsOnly(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:  "aws",
		Service:   "ec2",
		TermYears: 3,
	})

	require.NoError(t, err)
	assert.ElementsMatch(t, []searchCombo{
		{term: "3yr", payment: "all-upfront"},
		{term: "3yr", payment: "partial-upfront"},
		{term: "3yr", payment: "no-upfront"},
	}, client.combos())
}

// TestSearchRecommendationsFansOutOverTermsOnly is the mirror of the above:
// pinning payment_option must expand only the term dimension.
func TestSearchRecommendationsFansOutOverTermsOnly(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:      "aws",
		Service:       "ec2",
		PaymentOption: "all-upfront",
	})

	require.NoError(t, err)
	assert.ElementsMatch(t, []searchCombo{
		{term: "1yr", payment: "all-upfront"},
		{term: "3yr", payment: "all-upfront"},
	}, client.combos())
}

// TestSearchRecommendationsSavingsPlansDoesNotFanOut proves the fan-out is
// scoped to reservations. A Savings Plans search already resolves to exactly
// one required (term, payment, lookback) triple via
// applySavingsPlansSearchDefaults, so fanning out would re-issue queries the
// caller never asked for and return duplicate offers.
func TestSearchRecommendationsSavingsPlansDoesNotFanOut(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{}
	fp := &fakeProvider{
		name:      "aws",
		services:  []common.ServiceType{common.ServiceSavingsPlansCompute},
		recClient: client,
	}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "savings-plans-compute",
	})

	require.NoError(t, err)
	assert.Equal(t, 1, client.calls)
	assert.Equal(t, []searchCombo{{term: "1yr", payment: "no-upfront"}}, client.combos())
}

// TestSearchRecommendationsNonAWSDoesNotFanOut proves the fan-out is scoped
// to AWS. Azure derives term/payment from its own Advisor response rather
// than taking them as request filters, and GCP has no term/payment concept
// in its recommendations path, so fanning out there would issue identical
// repeat queries and duplicate every result.
func TestSearchRecommendationsNonAWSDoesNotFanOut(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{}
	fp := &fakeProvider{name: "azure", services: []common.ServiceType{common.ServiceCompute}, recClient: client}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "azure",
		Service:  "compute",
	})

	require.NoError(t, err)
	assert.Equal(t, 1, client.calls)
}

// TestSearchRecommendationsComboFailureFailsWholeSearch proves a failing
// combo is NOT skipped. Returning the five combos that succeeded is
// indistinguishable from "these are all your options" and would recreate the
// very defect the fan-out exists to fix, so the search fails loud and names
// the combo that broke.
func TestSearchRecommendationsComboFailureFailsWholeSearch(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{
		recs:        []common.Recommendation{{Provider: common.ProviderAWS, ResourceType: "t4g.nano"}},
		errOnCall:   3,
		errForCombo: errors.New("throttled"),
	}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	_, result, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "ec2",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "throttled")
	assert.Contains(t, err.Error(), "term=1yr", "the error must name the combo that failed")
	assert.Contains(t, err.Error(), "payment_option=no-upfront")
	assert.Empty(t, result.Recommendations, "a partial result set must never be returned")
}

// TestSearchRecommendationsEmptyResultIsEmptySliceNotNull pins the JSON shape
// of a no-results search: an empty slice serializes as [], where a nil slice
// serializes as null. Both showed up across services before this, so a
// client parsing the response had to handle two shapes for the same "nothing
// found" answer.
func TestSearchRecommendationsEmptyResultIsEmptySliceNotNull(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{recs: nil}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	_, result, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "ec2",
	})

	require.NoError(t, err)
	assert.Equal(t, 0, result.Count)
	assert.NotNil(t, result.Recommendations)
	assert.Empty(t, result.Recommendations)
}
