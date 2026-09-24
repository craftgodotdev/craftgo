package docs

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// forEachSecurityScheme calls fn with each scheme an `@security` in ds names,
// the `@security([A, B])` form included.
func forEachSecurityScheme(ds []*ast.Decorator, fn func(name string)) {
	for _, d := range ds {
		if d == nil || d.Name != "security" {
			continue
		}
		for _, a := range d.Args {
			for _, v := range ast.DecoratorArgValues(a) {
				if id, ok := v.(*ast.IdentExpr); ok {
					fn(id.Name.String())
				}
			}
		}
	}
}

func addSecuritySchemes(doc *openapi3.T, pkg *semantic.Package, cfg *config.Config) {
	if doc.Components == nil {
		doc.Components = &openapi3.Components{}
	}
	if doc.Components.SecuritySchemes == nil {
		doc.Components.SecuritySchemes = openapi3.SecuritySchemes{}
	}
	collect := func(ds []*ast.Decorator, into map[string]bool) {
		forEachSecurityScheme(ds, func(name string) { into[name] = true })
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

// securitySchemeFor returns the manifest's scheme called name, or http bearer
// JWT for an undeclared name, which the analyser allows only with no schemes.
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

// oauthFlowsFor converts the manifest's OAuth2 flows, nil when there are none.
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

// ValidateSecuritySchemes returns one message per oauth2 scheme in the
// manifest that declares no flow; OpenAPI requires one.
func ValidateSecuritySchemes(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	var out []string
	for _, name := range slices.Sorted(maps.Keys(cfg.OpenAPI.SecuritySchemes)) {
		if sc := cfg.OpenAPI.SecuritySchemes[name]; sc.Type == "oauth2" && !sc.Flows.HasFlow() {
			out = append(out, fmt.Sprintf("securityScheme %q is type oauth2 but declares no flows: add an openapi.securitySchemes.%s.flows entry (implicit / password / clientCredentials / authorizationCode) - an oauth2 scheme without flows is invalid OpenAPI", name, name))
		}
	}
	return out
}

// dedupSecurity drops each requirement equal to an earlier one, such as a
// method repeating its service's.
func dedupSecurity(reqs openapi3.SecurityRequirements) openapi3.SecurityRequirements {
	seen := map[string]bool{}
	out := make(openapi3.SecurityRequirements, 0, len(reqs))
	for _, req := range reqs {
		keys := make([]string, 0, len(req))
		for k, scopes := range req {
			keys = append(keys, k+"="+strings.Join(scopes, ","))
		}
		sort.Strings(keys)
		key := strings.Join(keys, "&")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, req)
	}
	return out
}

// securityFromDecorators returns one requirement per `@security` in ds, or nil:
// a requirement needs all its schemes, and any one requirement is enough.
func securityFromDecorators(ds []*ast.Decorator) *openapi3.SecurityRequirements {
	var reqs openapi3.SecurityRequirements
	for _, d := range ds {
		if d == nil || d.Name != "security" {
			continue
		}
		req := openapi3.SecurityRequirement{}
		for _, a := range d.Args {
			for _, v := range ast.DecoratorArgValues(a) {
				if id, ok := v.(*ast.IdentExpr); ok {
					req[id.Name.String()] = []string{}
				}
			}
		}
		reqs = append(reqs, req)
	}
	if len(reqs) == 0 {
		return nil
	}
	return &reqs
}
