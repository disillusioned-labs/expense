package approval

import (
	"encoding/json"
	"testing"
)

func TestApprovalMoneyResponsesUseExactDecimalStrings(t *testing.T) {
	const amount int64 = 9007199254740993
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "assigned", value: AssignedResponse{TotalAmount: amount}},
		{name: "aging", value: AgingResponse{TotalAmount: amount}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			var decoded struct {
				TotalAmount json.RawMessage `json:"total_amount"`
			}
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got, want := string(decoded.TotalAmount), `"9007199254740993"`; got != want {
				t.Errorf("total_amount JSON = %s, want %s", got, want)
			}
		})
	}
}
