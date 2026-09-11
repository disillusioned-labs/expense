package handler

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/platform/authkit"
)

func ActorFrom(w http.ResponseWriter, r *http.Request) (authz.Actor, bool) {
	claims, ok := authkit.FromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "missing claims")
		return authz.Actor{}, false
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "invalid subject claim")
		return authz.Actor{}, false
	}
	orgID, err := uuid.Parse(claims.OrgID)
	if err != nil {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "missing organization context - switch organization first")
		return authz.Actor{}, false
	}
	return authz.Actor{UserID: userID, OrgID: orgID, Role: claims.Role}, true
}
