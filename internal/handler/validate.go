package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
	return v
}

const maxBodyBytes = 1 << 20 // 1 MiB

func DecodeValid[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var req T
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			WriteError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
				fmt.Sprintf("request body must not exceed %d bytes", maxErr.Limit))
			return req, false
		}
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid request body: "+err.Error())
		return req, false
	}
	if err := validate.Struct(req); err != nil {
		var verrs validator.ValidationErrors
		if !errors.As(err, &verrs) {
			writeValidationError(w, nil)
			return req, false
		}
		fields := make(map[string]string, len(verrs))
		for _, fe := range verrs {
			fields[fe.Field()] = fieldMessage(fe)
		}
		writeValidationError(w, fields)
		return req, false
	}
	return req, true
}

func fieldMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email address"
	case "min":
		return fmt.Sprintf("must be at least %s characters", fe.Param())
	case "max":
		return fmt.Sprintf("must be at most %s characters", fe.Param())
	default:
		return fmt.Sprintf("is invalid (rule: %s)", fe.Tag())
	}
}
