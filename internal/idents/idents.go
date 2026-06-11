// Package idents holds the Go-identifier conversion helpers used by
// both the semantic analyser and the codegen pass. Keeping it here
// - instead of inside codegen - lets semantic detect "user_id and
// userId map to the same Go field name" collisions during analysis
// without pulling in the rest of codegen.
package idents

import (
	"strconv"
	"strings"
)

// BuiltinTypes is the closed set of primitive type spellings the DSL
// recognises out of the box. The semantic resolver, codegen, and the
// parser's disambiguation rules all consult it, so the table lives
// here in a transport-neutral package - adding a new primitive
// happens once, every consumer picks it up. `object` is the
// permissive bag-of-fields used inside `@example({...})`; `file` is
// the upload-only marker that codegen maps to `*multipart.FileHeader`.
//
// Prefer the [IsBuiltin] / [IsWireParseable] helpers over reading
// this map directly so the predicates stay consistent across every
// call site.
var BuiltinTypes = map[string]bool{
	"string": true, "bool": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
	"bytes":  true,
	"any":    true,
	"object": true,
	"file":   true,
}

// IsBuiltin reports whether name is one of the DSL's built-in type
// spellings (every entry in [BuiltinTypes]). Use this in code that
// has to differentiate "user-declared type" from "builtin" - codegen
// import resolution and semantic ref classification both reach for it.
func IsBuiltin(name string) bool { return BuiltinTypes[name] }

// IsWireParseable reports whether name is a primitive the wire-string
// binders (`@query`, `@header`, `@cookie`, `@form`) can parse from a
// single string. Excludes `bytes` / `any` / `object` / `file` -
// those need their own wire format. Mirrors the codegen's
// `queryPrims` table so semantic-time and gen-time rejections stay
// in sync without two hardcoded lists.
func IsWireParseable(name string) bool {
	switch name {
	case "string", "bool",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return true
	}
	return false
}

// commonInitialisms enumerates abbreviations that should be rendered
// fully upper-cased when they appear as a word inside a Go identifier
// (matches `golint`/`staticcheck` conventions). Adding entries here
// changes the canonical Go name for any DSL field whose word list
// includes the new initialism — projects must regenerate to pick up
// the new spelling.
var commonInitialisms = map[string]bool{
	"id": true, "url": true, "uri": true, "api": true, "http": true,
	"https": true, "json": true, "xml": true, "tcp": true, "udp": true,
	"dns": true, "db": true, "sql": true, "csv": true, "tls": true,
	"ssl": true, "sha": true, "md5": true, "cdn": true, "dom": true,
	"pdf": true, "gif": true, "jpeg": true, "png": true, "xss": true,
	"csrf": true, "cpu": true, "gpu": true, "ram": true, "os": true,
	"io": true, "eof": true, "ip": true, "mac": true, "utf8": true,
	"ascii": true,
}

// GoFieldName converts a DSL field name (which is allowed to be
// lowercase, snake_case, or camelCase) into an exported Go
// identifier applying the common-initialism rule.
//
// Hot path (called per field across codegen + collision detection):
// Builder keeps the per-part append allocation-free.
func GoFieldName(name string) string {
	parts := SplitFieldName(name)
	var sb strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if commonInitialisms[strings.ToLower(p)] {
			sb.WriteString(strings.ToUpper(p))
			continue
		}
		sb.WriteString(strings.ToUpper(p[:1]))
		sb.WriteString(strings.ToLower(p[1:]))
	}
	return sb.String()
}

// SplitFieldName breaks a name into word components on `_`, `-`, and
// camelCase boundaries. Consecutive uppercase letters are kept
// together as a single acronym word (so `DBError` → `["DB", "Error"]`
// and `HTTPRequest` → `["HTTP", "Request"]`); a new word starts
// whenever an uppercase letter follows a lowercase letter, OR when
// an uppercase letter sits between two other uppercase letters and
// is followed by a lowercase letter (the "acronym ends here"
// boundary).
//
// Exported so callers outside this package (codegen path / error
// helpers) can derive their own kebab / snake forms without
// duplicating the boundary logic.
func SplitFieldName(s string) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var parts []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			parts = append(parts, current.String())
			current.Reset()
		}
	}
	isUpper := func(r rune) bool { return r >= 'A' && r <= 'Z' }
	isLower := func(r rune) bool { return r >= 'a' && r <= 'z' }
	for i, r := range runes {
		if r == '_' || r == '-' {
			flush()
			continue
		}
		if i > 0 {
			prev := runes[i-1]
			switch {
			case isUpper(r) && isLower(prev):
				flush()
			case isUpper(r) && isUpper(prev) && i+1 < len(runes) && isLower(runes[i+1]):
				flush()
			}
		}
		current.WriteRune(r)
	}
	flush()
	return parts
}

