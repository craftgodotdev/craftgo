// Operation assembly: buildOperation, parameters, errors, tags, security.
package codegen

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func buildOperation(svcName string, m *ast.Method, pkg *semantic.Package, registry *genericRegistry, base string) *openapi3.Operation {
	op := &openapi3.Operation{
		OperationID: operationID(m, base),
		Tags:        operationTags(svcName, m, pkg),
		// NewResponsesWithCapacity (unlike NewResponses) seeds no phantom
		// `default` catch-all. Every operation Sets at least a success
		// response below, so the map is never empty.
		Responses:   openapi3.NewResponsesWithCapacity(2),
		Description: resolveDescription(m.Decorators, m.Doc),
		Summary:     decoratorStringArg(m.Decorators, "summary"),
	}
	svc := pkg.Services[svcName]
	// Service-level `@security` is appended to the method-level chain:
	// each entry in OpenAPI `security[]` is an OR alternative, so
	// declaring `@security(Bearer)` on the service plus `@security(Admin)`
	// on a method means "either Bearer alone OR Admin alone unlocks this
	// op". `@ignoreSecurity` on a method clears the inherited chain so
	// the method-level (if any) starts from empty - useful for public
	// endpoints inside an otherwise-authenticated service.
	ignoreSec := hasOwnDecorator(m.Decorators, "ignoreSecurity")
	var sec *openapi3.SecurityRequirements
	if !ignoreSec && svc != nil && svc.Primary != nil {
		sec = securityFromDecorators(svc.Primary.Decorators)
	}
	methodDecs := m.Decorators
	if ignoreSec {
		// Drop decorators propagated from the extend block too - those
		// count as "inherited" alongside the primary's chain, so the
		// method-level @ignoreSecurity should clear them.
		filtered := make([]*ast.Decorator, 0, len(m.Decorators))
		for _, d := range m.Decorators {
			if d != nil && d.Propagated && d.Name == "security" {
				continue
			}
			filtered = append(filtered, d)
		}
		methodDecs = filtered
	}
	if methodSec := securityFromDecorators(methodDecs); methodSec != nil {
		if sec == nil {
			sec = methodSec
		} else {
			combined := append(openapi3.SecurityRequirements{}, *sec...)
			combined = append(combined, *methodSec...)
			sec = &combined
		}
	}
	if sec != nil {
		deduped := dedupSecurity(*sec)
		op.Security = &deduped
	}
	// @deprecated may sit on the method itself or on the primary
	// service decl; either marks the operation deprecated. The
	// optional reason becomes a `Deprecated: ...` line in the
	// description so docs viewers surface it inline.
	deprecated := hasDeprecatedDecorator(m.Decorators)
	if !deprecated && svc != nil && svc.Primary != nil {
		deprecated = hasDeprecatedDecorator(svc.Primary.Decorators)
	}
	if deprecated {
		op.Deprecated = true
		reason := deprecatedReason(m.Decorators)
		if reason == "" && svc != nil && svc.Primary != nil {
			reason = deprecatedReason(svc.Primary.Decorators)
		}
		if reason != "" {
			op.Description = appendDescription(op.Description, "Deprecated: "+reason)
		}
	}
	isPassthrough := hasPassthroughDecorator(m.Decorators)
	isMultipart := false
	formStrings, formFiles := []paramBinding(nil), []paramBinding(nil)
	if m.Request != nil && !isPassthrough {
		// pkgAlias is empty here - the OpenAPI emission path doesn't
		// care about Go-side cast aliasing, only about which fields
		// are file vs text. Errors from form binding (cookie array,
		// numeric @form, ...) are surfaced by the transport gen pass;
		// silently drop them here so a single source of truth owns
		// the diagnostic.
		if fs, ff, _, err := collectFormBindings(m, pkg, "", nil); err == nil && len(ff) > 0 {
			isMultipart = true
			formStrings, formFiles = fs, ff
		}
	}
	if m.Request != nil && !isPassthrough {
		bins := binRequestFields(m, pkg)
		// Body-bearing verbs $ref the per-method body schema. The
		// per-kind schemas live in components.schemas so consumers have
		// a single canonical reference for each binding kind.
		if hasBodyVerb(m.Verb) {
			switch {
			case isMultipart:
				op.RequestBody = multipartRequestBody(formStrings, formFiles, pkg, registry)
			case len(bins.body) > 0:
				op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
					Required: true,
					Content: openapi3.Content{
						"application/json": &openapi3.MediaType{
							Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/" + base + "ReqBody"},
						},
					},
				}}
			}
		}
		// Parameters keep individual entries - that's the OpenAPI norm -
		// but each field's `schema:` $refs into the matching
		// `<Method>Req<Kind>` schema, which holds the canonical
		// definition. Multipart skips path/query/header params here too
		// since the form-data body covers the regular fields; only true
		// path/query/header bindings remain (handled in paramsFromBins).
		if !isMultipart {
			op.Parameters = paramsFromBins(bins, pkg, registry)
		} else {
			op.Parameters = paramsFromBins(fieldBins{path: bins.path, query: bins.query, header: bins.header, cookie: bins.cookie}, pkg, registry)
		}
	}
	if isPassthrough {
		op.Parameters = passthroughPathParams(m)
	}
	switch {
	case isPassthrough:
		successCode := passthroughStatus(m)
		desc := successDescription(successCode)
		op.Responses.Set(successCode, &openapi3.ResponseRef{Value: &openapi3.Response{
			Description: &desc,
			Content: openapi3.Content{
				"*/*": &openapi3.MediaType{},
			},
		}})
	case m.Response != nil && m.Response.Type != nil:
		successCode := strconv.Itoa(methodSuccessStatus(m))
		desc := successDescription(successCode)
		resp := &openapi3.Response{
			Description: &desc,
			Content: openapi3.Content{
				"application/json": &openapi3.MediaType{
					// Per the request-side convention, the response body
					// is referenced via `<Method>RespBody` so consumers
					// have a stable, per-operation $ref target.
					Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/" + base + "RespBody"},
				},
			},
		}
		if respBins := binResponseFields(m, pkg); len(respBins.header) > 0 || len(respBins.cookie) > 0 {
			resp.Headers = buildResponseHeaders(respBins.header, respBins.cookie, pkg, registry)
		}
		op.Responses.Set(successCode, &openapi3.ResponseRef{Value: resp})
	default:
		successCode := strconv.Itoa(methodSuccessStatus(m))
		desc := successDescription(successCode)
		op.Responses.Set(successCode, &openapi3.ResponseRef{Value: &openapi3.Response{Description: &desc}})
	}
	addErrorResponses(op, m, pkg, registry)
	return op
}

