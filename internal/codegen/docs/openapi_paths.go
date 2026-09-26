package docs

import (
	"cmp"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// addPaths adds pkg's operations to doc (which holds pkg's components), each with its basePath-bound
// fields; shared describes each left out because its path already holds an operation of its method.
func addPaths(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) (ops []boundOperation, shared []string) {
	held := map[string]string{}
	for _, o := range operations(pkg, doc.Components.Schemas) {
		s := newOpShape(o.svc, o.m, route.Resolve("", o.svc.Primary, o.m), o.id, o.stem, pkg, registry.resolver)
		path := route.OpenAPIPath(s.full)
		verb := strings.ToUpper(o.m.Verb)
		this := fmt.Sprintf("%s.%s (%s %s)", o.svc.Primary.Name, o.m.Name, verb, s.full)
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
		op := buildOperation(doc, o.svc, s, pkg, registry, names)
		setOperation(item, o.m.Verb, op)
		ops = append(ops, boundOperation{op: op, fields: s.server})
	}
	return ops, shared
}

// boundOperation is an operation and its request fields bound to a variable
// of the basePath.
type boundOperation struct {
	op     *openapi3.Operation
	fields []semantic.ResolvedField
}

// basePathServer returns the server at basePath, each variable as every
// operation describes it, bare for one binding it to no field; an operation
// describing it otherwise gets a server of its own.
func basePathServer(basePath string, ops []boundOperation, pkg *semantic.Package) *openapi3.Server {
	own := make([]*openapi3.Server, len(ops))
	for i, o := range ops {
		own[i] = describedServer(basePath, o.fields, pkg)
	}
	root := describedServer(basePath, nil, pkg)
	for name := range root.Variables {
		if len(own) > 0 && !slices.ContainsFunc(own[1:], func(s *openapi3.Server) bool {
			return !reflect.DeepEqual(s.Variables[name], own[0].Variables[name])
		}) {
			root.Variables[name] = own[0].Variables[name]
		}
	}
	for i, o := range ops {
		if !reflect.DeepEqual(own[i], root) {
			o.op.Servers = &openapi3.Servers{own[i]}
		}
	}
	return root
}

// describedServer returns the server at basePath, each `{name}` segment a
// variable the one of fields bound to it describes.
func describedServer(basePath string, fields []semantic.ResolvedField, pkg *semantic.Package) *openapi3.Server {
	server := &openapi3.Server{URL: basePath, Variables: map[string]*openapi3.ServerVariable{}}
	for _, name := range route.Vars(basePath) {
		v := &openapi3.ServerVariable{Default: name}
		for _, rf := range fields {
			if wire.WireName(rf.Field, wire.BindPath) == name {
				v = serverVariable(name, rf, pkg)
				break
			}
		}
		server.Variables[name] = v
	}
	return server
}

// serverVariable describes basePath variable name from rf, the request field
// bound to it: its doc, its enum's values and a default its type takes.
func serverVariable(name string, rf semantic.ResolvedField, pkg *semantic.Package) *openapi3.ServerVariable {
	v := &openapi3.ServerVariable{Default: name, Description: semantic.Description(rf.Field.Decorators, rf.Field.Doc)}
	sp, _ := prims.Lookup(rf.ResolvedPrim)
	switch {
	case rf.Category == semantic.CatEnum:
		if ed := pkg.Enums[rf.Field.Type.Named.Name.String()]; ed != nil && len(ed.EnumValues()) > 0 {
			v.Enum = enumWireStrings(ed)
			v.Default = v.Enum[0]
		}
	case sp.Kind == prims.Int || sp.Kind == prims.Uint || sp.Kind == prims.Float:
		v.Default = "0"
	case sp.Kind == prims.Bool:
		v.Default = "false"
	}
	if ex, ok := semantic.ExampleValue(rf.Field, pkg); ok {
		v.Default = fmt.Sprint(ex)
	}
	return v
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
// first, keeps it; each other takes it with the lowest number that no
// operation holds and that names no body component in declared (`ABC2`).
func operations(pkg *semantic.Package, declared openapi3.Schemas) []operation {
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
	taken := func(name string) bool {
		_, req := declared[name+"ReqBody"]
		_, resp := declared[name+"RespBody"]
		return len(byStem[name]) > 0 || req || resp
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
			for taken(stem + strconv.Itoa(n)) {
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
	server         []semantic.ResolvedField // request fields bound to a basePath variable
	form, files    []semantic.FormField     // files is non-empty for a multipart request
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
		s.req.path, s.server = splitServerBound(s.req.path, full)
		s.form, s.files = semantic.FormParts(fields)
	}
	if m.Response != nil && m.Response.Type != nil {
		if s.respType = pkg.Types[m.Response.Type.Name.String()]; s.respType != nil {
			s.resp = binFields(semantic.ResponseFields(m, pkg, r, nil))
		}
	}
	return s
}

// splitServerBound splits path-bound fields into those of route full's
// variables and those of the basePath's, which full leaves to the server.
func splitServerBound(path []semantic.ResolvedField, full string) (routed, server []semantic.ResolvedField) {
	vars := route.Vars(full)
	for _, rf := range path {
		if slices.Contains(vars, wire.WireName(rf.Field, wire.BindPath)) {
			routed = append(routed, rf)
		} else {
			server = append(server, rf)
		}
	}
	return routed, server
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
	if len(s.req.path)+len(s.server)+len(s.req.query)+len(s.req.header)+len(s.req.cookie) == 0 {
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
	if frags := inlineFragments(td, jsonKeys(td, registry), presentNonNull, registry); len(frags) > 0 {
		body = &openapi3.Schema{AllOf: append(openapi3.SchemaRefs{{Value: body}}, frags...)}
	}
	return &openapi3.SchemaRef{Value: body}
}

// buildResponseHeaders documents the @header fields as headers and the @cookie
// ones in one `Set-Cookie` header: OpenAPI has no response cookies. Fields
// sharing a header name in any letter case, which responses sharing a status
// may send, make one header, spelled as the first, whose schema admits each
// field's type; its description is the first one given, and it is deprecated
// when every field is.
func buildResponseHeaders(headers, cookies []semantic.ResolvedField, pkg *semantic.Package, registry *genericRegistry) openapi3.Headers {
	if len(headers) == 0 && len(cookies) == 0 {
		return nil
	}
	out := openapi3.Headers{}
	alternatives := map[string]openapi3.SchemaRefs{}
	spelled := map[string]string{}
	for _, rf := range headers {
		f := rf.Field
		name := wire.WireName(f, wire.BindHeader)
		key := http.CanonicalHeaderKey(name)
		if first, seen := spelled[key]; seen {
			name = first
		} else {
			spelled[key] = name
		}
		schema := schemaForTypeRef(nonNullType(f), pkg, registry)
		applyFieldMetadata(f, schema, pkg, false)
		if !slices.ContainsFunc(alternatives[name], func(s *openapi3.SchemaRef) bool { return reflect.DeepEqual(s, schema) }) {
			alternatives[name] = append(alternatives[name], schema)
		}
		desc, deprecated := semantic.Description(f.Decorators, f.Doc), semantic.IsDeprecated(f.Decorators)
		if h, seen := out[name]; seen {
			h.Value.Description = cmp.Or(h.Value.Description, desc)
			h.Value.Deprecated = h.Value.Deprecated && deprecated
			continue
		}
		out[name] = &openapi3.HeaderRef{Value: &openapi3.Header{
			Parameter: openapi3.Parameter{Description: desc, Deprecated: deprecated},
		}}
	}
	for name, schemas := range alternatives {
		out[name].Value.Schema = schemas[0]
		if len(schemas) > 1 {
			out[name].Value.Schema = &openapi3.SchemaRef{Value: &openapi3.Schema{AnyOf: schemas}}
		}
	}
	if len(cookies) > 0 {
		var names []string
		for _, rf := range cookies {
			if name := wire.WireName(rf.Field, wire.BindCookie); !slices.Contains(names, name) {
				names = append(names, name)
			}
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
