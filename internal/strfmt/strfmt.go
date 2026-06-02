// Package strfmt holds the canonical catalogue of named string formats the
// `@format` decorator accepts. It is a leaf below both the analyser (which
// rejects an unknown `@format` name) and codegen (which emits a per-format
// runtime check and an OpenAPI `format` keyword), so the set of legal formats
// is decided in exactly ONE place. Without this the legality list and the
// validator catalogue lived in two packages: a name added to one but not the
// other made the editor accept a `@format` whose field then got no runtime
// check while OpenAPI still advertised it.
//
// (Named `strfmt`, not `formats`, to stay clearly distinct from the
// `internal/format` printer package that renders DSL source.)
package strfmt

// Names is the canonical list of `@format` values, in documentation order
// (README §"Decorators by level"). The analyser uses it as the `@format`
// argument enum; codegen must provide a validator for every entry.
var Names = []string{
	"email", "url", "uri", "uuid", "datetime", "date", "time",
	"phone", "hostname", "ipv4", "ipv6", "cidr", "mac",
	"creditcard", "base64", "base64url", "hexcolor", "json",
}