// successDescription returns the IANA-registered reason phrase for an
// HTTP status code so OpenAPI clients see `Created` for 201, `No Content`
// for 204, etc. Falls back to "OK" for unknown codes - a generic but
// valid placeholder is better than an empty description (which some
// validators flag as required).
func successDescription(code string) string {
	n, err := strconv.Atoi(code)
	if err != nil {
		return "OK"
	}
	if text := http.StatusText(n); text != "" {
		return text
	}
	return "OK"
}

// statusOverride returns the explicit `@status(N)` code declared on the
// method, if any. The value is range-validated (100..599) by the
// semantic layer, so codegen can trust it.
func statusOverride(m *ast.Method) (int, bool) {
	for _, d := range m.Decorators {
		if d.Name != "status" || len(d.Args) == 0 {
			continue
		}
		if i, ok := d.Args[0].Value.(*ast.IntLit); ok {
			return int(i.Value), true
		}
	}
	return 0, false
}

// methodSuccessStatus resolves the success status code for a
// non-passthrough method. The transport handler and the OpenAPI spec
// both call this so they always agree on the same code. `@status(N)`
// wins; otherwise the default is verb-aware:
//
//   - no response body           → 204 No Content
//   - POST returning a body       → 201 Created
//   - any other verb with a body  → 200 OK
//
// The "no body → 204" rule deliberately takes precedence over the verb
// default: a POST that returns nothing is 204, not 201.
func methodSuccessStatus(m *ast.Method) int {
	if code, ok := statusOverride(m); ok {
		return code
	}
	if m.Response == nil || m.Response.Type == nil {
		return http.StatusNoContent
	}
	if strings.EqualFold(m.Verb, "post") {
		return http.StatusCreated
	}
	return http.StatusOK
}

