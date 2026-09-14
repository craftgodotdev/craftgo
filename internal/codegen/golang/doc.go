// Package golang is craftgo's Go target: it turns a validated
// [semantic.Project] into Go source - types, validators, HTTP handlers,
// routes, service stubs, the runtime scaffolds and the event library.
//
// It is one target among the packages under [codegen], alongside the
// OpenAPI projection. The targets share nothing with each other: what
// they all read is the language-independent model in [semantic] and the
// leaf catalogues below it.
package golang
