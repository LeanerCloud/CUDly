package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
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
		// GetRecommendationByID returns (nil, nil, nil) for absent recs.
		mockScheduler.On("GetRecommendationByID", ctx, "rec-missing").
			Return((*config.RecommendationRecord)(nil), ([]string)(nil), nil)
		t.Cleanup(func() { mockScheduler.AssertExpectations(t) })

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
		knownRec := knownRecs[0]
		mockScheduler := new(MockScheduler)
		mockScheduler.On("GetRecommendationByID", ctx, "rec-known").
			Return(&knownRec, ([]string)(nil), nil)
		t.Cleanup(func() { mockScheduler.AssertExpectations(t) })

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
		assert.Nil(t, got.HiddenBy, "visible rec must not carry hidden_by")
	})

	t.Run("returns 200 with hidden_by for override-filtered rec (issue #214)", func(t *testing.T) {
		// GetRecommendationByID returns the rec and the override reasons
		// when the rec exists but is filtered by an account-service override.
		knownRec := knownRecs[0]
		mockScheduler := new(MockScheduler)
		mockScheduler.On("GetRecommendationByID", ctx, "rec-known").
			Return(&knownRec, []string{"enabled=false"}, nil)
		t.Cleanup(func() { mockScheduler.AssertExpectations(t) })

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
		assert.Equal(t, []string{"enabled=false"}, got.HiddenBy,
			"override-hidden rec must carry hidden_by reasons")
	})

	t.Run("provenance degrades gracefully when freshness is unavailable", func(t *testing.T) {
		knownRec := knownRecs[0]
		mockScheduler := new(MockScheduler)
		mockScheduler.On("GetRecommendationByID", ctx, "rec-known").
			Return(&knownRec, ([]string)(nil), nil)
		t.Cleanup(func() { mockScheduler.AssertExpectations(t) })

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

// TestBuildRecommendationsResponse_CapacityFields is the #219 regression
// test. It replicates the REAL API shape: VCPU/MemoryGB live nested inside
// the opaque Details blob (a marshaled common.ComputeDetails), exactly as the
// scheduler persists them via common.MarshalServiceDetails — there are NO
// top-level vcpu/memory_gb fields on the stored record. The pre-fix code never
// decoded Details, so it emitted no top-level vcpu/memory_gb and every
// Capacity cell rendered "—" in production. This asserts the response now
// carries the decoded values at the top level (where the frontend reads them)
// for a compute rec, and omits them for non-compute / unknown-size / empty
// recs so the cell renders "—" rather than a misleading "0 vCPU / 0 GB".
func TestBuildRecommendationsResponse_CapacityFields(t *testing.T) {
	// computeDetails marshals a ComputeDetails the same way the scheduler
	// does, so the test feeds the exact Details JSON shape the API stores.
	computeDetails := func(vcpu int, memGB float64) json.RawMessage {
		raw, err := common.MarshalServiceDetails(&common.ComputeDetails{
			InstanceType: "m5.2xlarge",
			Platform:     "linux",
			Tenancy:      "default",
			VCPU:         vcpu,
			MemoryGB:     memGB,
		})
		require.NoError(t, err)
		require.NotEmpty(t, raw, "marshaled compute details must not be empty")
		return raw
	}

	recs := []config.RecommendationRecord{
		{
			ID:      "rec-compute",
			Service: "ec2",
			Region:  "us-east-1",
			Details: computeDetails(8, 32),
		},
		{
			// Compute rec whose converter didn't resolve a size: VCPU/MemoryGB
			// stay at the zero value → fields omitted → cell renders "—".
			ID:      "rec-compute-unknown-size",
			Service: "ec2",
			Region:  "us-east-1",
			Details: computeDetails(0, 0),
		},
		{
			// Non-compute service (RDS): no VCPU/MemoryGB to surface.
			ID:      "rec-database",
			Service: "rds",
			Region:  "us-east-1",
			Details: json.RawMessage(`{"instance_class":"db.r5.large","engine":"postgres"}`),
		},
		{
			// Legacy / pre-#453 row with an empty Details blob.
			ID:      "rec-legacy-empty",
			Service: "ec2",
			Region:  "us-east-1",
		},
	}

	resp := buildRecommendationsResponse(recs)
	require.NotNil(t, resp)
	require.Len(t, resp.Recommendations, 4)

	byID := make(map[string]config.RecommendationRecord, len(resp.Recommendations))
	for _, r := range resp.Recommendations {
		byID[r.ID] = r
	}

	// Compute rec with a known size → top-level vcpu=8, memory_gb=32.
	compute := byID["rec-compute"]
	require.NotNil(t, compute.VCPU, "compute rec must carry top-level vcpu")
	require.NotNil(t, compute.MemoryGB, "compute rec must carry top-level memory_gb")
	assert.Equal(t, 8, *compute.VCPU)
	assert.Equal(t, float64(32), *compute.MemoryGB)

	// The serialized JSON must expose them at the TOP LEVEL (not nested under
	// details) — this is the exact contract the frontend reads.
	blob, err := json.Marshal(compute)
	require.NoError(t, err)
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(blob, &top))
	assert.JSONEq(t, "8", string(top["vcpu"]))
	assert.JSONEq(t, "32", string(top["memory_gb"]))

	// Unknown size, non-compute service, and legacy-empty all omit the fields.
	for _, id := range []string{"rec-compute-unknown-size", "rec-database", "rec-legacy-empty"} {
		r := byID[id]
		assert.Nil(t, r.VCPU, "%s must not carry vcpu", id)
		assert.Nil(t, r.MemoryGB, "%s must not carry memory_gb", id)

		b, err := json.Marshal(r)
		require.NoError(t, err)
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(b, &m))
		_, hasVCPU := m["vcpu"]
		_, hasMem := m["memory_gb"]
		assert.False(t, hasVCPU, "%s JSON must omit vcpu", id)
		assert.False(t, hasMem, "%s JSON must omit memory_gb", id)
	}
}

