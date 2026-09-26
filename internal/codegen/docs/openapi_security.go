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
		for _, n := range ast.ArgNames(d) {
			fn(n.Value)
		}
	}
}

// usedSecuritySchemes returns the name of each scheme an `@security` of pkg's
// services and methods lists: the schemes its document writes.
func usedSecuritySchemes(pkg *semantic.Package) map[string]bool {
	names := map[string]bool{}
	collect := func(ds []*ast.Decorator) {
		forEachSecurityScheme(ds, func(name string) { names[name] = true })
	}
	for _, svc := range pkg.Services {
		if svc.Primary != nil {
			collect(svc.Primary.Decorators)
		}
		for _, m := range svc.Methods {
			collect(svc.Decorators(m))
		}
	}
	return names
}

func addSecuritySchemes(doc *openapi3.T, pkg *semantic.Package, cfg *config.Config) {
	if doc.Components == nil {
		doc.Components = &openapi3.Components{}
	}
	if doc.Components.SecuritySchemes == nil {
		doc.Components.SecuritySchemes = openapi3.SecuritySchemes{}
	}
	for n := range usedSecuritySchemes(pkg) {
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

// securitySchemeTypes are the scheme types OpenAPI defines.
const securitySchemeTypes = "apiKey, http, mutualTLS, oauth2 or openIdConnect"

// validateSecuritySchemes returns, of the manifest schemes named in used, a
// message per scheme without a type OpenAPI defines, per field its type
// requires that it lacks or holds a value the type does not admit, and per URL
// an oauth2 flow's grant requires that it lacks.
func validateSecuritySchemes(cfg *config.Config, used map[string]bool) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(used)) {
		sc, declared := cfg.OpenAPI.SecuritySchemes[name]
		if !declared {
			continue
		}
		missing := func(field, hint string) {
			out = append(out, fmt.Sprintf("securityScheme %q is type %s but has no %s: add openapi.securitySchemes.%s.%s%s - OpenAPI requires it of an %s scheme", name, sc.Type, field, name, field, hint, sc.Type))
		}
		switch sc.Type {
		case "":
			out = append(out, fmt.Sprintf("securityScheme %q has no type: set openapi.securitySchemes.%s.type to %s", name, name, securitySchemeTypes))
		case "http":
			if sc.Scheme == "" {
				missing("scheme", ", the HTTP authentication scheme (bearer, basic, ...)")
			}
		case "apiKey":
			switch sc.In {
			case "":
				missing("in", ", where the key rides (header, query or cookie)")
			case "header", "query", "cookie":
			default:
				out = append(out, fmt.Sprintf("securityScheme %q: in %q is not header, query or cookie - set openapi.securitySchemes.%s.in to where the key rides", name, sc.In, name))
			}
			if sc.Name == "" {
				missing("name", ", the header, query or cookie name that carries the key")
			}
		case "openIdConnect":
			if sc.OpenIDConnectURL == "" {
				missing("openIdConnectUrl", ", its OpenID Connect discovery URL")
			}
		case "oauth2":
			out = append(out, oauth2FlowErrors(name, sc.Flows)...)
		case "mutualTLS":
		default:
			out = append(out, fmt.Sprintf("securityScheme %q: type %q is not an OpenAPI security scheme type - use %s", name, sc.Type, securitySchemeTypes))
		}
	}
	return out
}

// oauth2FlowErrors returns a message when the oauth2 scheme name declares no
// flow, else one per URL OpenAPI requires of a flow's grant that it lacks.
func oauth2FlowErrors(name string, flows *config.OAuthFlows) []string {
	if !flows.HasFlow() {
		return []string{fmt.Sprintf("securityScheme %q is type oauth2 but declares no flows: add an openapi.securitySchemes.%s.flows entry (implicit / password / clientCredentials / authorizationCode) - an oauth2 scheme without flows is invalid OpenAPI", name, name)}
	}
	var out []string
	for _, grant := range []struct {
		flow                                 string
		f                                    *config.OAuthFlow
		needsAuthorizationURL, needsTokenURL bool
	}{
		{"implicit", flows.Implicit, true, false},
		{"password", flows.Password, false, true},
		{"clientCredentials", flows.ClientCredentials, false, true},
		{"authorizationCode", flows.AuthorizationCode, true, true},
	} {
		if grant.f == nil {
			continue
		}
		missing := func(key string) {
			out = append(out, fmt.Sprintf("securityScheme %q: flow %s has no %s: add openapi.securitySchemes.%s.flows.%s.%s - OpenAPI requires it of the %s flow", name, grant.flow, key, name, grant.flow, key, grant.flow))
		}
		if grant.needsAuthorizationURL && grant.f.AuthorizationURL == "" {
			missing("authorizationUrl")
		}
		if grant.needsTokenURL && grant.f.TokenURL == "" {
			missing("tokenUrl")
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
		for _, n := range ast.ArgNames(d) {
			req[n.Value] = []string{}
		}
		reqs = append(reqs, req)
	}
	if len(reqs) == 0 {
		return nil
	}
	return &reqs
}
