package transaction

import (
	"encoding/json"
	"testing"
)

func TestItemRequestAmountUsesExactDecimalString(t *testing.T) {
	const largeAmount int64 = 9007199254740993
	var request ItemRequest
	if err := json.Unmarshal([]byte(`{"description":"item","quantity":1,"amount":"9007199254740993"}`), &request); err != nil {
		t.Fatalf("decode exact amount string: %v", err)
	}
	if request.Amount != largeAmount {
		t.Fatalf("decoded amount = %d, want %d", request.Amount, largeAmount)
	}
	if err := json.Unmarshal([]byte(`{"description":"item","quantity":1,"amount":9007199254740993}`), &request); err == nil {
		t.Fatal("accepted an unsafe JSON number")
	}
	if err := json.Unmarshal([]byte(`{"description":"item","quantity":1,"amount":"9223372036854775808"}`), &request); err == nil {
		t.Fatal("accepted an amount above int64 maximum")
	}
}

func TestTransactionMoneyResponseUsesExactDecimalStrings(t *testing.T) {
	const amount int64 = 9223372036854775807
	response := TransactionResponse{
		TotalAmount: amount,
		Items:       []ItemResponse{{Amount: amount}},
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var decoded struct {
		TotalAmount json.RawMessage `json:"total_amount"`
		Items       []struct {
			Amount json.RawMessage `json:"amount"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, want := string(decoded.TotalAmount), `"9223372036854775807"`; got != want {
		t.Errorf("total_amount JSON = %s, want %s", got, want)
	}
	if got, want := string(decoded.Items[0].Amount), `"9223372036854775807"`; got != want {
		t.Errorf("item amount JSON = %s, want %s", got, want)
	}
}
