// Path + per-operation request/response schemas + response headers.
package codegen

import (
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

func addPaths(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) {
	counts := methodNameCounts(pkg)
	for _, svcName := range sortedServices(pkg) {
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
			op := buildOperation(svcName, m, pkg, registry, base)
			setOperation(item, m.Verb, op)
		}
	}
}

// methodNameCounts tallies how many methods across ALL services in the
// (already project-merged) package share each bare method name. Two
// services with a method of the same name (`ListItems`, `Ping`, ...)
// would otherwise emit a duplicate operationId and overwrite each
// other's `<Method>ReqBody` / `<Method>RespBody` component schemas —
// last-writer-wins, leaving one operation pointing at the other's shape.
func methodNameCounts(pkg *semantic.Package) map[string]int {
	return semantic.MethodNameCounts(pkg)
}

// operationBaseName is the collision-free base for a method's component
// schema names (`<base>ReqBody`, `<base>RespBody`, ...) and its default
// operationId. A method name that is unique project-wide stays bare
// (`ListOrders`); one shared by two or more services is prefixed with
// the service name (`HeaderEchoServiceListItems`) so every emitted name
// is globally unique. An explicit `@operationId` still overrides the
// operationId itself (see [operationID]); the component names always
// follow this base so they never collide regardless of the override.
func operationBaseName(svcName string, m *ast.Method, counts map[string]int) string {
	return semantic.OperationBaseName(svcName, m, counts)
}

// checkOperationIDUniqueness reports an error when two methods would emit
// the same operationId. The auto-prefixing in [operationBaseName] removes
// every same-method-name collision on its own, so a duplicate that
// survives here can only come from an explicit `@operationId("...")` —
// either two methods pinned to the same value, or an override that
// happens to equal another method's auto-generated id. Those are the
// user's to resolve, so codegen fails with an actionable message rather
// than emitting an invalid (duplicate-operationId) spec.
func checkOperationIDUniqueness(pkg *semantic.Package) error {
	counts := methodNameCounts(pkg)
	owners := map[string][]string{} // operationId -> ["Service.Method", ...]
	for _, svcName := range sortedServices(pkg) {
		for _, m := range pkg.Services[svcName].Methods {
			id := operationID(m, operationBaseName(svcName, m, counts))
			owners[id] = append(owners[id], svcName+"."+m.Name)
		}
	}
	var dups []string
	for _, id := range sortedKeys(owners) {
		if who := owners[id]; len(who) >= 2 {
			dups = append(dups, fmt.Sprintf("%q (from %s)", id, strings.Join(who, ", ")))
		}
	}
	if len(dups) > 0 {
		return fmt.Errorf("duplicate operationId %s — give each method a distinct @operationId(...)", strings.Join(dups, "; "))
	}
	return nil
}

// fieldBins splits a request type's fields by binding kind. Empty slices
// are returned for kinds that have no contributors.
type fieldBins struct {
	body, query, header, cookie, path []*ast.Field
}

// binRequestFields walks the method's request type and partitions every
// field into the matching bin. The rules mirror runtime binding:
//   - Explicit @path / @query / @header / @cookie / @body / @form wins.
//   - A field whose name matches a `{param}` segment in the method path
//     is bound to `path`.
//   - Body verbs (POST/PUT/PATCH) keep unmarked fields in `body`.
//   - Non-body verbs (GET/DELETE/HEAD/OPTIONS) keep unmarked fields in
//     `query`.
func binRequestFields(m *ast.Method, pkg *semantic.Package) fieldBins {
	var bins fieldBins
	// Read the resolved IR: the full binding (explicit + auto-@path/@query)
	// is computed once in resolveRequestFields, so this categorisation can't
	// drift from the transport binder's view of where each field rides.
	for _, rf := range resolveRequestFields(m, pkg, nil) {
		switch rf.Binding {
		case BindSensitive:
			continue
		case BindPath:
			bins.path = append(bins.path, rf.Field)
		case BindQuery:
			bins.query = append(bins.query, rf.Field)
		case BindHeader:
			bins.header = append(bins.header, rf.Field)
		case BindCookie:
			bins.cookie = append(bins.cookie, rf.Field)
		default: // BindBody, BindForm — both ride (or document) the request body
			bins.body = append(bins.body, rf.Field)
		}
	}
	return bins
}

