package golang

import (
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// transportData is the template input for transport.tmpl, one value per method.
type transportData struct {
	Package     string
	Method      string
	Verb        string
	RequestType string
	// RequestPkgAlias is `types` for a local request type, else the request package's alias.
	RequestPkgAlias string
	Doc             []string
	HasRequest      bool
	HasResponse     bool
	BodyVerb        bool
	BodyDecode      bool
	NeedsTypes      bool
	// RawRequest and RawResponse report the transport sides logic owns ([wire.RawSides]).
	RawRequest    bool
	RawResponse   bool
	BindRequest   bool
	WriteResponse bool
	Sig           methodSignature
	IsMultipart   bool
	// MultipartMaxMemory is the ParseMultipartForm memory budget: 32 MiB, or @maxBodySize when larger.
	MultipartMaxMemory int64
	PathParams         []paramBinding
	QueryParams        []paramBinding
	HeaderParams       []paramBinding
	CookieParams       []paramBinding
	FormStrings        []paramBinding
	FormFiles          []paramBinding
	// RespHeaders and RespCookies write the response's @header and @cookie fields before the body.
	RespHeaders []paramBinding
	RespCookies []paramBinding
	// Defaults pre-fill the request before binding, so an absent field keeps its @default.
	Defaults []defaultBinding
	// NeedsStrconv is set when a response header or cookie formats a non-string value.
	NeedsStrconv bool
	// SuccessStatus is the method's [wire.SuccessStatus]; SuccessStatusExpr spells it in Go.
	SuccessStatus     int
	SuccessStatusExpr string
	ServiceImport     string
	TypesImport       string
	SvccontextImport  string
	// ExtraTypesImports are the other packages the request's type and bindings reference.
	ExtraTypesImports []extraImport
}

// extraImport is one aliased import of a generated file.
type extraImport struct {
	Alias string
	Path  string
}

// defaultBinding pre-fills field GoName with the Go expression Literal, through a temp when Ptr.
type defaultBinding struct {
	GoName  string
	Literal string
	Ptr     bool
}

// paramBinding is one field binding in a handler; Bind is its rendered Go statement.
type paramBinding struct {
	DSLName   string
	GoName    string
	Bind      string
	MimeTypes []string
	Required  bool
	Field     *ast.Field
	// IsArray marks a `file[]` field, bound from every part of its name.
	IsArray bool
}

// generateTransport writes each method's handler to output.transport/<segment>/<method>.go,
// the segment being the @group or the service directory. A nil r resolves local names only.
func generateTransport(pkg *semantic.Package, cfg *config.Config, projectRoot string, r *projectResolver) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	r = resolverFor(pkg, r)
	for _, svcName := range pkg.ServiceNames() {
		svc := pkg.Services[svcName]
		if err := generateTransportFor(svcName, svc, pkg, cfg, projectRoot, r); err != nil {
			return err
		}
	}
	return nil
}

