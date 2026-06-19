package semantic

import (
	"strings"
	"testing"
)

// analyzeOneFile runs a single-package analysis and returns the diagnostics.
func analyzeOneFile(t *testing.T, src string) []Diagnostic {
	t.Helper()
	files := parseFiles(t, src)
	_, diags := Analyze(files)
	return diags
}

func hasDiagContaining(diags []Diagnostic, substr string) bool {
	for _, d := range diags {
		if strings.Contains(d.Msg, substr) {
			return true
		}
	}
	return false
}

// A numeric bound that overflows the scalar's primitive must be rejected
// at the scalar DECLARATION, matching the field path (else codegen emits
// non-compiling Go like `if uint8(v) > 300`).
func TestScalarDeclBoundCapacityRejected(t *testing.T) {
	for _, src := range []string{
		"package p\nscalar X uint8 @lte(300)\n",
		"package p\nscalar X int8 @gte(200)\n",
		"package p\nscalar X uint16 @gt(70000)\n",
	} {
		diags := analyzeOneFile(t, src)
		if !hasDiagContaining(diags, "exceeds") {
			t.Errorf("expected capacity reject for %q, got: %v", strings.TrimSpace(src), diags)
		}
	}
}

// @lt(0) / @negative on an unsigned scalar declaration is an always-false
// validator - reject like the field path does.
func TestScalarDeclUnsignedContradictionRejected(t *testing.T) {
	for _, src := range []string{
		"package p\nscalar X uint @lt(0)\n",
		"package p\nscalar X uint8 @negative\n",
	} {
		diags := analyzeOneFile(t, src)
		if !hasDiagContaining(diags, "cannot apply to an unsigned") {
			t.Errorf("expected unsigned-contradiction reject for %q, got: %v", strings.TrimSpace(src), diags)
		}
	}
}

// An in-range scalar bound stays clean (the capacity check must not over-fire).
func TestScalarDeclBoundInRangeClean(t *testing.T) {
	diags := analyzeOneFile(t, "package p\nscalar X uint8 @lte(200) @gte(1)\n")
	if hasDiagContaining(diags, "exceeds") {
		t.Errorf("in-range scalar bound wrongly rejected: %v", diags)
	}
}

// The 1-arg exact-length form `@length(-1)` must be rejected (it otherwise
// emits an always-true reject while OpenAPI advertises no constraint).
func TestNegativeExactLengthRejected(t *testing.T) {
	diags := analyzeOneFile(t, "package p\ntype T { a string @length(-1) }\n")
	if !hasDiagContaining(diags, "exact length must be") {
		t.Errorf("expected @length(-1) reject, got: %v", diags)
	}
}

// Explicit `@path @default` must be rejected, mirroring the auto-@path form
// (a path segment is always supplied, so the default can never apply).
func TestExplicitPathDefaultRejected(t *testing.T) {
	src := `package p
type R { id string @path @default("x") }
type Resp { x string }
service S { get M /u/{id} { request R  response Resp } }`
	diags := analyzeOneFile(t, src)
	if !hasDiagContaining(diags, "@default cannot be combined with @path") {
		t.Errorf("expected explicit @path @default reject, got: %v", diags)
	}
}

// A no-content success status on a body-returning method must be rejected.
func TestNoContentStatusWithBodyRejected(t *testing.T) {
	src := `package p
type Out { ok bool }
type Req { id string @path }
service S {
  @status(204)
  get G /things/{id} { request Req  response Out }
}`
	diags := analyzeOneFile(t, src)
	if !hasDiagContaining(diags, "no-content status and cannot carry a response body") {
		t.Errorf("expected @status(204)+body reject, got: %v", diags)
	}
}

// A field named like a path segment but diverted to @query no longer
// satisfies the path-coverage check - the {id} segment is reported missing.
func TestWireBoundFieldDoesNotCoverPathSegment(t *testing.T) {
	src := `package p
type R { id string @query }
type Resp { x string }
service S { get M /u/{id} { request R  response Resp } }`
	diags := analyzeOneFile(t, src)
	if len(diags) == 0 {
		t.Fatalf("expected a path-coverage diagnostic for the diverted {id} field")
	}
	if !hasDiagContaining(diags, "path segment") && !hasDiagContaining(diags, "no matching field") {
		t.Errorf("expected path-coverage reject, got: %v", diags)
	}
}

// A bare scalar/enum request type has no fields to bind/decode - reject it.
func TestBareScalarEnumRequestRejected(t *testing.T) {
	for _, src := range []string{
		"package p\nscalar Token string\nservice S { post Do /do { request Token  response Token } }",
		"package p\nenum Color { red green }\nservice S { post Do /do { request Color  response Color } }",
	} {
		diags := analyzeOneFile(t, src)
		if !hasDiagContaining(diags, "has no fields to bind or decode") {
			t.Errorf("expected bare scalar/enum request reject for %q, got: %v", strings.TrimSpace(src), diags)
		}
	}
}

