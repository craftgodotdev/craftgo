package format

import (
	"strings"
	"testing"
)

// TestFormatTypeBodyCommentPlacement pins that a type body's docs, section
// comments, closing note and blank lines format to themselves.
func TestFormatTypeBodyCommentPlacement(t *testing.T) {
	canonical := `package demo

// User doc comment attached to the decl.
type User {
	// Section: Identity

	id    string @path
	name  string

	// Section: Contact
	email string

	// Section: floating with blank lines around

	phone string

	// closing note before brace
}

// Between-decls floating block.

// Order doc.
type Order {
	sku string // trailing note
}
`
	formatExact(t, canonical, canonical)
}

// TestFormatServiceBodyCommentPlacement pins that comments in service and
// method bodies, including trailing and closing-brace notes, format to themselves.
func TestFormatServiceBodyCommentPlacement(t *testing.T) {
	canonical := `package demo

type Req {
	id string @path
}

type Resp {
	ok bool
}

service Things {
	// section: reads

	// doc for list
	get list /things {
		// above request
		request  Req
		response Resp // trailing on response

		// above method rbrace
	}

	// detached inside service, blank both sides

	post create /things {
		request  Req

		// detached inside method body

		response Resp
	}
	// closing note above service rbrace
}
`
	formatExact(t, canonical, canonical)
}

// A comment after code stays at the end of that code's line, a comment inside
// a decorator chain stays in the chain, and each formats to itself.
func TestFormatKeepsEveryCommentInPlace(t *testing.T) {
	for name, src := range map[string]string{
		"after package":                    "package x // c\n",
		"after middleware":                 "package x\n\nmiddleware M // c\n",
		"after bodiless error":             "package x\n\nerror NotFound Gone // c\n",
		"after mixin":                      "package x\n\ntype T {\n\tBase // c\n\ta string\n}\n",
		"after type brace":                 "package x\n\ntype T { // c\n\ta string\n}\n",
		"after generic brace":              "package x\n\ntype P<T> { // c\n\titems T[]\n}\n",
		"after enum brace":                 "package x\n\nenum E { // c\n\tA\n}\n",
		"after service brace":              "package x\n\nservice S { // c\n\tget A /a {\n\t\tresponse T\n\t}\n}\n",
		"after method brace":               "package x\n\nservice S {\n\tget A /a { // c\n\t\tresponse T\n\t}\n}\n",
		"after event brace":                "package x\n\nevent E { // c\n\tpayload T\n}\n",
		"after closing brace":              "package x\n\ntype T {\n\ta string\n} // c\n",
		"after empty body":                 "package x\n\nservice S {\n\tget A /a {} // c\n}\n",
		"doc above a commented decorator":  "package x\n\ntype T {\n\t// doc\n\t@minLength(1) // c\n\tb string\n}\n",
		"after a decorator and its field":  "package x\n\ntype T {\n\t@minLength(1) // c1\n\tb string // c2\n}\n",
		"after two field decorators":       "package x\n\ntype T {\n\t@minLength(1) // c1\n\t@maxLength(5) // c2\n\tb string\n}\n",
		"inside a field chain":             "package x\n\ntype T {\n\t@minLength(1)\n\t// between\n\tb string\n}\n",
		"inside a scalar chain":            "package x\n\n@minLength(1)\n// in-chain\n@maxLength(5)\nscalar Code string\n",
		"after a scalar decorator":         "package x\n\n@minLength(1) // c\nscalar S string\n",
		"under forwarded decorators":       "@doc(\"t\")\n// c\ntype T {\n\ty string\n}\n",
		"doc above the keyword":            "package x\n\n@deprecated\n// T is a type.\ntype T {\n\ty string\n}\n",
		"docs above and under decorators":  "package x\n\n// T doc.\n@deprecated\n// More on T.\ntype T {\n\ty string\n}\n",
		"doc above a scalar's keyword":     "package x\n\n@minLength(1)\n// Code is a code.\nscalar Code string\n",
		"chain comment apart from keyword": "package x\n\n@deprecated\n// apart\n\ntype T {\n\ty string\n}\n",
		"between file decorators":          "@version(\"1\")\n// c\n@doc(\"d\")\npackage x\n",
		"above file decorators":            "// prologue\n\n// doc\n@version(\"1\") // v\npackage x\n",
		"above forwarded decorators":       "// prologue\n\n// doc\n@doc(\"t\")\ntype T {\n\ty string\n}\n",
		"after enum values":                "package x\n\nenum E {\n\tA = 1 // c\n\tB = 2 @x // d\n}\n",
		"after clauses":                    "package x\n\nservice S {\n\tget A /a {\n\t\trequest  R // c\n\t\tresponse T // d\n\t}\n}\n",
	} {
		t.Run(name, func(t *testing.T) { formatExact(t, src, src) })
	}
}

