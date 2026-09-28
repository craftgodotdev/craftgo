package docs

import (
	"encoding/json"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// operationUses is what [dropHiddenOnly] weighs: the methods `@hidden` leaves
// out and the documented ones, and the operations of the hidden ones, built
// aside with the `<stem>ReqBody` and `<stem>RespBody` components they ref.
type operationUses struct {
	hidden, shown []operation
	built         []*openapi3.Operation
	bodies        openapi3.Schemas
}

// designRefs returns the component of each declaration the methods of ops
// take, answer or raise reach in pkg, through fields, mixins, map keys and
// values and generic arguments: the document writes some in place, as a map
// key's enum or a cookie's, where no $ref names them.
func designRefs(ops []operation, pkg *semantic.Package) []string {
	var out []string
	seen := map[string]bool{}
	var visit func(t *ast.TypeRef)
	body := func(members []ast.TypeMember) {
		for _, member := range members {
			switch v := member.(type) {
			case *ast.Field:
				visit(v.Type)
			case *ast.Mixin:
				visit(&ast.TypeRef{Named: v.Ref})
			}
		}
	}
	visit = func(t *ast.TypeRef) {
		t.WalkNamedRefs(func(n *ast.NamedTypeRef) {
			if n.Name == nil || seen[n.Name.String()] {
				return
			}
			name := n.Name.String()
			seen[name] = true
			if td := pkg.Types[name]; td != nil {
				if len(td.TypeParams) == 0 {
					out = append(out, name)
				}
				body(td.Body)
			} else if pkg.Enums[name] != nil || pkg.Scalars[name] != nil {
				out = append(out, name)
			}
		})
	}
	raised := map[string]bool{}
	for _, o := range ops {
		visit(&ast.TypeRef{Named: o.m.Request})
		if o.m.Response != nil {
			visit(&ast.TypeRef{Named: o.m.Response.Type})
		}
		for _, name := range errorRefsFromDecorators(o.svc.Decorators(o.m)) {
			if ed := pkg.Errors[name]; ed != nil && !raised[name] {
				raised[name] = true
				out = append(out, idents.ErrorTypeName(ed.Name))
				body(ed.Body)
			}
		}
	}
	return out
}

// dropHiddenOnly deletes from doc each component schema and security scheme
// the hidden methods use that no documented method uses, nor a component out
// of the hidden methods' reach: a component no method uses stays.
func dropHiddenOnly(doc *openapi3.T, pkg *semantic.Package, uses operationUses) error {
	if len(uses.hidden) == 0 {
		return nil
	}
	schemas := doc.Components.Schemas
	refs := make(map[string][]string, len(schemas))
	for name, s := range schemas {
		r, err := schemaRefs(s)
		if err != nil {
			return err
		}
		refs[name] = r
	}
	reach := func(roots []string) map[string]bool {
		seen := map[string]bool{}
		for len(roots) > 0 {
			name := roots[len(roots)-1]
			roots = roots[:len(roots)-1]
			if _, ok := schemas[name]; ok && !seen[name] {
				seen[name] = true
				roots = append(roots, refs[name]...)
			}
		}
		return seen
	}
	roots := designRefs(uses.hidden, pkg)
	for _, op := range uses.built {
		opRefs, err := schemaRefs(op)
		if err != nil {
			return err
		}
		for _, name := range opRefs {
			body, own := uses.bodies[name]
			if !own {
				roots = append(roots, name)
				continue
			}
			bodyRefs, err := schemaRefs(body)
			if err != nil {
				return err
			}
			roots = append(roots, bodyRefs...)
		}
	}
	hiddenReach := reach(roots)
	shown, err := schemaRefs(doc.Paths)
	if err != nil {
		return err
	}
	roots = append(shown, designRefs(uses.shown, pkg)...)
	for name := range schemas {
		if !hiddenReach[name] {
			roots = append(roots, name)
		}
	}
	kept := reach(roots)
	for name := range schemas {
		if !kept[name] {
			delete(schemas, name)
		}
	}
	visible := map[string]bool{}
	for _, item := range doc.Paths.Map() {
		for _, op := range item.Operations() {
			for _, name := range securitySchemeNames(op) {
				visible[name] = true
			}
		}
	}
	for _, op := range uses.built {
		for _, name := range securitySchemeNames(op) {
			if !visible[name] {
				delete(doc.Components.SecuritySchemes, name)
			}
		}
	}
	return nil
}

// securitySchemeNames returns the scheme of each requirement of op.
func securitySchemeNames(op *openapi3.Operation) []string {
	if op.Security == nil {
		return nil
	}
	var out []string
	for _, req := range *op.Security {
		for name := range req {
			out = append(out, name)
		}
	}
	return out
}

// schemaRefs returns the component schema each $ref in v, a part of a
// document, names.
func schemaRefs(v any) ([]string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, err
	}
	var out []string
	var walk func(n any)
	walk = func(n any) {
		switch n := n.(type) {
		case map[string]any:
			for key, child := range n {
				if ref, ok := child.(string); ok && key == "$ref" {
					if name, ok := strings.CutPrefix(ref, "#/components/schemas/"); ok {
						out = append(out, name)
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(tree)
	return out, nil
}
