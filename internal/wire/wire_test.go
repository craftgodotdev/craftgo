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
		{"propagated flag counts", []*ast.Decorator{{Name: "rawRequest", Propagated: true}}, true, false},
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
		{"nullable", field("a", false, DecoratorNullable), JSONNullable},
		{"optional beats nullable", field("a", true, DecoratorNullable), JSONOptional},
		{"sensitive leaves the body", field("a", false, DecoratorSensitive), JSONAbsent},
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