// A comment moves to the line its code joins: an argument list joined after
// its last argument, a path before the brace, a CRLF line.
func TestFormatMovesACommentWithItsCode(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"after a joined argument list", "package x\n\ntype T {\n\ta string @example({\n\t\tx: 1,\n\t\ty: 2\n\t}) // c\n}\n", "package x\n\ntype T {\n\ta string @example({x: 1, y: 2}) // c\n}\n"},
		{"path before the brace", "package x\n\nservice S {\n\tget A /a // c\n\t{\n\t\tresponse T\n\t}\n}\n", "package x\n\nservice S {\n\tget A /a { // c\n\t\tresponse T\n\t}\n}\n"},
		{"CRLF", "package x\r\n\r\ntype T { // c\r\n\ta string // d\r\n}\r\n", "package x\n\ntype T { // c\n\ta string // d\n}\n"},
		{"doc above the keyword of a one-line body", "package x\n\n@deprecated\n// c\ntype T { a string }\n", "package x\n\n@deprecated\n// c\ntype T {\n\ta string\n}\n"},
		{"doc above the keyword of a one-line service", "package x\n\n@deprecated\n// c\nservice S { get A /a {} }\n", "package x\n\n@deprecated\n// c\nservice S {\n\tget A /a {}\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) { formatExact(t, c.src, c.want) })
	}
}

// Decorators continued on the lines after a field or an enum value join its
// line without a blank line after it; a comment among them keeps them on
// their lines, one level deeper, and stays where it was.
// A decorator on a line of its own prints above the member it decorates, the
// one below it, one per line under the member's doc; the decorators on a
// member's line stay there.
func TestFormatKeepsOwnLineDecoratorsAboveTheirMember(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"field",
			"package x\n\ntype T {\n\tx string @minLength(1)\n\t\t@maxLength(5)\n\ty string\n}\n",
			"package x\n\ntype T {\n\tx string @minLength(1)\n\t@maxLength(5)\n\ty string\n}\n",
		},
		{
			"one per line",
			"package x\n\ntype T {\n\tid string\n\n\t@format(email) @maxLength(254)\n\temail string @minLength(3)\n}\n",
			"package x\n\ntype T {\n\tid    string\n\n\t@format(email)\n\t@maxLength(254)\n\temail string @minLength(3)\n}\n",
		},
		{
			"blank line under the decorator",
			"package x\n\ntype T {\n\tx string @minLength(1)\n\t\t@maxLength(5)\n\n\ty string\n}\n",
			"package x\n\ntype T {\n\tx string @minLength(1)\n\t@maxLength(5)\n\ty string\n}\n",
		},
		{
			"doc above the decorators",
			"package x\n\ntype T {\n\tid   string\n\tname string @minLength(1)\n\t// c\n\t@maxLength(5)\n\ty    string\n}\n",
			"package x\n\ntype T {\n\tid   string\n\tname string @minLength(1)\n\t// c\n\t@maxLength(5)\n\ty    string\n}\n",
		},
		{
			"doc above and under the decorators",
			"package x\n\ntype T {\n\tx string\n\t\t// c\n\t\t@maxLength(5)\n\t// d\n\ty string\n}\n",
			"package x\n\ntype T {\n\tx string\n\t// c\n\t@maxLength(5)\n\t// d\n\ty string\n}\n",
		},
		{
			"trailing comments",
			"package x\n\ntype T {\n\tx string @minLength(1) // c\n\t\t@maxLength(5) // d\n\ty string\n}\n",
			"package x\n\ntype T {\n\tx string @minLength(1) // c\n\t@maxLength(5) // d\n\ty string\n}\n",
		},
		{
			"a decorator before the name",
			"package x\n\ntype T {\n\t@maxLength(5) y string @minLength(1)\n}\n",
			"package x\n\ntype T {\n\ty string @maxLength(5) @minLength(1)\n}\n",
		},
		{
			"enum value",
			"package x\n\nenum E {\n\tA = 1 @doc(\"a\")\n\t\t@deprecated\n\tB = 2\n}\n",
			"package x\n\nenum E {\n\tA = 1 @doc(\"a\")\n\t@deprecated\n\tB = 2\n}\n",
		},
		{
			"comment between an enum value's decorators",
			"package x\n\nenum E {\n\tA = 1 @doc(\"a\")\n\t@doc(\"b\")\n\t// c\n\t@deprecated\n\tB = 2\n}\n",
			"package x\n\nenum E {\n\tA = 1 @doc(\"a\")\n\t@doc(\"b\")\n\t// c\n\t@deprecated\n\tB = 2\n}\n",
		},
		{
			"scalar",
			"package x\n\n@minLength(1)\nscalar S string @maxLength(5)\n",
			"package x\n\n@minLength(1)\nscalar S string @maxLength(5)\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatExact(t, c.src, c.want) })
	}
}

