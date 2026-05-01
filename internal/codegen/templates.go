package codegen

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"text/template"
)

// builtinTemplates embeds every codegen template shipped with the binary.
// Templates are looked up exclusively here — there is no project-local
// override mechanism. Projects that need custom shapes fork the
// repository and edit the .tmpl files directly.
//
//go:embed templates/*.tmpl
var builtinTemplates embed.FS

// tmpl loads a single named template from [builtinTemplates]. Templates
// are parsed lazily on first use so test failures are fail-fast and a
// missing template panics with a clear name rather than at startup.
func tmpl(name string) *template.Template {
	t, err := template.ParseFS(builtinTemplates, "templates/"+name)
	if err != nil {
		panic(fmt.Sprintf("codegen: parse %s: %v", name, err))
	}
	return t
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