// passthroughStatus is the success code documented for a `@passthrough`
// operation. The handler writes the response itself, so codegen cannot
// know the real status: `@status(N)` documents it explicitly, otherwise
// we fall back to 200. The verb-aware default is intentionally NOT
// applied here — a passthrough POST may write any status, and 201 would
// frequently be wrong.
func passthroughStatus(m *ast.Method) string {
	if code, ok := statusOverride(m); ok {
		return strconv.Itoa(code)
	}
	return "200"
}

// addErrorResponses walks `@errors(NameA, NameB)` on the method and
// adds one OpenAPI response entry per declared error type. The status
// code comes from the error's category (categoryStatus) and the schema
// $refs the error type's components.schemas entry. Unknown error names
// are silently skipped - semantic phase doesn't validate the refs yet,
// so we treat that as best-effort docs rather than fail codegen.
//
// When two or more `@errors(...)` entries share an HTTP status (e.g.
// both `Conflict EmailTaken` and `Conflict OwnershipConflict` → 409),
// the schemas merge into a `oneOf` list. Without this merge the second
// `op.Responses.Set(...)` call would overwrite the first and the lost
// error would be invisible to OpenAPI consumers.
func addErrorResponses(op *openapi3.Operation, m *ast.Method, pkg *semantic.Package, registry *genericRegistry) {
	names := errorRefsFromDecorators(m.Decorators)
	if len(names) == 0 {
		return
	}
	// Group declared error refs by HTTP status so multiple errors with
	// the same category render as a single oneOf response.
	type byStatus struct {
		refs       []string
		categories []string
		headers    []*ast.Field
		cookies    []*ast.Field
	}
	grouped := map[string]*byStatus{}
	var statusOrder []string
	for _, name := range names {
		ed, ok := pkg.Errors[name]
		if !ok {
			continue
		}
		typeName := errSuffix(ed.Name)
		status := strconv.Itoa(errcat.Status(ed.Category))
		entry, exists := grouped[status]
		if !exists {
			entry = &byStatus{}
			grouped[status] = entry
			statusOrder = append(statusOrder, status)
		}
		entry.refs = append(entry.refs, "#/components/schemas/"+typeName)
		entry.categories = append(entry.categories, ed.Category)
		// An error's @header / @cookie body fields are written onto the
		// response by the generated WriteResponseHeaders, so document
		// them as response.headers — mirroring the success-response path.
		hs, cs := errorHeaderCookieFields(ed, pkg)
		entry.headers = append(entry.headers, hs...)
		entry.cookies = append(entry.cookies, cs...)
	}
	for _, status := range statusOrder {
		entry := grouped[status]
		desc := entry.categories[0]
		var schema *openapi3.SchemaRef
		if len(entry.refs) == 1 {
			schema = &openapi3.SchemaRef{Ref: entry.refs[0]}
		} else {
			oneOf := make(openapi3.SchemaRefs, 0, len(entry.refs))
			for _, ref := range entry.refs {
				oneOf = append(oneOf, &openapi3.SchemaRef{Ref: ref})
			}
			schema = &openapi3.SchemaRef{Value: &openapi3.Schema{OneOf: oneOf}}
		}
		resp := &openapi3.Response{
			Description: &desc,
			Content: openapi3.Content{
				"application/json": &openapi3.MediaType{Schema: schema},
			},
		}
		if h := buildResponseHeaders(entry.headers, entry.cookies, pkg, registry); len(h) > 0 {
			resp.Headers = h
		}
		// A success `@status` may already occupy this code (e.g. `@status(409)`
		// on the method plus a Conflict-category `@errors`): the runtime
		// returns the success body on the happy path and this error on the
		// failure path, so merge both into a oneOf rather than letting the
		// error overwrite — and silently orphan — the success shape.
		if existing := op.Responses.Value(status); existing != nil && existing.Value != nil {
			resp = mergeStatusResponses(existing.Value, resp, schema)
		}
		op.Responses.Set(status, &openapi3.ResponseRef{Value: resp})
	}
}

