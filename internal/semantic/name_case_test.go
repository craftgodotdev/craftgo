package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// A declaration or method name the generated Go would declare unexported is
// an error naming the declaration's kind and the exported spelling.
func TestDeclNameMustBeExported(t *testing.T) {
	cases := []struct {
		label  string
		src    string
		wantIn []string
	}{
		{"type", `package x
type myType { id string }`, []string{`type name "myType"`, `"MyType"`}},
		{"underscore", `package x
type _User { id string }`, []string{`type name "_User"`, `"User"`}},
		{"error", `package x
error NotFound badName { code string }`, []string{`error name "badName"`, `"BadName"`}},
		{"enum", `package x
enum priority { low high }`, []string{`enum name "priority"`, `"Priority"`}},
		{"scalar", `package x
scalar id string`, []string{`scalar name "id"`, `"Id"`}},
		{"object", `package x
type object { id string }`, []string{`type name "object"`, `"Object"`}},
		{"method", `package x
service S { get listUsers /u {} }`, []string{`method name "listUsers"`, `"ListUsers"`}},
		{"middleware", `package x
middleware auth
service S { @middlewares(auth) get A /a {} }`, []string{`middleware name "auth"`, `"Auth"`}},
		{"event", `package x
type P { id string }
event orderPlaced { payload P }`, []string{`event name "orderPlaced"`, `"OrderPlaced"`}},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			d := expectError(t, c.src, CodeDeclNameCase)
			expectMessage(t, d, c.wantIn...)
		})
	}
}

// A lower-case service name, which names only directories and documents,
// warns.
func TestServiceNameCaseWarns(t *testing.T) {
	d := expectWarning(t, `package x
service userService { }`, CodeDeclNameCase)
	expectMessage(t, d, `service name "userService"`)
}

// PascalCase names pass.
func TestDeclNameCasePascalCasePasses(t *testing.T) {
	mustClean(t, `package x
type MyType { id string }
error NotFound BadName { code string }
enum Priority { Low High }
scalar ID string
middleware Auth
type P { id string }
event OrderPlaced { payload P }
service UserService { @middlewares(Auth) get ListUsers /u {} }`)
}

// A type named like a built-in gets the built-in-name error alone.
func TestDeclNameCaseLeavesBuiltinNames(t *testing.T) {
	expectCodeCount(t, `package x
type string { id int }`, CodeDeclNameCase, 0)
}

// An `extend service` does not report its lower-case service name again.
func TestDeclNameCaseExtendDoesNotDoubleReport(t *testing.T) {
	expectCodeCount(t, `package x
service userService {}
extend service userService {
    get listMore /m {}
}`, CodeDeclNameCase, 2)
}

// A type parameter must start with an uppercase letter, at its type's name.
func TestTypeParamNameMustBeExported(t *testing.T) {
	for param, fix := range map[string]string{"fmt": `"Fmt"`, "v": `"V"`, "time": `"Time"`, "shared": `"Shared"`, "_x": `"X"`} {
		d := expectError(t, "package app\ntype Box<"+param+"> { item "+param+" }", CodeDeclNameCase)
		expectMessage(t, d, `type parameter "`+param+`" of Box must start with an uppercase letter`, "rename it "+fix)
		if d.Pos.Line != 2 || d.Pos.Column != 6 {
			t.Errorf("%s: reported at %d:%d, want the type's name at 2:6", param, d.Pos.Line, d.Pos.Column)
		}
	}
	expectNoCode(t, "package app\ntype Box<T> { item T }\ntype Pair<Key, Value> { k Key  v Value }", CodeDeclNameCase)
}

// An empty name, left by a parse error, is not checked.
func TestDeclNameCaseEmptyNameSkipped(t *testing.T) {
	a := newTestAnalyzer(&Package{})
	a.checkExportedName("type", "", lexer.Position{})
	for _, d := range a.diags {
		if d.Code == CodeDeclNameCase {
			t.Errorf("empty-name decl must not produce a name-case diagnostic; got %q", d.Msg)
		}
	}
}
