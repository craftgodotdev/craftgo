package semantic

import (
	"slices"
	"strings"
	"testing"
)

func TestGenericInstanceCorrectArity(t *testing.T) {
	mustClean(t, `type Page<T> { items T[]  total int }
type User { id string }
type UserList { p Page<User>  flag bool }`)
}

func TestGenericMultiArg(t *testing.T) {
	mustClean(t, `type Pair<A, B> { left A  right B }
type User {}
type Org {}
type Team { members Pair<User, Org> }`)
}

func TestGenericNested(t *testing.T) {
	mustClean(t, `type Page<T> { items T[] }
type Box<T> { value T }
type User {}
type Wrapped { p Page<Box<User>> }`)
}

func TestGenericInMapValue(t *testing.T) {
	mustClean(t, `type Page<T> { items T[] }
type User {}
type Index { byTag map<string, Page<User>> }`)
}

func TestGenericInMethodResponse(t *testing.T) {
	mustClean(t, `type Page<T> { items T[] }
type User {}
service S {
	get List /list { response Page<User> }
}`)
}

func TestTypeParamFieldOK(t *testing.T) {
	mustClean(t, `type Page<T> { items T[]  total int }
type User {}
type UserList { p Page<User> }`)
}

func TestGenericArityTooFew(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Pair<A, B> { left A  right B }
type User {}
type X { p Pair<User> }`))
	d := findCode(diags, CodeGenericArity)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "expects 2") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestGenericArityTooMany(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type User {}
type Org {}
type X { p Page<User, Org> }`))
	d := findCode(diags, CodeGenericArity)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "got 2") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestGenericMissingArgsOnGenericRef(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type X { p Page }`))
	if findCode(diags, CodeGenericArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestArgsOnNonGenericType(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type User { id string }
type X { p User<Org> }
type Org {}`))
	d := findCode(diags, CodeGenericNonGeneric)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "User") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestArgsOnTypeParam(t *testing.T) {
	// A type parameter takes no type arguments.
	_, diags := Analyze(parseFiles(t, `type Page<T> { item T<X> }
type X {}`))
	if findCode(diags, CodeGenericNonGeneric) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestGenericOptionalArgRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type Item { id string }
type X { p Page<Item?> }`))
	d := findCode(diags, CodeGenericOptionalArg)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "optional") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestGenericOptionalArgRejectedOnPlainField(t *testing.T) {
	// Nullability belongs inside the generic, never on its argument.
	_, diags := Analyze(parseFiles(t, `type Wrap<T> { value T }
type Item { id string }
type X { p Wrap<Item?> }`))
	if findCode(diags, CodeGenericOptionalArg) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestGenericOptionalArgRejectedInMethod(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type Item { id string }
service S {
	get List /list { response Page<Item?> }
}`))
	if findCode(diags, CodeGenericOptionalArg) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestGenericOptionalArgRejectedNested(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type Box<T> { value T }
type Item { id string }
type X { p Box<Page<Item?>> }`))
	if findCode(diags, CodeGenericOptionalArg) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestGenericMapValueOptionalArgClean(t *testing.T) {
	// Only a `?` on the argument itself is rejected, not one on a map value inside it.
	mustClean(t, `type Page<T> { items T[] }
type Item { id string }
type X { p Page<map<string, Item?>> }`)
}

func TestGenericArrayArgClean(t *testing.T) {
	mustClean(t, `type Page<T> { items T[] }
type Item { id string }
type X { p Page<Item[]> }`)
}

// A generic mixin with the right arity passes the mixin and generics checks.
func TestMixinGenericValidatedByGenerics(t *testing.T) {
	mustClean(t, `type Page<T> { items T[] }
type User {}
type UserList { Page<User>  total int }`)
}

// A generic mixin with the wrong arity reports one mixin arity diagnostic.
func TestMixinSkipsGenericArityCheck(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type X { Page<A, B>  total int }
type A {}
type B {}`))
	if got := codes(diags); !slices.Equal(got, []string{CodeMixinArity}) {
		t.Errorf("want one %s, got %v", CodeMixinArity, diags)
	}
}

// A mixin's arguments are type references of their own.
func TestMixinArgumentArityChecked(t *testing.T) {
	expectError(t, `type Page<T> { items T[] }
type Box<T> { value T }
type X { Page<Box>  total int }`, CodeGenericArity)
}

// An enum, a scalar or a built-in takes no generic arguments.
func TestArgsOnEnumScalarAndBuiltin(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `enum Color { Red Blue }
scalar Email string
type T {
	c Color<int>
	e Email<string>
	s string<int>
}`))
	want := []string{CodeGenericNonGeneric, CodeGenericNonGeneric, CodeGenericNonGeneric}
	if got := codes(diags); !slices.Equal(got, want) {
		t.Errorf("want %v, got %v", want, diags)
	}
}

// The generics check skips an unknown type.
func TestUnknownTypeRefSkipsArity(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { p Foo<Bar> }`))
	if findCode(diags, CodeGenericArity) != nil {
		t.Errorf("unknown ref should not produce arity diag, got %v", codes(diags))
	}
	if findCode(diags, CodeGenericNonGeneric) != nil {
		t.Errorf("unknown ref should not produce non-generic diag, got %v", codes(diags))
	}
}

// A reference into an undeclared package reports the package alone.
func TestQualifiedGenericRefIntoUnknownPackage(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { p shared.Page<User> }
type User {}`))
	if got := codes(diags); !slices.Equal(got, []string{CodeRefUnknownPackage}) {
		t.Errorf("want only %s, got %v", CodeRefUnknownPackage, diags)
	}
}

