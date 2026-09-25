package golang

import (
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// checkTarget is the value a constraint check tests: a field, the primitive
// value of a scalar- or enum-typed field, or a scalar's own receiver.
type checkTarget struct {
	access   string                 // the Go expression holding the value
	pointer  bool                   // access is a pointer to the value
	nilGuard bool                   // nil is the value's valid absent state: optional or @nullable
	cat      semantic.FieldCategory // a field's category; 0 for a primitive value
	prim     string                 // the DSL primitive a flat value is checked as
	typ      *ast.TypeRef           // a field's type
	subject  string                 // the name its errors carry; "" for none
}

// fieldTarget is the check target of field rf held in access.
func fieldTarget(rf semantic.ResolvedField, access, subject string) checkTarget {
	return checkTarget{
		access:   access,
		pointer:  rf.GoPointer(),
		nilGuard: rf.NeedsNilGuard,
		cat:      rf.Category,
		prim:     rf.ResolvedPrim,
		typ:      rf.Field.Type,
		subject:  subject,
	}
}

// primTarget is the check target of a value of DSL primitive prim held in access.
func primTarget(access, prim, subject string) checkTarget {
	return checkTarget{access: access, prim: prim, subject: subject}
}

// val returns the value t tests: its access, dereferenced when a pointer.
func (t checkTarget) val() string {
	if t.pointer {
		return "*" + t.access
	}
	return t.access
}

// mayBeNil reports whether t's access can be nil: a pointer, or a value whose
// nil is its valid absent state.
func (t checkTarget) mayBeNil() bool {
	return t.pointer || t.nilGuard
}

// guarded returns cond behind t's `access != nil && ` guard when the access
// may be nil; a disjunction is parenthesised.
func (t checkTarget) guarded(cond string) string {
	if !t.mayBeNil() {
		return cond
	}
	if strings.Contains(cond, "||") {
		cond = "(" + cond + ")"
	}
	return t.access + " != nil && " + cond
}

// guardBlock returns body inside a [guardBlock] on t's access when it may be nil.
func (t checkTarget) guardBlock(body string) string {
	if !t.mayBeNil() {
		return body
	}
	return guardBlock(t.access, body)
}

// primIs reports whether t is a flat value of one of kinds.
func (t checkTarget) primIs(kinds ...prims.Kind) bool {
	sp, _ := prims.Lookup(t.prim)
	return slices.Contains(kinds, sp.Kind)
}
