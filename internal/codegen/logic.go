package codegen

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// logicData is the template input for `logic.tmpl`.
type logicData struct {
	Package          string
	Service          string
	Method           string
	LogicName        string
	RequestType      string
	ResponseType     string
	Doc              []string
	HasRequest       bool
	HasResponse      bool
	NeedsTypes       bool
	IsStream         bool
	StreamCtor       string // "SSE" / "NDJSON" — populated for stream methods
	IsRaw            bool
	TypesImport      string
	SvccontextImport string
}

// GenerateLogic scaffolds one `<method>_logic.go` per method per service
// under `<output.logic>/<servicePackage>/`. Unlike the other generators
// this one runs in **scaffold** mode: existing files are left untouched so
// user-written business logic is never overwritten.
func GenerateLogic(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	for _, svcName := range sortedServices(pkg) {
		svc := pkg.Services[svcName]
		if err := generateLogicFor(svcName, svc, pkg, cfg, projectRoot); err != nil {
			return err
		}
	}
	return nil
}

// generateLogicFor emits all per-method logic scaffold files for a single
// service, skipping any that already exist on disk.
func generateLogicFor(svcName string, svc *semantic.ServiceInfo, pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	imps := importPathsFor(cfg, pkg, svcName)
	dir := filepath.Join(projectRoot, cfg.Output.Logic, ServiceDir(svcName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	jsonTpl := tmpl("logic.tmpl")
	streamTpl := tmpl("logic-stream.tmpl")
	rawTpl := tmpl("logic-raw.tmpl")
	rawStreamTpl := tmpl("logic-raw-stream.tmpl")
	for _, m := range svc.Methods {
		filename := kebabCase(m.Name) + "-logic.go"
		fullPath := filepath.Join(dir, filename)
		if _, err := os.Stat(fullPath); err == nil {
			continue
		}
		data := buildLogicData(svcName, m, imps)
		t := jsonTpl
		switch {
		case data.IsRaw && data.IsStream:
			t = rawStreamTpl
		case data.IsStream:
			t = streamTpl
		case data.IsRaw:
			t = rawTpl
		}
		formatted, err := renderGo(t, data)
		if err != nil {
			return fmt.Errorf("render %s: %w", filename, err)
		}
		if err := os.WriteFile(fullPath, formatted, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// buildLogicData populates the logicData struct for one DSL method.
func buildLogicData(svcName string, m *ast.Method, imps importPaths) logicData {
	hasReq := m.Request != nil
	hasResp := m.Response != nil && m.Response.Type != nil
	d := logicData{
		Package:          ServicePackage(svcName),
		Service:          svcName,
		Method:           m.Name,
		LogicName:        m.Name + "Logic",
		Doc:              m.Doc,
		HasRequest:       hasReq,
		HasResponse:      hasResp,
		NeedsTypes:       hasReq || hasResp,
		TypesImport:      imps.Types,
		SvccontextImport: imps.Svccontext,
	}
	if hasReq {
		d.RequestType = m.Request.Name.String()
	}
	if hasResp {
		d.ResponseType = m.Response.Type.Name.String()
	}
	if (m.Response != nil && m.Response.Stream) || hasStreamDecorator(m.Decorators) {
		d.IsStream = true
		d.StreamCtor = streamCtor(streamFormat(m))
	}
	if hasRawDecorator(m.Decorators) {
		d.IsRaw = true
	}
	return d
}