// An out-of-capacity INTEGRAL-FLOAT bound must be rejected like the int form.
func TestIntegralFloatBoundCapacityRejected(t *testing.T) {
	diags := analyzeOneFile(t, "package p\ntype T { a int8 @gte(300.0) }\n")
	if !hasDiagContaining(diags, "exceeds") {
		t.Errorf("expected integral-float capacity reject, got: %v", diags)
	}
}

// @default on a file field is rejected (no literal default form).
func TestDefaultOnFileRejected(t *testing.T) {
	diags := analyzeOneFile(t, "package p\ntype U { blob file @form @default(\"x\") }\nservice S { post Up /up { request U  response U } }")
	if !hasDiagContaining(diags, "@default is not supported on a `file`") {
		t.Errorf("expected @default-on-file reject, got: %v", diags)
	}
}

// An auto-bound path field with a struct type is rejected; a cross-pkg
// scalar path field must NOT be false-rejected.
func TestAutoPathNonBindableRejected(t *testing.T) {
	diags := analyzeOneFile(t, "package p\ntype Inner { a string }\ntype R { id Inner }\ntype Resp { ok bool }\nservice S { get G /u/{id} { request R  response Resp } }")
	if !hasDiagContaining(diags, "@path requires a non-optional") {
		t.Errorf("expected auto-path non-bindable reject, got: %v", diags)
	}
}

// Repeated @errors (extend-service idiom) must NOT be false-rejected as a
// duplicate decorator.
func TestRepeatedErrorsNotDuplicate(t *testing.T) {
	src := `package p
type Resp { ok bool }
error NotFound Gone {}
error Conflict Taken {}
service S {
  @errors(Gone)
  @errors(Taken)
  get X /x { response Resp }
}`
	diags := analyzeOneFile(t, src)
	if hasDiagContaining(diags, "duplicate decorator") {
		t.Errorf("repeated @errors wrongly rejected as duplicate: %v", diags)
	}
}

// @status(205) (Reset Content) with a response body is rejected.
func TestStatus205WithBodyRejected(t *testing.T) {
	src := `package p
type Req { n string }
type Resp { ok bool }
service S {
  @status(205)
  post M /m { request Req  response Resp }
}`
	diags := analyzeOneFile(t, src)
	if !hasDiagContaining(diags, "no-content status and cannot carry a response body") {
		t.Errorf("expected @status(205)+body reject, got: %v", diags)
	}
}

// W2: a scalar declaration with contradictory pair bounds is rejected
// (pair-ordering now runs on scalar decls, not only fields).
func TestScalarDeclPairOrderingRejected(t *testing.T) {
	for _, src := range []string{
		"package p\nscalar Score int @gte(100) @lte(10)\n",
		"package p\nscalar Name string @minLength(10) @maxLength(5)\n",
	} {
		diags := analyzeOneFile(t, src)
		if !hasDiagContaining(diags, "must be ≥") && !hasDiagContaining(diags, "must be ≤") {
			t.Errorf("expected scalar pair-ordering reject for %q, got: %v", strings.TrimSpace(src), diags)
		}
	}
}

// W1 (#22): @example is now type-checked against the field like @default -
// a kind mismatch and a non-member enum example are rejected.
func TestExampleTypeChecked(t *testing.T) {
	cases := map[string]bool{ // src -> expectReject
		`package p
type T { count int @example("nope") }`: true,
		`package p
enum Color { Red Green }
type T { c Color @example(Purple) }`: true,
		`package p
enum Color { Red Green }
type T { c Color @example(Green) }`: false,
		`package p
type T { name string @example("alice") }`: false,
	}
	for src, expectReject := range cases {
		diags := analyzeOneFile(t, src)
		got := hasDiagContaining(diags, "requires a") || hasDiagContaining(diags, "not a value of enum") || hasDiagContaining(diags, "must reference an enum")
		if got != expectReject {
			t.Errorf("@example type-check: reject=%v want=%v for:\n%s\ndiags: %v", got, expectReject, src, diags)
		}
	}
}

// Parity: @default and @example reject the SAME type mismatch - they share
// checkLiteralType, so a string literal on an int field fails for both.
func TestParityDefaultExampleShareTypeCheck(t *testing.T) {
	defDiags := analyzeOneFile(t, "package p\ntype T { n int @default(\"nope\") }")
	exDiags := analyzeOneFile(t, "package p\ntype T { n int @example(\"nope\") }")
	defRej := hasDiagContaining(defDiags, "requires a")
	exRej := hasDiagContaining(exDiags, "requires a")
	if !defRej || !exRej {
		t.Errorf("@default/@example type-check parity broken: default rejected=%v, example rejected=%v", defRej, exRej)
	}
}
