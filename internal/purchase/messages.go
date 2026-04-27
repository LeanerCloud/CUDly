package purchase

import (
	"context"
	"encoding/json"
	"fmt"

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

// AsyncMessage represents an SQS message for async purchase processing
type AsyncMessage struct {
	Type        MessageType `json:"type"`
	ExecutionID string      `json:"execution_id,omitempty"`
	PlanID      string      `json:"plan_id,omitempty"`
	Token       string      `json:"token,omitempty"`
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
// KNOWN AUTHORIZATION GAP (legacy token-only path):
// The SQS approve/cancel handlers bypass the per-account contact_email
// gating enforced by the HTTP path's authorizeApprovalAction in
// internal/api/handler_purchases.go. ApproveExecution and CancelExecution
// validate only the approval token here, so any holder of the token
// (forwarded SQS message, replayed payload) can approve/cancel without a
// session-email match.
//
// This is acceptable today because the SQS path is internal-only (no
// external producers) and exists for the legacy email-link delivery flow.
// Future hardening should either:
//  1. Deprecate the SQS path in favour of the HTTP /purchases/{action}/:id
//     route gated by authorizeApprovalAction, or
//  2. Carry the approver's verified session email in AsyncMessage and have
//     ApproveExecution/CancelExecution enforce the same per-account
//     contact_email check before mutating state.
//
// TODO(security): pick (1) or (2) and remove this gap. See
// internal/api/handler_purchases.go authorizeApprovalAction for the HTTP
// counterpart.
func (m *Manager) handleApproveMessage(ctx context.Context, msg AsyncMessage) error {
	if msg.ExecutionID == "" || msg.Token == "" {
		return fmt.Errorf("execution_id and token required for approve message")
	}
	// Async-path actors aren't tracked — the SQS message only carries
	// execution_id + token. Pass empty actor so attribution falls back to
	// the approver email (reused at render time in fetchExecutionsAsHistory).
	return m.ApproveExecution(ctx, msg.ExecutionID, msg.Token, "")
}

// handleCancelMessage processes a cancel message.
//
// Inherits the same authorization gap documented on handleApproveMessage —
// CancelExecution only validates the token and does not enforce a
// session-email match against the per-account contact_email.
func (m *Manager) handleCancelMessage(ctx context.Context, msg AsyncMessage) error {
	if msg.ExecutionID == "" || msg.Token == "" {
		return fmt.Errorf("execution_id and token required for cancel message")
	}
	// Async-path actor fallback — see handleApproveMessage.
	return m.CancelExecution(ctx, msg.ExecutionID, msg.Token, "")
}
