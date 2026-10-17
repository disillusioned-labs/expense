package role

import (
	"net/http/httptest"
	"strings"
	"testing"

	handler "github.com/disillusioned-labs/expense/internal/handler"
)

func TestUpdateRequestValidatesRoleNameBound(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{name: `"name":"Finance"`, want: true},
		{name: `"name":"` + strings.Repeat("x", 101) + `"`, want: false},
		{name: `"name":""`, want: false},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("PATCH", "/", strings.NewReader("{"+tc.name+"}"))
		_, ok := handler.DecodeValid[UpdateRequest](recorder, request)
		if ok != tc.want {
			t.Errorf("DecodeValid(%s) = %v, want %v (status %d)", tc.name, ok, tc.want, recorder.Code)
		}
	}
}
