package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ErrNotImplemented, as a new logic stub returns it, answers 501 {"message":"not implemented"},
// wrapped or not, and is not logged.
func TestErrNotImplementedAnswers501(t *testing.T) {
	logs := observeLogs(t)
	for name, err := range map[string]error{
		"bare":    ErrNotImplemented,
		"wrapped": fmt.Errorf("create user: %w", ErrNotImplemented),
	} {
		rec := httptest.NewRecorder()
		WriteError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), err)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501", name, rec.Code)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != `{"message":"not implemented"}` {
			t.Errorf("%s: body = %s", name, got)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: Content-Type = %q", name, ct)
		}
	}
	if n := logs.Len(); n != 0 {
		t.Errorf("an expected outcome was logged %d times", n)
	}
}
