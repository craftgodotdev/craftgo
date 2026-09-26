package wire

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func decs(names ...string) []*ast.Decorator {
	out := make([]*ast.Decorator, 0, len(names))
	for _, n := range names {
		out = append(out, &ast.Decorator{Name: n})
	}
	return out
}

func TestRawSides(t *testing.T) {
	cases := []struct {
		name     string
		ds       []*ast.Decorator
		wantReq  bool
		wantResp bool
	}{
		{"nil", nil, false, false},
		{"unrelated", decs("doc", "status"), false, false},
		{"rawRequest", decs("rawRequest"), true, false},
		{"rawResponse", decs("rawResponse"), false, true},
		{"both flags", decs("rawRequest", "rawResponse"), true, true},
		{"passthrough", decs("passthrough"), true, true},
		{"passthrough + rawRequest", decs("passthrough", "rawRequest"), true, true},
		{"passthrough + rawResponse", decs("rawResponse", "passthrough"), true, true},
		{"nil entry tolerated", []*ast.Decorator{nil, {Name: "rawResponse"}}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, resp := RawSides(c.ds)
			if req != c.wantReq || resp != c.wantResp {
				t.Errorf("RawSides = (%v, %v), want (%v, %v)", req, resp, c.wantReq, c.wantResp)
			}
		})
	}
}

func TestWireName(t *testing.T) {
	withArg := func(v ast.Expr) *ast.Field {
		return &ast.Field{Name: "id", Decorators: []*ast.Decorator{
			{Name: BindingPath, Args: []*ast.DecoratorArg{{Value: v}}},
		}}
	}
	cases := []struct {
		name string
		f    *ast.Field
		want string
	}{
		{"no decorator", &ast.Field{Name: "id"}, "id"},
		{"no argument", &ast.Field{Name: "id", Decorators: decs(BindingPath)}, "id"},
		{"string argument", withArg(&ast.StringLit{Value: "user-id"}), "user-id"},
		{"empty string", withArg(&ast.StringLit{}), "id"},
		{"non-string argument", withArg(&ast.IntLit{}), "id"},
		{"another decorator's string", &ast.Field{Name: "id", Decorators: []*ast.Decorator{
			{Name: "doc", Args: []*ast.DecoratorArg{{Value: &ast.StringLit{Value: "x"}}}},
			{Name: BindingPath},
		}}, "id"},
		{"nil field", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := WireName(c.f, BindPath); got != c.want {
				t.Errorf("WireName = %q, want %q", got, c.want)
			}
		})
	}
	header := &ast.Field{Name: "traceId", Decorators: []*ast.Decorator{
		{Name: BindingHeader, Args: []*ast.DecoratorArg{{Value: &ast.StringLit{Value: "X-Trace-Id"}}}},
	}}
	if got := WireName(header, BindQuery); got != "traceId" {
		t.Errorf("another binding's argument must not leak: got %q, want traceId", got)
	}
}

func TestBindingKind(t *testing.T) {
	cases := []struct {
		decs   []string
		want   Binding
		wantOK bool
	}{
		{[]string{"query"}, BindQuery, true},
		{[]string{"path"}, BindPath, true},
		{[]string{"header"}, BindHeader, true},
		{[]string{"cookie"}, BindCookie, true},
		{[]string{"body"}, BindBody, true},
		{[]string{"form"}, BindForm, true},
		{[]string{"doc", "query"}, BindQuery, true}, // non-binding decorators are skipped
		{[]string{"sensitive"}, BindBody, false},
		{nil, BindBody, false},
		{[]string{"doc"}, BindBody, false},
	}
	for _, c := range cases {
		if got, ok := BindingKind(decs(c.decs...)); got != c.want || ok != c.wantOK {
			t.Errorf("BindingKind(%v) = (%v, %v), want (%v, %v)", c.decs, got, ok, c.want, c.wantOK)
		}
	}
}

func TestBindingStringNamesTheDecorator(t *testing.T) {
	for _, b := range []Binding{BindBody, BindPath, BindQuery, BindHeader, BindCookie, BindForm} {
		if got, ok := BindingKind(decs(b.String())); !ok || got != b {
			t.Errorf("BindingKind(@%s) = (%v, %v), want (%v, true)", b, got, ok, b)
		}
	}
	if got := BindSensitive.String(); got != "sensitive" {
		t.Errorf("BindSensitive.String() = %q, want sensitive", got)
	}
}

// Every binding but the body and @sensitive names a parameter.
func TestBindingIsParam(t *testing.T) {
	for b, want := range map[Binding]bool{BindBody: false, BindSensitive: false,
		BindPath: true, BindQuery: true, BindHeader: true, BindCookie: true, BindForm: true} {
		if got := b.IsParam(); got != want {
			t.Errorf("%v.IsParam() = %v, want %v", b, got, want)
		}
	}
}

func TestRequestFieldBinding(t *testing.T) {
	field := func(name string, ds ...string) *ast.Field {
		return &ast.Field{Name: name, Decorators: decs(ds...)}
	}
	paths := map[string]bool{"id": true}
	cases := []struct {
		f        *ast.Field
		bodyVerb bool
		want     Binding
		auto     bool
	}{
		{field("q", "query"), false, BindQuery, false}, // explicit wins, not auto
		{field("s", "sensitive"), false, BindSensitive, false},
		{field("b", "body"), true, BindBody, false},
		{field("up", "form"), true, BindForm, false},
		{field("id"), false, BindPath, true},      // un-decorated, name matches a path segment
		{field("page"), false, BindQuery, true},   // un-decorated on a body-less verb
		{field("payload"), true, BindBody, false}, // un-decorated on a body verb
	}
	for _, c := range cases {
		got, auto := RequestFieldBinding(c.f, paths, c.bodyVerb)
		if got != c.want || auto != c.auto {
			t.Errorf("RequestFieldBinding(%q, bodyVerb=%v) = (%v, %v), want (%v, %v)", c.f.Name, c.bodyVerb, got, auto, c.want, c.auto)
		}
	}
}

// TestJSONShapeSplitsPresenceFourWays checks the presence JSONShape reports
// for each mix of `?`, `@nullable` and `@sensitive`.
func TestJSONShapeSplitsPresenceFourWays(t *testing.T) {
	field := func(name string, optional bool, dec ...string) *ast.Field {
		return &ast.Field{
			Name:       name,
			Type:       &ast.TypeRef{Optional: optional},
			Decorators: decs(dec...),
		}
	}
	cases := []struct {
		name  string
		field *ast.Field
		want  JSONPresence
	}{
		{"plain field", field("a", false), JSONRequired},
		{"optional", field("a", true), JSONOptional},
		{"nullable", field("a", false, "nullable"), JSONNullable},
		{"optional beats nullable", field("a", true, "nullable"), JSONOptional},
		{"sensitive leaves the body", field("a", false, "sensitive"), JSONAbsent},
		{"header leaves the body", field("a", false, BindingHeader), JSONAbsent},
		{"form stays in the body", field("a", false, BindingForm), JSONRequired},
		{"nil field", nil, JSONAbsent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, got := JSONShape(c.field); got != c.want {
				t.Errorf("presence = %v, want %v", got, c.want)
			}
		})
	}
}
