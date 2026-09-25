package docs

import (
	"maps"
	"net/http"
	"slices"
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
	svc := pkg.Services[svcName]
	decs := svc.Decorators(m)
	op := &openapi3.Operation{
		OperationID: operationID(decs, base),
		Tags:        operationTags(svcName, m, pkg),
		// NewResponses would seed a `default` response.
		Responses:   openapi3.NewResponsesWithCapacity(2),
		Description: semantic.Description(decs, m.Doc),
		Summary:     summaryOf(decs),
	}
	// Any one requirement is enough; the service's come first.
	service, member, _ := svc.InheritedDecorators(m, "security")
	if sec := securityFromDecorators(slices.Concat(service, member)); sec != nil {
		deduped := dedupSecurity(*sec)
		op.Security = &deduped
	}
	// @deprecated on the method or its primary service marks the operation;
	// the reason joins the description.
	deprecated := semantic.IsDeprecated(decs)
	if !deprecated && svc != nil && svc.Primary != nil {
		deprecated = semantic.IsDeprecated(svc.Primary.Decorators)
	}
	if deprecated {
		op.Deprecated = true
		reason := semantic.DeprecatedReason(decs)
		if reason == "" && svc != nil && svc.Primary != nil {
			reason = semantic.DeprecatedReason(svc.Primary.Decorators)
		}
		if reason != "" {
			op.Description = appendDescription(op.Description, "Deprecated: "+reason)
		}
	}
	// A block on a raw side is documented like a typed one; the raw flags
	// matter only without a block and for a raw response's success status.
	rawReq, rawResp := wire.RawSides(decs)
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
		successCode := strconv.Itoa(wire.SuccessStatus(m, decs))
		if rawResp {
			successCode = rawResponseStatus(decs)
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
		successCode := rawResponseStatus(decs)
		desc := successDescription(successCode)
		op.Responses.Set(successCode, &openapi3.ResponseRef{Value: &openapi3.Response{
			Description: &desc,
			Content: openapi3.Content{
				"*/*": &openapi3.MediaType{},
			},
		}})
	default:
		successCode := strconv.Itoa(wire.SuccessStatus(m, decs))
		desc := successDescription(successCode)
		op.Responses.Set(successCode, &openapi3.ResponseRef{Value: &openapi3.Response{Description: &desc}})
	}
	addErrorResponses(op, decs, pkg, registry)
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

// rawResponseStatus is the documented success code of a raw response with
// decorators decs: `@status(N)`, else 200 whatever the verb, since logic
// writes the status.
func rawResponseStatus(decs []*ast.Decorator) string {
	if code, ok := wire.StatusOverride(decs); ok {
		return strconv.Itoa(code)
	}
	return "200"
}

// addErrorResponses adds a response per error the `@errors` among decs name,
// at its category's status, errors sharing a status in one `oneOf`; an
// unknown name is skipped.
func addErrorResponses(op *openapi3.Operation, decs []*ast.Decorator, pkg *semantic.Package, registry *genericRegistry) {
	names := errorRefsFromDecorators(decs)
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
	for _, ff := range semantic.FlattenFields(&ast.TypeDecl{Body: ed.Body}, "", r, nil) {
		f := ff.Field
		switch kind, _ := wire.BindingKind(f.Decorators); kind {
		case wire.BindHeader:
			headers = append(headers, f)
		case wire.BindCookie:
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
		for _, n := range ast.ArgNames(d) {
			name := n.Value[strings.LastIndexByte(n.Value, '.')+1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
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
			In:       wire.BindPath.String(),
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
	add := func(in wire.Binding, fields []*ast.Field, alwaysRequired bool) {
		for _, f := range fields {
			required := alwaysRequired || semantic.FieldIsRequired(f)
			ref := schemaForTypeRef(f.Type, pkg, registry)
			applyFieldMetadata(f, ref, pkg)
			params = append(params, &openapi3.ParameterRef{Value: &openapi3.Parameter{
				Name:     wire.WireName(f, in),
				In:       in.String(),
				Required: required,
				// The Parameter carries `deprecated` too, not only its schema.
				Deprecated: semantic.IsDeprecated(f.Decorators),
				Schema:     ref,
			}})
		}
	}
	add(wire.BindPath, bins.path, true)
	add(wire.BindQuery, bins.query, false)
	add(wire.BindHeader, bins.header, false)
	add(wire.BindCookie, bins.cookie, false)
	return params
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

func operationID(decs []*ast.Decorator, base string) string {
	return semantic.OperationID(decs, base)
}

// operationTags returns the service's `@tags`, its `@group`, then the method's
// `@tags`, each once, else the service name. `@ignoreTags` keeps only the
// method's own.
func operationTags(svcName string, m *ast.Method, pkg *semantic.Package) []string {
	var out []string
	add := func(tags ...string) {
		for _, t := range tags {
			if t != "" && !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	svc := pkg.Services[svcName]
	service, member, ignored := svc.InheritedDecorators(m, "tags")
	add(tagsFromDecorators(service)...)
	if !ignored && svc.Primary != nil {
		// The group, whole ("admin/ops"), is that of the method's own
		// block: an extend block's methods carry its @group.
		add(semantic.MethodGroupOf(svc, m))
	}
	add(tagsFromDecorators(member)...)
	if len(out) == 0 {
		out = []string{svcName}
	}
	return out
}

// tagsFromDecorators returns the tags every `@tags` in ds lists.
func tagsFromDecorators(ds []*ast.Decorator) []string {
	var out []string
	for _, d := range ds {
		if d == nil || d.Name != "tags" {
			continue
		}
		for _, n := range ast.ArgNames(d) {
			out = append(out, n.Value)
		}
	}
	return out
}

// summaryOf returns the `@summary` text in ds.
func summaryOf(ds []*ast.Decorator) string {
	s, _ := ast.StringArg(ds, "summary")
	return s
}