// TestGetRecommendations_MinSavingsFilters is the regression test for issue #1089:
// it asserts that min_savings_usd (dollar floor) and min_savings_pct (percentage
// floor) are distinct parameters with separate semantics and cannot be confused.
//
// Key invariants proven:
//   - A dollar value passed as min_savings_usd does NOT conflate with a percentage
//     (e.g. min_savings_usd=30 means "$30", not "30%").
//   - A percentage passed as min_savings_pct does NOT conflate with a dollar amount.
//   - Both can be specified independently and are applied independently.
//   - min_savings_pct validates the range 0-100.
//   - Non-numeric values are rejected with 400.
func TestGetRecommendations_MinSavingsFilters(t *testing.T) {
	ctx := context.Background()

	t.Run("min_savings_usd filter is wired to RecommendationFilter.MinSavingsUSD not MinSavingsPct", func(t *testing.T) {
		mockScheduler := new(MockScheduler)
		mockScheduler.On("ListRecommendations", ctx, mock.MatchedBy(func(f *config.RecommendationFilter) bool {
			// Dollar filter must be set; percentage filter must be zero.
			return f.MinSavingsUSD == 30 && f.MinSavingsPct == 0
		})).Return([]config.RecommendationRecord{}, nil)
		t.Cleanup(func() { mockScheduler.AssertExpectations(t) })

		handler := &Handler{scheduler: mockScheduler, apiKey: "test-key"}
		req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
		params := map[string]string{"min_savings_usd": "30"}

		result, err := handler.getRecommendations(ctx, req, params)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("min_savings_pct filter is wired to RecommendationFilter.MinSavingsPct not MinSavingsUSD", func(t *testing.T) {
		mockScheduler := new(MockScheduler)
		mockScheduler.On("ListRecommendations", ctx, mock.MatchedBy(func(f *config.RecommendationFilter) bool {
			// Percentage filter must be set; dollar filter must be zero.
			return f.MinSavingsPct == 30 && f.MinSavingsUSD == 0
		})).Return([]config.RecommendationRecord{}, nil)
		t.Cleanup(func() { mockScheduler.AssertExpectations(t) })

		handler := &Handler{scheduler: mockScheduler, apiKey: "test-key"}
		req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
		// Sending "30" as min_savings_pct must NOT be treated as $30 by the server.
		params := map[string]string{"min_savings_pct": "30"}

		result, err := handler.getRecommendations(ctx, req, params)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("both filters can be combined independently", func(t *testing.T) {
		mockScheduler := new(MockScheduler)
		mockScheduler.On("ListRecommendations", ctx, mock.MatchedBy(func(f *config.RecommendationFilter) bool {
			return f.MinSavingsUSD == 50 && f.MinSavingsPct == 20
		})).Return([]config.RecommendationRecord{}, nil)
		t.Cleanup(func() { mockScheduler.AssertExpectations(t) })

		handler := &Handler{scheduler: mockScheduler, apiKey: "test-key"}
		req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
		params := map[string]string{"min_savings_usd": "50", "min_savings_pct": "20"}

		result, err := handler.getRecommendations(ctx, req, params)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("min_savings_pct above 100 returns 400", func(t *testing.T) {
		handler := &Handler{apiKey: "test-key"}
		req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
		params := map[string]string{"min_savings_pct": "101"}

		result, err := handler.getRecommendations(ctx, req, params)
		require.Error(t, err)
		assert.Nil(t, result)
		ce, ok := IsClientError(err)
		require.True(t, ok)
		assert.Equal(t, 400, ce.code)
	})

	t.Run("min_savings_usd with non-numeric value returns 400", func(t *testing.T) {
		handler := &Handler{apiKey: "test-key"}
		req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
		params := map[string]string{"min_savings_usd": "thirty"}

		result, err := handler.getRecommendations(ctx, req, params)
		require.Error(t, err)
		assert.Nil(t, result)
		ce, ok := IsClientError(err)
		require.True(t, ok)
		assert.Equal(t, 400, ce.code)
	})

	t.Run("min_savings_pct with non-numeric value returns 400", func(t *testing.T) {
		handler := &Handler{apiKey: "test-key"}
		req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
		params := map[string]string{"min_savings_pct": "thirty"}

		result, err := handler.getRecommendations(ctx, req, params)
		require.Error(t, err)
		assert.Nil(t, result)
		ce, ok := IsClientError(err)
		require.True(t, ok)
		assert.Equal(t, 400, ce.code)
	})

	t.Run("absent filters pass through zero values in RecommendationFilter", func(t *testing.T) {
		mockScheduler := new(MockScheduler)
		mockScheduler.On("ListRecommendations", ctx, mock.MatchedBy(func(f *config.RecommendationFilter) bool {
			return f.MinSavingsUSD == 0 && f.MinSavingsPct == 0
		})).Return([]config.RecommendationRecord{}, nil)
		t.Cleanup(func() { mockScheduler.AssertExpectations(t) })

		handler := &Handler{scheduler: mockScheduler, apiKey: "test-key"}
		req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
		// No min_savings params at all.
		result, err := handler.getRecommendations(ctx, req, map[string]string{})
		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}

func TestConfidenceBucketFor(t *testing.T) {
	cases := []struct {
		name    string
		want    string
		savings float64
		count   int
	}{
		{name: "high requires both signals", savings: 250, count: 4, want: "high"},
		{name: "savings without fleet falls to medium", savings: 250, count: 1, want: "medium"},
		{name: "medium on savings alone", savings: 60, count: 1, want: "medium"},
		{name: "low when neither threshold is met", savings: 10, count: 1, want: "low"},
		{name: "count clamped to >=1 — savings still drives bucket", savings: 60, count: 0, want: "medium"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, confidenceBucketFor(tc.savings, tc.count))
		})
	}
}
