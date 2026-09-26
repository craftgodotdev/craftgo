package matrix

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// A failure inside a nested value names the fields that lead to it: a nested
// @requiresOneOf and a field four levels down.
func TestNestedErrorsNameTheirPath(t *testing.T) {
	ts := bootAll(t)
	for _, tc := range []struct{ name, path, body, want string }{
		{
			name: "nested @requiresOneOf", path: "/api/nested/user",
			body: `{"addr":{"street":"s","city":"c"},"name":"n","boss":{"addr":{"street":"s","city":"c"}}}`,
			want: `{"message":"boss: requiresOneOf [name alias] - at least one must be set"}`,
		},
		{
			name: "four levels down", path: "/api/nested/person",
			body: `{"home":{"rooms":[{"furniture":[{"name":"","weight":1}]}]}}`,
			want: `{"message":"home: rooms: furniture: name: length less than 1"}`,
		},
	} {
		resp, err := ts.Client().Post(ts.URL+tc.path, "application/json", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || strings.TrimSpace(string(body)) != tc.want {
			t.Errorf("%s: got %d %q, want 400 %s", tc.name, resp.StatusCode, body, tc.want)
		}
	}
}
