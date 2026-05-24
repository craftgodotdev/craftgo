package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tagMW returns a middleware that appends `:tag` to a shared trace
// before and after delegating, so test assertions can compare the
// concatenated trace against the expected outermost-first order.
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

func TestChainThenSkipsNil(t *testing.T) {
	var trace string
	chain := NewChain(tagMW(&trace, "A"), nil, tagMW(&trace, "C"))
	chain.Then(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		trace += "|H|"
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if strings.Contains(trace, "B") || !strings.Contains(trace, ">A") || !strings.Contains(trace, ">C") {
		t.Errorf("nil middleware should be skipped silently; got trace %q", trace)
	}
}

// TestChainAppendDoesNotMutateReceiver pins the value-semantics
// contract: a base chain shared between routes must not pick up
// extras from one route's Append landing on another route's chain.
func TestChainAppendDoesNotMutateReceiver(t *testing.T) {
	var trace string
	base := NewChain(tagMW(&trace, "A"))
	derived := base.Append(tagMW(&trace, "B"))

	base.Then(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		trace += "|H|"
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(trace, "B") {
		t.Errorf("base chain leaked Append target; trace %q must not contain B", trace)
	}

	trace = ""
	derived.Then(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		trace += "|H|"
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(trace, ">A>B") {
		t.Errorf("derived chain missing appended slot; got %q", trace)
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
