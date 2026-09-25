package docs

import (
	"cmp"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

const (
	mimeApplicationJSON   = "application/json"
	mimeMultipartFormData = "multipart/form-data"
)

// buildOperation builds the operation of s, adding to doc the body components
// it refs.
func buildOperation(doc *openapi3.T, svc *semantic.ServiceInfo, s opShape, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) *openapi3.Operation {
	op := &openapi3.Operation{
		OperationID: semantic.OperationID(s.decs, s.base),
		Tags:        operationTags(svc, s.m),
		// NewResponses would seed a `default` response.
		Responses:   openapi3.NewResponsesWithCapacity(2),
		Description: semantic.Description(s.decs, s.m.Doc),
		Summary:     summaryOf(s.decs),
		Security:    operationSecurity(svc, s.m),
	}
	markDeprecated(op, svc, s.decs)
	requestSide(doc, op, s, pkg, registry, names)
	successResponse(doc, op, s, pkg, registry, names)
	addErrorResponses(op, s.decs, pkg, registry)
	return op
}

// operationSecurity returns m's security requirements, the service's first,
// or nil for none; any one requirement is enough.
func operationSecurity(svc *semantic.ServiceInfo, m *ast.Method) *openapi3.SecurityRequirements {
	service, member, _ := svc.InheritedDecorators(m, "security")
	sec := securityFromDecorators(slices.Concat(service, member))
	if sec == nil {
		return nil
	}
	deduped := dedupSecurity(*sec)
	return &deduped
}

// markDeprecated marks op deprecated when decs, a method's decorators, or its
// primary service carry @deprecated; the reason, the method's first, joins the description.
func markDeprecated(op *openapi3.Operation, svc *semantic.ServiceInfo, decs []*ast.Decorator) {
	service := svc.Primary.Decorators
	if !semantic.IsDeprecated(decs) && !semantic.IsDeprecated(service) {
		return
	}
	op.Deprecated = true
	if reason := cmp.Or(semantic.DeprecatedReason(decs), semantic.DeprecatedReason(service)); reason != "" {
		op.Description = appendDescription(op.Description, "Deprecated: "+reason)
	}
}

// requestSide sets op's parameters and request body, adding the
// `<base>ReqBody` component a JSON body refs. A block on a raw side is
// documented like a typed one; a raw request without one has only its path.
func requestSide(doc *openapi3.T, op *openapi3.Operation, s opShape, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) {
	if s.m.Request == nil {
		if rawReq, _ := wire.RawSides(s.decs); rawReq {
			op.Parameters = rawPathParams(s.full)
		}
		return
	}
	op.Parameters = paramsFromBins(s.req, pkg, registry)
	if !wire.IsBodyVerb(s.m.Verb) {
		return
	}
	switch {
	case len(s.files) > 0:
		op.RequestBody = multipartRequestBody(s, pkg, registry)
	case len(s.req.body) > 0:
		names.put(doc, s.base+"ReqBody", requestBodySchema(s, pkg, registry))
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true,
			Content: openapi3.Content{
				mimeApplicationJSON: &openapi3.MediaType{
					Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/" + s.base + "ReqBody"},
				},
			},
		}}
	}
}