// A comment among a type's arguments stays there: the argument list keeps its
// lines, one level deeper, as a decorator's does, and the members around set
// their columns without it.
func TestFormatKeepsACommentInATypesArguments(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"trailing comments in a field's type",
			"package x\n\ntype A {\n\tp Page< // one page of\n\t\tUser // users\n\t>\n\tname string @minLength(1)\n}\n",
			"package x\n\ntype A {\n\tp    Page< // one page of\n\t\tUser, // users\n\t>\n\tname string @minLength(1)\n}\n",
		},
		{
			"map keeps no trailing comma",
			"package x\n\ntype T {\n\ta map<string, // t\n\t\tint>\n\tb string\n}\n",
			"package x\n\ntype T {\n\ta map<\n\t\tstring, // t\n\t\tint\n\t>\n\tb string\n}\n",
		},
		{
			"comment on a line of its own in a map",
			"package x\n\ntype T {\n\ta map<string,\n\t\t// c\n\t\tint> // t\n\tb string\n}\n",
			"package x\n\ntype T {\n\ta map<\n\t\tstring,\n\t\t// c\n\t\tint\n\t> // t\n\tb string\n}\n",
		},
		{
			"decorators after the type",
			"package x\n\ntype T {\n\ta Page<\n\t\tstring // t\n\t> @doc(\"d\")\n}\n",
			"package x\n\ntype T {\n\ta Page<\n\t\tstring, // t\n\t> @doc(\"d\")\n}\n",
		},
		{
			"arguments on the opener's line join it",
			"package x\n\ntype T {\n\ta Page<string // t\n\t> @doc(\"d\")\n}\n",
			"package x\n\ntype T {\n\ta Page<string> @doc(\"d\") // t\n}\n",
		},
		{
			"mixin",
			"package x\n\ntype U {\n\tPage< // t\n\t\tstring>\n\tb string\n}\n",
			"package x\n\ntype U {\n\tPage< // t\n\t\tstring,\n\t>\n\tb string\n}\n",
		},
		{
			"comment on a line of its own in a mixin",
			"package x\n\ntype U {\n\tPage<\n\t\t// c\n\t\tstring>\n\tb string\n}\n",
			"package x\n\ntype U {\n\tPage<\n\t\t// c\n\t\tstring,\n\t>\n\tb string\n}\n",
		},
		{
			"response",
			"package x\n\ntype Box<V> { v V }\n\nservice S {\n\tget A /a {\n\t\tresponse Box<\n\t\t// c\n\t\tint>\n\t}\n}\n",
			"package x\n\ntype Box<V> {\n\tv V\n}\n\nservice S {\n\tget A /a {\n\t\tresponse Box<\n\t\t\t// c\n\t\t\tint,\n\t\t>\n\t}\n}\n",
		},
		{
			"type parameters",
			"package x\n\ntype Pair<A, // a\n\tB // b\n\t> {\n\tx A\n\ty B\n}\n",
			"package x\n\ntype Pair<\n\tA, // a\n\tB, // b\n> {\n\tx A\n\ty B\n}\n",
		},
		{
			"comment on a line of its own in the type parameters",
			"package x\n\ntype Page<\n\t// c\n\tT> {\n\titems T[]\n}\n",
			"package x\n\ntype Page<\n\t// c\n\tT,\n> {\n\titems T[]\n}\n",
		},
		{
			"nested arguments",
			"package x\n\ntype T {\n\ta Page<map<string, // k\n\t\tint>>\n}\n",
			"package x\n\ntype T {\n\ta Page<map<\n\t\tstring, // k\n\t\tint\n\t>>\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatExact(t, c.src, c.want) })
	}
}

