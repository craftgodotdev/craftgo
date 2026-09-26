package golang

import (
	"slices"
	"testing"
)

// A struct has fill work when it holds a required list, map or bytes, a
// type-parameter value, or reaches such a struct, through recursion and
// across packages too; optional and @nullable lists, header fields and
// structs of scalars alone have none.
func TestFillSetWeighsEveryStruct(t *testing.T) {
	proj := analyzeFiles(t, map[string]string{
		"shared/s.craftgo": `package shared
type Tagged { tags string[] }
type Plain { n int }`,
		"app/a.craftgo": `package app
type A { b B?  x string[] }
type B { a A? }
type C { d D? }
type D { c C? }
type Opt { xs string[]?  ys string[] @nullable  h string[] @header("X-H") }
type Blob { b bytes }
type Box<T> { v T }
type Mixed { shared.Tagged  n int }
type Far { p shared.Plain  t shared.Tagged }
error Conflict E { items string[] }
error Conflict P { n int }`,
	})
	fills := newFillSet(proj)
	var got []string
	for _, name := range proj.PackageNames() {
		pkg := proj.Packages[name]
		for tn, td := range pkg.Types {
			if fills.types[td] {
				got = append(got, name+"."+tn)
			}
		}
		for en, ed := range pkg.Errors {
			if fills.errs[ed] {
				got = append(got, name+".error "+en)
			}
		}
	}
	slices.Sort(got)
	want := []string{"app.A", "app.B", "app.Blob", "app.Box", "app.Far", "app.Mixed", "app.error E", "shared.Tagged"}
	if !slices.Equal(got, want) {
		t.Errorf("fill work = %v, want %v", got, want)
	}
}

// A project is weighed once however many times a run asks, and another
// project anew.
func TestFillSetWeighedOncePerProject(t *testing.T) {
	a := analyzeProject(t, "package app\ntype T { xs string[] }")
	b := analyzeProject(t, "package app\ntype T { xs string[] }")
	first := fillSetOf(a)
	if again := fillSetOf(a); again != first {
		t.Error("a second ask weighed the project again")
	}
	if fillSetOf(b).proj != b || fillSetOf(a).proj != a {
		t.Error("another project must get its own fill set")
	}
}
