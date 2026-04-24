package api

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/commitmentopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubCommitmentOpts lets handler tests drive the endpoint without the full
// probe+store machinery. Mirrors the CommitmentOptsInterface surface.
type stubCommitmentOpts struct {
	getFn      func(ctx context.Context) (commitmentopts.Options, error)
	validateFn func(ctx context.Context, provider, service string, term int, payment string) (bool, error)
}

func (s *stubCommitmentOpts) Get(ctx context.Context) (commitmentopts.Options, error) {
	return s.getFn(ctx)
}

func (s *stubCommitmentOpts) Validate(ctx context.Context, provider, service string, term int, payment string) (bool, error) {
	return s.validateFn(ctx, provider, service, term, payment)
}

func TestGetCommitmentOptions_NilService_ReturnsUnavailable(t *testing.T) {
	h := &Handler{commitmentOpts: nil}

	resp, err := h.getCommitmentOptions(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "unavailable", resp.Status)
	assert.Nil(t, resp.AWS)
}

func TestGetCommitmentOptions_ErrNoData_ReturnsUnavailable(t *testing.T) {
	h := &Handler{commitmentOpts: &stubCommitmentOpts{
		getFn: func(context.Context) (commitmentopts.Options, error) {
			return nil, commitmentopts.ErrNoData
		},
	}}

	resp, err := h.getCommitmentOptions(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "unavailable", resp.Status)
	assert.Nil(t, resp.AWS)
}

func TestGetCommitmentOptions_UnexpectedError_Propagates(t *testing.T) {
	boom := errors.New("database exploded")
	h := &Handler{commitmentOpts: &stubCommitmentOpts{
		getFn: func(context.Context) (commitmentopts.Options, error) {
			return nil, boom
		},
	}}

	resp, err := h.getCommitmentOptions(context.Background())

	require.ErrorIs(t, err, boom)
	assert.Nil(t, resp)
}

func TestGetCommitmentOptions_EmptyAWS_CollapsesToUnavailable(t *testing.T) {
	// Probe succeeded but returned nothing for AWS. Treating this as
	// unavailable keeps the frontend on its hardcoded fallback rather than
	// rendering an empty constraint set.
	h := &Handler{commitmentOpts: &stubCommitmentOpts{
		getFn: func(context.Context) (commitmentopts.Options, error) {
			return commitmentopts.Options{"aws": map[string][]commitmentopts.Combo{}}, nil
		},
	}}

	resp, err := h.getCommitmentOptions(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "unavailable", resp.Status)
	assert.Nil(t, resp.AWS)
}

func TestGetCommitmentOptions_Success_ReturnsAWSCombos(t *testing.T) {
	h := &Handler{commitmentOpts: &stubCommitmentOpts{
		getFn: func(context.Context) (commitmentopts.Options, error) {
			return commitmentopts.Options{
				"aws": map[string][]commitmentopts.Combo{
					"rds": {
						{Provider: "aws", Service: "rds", TermYears: 1, Payment: "all-upfront"},
						{Provider: "aws", Service: "rds", TermYears: 1, Payment: "partial-upfront"},
						{Provider: "aws", Service: "rds", TermYears: 3, Payment: "all-upfront"},
					},
					"elasticache": {
						{Provider: "aws", Service: "elasticache", TermYears: 1, Payment: "no-upfront"},
						{Provider: "aws", Service: "elasticache", TermYears: 3, Payment: "no-upfront"},
					},
				},
			}, nil
		},
	}}

	resp, err := h.getCommitmentOptions(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
	assert.Len(t, resp.AWS["rds"], 3)
	assert.Len(t, resp.AWS["elasticache"], 2)
	// Spot-check the payload shape: term/payment only, no provider/service echo.
	assert.Contains(t, resp.AWS["elasticache"], commitmentOptionCombo{Term: 3, Payment: "no-upfront"})
}