// A comment trailing a header's last word and one trailing its body's `{`
// below keep their lines: the `{` stays on its own.
func TestFormatKeepsTheCommentsOfAHeaderAndItsBrace(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"type",
			"package x\n\ntype // c1\nA // c2\n{ // c3\n\ta string // c4\n} // c5\n",
			"package x\n\ntype // c1\n\tA // c2\n{ // c3\n\ta string // c4\n} // c5\n",
		},
		{
			"method",
			"package x\n\nservice S { // c1\n\tget // c2\n\tA // c3\n\t/a // c4\n\t{ // c5\n\t} // c6\n}\n",
			"package x\n\nservice S { // c1\n\tget // c2\n\t\tA // c3\n\t\t/a // c4\n\t{ // c5\n\t} // c6\n}\n",
		},
		{
			"enum",
			"package x\n\nenum E // c1\n{ // c2\n\tA\n}\n",
			"package x\n\nenum E // c1\n{ // c2\n\tA\n}\n",
		},
		{
			"event",
			"package x\n\ntype R { a string }\n\nevent E // c1\n{ // c2\n\tpayload R\n}\n",
			"package x\n\ntype R {\n\ta string\n}\n\nevent E // c1\n{ // c2\n\tpayload R\n}\n",
		},
		{
			"one comment joins the brace's line",
			"package x\n\ntype A // c\n{\n\ta string\n}\n",
			"package x\n\ntype A { // c\n\ta string\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatExact(t, c.src, c.want) })
	}
}

// A comment on its own line inside a decorator's arguments stays there: the
// argument list keeps its lines, one level deeper, with the comment above the
// arguments or the closer it sat above.
func TestFormatKeepsACommentInDecoratorArguments(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"field decorator",
			"package x\n\ntype T {\n\ta string @doc(\n\t\t// note about the doc\n\t\t\"x\" // why x\n\t)\n\tb string\n}\n",
			"package x\n\ntype T {\n\ta string @doc(\n\t\t// note about the doc\n\t\t\"x\", // why x\n\t)\n\tb string\n}\n",
		},
		{
			"closing parenthesis on the argument's line",
			"package x\n\ntype T {\n\ta string @header(\n\t//TODO\n\t\"X\") // n\n\tb string\n}\n",
			"package x\n\ntype T {\n\ta string @header(\n\t\t// TODO\n\t\t\"X\",\n\t) // n\n\tb string\n}\n",
		},
		{
			"arguments on the decorator's line",
			"package x\n\ntype T {\n\ta string @length(1, 64\n\t\t// why\n\t)\n}\n",
			"package x\n\ntype T {\n\ta string @length(\n\t\t1, 64,\n\t\t// why\n\t)\n}\n",
		},
		{
			"declaration decorator",
			"package x\n\n@tags(\n\t// lead\n\t\"a\", // first\n\t\"b\", \"c\" // second\n\t// tail\n)\nservice S { get A /a {} }\n",
			"package x\n\n@tags(\n\t// lead\n\t\"a\", // first\n\t\"b\", \"c\", // second\n\t// tail\n)\nservice S {\n\tget A /a {}\n}\n",
		},
		{
			"declaration's last decorator",
			"package x\n\n@doc(\n\t// inside\n\t\"d\"\n)\ntype T {\n\ty string\n}\n",
			"package x\n\n@doc(\n\t// inside\n\t\"d\",\n)\ntype T {\n\ty string\n}\n",
		},
		{
			"decorator above a field",
			"package x\n\ntype T {\n\t@minLength(\n\t\t// c\n\t\t1)\n\ta string\n}\n",
			"package x\n\ntype T {\n\t@minLength(\n\t\t// c\n\t\t1,\n\t)\n\ta string\n}\n",
		},
		{
			"array",
			"package x\n\ntype T {\n\ta string[] @example([\n\t\t// first\n\t\t\"x\",\n\t\t\"y\",\n\t\t// last\n\t]) // t\n}\n",
			"package x\n\ntype T {\n\ta string[] @example([\n\t\t// first\n\t\t\"x\",\n\t\t\"y\",\n\t\t// last\n\t]) // t\n}\n",
		},
		{
			"object",
			"package x\n\ntype T {\n\ta string @example({\n\t\t// k\n\t\tx: 1\n\t})\n}\n",
			"package x\n\ntype T {\n\ta string @example({\n\t\t// k\n\t\tx: 1,\n\t})\n}\n",
		},
		{
			"empty parentheses",
			"package x\n\ntype T {\n\ta string @deprecated(\n\t\t// why\n\t)\n}\n",
			"package x\n\ntype T {\n\ta string @deprecated(\n\t\t// why\n\t)\n}\n",
		},
		{
			"scalar decorator",
			"package x\n\nscalar When string @format(datetime\n// c\n)\n",
			"package x\n\nscalar When string @format(\n\tdatetime,\n\t// c\n)\n",
		},
		{
			"comment before the parenthesis",
			"package x\n\nscalar Size int @lte\n// c\n(100)\n",
			"package x\n\nscalar Size int @lte(\n\t// c\n\t100,\n)\n",
		},
		{
			"enum value decorator",
			"package x\n\nenum E {\n\tA @doc(\n\t\t// c\n\t\t\"a\")\n\tB\n}\n",
			"package x\n\nenum E {\n\tA @doc(\n\t\t// c\n\t\t\"a\",\n\t)\n\tB\n}\n",
		},
		{
			"method decorator",
			"package x\n\nservice S {\n\t@doc(\n\t\t// c\n\t\t\"a\")\n\tget A /a {}\n}\n",
			"package x\n\nservice S {\n\t@doc(\n\t\t// c\n\t\t\"a\",\n\t)\n\tget A /a {}\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatExact(t, c.src, c.want) })
	}
}

