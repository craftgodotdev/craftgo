package docs

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// addPaths adds an operation per method of pkg, each under its route, and
// describes each one it leaves out because its path holds an operation of
// its method already.
func addPaths(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) (shared []string) {
	held := map[string]string{}
	for _, op := range operations(pkg) {
		s := newOpShape(op.svc, op.m, route.Resolve("", op.svc.Primary, op.m), op.id, op.stem, pkg, registry.resolver)
		path := route.OpenAPIPath(s.full)
		verb := strings.ToUpper(op.m.Verb)
		this := fmt.Sprintf("%s.%s (%s %s)", op.svc.Primary.Name, op.m.Name, verb, s.full)
		if first, taken := held[verb+" "+path]; taken {
			shared = append(shared, fmt.Sprintf("OpenAPI path %s %s holds two operations: %s and %s", verb, path, first, this))
			continue
		}
		held[verb+" "+path] = this
		item := doc.Paths.Value(path)
		if item == nil {
			item = &openapi3.PathItem{}
			doc.Paths.Set(path, item)
		}
		setOperation(item, op.m.Verb, buildOperation(doc, op.svc, s, pkg, registry, names))
	}
	return shared
}

// operation is a method of a service of the merged package, with its
// operationId and the stem of its body component names.
type operation struct {
	svc      *semantic.ServiceInfo
	m        *ast.Method
	id, stem string
}

// operations returns pkg's methods in document order. A stem is the method's
// base name, its package first (`ASGet`) when a service of its name in
// another package has a method of its name. Of operations sharing a stem
// (`A.BC` and `AB.C` are both ABC), the one whose operationId it is, else the
// first, keeps it; each other takes it with the lowest number no operation
// holds (`ABC2`).
func operations(pkg *semantic.Package) []operation {
	counts := semantic.MethodNameCounts(pkg)
	owners := map[string]int{}
	for _, svc := range pkg.Services {
		for _, m := range svc.Methods {
			owners[svc.Primary.Name+"."+m.Name]++
		}
	}
	var ops []operation
	byStem := map[string][]int{}
	for _, key := range pkg.ServiceNames() {
		svc := pkg.Services[key]
		for _, m := range svc.Methods {
			base := semantic.OperationBaseName(svc.Primary.Name, m, counts)
			stem := base
			if owners[svc.Primary.Name+"."+m.Name] >= 2 {
				stem = idents.PascalCase(servicePackage(key)) + base
			}
			byStem[stem] = append(byStem[stem], len(ops))
			ops = append(ops, operation{svc: svc, m: m, id: semantic.OperationID(svc.Decorators(m), base), stem: stem})
		}
	}
	for _, stem := range slices.Sorted(maps.Keys(byStem)) {
		shared := byStem[stem]
		if len(shared) < 2 {
			continue
		}
		keep := shared[0]
		for _, i := range shared {
			if ops[i].id == stem {
				keep = i
				break
			}
		}
		n := 2
		for _, i := range shared {
			if i == keep {
				continue
			}
			for len(byStem[stem+strconv.Itoa(n)]) > 0 {
				n++
			}
			ops[i].stem = stem + strconv.Itoa(n)
			byStem[ops[i].stem] = []int{i}
		}
	}
	return ops
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
			s.resp = binFields(semantic.ResponseFields(m, pkg, r, nil))
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

// requestBodySchema is the `<stem>ReqBody` component of s's JSON body: the
// whole request type when nothing rides off the body, else [inlineBody].
func requestBodySchema(s opShape, pkg *semantic.Package, registry *genericRegistry) *openapi3.SchemaRef {
	td := s.reqType
	if len(s.req.path)+len(s.req.query)+len(s.req.header)+len(s.req.cookie) == 0 {
		if len(td.TypeParams) > 0 {
			// A generic declaration has no schema of its own.
			return &openapi3.SchemaRef{Ref: "#/components/schemas/" + registry.refName(s.m.Request)}
		}
		return &openapi3.SchemaRef{Value: schemaFromTypeDecl(td, nil, pkg, registry)}
	}
	return inlineBody(s.req.body, td, pkg, registry)
}

// responseBodySchema is the `<stem>RespBody` component: a $ref to the
// response type, or [inlineBody] when it sends headers or cookies.
func responseBodySchema(s opShape, pkg *semantic.Package, registry *genericRegistry) *openapi3.SchemaRef {
	if len(s.resp.header) == 0 && len(s.resp.cookie) == 0 {
		// A generic response refs its instance: the declaration has no schema.
		return &openapi3.SchemaRef{Ref: "#/components/schemas/" + registry.refName(s.m.Response.Type)}
	}
	return inlineBody(s.resp.body, s.respType, pkg, registry)
}

// inlineBody lists fields, the body fields of type td, in place, with the
// cross-field fragments of td and of the mixins it embeds.
func inlineBody(fields []semantic.ResolvedField, td *ast.TypeDecl, pkg *semantic.Package, registry *genericRegistry) *openapi3.SchemaRef {
	body := schemaFromFields(fields, pkg, registry)
	if frags := inlineFragments(td, jsonKeys(td, registry), registry); len(frags) > 0 {
		body = &openapi3.Schema{AllOf: append(openapi3.SchemaRefs{{Value: body}}, frags...)}
	}
	return &openapi3.SchemaRef{Value: body}
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
