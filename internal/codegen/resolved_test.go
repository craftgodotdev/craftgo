package codegen

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// TestResolveRequestFields pins the method-context auto-binding the IR
// centralises: an un-decorated field auto-binds to @path (name matches a
// segment), @query (body-less verb), or @body (body verb); an explicit
// binding wins. This is the single source for "where does each request
// field ride" — the per-stage walks read it instead of re-deriving.
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
	bind := func(m *ast.Method) map[string]Binding {
		out := map[string]Binding{}
		for _, rf := range resolveRequestFields(m, pkg, nil) {
			out[rf.DSLName] = rf.Binding
		}
		return out
	}

	get := bind(method("Get"))
	if get["id"] != BindPath {
		t.Errorf("GET id (matches {id}) -> %v, want BindPath", get["id"])
	}
	if get["q"] != BindQuery {
		t.Errorf("GET q (un-decorated, body-less verb) -> %v, want BindQuery", get["q"])
	}
	if get["hdr"] != BindHeader {
		t.Errorf("GET hdr (@header) -> %v, want BindHeader", get["hdr"])
	}

	post := bind(method("Create"))
	if post["id"] != BindPath {
		t.Errorf("POST id (matches {id}) -> %v, want BindPath", post["id"])
	}
	if post["body"] != BindBody {
		t.Errorf("POST body (un-decorated, body verb) -> %v, want BindBody", post["body"])
	}
}

// TestResolveFields pins the resolved IR: one flattened, fully-resolved
// view of a type's fields that every stage reads instead of re-deriving.
// Each field's expected facts are asserted explicitly, so a drift in any
// underlying helper (or a stage migrating onto the IR) is caught here.
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
	got := resolveFields(td, pkg, nil)
	byName := map[string]ResolvedField{}
	for _, rf := range got {
		byName[rf.DSLName] = rf
	}

	// The mixin-promoted field is flattened in — a stage reading the IR
	// can't miss it the way the per-stage td.Body walks used to.
	if _, ok := byName["createdAt"]; !ok {
		t.Fatalf("mixin field createdAt not flattened in: %v", names(got))
	}

	cases := []struct {
		name string
		want ResolvedField
	}{
		// createdAt: @header via mixin — off the body; a non-optional header
		// is a required parameter, so SpecRequired (= fieldIsRequired) is true.
		{"createdAt", ResolvedField{Binding: BindHeader, OnWireBody: false, SpecRequired: true}},
		// name: plain required body field.
		{"name", ResolvedField{Binding: BindBody, OnWireBody: true, IsPointer: false, SpecRequired: true}},
		// sort: optional (`?`) → pointer + nil-guard; @default → never required.
		{"sort", ResolvedField{Binding: BindBody, OnWireBody: true, IsPointer: true, NeedsNilGuard: true, HasDefault: true, SpecRequired: false}},
		// bio: @nullable non-optional → pointer + nil-guard, still required (must send key, may be null).
		{"bio", ResolvedField{Binding: BindBody, OnWireBody: true, IsPointer: true, NeedsNilGuard: true, SpecRequired: true}},
		// token: @query — off the body, wire name from the arg.
		{"token", ResolvedField{Binding: BindQuery, OnWireBody: false, SpecRequired: true}},
		// secret: @sensitive — server-only, off the body everywhere. SpecRequired
		// is the raw fieldIsRequired (true here); the schema skips it before use.
		{"secret", ResolvedField{Binding: BindSensitive, OnWireBody: false, SpecRequired: true}},
		// tags: @nullable nilable slice → nil-guarded but NOT a pointer.
		{"tags", ResolvedField{Binding: BindBody, OnWireBody: true, IsPointer: false, NeedsNilGuard: true, SpecRequired: true}},
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

	// @sensitive opts out of the runtime presence check: the field is
	// json:"-" (off the wire), so a presence gate could never be satisfied
	// and would 400 every request (acute for `any @sensitive`, which emits
	// a presence expression where a plain string @sensitive does not).
	if byName["secret"].RuntimeEnforced {
		t.Errorf("secret (@sensitive): RuntimeEnforced = true, want false (off-wire, presence check unsatisfiable)")
	}
}

// TestResolveFieldsInvariant asserts the cross-stage invariant the IR
// exists to guarantee: a field in the OpenAPI required[] (SpecRequired) is
// never optional and never defaulted — the two facts that used to be
// re-derived by separate stages and drift.
func TestResolveFieldsInvariant(t *testing.T) {
	pkg := analyze(t, `package design
type T {
	a string
	b string?
	c string  @default("x")
	d string? @default("x")
	e string  @nullable
}`)
	for _, rf := range resolveFields(pkg.Types["T"], pkg, nil) {
		optional := rf.Field.Type != nil && rf.Field.Type.Optional
		nullable := hasNullableDecorator(rf.Field.Decorators)
		if rf.SpecRequired && (optional || rf.HasDefault) {
			t.Errorf("%s: SpecRequired but optional=%v hasDefault=%v — required[] must exclude both",
				rf.DSLName, optional, rf.HasDefault)
		}
		if !rf.SpecRequired && !optional && !rf.HasDefault {
			t.Errorf("%s: not SpecRequired yet neither optional nor defaulted", rf.DSLName)
		}
		// RuntimeEnforced (validator presence check) excludes optional and
		// @nullable; it does NOT exclude @default. So the two facts diverge
		// exactly on @default (spec-optional but runtime-checked) and
		// @nullable (runtime-skipped but spec-required) — pin that the
		// divergence is only ever for one of those reasons.
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

func names(fs []ResolvedField) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.DSLName
	}
	return out
}
