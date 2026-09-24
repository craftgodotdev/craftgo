package golang

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"sync"
	"text/template"
)

// builtinTemplates embeds every codegen template.
//
//go:embed templates/*.tmpl
var builtinTemplates embed.FS

// tmplCache holds each parsed template by name; a parsed template is safe for concurrent execution.
var tmplCache sync.Map // name → *template.Template

// tmpl returns the named template, parsing it on first use; a missing or broken template panics.
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

// renderGo executes tmpl with data and gofmts the result.
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
