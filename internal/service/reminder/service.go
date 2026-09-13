// Package reminder nags approvers who have not decided. It is visibility
// only: a pending approval stays pending, and who may decide it never
// changes (the D14/MVP stall limitation - the remedy is making it loud).
package reminder

import (
	"context"
	"log/slog"
	"time"

	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel"

	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/contract"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
	approvalserv "github.com/disillusioned-labs/expense/internal/service/approval"
	notificationcontract "github.com/disillusioned-labs/platform/contract/notification"
)

var tracer = otel.Tracer("service/reminder")

// Service sends one reminder per stale active approval step per run.
type Service interface {
	// RemindOnce scans once and returns how many reminders it emitted.
	RemindOnce(ctx context.Context) (int, error)
}

type reminderService struct {
	repo          repository.Store
	identity      contract.IdentityClient
	thresholdDays int
	batchSize     int
	log           *slog.Logger
}

// NewService builds the reminder worker's service. thresholdDays is the age
// (in days) an active approval step must reach before it gets nagged;
// batchSize caps a single scan.
func NewService(repo repository.Store, identity contract.IdentityClient, thresholdDays, batchSize int, log *slog.Logger) Service {
	return &reminderService{repo: repo, identity: identity, thresholdDays: thresholdDays, batchSize: batchSize, log: log}
}

// RemindOnce lists every active (undecided) approval step waiting longer
// than the threshold, resolves fresh approver emails from identity (snapshot
// emails can be stale or "unknown"), and emits one notification.created per
// deliverable reminder through the outbox. The scan interval - not
// deduplication - controls frequency, mirroring how the outbox worker ticks.
func (s *reminderService) RemindOnce(ctx context.Context) (int, error) {
	ctx, span := tracer.Start(ctx, "ReminderService.RemindOnce")
	defer span.End()

	cutoff := pgtype.Timestamptz{Time: time.Now().AddDate(0, 0, -s.thresholdDays), Valid: true}
	rows, err := s.repo.ListStaleActiveApprovals(ctx, repository.ListStaleActiveApprovalsParams{
		ActivatedAt: cutoff,
		Limit:       int32(s.batchSize),
	})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	// One batched identity lookup per organization/approver pair.
	seen := make(map[uuid.UUID]bool)
	pairs := make([]contract.UserOrgPair, 0, len(rows))
	for _, r := range rows {
		if !seen[r.ApproverID] {
			seen[r.ApproverID] = true
			pairs = append(pairs, contract.UserOrgPair{UserID: r.ApproverID, OrganizationID: r.OrganizationID})
		}
	}
	users, err := s.identity.GetUsersInfo(ctx, pairs)
	if err != nil {
		return 0, err
	}
	byID := make(map[uuid.UUID]contract.UserInfo, len(users))
	for _, u := range users {
		byID[u.UserID] = u
	}

	sent := 0
	for _, r := range rows {
		u, ok := byID[r.ApproverID]
		// Push reaches the user regardless of snapshot email quality; a bad
		// email only removes the email target (see notify.go).
		if !ok || !u.IsActive {
			continue
		}

		description := ""
		if r.Description.Valid {
			description = r.Description.String
		}
		waitingDays := int(time.Since(r.ActivatedAt.Time).Hours() / 24)
		payload, err := json.Marshal(approvalserv.ReminderPayload{
			ApproverName:           u.Name,
			TransactionID:          r.TransactionID.String(),
			TransactionDescription: description,
			AmountDisplay:          approvalserv.FormatAmount(r.TotalAmount, r.Currency),
			WaitingDays:            waitingDays,
		})
		if err != nil {
			return sent, err
		}
		targets := make([]notificationcontract.Target, 0, 2)
		if approvalserv.ValidEmail(u.Email) {
			targets = append(targets, notificationcontract.Target{
				Channel:     notificationcontract.ChannelEmail,
				Destination: u.Email,
			})
		}
		targets = append(targets, notificationcontract.Target{
			Channel:     notificationcontract.ChannelPush,
			Destination: u.UserID.String(),
		})
		event := notificationcontract.CreatedEvent{
			NotificationType: approvalserv.NotificationReminder,
			Category:         notificationcontract.CategoryTransactional,
			RecipientID:      u.UserID.String(),
			Targets:          targets,
			Payload:          payload,
		}
		if err := event.Validate(); err != nil {
			return sent, err
		}
		if err := service.Emit(ctx, s.repo, "approval", r.ID, notificationcontract.EventTypeCreated, constant.TopicNotificationTransactional, event); err != nil {
			return sent, err
		}
		sent++
	}
	s.log.InfoContext(ctx, "approval reminders emitted", "stale", len(rows), "emitted", sent)
	return sent, nil
}