// addRequestBodySchema emits the `<base>ReqBody` schema referenced by the
// operation's requestBody. Only body-bound fields land here; path/query/
// header/cookie params are emitted inline by [paramsFromBins], so no
// `<base>Req{Query,Header,Cookie,Path}` components are registered (they
// would be orphaned — never $ref'd — and only bloat the spec).
func addRequestBodySchema(doc *openapi3.T, m *ast.Method, pkg *semantic.Package, registry *genericRegistry, base string, names *schemaNames) {
	if m.Request == nil {
		return
	}
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return
	}
	bins := binRequestFields(m, pkg)
	wireBound := len(bins.path)+len(bins.query)+len(bins.header)+len(bins.cookie) > 0
	if !wireBound {
		// Pure-body request: the JSON body IS the whole request type, so
		// the schema must carry everything the server decodes and
		// Validate()s — embedded mixin fields, generic type-argument
		// substitution, and type-level @requiresOneOf / @mutuallyExclusive
		// fragments. schemaFromFields renders only the loose *ast.Field
		// list and silently drops all three, so reuse the full type-decl
		// walk (the same one that builds the type's own component schema).
		if len(m.Request.Args) > 0 && len(td.TypeParams) > 0 {
			// Generic instance: $ref the registered monomorphised component
			// (PageOfEmail) — the bare generic decl is never emitted as a
			// schema and its fields are typed in the type-parameter T, so
			// an inline render would dangle a $ref to T.
			inst := registry.register(td, m.Request.Args)
			names.put(doc, base+"ReqBody", &openapi3.SchemaRef{Ref: "#/components/schemas/" + inst})
			return
		}
		if requestHasBodyContent(m, pkg) {
			names.put(doc, base+"ReqBody", &openapi3.SchemaRef{Value: schemaFromTypeDecl(td, nil, pkg, registry)})
		}
		return
	}
	// Mixed request (body + path/query/header/cookie): inline only the
	// body subset so wire-bound fields don't leak into the body schema.
	// Embedded mixin body fields ARE included (binRequestFields expands
	// mixins). Type-level @requiresOneOf / @mutuallyExclusive fragments
	// are carried too so the cross-field contract the server enforces
	// stays visible to the spec; a fragment naming a wire-bound field is a
	// design edge the body schema can't express, but cross-field over body
	// fields round-trips.
	if len(bins.body) > 0 {
		s := schemaFromFields(substituteGenericFields(bins.body, td, m.Request.Args), pkg, registry)
		if frags := crossFieldSchemaFragments(td.Decorators); len(frags) > 0 {
			s = &openapi3.Schema{
				AllOf: append(openapi3.SchemaRefs{{Value: s}}, frags...),
			}
		}
		names.put(doc, base+"ReqBody", &openapi3.SchemaRef{Value: s})
	}
}

// requestHasBodyContent reports whether m's request contributes anything
// to a JSON request body — any resolved field that rides the body
// (OnWireBody). A request whose fields are all @sensitive / @header /
// @cookie / wire-bound (even through a mixin) has no body schema to emit.
func requestHasBodyContent(m *ast.Method, pkg *semantic.Package) bool {
	// Read the resolved IR so this body-presence test uses the SAME
	// verb-aware, mixin-flattened binding the handler decode-block
	// (hasUnboundField) and the param categorisation (binRequestFields)
	// use — a mixin of only @header/@cookie fields contributes no body.
	for _, rf := range resolveRequestFields(m, pkg, nil) {
		if rf.OnWireBody {
			return true
		}
	}
	return false
}

