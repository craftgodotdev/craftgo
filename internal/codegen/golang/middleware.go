package golang

import (
	"maps"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// middlewareData is the template input for middleware.tmpl, and one middleware of
// middleware-fields.tmpl.
type middlewareData struct {
	Name string
	// Doc heads the doc comment of the middleware's declarations ([docHead]).
	Doc []string
}

// middlewareFieldsData is the template input for middleware-fields.tmpl.
type middlewareFieldsData struct {
	Middlewares []middlewareData
}

// generateProjectMiddlewares writes middlewares.go beside output.svccontext, one Middlewares
// field per declared middleware, and each middleware's gen-once scaffold under output.middleware.
func generateProjectMiddlewares(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	mws := projectMiddlewares(proj)
	if err := writeMiddlewareFields(cfg, projectRoot, mws); err != nil {
		return err
	}
	return writeProjectMiddlewareImpls(cfg, projectRoot, mws)
}

// projectMiddlewares returns every middleware the project declares, in name order; a name is
// unique across the project.
func projectMiddlewares(proj *semantic.Project) []middlewareData {
	byName := map[string]middlewareData{}
	for _, pkg := range proj.Packages {
		if pkg == nil {
			continue
		}
		for name, md := range pkg.Middlewares {
			byName[name] = middlewareData{Name: name, Doc: docHead(semantic.DescriptionLines(md.Decorators, md.Doc))}
		}
	}
	out := make([]middlewareData, 0, len(byName))
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		out = append(out, byName[name])
	}
	return out
}

// writeProjectMiddlewareImpls writes each middleware's scaffold unless its file exists.
func writeProjectMiddlewareImpls(cfg *config.Config, projectRoot string, mws []middlewareData) error {
	dir := outputsOf(cfg).middleware
	for _, mw := range mws {
		filename := idents.FileNameWords(cfg.Output.FileCase, append(idents.SplitFieldName(mw.Name), "middleware")) + ".go"
		if err := writeGoOnce(dir.at(projectRoot, filename), tmpl("middleware.tmpl"), mw); err != nil {
			return err
		}
	}
	return nil
}

// writeMiddlewareFields writes middlewares.go even with no middleware, since svccontext.go embeds
// its Middlewares type.
func writeMiddlewareFields(cfg *config.Config, projectRoot string, mws []middlewareData) error {
	return writeGo(outputsOf(cfg).svccontext.at(projectRoot, "middlewares.go"), tmpl("middleware-fields.tmpl"), middlewareFieldsData{Middlewares: mws})
}
