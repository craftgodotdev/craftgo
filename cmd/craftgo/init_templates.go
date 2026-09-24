package main

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

// initTemplatesFS holds the templates of the files `craftgo init` writes.
//
//go:embed templates/*.tmpl
var initTemplatesFS embed.FS

// renderInitTemplate renders templates/<name> with data, panicking when the
// template is missing or broken.
func renderInitTemplate(name string, data any) string {
	body, err := initTemplatesFS.ReadFile("templates/" + name)
	if err != nil {
		panic(fmt.Sprintf("craftgo init: template %q not embedded - check the //go:embed pattern in init_templates.go: %v", name, err))
	}
	tmpl, err := template.New(name).Parse(string(body))
	if err != nil {
		panic(fmt.Sprintf("craftgo init: template %q failed to parse: %v", name, err))
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("craftgo init: template %q failed to execute: %v", name, err))
	}
	return buf.String()
}
