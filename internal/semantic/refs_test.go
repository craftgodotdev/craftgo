package semantic

import (
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/ast"
)

// ---------- @errors ----------

func TestErrorsRefResolved(t *testing.T) {
	mustClean(t, `error NotFound UserNotFound
service S {
	@errors(UserNotFound)
	get GetUser /u {}
}`)
}

func TestErrorsRefUnknown(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	@errors(MysteryError)
	get GetUser /u {}
}`))
	d := findCode(diags, CodeDecoratorRef)
	if d == nil {
		t.Fatalf("expected decorator/ref diag, got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "MysteryError") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestErrorsRefArrayShortcut(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `error NotFound UserNotFound
service S {
	@errors([UserNotFound, MysteryError])
	get GetUser /u {}
}`))
	d := findCode(diags, CodeDecoratorRef)
	if d == nil {
		t.Fatalf("expected ref diag, got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "MysteryError") {
		t.Errorf("msg = %q", d.Msg)
	}
}

// ---------- @middlewares ----------

func TestMiddlewareRefResolved(t *testing.T) {
	mustClean(t, `middleware Auth
@middlewares(Auth)
service S {}`)
}

func TestMiddlewareRefUnknown(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@middlewares(Auth)
service S {}`))
	if findCode(diags, CodeDecoratorRef) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestMiddlewareRefOnMethod(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	@middlewares(Bogus)
	get GetUser /u {}
}`))
	if findCode(diags, CodeDecoratorRef) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// ---------- @requiresOneOf / @mutuallyExclusive ----------

func TestRequiresOneOfFieldExists(t *testing.T) {
	mustClean(t, `@requiresOneOf(email, phone)
type Contact { email string?  phone string? }`)
}

func TestRequiresOneOfFieldMissing(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@requiresOneOf(email, fax)
type Contact { email string? }`))
	d := findCode(diags, CodeDecoratorRef)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "fax") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestMutuallyExclusiveFieldMissing(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@mutuallyExclusive(["a", "missing"])
type T { a string? }`))
	if findCode(diags, CodeDecoratorRef) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// ---------- @security with Options ----------

func TestSecurityRefSkippedWithoutOptions(t *testing.T) {
	// No SecuritySchemes set on Options → check is skipped.
	mustClean(t, `@security(unknown)
service S {}`)
}

func TestSecurityRefValidatedWithOptions(t *testing.T) {
	files := parseFiles(t, `@security(unknown)
service S {}`)
	_, diags := AnalyzeWith(files, Options{SecuritySchemes: []string{"bearerAuth"}})
	d := findCode(diags, CodeDecoratorRef)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "bearerAuth") {
		t.Errorf("expected hint to list known schemes, got %q", d.Msg)
	}
}

func TestSecurityRefAcceptsKnown(t *testing.T) {
	files := parseFiles(t, `@security(bearerAuth)
service S {}`)
	_, diags := AnalyzeWith(files, Options{SecuritySchemes: []string{"bearerAuth", "apiKey"}})
	if findCode(diags, CodeDecoratorRef) != nil {
		t.Fatalf("expected no ref diag, got %v", codes(diags))
	}
}

func TestSecurityRefAcceptsNoauth(t *testing.T) {
	files := parseFiles(t, `service S {
	@security(noauth)
	get Public /p {}
}`)
	_, diags := AnalyzeWith(files, Options{SecuritySchemes: []string{"bearerAuth"}})
	if findCode(diags, CodeDecoratorRef) != nil {
		t.Fatalf("noauth should be allowed, got %v", codes(diags))
	}
}

func TestSecurityRefSkipsNonIdentArg(t *testing.T) {
	// Args pass already flagged the type — refs pass should silently skip.
	files := parseFiles(t, `@security(123)
service S {}`)
	_, diags := AnalyzeWith(files, Options{SecuritySchemes: []string{"bearerAuth"}})
	if findCode(diags, CodeDecoratorRef) != nil {
		t.Errorf("ref pass should not stack on argtype, got %v", codes(diags))
	}
}

func TestSecurityRefSkipsZeroArgs(t *testing.T) {
	// `@security` with no args — args pass diags arity. Refs pass silently
	// skips because there's no name to resolve.
	files := parseFiles(t, `@security
service S {}`)
	_, diags := AnalyzeWith(files, Options{SecuritySchemes: []string{"bearerAuth"}})
	if findCode(diags, CodeDecoratorRef) != nil {
		t.Errorf("ref pass should not stack on arity-zero security, got %v", codes(diags))
	}
}

// ---------- collect / structure ----------

func TestExtendServiceMiddlewareIsChecked(t *testing.T) {
	// `extend service` body methods are walked too.
	_, diags := Analyze(parseFiles(t, `service S {}
extend service S {
	@middlewares(Bogus)
	get Op /x {}
}`))
	if findCode(diags, CodeDecoratorRef) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestRefsNilDecoratorTolerated(t *testing.T) {
	// Defensive guard — parser doesn't emit nil entries today.
	a := &analyzer{pkg: &Package{
		Errors:      map[string]*ast.ErrorDecl{},
		Middlewares: map[string]*ast.MiddlewareDecl{},
	}}
	// Empty body decorators slice with a nil entry.
	a.checkFieldGroupRefs("X", []*ast.Decorator{nil}, nil)
	a.checkServiceLevelRefs([]*ast.Decorator{nil})
	// Build a synthetic method with a nil decorator.
	a.checkMethodLevelRefs(&ast.Method{Decorators: []*ast.Decorator{nil}})
	if len(a.diags) != 0 {
		t.Errorf("nil decorator entries should not diag, got %v", a.diags)
	}
}