// A comment block between the words of a declaration without a body stays
// there: the words after it continue one level deeper.
func TestFormatKeepsAHeaderCommentOfABodilessDeclaration(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"middleware",
			"package x\n\nmiddleware\n// c\n A // t\n\nmiddleware B\n",
			"package x\n\nmiddleware\n\t// c\n\tA // t\n\nmiddleware B\n",
		},
		{
			"middleware with doc and decorator",
			"package x\n\n// M doc.\n@doc(\"m\")\nmiddleware // k\n// c\nM\n",
			"package x\n\n// M doc.\n@doc(\"m\")\nmiddleware // k\n\t// c\n\tM\n",
		},
		{
			"trailing comment after the keyword",
			"package x\n\nmiddleware // c1\n\tM // c2\n",
			"package x\n\nmiddleware // c1\n\tM // c2\n",
		},
		{
			"two comment blocks",
			"package x\n\nmiddleware\n// a\n\n// b\nM\n",
			"package x\n\nmiddleware\n\t// a\n\n\t// b\n\tM\n",
		},
		{
			"scalar trailing comment",
			"package x\n\nscalar S // c\n string @minLength(1)\n",
			"package x\n\nscalar S // c\n\tstring @minLength(1)\n",
		},
		{
			"scalar name",
			"package x\n\nscalar\n// c\nS string @minLength(1)\n\ntype T {\n\ta S\n}\n",
			"package x\n\nscalar\n\t// c\n\tS string @minLength(1)\n\ntype T {\n\ta S\n}\n",
		},
		{
			"scalar primitive",
			"package x\n\nscalar S\n// c\nstring\n",
			"package x\n\nscalar S\n\t// c\n\tstring\n",
		},
		{
			"error name",
			"package x\n\nerror NotFound\n// c\nGone\n",
			"package x\n\nerror NotFound\n\t// c\n\tGone\n",
		},
		{
			"package",
			"package\n// c\nx\n\ntype T {\n\ta string\n}\n",
			"package\n\t// c\n\tx\n\ntype T {\n\ta string\n}\n",
		},
		{
			"package after file decorators",
			"@version(\"1\")\npackage // c\n x // d\n",
			"@version(\"1\")\npackage // c\n\tx // d\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatExact(t, c.src, c.want) })
	}
}