// mergeStatusResponses combines a response already registered at a status (the
// method's success shape) with an error response that lands on the same code.
// The two body schemas join as a oneOf so neither is dropped; the descriptions
// are concatenated and the headers unioned. A response with no JSON body (a
// bare-status success) contributes only its description.
func mergeStatusResponses(existing, errResp *openapi3.Response, errSchema *openapi3.SchemaRef) *openapi3.Response {
	var oneOf openapi3.SchemaRefs
	add := func(s *openapi3.SchemaRef) {
		if s == nil {
			return
		}
		// Flatten an existing oneOf so the merged list stays a single level.
		if s.Ref == "" && s.Value != nil && len(s.Value.OneOf) > 0 {
			oneOf = append(oneOf, s.Value.OneOf...)
			return
		}
		oneOf = append(oneOf, s)
	}
	if mt := existing.Content.Get("application/json"); mt != nil {
		add(mt.Schema)
	}
	add(errSchema)

	desc := ""
	if existing.Description != nil {
		desc = *existing.Description
	}
	if errResp.Description != nil && *errResp.Description != "" && *errResp.Description != desc {
		if desc != "" {
			desc += " or "
		}
		desc += *errResp.Description
	}
	merged := &openapi3.Response{Description: &desc}
	if len(oneOf) == 1 {
		merged.Content = openapi3.Content{"application/json": &openapi3.MediaType{Schema: oneOf[0]}}
	} else if len(oneOf) > 1 {
		merged.Content = openapi3.Content{"application/json": &openapi3.MediaType{
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{OneOf: oneOf}},
		}}
	}
	// Union the response headers (the success set first, then any error
	// headers the existing set doesn't already carry).
	if len(existing.Headers) > 0 || len(errResp.Headers) > 0 {
		merged.Headers = openapi3.Headers{}
		for k, v := range existing.Headers {
			merged.Headers[k] = v
		}
		for k, v := range errResp.Headers {
			if _, ok := merged.Headers[k]; !ok {
				merged.Headers[k] = v
			}
		}
	}
	return merged
}

// errorHeaderCookieFields partitions an error declaration's body into
// its @header and @cookie fields — the ones the runtime writes onto the
// response via WriteResponseHeaders rather than into the JSON body.
// Mirrors [binResponseFields] for the error path.
func errorHeaderCookieFields(ed *ast.ErrorDecl, pkg *semantic.Package) (headers, cookies []*ast.Field) {
	// Flatten so a `@header` / `@cookie` field the error inherits through a
	// mixin is documented as a response header too — matching the runtime,
	// which writes the promoted field via WriteResponseHeaders.
	for _, f := range flattenFields(&ast.TypeDecl{Body: ed.Body}, pkg, nil, map[string]bool{}) {
		switch bindingFromDecorators(f.Decorators) {
		case "header":
			headers = append(headers, f)
		case "cookie":
			cookies = append(cookies, f)
		}
	}
	return headers, cookies
}

