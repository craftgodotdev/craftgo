package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
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

// ServeDocs is [Server.RegisterDocs] for a chain: it panics where RegisterDocs fails.
func (s *Server) ServeDocs(opts DocsOptions) *Server {
	if err := s.RegisterDocs(opts); err != nil {
		panic(err)
	}
	return s
}

// RegisterDocs registers GET SpecPath, serving Spec, and GET Path, a page that loads the chosen
// UI from a CDN at a pinned version the browser checks against its integrity hash; it registers
// nothing when Spec is empty. It fails, registering neither, when Path or SpecPath does not start
// with "/" or is no path a ServeMux pattern takes, when the two are equal, and when either is a
// health probe's path or a registered route's.
func (s *Server) RegisterDocs(opts DocsOptions) error {
	if len(opts.Spec) == 0 {
		return nil
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
	if err := s.docsPathFree("Path", opts.Path); err != nil {
		return err
	}
	if err := s.docsPathFree("SpecPath", opts.SpecPath); err != nil {
		return err
	}
	if opts.Path == opts.SpecPath {
		return fmt.Errorf("server: docs Path and SpecPath are both %q", opts.Path)
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
	return nil
}

// docsPathFree reports why GET path cannot serve the docs option field: it does not start with
// "/", a ServeMux refuses it, a health probe answers it first, or a registered route has it.
func (s *Server) docsPathFree(field, path string) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("server: docs %s %q does not start with \"/\"", field, path)
	}
	if err := tryRegister(http.NewServeMux(), "GET "+path); err != nil {
		return fmt.Errorf("server: docs %s %q: %w", field, path, err)
	}
	s.mu.Lock()
	probe := !s.noHealth && (path == s.healthPaths.Liveness || path == s.healthPaths.Readiness)
	s.mu.Unlock()
	if probe {
		return fmt.Errorf("server: docs %s %q is a health probe's path", field, path)
	}
	// The pattern that serves a GET of path now is the one registering GET path could conflict with.
	_, taken := s.mux.Handler(&http.Request{Method: http.MethodGet, URL: &url.URL{Path: path}})
	if taken != "" && tryRegister(http.NewServeMux(), taken, "GET "+path) != nil {
		return fmt.Errorf("server: docs %s %q is the path of the route %q", field, path, taken)
	}
	return nil
}

// tryRegister registers patterns on m in order, returning the error ServeMux panics with on an
// invalid or conflicting one.
func tryRegister(m *http.ServeMux, patterns ...string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	for _, p := range patterns {
		m.Handle(p, http.NotFoundHandler())
	}
	return nil
}

// The UI assets, each at a pinned version with its Subresource Integrity hash: a browser refuses a
// file whose bytes differ from the ones hashed.
const (
	redocScript   = `<script src="https://cdn.jsdelivr.net/npm/redoc@2.5.4/bundles/redoc.standalone.js" integrity="sha384-w447zOpYfw/1Tv/5AK9NfHTlQIqE3RVR6KY62jCyy9zNDgO64cMwGGP1Fj0zJVf5" crossorigin="anonymous"></script>`
	swaggerStyle  = `<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.33.0/swagger-ui.css" integrity="sha384-Ov4/wv3j2bmct8cDc5X4ngJZohVPzEmc6uDPH8WeljUxO5vtoykvMEfbu9Vh6RaW" crossorigin="anonymous"/>`
	swaggerScript = `<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.33.0/swagger-ui-bundle.js" integrity="sha384-YDALVcy8kj8yltLBVi1vBiBAUqdxvus673gM8XKwiy6aDUJFXivF/KCufekjYbVf" crossorigin="anonymous"></script>`
	scalarScript  = `<script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.71.0/dist/browser/standalone.js" integrity="sha384-I7aSmSxf06vl5HT10vzNOAryO+PFCAVHIGwhZerHn6yM/O0642381S3kw9o7fFQd" crossorigin="anonymous"></script>`
)

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
			swaggerStyle +
			`</head><body><div id="swagger-ui"></div>` +
			swaggerScript +
			`<script>window.onload=function(){SwaggerUIBundle({url:` + string(js) + `,dom_id:"#swagger-ui"});};</script>` +
			`</body></html>`
	case DocsScalar:
		return `<!doctype html><html><head><meta charset="utf-8"/>` +
			`<meta name="viewport" content="width=device-width,initial-scale=1"/>` +
			`<title>` + t + `</title></head><body>` +
			`<script id="api-reference" data-url="` + attr + `"></script>` +
			scalarScript +
			`</body></html>`
	default: // Redoc
		return `<!doctype html><html><head><meta charset="utf-8"/>` +
			`<meta name="viewport" content="width=device-width,initial-scale=1"/>` +
			`<title>` + t + `</title></head><body>` +
			`<redoc spec-url="` + attr + `"></redoc>` +
			redocScript +
			`</body></html>`
	}
}
