// Package idents converts DSL names into Go identifiers, generated file
// names and symbol prefixes.
package idents

import (
	"strconv"
	"strings"
)

// commonInitialisms lists the words GoFieldName writes fully upper-cased: `id` → `ID`.
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

// GoFieldName converts a lower, snake or camel case DSL field name into an
// exported Go identifier with common initialisms upper-cased: `user_id` → `UserID`.
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

// SplitFieldName splits s into words on `_`, `-` and camelCase boundaries.
// A run of capitals stays one word until the capital that starts a lowercase
// word: `HTTPRequest` → `HTTP`, `Request`.
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

// KebabCase lowercases the words of [SplitFieldName] and joins them with `-`:
// `GetUser` → `get-user`. A digit→letter boundary is not a word break:
// `ListV2Items` → `list-v2items`.
func KebabCase(s string) string { return FileName(s, FileCaseKebab) }

// Values of `output.fileCase`, the case of generated file and directory names.
const (
	FileCaseKebab = "kebab"
	FileCaseSnake = "snake"
	FileCaseCamel = "camel"
	// DefaultFileCase applies when output.fileCase is unset.
	DefaultFileCase = FileCaseSnake
)

// FileName renders a DSL identifier as a file or directory name in the given
// `output.fileCase`: "snake" → `create_user`, "camel" → `createUser`, "kebab"
// → `create-user`; "" is [DefaultFileCase].
func FileName(name, style string) string {
	return FileNameWords(style, SplitFieldName(name))
}

// FileNameWords is [FileName] for pre-split words, so a caller can append a
// literal word: kebab `auth-middleware`, snake `auth_middleware`, camel `authMiddleware`.
func FileNameWords(style string, words []string) string {
	lowered := make([]string, len(words))
	for i, w := range words {
		lowered[i] = strings.ToLower(w)
	}
	if style == "" {
		style = DefaultFileCase
	}
	switch style {
	case FileCaseSnake:
		return strings.Join(lowered, "_")
	case FileCaseCamel:
		var b strings.Builder
		for i, w := range lowered {
			if i > 0 && w != "" {
				w = strings.ToUpper(w[:1]) + w[1:]
			}
			b.WriteString(w)
		}
		return b.String()
	default:
		return strings.Join(lowered, "-")
	}
}

// Collision is a group of DSL field names in one struct that map to the same
// Go identifier under [GoFieldName].
type Collision struct {
	// DSLNames are the colliding DSL spellings, in source order.
	DSLNames []string
	// CanonicalGoName is the shared Go identifier, kept bare by the first name.
	CanonicalGoName string
	// ResolvedGoNames holds the emitted Go name of each DSLNames entry.
	ResolvedGoNames []string
}

// DedupGoFieldNames returns the Go names for a struct's DSL field names, in
// source order, and one [Collision] per group mapping to the same name. The
// first name of a group keeps the bare Go name; later ones get `_2`, `_3`, ...
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

// LastSegment returns what follows the last `/` of a DSL import path, the
// name the package is referenced by: `auth/types` → `types`.
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

// ErrorTypeName returns the Go and OpenAPI name of an error's body type: the
// DSL name plus `Err`, unless it already ends in `Err` or `Error`.
func ErrorTypeName(name string) string {
	if strings.HasSuffix(name, "Err") || strings.HasSuffix(name, "Error") {
		return name
	}
	return name + "Err"
}

// ErrorBodyName returns the Go name of the struct an error with fields embeds:
// the DSL name plus `Body`.
func ErrorBodyName(name string) string { return name + "Body" }
