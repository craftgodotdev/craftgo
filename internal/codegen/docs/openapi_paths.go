package docs

import (
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// addPaths adds an operation per method of pkg, each under its route.
func addPaths(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) {
	counts := semantic.MethodNameCounts(pkg)
	// Services of one name in two packages may share a method name: those
	// operations' body components take their package first (`ASGetReqBody`).
	owners := map[string]int{}
	for _, svc := range pkg.Services {
		for _, m := range svc.Methods {
			owners[svc.Primary.Name+"."+m.Name]++
		}
	}
	for _, key := range pkg.ServiceNames() {
		svc := pkg.Services[key]
		for _, m := range svc.Methods {
			base := semantic.OperationBaseName(svc.Primary.Name, m, counts)
			stem := base
			if owners[svc.Primary.Name+"."+m.Name] >= 2 {
				stem = idents.PascalCase(servicePackage(key)) + base
			}
			s := newOpShape(svc, m, route.Resolve("", svc.Primary, m), semantic.OperationID(svc.Decorators(m), base), stem, pkg, registry.resolver)
			item := doc.Paths.Value(s.full)
			if item == nil {
				item = &openapi3.PathItem{}
				doc.Paths.Set(s.full, item)
			}
			setOperation(item, m.Verb, buildOperation(doc, svc, s, pkg, registry, names))
		}
	}
}

// opShape is a method as its operation documents it: its route, operationId
// and body component stem, and where each request and response field rides.
type opShape struct {
	m              *ast.Method
	decs           []*ast.Decorator // m's decorators, its extend block's first
	full, id, stem string
	req, resp      fieldBins
	form, files    []semantic.FormField // files is non-empty for a multipart request
	reqType        *ast.TypeDecl
	respType       *ast.TypeDecl // nil for a scalar or enum response
}

// newOpShape resolves m's request and response fields once, for its route
// full, operationId id and component stem.
func newOpShape(svc *semantic.ServiceInfo, m *ast.Method, full, id, stem string, pkg *semantic.Package, r *semantic.Resolver) opShape {
	s := opShape{m: m, decs: svc.Decorators(m), full: full, id: id, stem: stem}
	if m.Request != nil {
		s.reqType = pkg.Types[m.Request.Name.String()]
		fields := semantic.RequestFields(m, pkg, r, nil)
		s.req = binFields(fields)
		s.form, s.files = semantic.FormParts(fields)
	}
	if m.Response != nil && m.Response.Type != nil {
		if s.respType = pkg.Types[m.Response.Type.Name.String()]; s.respType != nil {
			s.resp = binFields(instanceFields(m.Response.Type, pkg, r))
		}
	}
	return s
}

// instanceFields resolves the fields of the type ref names as a body
// embedding ref gets them: mixins included, each level's generic arguments
// bound on that level alone (`Page<Item>`'s `items T[]` as `items Item[]`).
func instanceFields(ref *ast.NamedTypeRef, pkg *semantic.Package, r *semantic.Resolver) []semantic.ResolvedField {
	return semantic.ResolveFields(&ast.TypeDecl{Body: []ast.TypeMember{&ast.Mixin{Ref: ref}}}, "", pkg, r, nil)
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

// requestBodySchema is the `<stem>ReqBody` component of s's JSON body: the
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
	body := schemaFromFields(s.req.body, pkg, registry)
	if frags := typeFragments(td, registry); len(frags) > 0 {
		body = &openapi3.Schema{AllOf: append(openapi3.SchemaRefs{{Value: body}}, frags...)}
	}
	return &openapi3.SchemaRef{Value: body}
}

// responseBodySchema is the `<stem>RespBody` component: a $ref to the
// response type, or its body fields inline when it sends headers or cookies.
func responseBodySchema(s opShape, pkg *semantic.Package, registry *genericRegistry) *openapi3.SchemaRef {
	if len(s.resp.header) == 0 && len(s.resp.cookie) == 0 {
		// A generic response refs its instance: the declaration has no schema.
		return &openapi3.SchemaRef{Ref: "#/components/schemas/" + registry.refName(s.m.Response.Type)}
	}
	return &openapi3.SchemaRef{Value: schemaFromFields(s.resp.body, pkg, registry)}
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
