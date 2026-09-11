package service

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/disillusioned-labs/expense/internal/repository"
)

// Emit writes one domain event into the transactional outbox. Call it inside
// the same ExecTx as the state change it announces, so a committed state
// change can never be missing its event (and vice versa).
func Emit(ctx context.Context, q repository.Querier, aggregateType string, aggregateID uuid.UUID, eventType string, topic string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = q.CreateOutboxEvent(ctx, repository.CreateOutboxEventParams{
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		EventType:     eventType,
		EventVersion:  1,
		Topic:         topic,
		Payload:       body,
	})
	return err
}
