package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
)

// ---------- shared output directories (the @group merge) ----------

// TestGroupSharedByTwoServicesClean is the point of the decorator: @group
// lays out folders, so two services choosing one group share a directory
// and their methods merge into that directory's single routes.go. Nothing
// about it is an error.
func TestGroupSharedByTwoServicesClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"demo.craftgo": `package demo
type R { ok bool }
@group("shared/v1")
service Alpha { get A /a { response R } }
@group("shared/v1")
service Beta { get B /b { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) != 0 {
		t.Errorf("services sharing a group must analyse clean, got %v", codes(diags))
	}
}

// TestGroupSharedWithUngroupedServiceDirClean covers the same merge reached
// the other way: a @group naming an ungrouped service's own directory. The
// group replaces the service-name segment, so both land in `beta` - which
// is a layout choice, not a mistake.
func TestGroupSharedWithUngroupedServiceDirClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"demo.craftgo": `package demo
type R { ok bool }
@group("beta")
service Alpha { get A /a { response R } }
service Beta { get B /b { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) != 0 {
		t.Errorf("group naming another service's directory must merge, got %v", codes(diags))
	}
}

// TestGroupPerBlockGroupingClean pins the documented per-block layout: one
// service splitting its own methods across version folders.
func TestGroupPerBlockGroupingClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"demo.craftgo": `package demo
type R { ok bool }
@group("checkout/v1")
service Checkout { get A /a { response R } }
@group("checkout/v2")
extend service Checkout { get B /b { response R } }
extend service Checkout { get C /c { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) != 0 {
		t.Errorf("one service's own blocks must analyse clean, got %v", codes(diags))
	}
}

// ---------- what a shared directory cannot absorb ----------

// TestGroupPackageStraddle pins the first hard limit: a directory is one Go
// package, and generated files take their `package` declaration from the
// DSL package that declared the service. Two DSL packages feeding one
// folder would emit `package alpha` and `package beta` side by side.
func TestGroupPackageStraddle(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/svc.craftgo": `package alpha
type R { ok bool }
@group("shared/v1")
service Alpha { get A /a { response R } }`,
		"b/svc.craftgo": `package beta
type R { ok bool }
@group("shared/v1")
service Beta { get B /b { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeGroupPackageStraddle)
	if d == nil {
		t.Fatalf("expected %s, got %v", CodeGroupPackageStraddle, codes(diags))
	}
	if !strings.Contains(d.Msg, `"shared/v1"`) {
		t.Errorf("message should name the shared directory: %q", d.Msg)
	}
	// Both sites fire so the editor underlines each @group.
	hits := 0
	for _, dd := range diags {
		if dd.Code == CodeGroupPackageStraddle {
			hits++
		}
	}
	if hits != 2 {
		t.Errorf("want 2 straddle diagnostics, got %d", hits)
	}
}

// TestGroupMethodCollision pins the second hard limit: handlers and stubs
// are one file per method, named after it, and so is the handler function -
// so two services in one folder cannot both declare `Ping`.
func TestGroupMethodCollision(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"demo.craftgo": `package demo
type R { ok bool }
@group("shared/v1")
service Alpha { get Ping /alpha/ping { response R } }
@group("shared/v1")
service Beta { get Ping /beta/ping { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeGroupMethodCollision)
	if d == nil {
		t.Fatalf("expected %s, got %v", CodeGroupMethodCollision, codes(diags))
	}
	if !strings.Contains(d.Msg, `"Ping"`) || !strings.Contains(d.Msg, `"shared/v1"`) {
		t.Errorf("message should name the method and the directory: %q", d.Msg)
	}
	hits := 0
	for _, dd := range diags {
		if dd.Code == CodeGroupMethodCollision {
			hits++
		}
	}
	if hits != 2 {
		t.Errorf("want 2 method-collision diagnostics, got %d", hits)
	}
}

// TestGroupMethodNameReuseAcrossDirectoriesClean guards the negative: the
// same method name in two services is fine as long as they do not share a
// directory - that is the ordinary ungrouped layout.
func TestGroupMethodNameReuseAcrossDirectoriesClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"demo.craftgo": `package demo
type R { ok bool }
service Alpha { get Ping /alpha/ping { response R } }
service Beta { get Ping /beta/ping { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeGroupMethodCollision) != nil {
		t.Errorf("separate directories must not collide, got %v", codes(diags))
	}
}

// TestGroupChecksHonourFileCase pins that the ungrouped side of the
// comparison uses the project's output.fileCase: `UserService` occupies
// `user-service` under kebab and `user_service` under snake, so only the
// matching group spelling shares its directory - and only then can a method
// name collide there.
func TestGroupChecksHonourFileCase(t *testing.T) {
	src := map[string]string{
		"demo.craftgo": `package demo
type R { ok bool }
@group("user-service")
service Alpha { get Ping /alpha/ping { response R } }
service UserService { get Ping /user/ping { response R } }`,
	}
	root, files := projectFixture(t, src)
	_, diags := AnalyzeProject(files, Options{DesignRoot: root, FileCase: config.FileCaseKebab})
	if findCode(diags, CodeGroupMethodCollision) == nil {
		t.Fatalf("kebab: both land in user-service, so Ping must collide; got %v", codes(diags))
	}
	root, files = projectFixture(t, src)
	_, diags = AnalyzeProject(files, Options{DesignRoot: root, FileCase: config.FileCaseSnake})
	if findCode(diags, CodeGroupMethodCollision) != nil {
		t.Errorf("snake: UserService occupies user_service, so the directories differ")
	}
}

// TestGroupChecksDefaultToSnakeFileCase pins the default half of that rule:
// an unset FileCase must resolve the way the manifest resolves it, or the
// analyser would reason about a directory codegen never writes.
func TestGroupChecksDefaultToSnakeFileCase(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"demo.craftgo": `package demo
type R { ok bool }
@group("user_service")
service Alpha { get Ping /alpha/ping { response R } }
service UserService { get Ping /user/ping { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeGroupMethodCollision) == nil {
		t.Fatalf("unset FileCase must behave as snake, got %v", codes(diags))
	}
}

// TestGroupMethodlessBlockClaimsNothing pins that a block declaring no
// methods contributes nothing. Codegen derives its folder set from the
// merged method list, so an empty block writes no files and cannot drag a
// second DSL package into a directory.
func TestGroupMethodlessBlockClaimsNothing(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/svc.craftgo": `package alpha
@group("shared/v1")
service Alpha {}`,
		"b/svc.craftgo": `package beta
type R { ok bool }
@group("shared/v1")
service Beta { get B /b { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeGroupPackageStraddle) != nil {
		t.Errorf("method-less block emits nothing and must not straddle, got %v", codes(diags))
	}
}