// errorRefsFromDecorators flattens every `@errors(NameA, NameB, ...)`
// chain on the method into a deduplicated list of error declaration
// names. Both the bare-ident form (`@errors(Foo)`) and the
// fully-qualified `pkg.Foo` form parse here — qualified refs
// collapse to the trailing segment because cross-package resolution
// isn't yet supported.
func errorRefsFromDecorators(ds []*ast.Decorator) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range ds {
		if d.Name != "errors" {
			continue
		}
		for _, a := range d.Args {
			for _, v := range ast.DecoratorArgValues(a) {
				id, ok := v.(*ast.IdentExpr)
				if !ok || id.Name == nil {
					continue
				}
				name := id.Name.Parts[len(id.Name.Parts)-1]
				if seen[name] {
					continue
				}
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// passthroughPathParams emits one OpenAPI path-parameter entry per
// `{name}` segment in the route. Passthrough endpoints have no
// request type to mine for typed parameters, so the schema is the
// minimal `string` placeholder - enough to render Swagger UI's
// "try it" form without lying about the wire shape.
func passthroughPathParams(m *ast.Method) openapi3.Parameters {
	if m.Path == nil {
		return nil
	}
	var params openapi3.Parameters
	for _, seg := range m.Path.Segments {
		if !seg.Param {
			continue
		}
		params = append(params, &openapi3.ParameterRef{Value: &openapi3.Parameter{
			Name:     seg.Literal,
			In:       "path",
			Required: true,
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type: &openapi3.Types{"string"},
			}},
		}})
	}
	return params
}

// multipartRequestBody renders a `multipart/form-data` schema covering
// every plain-text form field plus every `file` field declared on the
// request type. File fields use `format: binary`, which Swagger UI
// renders as a file picker.
//
// Files carrying `@mimeTypes(["a/b", "c/d"])` surface their allowlist
// under the OpenAPI `encoding[field].contentType` slot — without this
// the client SDK has no way to see what MIME types the server's
// runtime validator will accept, so users would upload an arbitrary
// file and get a 400 from the validator instead of a typed rejection
// at SDK call time.
func multipartRequestBody(forms, files []paramBinding, pkg *semantic.Package, registry *genericRegistry) *openapi3.RequestBodyRef {
	props := openapi3.Schemas{}
	// required lists every non-optional form/file field so a generated
	// client mirrors the server's validator (a non-`?` `file @form` field
	// is mandatory). Without it the schema advertises every uploaded
	// field as optional and the SDK lets the caller omit a file the
	// handler then rejects with a 400.
	var required []string
	for _, f := range forms {
		// Build the field's real schema (type + `@maxLength` /
		// nullability / ...) the same way the JSON body component does,
		// instead of a bare `{type: string}` that drops every constraint
		// from the SERVED schema while the unused `<Name>ReqBody`
		// component kept them.
		var ref *openapi3.SchemaRef
		if f.Field != nil {
			ref = schemaForTypeRef(f.Field.Type, pkg, registry)
			applyFieldMetadata(f.Field, ref, pkg)
		} else {
			ref = &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}
		}
		props[f.DSLName] = ref
		if f.Required {
			required = append(required, f.DSLName)
		}
	}
	encoding := map[string]*openapi3.Encoding{}
	for _, f := range files {
		props[f.DSLName] = &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:   &openapi3.Types{"string"},
			Format: "binary",
		}}
		if f.Required {
			required = append(required, f.DSLName)
		}
		if len(f.MimeTypes) > 0 {
			encoding[f.DSLName] = &openapi3.Encoding{
				ContentType: strings.Join(f.MimeTypes, ", "),
			}
		}
	}
	mt := &openapi3.MediaType{
		Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Properties: props,
			Required:   required,
		}},
	}
	if len(encoding) > 0 {
		mt.Encoding = encoding
	}
	return &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
		Required: true,
		Content:  openapi3.Content{"multipart/form-data": mt},
	}}
}

