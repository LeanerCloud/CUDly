package purchase

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// MessageType defines the types of async messages that can be processed.
type MessageType string

const (
	// MessageTypeExecutePurchase triggers execution of a scheduled purchase.
	MessageTypeExecutePurchase MessageType = "execute_purchase"
	// MessageTypeApprove approves a pending execution.
	MessageTypeApprove MessageType = "approve"
	// MessageTypeCancel cancels a pending execution.
	MessageTypeCancel MessageType = "cancel"
	// MessageTypeSendNotification sends a notification for upcoming purchase.
	MessageTypeSendNotification MessageType = "send_notification"
)

// AsyncMessage represents an SQS message for async purchase processing.
//
// ActorEmail is REQUIRED for approve/cancel messages: the SQS path must
// carry the verified session email of the operator who initiated the
// action so the consumer can enforce the same per-account contact_email
// gate as the HTTP path's authorizeApprovalAction. Messages without
// ActorEmail are rejected (no tokenless legacy fallback) — this is the
// CRITICAL hardening for replayed/forwarded payloads. Producers in this
// repo have always paired token+actor; legacy in-flight messages
// pre-dating this field are rejected by design and the operator can
// re-issue the action via the HTTP route.
type AsyncMessage struct {
	Type        MessageType `json:"type"`
	ExecutionID string      `json:"execution_id,omitempty"`
	PlanID      string      `json:"plan_id,omitempty"`
	Token       string      `json:"token,omitempty"`
	ActorEmail  string      `json:"actor_email,omitempty"`
}

// ProcessMessage handles SQS messages for async purchase processing.
// Supported message types:
//   - execute_purchase: Execute a scheduled purchase by execution_id
//   - approve: Approve a pending execution (requires execution_id and token)
//   - cancel: Cancel a pending execution (requires execution_id and token)
//   - send_notification: Send notification for upcoming purchases
func (m *Manager) ProcessMessage(ctx context.Context, body string) error {
	logging.Debug("Processing async message")

	// Parse the message
	var msg AsyncMessage
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		// If not valid JSON, log and skip (don't fail the queue)
		logging.Warnf("Invalid message format (not JSON), skipping: %v", err)
		return nil
	}

	switch msg.Type {
	case MessageTypeExecutePurchase:
		return m.handleExecutePurchase(ctx, &msg)
	case MessageTypeApprove:
		return m.handleApproveMessage(ctx, &msg)
	case MessageTypeCancel:
		return m.handleCancelMessage(ctx, &msg)
	case MessageTypeSendNotification:
		_, err := m.SendUpcomingPurchaseNotifications(ctx)
		return err
	default:
		logging.Warnf("Unknown message type: %s, skipping", msg.Type)
		return nil
	}
}

// handleExecutePurchase processes an execute_purchase message.
// checkAutoExecuteGate enforces the AutoPurchase and source gate for
// pending/notified rows on the SQS execute_purchase path. Extracted from
// handleExecutePurchase to keep that function under the gocyclo:10 limit.
// Returns nil when execution may proceed; a non-nil error means the message
// should be dead-lettered (the row is not eligible for auto-execution).
//
// "approved" rows bypass the gate: they have already passed the human
// approval path and are always eligible for execution.
func (m *Manager) checkAutoExecuteGate(ctx context.Context, execution *config.PurchaseExecution) error {
	if execution.Status != "pending" && execution.Status != "notified" {
		return nil
	}
	eligible, err := m.executableByScheduler(ctx, execution)
	if err != nil {
		return fmt.Errorf("AutoPurchase gate check failed for execution %s: %w", execution.ExecutionID, err)
	}
	if !eligible {
		return fmt.Errorf("execution %s (status=%s source=%q) is not eligible for auto-execution: AutoPurchase=false or source=web — approve via the approval link",
			execution.ExecutionID, execution.Status, execution.Source)
	}
	return nil
}