// A trailing comment between the words of a declaration's or a method's
// header stays there, the words after it one level deeper.
func TestFormatKeepsATrailingCommentInABodiedHeader(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"type",
			"package x\n\ntype // c1\n X { // c2\n\ta string\n}\n",
			"package x\n\ntype // c1\n\tX { // c2\n\ta string\n}\n",
		},
		{
			"generic type",
			"package x\n\ntype // c1\n Page<T, U> {\n\ta T\n\tb U\n}\n",
			"package x\n\ntype // c1\n\tPage<T, U> {\n\ta T\n\tb U\n}\n",
		},
		{
			"enum",
			"package x\n\nenum // c1\n E { // c2\n\tA\n}\n",
			"package x\n\nenum // c1\n\tE { // c2\n\tA\n}\n",
		},
		{
			"error",
			"package x\n\nerror NotFound // c1\n E { // c2\n\ta string\n}\n",
			"package x\n\nerror NotFound // c1\n\tE { // c2\n\ta string\n}\n",
		},
		{
			"service",
			"package x\n\nservice // c1\n S { // c2\n\tget A /a {}\n}\n",
			"package x\n\nservice // c1\n\tS { // c2\n\tget A /a {}\n}\n",
		},
		{
			"extend block",
			"package x\n\nextend // c1\n service S { // c2\n\tget A /a {}\n}\n",
			"package x\n\nextend // c1\n\tservice S { // c2\n\tget A /a {}\n}\n",
		},
		{
			"extend block after its service",
			"package x\n\nservice S {\n\tget A /a {}\n}\n\nextend // c\nservice S {\n\tget B /b {}\n}\n",
			"package x\n\nservice S {\n\tget A /a {}\n}\n\nextend // c\n\tservice S {\n\tget B /b {}\n}\n",
		},
		{
			"event",
			"package x\n\nevent // c1\n E { // c2\n\tpayload R\n}\n",
			"package x\n\nevent // c1\n\tE { // c2\n\tpayload R\n}\n",
		},
		{
			"method",
			"package x\n\nservice S {\n\tget // c1\n\t A /a { // c2\n\t\tresponse R\n\t}\n}\n",
			"package x\n\nservice S {\n\tget // c1\n\t\tA /a { // c2\n\t\tresponse R\n\t}\n}\n",
		},
		{
			"method path",
			"package x\n\nservice S {\n\tget A // c1\n\t /a/{id} {} // c2\n}\n",
			"package x\n\nservice S {\n\tget A // c1\n\t\t/a/{id} {} // c2\n}\n",
		},
		{
			"method with an empty body",
			"package x\n\nservice S {\n\tget // c1\n\t A /a { // c2\n\t}\n}\n",
			"package x\n\nservice S {\n\tget // c1\n\t\tA /a { // c2\n\t}\n}\n",
		},
		{
			"event with an empty body",
			"package x\n\nevent E { // c\n}\n",
			"package x\n\nevent E { // c\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatExact(t, c.src, c.want) })
	}
}

// A comment block inside a declaration's or a method's header, before its
// opening brace, prints at the top of its body.
func TestFormatKeepsAHeaderCommentInItsDeclaration(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"type",
			"package x\n\ntype\n// c\nT { // t\n\ta string\n}\n\nmiddleware M\n",
			"package x\n\ntype T { // t\n\t// c\n\n\ta string\n}\n\nmiddleware M\n",
		},
		{
			"method",
			"package x\n\nservice S {\n\tget A\n\t// c\n\t/a {\n\t\tresponse T\n\t}\n\n\tget B /b {}\n}\n",
			"package x\n\nservice S {\n\tget A /a {\n\t\t// c\n\n\t\tresponse T\n\t}\n\n\tget B /b {}\n}\n",
		},
		{
			"empty method body",
			"package x\n\nservice S {\n\tget A\n\t// c\n\t/a {}\n}\n",
			"package x\n\nservice S {\n\tget A /a {\n\t\t// c\n\t}\n}\n",
		},
		{
			"extend block",
			"package x\n\n// d\n@group(\"g\")\nextend\n// c\n service S {\n\tget A /a {}\n}\n",
			"package x\n\n// d\n@group(\"g\")\nextend service S {\n\t// c\n\n\tget A /a {}\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatExact(t, c.src, c.want) })
	}
}