// paramsFromBins flattens the non-body bins into the `parameters[]`
// slice the OpenAPI spec requires. Path is always required; query /
// header / cookie required flags follow the field's optionality (the
// inverse of `?`). Each parameter's schema is emitted inline (by value)
// rather than `$ref`-ing into the wrapper `<Method>Req<Kind>` schema.
// Nested `$ref` (into `.../properties/<name>`) is technically valid
// JSON-Pointer but many TS / Java / Rust client generators (hey-api,
// openapi-typescript, openapi-generator < 7) fail to derive a stable
// type name from the property-walk path and emit anonymous placeholders
// or drop the type entirely. Inlining keeps the spec portable; the
// wrapper schemas stay in `components.schemas` for tooling that wants
// to ref the full request shape.
func paramsFromBins(bins fieldBins, pkg *semantic.Package, registry *genericRegistry) openapi3.Parameters {
	var params openapi3.Parameters
	add := func(in string, fields []*ast.Field, alwaysRequired bool) {
		for _, f := range fields {
			required := alwaysRequired || fieldIsRequired(f)
			ref := schemaForTypeRef(f.Type, pkg, registry)
			applyFieldMetadata(f, ref, pkg)
			params = append(params, &openapi3.ParameterRef{Value: &openapi3.Parameter{
				// Wire name, NOT the DSL field name: an explicit
				// `@header("X-Trace-Id")` / `@cookie("session_id")` /
				// `@query(..)` / `@path(..)` argument overrides the
				// field name. The runtime binder reads the same wire
				// name via [bindingWireName] (r.Header.Get("X-Trace-Id"),
				// r.PathValue("user_id"), ...), so emitting f.Name here
				// instead would advertise a parameter the server never
				// reads — a generated client would send `trace` while
				// the handler looks for `X-Trace-Id`, and the binding
				// silently fails.
				Name:     bindingWireName(f, in),
				In:       in,
				Required: required,
				// A deprecated parameter carries `deprecated` on the
				// Parameter object itself (what Swagger UI / client codegen
				// read for params), not only on the inner schema.
				Deprecated: hasDeprecatedDecorator(f.Decorators),
				Schema:     ref,
			}})
		}
	}
	add("path", bins.path, true)
	add("query", bins.query, false)
	add("header", bins.header, false)
	add("cookie", bins.cookie, false)
	return params
}

// bindingFromDecorators returns the OpenAPI `in` string implied by a
// field-binding decorator, or "" when the field has no explicit binding.
// `@body` and `@form` are returned verbatim so the caller can recognise
// and skip them - body-shaped fields land in requestBody, not parameters.
func bindingFromDecorators(ds []*ast.Decorator) string {
	return semantic.BindingKind(ds)
}

// hasOwnDecorator reports whether ds carries a non-propagated decorator
// with the given name. Used for the bare presence checks that drive
// decorators copied onto the method from an enclosing scope (currently
// `extend service` blocks - see [ast.Decorator.Propagated]). The
// `@ignore*` family must match only decorators the user wrote directly
// above the method; a propagated `@ignoreMiddleware` would have been
// rejected at extend-block placement anyway, but the explicit filter
// keeps the semantic clear.
func hasOwnDecorator(ds []*ast.Decorator, name string) bool {
	for _, d := range ds {
		if d == nil || d.Propagated {
			continue
		}
		if d.Name == name {
			return true
		}
	}
	return false
}

// setOperation routes a built operation onto the right verb slot.
func setOperation(item *openapi3.PathItem, verb string, op *openapi3.Operation) {
	switch strings.ToUpper(verb) {
	case "GET":
		item.Get = op
	case "POST":
		item.Post = op
	case "PUT":
		item.Put = op
	case "PATCH":
		item.Patch = op
	case "DELETE":
		item.Delete = op
	case "HEAD":
		item.Head = op
	case "OPTIONS":
		item.Options = op
	}
}

// fieldIsRequired reports whether f must be present in the request
// payload. craftgo's "required by default" model: a field is required
// unless its type carries the `?` suffix that explicitly marks it
// optional, OR it carries a `@default` — the transport pre-fills the
// default before decode (for both wire params and body fields), so an
// absent value is valid. Advertising such a field `required: true`
// contradicts the `default` the same schema carries and disagrees with
// the server, which never rejects its absence.
func fieldIsRequired(f *ast.Field) bool {
	if f == nil || f.Type == nil || f.Type.Optional {
		return false
	}
	return !ast.HasDecorator(f.Decorators, "default")
}

// operationID returns the OpenAPI operationId for a method. A method
// decorated with `@operationId("createUserProfile")` overrides the
// default verbatim (so projects can adopt camelCase / kebab-case /
// whatever convention their tooling expects). Otherwise it falls back
// to `base` — the collision-free name from [operationBaseName], which is
// the bare method name when unique and service-prefixed when two
// services share the method name.
func operationID(m *ast.Method, base string) string {
	return semantic.OperationID(m, base)
}

