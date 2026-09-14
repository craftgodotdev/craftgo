// OpenAPI security scheme components emission + manifest scheme validation.
package docs

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// forEachSecurityScheme calls fn with every scheme name referenced by an
// `@security(...)` decorator in ds (bare `@security(A)` and the array shortcut
// `@security([A, B])` both flatten through DecoratorArgValues).
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

// securitySchemeFor builds the OpenAPI security scheme for a referenced
// scheme name from the manifest's `openapi.securitySchemes` declaration
// (type / scheme / bearerFormat / in / name / openIdConnectUrl). It falls
// back to the legacy http/bearer/JWT default only when the manifest
// declares no schemes at all - in that mode ValidateSecurityRefs skips
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

// ValidateSecuritySchemes checks the manifest's declared
// `openapi.securitySchemes` definitions. An oauth2 scheme without a
// `flows` object (with at least one flow) emits an OpenAPI document that
// violates the spec and crashes downstream client generators, so it is
// rejected with a clear message. `@security(...)` references are resolved
// against the same declared set by the semantic analyser.
func ValidateSecuritySchemes(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	var out []string
	for _, name := range sortedKeys(cfg.OpenAPI.SecuritySchemes) {
		if sc := cfg.OpenAPI.SecuritySchemes[name]; sc.Type == "oauth2" && !sc.Flows.HasFlow() {
			out = append(out, fmt.Sprintf("securityScheme %q is type oauth2 but declares no flows: add an openapi.securitySchemes.%s.flows entry (implicit / password / clientCredentials / authorizationCode) - an oauth2 scheme without flows is invalid OpenAPI", name, name))
		}
	}
	return out
}

// dedupSecurity removes duplicate security requirements (identical
// scheme→scopes sets) that arise when a method repeats a requirement its
// service already declares. Each requirement is an OR-alternative, so two
// identical entries are redundant; mirrors the tag dedup so the spec
// carries one entry per distinct alternative.
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

// securityFromDecorators turns `@security(SchemeA, SchemeB)` declarations
// on a method or service into the OpenAPI `security` slice. Each
// decorator argument that is an identifier becomes one entry whose value
// is an empty scopes list - multi-scheme arguments inside a single
// decorator are AND-combined; multiple `@security(...)` decorators are
// OR-combined per the OpenAPI spec semantics. The array-shortcut form
// `@security([A, B])` is treated as equivalent to `@security(A, B)`. To
// opt out of inherited service-level security, use `@ignoreSecurity` at
// the method level instead of a sentinel scheme name.
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
