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
