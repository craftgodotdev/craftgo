package route

import (
	"strings"
	"testing"
)

func TestVars(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
	}{
		{"/users/{id}", "id"},
		{"/a/{x}/b/{y}", "x,y"},
		{"/tenant/{tenantID}", "tenantID"},
		{"/plain/path", ""},
		{"/", ""},
		{"", ""},
		{"/v{1}/x", ""},  // a variable is a whole segment
		{"/{}/x", ""},    // an empty name is not a variable
		{"/{open/x", ""}, // unterminated
	} {
		if got := strings.Join(Vars(c.in), ","); got != c.want {
			t.Errorf("Vars(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShape(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/users/{id}", "/users/{}"},
		{"/a/{x}/b/{y}", "/a/{}/b/{}"},
		{"/plain", "/plain"},
		{"/", "/"},
	} {
		if got := Shape(c.in); got != c.want {
			t.Errorf("Shape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
