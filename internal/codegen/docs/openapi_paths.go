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
	for _, key := range pkg.ServiceNames() {
		svc := pkg.Services[key]
		for _, m := range svc.Methods {
			s := newOpShape(svc, m, route.Resolve("", svc.Primary, m), operationBaseName(svc.Primary.Name, m, counts), pkg, registry.resolver)
			item := doc.Paths.Value(s.full)
			if item == nil {
				item = &openapi3.PathItem{}
				doc.Paths.Set(s.full, item)
			}
			setOperation(item, m.Verb, buildOperation(doc, svc, s, pkg, registry, names))
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
	for _, key := range pkg.ServiceNames() {
		svc := pkg.Services[key]
		for _, m := range svc.Methods {
			id := operationID(svc.Decorators(m), operationBaseName(svc.Primary.Name, m, counts))
			owners[id] = append(owners[id], svc.Primary.Name+"."+m.Name)
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

// opShape is a method as its operation documents it: its route, the stem of
// its body components, and where each request and response field rides.
type opShape struct {
	m           *ast.Method
	decs        []*ast.Decorator // m's decorators, its extend block's first
	full, base  string
	req, resp   fieldBins
	form, files []semantic.FormField // files is non-empty for a multipart request
	reqType     *ast.TypeDecl
	respType    *ast.TypeDecl // nil for a scalar or enum response
}

// newOpShape resolves m's request and response fields once, for its route
// full and component stem base.
func newOpShape(svc *semantic.ServiceInfo, m *ast.Method, full, base string, pkg *semantic.Package, r *semantic.Resolver) opShape {
	s := opShape{m: m, decs: svc.Decorators(m), full: full, base: base}
	if m.Request != nil {
		s.reqType = pkg.Types[m.Request.Name.String()]
		fields := semantic.RequestFields(m, pkg, r, nil)
		s.req = binFields(fields)
		s.form, s.files = semantic.FormParts(fields)
	}
	if m.Response != nil && m.Response.Type != nil {
		if s.respType = pkg.Types[m.Response.Type.Name.String()]; s.respType != nil {
			s.resp = binFields(semantic.ResolveFields(s.respType, "", pkg, r, nil))
		}
	}
	return s
}

// fieldBins holds resolved fields by where they ride, @sensitive ones left
// out; a @form field rides the body.
type fieldBins struct {
	body, query, header, cookie, path []semantic.ResolvedField
}

// binFields sorts fields into bins by their binding.
func binFields(fields []semantic.ResolvedField) fieldBins {
	var bins fieldBins
	for _, rf := range fields {
		switch rf.Binding {
		case wire.BindSensitive:
		case wire.BindPath:
			bins.path = append(bins.path, rf)
		case wire.BindQuery:
			bins.query = append(bins.query, rf)
		case wire.BindHeader:
			bins.header = append(bins.header, rf)
		case wire.BindCookie:
			bins.cookie = append(bins.cookie, rf)
		default: // BindBody, BindForm
			bins.body = append(bins.body, rf)
		}
	}
	return bins
}

// requestBodySchema is the `<base>ReqBody` component of s's JSON body: the
// whole request type when nothing rides off the body, else the body fields
// and the type's cross-field fragments.
func requestBodySchema(s opShape, pkg *semantic.Package, registry *genericRegistry) *openapi3.SchemaRef {
	td := s.reqType
	if len(s.req.path)+len(s.req.query)+len(s.req.header)+len(s.req.cookie) == 0 {
		if len(td.TypeParams) > 0 {
			// A generic declaration has no schema of its own.
			return &openapi3.SchemaRef{Ref: "#/components/schemas/" + registry.refName(s.m.Request)}
		}
		return &openapi3.SchemaRef{Value: schemaFromTypeDecl(td, nil, pkg, registry)}
	}
	body := schemaFromFields(substituteGenericFields(s.req.body, td, s.m.Request.Args), pkg, registry)
	if frags := typeFragments(td, registry); len(frags) > 0 {
		body = &openapi3.Schema{AllOf: append(openapi3.SchemaRefs{{Value: body}}, frags...)}
	}
	return &openapi3.SchemaRef{Value: body}
}

// responseBodySchema is the `<base>RespBody` component: a $ref to the
// response type, or its body fields inline when it sends headers or cookies.
func responseBodySchema(s opShape, pkg *semantic.Package, registry *genericRegistry) *openapi3.SchemaRef {
	if len(s.resp.header) == 0 && len(s.resp.cookie) == 0 {
		// A generic response refs its instance: the declaration has no schema.
		return &openapi3.SchemaRef{Ref: "#/components/schemas/" + registry.refName(s.m.Response.Type)}
	}
	return &openapi3.SchemaRef{Value: schemaFromFields(substituteGenericFields(s.resp.body, s.respType, s.m.Response.Type.Args), pkg, registry)}
}

// substituteGenericFields returns fields with args substituted for td's type
// parameters (`data T` → `data Item`).
func substituteGenericFields(fields []semantic.ResolvedField, td *ast.TypeDecl, args []*ast.TypeRef) []semantic.ResolvedField {
	if td == nil || len(td.TypeParams) == 0 || len(args) == 0 {
		return fields
	}
	subst := semantic.SubstMap(td.TypeParams, args)
	out := make([]semantic.ResolvedField, len(fields))
	for i, rf := range fields {
		fc := *rf.Field
		fc.Type = semantic.SubstituteTypeRef(rf.Field.Type, subst)
		rf.Field = &fc
		out[i] = rf
	}
	return out
}

// buildResponseHeaders documents the @header fields as headers and the @cookie
// ones in one `Set-Cookie` header: OpenAPI has no response cookies.
func buildResponseHeaders(headers, cookies []semantic.ResolvedField, pkg *semantic.Package, registry *genericRegistry) openapi3.Headers {
	if len(headers) == 0 && len(cookies) == 0 {
		return nil
	}
	out := openapi3.Headers{}
	for _, rf := range headers {
		f := rf.Field
		schema := schemaForTypeRef(f.Type, pkg, registry)
		applyFieldMetadata(f, schema, pkg)
		out[wire.WireName(f, wire.BindHeader)] = &openapi3.HeaderRef{Value: &openapi3.Header{
			Parameter: openapi3.Parameter{
				Schema:      schema,
				Description: semantic.Description(f.Decorators, f.Doc),
				Deprecated:  semantic.IsDeprecated(f.Decorators),
			},
		}}
	}
	if len(cookies) > 0 {
		names := make([]string, 0, len(cookies))
		for _, rf := range cookies {
			names = append(names, wire.WireName(rf.Field, wire.BindCookie))
		}
		out["Set-Cookie"] = &openapi3.HeaderRef{Value: &openapi3.Header{
			Parameter: openapi3.Parameter{
				Schema:      &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
				Description: "Sets cookies: " + strings.Join(names, ", "),
			},
		}}
	}
	return out
}

// schemaFromFields builds an inline object schema of the fields that ride
// the body.
func schemaFromFields(fields []semantic.ResolvedField, pkg *semantic.Package, registry *genericRegistry) *openapi3.Schema {
	s := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: openapi3.Schemas{},
	}
	for _, rf := range fields {
		addBodyProperty(s, rf, rf.Field.Type, pkg, registry)
	}
	return s
}