// successResponse adds op's success response, and the `<base>RespBody`
// component it refs: a raw response without a block has no schema, since
// logic writes it in any format.
func successResponse(doc *openapi3.T, op *openapi3.Operation, s opShape, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) {
	_, rawResp := wire.RawSides(s.decs)
	code := strconv.Itoa(wire.SuccessStatus(s.m, s.decs))
	if rawResp {
		code = rawResponseStatus(s.decs)
	}
	desc := successDescription(code)
	resp := &openapi3.Response{Description: &desc}
	switch {
	case s.m.Response != nil && s.m.Response.Type != nil:
		names.put(doc, s.base+"RespBody", responseBodySchema(s, pkg, registry))
		resp.Content = openapi3.Content{
			mimeApplicationJSON: &openapi3.MediaType{
				Schema: &openapi3.SchemaRef{Ref: "#/components/schemas/" + s.base + "RespBody"},
			},
		}
		resp.Headers = buildResponseHeaders(s.resp.header, s.resp.cookie, pkg, registry)
	case rawResp:
		resp.Content = openapi3.Content{"*/*": &openapi3.MediaType{}}
	}
	op.Responses.Set(code, &openapi3.ResponseRef{Value: resp})
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
		headers    []semantic.ResolvedField
		cookies    []semantic.ResolvedField
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
		bins := binFields(semantic.ResolveFields(&ast.TypeDecl{Body: ed.Body}, "", pkg, registry.resolver, nil))
		entry.headers = append(entry.headers, bins.header...)
		entry.cookies = append(entry.cookies, bins.cookie...)
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

// errorRefsFromDecorators returns the distinct error names of every `@errors`
// in ds, in order.
func errorRefsFromDecorators(ds []*ast.Decorator) []string {
	var out []string
	for _, d := range ds {
		if d == nil || d.Name != "errors" {
			continue
		}
		for _, n := range ast.ArgNames(d) {
			if !slices.Contains(out, n.Value) {
				out = append(out, n.Value)
			}
		}
	}
	return out
}

// rawPathParams declares each variable of route full, the service @prefix's
// included, as a string path parameter.
func rawPathParams(full string) openapi3.Parameters {
	var params openapi3.Parameters
	for _, name := range route.Vars(full) {
		params = append(params, &openapi3.ParameterRef{Value: &openapi3.Parameter{
			Name:     name,
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
func multipartRequestBody(s opShape, pkg *semantic.Package, registry *genericRegistry) *openapi3.RequestBodyRef {
	props := openapi3.Schemas{}
	keys := map[string]string{}
	var required []string
	for _, f := range s.form {
		ref := schemaForTypeRef(f.Field.Type, pkg, registry)
		applyFieldMetadata(f.Field, ref, pkg)
		props[f.WireName] = ref
		keys[f.Field.Name] = f.WireName
		if f.Required {
			required = append(required, f.WireName)
		}
	}
	encoding := map[string]*openapi3.Encoding{}
	for _, f := range s.files {
		// A file part is present or absent, never null: `required` carries its
		// optionality, so its schema is built from the type without `?`.
		ft := *f.Field.Type
		ft.Optional = false
		ref := schemaForTypeRef(&ft, pkg, registry)
		if ref.Value != nil {
			applyConstraintFamilies(f.Field.Decorators, ref.Value, semantic.ConstraintItems)
		}
		props[f.WireName] = ref
		keys[f.Field.Name] = f.WireName
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
	if frags := crossFieldSchemaFragments(s.reqType.Decorators, keys); len(frags) > 0 {
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
	add := func(in wire.Binding, fields []semantic.ResolvedField, alwaysRequired bool) {
		for _, rf := range fields {
			f := rf.Field
			ref := schemaForTypeRef(f.Type, pkg, registry)
			applyFieldMetadata(f, ref, pkg)
			params = append(params, &openapi3.ParameterRef{Value: &openapi3.Parameter{
				Name:     wire.WireName(f, in),
				In:       in.String(),
				Required: alwaysRequired || rf.SpecRequired,
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

// operationTags returns the service's `@tags`, its `@group`, then the method's
// `@tags`, each once, else the service name. `@ignoreTags` keeps only the
// method's own.
func operationTags(svc *semantic.ServiceInfo, m *ast.Method) []string {
	var out []string
	add := func(tags ...string) {
		for _, t := range tags {
			if t != "" && !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	service, member, ignored := svc.InheritedDecorators(m, "tags")
	add(tagsFromDecorators(service)...)
	if !ignored && svc.Primary != nil {
		// The group, whole ("admin/ops"), is that of the method's own
		// block: an extend block's methods carry its @group.
		add(semantic.MethodGroupOf(svc, m))
	}
	add(tagsFromDecorators(member)...)
	if len(out) == 0 {
		out = []string{svc.Primary.Name}
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
