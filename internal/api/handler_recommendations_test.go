package api

import (
	"context"
	"testing"

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
	mockScheduler.On("GetRecommendations", ctx, mock.Anything).Return([]config.RecommendationRecord{}, nil)

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
