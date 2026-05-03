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

// TestGetRecommendations_AccountIDFilter exercises the five cases documented
// in the issue #211 table. The DB-layer SQL semantics (NULL row exclusion)
// are tested indirectly: the mock returns only what the SQL filter would
// return for each case, so the handler contract is verified end-to-end.
//
// Case 5 (disabled-account exclusion) is enforced by
// filterRecommendationsByAllowedAccounts — that layer is exercised via the
// user's allowed-account list, not the account_ids param. It is not tested
// here to keep scope narrow; its own unit tests live in handler.go.
func TestGetRecommendations_AccountIDFilter(t *testing.T) {
	ctx := context.Background()
	validUUID := "12345678-1234-1234-1234-123456789abc"
	otherUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	acctPtr := func(s string) *string { return &s }

	// Fixtures matching the issue table.
	recWithAccount := config.RecommendationRecord{
		ID: "rec-with-account", Provider: "aws", Service: "ec2",
		Region: "us-east-1", CloudAccountID: acctPtr(validUUID),
	}
	recNullAccount := config.RecommendationRecord{
		ID: "rec-null-account", Provider: "aws", Service: "ec2",
		Region: "us-east-1", CloudAccountID: nil, // legacy ambient row
	}
	recOtherAccount := config.RecommendationRecord{
		ID: "rec-other-account", Provider: "aws", Service: "ec2",
		Region: "us-east-1", CloudAccountID: acctPtr(otherUUID),
	}

	tests := []struct {
		name       string
		params     map[string]string
		mockReturn []config.RecommendationRecord
		wantIDs    []string
		wantStatus int // 0 = no error expected
	}{
		{
			// Case 1: absent account_ids → all rows (incl. NULL) included.
			// The SQL emits no WHERE clause on account_ids; mock returns
			// both null-account and non-null-account rows.
			name:       "absent account_ids includes all rows",
			params:     map[string]string{},
			mockReturn: []config.RecommendationRecord{recNullAccount, recWithAccount},
			wantIDs:    []string{"rec-null-account", "rec-with-account"},
		},
		{
			// Case 2: non-empty account_ids + NULL row → excluded.
			// SQL: NULL = ANY(array) → NULL (falsy). The mock returns
			// only the rows Postgres would return after filtering.
			name:       "non-empty account_ids excludes NULL rows",
			params:     map[string]string{"account_ids": validUUID},
			mockReturn: []config.RecommendationRecord{recWithAccount},
			wantIDs:    []string{"rec-with-account"},
		},
		{
			// Case 3: non-empty account_ids matching a row → included.
			name:       "matching account_id includes row",
			params:     map[string]string{"account_ids": validUUID},
			mockReturn: []config.RecommendationRecord{recWithAccount},
			wantIDs:    []string{"rec-with-account"},
		},
		{
			// Case 4: non-empty account_ids non-matching → excluded.
			// Mock returns empty (Postgres returned nothing for the UUID).
			name:       "non-matching account_id returns empty",
			params:     map[string]string{"account_ids": otherUUID},
			mockReturn: []config.RecommendationRecord{},
			wantIDs:    []string{},
		},
		{
			// Validation: invalid UUID in account_ids → 400 before DB call.
			name:       "invalid UUID returns 400",
			params:     map[string]string{"account_ids": "not-a-uuid"},
			wantStatus: 400,
		},
		{
			// Validation: multiple valid UUIDs comma-separated — both included
			// when mock returns matching rows for both.
			name:   "two valid UUIDs comma-separated",
			params: map[string]string{"account_ids": validUUID + "," + otherUUID},
			mockReturn: []config.RecommendationRecord{
				recWithAccount, recOtherAccount,
			},
			wantIDs: []string{"rec-with-account", "rec-other-account"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockScheduler := new(MockScheduler)
			if tt.wantStatus == 0 {
				mockScheduler.On("ListRecommendations", ctx, mock.Anything).
					Return(tt.mockReturn, nil)
			}
			handler := &Handler{
				scheduler: mockScheduler,
				apiKey:    "test-key",
			}
			req := &events.LambdaFunctionURLRequest{
				Headers: map[string]string{"x-api-key": "test-key"},
			}

			result, err := handler.getRecommendations(ctx, req, tt.params)

			if tt.wantStatus != 0 {
				require.Error(t, err)
				ce, ok := IsClientError(err)
				require.True(t, ok, "expected ClientError, got %T: %v", err, err)
				assert.Equal(t, tt.wantStatus, ce.code)
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			gotIDs := make([]string, len(result.Recommendations))
			for i, r := range result.Recommendations {
				gotIDs[i] = r.ID
			}
			assert.ElementsMatch(t, tt.wantIDs, gotIDs)
		})
	}
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
