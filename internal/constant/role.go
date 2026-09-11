package constant

const (
	SystemRoleViewer  = "viewer"
	SystemRoleMember  = "member"
	SystemRoleAdmin = "admin"
)

func SystemRoleActions() map[string][]Action {
	return map[string][]Action{
		SystemRoleViewer:  {ActionTransactionView},
		SystemRoleMember:  {ActionTransactionView, ActionTransactionCreate, ActionDocumentUpload},
		SystemRoleAdmin:   {ActionTransactionView, ActionTransactionCreate, ActionDocumentUpload},
	}
}
