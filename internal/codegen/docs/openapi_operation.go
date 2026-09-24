package docs

import (
	"maps"
	"net/http"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

const (
	mimeApplicationJSON   = "application/json"
	mimeMultipartFormData = "multipart/form-data"
)

// isMultipartRequest reports whether m's request declares a file field. Such
// a body is inlined as multipart/form-data and gets no `<base>ReqBody`.
func isMultipartRequest(m *ast.Method, pkg *semantic.Package, r *semantic.Resolver) bool {
	if m == nil || m.Request == nil {
		return false
	}
	_, files := semantic.FormFields(m, pkg, r, nil)
	return len(files) > 0
}

func buildOperation(svcName string, m *ast.Method, pkg *semantic.Package, registry *genericRegistry, base string) *openapi3.Operation {
	op := &openapi3.Operation{
		OperationID: operationID(m, base),
		Tags:        operationTags(svcName, m, pkg),
		// NewResponses would seed a `default` response.
		Responses:   openapi3.NewResponsesWithCapacity(2),
		Description: semantic.Description(m.Decorators, m.Doc),
		Summary:     summaryOf(m.Decorators),
	}
	svc := pkg.Services[svcName]
	// Service `@security` requirements come first, then the method's; any one
	// is enough. The method's own `@ignoreSecurity` drops the inherited ones.
	ignoreSec := hasOwnDecorator(m.Decorators, "ignoreSecurity")
	var sec *openapi3.SecurityRequirements
	if !ignoreSec && svc != nil && svc.Primary != nil {
		sec = securityFromDecorators(svc.Primary.Decorators)
	}
	methodDecs := m.Decorators
	if ignoreSec {
		// `@security` propagated from an extend block is inherited too.
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
	// @deprecated on the method or its primary service marks the operation;
	// the reason joins the description.
	deprecated := semantic.IsDeprecated(m.Decorators)
	if !deprecated && svc != nil && svc.Primary != nil {
		deprecated = semantic.IsDeprecated(svc.Primary.Decorators)
	}
	if deprecated {
		op.Deprecated = true
		reason := semantic.DeprecatedReason(m.Decorators)
		if reason == "" && svc != nil && svc.Primary != nil {
			reason = semantic.DeprecatedReason(svc.Primary.Decorators)
		}
		if reason != "" {
			op.Description = appendDescription(op.Description, "Deprecated: "+reason)
		}
	}
	// A block on a raw side is documented like a typed one; the raw flags
	// matter only without a block and for a raw response's success status.
	rawReq, rawResp := wire.RawSides(m.Decorators)
	isMultipart := isMultipartRequest(m, pkg, registry.resolver)
	formStrings, formFiles := []semantic.FormField(nil), []semantic.FormField(nil)
	if isMultipart {
		formStrings, formFiles = semantic.FormFields(m, pkg, registry.resolver, nil)
	}
	if m.Request != nil {
		bins := binRequestFields(m, pkg, registry.resolver)
		if wire.IsBodyVerb(m.Verb) {
			switch {
			case isMultipart:
				// The request type's decorators carry its cross-field constraints.
				var crossDecs []*ast.Decorator
				if m.Request != nil && m.Request.Name != nil {
					if td, ok := pkg.Types[m.Request.Name.String()]; ok {
						crossDecs = td.Decorators
					}
				}
				op.RequestBody = multipartRequestBody(formStrings, formFiles, crossDecs, pkg, registry)
			case len(bins.body) > 0:
				op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
					Required: true,
					Content: openapi3.Content{
						mimeApplicationJSON: &openapi3.MediaType{
							Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/" + base + "ReqBody"},
						},
					},
				}}
			}
		}
		if !isMultipart {
			op.Parameters = paramsFromBins(bins, pkg, registry)
		} else {
			op.Parameters = paramsFromBins(fieldBins{path: bins.path, query: bins.query, header: bins.header, cookie: bins.cookie}, pkg, registry)
		}
	}
	if m.Request == nil && rawReq {
		op.Parameters = rawPathParams(m)
	}
	switch {
	case m.Response != nil && m.Response.Type != nil:
		successCode := strconv.Itoa(wire.SuccessStatus(m))
		if rawResp {
			successCode = rawResponseStatus(m)
		}
		desc := successDescription(successCode)
		resp := &openapi3.Response{
			Description: &desc,
			Content: openapi3.Content{
				mimeApplicationJSON: &openapi3.MediaType{
					Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/" + base + "RespBody"},
				},
			},
		}
		if respBins := binResponseFields(m, pkg, registry.resolver); len(respBins.header) > 0 || len(respBins.cookie) > 0 {
			resp.Headers = buildResponseHeaders(respBins.header, respBins.cookie, pkg, registry)
		}
		op.Responses.Set(successCode, &openapi3.ResponseRef{Value: resp})
	case rawResp:
		// Logic writes a raw response in any format, so it has no schema.
		successCode := rawResponseStatus(m)
		desc := successDescription(successCode)
		op.Responses.Set(successCode, &openapi3.ResponseRef{Value: &openapi3.Response{
			Description: &desc,
			Content: openapi3.Content{
				"*/*": &openapi3.MediaType{},
			},
		}})
	default:
		successCode := strconv.Itoa(wire.SuccessStatus(m))
		desc := successDescription(successCode)
		op.Responses.Set(successCode, &openapi3.ResponseRef{Value: &openapi3.Response{Description: &desc}})
	}
	addErrorResponses(op, m, pkg, registry)
	return op
}

