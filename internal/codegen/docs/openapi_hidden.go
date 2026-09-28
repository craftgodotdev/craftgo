package docs

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// operationUses is what [dropHiddenOnly] weighs: the operations `@hidden`
// leaves out, built aside with the `<stem>ReqBody` and `<stem>RespBody`
// components they ref, and the request and response types of the hidden
// methods and of the documented ones, which a body copies rather than refs.
type operationUses struct {
	hidden      []*openapi3.Operation
	bodies      openapi3.Schemas
	hiddenTypes []string
	shownTypes  []string
}

// methodTypes returns the names of m's request and response types.
func methodTypes(m *ast.Method) []string {
	var out []string
	if m.Request != nil {
		out = append(out, m.Request.Name.String())
	}
	if m.Response != nil && m.Response.Type != nil {
		out = append(out, m.Response.Type.Name.String())
	}
	return out
}

// dropHiddenOnly deletes from doc each component schema and security scheme
// the hidden operations use that no operation of doc uses, nor a component
// out of the hidden operations' reach: a component no operation uses stays.
func dropHiddenOnly(doc *openapi3.T, uses operationUses) error {
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
	roots := slices.Clone(uses.hiddenTypes)
	for _, op := range uses.hidden {
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
	roots = append(shown, uses.shownTypes...)
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
	for _, op := range uses.hidden {
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
