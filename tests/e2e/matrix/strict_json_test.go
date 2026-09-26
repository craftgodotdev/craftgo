package matrix

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// Strict JSON rejects an undeclared field by name; lenient mode ignores it.
func TestStrictJSONRejectsUnknownFields(t *testing.T) {
	ts, _ := boot(t)
	withExtra := func(name string) map[string]any {
		raw, err := json.Marshal(validProfile(name))
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		body["bogus"] = 1
		return body
	}
	if err := server.SetStrictJSON(true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.SetStrictJSON(false) })
	st, out := reqJSON(t, ts, http.MethodPost, "/api/v1/profiles", withExtra("strict-a"))
	if st != http.StatusBadRequest || !strings.Contains(string(out), "bogus: unknown field") {
		t.Fatalf("strict: status %d body %q, want 400 naming the field", st, out)
	}
	if err := server.SetStrictJSON(false); err != nil {
		t.Fatal(err)
	}
	if st, out := reqJSON(t, ts, http.MethodPost, "/api/v1/profiles", withExtra("strict-b")); st/100 != 2 {
		t.Fatalf("lenient: status %d body %q, want 2xx", st, out)
	}
}