// KebabCase lowercases each word [SplitFieldName] yields and joins them with
// `-`. It is the one word-splitting rule for kebab output (route segments,
// generated file names) so the analyser's pathless-method route and the
// route codegen registers cannot disagree: `ListV2Items` → `list-v2items`,
// `GetUser` → `get-user`. A digit→letter boundary is NOT a word break, so
// `V2Items` stays one word — unlike a hand-rolled camel walker that splits
// before any uppercase whose next rune is lowercase.
func KebabCase(s string) string {
	parts := SplitFieldName(s)
	for i, p := range parts {
		parts[i] = strings.ToLower(p)
	}
	return strings.Join(parts, "-")
}

// Collision records one DSL → Go-identifier mapping inside a group
// of names that produced the same Go identifier under [GoFieldName].
// The first occurrence keeps the bare Go name; subsequent ones are
// suffixed `_2`, `_3`, ... so the resulting struct compiles. Both
// the original and the resolved Go names are returned so callers
// (semantic warnings + codegen) stay consistent on what spelling
// each DSL name maps to in the emitted struct.
type Collision struct {
	// DSLNames are the DSL spellings, in source order, that all
	// converted to the same canonical Go identifier.
	DSLNames []string
	// CanonicalGoName is the Go identifier the first DSL name maps
	// to - the "winner" that keeps its bare spelling.
	CanonicalGoName string
	// ResolvedGoNames pairs each DSLName index with the Go
	// identifier emitted in the generated struct: index 0 is the
	// canonical name; indices ≥ 1 carry the `_N` disambiguator.
	ResolvedGoNames []string
}

// DedupGoFieldNames takes the DSL field names of a single struct in
// source order and returns:
//
//   - resolved: the Go identifiers to emit in the struct, with `_N`
//     suffixes appended to any duplicate beyond the first occurrence.
//   - collisions: one [Collision] per group whose size is > 1, in
//     source-order of the first occurrence. Empty when the struct is
//     collision-free, which is the overwhelming common case.
//
// The dedup keeps the first DSL spelling at its bare Go name so a
// project that adds a colliding alias later doesn't retroactively
// rename the original field — generated code stays stable for
// already-published struct shapes.
func DedupGoFieldNames(dslNames []string) (resolved []string, collisions []Collision) {
	resolved = make([]string, len(dslNames))
	groups := map[string][]int{}
	canonicalOrder := []string{}
	for i, n := range dslNames {
		go_ := GoFieldName(n)
		if _, seen := groups[go_]; !seen {
			canonicalOrder = append(canonicalOrder, go_)
		}
		groups[go_] = append(groups[go_], i)
	}
	for _, canonical := range canonicalOrder {
		indices := groups[canonical]
		switch len(indices) {
		case 1:
			resolved[indices[0]] = canonical
		default:
			c := Collision{CanonicalGoName: canonical}
			for rank, idx := range indices {
				c.DSLNames = append(c.DSLNames, dslNames[idx])
				if rank == 0 {
					resolved[idx] = canonical
				} else {
					resolved[idx] = canonical + "_" + strconv.Itoa(rank+1)
				}
				c.ResolvedGoNames = append(c.ResolvedGoNames, resolved[idx])
			}
			collisions = append(collisions, c)
		}
	}
	return resolved, collisions
}

// LastSegment returns the trailing slash-delimited segment of a DSL import
// path — the piece that becomes the package's referencing identifier
// (`import "auth/types"` → alias `types`). Returns p unchanged when it has
// no slash, and "" for an empty or slash-terminated path.
func LastSegment(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
