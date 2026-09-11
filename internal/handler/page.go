package handler

import (
	"fmt"
	"net/http"
	"strconv"
)

const (
	DefaultLimit int32 = 20
	MaxLimit     int32 = 100
)

type Page struct {
	Limit  int32
	Offset int32
}

func (p Page) Meta() Meta { return Meta(p) }

func DecodePage(w http.ResponseWriter, r *http.Request) (Page, bool) {
	q := r.URL.Query()
	fields := map[string]string{}

	limit := DefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		switch {
		case err != nil:
			fields["limit"] = "must be an integer"
		case n < 1:
			fields["limit"] = "must be at least 1"
		case int32(n) > MaxLimit:
			fields["limit"] = fmt.Sprintf("must be at most %d", MaxLimit)
		default:
			limit = int32(n)
		}
	}

	offset := int32(0)
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		switch {
		case err != nil:
			fields["offset"] = "must be an integer"
		case n < 0:
			fields["offset"] = "must not be negative"
		default:
			offset = int32(n)
		}
	}

	if len(fields) > 0 {
		writeValidationError(w, fields)
		return Page{}, false
	}
	return Page{Limit: limit, Offset: offset}, true
}
