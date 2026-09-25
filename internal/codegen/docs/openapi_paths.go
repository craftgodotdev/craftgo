package docs

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

func addPaths(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) {
	counts := methodNameCounts(pkg)
	for _, svcName := range pkg.ServiceNames() {
		svc := pkg.Services[svcName]
		for _, m := range svc.Methods {
			full := route.Resolve("", svc.Primary, m)
			base := operationBaseName(svcName, m, counts)
			addRequestBodySchema(doc, m, pkg, registry, base, names)
			addPerOperationResponseSchema(doc, m, pkg, registry, base, names)
			item := doc.Paths.Value(full)
			if item == nil {
				item = &openapi3.PathItem{}
				doc.Paths.Set(full, item)
			}
			op := buildOperation(svcName, m, pkg, registry, full, base)
			setOperation(item, m.Verb, op)
		}
	}
}

func methodNameCounts(pkg *semantic.Package) map[string]int {
	return semantic.MethodNameCounts(pkg)
}

func operationBaseName(svcName string, m *ast.Method, counts map[string]int) string {
	return semantic.OperationBaseName(svcName, m, counts)
}

// checkOperationIDUniqueness fails when two methods share an operationId,
// which only an explicit `@operationId` can cause.
func checkOperationIDUniqueness(pkg *semantic.Package) error {
	counts := methodNameCounts(pkg)
	owners := map[string][]string{} // operationId -> ["Service.Method", ...]
	for _, svcName := range pkg.ServiceNames() {
		svc := pkg.Services[svcName]
		for _, m := range svc.Methods {
			id := operationID(svc.Decorators(m), operationBaseName(svcName, m, counts))
			owners[id] = append(owners[id], svcName+"."+m.Name)
		}
	}
	var dups []string
	for _, id := range slices.Sorted(maps.Keys(owners)) {
		if who := owners[id]; len(who) >= 2 {
			dups = append(dups, fmt.Sprintf("%q (from %s)", id, strings.Join(who, ", ")))
		}
	}
	if len(dups) > 0 {
		return fmt.Errorf("duplicate operationId %s - give each method a distinct @operationId(...)", strings.Join(dups, "; "))
	}
	return nil
}

// fieldBins holds a request or response type's fields by binding.
type fieldBins struct {
	body, query, header, cookie, path []*ast.Field
}

// binRequestFields bins m's request fields by their resolved binding,
// dropping @sensitive ones; @form fields join the body.
func binRequestFields(m *ast.Method, pkg *semantic.Package, r *semantic.Resolver) fieldBins {
	var bins fieldBins
	for _, rf := range semantic.RequestFields(m, pkg, r, nil) {
		switch rf.Binding {
		case wire.BindSensitive:
			continue
		case wire.BindPath:
			bins.path = append(bins.path, rf.Field)
		case wire.BindQuery:
			bins.query = append(bins.query, rf.Field)
		case wire.BindHeader:
			bins.header = append(bins.header, rf.Field)
		case wire.BindCookie:
			bins.cookie = append(bins.cookie, rf.Field)
		default: // BindBody, BindForm
			bins.body = append(bins.body, rf.Field)
		}
	}
	return bins
}

// addRequestBodySchema emits the `<base>ReqBody` schema of m's body fields.
func addRequestBodySchema(doc *openapi3.T, m *ast.Method, pkg *semantic.Package, registry *genericRegistry, base string, names *schemaNames) {
	if m.Request == nil {
		return
	}
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return
	}
	// A multipart body is inlined on the operation.
	if isMultipartRequest(m, pkg, registry.resolver) {
		return
	}
	bins := binRequestFields(m, pkg, registry.resolver)
	wireBound := len(bins.path)+len(bins.query)+len(bins.header)+len(bins.cookie) > 0
	if !wireBound {
		// The body is the whole request type: its mixins, type arguments
		// and cross-field fragments included.
		if len(m.Request.Args) > 0 && len(td.TypeParams) > 0 {
			// A generic declaration has no schema of its own.
			inst := registry.register(td, m.Request.Args)
			names.put(doc, base+"ReqBody", &openapi3.SchemaRef{Ref: "#/components/schemas/" + inst})
			return
		}
		if requestHasBodyContent(m, pkg, registry.resolver) {
			names.put(doc, base+"ReqBody", &openapi3.SchemaRef{Value: schemaFromTypeDecl(td, nil, pkg, registry)})
		}
		return
	}
	// A mixed request's body schema holds its body fields, the ones mixins
	// bring included, and the cross-field fragments.
	if len(bins.body) > 0 {
		s := schemaFromFields(bins.body, pkg, registry)
		if frags := typeFragments(td, registry); len(frags) > 0 {
			s = &openapi3.Schema{
				AllOf: append(openapi3.SchemaRefs{{Value: s}}, frags...),
			}
		}
		names.put(doc, base+"ReqBody", &openapi3.SchemaRef{Value: s})
	}
}

