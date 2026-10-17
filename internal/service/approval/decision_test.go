package approval

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/service"
)

func TestRejectedDecisionRequiresNonWhitespaceNotes(t *testing.T) {
	s := &approvalService{}
	for _, notes := range []string{"", " \t\n "} {
		_, err := s.Decide(context.Background(), authz.Actor{}, uuid.Nil, DecideInput{
			Decision: constant.DecisionRejected, Notes: &notes,
		})
		if err != service.ErrSubmitValidation {
			t.Errorf("Decide(notes=%q) error = %v, want %v", notes, err, service.ErrSubmitValidation)
		}
	}
}