// An argument list, array or object written over several lines keeps its
// lines when a trailing comment sits on one of them: each source line of
// elements on its own line one level deeper, the closer on its own line.
func TestFormatKeepsAnArgumentListWithComments(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"two comments in the arguments",
			"package x\n\ntype T {\n\tname string\n\tid string @oneOf(\n\t\t\"a\", // first\n\t\t\"b\" // second\n\t)\n}\n",
			"package x\n\ntype T {\n\tname string\n\tid   string @oneOf(\n\t\t\"a\", // first\n\t\t\"b\", // second\n\t)\n}\n",
		},
		{
			"comment after the opening parenthesis",
			"package x\n\ntype T {\n\tid string @length( // c0\n\t\t1, // min\n\t\t64\n\t)\n}\n",
			"package x\n\ntype T {\n\tid string @length( // c0\n\t\t1, // min\n\t\t64,\n\t)\n}\n",
		},
		{
			"comment after the closing parenthesis",
			"package x\n\ntype T {\n\tid string @length(\n\t\t1, // a\n\t\t80\n\t) // b\n}\n",
			"package x\n\ntype T {\n\tid string @length(\n\t\t1, // a\n\t\t80,\n\t) // b\n}\n",
		},
		{
			"first argument on the opening line",
			"package x\n\ntype T {\n\tid string @length(1, // min\n\t\t64)\n}\n",
			"package x\n\ntype T {\n\tid string @length(\n\t\t1, // min\n\t\t64,\n\t)\n}\n",
		},
		{
			"arguments below the name",
			"package x\n\ntype T {\n\tid string @doc // c1\n\t\t(\"x\") // c2\n}\n",
			"package x\n\ntype T {\n\tid string @doc( // c1\n\t\t\"x\",\n\t) // c2\n}\n",
		},
		{
			"arguments sharing a line",
			"package x\n\ntype T {\n\tid string @oneOf(\n\t\t\"a\", \"b\", // ab\n\t\t\"c\" // c\n\t)\n}\n",
			"package x\n\ntype T {\n\tid string @oneOf(\n\t\t\"a\", \"b\", // ab\n\t\t\"c\", // c\n\t)\n}\n",
		},
		{
			"array",
			"package x\n\ntype T {\n\tid string @oneOf([\n\t\t\"a\", // c1\n\t\t\"b\" // c2\n\t])\n}\n",
			"package x\n\ntype T {\n\tid string @oneOf([\n\t\t\"a\", // c1\n\t\t\"b\", // c2\n\t])\n}\n",
		},
		{
			"object",
			"package x\n\ntype T {\n\ta string @example({\n\t\tx: 1, // c\n\t\ty: 2\n\t})\n}\n",
			"package x\n\ntype T {\n\ta string @example({\n\t\tx: 1, // c\n\t\ty: 2,\n\t})\n}\n",
		},
		{
			"declaration decorator",
			"package x\n\n@doc(\n\t\"a\", // a\n\t\"b\" // b\n)\nmiddleware M\n",
			"package x\n\n@doc(\n\t\"a\", // a\n\t\"b\", // b\n)\nmiddleware M\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatExact(t, c.src, c.want) })
	}
}

// A comment on the line where an argument list closes follows what comes after
// the closer there, another decorator included, and never joins the list.
func TestFormatKeepsTheCommentOfAListsClosingLineAfterIt(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"decorator after the list",
			"package x\n\ntype T {\n\tname string @length(3,\n\t\t20) @pattern(\"^[a-z]+$\") // c\n}\n",
			"package x\n\ntype T {\n\tname string @length(3, 20) @pattern(\"^[a-z]+$\") // c\n}\n",
		},
		{
			"list kept over lines",
			"package x\n\ntype T {\n\tname string @length(3, // min\n\t\t20) @pattern(\"^[a-z]+$\") // c\n}\n",
			"package x\n\ntype T {\n\tname string @length(\n\t\t3, // min\n\t\t20,\n\t) @pattern(\"^[a-z]+$\") // c\n}\n",
		},
		{
			"array argument",
			"package x\n\ntype T {\n\tid string @oneOf([\"a\",\n\t\t\"b\"]) @doc(\"d\") // c\n}\n",
			"package x\n\ntype T {\n\tid string @oneOf([\"a\", \"b\"]) @doc(\"d\") // c\n}\n",
		},
		{
			"declaration decorator",
			"package x\n\n@tags(\"a\",\n\t\"b\") // c\nservice S {\n\tget A /a {}\n}\n",
			"package x\n\n@tags(\"a\", \"b\") // c\nservice S {\n\tget A /a {}\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatExact(t, c.src, c.want) })
	}
}

