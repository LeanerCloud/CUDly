package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// LambdaInvokerInterface is the narrow subset of lambda.Client used by the
// async refresh handler. Extracted so tests can inject a stub without
// standing up a real Lambda client.
type LambdaInvokerInterface interface {
	Invoke(ctx context.Context, params *lambda.InvokeInput, optFns ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

// RefreshResponse is the 202 body returned by POST /api/recommendations/refresh.
// started_at is the timestamp recorded by MarkCollectionStarted.
// last_collected_at is the previous successful collection timestamp (may be
// nil on first-ever collection). The frontend polls GET
// /api/recommendations/freshness every 5 s and clears the refreshing banner
// once last_collected_at advances past started_at.
type RefreshResponse struct {
	StartedAt       time.Time  `json:"started_at"`
	LastCollectedAt *time.Time `json:"last_collected_at"`
}

// postRefreshRecommendations implements POST /api/recommendations/refresh.
//
// Flow:
//  1. Require view:recommendations permission.
//  2. Atomically set last_collection_started_at via MarkCollectionStarted.
//     Returns 409 if another collection is already in flight (started within the
//     past 5 minutes). The 5-minute window provides automatic recovery if the
//     scheduler Lambda crashes mid-run and never clears started_at.
//  3. Async-invoke the scheduler Lambda (this function itself) with the
//     scheduled_recommendations event payload — InvocationType=Event so the
//     API Lambda returns immediately without waiting for the scheduler to finish.
//  4. Return 202 with the started_at timestamp and the previous last_collected_at
//     value so the frontend can render a "last updated N minutes ago" indicator
//     while the new collection runs.
//
// When SCHEDULER_LAMBDA_ARN is not set (e.g. in non-Lambda HTTP mode or tests),
// the handler falls back to a synchronous CollectRecommendations call so that
// on-demand refresh still works in development. The fallback also fires when the
// Lambda SDK is unavailable or the invoke call fails — in those cases the error
// is surfaced directly rather than silently swallowed.
func (h *Handler) postRefreshRecommendations(ctx context.Context, req *events.LambdaFunctionURLRequest) (*RefreshResponse, error) {
	if _, err := h.requirePermission(ctx, req, "view", "recommendations"); err != nil {
		return nil, err
	}

	// Read current freshness so we can include last_collected_at in the 202 body.
	freshness, err := h.config.GetRecommendationsFreshness(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read freshness: %w", err)
	}

	// Atomically mark collection as started. Returns false (409) if another
	// collection is already in flight (started_at set within the last 5 minutes).
	ok, err := h.config.MarkCollectionStarted(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to mark collection started: %w", err)
	}
	if !ok {
		return nil, NewClientError(409, "recommendation collection already in progress; try again in a few minutes")
	}

	freshness, err = h.runMarkedCollection(ctx)
	if err != nil {
		return nil, err
	}

	// Re-read started_at (set by MarkCollectionStarted) for the 202 body.
	// In the synchronous path this will be NULL (cleared by the defer), so we
	// fall back to now as a reasonable sentinel.
	now := time.Now().UTC()
	startedAt := now
	if freshness.LastCollectionStartedAt != nil {
		startedAt = *freshness.LastCollectionStartedAt
	}

	return &RefreshResponse{
		StartedAt:       startedAt,
		LastCollectedAt: freshness.LastCollectedAt,
	}, nil
}

// runMarkedCollection triggers the actual collection after MarkCollectionStarted
// has already set the in-flight marker for this caller. In Lambda mode it
// fire-and-forgets an async self-invoke; in HTTP mode it does a synchronous
// collect. Both paths re-read freshness so the caller sees the value
// MarkCollectionStarted just wrote (the pre-mark snapshot has
// LastCollectionStartedAt as nil, which would force a sentinel fallback).
//
// Extracted from postRefreshRecommendations to keep that function under the
// project's cyclomatic-complexity gate after the post-async re-read was added.
func (h *Handler) runMarkedCollection(ctx context.Context) (*config.RecommendationsFreshness, error) {
	schedulerARN := os.Getenv("SCHEDULER_LAMBDA_ARN")
	if schedulerARN != "" {
		if invokeErr := h.asyncInvokeSelf(ctx, schedulerARN); invokeErr != nil {
			_ = h.config.ClearCollectionStarted(ctx)
			return nil, fmt.Errorf("failed to trigger async collection: %w", invokeErr)
		}
		freshness, err := h.config.GetRecommendationsFreshness(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read freshness after async invoke: %w", err)
		}
		return freshness, nil
	}
	// Non-Lambda (HTTP) mode: collect synchronously. ClearCollectionStarted
	// is called by the scheduler's deferred clearCollectionStartedBestEffort.
	if _, collectErr := h.scheduler.CollectRecommendations(ctx); collectErr != nil {
		return nil, fmt.Errorf("collection failed: %w", collectErr)
	}
	freshness, err := h.config.GetRecommendationsFreshness(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read freshness after collect: %w", err)
	}
	return freshness, nil
}

// asyncInvokeSelf fires an InvocationType=Event invoke of the given Lambda
// function ARN with the EventBridge-style payload that handleLambdaScheduledEvent
// recognizes as a "collect recommendations" job. The call returns immediately;
// the Lambda runtime delivers the event to the next available container
// (which may be this same container's next invocation).
func (h *Handler) asyncInvokeSelf(ctx context.Context, functionARN string) error {
	invoker, err := h.getLambdaInvoker(ctx)
	if err != nil {
		return fmt.Errorf("failed to build Lambda client: %w", err)
	}

	// Payload shape matches internal/server/handler.go::ScheduledEvent + its
	// ParseScheduledEvent dispatch table: the Action field selects the
	// scheduled-task type. "collect_recommendations" maps to
	// TaskCollectRecommendations, which is what we want to fire.
	//
	// The previous payload `{"event":"scheduled_recommendations"}` had no
	// matching field in ScheduledEvent at all — Action unmarshalled as ""
	// and ParseScheduledEvent rejected the event with "unknown scheduled
	// task action: \"\"". Verified in the deployed Lambda log
	// `/aws/lambda/cudly-dev-426fc8af-api` request 3befb380-6d46-... — the
	// async self-invoke was firing successfully but the receiving Lambda
	// was rejecting every invocation, so collection never actually ran.
	//
	// Source = "aws.events" so detectLambdaEventType in
	// internal/server/lambda.go (which checks Source == "aws.events" ||
	// Action != "") classifies this consistently with EventBridge cron
	// deliveries that already exercise this code path.
	payload, _ := json.Marshal(map[string]string{
		"source": "aws.events",
		"action": "collect_recommendations",
	})

	_, err = invoker.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   aws.String(functionARN),
		InvocationType: types.InvocationTypeEvent, // fire-and-forget
		Payload:        payload,
	})
	if err != nil {
		return fmt.Errorf("lambda invoke: %w", err)
	}
	return nil
}

