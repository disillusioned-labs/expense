package approval

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
	notificationcontract "github.com/disillusioned-labs/platform/contract/notification"
)

// Notification type names. The notification service's template registry and
// its templates/email/<name>/ directories must carry a matching entry, or
// delivery fails at render time.
const (
	NotificationStepActivated    = "approval_step_activated"
	NotificationDecisionReceived = "approval_decision_received"
	// NotificationReminder is emitted by the reminder worker
	// (internal/service/reminder), not by the approval engine.
	NotificationReminder = "approval_reminder"
)

// StepActivatedPayload feeds the approval_step_activated templates.
type StepActivatedPayload struct {
	ApproverName           string `json:"approver_name"`
	TransactionID          string `json:"transaction_id"`
	TransactionDescription string `json:"transaction_description,omitempty"`
	AmountDisplay          string `json:"amount_display"`
	Step                   int    `json:"step"`
}

// DecisionReceivedPayload feeds the approval_decision_received templates.
type DecisionReceivedPayload struct {
	CreatorName            string `json:"creator_name"`
	TransactionID          string `json:"transaction_id"`
	TransactionDescription string `json:"transaction_description,omitempty"`
	AmountDisplay          string `json:"amount_display"`
	Decision               string `json:"decision"` // "approved" | "rejected"
	DeciderName            string `json:"decider_name"`
}

// ReminderPayload feeds the approval_reminder templates.
type ReminderPayload struct {
	ApproverName           string `json:"approver_name"`
	TransactionID          string `json:"transaction_id"`
	TransactionDescription string `json:"transaction_description,omitempty"`
	AmountDisplay          string `json:"amount_display"`
	WaitingDays            int    `json:"waiting_days"`
}

// ValidEmail guards notification targets: snapshot emails can be the literal
// "unknown" on rows created before GetUsersInfo was wired, and no email means
// nothing to deliver to. Exported because the reminder worker filters on it
// too.
func ValidEmail(email string) bool {
	return email != "" && email != snapshotUnknownEmail && strings.Contains(email, "@")
}

// notificationEvent builds a notification.created envelope. The email target
// appears only for a deliverable address; the push target is always present
// and carries the recipient's user id - the notification service resolves it
// to device tokens via identity's GetDeviceTokens gRPC. A user with no
// registered devices simply produces no push deliveries.
func notificationEvent(notificationType, recipientID, email string, payload any) (notificationcontract.CreatedEvent, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return notificationcontract.CreatedEvent{}, err
	}

	targets := make([]notificationcontract.Target, 0, 2)
	if ValidEmail(email) {
		targets = append(targets, notificationcontract.Target{
			Channel:     notificationcontract.ChannelEmail,
			Destination: email,
		})
	}
	targets = append(targets, notificationcontract.Target{
		Channel:     notificationcontract.ChannelPush,
		Destination: recipientID,
	})

	event := notificationcontract.CreatedEvent{
		NotificationType: notificationType,
		Category:         notificationcontract.CategoryTransactional,
		RecipientID:      recipientID,
		Targets:          targets,
		Payload:          body,
	}
	return event, event.Validate()
}

// emitStepActivated publishes the "your turn" notification for an approver
// whose step just went active. A snapshot email of "unknown" only removes the
// email target; the push target (user id) is unaffected.
func emitStepActivated(ctx context.Context, q repository.Querier, orgID, txID, approverID uuid.UUID, approverName, approverEmail, description string, total int64, currency string, step int) error {
	event, err := notificationEvent(NotificationStepActivated, approverID.String(), approverEmail, StepActivatedPayload{
		ApproverName:           approverName,
		TransactionID:          txID.String(),
		TransactionDescription: description,
		AmountDisplay:          FormatAmount(total, currency),
		Step:                   step,
	})
	if err != nil {
		return err
	}
	return service.Emit(ctx, q, "transaction", txID, notificationcontract.EventTypeCreated, constant.TopicNotificationTransactional, event)
}

// emitDecisionReceived publishes the "a decision landed on your transaction"
// notification to the transaction creator.
func emitDecisionReceived(ctx context.Context, q repository.Querier, orgID, txID, creatorID uuid.UUID, creatorEmail, creatorName, description string, total int64, currency, decision, deciderName string) error {
	event, err := notificationEvent(NotificationDecisionReceived, creatorID.String(), creatorEmail, DecisionReceivedPayload{
		CreatorName:            creatorName,
		TransactionID:          txID.String(),
		TransactionDescription: description,
		AmountDisplay:          FormatAmount(total, currency),
		Decision:               decision,
		DeciderName:            deciderName,
	})
	if err != nil {
		return err
	}
	return service.Emit(ctx, q, "transaction", txID, notificationcontract.EventTypeCreated, constant.TopicNotificationTransactional, event)
}
