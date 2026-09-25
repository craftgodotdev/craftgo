package matrix

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// A validation failure names what the wire carries: a cross-field group lists
// its members by their @json keys and an enum its values, and neither names
// the DSL type.
func TestValidationMessagesUseWireNames(t *testing.T) {
	ts := bootAll(t)
	for _, tc := range []struct {
		name, path, body string
		want, dslName    string
	}{
		{
			name: "requiresOneOf over @json members", path: "/api/combine/pairs/renamed/x", body: `{}`,
			want: "requiresOneOf [primary_email backup_email] - at least one must be set", dslName: "PairsRenamed",
		},
		{
			name: "enum outside its values", path: "/api/combine/defaults/enum", body: `{"c": "Purple"}`,
			want: "c: must be one of [Red Green Blue]", dslName: "Color",
		},
	} {
		resp, err := ts.Client().Post(ts.URL+tc.path, "application/json", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), tc.want) {
			t.Errorf("%s: want 400 carrying %q, got %d %q", tc.name, tc.want, resp.StatusCode, body)
		}
		if strings.Contains(string(body), tc.dslName) {
			t.Errorf("%s: the message names the DSL type %s: %q", tc.name, tc.dslName, body)
		}
	}
}
