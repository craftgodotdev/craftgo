package codegen

import (
	"strconv"
	"strings"

	"github.com/dropship-dev/craftgo/internal/ast"
)

// This file groups the decorator-argument extractors used by every emit
// function in the validator registry. Each helper takes one
// [ast.DecoratorArg] and pulls a typed value out of it, returning the
// `(value, ok)` shape; `ok == false` is the standard signal for the
// caller to skip emitting any check (validator opts out silently).

// intArg pulls an int64 out of a literal DecoratorArg.
func intArg(a *ast.DecoratorArg) (int64, bool) {
	if a == nil || a.Value == nil {
		return 0, false
	}
	if i, ok := a.Value.(*ast.IntLit); ok {
		return i.Value, true
	}
	return 0, false
}

// stringArg pulls a string out of a literal DecoratorArg.
func stringArg(a *ast.DecoratorArg) (string, bool) {
	if a == nil || a.Value == nil {
		return "", false
	}
	if s, ok := a.Value.(*ast.StringLit); ok {
		return s.Value, true
	}
	return "", false
}

// stringOrIdentArg returns the underlying name from either a quoted
// string literal or a bare identifier. Used by `@format(...)` where both
// forms are accepted in the DSL. Returns "" for any other expression
// kind (numbers, booleans, nested decorators).
func stringOrIdentArg(a *ast.DecoratorArg) string {
	if a == nil || a.Value == nil {
		return ""
	}
	switch v := a.Value.(type) {
	case *ast.StringLit:
		return v.Value
	case *ast.IdentExpr:
		if v.Name != nil {
			return v.Name.String()
		}
	}
	return ""
}

// stringArrayArg pulls a list of strings out of an `@mimeTypes(["a","b"])`
// argument. The literal must be an ArrayLit of StringLits - anything
// else returns nil,false so the validator skips the check.
func stringArrayArg(a *ast.DecoratorArg) ([]string, bool) {
	if a == nil || a.Value == nil {
		return nil, false
	}
	arr, ok := a.Value.(*ast.ArrayLit)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr.Elements))
	for _, e := range arr.Elements {
		s, ok := e.(*ast.StringLit)
		if !ok {
			return nil, false
		}
		out = append(out, s.Value)
	}
	return out, true
}

// sizeArg extracts a byte count from a Size literal (`5MB`) or a bare
// integer. Returns 0,false on any other expression kind.
func sizeArg(a *ast.DecoratorArg) (int64, bool) {
	if a == nil || a.Value == nil {
		return 0, false
	}
	switch v := a.Value.(type) {
	case *ast.IntLit:
		return v.Value, true
	case *ast.SizeLit:
		return parseSizeText(v.Text)
	}
	return 0, false
}

// parseSizeText converts a `5MB` / `1024B` / `1.5GB` style literal into
// bytes. Floats are rounded down via int64 truncation. Unrecognised
// suffixes return 0,false so the validator skips the check rather than
// emitting nonsense.
func parseSizeText(text string) (int64, bool) {
	t := strings.TrimSpace(text)
	if t == "" {
		return 0, false
	}
	type unit struct {
		suffix string
		mult   int64
	}
	// Order matters: longer suffixes first so "MB" matches before "B".
	units := []unit{
		{"GB", 1 << 30},
		{"MB", 1 << 20},
		{"KB", 1 << 10},
		{"B", 1},
	}
	for _, u := range units {
		if !strings.HasSuffix(t, u.suffix) {
			continue
		}
		num := strings.TrimSpace(strings.TrimSuffix(t, u.suffix))
		if num == "" {
			return 0, false
		}
		// Try integer first to keep round numbers exact.
		if n, err := strconv.ParseInt(num, 10, 64); err == nil {
			return n * u.mult, true
		}
		if fl, err := strconv.ParseFloat(num, 64); err == nil {
			return int64(fl * float64(u.mult)), true
		}
		return 0, false
	}
	// No suffix → assume bytes (matches DSL "bare number → bytes" rule).
	if n, err := strconv.ParseInt(t, 10, 64); err == nil {
		return n, true
	}
	return 0, false
}
