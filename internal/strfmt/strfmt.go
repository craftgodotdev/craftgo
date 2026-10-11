// Package strfmt is the catalogue of named string formats `@format` accepts:
// each one's OpenAPI `format` keyword and the Go check generated for it.
package strfmt

import (
	_ "embed"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"slices"
	"strconv"
	"sync"
)

// Spec is one named string format.
type Spec struct {
	Name  string // the `@format` argument
	Label string // fills the validation message "not a valid <Label>"
	OAS   string // the OpenAPI `format` keyword when it differs from Name
	// Func is the function of checks.go the generated check calls, true for a
	// valid value; the generated file declares it as checks.go does.
	Func string
	// Pattern is the regular expression the value must match when Func is empty.
	Pattern string
	// Imports are the Go packages the generated check needs: those the
	// functions it calls use.
	Imports []string
}

// Valid reports whether v passes the check generated for s: its Pattern, or
// its Func.
func (s Spec) Valid(v string) bool {
	if s.Pattern != "" {
		return regexp.MustCompile(s.Pattern).MatchString(v)
	}
	return checks[s.Func](v)
}

// All lists every format.
var All = []Spec{
	{Name: "email", Label: "email", Func: "_isEmail"},
	{Name: "url", Label: "URL", Func: "_isURL"},
	{Name: "uri", Label: "URI", Func: "_isURI"},
	// RFC 4122 UUID layout; the version digit is not checked.
	{Name: "uuid", Label: "UUID",
		Pattern: `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`},
	{Name: "datetime", Label: "RFC 3339 datetime", OAS: "date-time", Func: "_isDateTime"},
	{Name: "date", Label: "date", Func: "_isDate"},
	{Name: "time", Label: "time", Func: "_isTime"},
	{Name: "phone", Label: "phone", Func: "_isPhone"},
	{Name: "ipv4", Label: "IPv4", Func: "_isIPv4"},
	{Name: "ipv6", Label: "IPv6", Func: "_isIPv6"},
	{Name: "cidr", Label: "CIDR", Func: "_isCIDR"},
	{Name: "mac", Label: "MAC address", Func: "_isMAC"},
	// Card number length only; no Luhn checksum.
	{Name: "creditcard", Label: "credit card number", Pattern: `^[0-9]{12,19}$`},
	{Name: "base64", Label: "base64", Func: "_isBase64"},
	{Name: "base64url", Label: "base64url", Func: "_isBase64URL"},
	// CSS hex color: 3 or 6 hex digits, optional `#`.
	{Name: "hexcolor", Label: "hex color", Pattern: `^#?[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`},
	{Name: "json", Label: "JSON", Func: "_isJSON"},
}

// checks are the functions of checks.go [Spec.Valid] calls, by name.
var checks = map[string]func(string) bool{
	"_isEmail":     _isEmail,
	"_isURL":       _isURL,
	"_isURI":       _isURI,
	"_isDateTime":  _isDateTime,
	"_isDate":      _isDate,
	"_isTime":      _isTime,
	"_isPhone":     _isPhone,
	"_isIPv4":      _isIPv4,
	"_isIPv6":      _isIPv6,
	"_isCIDR":      _isCIDR,
	"_isMAC":       _isMAC,
	"_isBase64":    _isBase64,
	"_isBase64URL": _isBase64URL,
	"_isJSON":      _isJSON,
}

func init() {
	for i, s := range All {
		for _, h := range s.Helpers() {
			All[i].Imports = append(All[i].Imports, h.Imports...)
		}
		slices.Sort(All[i].Imports)
		All[i].Imports = slices.Compact(All[i].Imports)
	}
}

// Helper is a function of checks.go a generated check calls.
type Helper struct {
	Name    string
	Code    string   // its declaration, doc comment included
	Imports []string // the paths of the packages it uses
	order   int      // its place in checks.go
}

// Helpers returns the functions the generated check of s calls, directly or
// through another, in the order checks.go declares them; a Pattern calls none.
func (s Spec) Helpers() []Helper {
	if s.Func == "" {
		return nil
	}
	all := helpers()
	seen := map[string]bool{}
	var out []Helper
	var visit func(name string)
	visit = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		h := all[name]
		out = append(out, h.Helper)
		for _, callee := range h.calls {
			visit(callee)
		}
	}
	visit(s.Func)
	SortHelpers(out)
	return out
}

// SortHelpers sorts hs in the order checks.go declares them.
func SortHelpers(hs []Helper) {
	slices.SortFunc(hs, func(a, b Helper) int { return a.order - b.order })
}

//go:embed checks.go
var checksSource string

// helper is a function of checks.go and the functions of checks.go it calls.
type helper struct {
	Helper
	calls []string
}

// helpers reads the functions of checks.go, by name.
var helpers = sync.OnceValue(func() map[string]helper {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "checks.go", checksSource, parser.ParseComments)
	if err != nil {
		panic("strfmt: checks.go: " + err.Error())
	}
	pkgs := map[string]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		pkgs[path.Base(p)] = p
	}
	funcs := map[string]bool{}
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			funcs[fd.Name.Name] = true
		}
	}
	out := map[string]helper{}
	for i, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		start := fd.Pos()
		if fd.Doc != nil {
			start = fd.Doc.Pos()
		}
		h := helper{Helper: Helper{
			Name:  fd.Name.Name,
			Code:  checksSource[fset.Position(start).Offset:fset.Position(fd.End()).Offset],
			order: i,
		}}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && funcs[id.Name] && !slices.Contains(h.calls, id.Name) {
					h.calls = append(h.calls, id.Name)
				}
			case *ast.SelectorExpr:
				if id, ok := n.X.(*ast.Ident); ok && pkgs[id.Name] != "" && !slices.Contains(h.Imports, pkgs[id.Name]) {
					h.Imports = append(h.Imports, pkgs[id.Name])
				}
			}
			return true
		})
		out[h.Name] = h
	}
	return out
})

// Names returns every format name, in [All] order.
func Names() []string {
	out := make([]string, len(All))
	for i, s := range All {
		out[i] = s.Name
	}
	return out
}

// Lookup returns the spec for name.
func Lookup(name string) (Spec, bool) {
	for _, s := range All {
		if s.Name == name {
			return s, true
		}
	}
	return Spec{}, false
}

// OpenAPIFormat returns the OpenAPI `format` keyword for name: the spec's OAS
// when set, else name itself.
func OpenAPIFormat(name string) string {
	if s, ok := Lookup(name); ok && s.OAS != "" {
		return s.OAS
	}
	return name
}
