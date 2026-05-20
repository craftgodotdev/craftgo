package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
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
	d := expectDiag(t, `service S {
	@errors(MysteryError)
	get GetUser /u {}
}`, CodeDecoratorRef)
	expectMessage(t, d, "MysteryError")
}

func TestErrorsRefArrayShortcut(t *testing.T) {
	d := expectDiag(t, `error NotFound UserNotFound
service S {
	@errors([UserNotFound, MysteryError])
	get GetUser /u {}
}`, CodeDecoratorRef)
	expectMessage(t, d, "MysteryError")
}

// ---------- @middlewares ----------

func TestMiddlewareRefResolved(t *testing.T) {
	mustClean(t, `middleware Auth
@middlewares(Auth)
service S {}`)
}

func TestMiddlewareRefUnknown(t *testing.T) {
	expectDiag(t, `@middlewares(Auth)
service S {}`, CodeDecoratorRef)
}

func TestMiddlewareRefOnMethod(t *testing.T) {
	expectDiag(t, `service S {
	@middlewares(Bogus)
	get GetUser /u {}
}`, CodeDecoratorRef)
}

// ---------- @requiresOneOf / @mutuallyExclusive ----------

func TestRequiresOneOfFieldExists(t *testing.T) {
	mustClean(t, `@requiresOneOf(email, phone)
type Contact { email string?  phone string? }`)
}

func TestRequiresOneOfFieldMissing(t *testing.T) {
	d := expectDiag(t, `@requiresOneOf(email, fax)
type Contact { email string? }`, CodeDecoratorRef)
	expectMessage(t, d, "fax")
}

func TestMutuallyExclusiveFieldMissing(t *testing.T) {
	expectDiag(t, `@mutuallyExclusive(["a", "missing"])
type T { a string? }`, CodeDecoratorRef)
}

// ---------- @security with Options ----------

func TestSecurityRefSkippedWithoutOptions(t *testing.T) {
	// No SecuritySchemes set on Options → check is skipped.
	mustClean(t, `@security(unknown)
service S {}`)
}

// expectRefWithOptions runs the analyzer with explicit Options and
// asserts a CodeDecoratorRef diag fires; returns the diagnostic so
// callers can chain message assertions. Wraps the AnalyzeWith path
// the security-scheme tests need without rebuilding the boilerplate.
func expectRefWithOptions(t *testing.T, src string, opts Options) *Diagnostic {
	t.Helper()
	_, diags := AnalyzeWith(parseFiles(t, src), opts)
	d := findCode(diags, CodeDecoratorRef)
	if d == nil {
		t.Fatalf("expected %s, got %v", CodeDecoratorRef, codes(diags))
	}
	return d
}

// expectNoRefWithOptions is the negative twin - checks that no
// CodeDecoratorRef fires under the supplied Options, so positive
// security/scheme tests stay similarly compact.
func expectNoRefWithOptions(t *testing.T, src string, opts Options) {
	t.Helper()
	_, diags := AnalyzeWith(parseFiles(t, src), opts)
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Fatalf("did not expect %s, got %q", CodeDecoratorRef, d.Msg)
	}
}

func TestSecurityRefValidatedWithOptions(t *testing.T) {
	d := expectRefWithOptions(t, `@security(unknown)
service S {}`, Options{SecuritySchemes: []string{"bearerAuth"}})
	expectMessage(t, d, "bearerAuth")
}

func TestSecurityRefAcceptsKnown(t *testing.T) {
	expectNoRefWithOptions(t, `@security(bearerAuth)
service S {}`, Options{SecuritySchemes: []string{"bearerAuth", "apiKey"}})
}

// TestSecurityRefAcceptsIgnoreSecurity covers the public-endpoint
// pattern: rather than threading a sentinel scheme name through
// `@security(...)`, the method opts out via `@ignoreSecurity`. The
// ref-pass should never see `@ignoreSecurity` as a scheme reference,
// so the SecuritySchemes manifest list is irrelevant for that method.
func TestSecurityRefAcceptsIgnoreSecurity(t *testing.T) {
	expectNoRefWithOptions(t, `service S {
	@ignoreSecurity
	get Public /p {}
}`, Options{SecuritySchemes: []string{"bearerAuth"}})
}

func TestSecurityRefSkipsNonIdentArg(t *testing.T) {
	// Args pass already flagged the type - refs pass should silently skip.
	expectNoRefWithOptions(t, `@security(123)
service S {}`, Options{SecuritySchemes: []string{"bearerAuth"}})
}

func TestSecurityRefSkipsZeroArgs(t *testing.T) {
	// `@security` with no args - args pass diags arity. Refs pass silently
	// skips because there's no name to resolve.
	expectNoRefWithOptions(t, `@security
service S {}`, Options{SecuritySchemes: []string{"bearerAuth"}})
}

// ---------- collect / structure ----------

func TestExtendServiceMiddlewareIsChecked(t *testing.T) {
	// `extend service` body methods are walked too.
	expectDiag(t, `service S {}
extend service S {
	@middlewares(Bogus)
	get Op /x {}
}`, CodeDecoratorRef)
}

func TestRefsNilDecoratorTolerated(t *testing.T) {
	// Defensive guard - parser doesn't emit nil entries today.
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
