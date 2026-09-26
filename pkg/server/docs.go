package server

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
)

// DocsUI names an API-reference UI [Server.ServeDocs] can render; [DocsOptions] takes it
// as a string.
//
// Deprecated: set [DocsOptions.UI] to "redoc", "swagger" or "scalar".
type DocsUI string

// DocsRedoc, DocsSwagger and DocsScalar are the [DocsUI] values.
//
// Deprecated: set [DocsOptions.UI] to the name itself.
const (
	DocsRedoc   DocsUI = "redoc"
	DocsSwagger DocsUI = "swagger"
	DocsScalar  DocsUI = "scalar"
)

// DocsOptions configures [Server.ServeDocs].
type DocsOptions struct {
	// Spec is the OpenAPI document, served verbatim: as JSON if it starts with `{`, else as YAML.
	Spec []byte
	// UI is "redoc" (default), "swagger" or "scalar", in any case; other values mean Redoc.
	UI string
	// Path is the route for the HTML docs page (default "/docs").
	Path string
	// SpecPath is the route serving the raw spec (default "/openapi.yaml").
	SpecPath string
	// Title is the page <title> (default "API Reference").
	Title string
}

// ServeDocs registers GET SpecPath, serving Spec, and GET Path, a page that loads the chosen
// UI from a CDN; it registers nothing when Spec is empty.
func (s *Server) ServeDocs(opts DocsOptions) *Server {
	if len(opts.Spec) == 0 {
		return s
	}
	if opts.Path == "" {
		opts.Path = "/docs"
	}
	if opts.SpecPath == "" {
		opts.SpecPath = "/openapi.yaml"
	}
	if opts.Title == "" {
		opts.Title = "API Reference"
	}

	specCT := "application/yaml; charset=utf-8"
	if t := strings.TrimSpace(string(opts.Spec)); strings.HasPrefix(t, "{") {
		specCT = contentTypeJSON
	}
	spec := opts.Spec
	s.HandleFunc("GET "+opts.SpecPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", specCT)
		_, _ = w.Write(spec)
	})

	page := []byte(docsHTML(DocsUI(strings.ToLower(opts.UI)), opts.SpecPath, opts.Title))
	s.HandleFunc("GET "+opts.Path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	return s
}

// docsHTML renders the page for ui, with title and specPath escaped for where they land.
func docsHTML(ui DocsUI, specPath, title string) string {
	t := template.HTMLEscapeString(title)
	attr := template.HTMLEscapeString(specPath) // attribute-context value
	js, _ := json.Marshal(specPath)             // safe quoted JS string literal

	switch ui {
	case DocsSwagger:
		return `<!doctype html><html><head><meta charset="utf-8"/>` +
			`<meta name="viewport" content="width=device-width,initial-scale=1"/>` +
			`<title>` + t + `</title>` +
			`<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css"/>` +
			`</head><body><div id="swagger-ui"></div>` +
			`<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>` +
			`<script>window.onload=function(){SwaggerUIBundle({url:` + string(js) + `,dom_id:"#swagger-ui"});};</script>` +
			`</body></html>`
	case DocsScalar:
		return `<!doctype html><html><head><meta charset="utf-8"/>` +
			`<meta name="viewport" content="width=device-width,initial-scale=1"/>` +
			`<title>` + t + `</title></head><body>` +
			`<script id="api-reference" data-url="` + attr + `"></script>` +
			`<script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>` +
			`</body></html>`
	default: // Redoc
		return `<!doctype html><html><head><meta charset="utf-8"/>` +
			`<meta name="viewport" content="width=device-width,initial-scale=1"/>` +
			`<title>` + t + `</title></head><body>` +
			`<redoc spec-url="` + attr + `"></redoc>` +
			`<script src="https://cdn.jsdelivr.net/npm/redoc@2/bundles/redoc.standalone.js"></script>` +
			`</body></html>`
	}
}
