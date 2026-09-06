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

func TestPatternsConflict(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		// Cross-over (literal/wildcard swapped) → conflict.
		{"/orders/{id}/track", "/orders/by-status/{status}", true},
		// One strictly more specific (literal beats wildcard, same elsewhere) → OK.
		{"/orders/{id}", "/orders/by-status", false},
		{"/orders/health", "/orders/{id}", false},
		// Distinct trailing literals on the same {id} prefix → disjoint → OK.
		{"/orders/{id}/cancel", "/orders/{id}/ship", false},
		// Different segment counts can't overlap (single-segment wildcards).
		{"/orders/{id}", "/orders/{id}/track", false},
		// Identical pattern (wildcard name is irrelevant to matching) → conflict.
		{"/orders/{id}", "/orders/{oid}", true},
		// Same literal path → conflict (exact duplicate).
		{"/orders/list", "/orders/list", true},
		// Both wildcard at the cross position but a distinct literal elsewhere.
		{"/a/{x}/b", "/a/{y}/c", false},
		// Two cross-overs deeper in the path → conflict.
		{"/a/{x}/c", "/a/b/{y}", true},
	}
	for _, c := range cases {
		if got := PatternsConflict(c.a, c.b); got != c.want {
			t.Errorf("PatternsConflict(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
		if got := PatternsConflict(c.b, c.a); got != c.want {
			t.Errorf("PatternsConflict(%q,%q) [swapped] = %v, want %v", c.b, c.a, got, c.want)
		}
	}
}