// successDescription returns the reason phrase of status code (`Created`), or
// "OK" when it has none: a response requires a description.
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

// rawResponseStatus is the documented success code of a raw response:
// `@status(N)`, else 200 whatever the verb, since logic writes the status.
func rawResponseStatus(m *ast.Method) string {
	if code, ok := wire.StatusOverride(m); ok {
		return strconv.Itoa(code)
	}
	return "200"
}

// addErrorResponses adds a response per `@errors` error at its category's
// status, errors sharing a status in one `oneOf`; an unknown name is skipped.
func addErrorResponses(op *openapi3.Operation, m *ast.Method, pkg *semantic.Package, registry *genericRegistry) {
	names := errorRefsFromDecorators(m.Decorators)
	if len(names) == 0 {
		return
	}
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
		typeName := idents.ErrorTypeName(ed.Name)
		status := strconv.Itoa(errcat.Status(ed.Category))
		entry, exists := grouped[status]
		if !exists {
			entry = &byStatus{}
			grouped[status] = entry
			statusOrder = append(statusOrder, status)
		}
		entry.refs = append(entry.refs, "#/components/schemas/"+typeName)
		entry.categories = append(entry.categories, ed.Category)
		hs, cs := errorHeaderCookieFields(ed, pkg, registry.resolver)
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
				mimeApplicationJSON: &openapi3.MediaType{Schema: schema},
			},
		}
		if h := buildResponseHeaders(entry.headers, entry.cookies, pkg, registry); len(h) > 0 {
			resp.Headers = h
		}
		// A success `@status` may hold this code already (`@status(409)`).
		if existing := op.Responses.Value(status); existing != nil && existing.Value != nil {
			resp = mergeStatusResponses(existing.Value, resp, schema)
		}
		op.Responses.Set(status, &openapi3.ResponseRef{Value: resp})
	}
}

// mergeStatusResponses joins an error response onto the success one at its
// status: bodies in a oneOf, descriptions with "or", success headers first.
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
	if mt := existing.Content.Get(mimeApplicationJSON); mt != nil {
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
		merged.Content = openapi3.Content{mimeApplicationJSON: &openapi3.MediaType{Schema: oneOf[0]}}
	} else if len(oneOf) > 1 {
		merged.Content = openapi3.Content{mimeApplicationJSON: &openapi3.MediaType{
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{OneOf: oneOf}},
		}}
	}
	if len(existing.Headers) > 0 || len(errResp.Headers) > 0 {
		merged.Headers = openapi3.Headers{}
		maps.Copy(merged.Headers, existing.Headers)
		for k, v := range errResp.Headers {
			if _, ok := merged.Headers[k]; !ok {
				merged.Headers[k] = v
			}
		}
	}
	return merged
}

// errorHeaderCookieFields returns the @header and @cookie fields of ed, the
// ones a mixin brings included.
func errorHeaderCookieFields(ed *ast.ErrorDecl, pkg *semantic.Package, r *semantic.Resolver) (headers, cookies []*ast.Field) {
	for _, f := range semantic.FlattenFields(&ast.TypeDecl{Body: ed.Body}, pkg, r, map[string]bool{}) {
		switch wire.BindingKind(f.Decorators) {
		case wire.BindingHeader:
			headers = append(headers, f)
		case wire.BindingCookie:
			cookies = append(cookies, f)
		}
	}
	return headers, cookies
}

// errorRefsFromDecorators returns the distinct error names of every `@errors`
// in ds, in order; a qualified name keeps its last segment.
func errorRefsFromDecorators(ds []*ast.Decorator) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range ds {
		if d == nil || d.Name != "errors" {
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

// rawPathParams declares each `{name}` segment of m's path as a string path
// parameter.
func rawPathParams(m *ast.Method) openapi3.Parameters {
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
			In:       wire.BindingPath,
			Required: true,
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type: &openapi3.Types{"string"},
			}},
		}})
	}
	return params
}