// The member after one written over several lines gets a blank line above it
// only where the source has one between the two.
func TestFormatBlankLinesAfterAListOverSeveralLines(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"on the line the arguments above end",
			"package x\n\ntype T {\n\ta string @doc(\n\n\t\t\"x\") b string\n}\n",
			"package x\n\ntype T {\n\ta string @doc(\"x\")\n\tb string\n}\n",
		},
		{
			"on the line the type above ends",
			"package x\n\ntype T {\n\ta map<string,\n\n\t\tint> b string\n}\n",
			"package x\n\ntype T {\n\ta map<string, int>\n\tb string\n}\n",
		},
		{
			"on the line the mixin above ends",
			"package x\n\ntype T {\n\tPage<\n\n\t\tstring> b string\n}\n",
			"package x\n\ntype T {\n\tPage<string>\n\tb string\n}\n",
		},
		{
			"on the line the enum value above ends",
			"package x\n\nenum E {\n\tA @doc(\n\n\t\t\"x\") B\n}\n",
			"package x\n\nenum E {\n\tA @doc(\"x\")\n\tB\n}\n",
		},
		{
			"on the line the clause above ends",
			"package x\n\nservice S {\n\tget A /a {\n\t\trequest Box<\n\n\t\t\tint> response R\n\t}\n}\n",
			"package x\n\nservice S {\n\tget A /a {\n\t\trequest  Box<int>\n\t\tresponse R\n\t}\n}\n",
		},
		{
			"kept lines",
			"package x\n\ntype T {\n\tid string @oneOf(\n\t\t\"a\", // first\n\t\t\"b\" // second\n\t)\n\tname string\n}\n",
			"package x\n\ntype T {\n\tid   string @oneOf(\n\t\t\"a\", // first\n\t\t\"b\", // second\n\t)\n\tname string\n}\n",
		},
		{
			"joined",
			"package x\n\ntype T {\n\tid string @length(\n\t\t1,\n\t\t64\n\t)\n\tname string\n\n\tage int\n}\n",
			"package x\n\ntype T {\n\tid   string @length(1, 64)\n\tname string\n\n\tage  int\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) { formatExact(t, c.src, c.want) })
	}
}

// Format refuses to put two comments on one line, and says so.
func TestFormatRefusesTwoCommentsOnOneLine(t *testing.T) {
	src := "package x\n\ntype T {\n\ta // c1\n\t\tstring // c2\n}\n"
	out, diags := Format("t.craftgo", src)
	want := `t.craftgo:5:10: formatting would put the comments "c1" and "c2" on one line`
	if len(diags) != 1 || diags[0].Error() != want || out != src {
		t.Fatalf("diagnostics %v, want %q with the source unchanged:\n%s", diags, want, out)
	}
}

// TestFormatBlankRunsCollapse pins that a run of blank lines collapses to one.
func TestFormatBlankRunsCollapse(t *testing.T) {
	src := `package demo

type User {
	id string



	// far-away section


	name string
}
`
	want := `package demo

type User {
	id   string

	// far-away section

	name string
}
`
	formatExact(t, src, want)
}

// TestFormatNoBlankInsertedAfterBrace pins that a comment directly under an
// opening brace gets no blank line above it.
func TestFormatNoBlankInsertedAfterBrace(t *testing.T) {
	canonical := `package demo

type User {
	// Section: Identity

	id string
}
`
	formatExact(t, canonical, canonical)
}

// TestFormatEmptyMethodTrailingNote pins the trailing comment after an empty
// method body `{}`.
func TestFormatEmptyMethodTrailingNote(t *testing.T) {
	canonical := `package demo

@passthrough
service Raw {
	get stream /stream {}  // bypasses the JSON codec
}
`
	if out := formatStable(t, canonical); !strings.Contains(out, "// bypasses the JSON codec") {
		t.Errorf("empty-body trailing note dropped:\n%s", out)
	}
}

// TestFormatTopLevelBlocksKeepBlankSeparation pins that two file-scope comment
// blocks separated by a blank line stay two blocks.
func TestFormatTopLevelBlocksKeepBlankSeparation(t *testing.T) {
	canonical := `package demo

// First commented-out probe:
//
// scalar OptionalEmail string? @format(email)

// Second block explaining the decl below.
type User {
	id string
}

// end of file note
`
	formatExact(t, canonical, canonical)
}

// TestFormatDetachedCommentAbovePackage pins that a comment block separated
// from the package line by a blank line stays detached from it.
func TestFormatDetachedCommentAbovePackage(t *testing.T) {
	canonical := `// file prologue, not the package doc

package demo

type User {
	id string
}
`
	formatExact(t, canonical, canonical)
}

// TestFormatMethodGroupingPreserved pins that adjacent methods stay adjacent
// and blank-separated methods keep one blank line.
func TestFormatMethodGroupingPreserved(t *testing.T) {
	canonical := `package demo

service Pings {
	get a /a {}
	get b /b {}

	get c /c {}
}
`
	formatExact(t, canonical, canonical)
}
