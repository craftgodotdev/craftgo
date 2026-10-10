package route

import (
	"fmt"
	"net/http"
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
		{"/files/{rest...}", "rest"},
		{"/a/{id}/{rest...}", "id,rest"},
		{"/root/{$}", ""},
	} {
		if got := strings.Join(Vars(c.in), ","); got != c.want {
			t.Errorf("Vars(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// OpenAPIPath names a trailing `{name...}` as `{name}` and spells `{$}` as the
// trailing slash it matches.
func TestOpenAPIPath(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/users/{id}", "/users/{id}"},
		{"/files/{rest...}", "/files/{rest}"},
		{"/a/{id}/{rest...}", "/a/{id}/{rest}"},
		{"/root/{$}", "/root/"},
		{"/", "/"},
	} {
		if got := OpenAPIPath(c.in); got != c.want {
			t.Errorf("OpenAPIPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShape(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/users/{id}", "/users/{}"},
		{"/a/{x}/b/{y}", "/a/{}/b/{}"},
		{"/files/{path...}", "/files/{...}"},
		{"/x/{$}", "/x/{$}"},
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
		// A rest wildcard is more general than a one-segment one.
		{"/files/{path...}", "/files/{id}", false},
		{"/a/{x}/{y...}", "/a/{p}/{q}", false},
		{"/a/{x...}", "/{y}/b", true},
		// `{$}` matches only the end after a `/`, which no wildcard matches.
		{"/x/{$}", "/x/{id}", false},
		{"/{a}/{$}", "/x/{b}", false},
		{"/x/{$}", "/x/{rest...}", false},
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

// Two routes of one length with one verb conflict exactly when a real
// ServeMux refuses to register them together: they share a shape, or
// PatternsConflict holds.
func TestConflictsMatchServeMux(t *testing.T) {
	segment := func(kind byte, i int) string {
		switch kind {
		case 'x':
			return "x"
		case 'y':
			return "y"
		case 'w':
			return fmt.Sprintf("{w%d}", i)
		case 'r':
			return fmt.Sprintf("{r%d...}", i)
		}
		return "{$}"
	}
	var routes []string
	var build func(prefix string, n int)
	build = func(prefix string, n int) {
		if n == 3 {
			return
		}
		for _, k := range []byte("xywr$") {
			route := prefix + "/" + segment(k, n)
			routes = append(routes, route)
			if k != 'r' && k != '$' {
				build(route, n+1)
			}
		}
	}
	build("", 0)
	refuses := func(a, b string) (refused bool) {
		defer func() { refused = recover() != nil }()
		mux := http.NewServeMux()
		mux.Handle("GET "+a, http.NotFoundHandler())
		mux.Handle("GET "+b, http.NotFoundHandler())
		return false
	}
	for _, a := range routes {
		for _, b := range routes {
			if len(Segments(a)) != len(Segments(b)) {
				continue
			}
			want := refuses(a, b)
			if got := Shape(a) == Shape(b) || PatternsConflict(a, b); got != want {
				t.Errorf("%s and %s: ServeMux refuses the pair: %v, the check says %v", a, b, want, got)
			}
		}
	}
}

// SegmentProblem, EndsRoute and WildcardName read a segment as a real
// ServeMux does: it refuses a route exactly when a segment has a problem, a
// segment that must end the route does not, or a wildcard name repeats.
func TestSegmentRulesMatchServeMux(t *testing.T) {
	segments := []string{
		"users", "{id}", "{id...}", "{$}", "{}", "{...}", "{$...}", "{a-b}", "{1x}", "{_x9}",
		"{ünï}", "org-{org}", "{org}x", "{a}{b}", "x}", ".", "..", "...", "v1.2",
	}
	refuses := func(pattern string) (refused bool) {
		defer func() { refused = recover() != nil }()
		http.NewServeMux().Handle(pattern, http.NotFoundHandler())
		return false
	}
	for _, seg := range segments {
		for _, route := range []string{"/a/" + seg, "/a/" + seg + "/b", "/" + seg + "/{id}"} {
			segs := Segments(route)
			want := false
			names := map[string]bool{}
			for i, s := range segs {
				name, wild := WildcardName(s)
				switch {
				case SegmentProblem(s) != "", EndsRoute(s) && i < len(segs)-1, wild && names[name]:
					want = true
				}
				names[name] = wild
			}
			if got := refuses("GET " + route); got != want {
				t.Errorf("ServeMux refuses %q: %v, the rules say %v", route, got, want)
			}
		}
	}
}
