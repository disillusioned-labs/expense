package constant

type Action string

const (
	ActionTransactionView   Action = "transaction.view"
	ActionTransactionCreate Action = "transaction.create"
	ActionDocumentUpload    Action = "document.upload"
)

var AllActions = []Action{
	ActionTransactionView,
	ActionTransactionCreate,
	ActionDocumentUpload,
}

func IsValidAction(a string) bool {
	for _, known := range AllActions {
		if string(known) == a {
			return true
		}
	}
	return false
}
