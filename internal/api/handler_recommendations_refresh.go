package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// markedCollectionRollbackTimeout bounds the detached best-effort clear issued
// when the async self-invoke fails. Matches the scheduler's deferred clear and
// Application.releaseSkippedCollectionMarker budgets.
const markedCollectionRollbackTimeout = 5 * time.Second

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

	// Pre-check freshness store is reachable before acquiring the collection slot.
	if _, err := h.config.GetRecommendationsFreshness(ctx); err != nil {
		return nil, fmt.Errorf("failed to read freshness: %w", err)
	}

	// Atomically mark collection as started. Returns false (409) if another
	// collection is already in flight (started_at set within the last 5 minutes).
	// The returned token identifies this caller as the marker's owner; it is
	// threaded through the async invoke (or the sync collect call) so only
	// this run can later clear the marker (issue #261 compare-and-clear guard).
	token, ok, err := h.config.MarkCollectionStarted(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to mark collection started: %w", err)
	}
	if !ok {
		return nil, NewClientError(409, "recommendation collection already in progress; try again in a few minutes")
	}

	freshness, err := h.runMarkedCollection(ctx, token)
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
//
// token is the owner token MarkCollectionStarted returned to the caller; it
// is passed to the async invoke payload (so the scheduler can thread it
// back into ClearCollectionStarted) and to the rollback clear on this
// handler's own failure path.
func (h *Handler) runMarkedCollection(ctx context.Context, token string) (*config.RecommendationsFreshness, error) {
	schedulerARN := os.Getenv("SCHEDULER_LAMBDA_ARN")
	if schedulerARN != "" {
		if invokeErr := h.asyncInvokeSelf(ctx, schedulerARN, token); invokeErr != nil {
			h.rollbackMarkedCollection(token)
			return nil, fmt.Errorf("failed to trigger async collection: %w", invokeErr)
		}
		freshness, err := h.config.GetRecommendationsFreshness(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read freshness after async invoke: %w", err)
		}
		return freshness, nil
	}
	// Non-Lambda (HTTP) mode: collect synchronously. ClearCollectionStarted
	// is called by the scheduler's deferred clearCollectionStartedBestEffort,
	// passed this same token so the clear is scoped to this run.
	if _, collectErr := h.scheduler.CollectRecommendations(ctx, token); collectErr != nil {
		return nil, fmt.Errorf("collection failed: %w", collectErr)
	}
	freshness, err := h.config.GetRecommendationsFreshness(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read freshness after collect: %w", err)
	}
	return freshness, nil
}

// rollbackMarkedCollection releases this caller's own in-flight marker after
// the async self-invoke failed and no collection will ever run to release it.
//
// Deliberately detached from the request context. The dominant cause of an
// asyncInvokeSelf failure is the request context itself expiring or being
// canceled (API Gateway deadline, client disconnect, a slow SDK credential
// refresh), so reusing that context here would fail the rollback in exactly
// the case that produced it. The marker would then sit stranded for the full
// 5-minute auto-recovery window in MarkCollectionStarted, rejecting every
// refresh the user attempts with 409 while no collection is running. Matches
// the detached-clear pattern in the scheduler's deferred clear and in
// Application.releaseSkippedCollectionMarker.
//
// Scoped by token, so it can only ever release the marker this caller owns.
// Best effort: a failure only defers cleanup to the 5-minute window, and the
// caller already returns the underlying invoke error, so it is logged rather
// than masking that error.
func (h *Handler) rollbackMarkedCollection(token string) {
	clearCtx, cancel := context.WithTimeout(context.Background(), markedCollectionRollbackTimeout)
	defer cancel()
	if err := h.config.ClearCollectionStarted(clearCtx, token); err != nil {
		logging.Warnf("rollbackMarkedCollection: failed to clear collection started marker: %v", err)
	}
}

// asyncInvokeSelf fires an InvocationType=Event invoke of the given Lambda
// function ARN with the EventBridge-style payload that handleLambdaScheduledEvent
// recognizes as a "collect recommendations" job. The call returns immediately;
// the Lambda runtime delivers the event to the next available container
// (which may be this same container's next invocation).
func (h *Handler) asyncInvokeSelf(ctx context.Context, functionARN, ownerToken string) error {
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
	payload, marshalErr := json.Marshal(map[string]string{
		"source":      "aws.events",
		"action":      "collect_recommendations",
		"owner_token": ownerToken,
	})
	if marshalErr != nil {
		return fmt.Errorf("asyncInvokeSelf: failed to marshal payload: %w", marshalErr)
	}

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
