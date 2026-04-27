package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandler_getRecommendations(t *testing.T) {
	ctx := context.Background()
	mockScheduler := new(MockScheduler)

	// Mock the scheduler to return empty recommendations
	mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return([]config.RecommendationRecord{}, nil)

	handler := &Handler{
		scheduler: mockScheduler,
		apiKey:    "test-key",
	}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"x-api-key": "test-key"},
	}

	params := map[string]string{
		"provider": "aws",
		"service":  "rds",
	}

	result, err := handler.getRecommendations(ctx, req, params)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Summary.TotalCount)
	assert.Equal(t, float64(0), result.Summary.TotalMonthlySavings)
}

func TestHandler_getRecommendationsFreshness(t *testing.T) {
	ctx := context.Background()

	t.Run("returns freshness payload", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		now := time.Now().UTC()
		mockStore.On("GetRecommendationsFreshness", ctx).
			Return(&config.RecommendationsFreshness{LastCollectedAt: &now}, nil)

		handler := &Handler{config: mockStore, apiKey: "test-key"}
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"x-api-key": "test-key"},
		}

		got, err := handler.getRecommendationsFreshness(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.NotNil(t, got.LastCollectedAt)
		assert.WithinDuration(t, now, *got.LastCollectedAt, time.Second)
	})

	t.Run("surfaces store error", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockStore.On("GetRecommendationsFreshness", ctx).Return(nil, errors.New("db down"))

		handler := &Handler{config: mockStore, apiKey: "test-key"}
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"x-api-key": "test-key"},
		}

		got, err := handler.getRecommendationsFreshness(ctx, req)
		require.Error(t, err)
		assert.Nil(t, got)
	})
}

func TestHandler_getRecommendationDetail(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	knownRecs := []config.RecommendationRecord{
		{
			ID:           "rec-known",
			Provider:     "aws",
			Service:      "ec2",
			Region:       "us-east-1",
			ResourceType: "t3.large",
			Count:        4,
			Savings:      250,
		},
	}

	t.Run("returns 400 on empty id (post-auth)", func(t *testing.T) {
		// Auth gate runs before id validation so an unauthenticated
		// caller doesn't learn the endpoint exists by probing id
		// shape — the test exercises the authenticated path.
		handler := &Handler{apiKey: "test-key"}
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"x-api-key": "test-key"},
		}

		got, err := handler.getRecommendationDetail(ctx, req, "")
		require.Error(t, err)
		assert.Nil(t, got)
		ce, ok := IsClientError(err)
		require.True(t, ok, "expected ClientError, got %T", err)
		assert.Equal(t, 400, ce.code)
	})

	t.Run("returns errNotFound on unknown id", func(t *testing.T) {
		mockScheduler := new(MockScheduler)
		mockScheduler.On("ListRecommendations", ctx, mock.Anything).
			Return(knownRecs, nil)

		handler := &Handler{
			scheduler: mockScheduler,
			apiKey:    "test-key",
		}
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"x-api-key": "test-key"},
		}

		got, err := handler.getRecommendationDetail(ctx, req, "rec-missing")
		require.Error(t, err)
		assert.Nil(t, got)
		assert.True(t, IsNotFoundError(err), "expected 404 not-found, got %v", err)
	})

	t.Run("returns 200 with the expected shape for a known id", func(t *testing.T) {
		mockScheduler := new(MockScheduler)
		mockScheduler.On("ListRecommendations", ctx, mock.Anything).
			Return(knownRecs, nil)

		mockStore := new(MockConfigStore)
		mockStore.On("GetRecommendationsFreshness", ctx).
			Return(&config.RecommendationsFreshness{LastCollectedAt: &now}, nil)

		handler := &Handler{
			scheduler: mockScheduler,
			config:    mockStore,
			apiKey:    "test-key",
		}
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"x-api-key": "test-key"},
		}

		got, err := handler.getRecommendationDetail(ctx, req, "rec-known")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "rec-known", got.ID)
		// Empty (not nil) so json.Marshal renders [] rather than null —
		// the frontend's `usage_history.length === 0` check needs the
		// array form, not a null.
		assert.NotNil(t, got.UsageHistory)
		assert.Equal(t, 0, len(got.UsageHistory))
		// $250/mo savings + 4-instance fleet → high (matches the
		// frontend shim's thresholds 1:1).
		assert.Equal(t, "high", got.ConfidenceBucket)
		assert.Contains(t, got.ProvenanceNote, "AWS")
		assert.Contains(t, got.ProvenanceNote, "ec2")
		assert.Contains(t, got.ProvenanceNote, "last collected")
	})

	t.Run("provenance degrades gracefully when freshness is unavailable", func(t *testing.T) {
		mockScheduler := new(MockScheduler)
		mockScheduler.On("ListRecommendations", ctx, mock.Anything).
			Return(knownRecs, nil)

		mockStore := new(MockConfigStore)
		mockStore.On("GetRecommendationsFreshness", ctx).
			Return(nil, errors.New("db down"))

		handler := &Handler{
			scheduler: mockScheduler,
			config:    mockStore,
			apiKey:    "test-key",
		}
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"x-api-key": "test-key"},
		}

		got, err := handler.getRecommendationDetail(ctx, req, "rec-known")
		require.NoError(t, err)
		require.NotNil(t, got)
		// The drawer still renders a useful provenance line on
		// freshness backend failure — drop the "last collected …"
		// suffix rather than 500ing the whole detail call.
		assert.NotEmpty(t, got.ProvenanceNote)
		assert.NotContains(t, got.ProvenanceNote, "last collected")
	})
}

func TestConfidenceBucketFor(t *testing.T) {
	cases := []struct {
		name    string
		savings float64
		count   int
		want    string
	}{
		{"high requires both signals", 250, 4, "high"},
		{"savings without fleet falls to medium", 250, 1, "medium"},
		{"medium on savings alone", 60, 1, "medium"},
		{"low when neither threshold is met", 10, 1, "low"},
		{"count clamped to >=1 — savings still drives bucket", 60, 0, "medium"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, confidenceBucketFor(tc.savings, tc.count))
		})
	}
}
