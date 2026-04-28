package purchase

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// MessageType defines the types of async messages that can be processed
type MessageType string

const (
	// MessageTypeExecutePurchase triggers execution of a scheduled purchase
	MessageTypeExecutePurchase MessageType = "execute_purchase"
	// MessageTypeApprove approves a pending execution
	MessageTypeApprove MessageType = "approve"
	// MessageTypeCancel cancels a pending execution
	MessageTypeCancel MessageType = "cancel"
	// MessageTypeSendNotification sends a notification for upcoming purchase
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
		return m.handleExecutePurchase(ctx, msg)
	case MessageTypeApprove:
		return m.handleApproveMessage(ctx, msg)
	case MessageTypeCancel:
		return m.handleCancelMessage(ctx, msg)
	case MessageTypeSendNotification:
		_, err := m.SendUpcomingPurchaseNotifications(ctx)
		return err
	default:
		logging.Warnf("Unknown message type: %s, skipping", msg.Type)
		return nil
	}
}

// handleExecutePurchase processes an execute_purchase message
func (m *Manager) handleExecutePurchase(ctx context.Context, msg AsyncMessage) error {
	if msg.ExecutionID == "" {
		return fmt.Errorf("execution_id required for execute_purchase message")
	}

	execution, err := m.config.GetExecutionByID(ctx, msg.ExecutionID)
	if err != nil {
		return fmt.Errorf("failed to get execution %s: %w", msg.ExecutionID, err)
	}
	if execution == nil {
		return fmt.Errorf("execution not found: %s", msg.ExecutionID)
	}

	// Only execute if approved or pending (auto-approved)
	if execution.Status != "approved" && execution.Status != "pending" {
		logging.Warnf("Execution %s not in executable state (status: %s), skipping", msg.ExecutionID, execution.Status)
		return nil
	}

	logging.Infof("Executing purchase from async message: %s", msg.ExecutionID)
	wasMultiAccount, purchaseErr := m.executePurchase(ctx, execution)
	m.finalizeExecution(execution, purchaseErr)

	if !wasMultiAccount {
		if saveErr := m.config.SavePurchaseExecution(ctx, execution); saveErr != nil {
			logging.Errorf("Failed to save execution status: %v", saveErr)
			return fmt.Errorf("failed to save execution status for %s: %w", msg.ExecutionID, saveErr)
		}
	}
	if purchaseErr == nil {
		if err := m.updatePlanProgress(ctx, execution.PlanID); err != nil {
			logging.Errorf("Failed to update plan progress: %v", err)
		}
	}
	return purchaseErr
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
func (m *Manager) handleApproveMessage(ctx context.Context, msg AsyncMessage) error {
	if msg.ExecutionID == "" || msg.Token == "" {
		return fmt.Errorf("execution_id and token required for approve message")
	}
	if err := m.verifyAsyncApprovalActor(ctx, &msg); err != nil {
		return err
	}
	return m.ApproveExecution(ctx, msg.ExecutionID, msg.Token, msg.ActorEmail)
}

// handleCancelMessage processes a cancel message. Same hardening as
// handleApproveMessage: token + actor_email + approver-list match are
// all required.
func (m *Manager) handleCancelMessage(ctx context.Context, msg AsyncMessage) error {
	if msg.ExecutionID == "" || msg.Token == "" {
		return fmt.Errorf("execution_id and token required for cancel message")
	}
	if err := m.verifyAsyncApprovalActor(ctx, &msg); err != nil {
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
	if err != nil {
		return nil, fmt.Errorf("failed to get execution: %w", err)
	}
	if execution == nil {
		return nil, fmt.Errorf("execution not found: %s", msg.ExecutionID)
	}
	if execution.ApprovalToken == "" || msg.Token == "" {
		return nil, fmt.Errorf("invalid approval token")
	}
	if subtle.ConstantTimeCompare([]byte(execution.ApprovalToken), []byte(msg.Token)) != 1 {
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
	return fmt.Errorf("actor email is not an authorised approver for this purchase")
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
	for _, rec := range recs {
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
