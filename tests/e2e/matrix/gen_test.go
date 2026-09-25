package matrix

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/server"

	designtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/design"
	svctypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/services"
)

func TestGen_ErrorTypeShape(t *testing.T) {
	err := svctypes.NewAcctUserNotFoundErr()
	if err.HTTPStatus() != 404 {
		t.Errorf("status %d", err.HTTPStatus())
	}
	if svctypes.ErrCodeAcctUserNotFound != "ACCT_USER_NOT_FOUND" {
		t.Errorf("code const %q", svctypes.ErrCodeAcctUserNotFound)
	}
	if err.Error() == "" {
		t.Error("Error() empty")
	}
}

// A generated error writes its own JSON: a body-less one the {code, message}
// envelope, one with a body its declared fields, {} when every optional field
// is unset. Its code and message need no constructor.
func TestGen_ErrorWireShape(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"body-less":           {svctypes.NewAcctUserNotFoundErr(), `{"code":"ACCT_USER_NOT_FOUND","message":"Not found"}`},
		"optional body unset": {designtypes.NewThrottledErr(designtypes.ThrottledBody{}), `{}`},
	}
	for name, c := range cases {
		rec := httptest.NewRecorder()
		server.WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), c.err)
		if got := strings.TrimSpace(rec.Body.String()); got != c.want {
			t.Errorf("%s: body = %s, want %s", name, got, c.want)
		}
	}
	var zero svctypes.AcctUserNotFoundErr
	if zero.Error() != "Not found" || zero.ErrCode() != svctypes.ErrCodeAcctUserNotFound {
		t.Errorf("zero value: Error() = %q, ErrCode() = %q", zero.Error(), zero.ErrCode())
	}
}

// An error whose fields all ride headers, its own or a mixin's, writes the
// {code, message} envelope its OpenAPI schema requires.
func TestGen_HeaderOnlyErrorWritesItsDocumentedBody(t *testing.T) {
	schemas := readSchemas(t)
	for _, c := range []struct {
		schema, code, message, header, value string
		status                               int
		err                                  error
	}{
		{"RateLimitedErr", svctypes.ErrCodeRateLimited, "Too many requests", "Retry-After", "30", http.StatusTooManyRequests,
			svctypes.NewRateLimitedErr(svctypes.RateLimitedBody{RetryAfter: 30})},
		{"OverloadedErr", svctypes.ErrCodeOverloaded, "Service unavailable", "X-Retry-In", "5", http.StatusServiceUnavailable,
			svctypes.NewOverloadedErr(svctypes.OverloadedBody{RetryHint: svctypes.RetryHint{RetryIn: 5}})},
	} {
		rec := httptest.NewRecorder()
		server.WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), c.err)
		if rec.Code != c.status || rec.Header().Get(c.header) != c.value {
			t.Errorf("%s: status %d, %s %q; want %d, %q", c.schema, rec.Code, c.header, rec.Header().Get(c.header), c.status, c.value)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: body %s: %v", c.schema, rec.Body, err)
		}
		if body["code"] != c.code || body["message"] != c.message {
			t.Errorf("%s: body = %s, want the %s envelope", c.schema, rec.Body, c.code)
		}
		documented := schemas[c.schema].Required
		if got := slices.Sorted(maps.Keys(body)); !slices.Equal(got, slices.Sorted(slices.Values(documented))) {
			t.Errorf("%s: wire keys %v, the schema requires %v", c.schema, got, documented)
		}
	}
}

func TestGen_EnumConstants(t *testing.T) {
	if string(svctypes.AcctRoleAdmin) != "admin" {
		t.Errorf("got %q", svctypes.AcctRoleAdmin)
	}
	if int(svctypes.AcctPriorityHigh) != 3 {
		t.Errorf("got %d", svctypes.AcctPriorityHigh)
	}
}

func TestGen_DocCommentsPropagate(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Dir(here)
	data, err := os.ReadFile(filepath.Join(root, "internal/types/services/types.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"// AcctUser is the wire-level shape of a stored user.",
		"// GetUserReq is the path-bound input for GET /account-users/{id}.",
		"// id locates the user.",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("doc comment not propagated: %q", want)
		}
	}
}
