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

func addSecuritySchemes(doc *openapi3.T, pkg *semantic.Package, cfg *config.Config) {
	if doc.Components == nil {
		doc.Components = &openapi3.Components{}
	}
	if doc.Components.SecuritySchemes == nil {
		doc.Components.SecuritySchemes = openapi3.SecuritySchemes{}
	}
	collect := func(ds []*ast.Decorator, into map[string]bool) {
		add := func(e ast.Expr) {
			if id, ok := e.(*ast.IdentExpr); ok {
				into[id.Name.String()] = true
			}
		}
		for _, d := range ds {
			if d.Name != "security" {
				continue
			}
			for _, a := range d.Args {
				// An arg is either a bare scheme ident OR an array of them
				// (`@security([A, B])`); DecoratorArgValues flattens both.
				for _, v := range ast.DecoratorArgValues(a) {
					add(v)
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
		doc.Components.SecuritySchemes[n] = &openapi3.SecuritySchemeRef{Value: securitySchemeFor(n, cfg)}
	}
}

// securitySchemeFor builds the OpenAPI security scheme for a referenced
// scheme name from the manifest's `openapi.securitySchemes` declaration
// (type / scheme / bearerFormat / in / name / openIdConnectUrl). It falls
// back to the legacy http/bearer/JWT default only when the manifest
// declares no schemes at all — in that mode ValidateSecurityRefs skips
// reference validation, so every referenced scheme uses the default. When
// schemes ARE declared, a referenced-but-undeclared name is already
// rejected by ValidateSecurityRefs before emission.
func securitySchemeFor(name string, cfg *config.Config) *openapi3.SecurityScheme {
	if cfg != nil {
		if sc, ok := cfg.OpenAPI.SecuritySchemes[name]; ok {
			return &openapi3.SecurityScheme{
				Type:             sc.Type,
				Scheme:           sc.Scheme,
				BearerFormat:     sc.BearerFormat,
				In:               sc.In,
				Name:             sc.Name,
				OpenIdConnectUrl: sc.OpenIDConnectURL,
				Flows:            oauthFlowsFor(sc.Flows),
			}
		}
	}
	return &openapi3.SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "JWT"}
}

// oauthFlowsFor maps the manifest's OAuth2 flow config to the kin-openapi
// model. Returns nil when no flows are configured (non-oauth2 schemes).
func oauthFlowsFor(f *config.OAuthFlows) *openapi3.OAuthFlows {
	if f == nil {
		return nil
	}
	conv := func(fl *config.OAuthFlow) *openapi3.OAuthFlow {
		if fl == nil {
			return nil
		}
		scopes := fl.Scopes
		if scopes == nil {
			scopes = map[string]string{} // OpenAPI requires `scopes` (may be empty)
		}
		return &openapi3.OAuthFlow{
			AuthorizationURL: fl.AuthorizationURL,
			TokenURL:         fl.TokenURL,
			RefreshURL:       fl.RefreshURL,
			Scopes:           scopes,
		}
	}
	return &openapi3.OAuthFlows{
		Implicit:          conv(f.Implicit),
		Password:          conv(f.Password),
		ClientCredentials: conv(f.ClientCredentials),
		AuthorizationCode: conv(f.AuthorizationCode),
	}
}

// ValidateSecurityRefs cross-checks every `@security(scheme)` reference
// in pkg against the manifest's declared `openapi.securitySchemes` map.
// The check is permissive when the manifest declares no schemes: in
// that case we keep the legacy auto-generated bearer behaviour (so
// projects that haven't migrated continue to work). When the manifest
// HAS declared at least one scheme, every reference must resolve to a
// key in that map; unknown references produce a sorted list of error
// strings the caller can format. To express "this endpoint is public"
// use `@ignoreSecurity` at the method level rather than a sentinel
// scheme name.
func ValidateSecurityRefs(pkg *semantic.Package, cfg *config.Config) []string {
	if cfg == nil || len(cfg.OpenAPI.SecuritySchemes) == 0 {
		return nil
	}
	declared := cfg.OpenAPI.SecuritySchemes
	// An oauth2 scheme without a `flows` object (with at least one flow) emits
	// an OpenAPI document that violates the spec and crashes downstream client
	// generators. Reject it here with a clear message instead.
	var schemeErrs []string
	for name, sc := range declared {
		if sc.Type == "oauth2" && !sc.Flows.HasFlow() {
			schemeErrs = append(schemeErrs, fmt.Sprintf("securityScheme %q is type oauth2 but declares no flows: add an openapi.securitySchemes.%s.flows entry (implicit / password / clientCredentials / authorizationCode) — an oauth2 scheme without flows is invalid OpenAPI", name, name))
		}
	}
	collect := func(svcName, scope string, ds []*ast.Decorator, dst map[string]bool) {
		check := func(e ast.Expr) {
			id, ok := e.(*ast.IdentExpr)
			if !ok {
				return
			}
			name := id.Name.String()
			if _, exists := declared[name]; exists {
				return
			}
			dst[svcName+"/"+scope+"/"+name] = true
		}
		for _, d := range ds {
			if d.Name != "security" {
				continue
			}
			for _, a := range d.Args {
				// Bare-ident and array (`@security([A,B])`) forms both flatten
				// through DecoratorArgValues.
				for _, v := range ast.DecoratorArgValues(a) {
					check(v)
				}
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
	out := make([]string, 0, len(bad)+len(schemeErrs))
	for k := range bad {
		parts := strings.SplitN(k, "/", 3)
		// parts: svc, scope, name
		out = append(out, fmt.Sprintf("@security(%s) on %s %s: scheme %q is not declared in openapi.securitySchemes", parts[2], parts[1], parts[0], parts[2]))
	}
	out = append(out, schemeErrs...)
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
