package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// resolveTypeFields resolves td's fields, mixins flattened, with their Go rendering.
func resolveTypeFields(td *ast.TypeDecl, pkg *semantic.Package) []resolvedField {
	var out []resolvedField
	for _, rf := range semantic.ResolveFields(td, "", pkg, resolverFor(pkg, nil).Resolver, resolvedGoFieldNames) {
		out = append(out, decorate(rf))
	}
	return out
}

// An undecorated request field binds to @path when a segment matches it, else to @query on a
// body-less verb or the body on a body verb; an explicit binding wins.
func TestResolveRequestFields(t *testing.T) {
	pkg := analyze(t, `package design
type GetReq { id string  q string?  hdr string @header("H") }
type PostReq { id string  body string }
service S {
	get Get /items/{id} { request GetReq }
	post Create /items/{id} { request PostReq }
}`)
	method := func(name string) *ast.Method {
		for _, si := range pkg.Services {
			for _, m := range si.Methods {
				if m.Name == name {
					return m
				}
			}
		}
		t.Fatalf("method %s not found", name)
		return nil
	}
	bind := func(m *ast.Method) map[string]wire.Binding {
		out := map[string]wire.Binding{}
		for _, rf := range resolveRequestFields(m, pkg, resolverFor(pkg, nil)) {
			out[rf.DSLName] = rf.Binding
		}
		return out
	}

	get := bind(method("Get"))
	if get["id"] != wire.BindPath {
		t.Errorf("GET id (matches {id}) -> %v, want BindPath", get["id"])
	}
	if get["q"] != wire.BindQuery {
		t.Errorf("GET q (un-decorated, body-less verb) -> %v, want BindQuery", get["q"])
	}
	if get["hdr"] != wire.BindHeader {
		t.Errorf("GET hdr (@header) -> %v, want BindHeader", get["hdr"])
	}

	post := bind(method("Create"))
	if post["id"] != wire.BindPath {
		t.Errorf("POST id (matches {id}) -> %v, want BindPath", post["id"])
	}
	if post["body"] != wire.BindBody {
		t.Errorf("POST body (un-decorated, body verb) -> %v, want BindBody", post["body"])
	}
}

// resolveFields flattens mixins and resolves each field's wire and presence facts.
func TestResolveFields(t *testing.T) {
	pkg := analyze(t, `package design
type Audit { createdAt string @header("X-Created") }
type Req {
	Audit
	name   string  @minLength(1)
	sort   string? @default("asc")
	bio    string  @nullable
	token  string  @query("t")
	secret string  @sensitive
	tags   int[]   @nullable @minItems(1)
}`)
	td := pkg.Types["Req"]
	if td == nil {
		t.Fatal("Req not found")
	}
	got := resolveTypeFields(td, pkg)
	byName := map[string]resolvedField{}
	for _, rf := range got {
		byName[rf.DSLName] = rf
	}

	if _, ok := byName["createdAt"]; !ok {
		t.Fatalf("mixin field createdAt not flattened in: %v", names(got))
	}

	cases := []struct {
		name string
		want resolvedField
	}{
		// createdAt: @header via mixin; a non-optional header is a required parameter.
		{"createdAt", resolvedField{ResolvedField: semantic.ResolvedField{Binding: wire.BindHeader, OnWireBody: false, SpecRequired: true}}},
		// name: plain required body field.
		{"name", resolvedField{ResolvedField: semantic.ResolvedField{Binding: wire.BindBody, OnWireBody: true, SpecRequired: true}, IsPointer: false}},
		// sort: optional (`?`) → pointer + nil-guard; @default → never required.
		{"sort", resolvedField{ResolvedField: semantic.ResolvedField{Binding: wire.BindBody, OnWireBody: true, NeedsNilGuard: true, HasDefault: true, SpecRequired: false}, IsPointer: true}},
		// bio: @nullable → pointer + nil-guard, still required (the key is sent, maybe as null).
		{"bio", resolvedField{ResolvedField: semantic.ResolvedField{Binding: wire.BindBody, OnWireBody: true, NeedsNilGuard: true, SpecRequired: true}, IsPointer: true}},
		// token: @query - off the body, wire name from the arg.
		{"token", resolvedField{ResolvedField: semantic.ResolvedField{Binding: wire.BindQuery, OnWireBody: false, SpecRequired: true}}},
		// secret: @sensitive - off the body; SpecRequired stays true, the schema skips the field.
		{"secret", resolvedField{ResolvedField: semantic.ResolvedField{Binding: wire.BindSensitive, OnWireBody: false, SpecRequired: true}}},
		// tags: @nullable nilable slice → nil-guarded but not a pointer.
		{"tags", resolvedField{ResolvedField: semantic.ResolvedField{Binding: wire.BindBody, OnWireBody: true, NeedsNilGuard: true, SpecRequired: true}, IsPointer: false}},
	}
	for _, c := range cases {
		rf := byName[c.name]
		if rf.Binding != c.want.Binding {
			t.Errorf("%s: Binding = %v, want %v", c.name, rf.Binding, c.want.Binding)
		}
		if rf.OnWireBody != c.want.OnWireBody {
			t.Errorf("%s: OnWireBody = %v, want %v", c.name, rf.OnWireBody, c.want.OnWireBody)
		}
		if rf.IsPointer != c.want.IsPointer {
			t.Errorf("%s: IsPointer = %v, want %v", c.name, rf.IsPointer, c.want.IsPointer)
		}
		if rf.NeedsNilGuard != c.want.NeedsNilGuard {
			t.Errorf("%s: NeedsNilGuard = %v, want %v", c.name, rf.NeedsNilGuard, c.want.NeedsNilGuard)
		}
		if rf.HasDefault != c.want.HasDefault {
			t.Errorf("%s: HasDefault = %v, want %v", c.name, rf.HasDefault, c.want.HasDefault)
		}
		if rf.SpecRequired != c.want.SpecRequired {
			t.Errorf("%s: SpecRequired = %v, want %v", c.name, rf.SpecRequired, c.want.SpecRequired)
		}
	}

	if wn := byName["createdAt"].WireName(); wn != "X-Created" {
		t.Errorf("createdAt WireName = %q, want X-Created", wn)
	}
	if wn := byName["token"].WireName(); wn != "t" {
		t.Errorf("token WireName = %q, want t", wn)
	}
	if dv := byName["sort"].DefaultWire; dv != "asc" {
		t.Errorf("sort DefaultWire = %v, want asc", dv)
	}

	// A @sensitive field is off the wire, so it gets no runtime presence check.
	if byName["secret"].RuntimeEnforced {
		t.Errorf("secret (@sensitive): RuntimeEnforced = true, want false (off-wire, presence check unsatisfiable)")
	}
}

