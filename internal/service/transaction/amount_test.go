package transaction

import (
	"testing"

	"github.com/disillusioned-labs/expense/internal/service"
)

func TestValidateItemsRejectsSumOverflow(t *testing.T) {
	items := []ItemInput{
		{Description: "large", Quantity: 1, Amount: 9223372036854775807},
		{Description: "overflow", Quantity: 1, Amount: 1},
	}
	if err := validateItems(items); err != service.ErrInvalidAmount {
		t.Fatalf("validateItems() error = %v, want %v", err, service.ErrInvalidAmount)
	}
}

func TestValidateItemsAcceptsInt64MaximumTotal(t *testing.T) {
	items := []ItemInput{{Description: "maximum", Quantity: 1, Amount: 9223372036854775807}}
	if err := validateItems(items); err != nil {
		t.Fatalf("validateItems() error = %v, want nil", err)
	}
}