func generateTransportFor(svcName string, svc *semantic.ServiceInfo, pkg *semantic.Package, cfg *config.Config, projectRoot string, r *projectResolver) error {
	groups := methodGroups(svc)
	t := tmpl("transport.tmpl")
	for _, m := range svc.Methods {
		group := groups[m.Name]
		imps := importPathsForGroup(cfg, pkg, svcName, group)
		dir := serviceOutputDir(projectRoot, cfg.Output.Transport, svcName, group, cfg.Output.FileCase)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		data, err := buildTransportData(svcName, m, imps, pkg, r)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", svcName, m.Name, err)
		}
		formatted, err := renderGo(t, data)
		if err != nil {
			return fmt.Errorf("render %s transport: %w", idents.FileName(m.Name, cfg.Output.FileCase), err)
		}
		filename := idents.FileName(m.Name, cfg.Output.FileCase) + ".go"
		if err := os.WriteFile(filepath.Join(dir, filename), formatted, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// buildTransportData fails on a field its binding source cannot carry, such as @query on a struct.
func buildTransportData(svcName string, m *ast.Method, imps importPaths, pkg *semantic.Package, r *projectResolver) (transportData, error) {
	crossPkg := r.CrossPkg
	mode := modeOf(m)
	// The handler names a type only in `var req`, so only a bound request needs the types import.
	d := transportData{
		Package:          servicePkgName(pkg.Name, svcName),
		Method:           m.Name,
		Verb:             httpVerb(m.Verb),
		Doc:              m.Doc,
		HasRequest:       mode.HasRequest,
		HasResponse:      mode.HasResponse,
		BodyVerb:         wire.IsBodyVerb(m.Verb),
		RawRequest:       mode.RawRequest,
		RawResponse:      mode.RawResponse,
		BindRequest:      mode.BindRequest(),
		WriteResponse:    mode.WriteResponse(),
		NeedsTypes:       mode.BindRequest(),
		ServiceImport:    imps.Service,
		TypesImport:      imps.Types,
		SvccontextImport: imps.Svccontext,
	}
	var reqRef, respRef string
	if d.BindRequest {
		alias, bare, extra, use := resolveTypeRef(m.Request, crossPkg)
		d.RequestPkgAlias = alias
		d.RequestType = bare
		reqRef = alias + "." + bare
		extraSeen := map[string]bool{}
		addExtra := func(e extraImport) {
			if e.Path == "" || extraSeen[e.Path] {
				return
			}
			extraSeen[e.Path] = true
			d.ExtraTypesImports = append(d.ExtraTypesImports, e)
		}
		// A cross-package request drops the types import unless a local type argument needs it;
		// a request package named `types` takes that alias, so then the import is always dropped.
		if extra.Path != "" {
			if alias == "types" || !use.LocalTypes {
				d.NeedsTypes = false
			}
			addExtra(extra)
		}
		// The request type's generic arguments can reach further packages.
		argSet := map[string]bool{}
		walkCrossPkgImports(&ast.TypeRef{Named: m.Request}, crossPkg, argSet)
		pathAlias := map[string]string{}
		for a, p := range crossPkg {
			pathAlias[p] = a
		}
		for path := range argSet {
			addExtra(extraImport{Alias: pathAlias[path], Path: path})
		}
		// Binders cast cross-package scalars (`shared.ID(r.PathValue("id"))`).
		fieldImports := collectRequestFieldImports(m, pkg, r)
		for _, alias := range slices.Sorted(maps.Keys(fieldImports)) {
			addExtra(extraImport{Alias: alias, Path: fieldImports[alias]})
		}
		var err error
		d.PathParams, d.QueryParams, d.HeaderParams, d.CookieParams, err = collectBindings(m, pkg, d.RequestPkgAlias, r)
		if err != nil {
			return transportData{}, err
		}
		d.BodyDecode = wire.IsBodyVerb(m.Verb) && hasUnboundField(m, pkg, r)
		forms, files, ferr := collectFormBindings(m, pkg, d.RequestPkgAlias, r)
		if ferr != nil {
			return d, ferr
		}
		if len(files) > 0 {
			d.IsMultipart = true
			d.FormStrings = forms
			d.FormFiles = files
			// The multipart parser owns the body.
			d.BodyDecode = false
			const stdlibDefault int64 = 32 << 20
			d.MultipartMaxMemory = stdlibDefault
			if n := sizeDecoratorArg(m.Decorators, "maxBodySize"); n > stdlibDefault {
				d.MultipartMaxMemory = n
			}
		}
		d.Defaults = collectDefaults(m, pkg, d.RequestPkgAlias, r)
	}
	// On a raw response side logic writes its own headers.
	if d.WriteResponse && mode.HasResponse {
		var respStrconv bool
		d.RespHeaders, d.RespCookies, respStrconv = collectResponseBindings(m, pkg, r)
		if respStrconv {
			d.NeedsStrconv = true
		}
	}
	if mode.StubReturnsResp() {
		// The handler infers resp's type, so respRef only feeds the signature and adds no import.
		alias, bare, _, _ := resolveTypeRef(m.Response.Type, crossPkg)
		respRef = alias + "." + bare
	}
	d.Sig = buildSignature(mode, reqRef, respRef)
	d.SuccessStatus = wire.SuccessStatus(m)
	d.SuccessStatusExpr = statusConstExpr(d.SuccessStatus)
	return d, nil
}

// statusConstExpr spells a 2xx code as its net/http constant and any other code as a literal.
func statusConstExpr(code int) string {
	switch code {
	case http.StatusOK:
		return "http.StatusOK"
	case http.StatusCreated:
		return "http.StatusCreated"
	case http.StatusAccepted:
		return "http.StatusAccepted"
	case http.StatusNonAuthoritativeInfo:
		return "http.StatusNonAuthoritativeInfo"
	case http.StatusNoContent:
		return "http.StatusNoContent"
	case http.StatusResetContent:
		return "http.StatusResetContent"
	case http.StatusPartialContent:
		return "http.StatusPartialContent"
	}
	return strconv.Itoa(code)
}
