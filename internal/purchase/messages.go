package purchase

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	if err := m.executePurchase(ctx, execution); err != nil {
		execution.Status = "failed"
		execution.Error = err.Error()
	} else {
		execution.Status = "completed"
		completedAt := time.Now()
		execution.CompletedAt = &completedAt
	}

	// Save the updated execution
	if saveErr := m.config.SavePurchaseExecution(ctx, execution); saveErr != nil {
		logging.Errorf("Failed to save execution status: %v", saveErr)
	}

	// Update plan progress
	if err := m.updatePlanProgress(ctx, execution.PlanID); err != nil {
		logging.Errorf("Failed to update plan progress: %v", err)
	}

	return nil
}

// handleApproveMessage processes an approve message
func (m *Manager) handleApproveMessage(ctx context.Context, msg AsyncMessage) error {
	if msg.ExecutionID == "" || msg.Token == "" {
		return fmt.Errorf("execution_id and token required for approve message")
	}
	return m.ApproveExecution(ctx, msg.ExecutionID, msg.Token)
}

// handleCancelMessage processes a cancel message
func (m *Manager) handleCancelMessage(ctx context.Context, msg AsyncMessage) error {
	if msg.ExecutionID == "" || msg.Token == "" {
		return fmt.Errorf("execution_id and token required for cancel message")
	}
	return m.CancelExecution(ctx, msg.ExecutionID, msg.Token)
}
