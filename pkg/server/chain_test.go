package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// tagMW records its entry and exit in trace as >tag and <tag.
func tagMW(trace *string, tag string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*trace += ">" + tag
			next.ServeHTTP(w, r)
			*trace += "<" + tag
		})
	}
}

func TestChainThenOrder(t *testing.T) {
	var trace string
	chain := NewChain(tagMW(&trace, "A"), tagMW(&trace, "B"), tagMW(&trace, "C"))
	chain.Then(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		trace += "|H|"
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := ">A>B>C|H|<C<B<A"
	if trace != want {
		t.Errorf("chain order = %q, want %q (outermost-first)", trace, want)
	}
}

// Then skips a nil middleware and keeps the others in order.
func TestChainThenSkipsNil(t *testing.T) {
	var trace string
	chain := NewChain(tagMW(&trace, "A"), nil, tagMW(&trace, "C"))
	chain.Then(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		trace += "|H|"
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if want := ">A>C|H|<C<A"; trace != want {
		t.Errorf("trace %q, want %q", trace, want)
	}
}

// Append copies, so two chains appended to one base keep their own innermost middleware and
// the base stays as it was.
func TestChainAppendDoesNotMutateReceiver(t *testing.T) {
	var trace string
	base := NewChain(tagMW(&trace, "A")).Append(tagMW(&trace, "B")).Append(tagMW(&trace, "C"))
	x := base.Append(tagMW(&trace, "X"))
	y := base.Append(tagMW(&trace, "Y"))
	for _, c := range []struct {
		name  string
		chain Chain
		want  string
	}{
		{"base", base, ">A>B>C|H|<C<B<A"},
		{"x", x, ">A>B>C>X|H|<X<C<B<A"},
		{"y", y, ">A>B>C>Y|H|<Y<C<B<A"},
	} {
		trace = ""
		c.chain.Then(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			trace += "|H|"
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		if trace != c.want {
			t.Errorf("%s: trace %q, want %q", c.name, trace, c.want)
		}
	}
}

func TestChainEmptyThenReturnsInnerHandler(t *testing.T) {
	hit := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hit = true })
	NewChain().Then(inner).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !hit {
		t.Error("empty chain must pass through to inner handler")
	}
}

func TestChainThenFunc(t *testing.T) {
	var trace string
	chain := NewChain(tagMW(&trace, "A"))
	chain.ThenFunc(func(w http.ResponseWriter, _ *http.Request) {
		trace += "|H|"
	}).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if trace != ">A|H|<A" {
		t.Errorf("ThenFunc wiring = %q, want >A|H|<A", trace)
	}
}
