package contract

// ReminderPayload is the stable JSON shape consumed by approval reminder templates.
type ReminderPayload struct {
	ApproverName           string `json:"approver_name"`
	TransactionID          string `json:"transaction_id"`
	TransactionDescription string `json:"transaction_description,omitempty"`
	AmountDisplay          string `json:"amount_display"`
	WaitingDays            int    `json:"waiting_days"`
}
