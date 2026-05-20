// OpenAPI security scheme components emission + manifest cross-check.
package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func addSecuritySchemes(doc *openapi3.T, pkg *semantic.Package) {
	if doc.Components == nil {
		doc.Components = &openapi3.Components{}
	}
	if doc.Components.SecuritySchemes == nil {
		doc.Components.SecuritySchemes = openapi3.SecuritySchemes{}
	}
	collect := func(ds []*ast.Decorator, into map[string]bool) {
		for _, d := range ds {
			if d.Name != "security" {
				continue
			}
			for _, a := range d.Args {
				if id, ok := a.Value.(*ast.IdentExpr); ok {
					n := id.Name.String()
					if n != "noauth" {
						into[n] = true
					}
				}
			}
		}
	}
	names := map[string]bool{}
	for _, svc := range pkg.Services {
		if svc.Primary != nil {
			collect(svc.Primary.Decorators, names)
		}
		for _, m := range svc.Methods {
			collect(m.Decorators, names)
		}
	}
	for n := range names {
		if _, exists := doc.Components.SecuritySchemes[n]; exists {
			continue
		}
		doc.Components.SecuritySchemes[n] = &openapi3.SecuritySchemeRef{Value: &openapi3.SecurityScheme{
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "JWT",
		}}
	}
}

// ValidateSecurityRefs cross-checks every `@security(scheme)` reference
// in pkg against the manifest's declared `openapi.securitySchemes` map.
// The check is permissive when the manifest declares no schemes: in
// that case we keep the legacy auto-generated bearer behaviour (so
// projects that haven't migrated continue to work). When the manifest
// HAS declared at least one scheme, every reference must resolve to a
// key in that map; unknown references produce a sorted list of error
// strings the caller can format.
//
// The special name `noauth` is always accepted - it is a marker, not a
// scheme reference.
func ValidateSecurityRefs(pkg *semantic.Package, cfg *config.Config) []string {
	if cfg == nil || len(cfg.OpenAPI.SecuritySchemes) == 0 {
		return nil
	}
	declared := cfg.OpenAPI.SecuritySchemes
	collect := func(svcName, scope string, ds []*ast.Decorator, dst map[string]bool) {
		for _, d := range ds {
			if d.Name != "security" {
				continue
			}
			for _, a := range d.Args {
				id, ok := a.Value.(*ast.IdentExpr)
				if !ok {
					continue
				}
				name := id.Name.String()
				if name == "noauth" {
					continue
				}
				if _, exists := declared[name]; exists {
					continue
				}
				key := svcName + "/" + scope + "/" + name
				dst[key] = true
			}
		}
	}
	bad := map[string]bool{}
	for svcName, svc := range pkg.Services {
		if svc.Primary != nil {
			collect(svcName, "service", svc.Primary.Decorators, bad)
		}
		for _, m := range svc.Methods {
			collect(svcName, "method "+m.Name, m.Decorators, bad)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	out := make([]string, 0, len(bad))
	for k := range bad {
		parts := strings.SplitN(k, "/", 3)
		// parts: svc, scope, name
		out = append(out, fmt.Sprintf("@security(%s) on %s %s: scheme %q is not declared in openapi.securitySchemes", parts[2], parts[1], parts[0], parts[2]))
	}
	sort.Strings(out)
	return out
}
