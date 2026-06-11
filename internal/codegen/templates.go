package codegen

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"sync"
	"text/template"
)

// builtinTemplates embeds every codegen template shipped with the binary.
// Templates are looked up exclusively here - there is no project-local
// override mechanism. Projects that need custom shapes fork the
// repository and edit the .tmpl files directly.
//
//go:embed templates/*.tmpl
var builtinTemplates embed.FS

// tmplCache memoizes parsed templates by name. The embedded sources are
// immutable for the process lifetime and an executed *template.Template is
// safe for concurrent use, so each template parses exactly once no matter how
// many services / methods render through it.
var tmplCache sync.Map // name → *template.Template

// tmpl loads a single named template from [builtinTemplates], parsing it on
// first use and serving the cached parse afterwards. Lazy parsing keeps a
// missing/broken template failing at its first render with a clear name
// rather than at process start.
func tmpl(name string) *template.Template {
	if t, ok := tmplCache.Load(name); ok {
		return t.(*template.Template)
	}
	t, err := template.ParseFS(builtinTemplates, "templates/"+name)
	if err != nil {
		panic(fmt.Sprintf("codegen: parse %s: %v", name, err))
	}
	actual, _ := tmplCache.LoadOrStore(name, t)
	return actual.(*template.Template)
}

// renderGo executes tmpl with data, then runs `go/format.Source` over the
// result. Returns the formatted bytes ready to be written to disk.
func renderGo(tmpl *template.Template, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template %q: %w", tmpl.Name(), err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w\n--- source ---\n%s", err, buf.String())
	}
	return formatted, nil
}
