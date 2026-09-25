package golang

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// escapeErrorf makes s safe inside a generated fmt.Errorf format literal: Go
// string escapes, and `%` doubled.
func escapeErrorf(s string) string {
	q := strconv.Quote(s)
	q = q[1 : len(q)-1]
	return strings.ReplaceAll(q, "%", "%%")
}

// errorf renders the fmt.Errorf call of a validation error, "<subject>: text",
// or text alone for the subject-less error of a scalar's or enum's own
// Validate().
func errorf(subject, text string, ctx emitCtx) string {
	ctx.uses["fmt"] = true
	if subject != "" {
		text = subject + ": " + text
	}
	return `fmt.Errorf("` + escapeErrorf(text) + `")`
}

// failIf renders `if cond { return err }`, err the [errorf] of subject and text.
func failIf(cond, subject, text string, ctx emitCtx) string {
	return fmt.Sprintf("if %s {\n\treturn %s\n}", cond, errorf(subject, text, ctx))
}

// guardBlock renders body inside `if access != nil { ... }`.
func guardBlock(access, body string) string {
	return fmt.Sprintf("if %s != nil {\n%s\n}", access, body)
}

// wireName returns the name f travels under with binding b: its parameter
// name off the body, else its JSON key.
func wireName(f *ast.Field, b wire.Binding) string {
	if b.IsParam() {
		return wire.WireName(f, b)
	}
	return wire.JSONName(f)
}

// subject returns the name f's validation messages carry: the parameter its
// auto-binding reads it from, else the name its own binding gives it.
func (ctx emitCtx) subject(f *ast.Field) string {
	b, auto := ctx.autoBound[f.Pos]
	if !auto {
		b = wire.ExplicitBinding(f)
	}
	return wireName(f, b)
}

// autoBindings returns, by declaration, the binding of each field that every
// request reading it auto-binds to a path or query parameter and no JSON value
// carries.
func autoBindings(proj *semantic.Project) map[ast.Pos]wire.Binding {
	auto := map[ast.Pos]wire.Binding{}
	fromJSON := map[ast.Pos]bool{}
	for _, name := range proj.PackageNames() {
		pkg, res := proj.Packages[name], semantic.NewResolver(proj, name)
		for _, svcName := range pkg.ServiceNames() {
			for _, m := range pkg.Services[svcName].Methods {
				for _, rf := range semantic.RequestFields(m, pkg, res, nil) {
					switch {
					case rf.AutoBound:
						auto[rf.Field.Pos] = rf.Binding
					case rf.OnWireBody:
						fromJSON[rf.Field.Pos] = true
					}
				}
			}
		}
	}
	for td := range jsonTypes(proj) {
		for _, f := range ast.Fields(td.Body) {
			fromJSON[f.Pos] = true
		}
	}
	for pos := range fromJSON {
		delete(auto, pos)
	}
	return auto
}

// jsonTypes returns the declared types whose fields a JSON value carries: the
// types a field, a generic argument, a response, an event payload or an error
// body names, and the types those mix in. A method's request type is a JSON
// value only through such a use.
func jsonTypes(proj *semantic.Project) map[*ast.TypeDecl]bool {
	out := map[*ast.TypeDecl]bool{}
	var add func(home string, n *ast.NamedTypeRef)
	add = func(home string, n *ast.NamedTypeRef) {
		pkgName, sym := home, n.Name.String()
		if parts := n.Name.Parts; len(parts) == 2 {
			pkgName, sym = parts[0], parts[1]
		}
		pkg := proj.Packages[pkgName]
		if pkg == nil || pkg.Types[sym] == nil || out[pkg.Types[sym]] {
			return
		}
		td := pkg.Types[sym]
		out[td] = true
		for _, m := range td.Body {
			if mx, ok := m.(*ast.Mixin); ok {
				mx.Ref.WalkNamedRefs(func(n *ast.NamedTypeRef) { add(pkgName, n) })
			}
		}
	}
	for _, name := range proj.PackageNames() {
		pkg := proj.Packages[name]
		value := func(n *ast.NamedTypeRef) { add(name, n) }
		for _, td := range pkg.Types {
			for _, m := range td.Body {
				switch v := m.(type) {
				case *ast.Field:
					v.Type.WalkNamedRefs(value)
				case *ast.Mixin:
					for _, a := range v.Ref.Args {
						a.WalkNamedRefs(value)
					}
				}
			}
		}
		for _, ed := range pkg.Errors {
			for _, m := range ed.Body {
				switch v := m.(type) {
				case *ast.Field:
					v.Type.WalkNamedRefs(value)
				case *ast.Mixin:
					v.Ref.WalkNamedRefs(value)
				}
			}
		}
		for _, svcName := range pkg.ServiceNames() {
			for _, m := range pkg.Services[svcName].Methods {
				if m.Request != nil {
					for _, a := range m.Request.Args {
						a.WalkNamedRefs(value)
					}
				}
				if m.Response != nil {
					m.Response.Type.WalkNamedRefs(value)
				}
			}
		}
		for _, ev := range pkg.Events {
			if ev.Payload != nil {
				ev.Payload.Type.WalkNamedRefs(value)
			}
		}
	}
	return out
}
