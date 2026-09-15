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

// commonInitialisms enumerates abbreviations that should be rendered
// fully upper-cased when they appear as a word inside a Go identifier
// (matches `golint`/`staticcheck` conventions). Adding entries here
// changes the canonical Go name for any DSL field whose word list
// includes the new initialism - projects must regenerate to pick up
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
// `V2Items` stays one word - unlike a hand-rolled camel walker that splits
// before any uppercase whose next rune is lowercase.
func KebabCase(s string) string {
	parts := SplitFieldName(s)
	for i, p := range parts {
		parts[i] = strings.ToLower(p)
	}
	return strings.Join(parts, "-")
}

// FileName renders a DSL identifier as a generated file/directory name in the
// requested case: "snake" → `create_user`, "camel" → `createUser`, anything
// else (including "kebab" and "") → `create-user`. It uses the same
// [SplitFieldName] word-splitting as [KebabCase], so the default stays byte-for-
// byte identical to KebabCase; only the join changes. Callers pass the value of
// `output.fileCase`.
func FileName(name, style string) string {
	return FileNameWords(style, SplitFieldName(name))
}

// FileNameWords joins pre-split words into a file/directory name under the given
// case. It backs [FileName] and lets callers append a literal suffix word (e.g.
// "middleware") so the separator between it and the name follows the same case:
// kebab `auth-middleware`, snake `auth_middleware`, camel `authMiddleware`.
func FileNameWords(style string, words []string) string {
	lowered := make([]string, len(words))
	for i, w := range words {
		lowered[i] = strings.ToLower(w)
	}
	switch style {
	case "snake":
		return strings.Join(lowered, "_")
	case "camel":
		var b strings.Builder
		for i, w := range lowered {
			if i > 0 && w != "" {
				w = strings.ToUpper(w[:1]) + w[1:]
			}
			b.WriteString(w)
		}
		return b.String()
	default: // "kebab" and the empty/unknown fallback
		return strings.Join(lowered, "-")
	}
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
// rename the original field - generated code stays stable for
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
// path - the piece that becomes the package's referencing identifier
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

// PascalCase upper-cases the first letter of each `-`, `_` or `/`
// separated word and drops the separators: `user_profile` → `UserProfile`.
// Used wherever a DSL package or path segment has to prefix a generated
// symbol - an OpenAPI component name, a Go import alias - so the same
// input spells the same prefix in every target.
func PascalCase(s string) string {
	var b []byte
	upNext := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' || c == '_' || c == '/' {
			upNext = true
			continue
		}
		if upNext {
			if c >= 'a' && c <= 'z' {
				c -= 'a' - 'A'
			}
			upNext = false
		}
		b = append(b, c)
	}
	return string(b)
}

// ErrorTypeName is the name an error's body type is known by: the DSL name
// with `Err` appended, unless it already reads as an error. The Go type and
// the OpenAPI component share it so both sides name the same shape alike.
func ErrorTypeName(name string) string {
	if strings.HasSuffix(name, "Err") || strings.HasSuffix(name, "Error") {
		return name
	}
	return name + "Err"
}