// getLambdaInvoker returns the injected invoker (tests) or constructs a real
// lambda.Client from the handler's cached AWS config.
func (h *Handler) getLambdaInvoker(ctx context.Context) (LambdaInvokerInterface, error) {
	if h.lambdaInvoker != nil {
		return h.lambdaInvoker, nil
	}
	// Use the handler's cached AWS config (loaded once via awsCfgOnce in handler.go).
	h.awsCfgOnce.Do(func() {
		h.awsCfg, h.awsCfgErr = awsconfig.LoadDefaultConfig(ctx)
	})
	if h.awsCfgErr != nil {
		return nil, fmt.Errorf("load AWS config: %w", h.awsCfgErr)
	}
	return lambda.NewFromConfig(h.awsCfg), nil
}

// triggerColdStartCollect is the GET /api/recommendations cold-start path.
// It is called from ListRecommendations when last_collected_at is nil AND
// last_collection_started_at is nil (no collection running). It fires an
// async self-invoke (Lambda mode) or a synchronous collect (HTTP mode) and
// returns the freshness state after the trigger so the caller can return an
// empty list to the user with the correct "collecting" indicator.
//
// The returned freshness may have LastCollectionStartedAt set (async) or
// LastCollectedAt set (sync). Callers should treat a non-nil
// LastCollectionStartedAt as "collection in progress".
func (h *Handler) triggerColdStartCollect(ctx context.Context) (*config.RecommendationsFreshness, error) {
	schedulerARN := os.Getenv("SCHEDULER_LAMBDA_ARN")
	if schedulerARN != "" {
		// Atomic mark. ok=false means another caller already marked it — we
		// MUST NOT trigger a second async invoke or call ClearCollectionStarted
		// (that would wipe the other caller's in-flight marker). Returning the
		// current freshness lets the caller see the in-flight collection and
		// poll for completion.
		ok, err := h.config.MarkCollectionStarted(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to mark cold-start collection: %w", err)
		}
		if !ok {
			return h.config.GetRecommendationsFreshness(ctx)
		}
		if invokeErr := h.asyncInvokeSelf(ctx, schedulerARN); invokeErr != nil {
			// Roll back ONLY because we own the marker (ok==true above).
			_ = h.config.ClearCollectionStarted(ctx)
			return nil, fmt.Errorf("failed to trigger cold-start collect: %w", invokeErr)
		}
		// Re-read freshness to return the started_at value.
		return h.config.GetRecommendationsFreshness(ctx)
	}

	// HTTP / non-Lambda mode: synchronous collect.
	if _, err := h.scheduler.CollectRecommendations(ctx); err != nil {
		return nil, fmt.Errorf("cold-start collect failed: %w", err)
	}
	return h.config.GetRecommendationsFreshness(ctx)
}