// multipartRequestBody renders the multipart/form-data body of the form and
// file fields; a file's `@mimeTypes` becomes its `encoding` contentType.
func multipartRequestBody(forms, files []semantic.FormField, crossDecs []*ast.Decorator, pkg *semantic.Package, registry *genericRegistry) *openapi3.RequestBodyRef {
	props := openapi3.Schemas{}
	var required []string
	for _, f := range forms {
		var ref *openapi3.SchemaRef
		if f.Field != nil {
			ref = schemaForTypeRef(f.Field.Type, pkg, registry)
			applyFieldMetadata(f.Field, ref, pkg)
		} else {
			ref = &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}
		}
		props[f.WireName] = ref
		if f.Required {
			required = append(required, f.WireName)
		}
	}
	encoding := map[string]*openapi3.Encoding{}
	for _, f := range files {
		// A file part is present or absent, never null: `required` carries its
		// optionality, so its schema is built from the type without `?`.
		var ref *openapi3.SchemaRef
		if f.Field != nil && f.Field.Type != nil {
			ft := *f.Field.Type
			ft.Optional = false
			ref = schemaForTypeRef(&ft, pkg, registry)
			if ref.Value != nil {
				applyConstraintFamilies(f.Field.Decorators, ref.Value, semantic.ConstraintItems)
			}
		} else {
			ref = &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"}}
		}
		props[f.WireName] = ref
		if f.Required {
			required = append(required, f.WireName)
		}
		if len(f.MimeTypes) > 0 {
			encoding[f.WireName] = &openapi3.Encoding{
				ContentType: strings.Join(f.MimeTypes, ", "),
			}
		}
	}
	schema := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: props,
		Required:   required,
	}
	// Parts go by their form names, so the fragments get no JSON-key map.
	if frags := crossFieldSchemaFragments(crossDecs, nil); len(frags) > 0 {
		schema = &openapi3.Schema{
			Type:  &openapi3.Types{"object"},
			AllOf: append(openapi3.SchemaRefs{{Value: schema}}, frags...),
		}
	}
	mt := &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: schema}}
	if len(encoding) > 0 {
		mt.Encoding = encoding
	}
	return &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
		Required: true,
		Content:  openapi3.Content{mimeMultipartFormData: mt},
	}}
}

// paramsFromBins turns the path, query, header and cookie bins into
// parameters with inline schemas; a path parameter is always required.
func paramsFromBins(bins fieldBins, pkg *semantic.Package, registry *genericRegistry) openapi3.Parameters {
	var params openapi3.Parameters
	add := func(in string, fields []*ast.Field, alwaysRequired bool) {
		for _, f := range fields {
			required := alwaysRequired || semantic.FieldIsRequired(f)
			ref := schemaForTypeRef(f.Type, pkg, registry)
			applyFieldMetadata(f, ref, pkg)
			params = append(params, &openapi3.ParameterRef{Value: &openapi3.Parameter{
				Name:     wire.WireName(f, in),
				In:       in,
				Required: required,
				// The Parameter carries `deprecated` too, not only its schema.
				Deprecated: semantic.IsDeprecated(f.Decorators),
				Schema:     ref,
			}})
		}
	}
	add(wire.BindingPath, bins.path, true)
	add(wire.BindingQuery, bins.query, false)
	add(wire.BindingHeader, bins.header, false)
	add(wire.BindingCookie, bins.cookie, false)
	return params
}

// hasOwnDecorator reports whether ds carries a decorator called name that
// was written on the method, not propagated ([ast.Decorator.Propagated]).
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

// setOperation puts op in item's slot for verb.
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

func operationID(m *ast.Method, base string) string {
	return semantic.OperationID(m, base)
}

// operationTags returns the service's `@tags`, its `@group`, then the method's
// `@tags`, else the service name. `@ignoreTags` keeps only the method's own.
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
			// The group, whole ("admin/ops"), is that of the method's own
			// block: an extend block's methods carry its @group.
			add(semantic.MethodGroupOf(svc, m))
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

// tagsFromDecorators returns the string and identifier arguments of every
// `@tags` in ds.
func tagsFromDecorators(ds []*ast.Decorator) []string {
	var out []string
	for _, d := range ds {
		if d == nil || d.Name != "tags" {
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

// summaryOf returns the `@summary` text in ds.
func summaryOf(ds []*ast.Decorator) string {
	s, _ := semantic.DecoratorStringArg(ds, "summary")
	return s
}