func (m *Manager) handleExecutePurchase(ctx context.Context, msg *AsyncMessage) error {
	if msg.ExecutionID == "" {
		return fmt.Errorf("execution_id required for execute_purchase message")
	}

	execution, err := m.config.GetExecutionByID(ctx, msg.ExecutionID)
	if errors.Is(err, config.ErrNotFound) {
		return fmt.Errorf("execution not found: %s", msg.ExecutionID)
	}
	if err != nil {
		return fmt.Errorf("failed to get execution %s: %w", msg.ExecutionID, err)
	}

	logging.Infof("Executing purchase from async message: %s", msg.ExecutionID)

	// AutoPurchase gate: pending/notified rows require AutoPurchase=true on the
	// owning plan; web-sourced rows must use the token-link approval path.
	// "approved" rows bypass the gate (already human-approved). Fail closed.
	if err := m.checkAutoExecuteGate(ctx, execution); err != nil {
		return err
	}

	// Atomically claim the row before touching the cloud (issue #1013). SQS
	// delivery is at-least-once: a redelivered execute_purchase message (slow
	// purchase whose visibility timeout expired, partial-batch replay, operator
	// re-drive) would otherwise pass the old non-atomic status check and execute
	// the same row a second time concurrently. claimAndExecute CASes
	// approved/pending/notified -> running and only proceeds on a won claim; a
	// lost claim is a benign duplicate that we ack (return nil) without
	// re-executing.
	claimed, execErr := m.claimAndExecute(ctx, execution)
	if !claimed {
		// Either a benign CAS race-loss (execErr == nil — ack the duplicate) or
		// a real DB error during the claim (execErr != nil — surface it so the
		// message is redelivered, since nothing was executed).
		if execErr != nil {
			return fmt.Errorf("failed to claim execution %s: %w", msg.ExecutionID, execErr)
		}
		return nil
	}

	// A multi-account run where at least one account committed must be ACKed,
	// not redelivered (issue #1014): redelivery would re-run the fan-out and,
	// absent the per-account idempotency key (#1012), double-buy the accounts
	// that already succeeded. errAllAccountsFailed (nothing committed) and
	// single-account errors fall through and are returned so SQS redelivers.
	if isMultiAccountAckable(execErr) {
		return nil
	}
	return execErr
}

// handleApproveMessage processes an approve message.
//
// SECURITY: closes the previous async-SQS bypass of the approver gate.
// The handler now requires an actor_email field on the message and
// verifies it against the same per-account contact_email approver list
// that authorizeApprovalAction enforces on the HTTP path (see
// internal/api/handler_purchases.go). Token validation alone is no
// longer sufficient — a replayed or forwarded payload without a
// matching actor is rejected.
//
// Legacy in-flight messages produced before this field existed will be
// rejected (option (a) in the hardening plan): we preferred a clean
// reject over a tokenless backfill because reopening the tokenless path
// for any reason is exactly the threat model this fix addresses. Any
// legitimate stranded action can be re-issued via the HTTP route gated
// by authorizeApprovalAction.
func (m *Manager) handleApproveMessage(ctx context.Context, msg *AsyncMessage) error {
	if msg.ExecutionID == "" || msg.Token == "" {
		return fmt.Errorf("execution_id and token required for approve message")
	}
	if err := m.verifyAsyncApprovalActor(ctx, msg); err != nil {
		return err
	}
	return m.ApproveExecution(ctx, msg.ExecutionID, msg.Token, msg.ActorEmail)
}

// handleCancelMessage processes a cancel message. Same hardening as
// handleApproveMessage: token + actor_email + approver-list match are
// all required.
func (m *Manager) handleCancelMessage(ctx context.Context, msg *AsyncMessage) error {
	if msg.ExecutionID == "" || msg.Token == "" {
		return fmt.Errorf("execution_id and token required for cancel message")
	}
	if err := m.verifyAsyncApprovalActor(ctx, msg); err != nil {
		return err
	}
	return m.CancelExecution(ctx, msg.ExecutionID, msg.Token, msg.ActorEmail)
}