// A generic with the wrong arity in a map key is reported.
func TestGenericMapKeyArity(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type User {}
type X { byPage map<Page<User, X>, string> }`))
	if findCode(diags, CodeGenericArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// The rules read a field a nested mixin brings with its own type: Meta's `t`
// is the struct T, which a query string cannot carry and whose pointer has a
// present/absent state for a cross-field group, or the comparable scalar T.
func TestGenericMixinLeavesNestedMixinFieldsAlone(t *testing.T) {
	d := expectError(t, `package app
type T { a int }
type Meta { t T? }
type Page<T> { Meta }
type Req { Page<string> }
type Resp { ok bool }
service S { get L /l { request Req  response Resp } }`, CodeBindingType)
	expectMessage(t, d, "Req.t", "T? can't ride a query string")
	mustClean(t, `package app
type T { a int }
type Meta { t T? }
type Page<T> { Meta  other string? }
@requiresOneOf(t, other)
type Req { Page<string[]> }
type Resp { ok bool }
service S { post Make /items { request Req  response Resp } }`)
	mustClean(t, `package app
scalar T string
type Meta { t T }
type Page<T> { Meta }
type Row { Page<string[]> }
type Req { rows Row[] @uniqueItems  pages Page<string[]>[] @uniqueItems }`)
}

// A generic request type binds with its arguments substituted: `id T` on
// `IdHolder<string>` fills the route's {id}, `filter T?` on `Paged<Status>`
// rides the query string, and `T?` over an array is still refused there.
func TestGenericRequestTypeBindsWithItsArguments(t *testing.T) {
	mustClean(t, `package app
type IdHolder<T> { id T }
type Resp { ok bool }
service S { get Get /things/{id} { request IdHolder<string>  response Resp } }`)
	mustClean(t, `package app
enum Status { active  inactive }
type Paged<T> { page int  filter T? }
type Resp { ok bool }
service S { get L /l { request Paged<Status>  response Resp } }`)
	d := expectError(t, `package app
type Box<T> { a T? }
type Resp { ok bool }
service S { get A /a { request Box<string[]>  response Resp } }`, CodeBindingType)
	expectMessage(t, d, "Box.a", "optional type parameter over an array")
}

// A type parameter spelled like a declaration is the parameter: the rules a
// type parameter breaks are enforced although the declaration would pass them.
func TestTypeParamShadowsDeclaration(t *testing.T) {
	for label, c := range map[string]struct{ src, code string }{
		"enum default":   {"enum Color { Red  Green }\ntype Box<Color> { c Color? @default(Red) }", CodeDecoratorConflict},
		"scalar default": {"scalar Blob string\ntype Box<Blob> { v Blob? @default(\"x\") }", CodeDecoratorConflict},
		"query":          {"scalar Key string\ntype Q<Key> { k Key @query }", CodeBindingType},
	} {
		t.Run(label, func(t *testing.T) {
			expectError(t, "package app\n"+c.src, c.code)
		})
	}
}

// A @header or @cookie on a type parameter is legal at the declaration; each
// request, response or error mixin that instantiates the type is checked
// with the argument, at the clause or mixin naming the instance.
func TestTypeParamWireBindingCheckedPerInstance(t *testing.T) {
	const decls = `package app
type Item { id string }
type Paged<T> { count T @header("X-Count")  items T[] }
type Tagged<T> { tag T @cookie("tag") }
type Req<T> { h T @header("X-H")  id string }
type Wrap { Paged<Item> }
`
	mustClean(t, decls+`enum Prio { Low  High }
service S {
	get A /a { response Paged<int> }
	get B /b { response Paged<Prio> }
	get C /c { response Paged<string[]> }
	get D /d { response Tagged<bool> }
	post E /e { request Req<int>  response Item }
}`)
	for label, c := range map[string]struct{ src, msg string }{
		"struct":       {`service S { get A /a { response Paged<Item> } }`, "field Paged<Item>.count: @header requires"},
		"map":          {`service S { get A /a { response Paged<map<string, int>> } }`, "got map<string, int>"},
		"nested array": {`service S { get A /a { response Paged<int[][]> } }`, "field Paged<int[][]>.count: @header cannot bind to a multi-dimensional array"},
		"cookie array": {`service S { get A /a { response Tagged<int[]> } }`, "field Tagged<int[]>.tag: @cookie cannot bind to an array"},
		"request":      {`service S { post A /a { request Req<Item>  response Item } }`, "field Req<Item>.h: @header requires"},
		"mixin":        {`service S { get A /a { response Wrap } }`, "field Wrap.count: @header requires"},
		"error mixin":  {`error Conflict E { Paged<Item> }`, "field Paged<Item>.count: @header requires"},
		"optional over an array": {`type OptH<T> { h T? @header("X-Opt")  n int }
service S { get A /a { response OptH<string[]> } }`, "field OptH<string[]>.h: @header rides an optional type parameter over an array"},
		"optional over an array in a request": {`type OptH<T> { h T? @header("X-Opt")  n int }
service S { get A /a { request OptH<int[]>  response Item } }`, "field OptH<int[]>.h: @header rides an optional type parameter over an array"},
		"optional over an array in an error mixin": {`type OptH<T> { h T? @header("X-Opt")  n int }
error Conflict E { OptH<int[]> }`, "field OptH<int[]>.h: @header rides an optional type parameter over an array"},
	} {
		t.Run(label, func(t *testing.T) {
			src := decls + c.src
			d := expectError(t, src, CodeBindingType)
			expectMessage(t, d, c.msg)
			if want := strings.Count(src, "\n") + 1; d.Pos.Line != want {
				t.Errorf("reported at line %d, want the instantiating line %d", d.Pos.Line, want)
			}
		})
	}
}
