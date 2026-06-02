package matrix

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
