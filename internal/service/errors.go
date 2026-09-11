// Package service holds cross-resource service bits: the domain error type
// and storage-error helpers. Each resource's business logic lives in its own
// subpackage (service/project, ...), exposing an interface plus an unexported
// implementation so handlers can mock the contract in tests.
//
// Domain errors are self-describing: each carries the HTTP status and
// machine-readable code it maps to. handler.WriteServiceError reads those
// fields, so adding a resource never means editing the shared handler layer.
package service

import (
	"github.com/disillusioned-labs/platform/errors"
)

// Error is a domain error that knows how it surfaces over HTTP.
type Error = errors.Error

// NewError builds a domain error for a resource-specific failure.
var NewError = errors.NewError

var (
	ErrUnauthenticated = errors.ErrUnauthenticated
	ErrForbidden       = errors.ErrForbidden
	ErrNotFound        = errors.ErrNotFound
	ErrConflict        = errors.ErrConflict
	ErrInternal        = errors.ErrInternal
)

// IsUniqueViolation reports whether err is a Postgres unique-constraint violation.
var IsUniqueViolation = errors.IsUniqueViolation

// Expense-specific errors.
var (
	ErrApproverStillAssigned  = NewError("APPROVER_STILL_ASSIGNED", 409, "member is still an approver on active rules")
	ErrApproverAlreadyExists  = NewError("APPROVER_ALREADY_EXISTS", 409, "approver already assigned for this project")
	ErrApproverNotMember      = NewError("APPROVER_NOT_MEMBER", 422, "approver is not a member of the organization")
	ErrLastApprovalRule       = NewError("LAST_APPROVAL_RULE", 409, "cannot delete the last approval rule")
	ErrTransactionLocked      = NewError("TRANSACTION_LOCKED", 409, "transaction already submitted")
	ErrInvalidTransition      = NewError("INVALID_TRANSITION", 409, "invalid status transition")
	ErrNoApproverResolved     = NewError("NO_APPROVER_RESOLVED", 409, "no approver resolved for this transaction")
	ErrNotYourTurn            = NewError("NOT_YOUR_TURN", 409, "approval is not activated yet")
	ErrProjectHasTransactions = NewError("PROJECT_HAS_TRANSACTIONS", 409, "project has transactions")
	ErrProjectArchived        = NewError("PROJECT_ARCHIVED", 409, "project is archived")
	ErrTooManyItems           = NewError("TOO_MANY_ITEMS", 422, "too many items")
	ErrItemInvalid            = NewError("ITEM_INVALID", 422, "item is invalid")
	ErrCurrencyInvalid        = NewError("CURRENCY_INVALID", 422, "currency is invalid")
	ErrCategoryInvalid        = NewError("CATEGORY_INVALID", 422, "category is invalid")
	ErrInvalidAmount          = NewError("INVALID_AMOUNT", 422, "amount is invalid")
	ErrInvalidAction          = NewError("INVALID_ACTION", 422, "action is invalid")
	ErrRoleNameTaken          = NewError("ROLE_NAME_TAKEN", 409, "role name already taken")
	ErrSystemRoleLocked       = NewError("SYSTEM_ROLE_LOCKED", 409, "system role is locked")
	ErrRoleInUse              = NewError("ROLE_IN_USE", 409, "role is still in use")
	ErrSubmitValidation       = NewError("SUBMIT_VALIDATION", 422, "submit validation failed")
	ErrOrgFrozen              = NewError("ORG_FROZEN", 403, "organization is frozen")
	ErrOrgDeleted             = NewError("ORG_DELETED", 404, "organization is deleted")
	ErrServiceNotAllowed      = NewError("SERVICE_NOT_ALLOWED", 403, "service access not allowed for this organization")
)