// addPerOperationResponseSchema emits `<Method>RespBody` carrying the
// response shape consumers see in JSON. When the response type has no
// `@header` / `@cookie` bindings the schema is a thin alias of the type
// itself; when it does, header/cookie fields are stripped and only the
// JSON-body fields end up in the schema (the wire form the runtime
// serialises). The matching response.headers map is emitted by
// buildOperation.
func addPerOperationResponseSchema(doc *openapi3.T, m *ast.Method, pkg *semantic.Package, registry *genericRegistry, base string, names *schemaNames) {
	if m.Response == nil || m.Response.Type == nil {
		return
	}
	bins := binResponseFields(m, pkg)
	if len(bins.header) == 0 && len(bins.cookie) == 0 {
		// Generic response (e.g. `response Envelope<Order>`) must
		// $ref the synthetic instance name, NOT the bare generic
		// decl name - the generic decl is never emitted as a
		// component since it has no concrete schema, so a bare
		// `Envelope` $ref would dangle.
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

// substituteGenericFields substitutes a generic instance's type-args into a
// field list's types (`data T` → `data Item`) so a per-operation body schema
// built from a wire-bound generic request/response $refs the concrete arg
// instead of dangling a `$ref` to the type-parameter `T`. A no-op for a
// non-generic type or when no args are supplied.
func substituteGenericFields(fields []*ast.Field, td *ast.TypeDecl, args []*ast.TypeRef) []*ast.Field {
	if td == nil || len(td.TypeParams) == 0 || len(args) == 0 {
		return fields
	}
	subst := substMap(td.TypeParams, args)
	out := make([]*ast.Field, len(fields))
	for i, f := range fields {
		fc := *f
		fc.Type = substituteTypeRef(f.Type, subst)
		out[i] = &fc
	}
	return out
}

// binResponseFields partitions the response type's fields the same way
// [binRequestFields] does on the request side. Fields without an
// explicit response-side binding decorator default to `body` (the JSON
// payload), so adding @header / @cookie to a couple of fields does not
// silently drop the rest.
func binResponseFields(m *ast.Method, pkg *semantic.Package) fieldBins {
	var bins fieldBins
	if m.Response == nil || m.Response.Type == nil {
		return bins
	}
	td, ok := pkg.Types[m.Response.Type.Name.String()]
	if !ok {
		return bins
	}
	// Read the resolved IR instead of re-deriving binding/sensitivity from
	// the AST: the same flattened field list + binding classification every
	// other stage sees, so this categorisation can't drift from theirs.
	for _, rf := range resolveFields(td, pkg, nil) {
		switch rf.Binding {
		case BindSensitive:
			continue
		case BindHeader:
			bins.header = append(bins.header, rf.Field)
		case BindCookie:
			bins.cookie = append(bins.cookie, rf.Field)
		default:
			bins.body = append(bins.body, rf.Field)
		}
	}
	return bins
}

// buildResponseHeaders converts response-side @header / @cookie fields
// into the OpenAPI `response.headers` map. Cookie fields collapse into
// a single `Set-Cookie` entry because OpenAPI 3.x has no first-class
// cookie response slot — listing the cookie names there documents what
// the runtime writes via http.SetCookie even when the spec format has
// to round-trip through Set-Cookie.
func buildResponseHeaders(headers, cookies []*ast.Field, pkg *semantic.Package, registry *genericRegistry) openapi3.Headers {
	if len(headers) == 0 && len(cookies) == 0 {
		return nil
	}
	out := openapi3.Headers{}
	for _, f := range headers {
		name := bindingWireName(f, wire.BindingHeader)
		schema := schemaForTypeRef(f.Type, pkg, registry)
		// Carry @example / @deprecated / field constraints onto the header
		// schema, and @deprecated onto the Header Object itself — the same
		// metadata every other field-emit site applies (paramsFromBins,
		// schemaFromFields), so a documented response header isn't silently
		// stripped of it.
		applyFieldMetadata(f, schema, pkg)
		hdr := &openapi3.Header{
			Parameter: openapi3.Parameter{
				Schema:      schema,
				Description: resolveDescription(f.Decorators, f.Doc),
				Deprecated:  hasDeprecatedDecorator(f.Decorators),
			},
		}
		out[name] = &openapi3.HeaderRef{Value: hdr}
	}
	if len(cookies) > 0 {
		names := make([]string, 0, len(cookies))
		for _, f := range cookies {
			names = append(names, bindingWireName(f, wire.BindingCookie))
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

// schemaFromFields builds an inline object schema covering the supplied
// fields. Required[] lists every non-optional field (required-by-default
// model — the inverse of the `?` suffix); nested types follow
// schemaForTypeRef. Per-field decorator effects (@default, @example,
// @nullable, @deprecated, @doc) are applied via [applyFieldMetadata] so
// per-operation `<Method>Req<Kind>` schemas carry the same metadata
// the top-level type schemas do.
func schemaFromFields(fields []*ast.Field, pkg *semantic.Package, registry *genericRegistry) *openapi3.Schema {
	s := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: openapi3.Schemas{},
	}
	for _, f := range fields {
		if hasSensitiveDecorator(f.Decorators) {
			continue
		}
		ref := schemaForTypeRef(f.Type, pkg, registry)
		applyFieldMetadata(f, ref, pkg)
		s.Properties[f.Name] = ref
		if fieldIsRequired(f) {
			s.Required = append(s.Required, f.Name)
		}
	}
	return s
}
