package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"text/template"

	"github.com/craftgodotdev/craftgo/internal/config"
)

// runtimeData is the shared template input for the runtime-scaffold
// templates (config.go, config.yaml, example.config.yaml,
// svccontext.go). Every template that needs the project's import path
// or operation name reads from these two fields, so the build sites
// stay consistent.
type runtimeData struct {
	Package       string
	OperationName string
}

// GenerateRuntimeConfig scaffolds the project's `config/` package
// (`config.go` + `config.yaml` + `example.config.yaml`) under
// `cfg.Output.Config`. Every file is gen-once: written when missing,
// left untouched when present. main.go reads `<Config>/config.yaml`
// at boot and hands the loaded `*config.Config` to
// `svccontext.NewServiceContext`.
//
// Skipped when `cfg.Output.Main == "-"` - projects opting out of the
// generated main.go (test fixtures, library-style modules) don't need
// the runtime config package; emitting it would only add a stray
// import and force the module to track yaml.v3.
//
// The template body lives in `internal/codegen/templates/`. Edit
// those files to change the shape of the scaffolded artefact -
// per-project overrides are out of scope here (the runtime config
// is meant to be edited freely after the first gen).
func GenerateRuntimeConfig(cfg *config.Config, projectRoot string) error {
	if cfg.Output.Main == "-" {
		return nil
	}
	dir := filepath.Join(projectRoot, cfg.Output.Config)
	data := runtimeData{
		Package:       cfg.Package,
		OperationName: operationNameFor(cfg.Package),
	}
	files := []struct {
		name     string
		template string
		formatGo bool
	}{
		{"config.go", "config.go.tmpl", true},
		{"config.yaml", "config.yaml.tmpl", false},
		{"example.config.yaml", "example.config.yaml.tmpl", false},
	}
	for _, f := range files {
		dest := filepath.Join(dir, f.name)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		body, err := renderRuntimeTemplate(f.template, data, f.formatGo)
		if err != nil {
			return fmt.Errorf("render %s: %w", f.name, err)
		}
		if err := os.WriteFile(dest, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// GenerateSvccontext scaffolds `svccontext.go` at the location pointed
// to by `cfg.Output.Svccontext`. The file accepts a `*config.Config`
// in its constructor and embeds the codegen-managed `Middlewares`
// struct (which is regenerated next to it on every gen run).
//
// Gen-once: existing svccontext.go is left untouched so user-added
// fields (database handles, caches, ...) survive regeneration. The
// adjacent `middlewares.go` IS regenerated; splitting the two keeps
// the auto-managed struct from colliding with hand-edited code.
//
// Skipped when `cfg.Output.Main == "-"` - same rationale as
// [GenerateRuntimeConfig]: opting out of main.go means the project
// doesn't want the framework's runtime scaffolding in its module.
func GenerateSvccontext(cfg *config.Config, projectRoot string) error {
	if cfg.Output.Main == "-" {
		return nil
	}
	dest := filepath.Join(projectRoot, cfg.Output.Svccontext)
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	data := runtimeData{
		Package:       cfg.Package,
		OperationName: operationNameFor(cfg.Package),
	}
	body, err := renderRuntimeTemplate("svccontext.go.tmpl", data, true)
	if err != nil {
		return fmt.Errorf("render svccontext.go: %w", err)
	}
	return os.WriteFile(dest, body, 0o644)
}

// renderRuntimeTemplate executes the named template against data.
// When formatGo is true the result is run through `go/format.Source`
// so the produced .go file is canonically formatted. YAML templates
// pass through as-is - gofmt would corrupt them.
func renderRuntimeTemplate(name string, data any, formatGo bool) ([]byte, error) {
	t, err := template.ParseFS(builtinTemplates, "templates/"+name)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute %s: %w", name, err)
	}
	if !formatGo {
		return buf.Bytes(), nil
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format %s: %w\n--- source ---\n%s", name, err, buf.String())
	}
	return formatted, nil
}
