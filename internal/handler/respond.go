package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/disillusioned-labs/expense/internal/service"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeBadRequest       = "BAD_REQUEST"
	CodeValidationFailed = "VALIDATION_FAILED"
	CodeNotFound         = "NOT_FOUND"
	CodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	CodePayloadTooLarge  = "PAYLOAD_TOO_LARGE"
	CodeRateLimited      = "RATE_LIMITED"
	CodeTimeout          = "TIMEOUT"
	CodeInternal         = "INTERNAL"
)

type Meta struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
}

type successEnvelope struct {
	Data any   `json:"data"`
	Meta *Meta `json:"meta,omitempty"`
}

type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	// Details carries structured context the client acts on (e.g. the
	// APPROVER_STILL_ASSIGNED rules list). Populated from the domain error's
	// optional Details; nil for every other error.
	Details any `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	if v == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		return
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL","message":"internal server error"}}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func OK(w http.ResponseWriter, status int, data any) {
	WriteJSON(w, status, successEnvelope{Data: data})
}

func OKList(w http.ResponseWriter, data any, meta Meta) {
	WriteJSON(w, http.StatusOK, successEnvelope{Data: data, Meta: &meta})
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

func writeValidationError(w http.ResponseWriter, fields map[string]string) {
	WriteJSON(w, http.StatusUnprocessableEntity, errorEnvelope{Error: errorBody{
		Code:    CodeValidationFailed,
		Message: "validation failed",
		Fields:  fields,
	}})
}

func WriteServiceError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	span := trace.SpanFromContext(r.Context())

	if ctxErr := r.Context().Err(); ctxErr != nil {
		switch {
		case errors.Is(ctxErr, context.DeadlineExceeded):
			span.SetStatus(codes.Error, "request timeout")
			log.WarnContext(r.Context(), "request timed out", "error", err)
			WriteError(w, http.StatusGatewayTimeout, CodeTimeout, "request timed out")
			return
		case errors.Is(ctxErr, context.Canceled):
			span.SetStatus(codes.Error, "client disconnected")
			log.DebugContext(r.Context(), "client disconnected before response", "error", err)
			return
		}
	}

	var domainErr *service.Error
	if errors.As(err, &domainErr) {
		span.SetStatus(codes.Error, domainErr.Code)
		body := errorBody{Code: domainErr.Code, Message: domainErr.Message, Details: domainErr.Details}
		WriteJSON(w, domainErr.Status, errorEnvelope{Error: body})
		return
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, "internal error")
	log.ErrorContext(r.Context(), "unhandled service error", "error", err)
	WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error")
}
