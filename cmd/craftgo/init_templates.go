package main

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

// initTemplatesFS holds the starter files emitted by `craftgo init`:
// the project manifest, and nothing else - the runtime scaffolds are
// written by `craftgo gen` under its gen-once policy. Keeping the
// bodies in real `.tmpl` files rather than Go string literals makes
// them editable without touching Go source.
//
//go:embed templates/*.tmpl
var initTemplatesFS embed.FS

// renderInitTemplate parses the named template under templates/ and
// renders it with data. Errors carry the template name so a typo in
// the embed path surfaces clearly at startup, not at the call site.
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
