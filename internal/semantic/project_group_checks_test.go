package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/idents"
)

// Two services in one @group share its directory without a diagnostic.
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

// A @group naming an ungrouped service's directory shares it without a diagnostic.
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

// One service may split its blocks across @group folders.
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

// A @group directory fed by two DSL packages is reported at both; a directory is one Go package.
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

// Two services in one @group directory cannot both declare `Ping`; each method is its own file.
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

// One method name in two services in separate directories does not collide.
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

// An ungrouped service's directory name follows output.fileCase.
func TestGroupChecksHonourFileCase(t *testing.T) {
	src := map[string]string{
		"demo.craftgo": `package demo
type R { ok bool }
@group("user-service")
service Alpha { get Ping /alpha/ping { response R } }
service UserService { get Ping /user/ping { response R } }`,
	}
	root, files := projectFixture(t, src)
	_, diags := AnalyzeProject(files, Options{DesignRoot: root, FileCase: idents.FileCaseKebab})
	if findCode(diags, CodeGroupMethodCollision) == nil {
		t.Fatalf("kebab: both land in user-service, so Ping must collide; got %v", codes(diags))
	}
	root, files = projectFixture(t, src)
	_, diags = AnalyzeProject(files, Options{DesignRoot: root, FileCase: idents.FileCaseSnake})
	if findCode(diags, CodeGroupMethodCollision) != nil {
		t.Errorf("snake: UserService occupies user_service, so the directories differ")
	}
}

// An unset FileCase means snake case, the manifest default.
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

// A block without methods claims no directory, so it cannot straddle packages.
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

// Methods `X` and `NewX` in one output directory are reported at both, as
// X's logic constructor and NewX's logic type are both NewXService, and so
// are methods writing one file; a method named `Logger` is reported, as
// every logic type embeds log.Logger.
func TestMethodLogicGoNames(t *testing.T) {
	for label, c := range map[string]struct {
		src   string
		sites int
		want  string
	}{
		"one service": {`package shop
type R { ok bool }
service Orders {
    get Order /o { response R }
    post NewOrder /o { response R }
}`, 2, "NewOrderService"},
		"shared group": {`package shop
type R { ok bool }
@group("orders")
service Read { get Order /o { response R } }
@group("orders")
service Write { post NewOrder /o { response R } }`, 2, "NewOrderService"},
		"extend block": {`package shop
type R { ok bool }
service Orders { get Order /o { response R } }
extend service Orders { post NewOrder /o { response R } }`, 2, "NewOrderService"},
		"logger": {`package shop
type R { ok bool }
service Admin { get Logger /loggers { response R } }`, 1, "log.Logger"},
		"one file": {`package shop
type R { ok bool }
service Links {
    get GetURL /a { response R }
    get GetUrl /b { response R }
}`, 2, "get_url.go"},
		"one file across a group": {`package shop
type R { ok bool }
@group("links")
service A { get GetURL /a { response R } }
@group("links")
service B { get GetUrl /b { response R } }`, 2, "get_url.go"},
	} {
		t.Run(label, func(t *testing.T) {
			root, files := projectFixture(t, map[string]string{"shop.craftgo": c.src})
			_, diags := AnalyzeProject(files, Options{DesignRoot: root})
			hits := 0
			for _, d := range diags {
				if d.Code == CodeMethodNameClash {
					hits++
					if !strings.Contains(d.Msg, c.want) {
						t.Errorf("message should name %s: %q", c.want, d.Msg)
					}
				}
			}
			if hits != c.sites {
				t.Errorf("want %d %s, got %v", c.sites, CodeMethodNameClash, diags)
			}
		})
	}
	// Separate directories hold separate Go packages.
	root, files := projectFixture(t, map[string]string{"shop.craftgo": `package shop
type R { ok bool }
service Read { get Order /o { response R } }
service Write { post NewOrder /o { response R } }`})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeMethodNameClash); d != nil {
		t.Errorf("separate directories must not clash: %s", d.Msg)
	}
}

// A method whose file the go command would build only for tests or one
// system is reported; under a file case without `_` it is not.
func TestMethodFileNameGoTreatsSpecially(t *testing.T) {
	const src = `package shop
type R { ok bool }
service Lab {
	post RunTest /run { response R }
	get ListWindows /windows { response R }
	get GetItem /item { response R }
}`
	root, files := projectFixture(t, map[string]string{"shop.craftgo": src})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	var got []string
	for _, d := range diags {
		if d.Code == CodeMethodFileName {
			got = append(got, d.Msg)
		}
	}
	if len(got) != 2 || !strings.Contains(got[0]+got[1], "run_test.go") || !strings.Contains(got[0]+got[1], "list_windows.go") {
		t.Errorf("diagnostics = %v, want run_test.go and list_windows.go", got)
	}
	root, files = projectFixture(t, map[string]string{"shop.craftgo": src})
	_, diags = AnalyzeProject(files, Options{DesignRoot: root, FileCase: "kebab"})
	if d := findCode(diags, CodeMethodFileName); d != nil {
		t.Errorf("kebab file case: unexpected %s", d.Msg)
	}
}
