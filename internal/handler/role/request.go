package role

import roleservice "github.com/disillusioned-labs/expense/internal/service/role"

type CreateRequest struct {
	Name    string   `json:"name" validate:"required,max=100"`
	Actions []string `json:"actions" validate:"required,min=1"`
}

type UpdateRequest struct {
	Name    *string   `json:"name"`
	Actions *[]string `json:"actions"`
}

func (r CreateRequest) ToInput() roleservice.CreateInput {
	return roleservice.CreateInput{Name: r.Name, Actions: r.Actions}
}

func (r UpdateRequest) ToInput() roleservice.UpdateInput {
	return roleservice.UpdateInput{Name: r.Name, Actions: r.Actions}
}