// operationTags assembles the OpenAPI `tags:` slice for one method.
// Service-level `@tags(...)` come first (so they sort before method
// tags in the resulting spec), then method-level `@tags(...)` are
// appended. `@ignoreTags` on a method skips the service-level chain
// entirely. When neither level declares tags the service name is used
// as a single default - keeping every operation grouped by service for
// tools that don't render an empty tag list well.
func operationTags(svcName string, m *ast.Method, pkg *semantic.Package) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	ignore := hasOwnDecorator(m.Decorators, "ignoreTags")
	if !ignore {
		if svc, ok := pkg.Services[svcName]; ok && svc.Primary != nil {
			for _, t := range tagsFromDecorators(svc.Primary.Decorators) {
				add(t)
			}
		}
	}
	for _, d := range m.Decorators {
		if d == nil || d.Name != "tags" {
			continue
		}
		if d.Propagated && ignore {
			continue
		}
		for _, t := range tagsFromDecorators([]*ast.Decorator{d}) {
			add(t)
		}
	}
	if len(out) == 0 {
		out = []string{svcName}
	}
	return out
}

// tagsFromDecorators collects every argument from every `@tags(...)`
// decorator in ds. Arguments may be string literals (`@tags("v1")`) or
// bare identifiers (`@tags(api, v1)`); both shapes produce the same
// stringified entry in the resulting slice.
func tagsFromDecorators(ds []*ast.Decorator) []string {
	var out []string
	for _, d := range ds {
		if d.Name != "tags" {
			continue
		}
		for _, a := range d.Args {
			for _, val := range ast.DecoratorArgValues(a) {
				switch v := val.(type) {
				case *ast.StringLit:
					out = append(out, v.Value)
				case *ast.IdentExpr:
					out = append(out, v.Name.String())
				}
			}
		}
	}
	return out
}

// dedupSecurity removes duplicate security requirements (identical
// scheme→scopes sets) that arise when a method repeats a requirement its
// service already declares. Each requirement is an OR-alternative, so two
// identical entries are redundant; mirrors the tag dedup so the spec
// carries one entry per distinct alternative.
func dedupSecurity(reqs openapi3.SecurityRequirements) openapi3.SecurityRequirements {
	seen := map[string]bool{}
	out := make(openapi3.SecurityRequirements, 0, len(reqs))
	for _, req := range reqs {
		keys := make([]string, 0, len(req))
		for k, scopes := range req {
			keys = append(keys, k+"="+strings.Join(scopes, ","))
		}
		sort.Strings(keys)
		key := strings.Join(keys, "&")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, req)
	}
	return out
}

// securityFromDecorators turns `@security(SchemeA, SchemeB)` declarations
// on a method or service into the OpenAPI `security` slice. Each
// decorator argument that is an identifier becomes one entry whose value
// is an empty scopes list - multi-scheme arguments inside a single
// decorator are AND-combined; multiple `@security(...)` decorators are
// OR-combined per the OpenAPI spec semantics. The array-shortcut form
// `@security([A, B])` is treated as equivalent to `@security(A, B)`. To
// opt out of inherited service-level security, use `@ignoreSecurity` at
// the method level instead of a sentinel scheme name.
func securityFromDecorators(ds []*ast.Decorator) *openapi3.SecurityRequirements {
	var reqs openapi3.SecurityRequirements
	for _, d := range ds {
		if d.Name != "security" {
			continue
		}
		req := openapi3.SecurityRequirement{}
		for _, a := range d.Args {
			for _, v := range ast.DecoratorArgValues(a) {
				if id, ok := v.(*ast.IdentExpr); ok {
					req[id.Name.String()] = []string{}
				}
			}
		}
		reqs = append(reqs, req)
	}
	if len(reqs) == 0 {
		return nil
	}
	return &reqs
}