// requestHasBodyContent reports whether any resolved field of m's request,
// mixins included, rides the JSON body.
func requestHasBodyContent(m *ast.Method, pkg *semantic.Package, r *semantic.Resolver) bool {
	for _, rf := range semantic.RequestFields(m, pkg, r, nil) {
		if rf.OnWireBody {
			return true
		}
	}
	return false
}

// addPerOperationResponseSchema emits `<base>RespBody`, a $ref to the response
// type, or its body fields inline when it has @header or @cookie fields.
func addPerOperationResponseSchema(doc *openapi3.T, m *ast.Method, pkg *semantic.Package, registry *genericRegistry, base string, names *schemaNames) {
	if m.Response == nil || m.Response.Type == nil {
		return
	}
	bins := binResponseFields(m, pkg, registry.resolver)
	if len(bins.header) == 0 && len(bins.cookie) == 0 {
		// A generic response refs its instance: the declaration has no schema.
		respName := m.Response.Type.Name.String()
		if len(m.Response.Type.Args) > 0 {
			if decl, ok := pkg.Types[respName]; ok && len(decl.TypeParams) > 0 {
				respName = registry.register(decl, m.Response.Type.Args)
			}
		}
		names.put(doc, base+"RespBody", &openapi3.SchemaRef{
			Ref: "#/components/schemas/" + respName,
		})
		return
	}
	respBody := bins.body
	if len(m.Response.Type.Args) > 0 {
		if decl, ok := pkg.Types[m.Response.Type.Name.String()]; ok {
			respBody = substituteGenericFields(bins.body, decl, m.Response.Type.Args)
		}
	}
	names.put(doc, base+"RespBody", &openapi3.SchemaRef{
		Value: schemaFromFields(respBody, pkg, registry),
	})
}

// substituteGenericFields returns fields with args substituted for td's type
// parameters (`data T` → `data Item`).
func substituteGenericFields(fields []*ast.Field, td *ast.TypeDecl, args []*ast.TypeRef) []*ast.Field {
	if td == nil || len(td.TypeParams) == 0 || len(args) == 0 {
		return fields
	}
	subst := semantic.SubstMap(td.TypeParams, args)
	out := make([]*ast.Field, len(fields))
	for i, f := range fields {
		fc := *f
		fc.Type = semantic.SubstituteTypeRef(f.Type, subst)
		out[i] = &fc
	}
	return out
}

// binResponseFields bins the response type's fields into header, cookie and
// body, dropping @sensitive ones.
func binResponseFields(m *ast.Method, pkg *semantic.Package, r *semantic.Resolver) fieldBins {
	var bins fieldBins
	if m.Response == nil || m.Response.Type == nil {
		return bins
	}
	td, ok := pkg.Types[m.Response.Type.Name.String()]
	if !ok {
		return bins
	}
	for _, rf := range semantic.ResolveFields(td, "", pkg, r, nil) {
		switch rf.Binding {
		case wire.BindSensitive:
			continue
		case wire.BindHeader:
			bins.header = append(bins.header, rf.Field)
		case wire.BindCookie:
			bins.cookie = append(bins.cookie, rf.Field)
		default:
			bins.body = append(bins.body, rf.Field)
		}
	}
	return bins
}

// buildResponseHeaders documents the @header fields as headers and the @cookie
// ones in one `Set-Cookie` header: OpenAPI has no response cookies.
func buildResponseHeaders(headers, cookies []*ast.Field, pkg *semantic.Package, registry *genericRegistry) openapi3.Headers {
	if len(headers) == 0 && len(cookies) == 0 {
		return nil
	}
	out := openapi3.Headers{}
	for _, f := range headers {
		name := wire.WireName(f, wire.BindHeader)
		schema := schemaForTypeRef(f.Type, pkg, registry)
		applyFieldMetadata(f, schema, pkg)
		hdr := &openapi3.Header{
			Parameter: openapi3.Parameter{
				Schema:      schema,
				Description: semantic.Description(f.Decorators, f.Doc),
				Deprecated:  semantic.IsDeprecated(f.Decorators),
			},
		}
		out[name] = &openapi3.HeaderRef{Value: hdr}
	}
	if len(cookies) > 0 {
		names := make([]string, 0, len(cookies))
		for _, f := range cookies {
			names = append(names, wire.WireName(f, wire.BindCookie))
		}
		desc := "Sets cookies: " + strings.Join(names, ", ")
		out["Set-Cookie"] = &openapi3.HeaderRef{Value: &openapi3.Header{
			Parameter: openapi3.Parameter{
				Schema:      &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
				Description: desc,
			},
		}}
	}
	return out
}

// schemaFromFields builds an inline object schema of the fields that ride
// the body.
func schemaFromFields(fields []*ast.Field, pkg *semantic.Package, registry *genericRegistry) *openapi3.Schema {
	s := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: openapi3.Schemas{},
	}
	for _, f := range fields {
		addBodyProperty(s, semantic.ResolveField(f, pkg, registry.resolver.Project()), f.Type, pkg, registry)
	}
	return s
}