// A SpecRequired field is never optional or defaulted, and differs from RuntimeEnforced only by
// @default or @nullable.
func TestResolveFieldsInvariant(t *testing.T) {
	pkg := analyze(t, `package design
type T {
	a string
	b string?
	c string  @default("x")
	d string? @default("x")
	e string  @nullable
}`)
	for _, rf := range resolveTypeFields(pkg.Types["T"], pkg) {
		optional := rf.Field.Type != nil && rf.Field.Type.Optional
		nullable := ast.HasDecorator(rf.Field.Decorators, "nullable")
		if rf.SpecRequired && (optional || rf.HasDefault) {
			t.Errorf("%s: SpecRequired but optional=%v hasDefault=%v - required[] must exclude both",
				rf.DSLName, optional, rf.HasDefault)
		}
		if !rf.SpecRequired && !optional && !rf.HasDefault {
			t.Errorf("%s: not SpecRequired yet neither optional nor defaulted", rf.DSLName)
		}
		// RuntimeEnforced excludes optional and @nullable fields but not defaulted ones.
		if rf.RuntimeEnforced != (!optional && !nullable) {
			t.Errorf("%s: RuntimeEnforced=%v, want %v", rf.DSLName, rf.RuntimeEnforced, !optional && !nullable)
		}
		if rf.SpecRequired != rf.RuntimeEnforced {
			divergeByDefault := rf.HasDefault && !optional && !nullable
			divergeByNullable := nullable && !optional && !rf.HasDefault
			if !divergeByDefault && !divergeByNullable {
				t.Errorf("%s: SpecRequired(%v) != RuntimeEnforced(%v) for an unexpected reason (default=%v nullable=%v)",
					rf.DSLName, rf.SpecRequired, rf.RuntimeEnforced, rf.HasDefault, nullable)
			}
		}
	}
}

func names(fs []resolvedField) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.DSLName
	}
	return out
}

// Fields colliding on one Go name keep their deduplicated names in the binder and the validator.
func TestCollidingGoFieldNamesDedupAcrossConsumers(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"m/m.craftgo": `package m
@requiresOneOf(userId, user_id)
type R {
  userId  string? @minLength(2)
  user_id string?
  sortBy  string? @query("sortBy")
  sort_by string? @query("sort_by")
}
type Resp { ok bool }
service S {
  post Echo /e { request R  response Resp }
}`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	// The collision is a warning; only errors fail the test.
	for _, d := range diags {
		if d.Severity == lexer.SeverityError {
			t.Fatalf("semantic error: %v", d)
		}
	}
	dir := t.TempDir()
	mPkg := proj.Packages["m"]
	r := buildProjectResolver(proj, newFixtureConfig(), "m")
	if err := generateTypes(mPkg, dir, r); err != nil {
		t.Fatal(err)
	}
	if err := generateValidators(mPkg, dir, &projectResolver{Resolver: semantic.NewResolver(proj, "m")}); err != nil {
		t.Fatal(err)
	}

	var goNames []string
	for _, rf := range resolveRequestFields(mPkg.Services["S"].Methods[0], mPkg, r) {
		goNames = append(goNames, rf.GoName)
	}
	mustContainAll(t, strings.Join(goNames, " "), "UserID", "UserID_2", "SortBy", "SortBy_2")

	val, _ := os.ReadFile(filepath.Join(dir, "m", "validate.go"))
	mustParseGo(t, string(val))
	vs := string(val)
	// The cross-field group reads both deduplicated names.
	mustContainAll(t, vs,
		"v.UserID != nil",
		"v.UserID == nil && v.UserID_2 == nil",
	)
}
