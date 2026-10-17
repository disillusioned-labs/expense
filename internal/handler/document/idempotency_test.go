package document

import (
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestUploadIdempotencyKey(t *testing.T) {
	valid := uuid.NewString()
	for _, tt := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "valid UUID", value: valid, want: true},
		{name: "missing", want: false},
		{name: "malformed", value: "retry-1", want: false},
		{name: "zero UUID", value: uuid.Nil.String(), want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", nil)
			if tt.value != "" {
				r.Header.Set("Idempotency-Key", tt.value)
			}
			key, ok := uploadIdempotencyKey(r)
			if ok != tt.want {
				t.Fatalf("uploadIdempotencyKey() ok = %v, want %v", ok, tt.want)
			}
			if ok && key.String() != valid {
				t.Fatalf("uploadIdempotencyKey() = %s, want %s", key, valid)
			}
		})
	}

	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Add("Idempotency-Key", valid)
	r.Header.Add("Idempotency-Key", uuid.NewString())
	if _, ok := uploadIdempotencyKey(r); ok {
		t.Fatal("duplicate Idempotency-Key headers were accepted")
	}
}