// verifyAsyncApprovalActor enforces the same per-account contact_email
// approver gate the HTTP path runs through authorizeApprovalAction
// (internal/api/handler_purchases.go) before approve/cancel mutates
// state. Steps:
//
//  1. Reject the message outright if actor_email is empty — legacy /
//     replayed payloads land here and must NOT fall through to a
//     tokenless approval.
//  2. Load the execution by ID and validate the approval token using
//     constant-time compare, mirroring ApproveExecution / CancelExecution
//     so a token mismatch fails fast with the same error shape and we
//     don't leak whether the actor or the token was wrong.
//  3. Resolve the per-account contact_email approver list from the
//     execution's recommendations and assert actor_email matches one
//     of them (case-insensitive, trimmed). Empty approver list is a
//     hard reject — same policy as authorizeApprovalAction.
//
// Returning an error here causes ProcessMessage to surface it to the
// SQS layer; the message is not re-driven into ApproveExecution /
// CancelExecution.
func (m *Manager) verifyAsyncApprovalActor(ctx context.Context, msg *AsyncMessage) error {
	if strings.TrimSpace(msg.ActorEmail) == "" {
		return fmt.Errorf("actor_email required for approve/cancel message")
	}
	execution, err := m.loadAsyncExecutionForApproval(ctx, msg)
	if err != nil {
		return err
	}
	return m.matchActorAgainstApprovers(ctx, msg.ActorEmail, execution.Recommendations)
}

// loadAsyncExecutionForApproval fetches the execution targeted by an
// async approve/cancel message and validates the approval token. Split
// out from verifyAsyncApprovalActor so the parent stays under the
// repo's gocyclo threshold.
func (m *Manager) loadAsyncExecutionForApproval(ctx context.Context, msg *AsyncMessage) (*config.PurchaseExecution, error) {
	execution, err := m.config.GetExecutionByID(ctx, msg.ExecutionID)
	if errors.Is(err, config.ErrNotFound) {
		return nil, fmt.Errorf("execution not found: %s", msg.ExecutionID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get execution: %w", err)
	}
	if execution.ApprovalToken == "" || msg.Token == "" {
		return nil, fmt.Errorf("invalid approval token")
	}
	storedHash := sha256.Sum256([]byte(execution.ApprovalToken))
	userHash := sha256.Sum256([]byte(msg.Token))
	if subtle.ConstantTimeCompare(storedHash[:], userHash[:]) != 1 {
		return nil, fmt.Errorf("invalid approval token")
	}
	return execution, nil
}

// matchActorAgainstApprovers resolves the per-account contact_email
// approver list for an execution and asserts the actor matches one of
// them (case-insensitive, trimmed). Empty approver list is a hard
// reject — mirrors authorizeApprovalAction's policy.
func (m *Manager) matchActorAgainstApprovers(ctx context.Context, actor string, recs []config.RecommendationRecord) error {
	approvers, err := m.gatherApproverContactEmails(ctx, recs)
	if err != nil {
		return fmt.Errorf("failed to resolve approvers: %w", err)
	}
	if len(approvers) == 0 {
		return fmt.Errorf("no per-account contact email configured for this execution; set the cloud account's contact_email before approving")
	}
	actorLower := strings.ToLower(strings.TrimSpace(actor))
	for _, addr := range approvers {
		if strings.ToLower(strings.TrimSpace(addr)) == actorLower {
			return nil
		}
	}
	return fmt.Errorf("actor email is not an authorized approver for this purchase")
}

// gatherApproverContactEmails mirrors the algorithm in
// internal/api/handler_purchases.go gatherAccountContactEmails: dedup
// by lowercase, preserve insertion order, skip recs without a
// CloudAccountID, skip accounts that are not found or have an empty
// ContactEmail.
//
// The two implementations are deliberately duplicated rather than
// extracted into a shared package: internal/api and internal/purchase
// share neither a parent package nor a transitive dependency, and
// hoisting one helper into config or a new package costs more than
// keeping the ~15-line loop in sync. If a third caller appears, hoist
// it then. Until then, each side cross-references the other so future
// edits don't drift.
func (m *Manager) gatherApproverContactEmails(ctx context.Context, recs []config.RecommendationRecord) ([]string, error) {
	seenAccount := map[string]bool{}
	seenEmail := map[string]bool{}
	var out []string
	for i := range recs {
		rec := &recs[i]
		if rec.CloudAccountID == nil || *rec.CloudAccountID == "" {
			continue
		}
		id := *rec.CloudAccountID
		if seenAccount[id] {
			continue
		}
		seenAccount[id] = true
		acct, err := m.config.GetCloudAccount(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("failed to get cloud account %s: %w", id, err)
		}
		if acct == nil {
			continue
		}
		addr := strings.TrimSpace(acct.ContactEmail)
		if addr == "" {
			continue
		}
		norm := strings.ToLower(addr)
		if seenEmail[norm] {
			continue
		}
		seenEmail[norm] = true
		out = append(out, addr)
	}
	return out, nil
}
