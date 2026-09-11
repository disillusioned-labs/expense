package transaction

import (
	transactionservice "github.com/disillusioned-labs/expense/internal/service/transaction"
)

type ItemRequest struct {
	Description string `json:"description" validate:"required"`
	Quantity    int32  `json:"quantity" validate:"min=1"`
	Amount      int64  `json:"amount" validate:"min=0"`
}

type UpdateRequest struct {
	Description *string       `json:"description"`
	Items       []ItemRequest `json:"items"`
	Currency    string        `json:"currency"`
	Category    *string       `json:"category"`
}

func toItemInputs(items []ItemRequest) []transactionservice.ItemInput {
	if items == nil {
		return nil
	}
	out := make([]transactionservice.ItemInput, len(items))
	for i, it := range items {
		out[i] = transactionservice.ItemInput{Description: it.Description, Quantity: it.Quantity, Amount: it.Amount}
	}
	return out
}

func (r UpdateRequest) ToInput() transactionservice.UpdateInput {
	return transactionservice.UpdateInput{
		Description: r.Description,
		Items:       toItemInputs(r.Items),
		Currency:    r.Currency,
		Category:    r.Category,
	}
}
